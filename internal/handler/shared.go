package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	chatgpt_types "aurora/internal/types/chatgpt"
	officialtypes "aurora/internal/types/official"
	"aurora/util"
		"aurora/internal/config"

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
func resolveAccount(c *gin.Context, pool *accounts.Pool, cfg *config.Config, needsPaid bool) (*accounts.Account, error) {
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

	expected := cfg.Authorization

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
		result, _, err := chatgpt.GETTokenForRefreshToken(client, token, cfg.ProxyURL)
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
func conversationClientOrder(client **bogdanfinn.TlsClient, account *accounts.Account, translatedRequest chatgpt_types.ChatGPTRequest, proxyUrl string, stream bool, state *chatgpt.ChatClientState) (*http.Response, *websocket.Conn, *chatgpt.TurnStile, int, error) {
	if state != nil {
		state.ApplyToRequest(&translatedRequest)
	}
	turnTraceID := uuid.NewString()

	// 对齐浏览器: sentinel 前设置 BasicCookies
	(*client).SetCookies("https://chatgpt.com", chatgpt.BasicCookies)

	turnStile, status, err := chatgpt.InitSentinelWithState(*client, account, proxyUrl, 0, state)
	if err != nil {
		// 401 时重试一次（cookies 可能过期）
		if status == http.StatusUnauthorized {
			(*client).SetCookies("https://chatgpt.com", chatgpt.BasicCookies)
			turnStile, status, err = chatgpt.InitSentinelWithState(*client, account, proxyUrl, 0, state)
			if err != nil {
				return nil, nil, nil, status, err
			}
		} else {
			return nil, nil, nil, status, err
		}
	}

	chatgpt.POSTConversationInit(*client, account, state)

	var wsConn *websocket.Conn
	if stream && account.Type == accounts.TypePUID {
		wsConn, err = chatgpt.DialChatWebsocketWithStateAndProxy(*client, account, state, proxyUrl)
		if err != nil {
			return nil, nil, nil, http.StatusInternalServerError, err
		}
	}

	conduitToken, err := chatgpt.PrepareConversationConduitFullWithSentinel(*client, translatedRequest, account, proxyUrl, turnTraceID, state, turnStile)
	if err != nil {
		if wsConn != nil {
			wsConn.Close()
		}
		return nil, nil, nil, http.StatusInternalServerError, err
	}

	response, err := chatgpt.POSTconversationPreparedWithState(*client, translatedRequest, account, turnStile, proxyUrl, conduitToken, turnTraceID, state)
	if err != nil {
		if wsConn != nil {
			wsConn.Close()
		}
		return nil, nil, nil, http.StatusInternalServerError, err
	}
	return response, wsConn, turnStile, http.StatusOK, nil
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

// original_requestHasFiles 检查请求消息中是否包含文件引用
func original_requestHasFiles(request officialtypes.APIRequest) bool {
	for _, message := range request.Messages {
		if len(message.Files()) > 0 {
			return true
		}
	}
	return false
}

// toolCallingEnabled 根据 Config + Tools 列表判定是否启用工具调用模拟。
func toolCallingEnabled(tools []officialtypes.Tool, cfg *config.Config) bool {
	if cfg != nil && !cfg.ToolCallingEnabled {
		return false
	}
	return len(tools) > 0
}

// countMessagesTokens 统计消息的 token 数
func countMessagesTokens(messages []officialtypes.APIMessage) int {
	total := 0
	for _, message := range messages {
		total += util.CountToken(message.Text())
	}
	return total
}

// writeChatCompletionStreamDone 写入流式结束标记
func writeChatCompletionStreamDone(c *gin.Context, stopSent bool, model string, conversationID string) {
	if !stopSent {
		finalLine := officialtypes.StopChunkWithConversation("stop", model, conversationID)
		c.Writer.WriteString("data: " + finalLine.String() + "\n\n")
		c.Writer.Flush()
	}
	c.Writer.WriteString("data: [DONE]\n\n")
	c.Writer.Flush()
}

// looksLikeSandboxRefusal 检测模型是否声称自己处于隔离环境/无法访问工具。
func looksLikeSandboxRefusal(text string) bool {
	if text == "" {
		return false
	}
	t := strings.ToLower(text)
	markers := []string{
		"/mnt/data", "/workspace", "/home/oai", "filesystem isolado", "ambiente isolado",
		"root linux", "linux/container", "container atual", "não tem acesso ao diret",
		"nao tem acesso ao diret", "não está montado", "nao esta montado",
		"não foi montado", "nao foi montado", "não existe neste ambiente",
		"nao existe neste ambiente", "não pode continuar neste ambiente",
		"não é possível ler", "nao e possivel ler",
		"não foi possível abrir", "nao foi possivel abrir",
		"não foi possível executar", "nao foi possivel executar",
		"falha na interface de execução", "falha no parsing",
		"inferência baseada na estrutura", "inferencia baseada na estrutura",
		"baseada apenas na estrutura",
	}
	for _, m := range markers {
		if strings.Contains(t, m) {
			return true
		}
	}
	return false
}

// appendToolDebugLog 把每次工具解析的输入文本和解析结果写入日志文件
func appendToolDebugLog(path string, attempt int, text string, calls []officialtypes.ToolCall) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	callsJSON, _ := json.Marshal(calls)
	fmt.Fprintf(f, "\n=== attempt %d ===\ntext: %s\ncalls: %s\n", attempt, text, string(callsJSON))
}
