package chatgpt

import (
	"aurora/conversion/response/chatgpt"
	"aurora/httpclient"
	"aurora/internal/accounts"
	"aurora/internal/headerbuilder"
	"aurora/typings"
	chatgpt_types "aurora/typings/chatgpt"
	official_types "aurora/typings/official"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/bogdanfinn/websocket"
)

type ContinueInfo struct {
	ConversationID string `json:"conversation_id"`
	ParentID       string `json:"parent_id"`
}

type HandlerResult struct {
	Text              string
	ThinkingText      string
	ConversationID    string
	ParentMessageID   string
	Sentinel          []map[string]interface{}
	ArtifactSignals   []ArtifactSignal
	SandboxArtifacts  []SandboxArtifact
	PDFArtifacts      []PDFArtifact
	GeneratedImageIDs []string
	StopSent          bool
	Continue          *ContinueInfo
	// ToolCalls 在 Tools 模式启用时携带从 <tool_call>{...}</tool_call> 协议
	// 抽取出的工具调用列表。当 len(ToolCalls) > 0 时,FinishReason 为 "tool_calls"。
	ToolCalls []official_types.ToolCall
}

type conversationPatchState struct {
	response chatgpt_types.ChatGPTResponse
	channel  string
}

type conversationStreamEvent struct {
	response       chatgpt_types.ChatGPTResponse
	chunk          *official_types.ChatCompletionChunk
	text           string
	role           string
	conversationID string
	messageID      string
	channel        string
	finishReason   string
	isStop         bool
}

func sseDataPayloads(line string) []string {
	var payloads []string
	for _, part := range strings.Split(strings.TrimRight(line, "\r\n"), "\n") {
		part = strings.TrimSpace(part)
		if !strings.HasPrefix(part, "data:") {
			continue
		}
		payloads = append(payloads, splitSSEDataPayloads(strings.TrimSpace(strings.TrimPrefix(part, "data:")))...)
	}
	return payloads
}

func splitSSEDataPayloads(payload string) []string {
	var payloads []string
	for {
		payload = strings.TrimSpace(payload)
		if payload == "" {
			return payloads
		}
		if strings.HasPrefix(payload, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
			continue
		}
		if strings.HasPrefix(payload, "[DONE]") {
			payloads = append(payloads, "[DONE]")
			payload = payload[len("[DONE]"):]
			continue
		}

		reader := strings.NewReader(payload)
		decoder := json.NewDecoder(reader)
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err == nil {
			payloads = append(payloads, string(raw))
			payload = payload[decoder.InputOffset():]
			continue
		}

		next := strings.Index(payload, "data:")
		if next < 0 {
			return payloads
		}
		if first := strings.TrimSpace(payload[:next]); first != "" {
			payloads = append(payloads, first)
		}
		payload = payload[next:]
	}
}

func sseEventName(line string) (string, bool) {
	for _, part := range strings.Split(strings.TrimRight(line, "\r\n"), "\n") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "event:") {
			return strings.TrimSpace(strings.TrimPrefix(part, "event:")), true
		}
	}
	return "", false
}

func streamHandoffTopicFromPayload(payload string, currentEvent string) (string, bool) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return "", false
	}
	eventType, _ := raw["type"].(string)
	if eventType == "stream_handoff" {
		if topicID := streamHandoffTopicFromEvent(raw); topicID != "" {
			return topicID, true
		}
		return "", true
	}
	if eventType == "server_ste_metadata" || currentEvent == "server_ste_metadata" {
		if topicID := streamHandoffTopicFromMetadata(raw); topicID != "" {
			return topicID, true
		}
		return "", eventType == "server_ste_metadata"
	}
	if eventType == "resume_conversation_token" {
		return "", true
	}
	return "", false
}

func streamHandoffTopicFromEvent(raw map[string]interface{}) string {
	options, ok := raw["options"].([]interface{})
	if !ok {
		return ""
	}
	for _, optionValue := range options {
		option, ok := optionValue.(map[string]interface{})
		if !ok {
			continue
		}
		optionType, _ := option["type"].(string)
		if optionType != "subscribe_ws_topic" {
			continue
		}
		topicID, _ := option["topic_id"].(string)
		return topicID
	}
	return ""
}

func streamHandoffTopicFromMetadata(raw map[string]interface{}) string {
	if turnExchangeID, _ := raw["turn_exchange_id"].(string); turnExchangeID != "" {
		return "conversation-turn-" + turnExchangeID
	}
	metadata, ok := raw["metadata"].(map[string]interface{})
	if !ok {
		return ""
	}
	if turnExchangeID, _ := metadata["turn_exchange_id"].(string); turnExchangeID != "" {
		return "conversation-turn-" + turnExchangeID
	}
	return ""
}

func parseConversationEvent(line string, state *conversationPatchState, model string) (conversationStreamEvent, bool) {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return conversationStreamEvent{}, false
	}

	if chunk, ok := chatCompletionChunkFromRaw(raw, model); ok {
		event := conversationStreamEvent{
			chunk:          &chunk,
			text:           firstChunkContent(chunk),
			role:           firstChunkRole(chunk),
			conversationID: chunk.ConversationID,
			channel:        channelFromValue(raw),
			finishReason:   firstChunkFinishReason(chunk),
		}
		event.isStop = event.finishReason != ""
		return event, true
	}

	var direct chatgpt_types.ChatGPTResponse
	if err := json.Unmarshal([]byte(line), &direct); err == nil && isUsableConversationResponse(direct) {
		channel := channelFromValue(raw)
		state.channel = firstNonEmpty(channel, state.channel)
		return conversationStreamEvent{response: direct, messageID: direct.Message.ID, channel: state.channel}, true
	}

	if response, ok := responseFromValue(raw["v"]); ok {
		state.response = response
		if channel := channelFromValue(raw["v"]); channel != "" {
			state.channel = channel
		}
		return conversationStreamEvent{response: state.response, messageID: state.response.Message.ID, channel: state.channel}, true
	}
	if text, ok := raw["v"].(string); ok && raw["p"] == nil && raw["o"] == nil {
		ensureConversationPatchDefaults(state)
		current, _ := state.response.Message.Content.Parts[0].(string)
		state.response.Message.Content.Parts[0] = current + text
		return conversationStreamEvent{response: state.response, messageID: state.response.Message.ID, channel: state.channel}, true
	}

	if patchPath, ok := raw["p"].(string); ok {
		patchOperation, _ := raw["o"].(string)
		// 处理批量 patch: {"p": "", "o": "patch", "v": [{"p": "...", "o": "append", "v": "..."}, ...]}
		// 新版 ChatGPT Web 会在最后把多个 patch 打包成一条 SSE 发出。
		if patchPath == "" && patchOperation == "patch" {
			if batch, ok := raw["v"].([]interface{}); ok {
				applied := false
				for _, item := range batch {
					op, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					subPath, _ := op["p"].(string)
					subOp, _ := op["o"].(string)
					if applyConversationPatch(state, subPath, subOp, op["v"]) {
						applied = true
					}
				}
				if applied {
					return conversationStreamEvent{response: state.response, messageID: state.response.Message.ID, channel: state.channel}, true
				}
			}
		}
		if applyConversationPatch(state, patchPath, patchOperation, raw["v"]) {
			return conversationStreamEvent{response: state.response, messageID: state.response.Message.ID, channel: state.channel}, true
		}
	}

	return conversationStreamEvent{}, false
}

