package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/middleware"
	"chatgpt-codex-proxy/internal/openai"
)

func (a *App) handleModels(c *gin.Context) {
	entries := a.modelCatalog().List()
	data := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		data = append(data, modelObject(entry.ID))
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func (a *App) handleModelByID(c *gin.Context) {
	model, ok := a.modelCatalog().Get(c.Param("model_id"))
	if !ok {
		middleware.AbortJSON(c, http.StatusNotFound, middleware.OpenAIErrorPayload("Model '"+c.Param("model_id")+"' not found", "invalid_request_error", "model_not_found", "model"))
		return
	}
	c.JSON(http.StatusOK, modelObject(model.ID))
}

func modelObject(model string) gin.H {
	return gin.H{
		"id":       model,
		"object":   "model",
		"created":  openai.ModelCreatedTimestamp,
		"owned_by": "openai",
	}
}
