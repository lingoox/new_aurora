package handler

import (
	"io"
	"os"
	"time"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	"aurora/internal/tokens"
	chatgpt_types "aurora/internal/types/chatgpt"
	officialtypes "aurora/internal/types/official"
	"aurora/util"
	chatgptrequestconverter "aurora/conversion/requests/chatgpt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type ChatHandler struct {
	accountPool *accounts.Pool
	sessions    *SessionManager
}

func NewChatHandler(pool *accounts.Pool) *ChatHandler {
	return &ChatHandler{
		accountPool: pool,
		sessions:    NewSessionManager(),
	}
}

func (h *ChatHandler) Nightmare(c *gin.Context) {
	var req officialtypes.APIRequest
	if err := c.BindJSON(&req); err != nil {
		respondError(c, 400, err)
		return
	}
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ChatHandler) Responses(c *gin.Context) {
	var responsesRequest officialtypes.ResponsesAPIRequest
	err := c.BindJSON(&responsesRequest)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Request must be proper JSON",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    err.Error(),
		}})
		return
	}

	original_request, err := responsesRequest.ToAPIRequest()
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "invalid_request_error",
			"param":   "input",
			"code":    "invalid_request_error",
		}})
		return
	}

	account, err := resolveAccount(c, h.accountPool, original_requestHasFiles(original_request))
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "authorization_error",
			"param":   "Authorization",
			"code":    400,
		}})
		return
	}
	if account == nil {
		c.JSON(400, gin.H{"error": "Not Account Found."})
		c.Abort()
		return
	}

	proxyUrl := account.Proxy
	input_tokens := 0
	for _, message := range original_request.Messages {
		input_tokens += util.CountToken(message.Text())
	}

	uid := uuid.NewString()
	secret := createTempSecret(account)
	client := setupClientWithProxy(proxyUrl)

	translated_request := chatgptrequestconverter.ConvertAPIRequest(original_request, secret, proxyUrl, client)

	// 按 conversationID 复用 ChatClientState，保持 DeviceID/SessionID 一致
	var clientState *chatgpt.ChatClientState
	if translated_request.ConversationID != "" {
		clientState = h.sessions.Get(translated_request.ConversationID)
	}
	if clientState == nil {
		clientState = chatgpt.NewChatClientState()
	}
	clientState.ConversationID = translated_request.ConversationID
	clientState.ParentMessageID = translated_request.ParentMessageID
	reqModel := original_request.Model
	if reqModel == "" {
		reqModel = "auto"
	}

	response, wsConn, _, status, err := conversationClientOrder(&client, secret, translated_request, proxyUrl, false, clientState)
	if err != nil {
		c.JSON(status, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "request_conversion_error",
			"param":   "model",
			"code":    "request_conversion_error",
		}})
		return
	}
	defer response.Body.Close()
	if chatgpt.Handle_request_error(c, response) {
		if wsConn != nil {
			wsConn.Close()
			wsConn = nil
		}
		return
	}

	var full_response string
	for i := maxContinueCount(); i > 0; i-- {
		var continue_info *chatgpt.ContinueInfo
		var response_part string
		result := chatgpt.HandlerDetailedWithOptions(c, response, client, secret, uid, translated_request, false, reqModel, chatgpt.HandlerDetailedOptions{
			Websocket:   wsConn,
			ClientState: clientState,
		})
		wsConn = nil
		response_part, continue_info = result.Text, result.Continue
		full_response += response_part
		parentMessageID := result.ParentMessageID
		if continue_info != nil {
			parentMessageID = continue_info.ParentID
		}
		clientState.NoteTurnResult(result.ConversationID, parentMessageID)
		if result.ConversationID != "" {
			h.sessions.Register(result.ConversationID, clientState)
		}
		if continue_info == nil {
			break
		}
		translated_request.Messages = nil
		translated_request.Action = "continue"
		translated_request.ConversationID = continue_info.ConversationID
		translated_request.ParentMessageID = continue_info.ParentID

		response, wsConn, _, status, err = conversationClientOrder(&client, secret, translated_request, proxyUrl, false, clientState)
		if err != nil {
			c.JSON(status, gin.H{"error": gin.H{
				"message": err.Error(),
				"type":    "request_conversion_error",
				"param":   "model",
				"code":    "request_conversion_error",
			}})
			return
		}
		defer response.Body.Close()
		if chatgpt.Handle_request_error(c, response) {
			if wsConn != nil {
				wsConn.Close()
				wsConn = nil
			}
			return
		}
	}
	if c.Writer.Status() != 200 {
		return
	}

	output_tokens := util.CountToken(full_response)
	responsesResponse := officialtypes.NewResponsesResponse(full_response, input_tokens, output_tokens, reqModel)
	if !responsesRequest.Stream || os.Getenv("STREAM_MODE") == "false" {
		c.JSON(200, responsesResponse)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.String(200, "event: response.created\ndata: "+officialtypes.ResponsesCreated(responsesResponse)+"\n\n")
	c.String(200, "event: response.output_text.delta\ndata: "+officialtypes.ResponsesTextDelta(full_response)+"\n\n")
	c.String(200, "event: response.completed\ndata: "+officialtypes.ResponsesCompleted(responsesResponse)+"\n\n")
	c.String(200, "data: [DONE]\n\n")
}

