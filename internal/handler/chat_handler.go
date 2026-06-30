package handler

import (
	"io"
	"time"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	"aurora/internal/tokens"
	chatgpt_types "aurora/internal/types/chatgpt"
	officialtypes "aurora/internal/types/official"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	accountPool *accounts.Pool
}

func NewChatHandler(pool *accounts.Pool) *ChatHandler {
	return &ChatHandler{accountPool: pool}
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
	var req officialtypes.ResponsesAPIRequest
	if err := c.BindJSON(&req); err != nil {
		respondError(c, 400, err)
		return
	}
	c.JSON(200, gin.H{"message": "not implemented"})
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
