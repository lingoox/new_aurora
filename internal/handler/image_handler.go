package handler

import (
	"aurora/internal/accounts"
	"github.com/gin-gonic/gin"
)

type ImageHandler struct {
	accountPool *accounts.Pool
}

func NewImageHandler(pool *accounts.Pool) *ImageHandler {
	return &ImageHandler{accountPool: pool}
}

func (h *ImageHandler) Generations(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ImageHandler) Edits(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}

func (h *ImageHandler) Variations(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}
