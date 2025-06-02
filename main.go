package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

var (
	target string // 目标域名
	port   int    // 代理端口
	// httpProxy = "http://127.0.0.1:10809" // 本地代理地址和端口
)

func main() {
	// 从命令行参数获取配置文件路径
	flag.StringVar(&target, "domain", "https://api.openai.com", "The target domain to proxy.")
	flag.IntVar(&port, "port", 9000, "The proxy port.")
	flag.Parse()

	// 打印配置信息
	log.Println("Target domain: ", target)
	log.Println("Proxy port: ", port)

	http.HandleFunc("/", handleRequest)
	http.ListenAndServe(":"+strconv.Itoa(port), nil)
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	// 过滤无效URL
	_, err := url.Parse(r.URL.String())
	if err != nil {
		log.Println("Error parsing URL: ", err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// 去掉环境前缀（针对腾讯云，如果包含的话，目前我只用到了test和release）
	newPath := strings.Replace(r.URL.Path, "/release", "", 1)
	newPath = strings.Replace(newPath, "/test", "", 1)

	// 拼接目标URL（带上查询字符串，如果有的话）
	// 如果请求中包含 X-Target-Host 头，则使用该头作为目标域名
	// 优先级 header > args > default
	var targetURL string
	if r.Header.Get("X-Target-Host") != "" {
		targetURL = "https://" + r.Header.Get("X-Target-Host") + newPath
	} else {
		targetURL = target + newPath
	}
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// 本地打印代理请求完整URL用于调试
	if os.Getenv("ENV") == "local" {
		fmt.Printf("Proxying request to: %s\n", targetURL)
	}

	// 创建代理HTTP请求
	proxyReq, err := http.NewRequest(r.Method, targetURL, r.Body)
	if err != nil {
		log.Println("Error creating proxy request: ", err.Error())
		http.Error(w, "Error creating proxy request", http.StatusInternalServerError)
		return
	}

	// 移除可能暴露客户端IP的请求头
	r.Header.Del("X-Forwarded-For")
	r.Header.Del("X-Real-IP")
	r.Header.Del("Client-IP")

	// 将原始请求头复制到新请求中
	for headerKey, headerValues := range r.Header {
		for _, headerValue := range headerValues {
			proxyReq.Header.Add(headerKey, headerValue)
		}
	}

	// 适配 Vertex 本地认证，需要先本地安装 gcloud 并初始化
	if strings.Contains(targetURL, "aiplatform.googleapis.com") {
		// 如果请求头中没有 Authorization 头，则通过 gcloud 获取 vertex 认证令牌
		if proxyReq.Header.Get("Authorization") == "" {
			_token, err := getVertexAuthToken()
			if err != nil {
				log.Println("Error getting vertex auth token: ", err.Error())
				http.Error(w, "Error getting vertex auth token", http.StatusInternalServerError)
				return
			}
			// 设置新的 Authorization 头
			proxyReq.Header.Set("Authorization", "Bearer "+_token)
		}
	}

	// 默认超时时间设置为300s（应对长上下文）
	client := &http.Client{
		// Timeout: 300 * time.Second,  // 代理不干涉超时逻辑，由客户端自行设置
	}

	// 支持本地测试通过代理请求
	/*if os.Getenv("ENV") == "local" {
		proxyURL, _ := url.Parse(httpProxy) // 本地HTTP代理配置
		client.Transport = &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}*/

	// 发起代理请求
	resp, err := client.Do(proxyReq)
	if err != nil {
		log.Println("Error sending proxy request: ", err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 将响应头复制到代理响应头中
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 将响应状态码设置为原始响应状态码
	w.WriteHeader(resp.StatusCode)

	// 将响应实体写入到响应流中（支持流式响应）
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			// 处理已读取的数据，即使遇到错误也要处理这部分数据
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				log.Println("Error writing response:", writeErr.Error())
				return
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}

		// 处理各种错误情况
		if err == io.EOF {
			// 读取完毕，正常结束
			break
		} else if err != nil {
			// 其他错误
			log.Println("Error reading response body:", err.Error())
			return
		}

		// 当 n == 0 且没有错误时，继续读取
	}
}
