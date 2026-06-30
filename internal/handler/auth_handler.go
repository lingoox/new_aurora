package handler

import (
	"aurora/internal/accounts"
	officialtypes "aurora/internal/types/official"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	accountPool *accounts.Pool
}

func NewAuthHandler(pool *accounts.Pool) *AuthHandler {
	return &AuthHandler{accountPool: pool}
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req officialtypes.OpenAIRefreshToken
	if err := c.BindJSON(&req); err != nil {
		respondError(c, 400, err)
		return
	}
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *AuthHandler) Session(c *gin.Context) {
	var req officialtypes.OpenAISessionToken
	if err := c.BindJSON(&req); err != nil {
		respondError(c, 400, err)
		return
	}
	c.JSON(200, gin.H{"message": "not implemented"})
}
