package handler

import (
	"errors"
	"net/http"
	"os"
	"strings"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func respondError(c *gin.Context, status int, err error) {
	c.JSON(status, gin.H{"error": gin.H{
		"message": err.Error(),
		"type":    "invalid_request_error",
		"param":   nil,
		"code":    http.StatusText(status),
	}})
}

// resolveAccount 从请求 Authorization header 解析账号
// 替代旧的 secretFromAuthorization + accessTokenFromRefreshToken
func resolveAccount(c *gin.Context, pool *accounts.Pool, needsPaid bool) (*accounts.Account, error) {
	authHeader := c.GetHeader("Authorization")

	// 提取 Bearer token
	payload := strings.TrimSpace(authHeader)
	if len(payload) >= 7 && strings.EqualFold(payload[:7], "Bearer ") {
		payload = strings.TrimSpace(payload[7:])
	}
	parts := strings.SplitN(payload, ",", 2)
	token := strings.TrimSpace(parts[0])
	teamAccountID := ""
	if len(parts) > 1 {
		teamAccountID = strings.TrimSpace(parts[1])
	}

	expected := os.Getenv("Authorization")

	// 无 token 或匹配全局密钥 → 从池里取默认账号
	if token == "" || (expected != "" && token == expected) {
		acct, err := pool.Acquire(accounts.TypePUID)
		if err != nil {
			return nil, err
		}
		return acct, nil
	}

	// access_token (JWT) → 创建临时账号
	if strings.HasPrefix(token, "eyJ") {
		acct := accounts.NewAccount(token, accounts.TypePUID, token)
		acct.TeamUserID = teamAccountID
		if err := acct.InitClient(); err != nil {
			return nil, err
		}
		acct.Status = accounts.StatusActive
		return acct, nil
	}

	// UUID → noauth 账号
	if _, err := uuid.Parse(token); err == nil {
		acct := accounts.NewAccount(token, accounts.TypeNoAuth, token)
		if err := acct.InitClient(); err != nil {
			return nil, err
		}
		acct.Status = accounts.StatusActive
		return acct, nil
	}

	// refresh_token（有 team id 或长 token）→ 换 access_token
	if teamAccountID != "" || len(token) > 64 {
		client := bogdanfinn.NewStdClient()
		result, status, err := chatgpt.GETTokenForRefreshToken(client, token, "")
		if err != nil {
			return nil, err
		}
		if status == 0 {
			// fall through
		}
		if data, ok := result.(map[string]interface{}); ok {
			if accessToken, ok := data["access_token"].(string); ok && accessToken != "" {
				acct := accounts.NewAccount(accessToken, accounts.TypePUID, accessToken)
				acct.TeamUserID = teamAccountID
				acct.RefreshToken = token
				if err := acct.InitClient(); err != nil {
					return nil, err
				}
				acct.Status = accounts.StatusActive
				return acct, nil
			}
		}
		return nil, errors.New("refresh token response did not include access_token")
	}

	// 兜底：从池里取
	acct, err := pool.Acquire(accounts.TypePUID)
	if err != nil {
		return nil, err
	}
	return acct, nil
}
