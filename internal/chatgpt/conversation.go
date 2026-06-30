package chatgpt

import (
	"aurora/httpclient"
	"aurora/internal/tokens"
	chatgpt_types "aurora/typings/chatgpt"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func getConduitToken(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, chatToken *TurnStile, turnTraceID string) (string, error) {
	return getConduitTokenWithState(client, message, secret, chatToken, turnTraceID, nil, PrepareStateNone, "")
}

func getConduitTokenWithState(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, chatToken *TurnStile, turnTraceID string, state *ChatClientState, prepareState PrepareState, previousConduitToken string) (string, error) {
	message = requestWithClientState(message, state)
	apiUrl, targetPath := conversationURL(secret, "/f/conversation/prepare")
	parentMessageID := message.ParentMessageID
	if parentMessageID == "" {
		parentMessageID = "client-created-root"
	}
	payload := map[string]interface{}{
		"action":                 "next",
		"parent_message_id":      parentMessageID,
		"model":                  conversationPrepareModel(message.Model),
		"client_prepare_state":   string(prepareState),
		"timezone_offset_min":    message.TimezoneOffsetMin,
		"timezone":               "America/Los_Angeles",
		"conversation_mode":      map[string]string{"kind": "primary_assistant"},
		"system_hints":           []string{},
		"supports_buffering":     true,
		"supported_encodings":    []string{"v1"},
		"client_contextual_info": conversationPrepareClientContext(message),
	}
	// partial_query 只在 sent / success 阶段携带,none 阶段用户还没开始打字
	if prepareState == PrepareStateSent || prepareState == PrepareStateSuccess {
		payload["partial_query"] = map[string]interface{}{
			"id":      uuid.NewString(),
			"author":  map[string]string{"role": "user"},
			"content": map[string]interface{}{"content_type": "text", "parts": []string{conversationPartialText(message)}},
		}
	}
	if message.ConversationID != "" {
		payload["conversation_id"] = message.ConversationID
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	// 关键:conduit token 在每一步都不同,严格按"上一步响应拿到的 token"作为下一步的请求头
	header := conversationHeadersWithState(secret, chatToken, "*/*", targetPath, previousConduitToken, turnTraceID, state)
	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("conversation prepare failed: %s", string(body))
	}
	var result struct {
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	return result.ConduitToken, nil
}

func PrepareConversationConduit(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, proxy string, turnTraceID string) (string, error) {
	return PrepareConversationConduitWithState(client, message, secret, proxy, turnTraceID, nil)
}

func PrepareConversationConduitWithState(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, proxy string, turnTraceID string, state *ChatClientState) (string, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	return getConduitTokenWithState(client, message, secret, nil, turnTraceID, state, PrepareStateNone, "")
}

// PrepareConversationConduitFull 走完整的 none -> sent -> success 三态,
// 每次 prepare 都用上一步返回的 conduit_token 作下一步请求头。
// success 状态返回的 token 用于 POST /f/conversation,这是真实浏览器
// 进入"主路由决策"前的最后一步 —— 缺这一步会让后端降级到 mini 池。
func PrepareConversationConduitFull(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, proxy string, turnTraceID string, state *ChatClientState) (string, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	// 在三态 prepare 之前先确保 CookieJar 有 CF 注入的 cf_clearance / __cf_bm
	// 等关键 cookie,否则直接被 CF 拦截,根本到不了 OpenAI 后端。
	ensureBootstrapped(client, secret)
	// Step 1: none —— 用户还没开始打字,partial_query 不带
	token1, err := getConduitTokenWithState(client, message, secret, nil, turnTraceID, state, PrepareStateNone, "")
	if err != nil {
		return "", fmt.Errorf("prepare(none) failed: %w", err)
	}
	// Step 2: sent —— 打字中,带 partial_query
	token2, err := getConduitTokenWithState(client, message, secret, nil, turnTraceID, state, PrepareStateSent, token1)
	if err != nil {
		return "", fmt.Errorf("prepare(sent) failed: %w", err)
	}
	// Step 3: success —— 用户按回车,后端在这一步给出模型路由决策
	token3, err := getConduitTokenWithState(client, message, secret, nil, turnTraceID, state, PrepareStateSuccess, token2)
	if err != nil {
		return "", fmt.Errorf("prepare(success) failed: %w", err)
	}
	return token3, nil
}

// PrepareConversationConduitFullWithSentinel 与 PrepareConversationConduitFull 相同,
// 但在三态 prepare 的每一步都携带已获取的 sentinel token 头。
// 对齐浏览器行为:sentinel 流程(prepare→ping→finalize)在 prepare 流程之前完成,
// conduit token 在 sentinel 上下文中签发,服务器据此判定客户端可信度与模型路由。
//
// 浏览器真实顺序:
//  1. /sentinel/req          → oai-sc cookie (会话级)
//  2. /chat-requirements/prepare → challenge
//  3. /sentinel/ping         → 风控汇报
//  4. /chat-requirements/finalize → chat-requirements token
//  5. /f/conversation/prepare (none→sent→success) → conduit tokens (带 sentinel 头)
//  6. /f/conversation        → 主请求
func PrepareConversationConduitFullWithSentinel(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, proxy string, turnTraceID string, state *ChatClientState, turnStile *TurnStile) (string, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	ensureBootstrapped(client, secret)
	// Step 1: none
	token1, err := getConduitTokenWithState(client, message, secret, turnStile, turnTraceID, state, PrepareStateNone, "")
	if err != nil {
		return "", fmt.Errorf("prepare(none) failed: %w", err)
	}
	// Step 2: sent
	token2, err := getConduitTokenWithState(client, message, secret, turnStile, turnTraceID, state, PrepareStateSent, token1)
	if err != nil {
		return "", fmt.Errorf("prepare(sent) failed: %w", err)
	}
	// Step 3: success
	token3, err := getConduitTokenWithState(client, message, secret, turnStile, turnTraceID, state, PrepareStateSuccess, token2)
	if err != nil {
		return "", fmt.Errorf("prepare(success) failed: %w", err)
	}
	return token3, nil
}

func conversationPrepareModel(model string) string {
	if model == "" {
		return "auto"
	}
	return model
}

func conversationPartialText(message chatgpt_types.ChatGPTRequest) string {
	for i := len(message.Messages) - 1; i >= 0; i-- {
		msg := message.Messages[i]
		if msg.Author.Role != "user" {
			continue
		}
		for _, part := range msg.Content.Parts {
			if text, ok := part.(string); ok && strings.TrimSpace(text) != "" {
				return runeSlice(text, 5)
			}
		}
	}
	return "h"
}

func runeSlice(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) > maxRunes {
		r = r[:maxRunes]
	}
	return string(r)
}

func conversationPrepareClientContext(message chatgpt_types.ChatGPTRequest) map[string]interface{} {
	info := map[string]interface{}{"app_name": "chatgpt.com"}
	for key, value := range message.ClientContextualInfo {
		info[key] = value
	}
	info["app_name"] = "chatgpt.com"
	return info
}

func POSTconversation(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, chat_token *TurnStile, proxy string) (*http.Response, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	turnTraceID := uuid.NewString()
	conduitToken, err := getConduitToken(client, message, secret, nil, turnTraceID)
	if err != nil {
		return nil, err
	}
	return POSTconversationPrepared(client, message, secret, chat_token, proxy, conduitToken, turnTraceID)
}

func POSTconversationPrepared(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, chat_token *TurnStile, proxy string, conduitToken string, turnTraceID string) (*http.Response, error) {
	return POSTconversationPreparedWithState(client, message, secret, chat_token, proxy, conduitToken, turnTraceID, nil)
}

func POSTconversationPreparedWithState(client httpclient.AuroraHttpClient, message chatgpt_types.ChatGPTRequest, secret *tokens.Secret, chat_token *TurnStile, proxy string, conduitToken string, turnTraceID string, state *ChatClientState) (*http.Response, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	message = requestWithClientState(message, state)
	apiUrl, targetPath := conversationURL(secret, "/f/conversation")
	if API_REVERSE_PROXY != "" {
		apiUrl = API_REVERSE_PROXY
	}
	// JSONify the body and add it to the request
	body_json, err := json.Marshal(message)
	if err != nil {
		return &http.Response{}, err
	}
	header := conversationHeadersWithState(secret, chat_token, "text/event-stream", targetPath, conduitToken, turnTraceID, state)
	if secret.IsFree {
		client.SetCookies("https://chatgpt.com", []*http.Cookie{
			{Name: "oai-device-id", Value: secret.Token, Path: "/", Domain: "chatgpt.com"},
		})
	}

	response, err := client.Request(http.MethodPost, apiUrl, header, nil, bytes.NewBuffer(body_json))
	if err != nil {
		return nil, err
	}
	return response, nil
}

func Handle_request_error(c *gin.Context, response *http.Response) bool {
	if response.StatusCode != 200 {
		// Try read response body as JSON
		var error_response map[string]interface{}
		err := json.NewDecoder(response.Body).Decode(&error_response)
		if err != nil {
			// Read response body
			body, _ := io.ReadAll(response.Body)
			c.JSON(response.StatusCode, gin.H{"error": gin.H{
				"message": "Unknown error",
				"type":    "internal_server_error",
				"param":   nil,
				"code":    "500",
				"details": string(body),
			}})
			return true
		}
		c.JSON(response.StatusCode, gin.H{"error": gin.H{
			"message": error_response["detail"],
			"type":    response.Status,
			"param":   nil,
			"code":    "error",
		}})
		return true
	}
	return false
}
