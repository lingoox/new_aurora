package handler

import (
	"io"
	"time"

	"aurora/httpclient/bogdanfinn"
	"aurora/internal/accounts"
	"aurora/internal/chatgpt"
	"aurora/internal/tokens"
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
	c.JSON(200, gin.H{"message": "not implemented"})
}
