package handler

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	"aurora/internal/tokens"
	chatgpt_types "aurora/internal/types/chatgpt"
	officialtypes "aurora/internal/types/official"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/websocket"
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
		result, _, err := chatgpt.GETTokenForRefreshToken(client, token, "")
		if err != nil {
			return nil, err
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

// conversationClientOrder 执行标准的 conversation 流程：
// sentinel → init → ws → prepare → POST
//
// 对齐 initialize/handlers.go:postConversationGptClientOrder
// 使用 tokens.Secret 桥接（后续统一改为 *Account）
func conversationClientOrder(client **bogdanfinn.TlsClient, secret *tokens.Secret, translatedRequest chatgpt_types.ChatGPTRequest, proxyUrl string, stream bool, state *chatgpt.ChatClientState) (*http.Response, *websocket.Conn, *chatgpt.TurnStile, int, error) {
	if state != nil {
		state.ApplyToRequest(&translatedRequest)
	}
	turnTraceID := uuid.NewString()

	turnStile, status, err := chatgpt.InitSentinelWithState(*client, secret, proxyUrl, 0, state)
	if err != nil {
		return nil, nil, nil, status, err
	}

	chatgpt.POSTConversationInit(*client, secret, state)

	var wsConn *websocket.Conn
	if stream && !secret.IsFree {
		wsConn, err = chatgpt.DialChatWebsocketWithStateAndProxy(*client, secret, state, proxyUrl)
		if err != nil {
			return nil, nil, nil, http.StatusInternalServerError, err
		}
	}

	conduitToken, err := chatgpt.PrepareConversationConduitFullWithSentinel(*client, translatedRequest, secret, proxyUrl, turnTraceID, state, turnStile)
	if err != nil {
		if wsConn != nil {
			wsConn.Close()
		}
		return nil, nil, nil, http.StatusInternalServerError, err
	}

	response, err := chatgpt.POSTconversationPreparedWithState(*client, translatedRequest, secret, turnStile, proxyUrl, conduitToken, turnTraceID, state)
	if err != nil {
		if wsConn != nil {
			wsConn.Close()
		}
		return nil, nil, nil, http.StatusInternalServerError, err
	}
	return response, wsConn, turnStile, http.StatusOK, nil
}

// createTempSecret 从 account 创建临时 tokens.Secret（桥接用）
func createTempSecret(account *accounts.Account) *tokens.Secret {
	return &tokens.Secret{
		Token:      account.Token,
		IsFree:     account.Type != accounts.TypePUID,
		PUID:       account.PUID,
		TeamUserID: account.TeamUserID,
	}
}

// setupClientWithProxy 创建带代理的 std client
func setupClientWithProxy(proxyUrl string) *bogdanfinn.TlsClient {
	client := bogdanfinn.NewStdClient()
	if proxyUrl != "" {
		_ = client.SetProxy(proxyUrl)
	}
	return client
}

// websocketProxyFunc 为 WebSocket 连接配置代理（从原 request.go 复制）
func websocketProxyFunc(proxy string) (func(*fhttp.Request) (*url.URL, error), error) {
	if proxy == "" {
		return fhttp.ProxyFromEnvironment, nil
	}
	proxyURL, err := url.Parse(proxy)
	if err != nil {
		return nil, err
	}
	return fhttp.ProxyURL(proxyURL), nil
}

// maxContinueCount 返回 max_tokens 触发时自动 continue 的最大轮数。
func maxContinueCount() int {
	v := os.Getenv("MAX_CONTINUE_COUNT")
	if v == "" {
		return 3
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 3
	}
	return n
}

// original_requestHasFiles 检查请求消息中是否包含文件引用
func original_requestHasFiles(request officialtypes.APIRequest) bool {
	for _, message := range request.Messages {
		if len(message.Files()) > 0 {
			return true
		}
	}
	return false
}
