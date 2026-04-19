package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/openai"
)

func (a *App) handleModels(c *gin.Context) {
	data := make([]gin.H, 0, 8)
	for _, model := range openai.PublicModelList(a.cfg.DefaultModel) {
		data = append(data, gin.H{
			"id":       model,
			"object":   "model",
			"owned_by": "openai",
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}