func (h *ChatHandler) Files(c *gin.Context) {
	account, err := resolveAccount(c, h.accountPool, true)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Files API requires a logged-in ChatGPT access token.",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    "missing_access_token",
		}})
		return
	}
	if account == nil || account.Token == "" {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Files API requires a logged-in ChatGPT access token.",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    "missing_access_token",
		}})
		return
	}

	formFile, err := c.FormFile("file")
	if err != nil {
		respondError(c, 400, err)
		return
	}
	file, err := formFile.Open()
	if err != nil {
		respondError(c, 400, err)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		respondError(c, 400, err)
		return
	}
	if len(data) == 0 {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Uploaded file is empty",
			"type":    "invalid_request_error",
			"param":   "file",
			"code":    "empty_file",
		}})
		return
	}

	contentType := formFile.Header.Get("Content-Type")

	client := bogdanfinn.NewStdClient()
	client.SetCookies("https://chatgpt.com", chatgpt.BasicCookies)

	// 临时桥接：chatgpt 函数仍使用 tokens.Secret
	secret := &tokens.Secret{
		Token:      account.Token,
		IsFree:     account.Type != accounts.TypePUID,
		PUID:       account.PUID,
		TeamUserID: account.TeamUserID,
	}

	uploaded, status, err := chatgpt.UploadFile(client, secret, account.Proxy, formFile.Filename, contentType, data)
	if err != nil {
		c.JSON(status, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "file_upload_error",
			"param":   "file",
			"code":    "file_upload_error",
		}})
		return
	}
	uploaded.CreatedAt = time.Now().Unix()
	chatgpt.RegisterUploadedFile(uploaded)
	c.JSON(200, uploaded)
}

func (h *ChatHandler) ChatGPTConversation(c *gin.Context) {
	var original_request chatgpt_types.ChatGPTRequest
	if err := c.BindJSON(&original_request); err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": "Request must be proper JSON",
			"type":    "invalid_request_error",
			"param":   nil,
			"code":    err.Error(),
		}})
		return
	}
	if len(original_request.Messages) > 0 && original_request.Messages[0].Author.Role == "" {
		original_request.Messages[0].Author.Role = "user"
	}

	account, err := resolveAccount(c, h.accountPool, false)
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{
			"message": err.Error(),
			"type":    "authorization_error",
			"param":   "Authorization",
			"code":    400,
		}})
		return
	}
	if account == nil || account.Token == "" {
		c.JSON(400, gin.H{"error": "Not Account Found."})
		return
	}

	// 临时桥接：chatgpt 函数仍使用 tokens.Secret
	secret := &tokens.Secret{
		Token:      account.Token,
		IsFree:     account.Type != accounts.TypePUID,
		PUID:       account.PUID,
		TeamUserID: account.TeamUserID,
	}

	client := bogdanfinn.NewStdClient()
	if account.Proxy != "" {
		client.SetProxy(account.Proxy)
	}
	turnStile, status, err := chatgpt.InitSentinel(client, secret, account.Proxy, 0)
	if err != nil {
		c.JSON(status, gin.H{
			"message": err.Error(),
			"type":    "InitTurnStile_request_error",
			"param":   err,
			"code":    status,
		})
		return
	}

	response, err := chatgpt.POSTconversation(client, original_request, secret, turnStile, account.Proxy)
	if err != nil {
		c.JSON(500, gin.H{"error": "error sending request"})
		return
	}
	defer response.Body.Close()

	if chatgpt.Handle_request_error(c, response) {
		return
	}

	c.Header("Content-Type", response.Header.Get("Content-Type"))
	if cacheControl := response.Header.Get("Cache-Control"); cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}

	if _, err := io.Copy(c.Writer, response.Body); err != nil {
		c.JSON(500, gin.H{"error": "Error sending response"})
	}
}