func chatCompletionChunkFromRaw(raw map[string]interface{}, model string) (official_types.ChatCompletionChunk, bool) {
	choices, ok := raw["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return official_types.ChatCompletionChunk{}, false
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return official_types.ChatCompletionChunk{}, false
	}
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return official_types.ChatCompletionChunk{}, false
	}

	text, _ := delta["content"].(string)
	chunk := official_types.NewChatCompletionChunk(text, model)
	if id, ok := raw["id"].(string); ok && id != "" {
		chunk.ID = id
	}
	if object, ok := raw["object"].(string); ok && object != "" {
		chunk.Object = object
	}
	if created, ok := numberToInt64(raw["created"]); ok {
		chunk.Created = created
	}
	if upstreamModel, ok := raw["model"].(string); ok && upstreamModel != "" {
		chunk.Model = upstreamModel
	}
	if role, ok := delta["role"].(string); ok && role != "" {
		chunk.Choices[0].Delta.Role = role
	}
	if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
		chunk.Choices[0].FinishReason = finishReason
	}
	if conversationID, ok := raw["conversation_id"].(string); ok && conversationID != "" {
		chunk.ConversationID = conversationID
	}
	if sentinel, ok := raw["sentinel"].(map[string]interface{}); ok {
		chunk.Sentinel = sentinel
	}
	return chunk, true
}

func channelFromValue(value interface{}) string {
	switch item := value.(type) {
	case map[string]interface{}:
		if channel, _ := item["channel"].(string); channel != "" {
			return channel
		}
		if delta, ok := item["delta"].(map[string]interface{}); ok {
			if channel, _ := delta["channel"].(string); channel != "" {
				return channel
			}
		}
		if choices, ok := item["choices"].([]interface{}); ok {
			for _, choiceValue := range choices {
				choice, ok := choiceValue.(map[string]interface{})
				if !ok {
					continue
				}
				if channel, _ := choice["channel"].(string); channel != "" {
					return channel
				}
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					if channel, _ := delta["channel"].(string); channel != "" {
						return channel
					}
				}
			}
		}
		if message, ok := item["message"].(map[string]interface{}); ok {
			if channel := channelFromValue(message); channel != "" {
				return channel
			}
		}
		if nested, ok := item["v"].(map[string]interface{}); ok {
			if channel := channelFromValue(nested); channel != "" {
				return channel
			}
		}
	}
	return ""
}

func numberToInt64(value interface{}) (int64, bool) {
	switch item := value.(type) {
	case float64:
		return int64(item), true
	case int64:
		return item, true
	case int:
		return int64(item), true
	default:
		return 0, false
	}
}

func firstChunkContent(chunk official_types.ChatCompletionChunk) string {
	if len(chunk.Choices) == 0 {
		return ""
	}
	return chunk.Choices[0].Delta.Content
}

func firstChunkRole(chunk official_types.ChatCompletionChunk) string {
	if len(chunk.Choices) == 0 {
		return ""
	}
	return chunk.Choices[0].Delta.Role
}

func normalizeOpenAIContentDelta(currentText string, incoming string) string {
	if incoming == "" {
		return ""
	}
	if currentText == "" {
		return incoming
	}
	if strings.HasPrefix(incoming, currentText) {
		return incoming[len(currentText):]
	}
	return incoming
}

func firstStringPart(parts []interface{}) string {
	if len(parts) == 0 {
		return ""
	}
	text, _ := parts[0].(string)
	return text
}

func firstChunkFinishReason(chunk official_types.ChatCompletionChunk) string {
	if len(chunk.Choices) == 0 || chunk.Choices[0].FinishReason == nil {
		return ""
	}
	if reason, ok := chunk.Choices[0].FinishReason.(string); ok {
		return reason
	}
	return fmt.Sprint(chunk.Choices[0].FinishReason)
}

func sentinelsFromResponse(response chatgpt_types.ChatGPTResponse) []map[string]interface{} {
	var raw map[string]interface{}
	data, err := json.Marshal(response)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	var sentinel []map[string]interface{}
	collectSentinelsFromValue(raw["sentinel"], &sentinel)
	collectSentinelsFromValue(raw["message"], &sentinel)
	return sentinel
}

func collectSentinelsFromValue(value interface{}, sentinel *[]map[string]interface{}) {
	switch item := value.(type) {
	case map[string]interface{}:
		if event, ok := item["event"].(string); ok && event != "" {
			*sentinel = append(*sentinel, item)
		}
		for _, nested := range item {
			collectSentinelsFromValue(nested, sentinel)
		}
	case []interface{}:
		for _, nested := range item {
			collectSentinelsFromValue(nested, sentinel)
		}
	}
}

func isUsableConversationResponse(response chatgpt_types.ChatGPTResponse) bool {
	return response.Error != nil ||
		response.Message.ID != "" ||
		response.Message.Author.Role != "" ||
		len(response.Message.Content.Parts) > 0 ||
		response.Message.EndTurn != nil
}

func responseFromValue(value interface{}) (chatgpt_types.ChatGPTResponse, bool) {
	if value == nil {
		return chatgpt_types.ChatGPTResponse{}, false
	}
	data, err := json.Marshal(value)
	if err != nil {
		return chatgpt_types.ChatGPTResponse{}, false
	}

	var response chatgpt_types.ChatGPTResponse
	if err := json.Unmarshal(data, &response); err == nil && isUsableConversationResponse(response) {
		return response, true
	}

	var message chatgpt_types.Message
	if err := json.Unmarshal(data, &message); err == nil && (message.ID != "" || message.Author.Role != "" || len(message.Content.Parts) > 0 || message.EndTurn != nil) {
		response.Message = message
		return response, true
	}

	return chatgpt_types.ChatGPTResponse{}, false
}

func applyConversationPatch(state *conversationPatchState, patchPath string, operation string, value interface{}) bool {
	ensureConversationPatchDefaults(state)
	switch {
	case patchPath == "/conversation_id":
		if text, ok := value.(string); ok {
			state.response.ConversationID = text
		}
	case patchPath == "/message":
		if response, ok := responseFromValue(value); ok {
			if response.ConversationID != "" {
				state.response.ConversationID = response.ConversationID
			}
			state.response.Message = response.Message
		}
		if channel := channelFromValue(value); channel != "" {
			state.channel = channel
		}
	case patchPath == "/message/id":
		if text, ok := value.(string); ok {
			state.response.Message.ID = text
		}
	case patchPath == "/message/channel":
		if text, ok := value.(string); ok {
			state.channel = text
		}
	case patchPath == "/message/author/role":
		if text, ok := value.(string); ok {
			state.response.Message.Author.Role = text
		}
	case patchPath == "/message/recipient":
		if text, ok := value.(string); ok {
			state.response.Message.Recipient = text
		}
	case patchPath == "/message/content/content_type":
		if text, ok := value.(string); ok {
			state.response.Message.Content.ContentType = text
		}
	case patchPath == "/message/content/parts":
		if parts, ok := value.([]interface{}); ok {
			state.response.Message.Content.Parts = parts
		}
	case strings.HasPrefix(patchPath, "/message/content/parts/0"):
		if text, ok := value.(string); ok {
			current, _ := state.response.Message.Content.Parts[0].(string)
			if operation == "append" {
				text = current + text
			}
			state.response.Message.Content.Parts[0] = text
		}
	case patchPath == "/message/metadata/message_type":
		if text, ok := value.(string); ok {
			state.response.Message.Metadata.MessageType = text
		}
	case patchPath == "/message/metadata/model_slug":
		if text, ok := value.(string); ok {
			state.response.Message.Metadata.ModelSlug = text
		}
	case patchPath == "/message/metadata/finish_details":
		if value == nil {
			state.response.Message.Metadata.FinishDetails = nil
			break
		}
		data, err := json.Marshal(value)
		if err != nil {
			break
		}
		var finishDetails chatgpt_types.FinishDetails
		if json.Unmarshal(data, &finishDetails) == nil {
			state.response.Message.Metadata.FinishDetails = &finishDetails
		}
	case patchPath == "/message/end_turn":
		state.response.Message.EndTurn = value
	default:
		return false
	}
	return true
}

func ensureConversationPatchDefaults(state *conversationPatchState) {
	if state.response.Message.Author.Role == "" {
		state.response.Message.Author.Role = "assistant"
	}
	if state.response.Message.Recipient == "" {
		state.response.Message.Recipient = "all"
	}
	if state.response.Message.Content.ContentType == "" {
		state.response.Message.Content.ContentType = "text"
	}
	if state.response.Message.Content.Parts == nil {
		state.response.Message.Content.Parts = []interface{}{""}
	}
	if state.response.Message.Metadata.MessageType == "" {
		state.response.Message.Metadata.MessageType = "next"
	}
}

