package handler

import (
	"aurora/internal/accounts"
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
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ChatHandler) ChatGPTConversation(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}
