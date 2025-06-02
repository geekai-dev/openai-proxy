package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// 获取Vertex认证令牌
func getVertexAuthToken() (string, error) {
	cmd := exec.Command("gcloud", "auth", "application-default", "print-access-token")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %v", err)
	}
	return strings.TrimSpace(string(output)), nil
}