type fileInfo struct {
	DownloadURL string `json:"download_url"`
	Status      string `json:"status"`
	URL         string `json:"url"`
}

type ImageGenerationResult struct {
	URL     string
	B64JSON string
}

func GetImageSource(client httpclient.AuroraHttpClient, wg *sync.WaitGroup, url string, prompt string, account *accounts.Account, idx int, imgSource []string) {
	defer wg.Done()
	header := make(httpclient.AuroraHeaders)
	// Clear cookies
	if account != nil && account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	header.Set("User-Agent", defaultUserAgent())
	header.Set("Accept", "*/*")
	if account != nil && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	response, err := client.Request(http.MethodGet, url, header, nil, nil)
	if err != nil {
		return
	}
	defer response.Body.Close()
	var file_info fileInfo
	err = json.NewDecoder(response.Body).Decode(&file_info)
	if err != nil || file_info.Status != "success" {
		return
	}
	imgSource[idx] = "[![image](" + file_info.DownloadURL + " \"" + prompt + "\")](" + file_info.DownloadURL + ")"
}

func GetImageDownloadURL(client httpclient.AuroraHttpClient, url string, account *accounts.Account) (string, error) {
	header := make(httpclient.AuroraHeaders)
	if account != nil && account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	header.Set("User-Agent", defaultUserAgent())
	header.Set("Accept", "*/*")
	if account != nil && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	response, err := client.Request(http.MethodGet, url, header, nil, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var info fileInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		return "", err
	}
	if info.Status != "" && info.Status != "success" {
		return "", fmt.Errorf("image download url is not ready")
	}
	if info.DownloadURL == "" {
		info.DownloadURL = info.URL
	}
	if info.DownloadURL == "" {
		return "", fmt.Errorf("image download url is missing")
	}
	return info.DownloadURL, nil
}

func DownloadImageBytes(client httpclient.AuroraHttpClient, url string, account *accounts.Account) ([]byte, error) {
	header := make(httpclient.AuroraHeaders)
	if account != nil && account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	header.Set("User-Agent", defaultUserAgent())
	header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	if account != nil && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	response, err := client.Request(http.MethodGet, url, header, nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image download failed: %s", string(body))
	}
	return body, nil
}

func addImageResult(results *[]ImageGenerationResult, seen map[string]bool, result ImageGenerationResult) {
	key := result.URL
	if key == "" {
		key = result.B64JSON
	}
	if key == "" || seen[key] {
		return
	}
	seen[key] = true
	*results = append(*results, result)
}

func stripDataImagePrefix(value string) (string, bool) {
	if !strings.HasPrefix(value, "data:image/") {
		return value, false
	}
	parts := strings.SplitN(value, ",", 2)
	if len(parts) != 2 {
		return value, false
	}
	return parts[1], true
}

func fileDownloadBaseURL() string {
	apiURL := BaseURL + "/files/"
	if FILES_REVERSE_PROXY != "" {
		apiURL = FILES_REVERSE_PROXY
	}
	return strings.TrimRight(apiURL, "/") + "/"
}

func appendAssetPointerResult(client httpclient.AuroraHttpClient, account *accounts.Account, results *[]ImageGenerationResult, seen map[string]bool, assetPointer string) {
	if assetPointer == "" {
		return
	}
	assetParts := strings.Split(assetPointer, "//")
	if len(assetParts) != 2 || assetParts[1] == "" {
		return
	}
	downloadURL, err := GetImageDownloadURL(client, fileDownloadBaseURL()+assetParts[1]+"/download", account)
	if err != nil {
		return
	}
	addImageResult(results, seen, ImageGenerationResult{URL: downloadURL})
}

func appendFileIDResult(client httpclient.AuroraHttpClient, account *accounts.Account, results *[]ImageGenerationResult, seen map[string]bool, fileID string) {
	if fileID == "" {
		return
	}
	downloadURL, err := GetImageDownloadURL(client, fileDownloadBaseURL()+fileID+"/download", account)
	if err != nil {
		return
	}
	addImageResult(results, seen, ImageGenerationResult{URL: downloadURL})
}

func collectImageResultsFromValue(client httpclient.AuroraHttpClient, account *accounts.Account, value interface{}, results *[]ImageGenerationResult, seen map[string]bool) {
	switch item := value.(type) {
	case map[string]interface{}:
		if result, ok := item["result"].(string); ok && result != "" {
			if b64, isDataImage := stripDataImagePrefix(result); isDataImage {
				addImageResult(results, seen, ImageGenerationResult{B64JSON: b64})
			}
		}
		for _, key := range []string{"asset_pointer", "assetPointer"} {
			if assetPointer, ok := item[key].(string); ok {
				appendAssetPointerResult(client, account,results, seen, assetPointer)
			}
		}
		for _, key := range []string{"file_id", "fileId", "id"} {
			if fileID, ok := item[key].(string); ok && strings.HasPrefix(fileID, "file-") {
				appendFileIDResult(client, account,results, seen, fileID)
			}
		}
		for _, key := range []string{"download_url", "downloadUrl", "url"} {
			if rawURL, ok := item[key].(string); ok && strings.HasPrefix(rawURL, "http") {
				addImageResult(results, seen, ImageGenerationResult{URL: rawURL})
			}
		}
		for _, nested := range item {
			collectImageResultsFromValue(client, account,nested, results, seen)
		}
	case []interface{}:
		for _, nested := range item {
			collectImageResultsFromValue(client, account,nested, results, seen)
		}
	case string:
		if b64, isDataImage := stripDataImagePrefix(item); isDataImage {
			addImageResult(results, seen, ImageGenerationResult{B64JSON: b64})
		}
	}
}

func CollectImageResults(response *http.Response, client httpclient.AuroraHttpClient, account *accounts.Account) ([]ImageGenerationResult, string, string, error) {
	reader := bufio.NewReader(response.Body)
	var originalResponse chatgpt_types.ChatGPTResponse
	var convID string
	var results []ImageGenerationResult
	seen := make(map[string]bool)
	var textParts []string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return results, convID, strings.Join(textParts, ""), err
		}
		if len(line) < 6 {
			continue
		}
		line = line[6:]
		if strings.HasPrefix(line, "[DONE]") {
			break
		}
		originalResponse.Message.ID = ""
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err == nil {
			collectImageResultsFromValue(client, account,raw, &results, seen)
		}
		if err := json.Unmarshal([]byte(line), &originalResponse); err != nil {
			continue
		}
		if originalResponse.Error != nil {
			return results, convID, strings.Join(textParts, ""), fmt.Errorf("image generation error: %v", originalResponse.Error)
		}
		if originalResponse.ConversationID != convID {
			if convID == "" {
				convID = originalResponse.ConversationID
			} else {
				continue
			}
		}
		if originalResponse.Message.Recipient != "all" {
			continue
		}
		if originalResponse.Message.Content.ContentType == "text" && len(originalResponse.Message.Content.Parts) > 0 {
			if text, ok := originalResponse.Message.Content.Parts[0].(string); ok && text != "" {
				textParts = append(textParts, text)
			}
			continue
		}
		if originalResponse.Message.Content.ContentType != "multimodal_text" {
			continue
		}
		for _, part := range originalResponse.Message.Content.Parts {
			jsonItem, _ := json.Marshal(part)
			var dalleContent chatgpt_types.DalleContent
			if err := json.Unmarshal(jsonItem, &dalleContent); err != nil || dalleContent.AssetPointer == "" {
				continue
			}
			appendAssetPointerResult(client, account,&results, seen, dalleContent.AssetPointer)
		}
	}
	return results, convID, strings.Join(textParts, ""), nil
}

func conversationFetchHeaders(account *accounts.Account) httpclient.AuroraHeaders {
	header := createBaseHeader()
	header.Set("Accept", "application/json")
	header.Set("Content-Type", "application/json")
	if account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	if account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	setTeamAccountHeader(header, account)
	return header
}

