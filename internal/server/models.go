package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/openai"
)

func (a *App) handleModels(c *gin.Context) {
	data := make([]gin.H, 0, 8)
	for _, model := range openai.PublicModelList(a.cfg.DefaultModel) {
		data = append(data, modelObject(model))
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func (a *App) handleModelByID(c *gin.Context) {
	modelID, ok := openai.ResolvePublicModel(c.Param("model_id"), a.cfg.DefaultModel)
	if !ok {
		c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"message": "Model '" + c.Param("model_id") + "' not found",
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		})
		return
	}
	c.JSON(http.StatusOK, modelObject(modelID))
}

func modelObject(model string) gin.H {
	return gin.H{
		"id":       model,
		"object":   "model",
		"created":  openai.ModelCreatedTimestamp,
		"owned_by": "openai",
	}
}
