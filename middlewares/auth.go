package middlewares

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// Authorization 中间件验证请求的 Authorization header
func Authorization(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	expected := os.Getenv("Authorization")

	// 未配置全局密钥时，放行所有请求（向后兼容）
	if expected == "" {
		c.Next()
		return
	}

	// 没有 Authorization header
	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
			"error": gin.H{
				"message": "Authorization header is required",
				"type":    "invalid_request_error",
			},
		})
		return
	}

	// 去掉 "Bearer " 前缀
	payload := strings.TrimSpace(authHeader)
	if len(payload) >= 7 && strings.EqualFold(payload[:7], "Bearer ") {
		payload = strings.TrimSpace(payload[7:])
	}

	// 解析 token 和可选的 team_id（格式: token,team_id）
	parts := strings.SplitN(payload, ",", 2)
	token := strings.TrimSpace(parts[0])
	teamAccountID := ""
	if len(parts) > 1 {
		teamAccountID = strings.TrimSpace(parts[1])
	}

	// 匹配全局密钥
	if token == expected {
		c.Set("auth_token", "")
		if teamAccountID != "" {
			c.Set("team_account_id", teamAccountID)
		}
		c.Next()
		return
	}

	// access_token（以 eyJ 开头）或 refresh_token/其他长 token
	// 放行让 handler 进一步处理
	if strings.HasPrefix(token, "eyJ") || len(token) > 64 {
		c.Set("auth_token", token)
		if teamAccountID != "" {
			c.Set("team_account_id", teamAccountID)
		}
		c.Next()
		return
	}

	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
		"error": gin.H{
			"message": "Invalid authorization token",
			"type":    "invalid_request_error",
		},
	})
}
