package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *App) handleModels(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data": []gin.H{
			{"id": "codex", "object": "model", "owned_by": "openai"},
			{"id": a.cfg.DefaultModel, "object": "model", "owned_by": "openai"},
			{"id": "gpt-5.2-codex", "object": "model", "owned_by": "openai"},
			{"id": "gpt-5.2-codex-low", "object": "model", "owned_by": "openai"},
			{"id": "gpt-5.2-codex-medium", "object": "model", "owned_by": "openai"},
			{"id": "gpt-5.2-codex-high", "object": "model", "owned_by": "openai"},
		},
	})
}