func getConversation(client httpclient.AuroraHttpClient, account *accounts.Account, conversationID string) (map[string]interface{}, error) {
	if conversationID == "" {
		return nil, fmt.Errorf("missing conversation id")
	}
	reqURL := BaseURL + "/conversation/" + conversationID
	response, err := client.Request(http.MethodGet, reqURL, conversationFetchHeaders(account), nil, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get conversation failed: %s", string(body))
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func collectImageResultsFromConversation(client httpclient.AuroraHttpClient, account *accounts.Account, conversation map[string]interface{}) []ImageGenerationResult {
	var results []ImageGenerationResult
	seen := make(map[string]bool)
	collectImageResultsFromValue(client, account,conversation, &results, seen)
	return results
}

func findImageGenerationError(value interface{}) string {
	switch item := value.(type) {
	case map[string]interface{}:
		if itemType, ok := item["type"].(string); ok {
			switch itemType {
			case "content_policy_violation", "content_policy_error":
				if message, ok := item["message"].(string); ok && message != "" {
					return message
				}
				return "Image generation was rejected by the upstream content policy."
			}
		}
		if code, ok := item["code"].(string); ok && strings.Contains(strings.ToLower(code), "content_policy") {
			if message, ok := item["message"].(string); ok && message != "" {
				return message
			}
			return "Image generation was rejected by the upstream content policy."
		}
		for _, nested := range item {
			if message := findImageGenerationError(nested); message != "" {
				return message
			}
		}
	case []interface{}:
		for _, nested := range item {
			if message := findImageGenerationError(nested); message != "" {
				return message
			}
		}
	}
	return ""
}

func PollImageResults(client httpclient.AuroraHttpClient, account *accounts.Account, conversationID string, initial []ImageGenerationResult) ([]ImageGenerationResult, error) {
	if len(initial) > 0 || conversationID == "" {
		return initial, nil
	}
	var lastErr error
	for i := 0; i < 45; i++ {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		conversation, err := getConversation(client, account,conversationID)
		if err != nil {
			lastErr = err
			continue
		}
		if message := findImageGenerationError(conversation); message != "" {
			return nil, errors.New(message)
		}
		results := collectImageResultsFromConversation(client, account,conversation)
		if len(results) > 0 {
			return results, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, nil
}

func imageModelSlug(model string) string {
	if model == "" || strings.HasPrefix(model, "dall-e") {
		model = "gpt-image-2"
	}
	if model == "gpt-image-2" || strings.HasPrefix(model, "gpt-image") {
		return "auto"
	}
	return model
}

func imageConversationHeaders(account *accounts.Account, turnStile *TurnStile, conduitToken, accept string) httpclient.AuroraHeaders {
	return imageConversationHeadersWithState(account, turnStile, conduitToken, accept, nil)
}

func imageConversationHeadersWithState(account *accounts.Account, turnStile *TurnStile, conduitToken, accept string, state *ChatClientState) httpclient.AuroraHeaders {
	conversationID := ""
	deviceID := oaiDeviceID
	sessionID := oaiSessionID
	ua := ""
	if state != nil {
		if state.ConversationID != "" {
			conversationID = state.ConversationID
		}
		if state.DeviceID != "" {
			deviceID = state.DeviceID
		}
		if state.SessionID != "" {
			sessionID = state.SessionID
		}
		if state.UserAgent != "" {
			ua = state.UserAgent
		}
	}
	acceptVal := accept
	if acceptVal == "" {
		acceptVal = "*/*"
	}
	b := headerbuilder.New().
		WithBaseHeaders(conversationID).
		WithDeviceID(deviceID).
		WithSessionID(sessionID).
		WithUserAgent(ua).
		WithContentType("application/json").
		WithAccept(acceptVal).
		WithSentinelTokens(headerbuilder.SentinelTokens{
			TurnStileToken:   turnStile.TurnStileToken,
			ProofOfWorkToken: turnStile.ProofOfWorkToken,
			TurnstileToken:   turnStile.TurnstileToken,
		}).
		WithAuth(account).
		WithTeamAccount(account)
	if conduitToken != "" {
		b.WithConduitToken(conduitToken)
	}
	if accept == "text/event-stream" {
		b.WithTurnTraceID(uuid.NewString())
	}
	if account.PUID != "" {
		b.WithCookies(account)
	}
	return b.Build()
}

func prepareImageConversation(client httpclient.AuroraHttpClient, account *accounts.Account, turnStile *TurnStile, prompt, model string, state *ChatClientState) (string, error) {
	parentMessageID := "client-created-root"
	if state != nil && state.ParentMessageID != "" {
		parentMessageID = state.ParentMessageID
	}
	payload := map[string]interface{}{
		"action":                "next",
		"fork_from_shared_post": false,
		"parent_message_id":     parentMessageID,
		"model":                 imageModelSlug(model),
		"client_prepare_state":  "success",
		"timezone_offset_min":   420,
		"timezone":              "America/Los_Angeles",
		"conversation_mode":     map[string]string{"kind": "primary_assistant"},
		"system_hints":          []string{"picture_v2"},
		"partial_query": map[string]interface{}{
			"id":      uuid.NewString(),
			"author":  map[string]string{"role": "user"},
			"content": map[string]interface{}{"content_type": "text", "parts": []string{prompt}},
		},
		"supports_buffering":     true,
		"supported_encodings":    []string{"v1"},
		"client_contextual_info": state.ClientContextualInfo(),
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	response, err := client.Request(http.MethodPost, BaseURL+"/f/conversation/prepare", imageConversationHeadersWithState(account, turnStile, "", "*/*", state), nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("prepare image conversation failed: %s", string(body))
	}
	var result struct {
		ConduitToken string `json:"conduit_token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	if result.ConduitToken == "" {
		return "", fmt.Errorf("missing conduit_token: %s", string(body))
	}
	return result.ConduitToken, nil
}

func GeneratePictureConversationImages(client httpclient.AuroraHttpClient, account *accounts.Account, turnStile *TurnStile, prompt, model, proxy string) ([]ImageGenerationResult, string, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	state := NewChatClientState()
	conduitToken, err := prepareImageConversation(client, account,turnStile, prompt, model, state)
	if err != nil {
		return nil, "", err
	}
	payload := map[string]interface{}{
		"action": "next",
		"messages": []map[string]interface{}{
			{
				"id":          uuid.NewString(),
				"author":      map[string]string{"role": "user"},
				"create_time": time.Now().Unix(),
				"content":     map[string]interface{}{"content_type": "text", "parts": []string{prompt}},
				"metadata": map[string]interface{}{
					"developer_mode_connector_ids": []interface{}{},
					"selected_github_repos":        []interface{}{},
					"selected_all_github_repos":    false,
					"system_hints":                 []string{"picture_v2"},
					"serialization_metadata":       map[string]interface{}{"custom_symbol_offsets": []interface{}{}},
				},
			},
		},
		"parent_message_id":                    state.ParentMessageID,
		"model":                                imageModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  420,
		"timezone":                             "America/Los_Angeles",
		"conversation_mode":                    map[string]string{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []string{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []string{"v1"},
		"client_contextual_info":               state.ClientContextualInfo(),
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"thinking_effort":                      "standard",
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	response, err := client.Request(http.MethodPost, BaseURL+"/f/conversation", imageConversationHeadersWithState(account, turnStile, conduitToken, "text/event-stream", state), nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return nil, "", fmt.Errorf("image conversation failed: %s", string(body))
	}
	results, conversationID, upstreamText, err := CollectImageResults(response, client, account)
	if err != nil {
		return results, upstreamText, err
	}
	results, err = PollImageResults(client, account,conversationID, results)
	if err != nil {
		return results, upstreamText, err
	}
	return results, upstreamText, nil
}

// ImageEditReference 表示已经上传到 ChatGPT 文件服务的一张源图,
// 用于构造 /f/conversation 时的 image_asset_pointer 部件。
type ImageEditReference struct {
	FileID   string
	Width    int
	Height   int
	Size     int
	MimeType string
	Filename string
}

// GeneratePictureConversationImagesWithReferences 在原有文生图流程基础上支持
// 携带已上传的源图(image_asset_pointer + attachments)进入对话,
// 用于实现 OpenAI 兼容的 /v1/images/edits 和 /v1/images/variations。
// 当 references 为空时,行为等价于 GeneratePictureConversationImages。
func GeneratePictureConversationImagesWithReferences(client httpclient.AuroraHttpClient, account *accounts.Account, turnStile *TurnStile, prompt, model, proxy string, references []ImageEditReference) ([]ImageGenerationResult, string, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	state := NewChatClientState()
	conduitToken, err := prepareImageConversation(client, account,turnStile, prompt, model, state)
	if err != nil {
		return nil, "", err
	}

	// 组装 message.parts:每个 reference -> image_asset_pointer,然后追加 prompt 文本
	parts := make([]interface{}, 0, len(references)+1)
	attachments := make([]map[string]interface{}, 0, len(references))
	for _, ref := range references {
		if ref.FileID == "" {
			continue
		}
		part := map[string]interface{}{
			"content_type":  "image_asset_pointer",
			"asset_pointer": "file-service://" + ref.FileID,
		}
		if ref.Width > 0 {
			part["width"] = ref.Width
		}
		if ref.Height > 0 {
			part["height"] = ref.Height
		}
		if ref.Size > 0 {
			part["size_bytes"] = ref.Size
		}
		parts = append(parts, part)

		attachment := map[string]interface{}{
			"id":        ref.FileID,
			"size":      ref.Size,
			"name":      ref.Filename,
			"mime":      ref.MimeType,
			"mimeType":  ref.MimeType,
			"source":    "library",
		}
		if ref.Width > 0 {
			attachment["width"] = ref.Width
		}
		if ref.Height > 0 {
			attachment["height"] = ref.Height
		}
		attachments = append(attachments, attachment)
	}
	if prompt != "" {
		parts = append(parts, prompt)
	}

	var content map[string]interface{}
	if len(parts) == 0 {
		content = map[string]interface{}{"content_type": "text", "parts": []string{prompt}}
	} else {
		content = map[string]interface{}{"content_type": "multimodal_text", "parts": parts}
	}

	metadata := map[string]interface{}{
		"developer_mode_connector_ids": []interface{}{},
		"selected_github_repos":        []interface{}{},
		"selected_all_github_repos":    false,
		"system_hints":                 []string{"picture_v2"},
		"serialization_metadata":       map[string]interface{}{"custom_symbol_offsets": []interface{}{}},
	}
	if len(attachments) > 0 {
		metadata["attachments"] = attachments
	}

	payload := map[string]interface{}{
		"action": "next",
		"messages": []map[string]interface{}{
			{
				"id":          uuid.NewString(),
				"author":      map[string]string{"role": "user"},
				"create_time": time.Now().Unix(),
				"content":     content,
				"metadata":    metadata,
			},
		},
		"parent_message_id":                    state.ParentMessageID,
		"model":                                imageModelSlug(model),
		"client_prepare_state":                 "sent",
		"timezone_offset_min":                  420,
		"timezone":                             "America/Los_Angeles",
		"conversation_mode":                    map[string]string{"kind": "primary_assistant"},
		"enable_message_followups":             true,
		"system_hints":                         []string{"picture_v2"},
		"supports_buffering":                   true,
		"supported_encodings":                  []string{"v1"},
		"client_contextual_info":               state.ClientContextualInfo(),
		"paragen_cot_summary_display_override": "allow",
		"force_parallel_switch":                "auto",
		"thinking_effort":                      "standard",
	}
	bodyJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	response, err := client.Request(http.MethodPost, BaseURL+"/f/conversation", imageConversationHeadersWithState(account, turnStile, conduitToken, "text/event-stream", state), nil, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return nil, "", fmt.Errorf("image conversation failed: %s", string(body))
	}
	results, conversationID, upstreamText, err := CollectImageResults(response, client, account)
	if err != nil {
		return results, upstreamText, err
	}
	results, err = PollImageResults(client, account,conversationID, results)
	if err != nil {
		return results, upstreamText, err
	}
	return results, upstreamText, nil
}

func Handler(c *gin.Context, response *http.Response, client httpclient.AuroraHttpClient, account *accounts.Account, uuid string, translated_request chatgpt_types.ChatGPTRequest, stream bool, model string) (string, *ContinueInfo) {
	result := HandlerDetailed(c, response, client, account,uuid, translated_request, stream, model)
	return result.Text, result.Continue
}

func HandlerDetailed(c *gin.Context, response *http.Response, client httpclient.AuroraHttpClient, account *accounts.Account, uuid string, translated_request chatgpt_types.ChatGPTRequest, stream bool, model string) HandlerResult {
	return HandlerDetailedWithWebsocket(c, response, client, account,uuid, translated_request, stream, model, nil)
}

type HandlerDetailedOptions struct {
	Websocket        *websocket.Conn
	ClientState      *ChatClientState
	ArtifactDelivery string
	ProxyURL         string
	// Tools 启用工具调用解析:设置后,HandlerDetailedWithOptions 会把
	// 累积的 text 喂给 toolcall.Parser,把 <tool_call>{...}</tool_call>
	// 切成 OpenAI delta.tool_calls 流式 chunk,并在 HandlerResult.ToolCalls
	// 中返回完整调用列表(用于多轮工具调用循环)。
	// 为空时保持原行为不变(向后兼容)。
	Tools []official_types.Tool
}

func HandlerDetailedWithWebsocket(c *gin.Context, response *http.Response, client httpclient.AuroraHttpClient, account *accounts.Account, uuid string, translated_request chatgpt_types.ChatGPTRequest, stream bool, model string, wsConn *websocket.Conn) HandlerResult {
	return HandlerDetailedWithOptions(c, response, client, account,uuid, translated_request, stream, model, HandlerDetailedOptions{Websocket: wsConn})
}

func HandlerDetailedWithOptions(c *gin.Context, response *http.Response, client httpclient.AuroraHttpClient, account *accounts.Account, uuid string, translated_request chatgpt_types.ChatGPTRequest, stream bool, model string, options HandlerDetailedOptions) HandlerResult {
	if model == "" {
		model = translated_request.Model
	}
	wsConn := options.Websocket
	if options.ClientState != nil {
		options.ClientState.ApplyToRequest(&translated_request)
	}
	max_tokens := false

	// Create a bufio.Reader from the response body
	reader := bufio.NewReader(response.Body)
	if stream && client != nil && account != nil {
		if wsConn == nil {
			if conn, err := DialChatWebsocketWithStateAndProxy(client, account,options.ClientState, options.ProxyURL); err == nil {
				wsConn = conn
				defer wsConn.Close()
			}
		} else {
			defer wsConn.Close()
		}
	}

	// Read the response byte by byte until a newline character is encountered
	if stream {
		// Response content type is text/event-stream
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	} else {
		// Response content type is application/json
		c.Header("Content-Type", "application/json")
	}
	var finish_reason string
	var previous_text types.StringStruct
	var original_response chatgpt_types.ChatGPTResponse
	var isRole = true
	var waitSource = false
	var isEnd = false
	var imgSource []string
	var convId string
	var sentinel []map[string]interface{}
	var thinkingText string
	var activeChannel string
	var assistantMessageID string
	artifactState := newArtifactAccumulator()
	artifactConfig := ArtifactStreamConfig{Delivery: options.ArtifactDelivery}
	var patchState conversationPatchState
	var handoffTopicID string
	var currentEvent string
	var readingWebsocket bool
	var websocketStream io.ReadCloser
	emitSentinels := func(items []map[string]interface{}) {
		if len(items) == 0 {
			return
		}
		sentinel = append(sentinel, items...)
		if !stream {
			return
		}
		for _, item := range items {
			chunk := official_types.NewChatCompletionChunk("", model)
			if convId != "" {
				chunk.ConversationID = convId
			}
			chunk.Sentinel = item
			c.Writer.WriteString("data: " + chunk.String() + "\n\n")
			c.Writer.Flush()
		}
	}
	observeArtifacts := func(line string) {
		var raw map[string]interface{}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			return
		}
		if cid := firstConversationID(raw); cid != "" && convId == "" {
			convId = cid
		}
		events := artifactState.ObserveRaw(raw, convId)
		emitSentinels(materializeArtifactEvents(client, account,convId, events, artifactConfig))
		if artifactState.LastAssistantMsgID != "" {
			assistantMessageID = artifactState.LastAssistantMsgID
		}
		if artifactState.ConversationID != "" && convId == "" {
			convId = artifactState.ConversationID
		}
	}
	emitThinking := func(delta string) {
		if delta == "" {
			return
		}
		thinkingText += delta
		emitSentinels([]map[string]interface{}{{
			"event": "thinking",
			"kind":  "analysis",
			"delta": delta,
		}})
		if stream {
			reasoningChunk := official_types.NewReasoningChunk(delta, model)
			if convId != "" {
				reasoningChunk.ConversationID = convId
			}
			c.Writer.WriteString("data: " + reasoningChunk.String() + "\n\n")
			c.Writer.Flush()
		}
	}
	finalizeArtifacts := func() {
		emitSentinels(materializeArtifactEvents(client, account,convId, artifactState.Finalize(), artifactConfig))
	}
readLoop:
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				break
			}
			if err != io.EOF {
				return HandlerResult{}
			}
		}
		if eventName, ok := sseEventName(line); ok {
			currentEvent = eventName
		}
		for _, line := range sseDataPayloads(line) {
			// Check if line starts with [DONE]
			if strings.HasPrefix(line, "[DONE]") {
				if shouldUseWebsocketHandoff(readingWebsocket, handoffTopicID, wsConn, previous_text.Text, imgSource) {
					wsReader, err := chatWebsocketStreamReader(wsConn, handoffTopicID)
					if err == nil {
						websocketStream = wsReader
						defer websocketStream.Close()
						reader = bufio.NewReader(wsReader)
						readingWebsocket = true
						currentEvent = ""
						continue readLoop
					}
				}
				finalizeArtifacts()
				break readLoop
			}
			observeArtifacts(line)
			if topicID, skip := streamHandoffTopicFromPayload(line, currentEvent); skip {
				if topicID != "" {
					handoffTopicID = topicID
				}
				currentEvent = ""
				continue
			}
			// Parse the line as JSON
			streamEvent, ok := parseConversationEvent(line, &patchState, model)
			if os.Getenv("DEBUG_SSE") != "" {
				debugText := streamEvent.text
				debugSrc := "chunk"
				if streamEvent.response.Message.ID != "" {
					debugText = firstStringPart(streamEvent.response.Message.Content.Parts)
					debugSrc = "response"
				}
				raw := strings.TrimSpace(line)
				if len(raw) > 200 {
					raw = raw[:200] + "..."
				}
				fmt.Printf("[sse-in] src=%s channel=%q textLen=%d finish=%q parsed=%v raw=%q\n", debugSrc, streamEvent.channel, len(debugText), streamEvent.finishReason, ok, raw)
			}
			if !ok {
				currentEvent = ""
				continue
			}
			if streamEvent.chunk != nil {
				if streamEvent.conversationID != "" {
					convId = streamEvent.conversationID
				}
				if streamEvent.chunk.Sentinel != nil {
					sentinel = append(sentinel, streamEvent.chunk.Sentinel)
				}
				deltaText := normalizeOpenAIContentDelta(previous_text.Text, streamEvent.text)
				if streamEvent.channel != "" {
					activeChannel = streamEvent.channel
				}
				if streamEvent.finishReason != "" {
					finish_reason = streamEvent.finishReason
					if finish_reason == "length" {
						max_tokens = true
					}
					isEnd = true
				}
				if activeChannel == "analysis" {
					emitThinking(streamEvent.text)
					if streamEvent.isStop {
						if stream {
							finalLine := official_types.StopChunkWithConversation(finish_reason, model, convId)
							c.Writer.WriteString("data: " + finalLine.String() + "\n\n")
							c.Writer.Flush()
						}
						if max_tokens && convId != "" && assistantMessageID != "" {
							finalizeArtifacts()
							return HandlerResult{
								Text:              strings.Join(imgSource, "") + previous_text.Text,
								ThinkingText:      thinkingText,
								ConversationID:    convId,
								ParentMessageID:   assistantMessageID,
								Sentinel:          sentinel,
								ArtifactSignals:   artifactState.Signals,
								SandboxArtifacts:  artifactState.SandboxArtifacts,
								PDFArtifacts:      artifactState.PDFArtifacts,
								GeneratedImageIDs: artifactState.ImageFileIDs,
								StopSent:          true,
								Continue: &ContinueInfo{
									ConversationID: convId,
									ParentID:       assistantMessageID,
								},
							}
						}
						finalizeArtifacts()
						return HandlerResult{
							Text:              strings.Join(imgSource, "") + previous_text.Text,
							ThinkingText:      thinkingText,
							ConversationID:    convId,
							ParentMessageID:   assistantMessageID,
							Sentinel:          sentinel,
							ArtifactSignals:   artifactState.Signals,
							SandboxArtifacts:  artifactState.SandboxArtifacts,
							PDFArtifacts:      artifactState.PDFArtifacts,
							GeneratedImageIDs: artifactState.ImageFileIDs,
							StopSent:          true,
						}
					}
					currentEvent = ""
					continue
				}
				if stream {
					outChunk := *streamEvent.chunk
					if len(outChunk.Choices) > 0 {
						outChunk.Choices[0].Delta.Content = deltaText
						if streamEvent.role == "" || !isRole {
							outChunk.Choices[0].Delta.Role = ""
						}
					}
					if streamEvent.isStop && outChunk.ConversationID == "" {
						outChunk.ConversationID = convId
					}
					shouldWrite := deltaText != "" ||
						(streamEvent.role != "" && isRole) ||
						streamEvent.chunk.Sentinel != nil ||
						streamEvent.isStop
					if shouldWrite {
						c.Writer.WriteString("data: " + outChunk.String() + "\n\n")
						c.Writer.Flush()
					}
					if streamEvent.role != "" && isRole {
						isRole = false
					}
				}
				if deltaText != "" {
					previous_text.Text += deltaText
				}
				if streamEvent.isStop {
					if max_tokens && convId != "" && assistantMessageID != "" {
						finalizeArtifacts()
						return HandlerResult{
							Text:              strings.Join(imgSource, "") + previous_text.Text,
							ThinkingText:      thinkingText,
							ConversationID:    convId,
							ParentMessageID:   assistantMessageID,
							Sentinel:          sentinel,
							ArtifactSignals:   artifactState.Signals,
							SandboxArtifacts:  artifactState.SandboxArtifacts,
							PDFArtifacts:      artifactState.PDFArtifacts,
							GeneratedImageIDs: artifactState.ImageFileIDs,
							StopSent:          true,
							Continue: &ContinueInfo{
								ConversationID: convId,
								ParentID:       assistantMessageID,
							},
						}
					}
					finalizeArtifacts()
					return HandlerResult{
						Text:              strings.Join(imgSource, "") + previous_text.Text,
						ThinkingText:      thinkingText,
						ConversationID:    convId,
						ParentMessageID:   assistantMessageID,
						Sentinel:          sentinel,
						ArtifactSignals:   artifactState.Signals,
						SandboxArtifacts:  artifactState.SandboxArtifacts,
						PDFArtifacts:      artifactState.PDFArtifacts,
						GeneratedImageIDs: artifactState.ImageFileIDs,
						StopSent:          true,
					}
				}
				currentEvent = ""
				continue
			}
			original_response = streamEvent.response
			if original_response.Error != nil {
				c.JSON(500, gin.H{"error": original_response.Error})
				return HandlerResult{}
			}
			sentinel = append(sentinel, sentinelsFromResponse(original_response)...)
			if original_response.ConversationID != convId {
				if convId == "" {
					convId = original_response.ConversationID
				} else {
					continue
				}
			}
			if streamEvent.channel != "" {
				activeChannel = streamEvent.channel
			}
			if original_response.Message.ID != "" && (original_response.Message.Author.Role == "assistant" || original_response.Message.Author.Role == "tool") {
				assistantMessageID = original_response.Message.ID
			}
			if activeChannel == "analysis" {
				thinkingDelta := normalizeOpenAIContentDelta(thinkingText, firstStringPart(original_response.Message.Content.Parts))
				emitThinking(thinkingDelta)
				currentEvent = ""
				continue
			}
			if !(original_response.Message.Author.Role == "assistant" || (original_response.Message.Author.Role == "tool" && original_response.Message.Content.ContentType != "text")) || original_response.Message.Content.Parts == nil {
				continue
			}
			if original_response.Message.Metadata.MessageType == "" && activeChannel != "final" {
				continue
			}
			if (original_response.Message.Metadata.MessageType != "next" && original_response.Message.Metadata.MessageType != "continue" && activeChannel != "final") || !strings.HasSuffix(original_response.Message.Content.ContentType, "text") {
				continue
			}
			if original_response.Message.EndTurn != nil {
				if waitSource {
					waitSource = false
				}
				isEnd = true
			}
			if len(original_response.Message.Metadata.Citations) != 0 {
				r := []rune(original_response.Message.Content.Parts[0].(string))
				if waitSource {
					if string(r[len(r)-1:]) == "】" {
						waitSource = false
					} else {
						continue
					}
				}
				offset := 0
				for _, citation := range original_response.Message.Metadata.Citations {
					rl := len(r)
					attr := urlAttrMap[citation.Metadata.URL]
					if attr == "" {
						u, _ := url.Parse(citation.Metadata.URL)
						BaseURL := u.Scheme + "://" + u.Host + "/"
						attr = getURLAttribution(client, account,BaseURL)
						if attr != "" {
							urlAttrMap[citation.Metadata.URL] = attr
						}
					}
					original_response.Message.Content.Parts[0] = string(r[:citation.StartIx+offset]) + " ([" + attr + "](" + citation.Metadata.URL + " \"" + citation.Metadata.Title + "\"))" + string(r[citation.EndIx+offset:])
					r = []rune(original_response.Message.Content.Parts[0].(string))
					offset += len(r) - rl
				}
			} else if waitSource {
				continue
			}
			response_string := ""
			if original_response.Message.Recipient != "all" {
				continue
			}
			if original_response.Message.Content.ContentType == "multimodal_text" {
				apiUrl := BaseURL + "/files/"
				if FILES_REVERSE_PROXY != "" {
					apiUrl = FILES_REVERSE_PROXY
				}
				imgSource = make([]string, len(original_response.Message.Content.Parts))
				var wg sync.WaitGroup
				for index, part := range original_response.Message.Content.Parts {
					jsonItem, _ := json.Marshal(part)
					var dalle_content chatgpt_types.DalleContent
					err = json.Unmarshal(jsonItem, &dalle_content)
					if err != nil {
						continue
					}
					url := apiUrl + strings.Split(dalle_content.AssetPointer, "//")[1] + "/download"
					wg.Add(1)
					go GetImageSource(client, &wg, url, dalle_content.Metadata.Dalle.Prompt, account,index, imgSource)
				}
				wg.Wait()
				translated_response := official_types.NewChatCompletionChunk(strings.Join(imgSource, ""), model)
				if isRole {
					translated_response.Choices[0].Delta.Role = original_response.Message.Author.Role
				}
				response_string = "data: " + translated_response.String() + "\n\n"
			}
			if response_string == "" {
				response_string = chatgpt.ConvertToString(&original_response, &previous_text, isRole, model)
			}
			if response_string == "" {
				if isEnd {
					goto endProcess
				} else {
					continue
				}
			}
			if response_string == "【" {
				waitSource = true
				continue
			}
		endProcess:
			isRole = false
			if stream {
				_, err = c.Writer.WriteString(response_string)
				if err != nil {
					return HandlerResult{}
				}
				c.Writer.Flush()
			}

			if original_response.Message.Metadata.FinishDetails != nil {
				if original_response.Message.Metadata.FinishDetails.Type == "max_tokens" {
					max_tokens = true
				}
				finish_reason = original_response.Message.Metadata.FinishDetails.Type
			}
			if isEnd {
				if stream {
					final_line := official_types.StopChunkWithConversation(finish_reason, model, convId)
					c.Writer.WriteString("data: " + final_line.String() + "\n\n")
					c.Writer.Flush()
				}
				finalizeArtifacts()
				return HandlerResult{
					Text:              strings.Join(imgSource, "") + previous_text.Text,
					ThinkingText:      thinkingText,
					ConversationID:    convId,
					ParentMessageID:   assistantMessageID,
					Sentinel:          sentinel,
					ArtifactSignals:   artifactState.Signals,
					SandboxArtifacts:  artifactState.SandboxArtifacts,
					PDFArtifacts:      artifactState.PDFArtifacts,
					GeneratedImageIDs: artifactState.ImageFileIDs,
					StopSent:          stream,
				}
			}
			currentEvent = ""
		}
		if err == io.EOF {
			break
		}
	}
	if !max_tokens {
		finalizeArtifacts()
		return HandlerResult{
			Text:              strings.Join(imgSource, "") + previous_text.Text,
			ThinkingText:      thinkingText,
			ConversationID:    convId,
			ParentMessageID:   assistantMessageID,
			Sentinel:          sentinel,
			ArtifactSignals:   artifactState.Signals,
			SandboxArtifacts:  artifactState.SandboxArtifacts,
			PDFArtifacts:      artifactState.PDFArtifacts,
			GeneratedImageIDs: artifactState.ImageFileIDs,
		}
	}
	finalizeArtifacts()
	return HandlerResult{
		Text:              strings.Join(imgSource, "") + previous_text.Text,
		ThinkingText:      thinkingText,
		ConversationID:    convId,
		ParentMessageID:   assistantMessageID,
		Sentinel:          sentinel,
		ArtifactSignals:   artifactState.Signals,
		SandboxArtifacts:  artifactState.SandboxArtifacts,
		PDFArtifacts:      artifactState.PDFArtifacts,
		GeneratedImageIDs: artifactState.ImageFileIDs,
		Continue: &ContinueInfo{
			ConversationID: original_response.ConversationID,
			ParentID:       original_response.Message.ID,
		},
	}
}

type AuthSession struct {
	User struct {
		Id           string        `json:"id"`
		Name         string        `json:"name"`
		Email        string        `json:"email"`
		Image        string        `json:"image"`
		Picture      string        `json:"picture"`
		Idp          string        `json:"idp"`
		Iat          int           `json:"iat"`
		Mfa          bool          `json:"mfa"`
		Groups       []interface{} `json:"groups"`
		IntercomHash string        `json:"intercom_hash"`
	} `json:"user"`
	Expires      time.Time `json:"expires"`
	AccessToken  string    `json:"accessToken"`
	AuthProvider string    `json:"authProvider"`
}

func GETTokenForRefreshToken(client httpclient.AuroraHttpClient, refresh_token string, proxy string) (interface{}, int, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	rawUrl := "https://auth.openai.com/oauth/token"

	data := map[string]interface{}{
		"redirect_uri":  "com.openai.chat://auth.openai.com/ios/com.openai.chat/callback",
		"grant_type":    "refresh_token",
		"client_id":     "pdlLIX2Y72MIl2rhLhTE9VV9bN905kBh",
		"refresh_token": refresh_token,
	}

	reqBody, err := json.Marshal(data)
	if err != nil {
		return nil, 0, err
	}

	header := make(httpclient.AuroraHeaders)
	//req, _ := http.NewRequest("POST", url, bytes.NewBuffer(reqBody))
	header.Set("Authority", "auth.openai.com")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("Content-Type", "application/json")
	header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/112.0.0.0 Safari/537.36")
	header.Set("Accept", "*/*")
	resp, err := client.Request(http.MethodPost, rawUrl, header, nil, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, 0, err
	}
	return result, resp.StatusCode, nil
}

func GETTokenForSessionToken(client httpclient.AuroraHttpClient, session_token string, proxy string) (interface{}, int, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	url := "https://chatgpt.com/api/auth/session"
	header := make(httpclient.AuroraHeaders)
	header.Set("Authority", "chat.openai.com")
	header.Set("Accept-Language", "en-US,en;q=0.9")
	header.Set("User-Agent", defaultUserAgent())
	header.Set("Accept", "*/*")
	header.Set("Oai-Language", "en-US")
	header.Set("Origin", "https://chatgpt.com")
	header.Set("Referer", "https://chatgpt.com/")
	header.Set("Cookie", "__Secure-next-auth.session-token="+session_token)
	resp, err := client.Request(http.MethodGet, url, header, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	var result AuthSession
	json.NewDecoder(resp.Body).Decode(&result)

	cookies := parseCookies(resp.Cookies())
	if value, ok := cookies["__Secure-next-auth.session-token"]; ok {
		session_token = value
	}
	openai_sessionToken := official_types.NewOpenAISessionToken(session_token, result.AccessToken)
	return openai_sessionToken, resp.StatusCode, nil
}

func parseCookies(cookies []*http.Cookie) map[string]string {
	cookieDict := make(map[string]string)
	for _, cookie := range cookies {
		cookieDict[cookie.Name] = cookie.Value
	}
	return cookieDict
}

func HandlerTTS(response *http.Response, input string) (string, string) {
	reader := bufio.NewReader(response.Body)

	var convId string
	var fallbackMsgID string
	var patchState conversationPatchState

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && line == "" {
				break
			}
			if err != io.EOF {
				return "", ""
			}
		}
		for _, payload := range sseDataPayloads(line) {
			if strings.HasPrefix(payload, "[DONE]") {
				break
			}
			streamEvent, ok := parseConversationEvent(payload, &patchState, "auto")
			if !ok {
				var raw map[string]interface{}
				if json.Unmarshal([]byte(payload), &raw) == nil {
					if cid := firstConversationID(raw); cid != "" && convId == "" {
						convId = cid
					}
					if msgID := lastAssistantMessageID(raw); msgID != "" && fallbackMsgID == "" {
						fallbackMsgID = msgID
					}
				}
				continue
			}
			if streamEvent.response.Error != nil {
				return "", ""
			}
			originalResponse := streamEvent.response
			if streamEvent.conversationID != "" && convId == "" {
				convId = streamEvent.conversationID
			}
			if originalResponse.ConversationID != convId {
				if convId == "" {
					convId = originalResponse.ConversationID
				} else {
					continue
				}
			}
			if originalResponse.Message.ID == "" {
				continue
			}
			if originalResponse.Message.Author.Role != "assistant" {
				continue
			}

			// Newer upstream responses are not always an exact single-part echo of the
			// requested TTS input. Prefer an exact match, then fall back to the first
			// assistant message in the same conversation so synthesize still works.
			if fallbackMsgID == "" {
				fallbackMsgID = originalResponse.Message.ID
			}
			if len(originalResponse.Message.Content.Parts) == 0 {
				continue
			}
			for _, rawPart := range originalResponse.Message.Content.Parts {
				part, ok := rawPart.(string)
				if !ok {
					continue
				}
				if part == input || strings.Contains(part, input) || strings.Contains(input, part) {
					return originalResponse.Message.ID, convId
				}
			}
		}
		if err == io.EOF {
			break
		}
	}
	if fallbackMsgID != "" && convId != "" {
		return fallbackMsgID, convId
	}
	return "", ""
}

func getTTSBlobFromURL(client httpclient.AuroraHttpClient, account *accounts.Account, reqURL string) ([]byte, int, error) {
	header := createBaseHeader()
	header.Set("Accept", "audio/*,*/*")
	if account.Type != accounts.TypeNoAuth && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	if account.Type == accounts.TypeNoAuth {
		header.Set("Oai-Device-Id", account.Token)
	}
	if account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	setTeamAccountHeader(header, account)
	response, err := client.Request(http.MethodGet, reqURL, header, nil, nil)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer response.Body.Close()
	blob, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, response.StatusCode, fmt.Errorf("tts download failed: %s", string(blob))
	}
	return blob, response.StatusCode, nil
}

func parseTTSDownloadURL(blob []byte) string {
	var info fileInfo
	if err := json.Unmarshal(blob, &info); err != nil {
		return ""
	}
	if info.DownloadURL != "" {
		return info.DownloadURL
	}
	return info.URL
}

func GetTTS(client httpclient.AuroraHttpClient, account *accounts.Account, msgId, convId, voice, format, proxy string) ([]byte, int, error) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	params := url.Values{}
	params.Set("message_id", msgId)
	params.Set("conversation_id", convId)
	params.Set("voice", voice)
	params.Set("format", format)
	var reqUrl string
	if account.Type == accounts.TypeNoAuth {
		reqUrl = strings.Replace(BaseURL, "backend-api", "backend-anon", 1) + "/synthesize?" + params.Encode()
	} else {
		reqUrl = BaseURL + "/synthesize?" + params.Encode()
	}

	blob, status, err := getTTSBlobFromURL(client, account,reqUrl)
	if err == nil {
		if downloadURL := parseTTSDownloadURL(blob); downloadURL != "" {
			return getTTSBlobFromURL(client, account,downloadURL)
		}
		return blob, status, nil
	}

	// Some upstream variants now return a signed file URL payload or fail on the
	// first synthesize URL shape. If the error body still contains a download URL,
	// honor it before surfacing the failure.
	if downloadURL := parseTTSDownloadURL(blob); downloadURL != "" {
		return getTTSBlobFromURL(client, account,downloadURL)
	}
	return nil, status, err
}

func RemoveConversation(client httpclient.AuroraHttpClient, account *accounts.Account, id string, proxy string) {
	if proxy != "" {
		client.SetProxy(proxy)
	}
	var url string
	if account.Type == accounts.TypeNoAuth {
		url = strings.Replace(BaseURL, "backend-api", "backend-anon", 1) + "/conversation/" + id
	} else {
		url = BaseURL + "/conversation/" + id
	}
	header := createBaseHeader()
	header.Set("Content-Type", "application/json")
	if account.Type != accounts.TypeNoAuth && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	if account.Type == accounts.TypeNoAuth {
		header.Set("Oai-Device-Id", account.Token)
	}
	if account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	setTeamAccountHeader(header, account)
	payload := bytes.NewBuffer([]byte(`{"is_visible":false}`))
	response, err := client.Request(http.MethodPatch, url, header, nil, payload)
	if err != nil {
		return
	}
	response.Body.Close()
}

var urlAttrMap = make(map[string]string)

type urlAttr struct {
	Url         string `json:"url"`
	Attribution string `json:"attribution"`
}

func getURLAttribution(client httpclient.AuroraHttpClient, account *accounts.Account, url string) string {
	requestURL := BaseURL + "/attributions"
	payload := bytes.NewBuffer([]byte(`{"urls":["` + url + `"]}`))
	header := createBaseHeader()
	if account != nil && account.PUID != "" {
		header.Set("Cookie", "_puid="+account.PUID+";")
	}
	header.Set("Content-Type", "application/json")
	if account != nil && account.Token != "" {
		header.Set("Authorization", "Bearer "+account.Token)
	}
	setTeamAccountHeader(header, account)
	response, err := client.Request(http.MethodPost, requestURL, header, nil, payload)
	if err != nil {
		return ""
	}
	defer response.Body.Close()
	var attr urlAttr
	err = json.NewDecoder(response.Body).Decode(&attr)
	if err != nil {
		return ""
	}
	return attr.Attribution
}
