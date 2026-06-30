package handler

import (
	"aurora/internal/accounts"
	"github.com/gin-gonic/gin"
)

type AudioHandler struct {
	accountPool *accounts.Pool
}

func NewAudioHandler(pool *accounts.Pool) *AudioHandler {
	return &AudioHandler{accountPool: pool}
}

func (h *AudioHandler) TTS(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *AudioHandler) Transcriptions(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *AudioHandler) Translations(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}
