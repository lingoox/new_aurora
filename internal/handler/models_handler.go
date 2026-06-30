package handler

import "github.com/gin-gonic/gin"

type ModelsHandler struct{}

func NewModelsHandler() *ModelsHandler {
	return &ModelsHandler{}
}

func (h *ModelsHandler) ListModels(c *gin.Context) {
	c.JSON(200, gin.H{"message": "not implemented"})
}
