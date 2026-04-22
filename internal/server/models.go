package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"chatgpt-codex-proxy/internal/middleware"
)

const modelCreatedTimestamp int64 = 1700000000

type modelListResponse struct {
	Object string          `json:"object"`
	Data   []modelResponse `json:"data"`
}

type modelResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (a *App) handleModels(c *gin.Context) {
	entries := a.modelCatalog().List()
	data := make([]modelResponse, 0, len(entries))
	for _, entry := range entries {
		data = append(data, modelObject(entry.ID))
	}
	c.JSON(http.StatusOK, modelListResponse{Object: "list", Data: data})
}

func (a *App) handleModelByID(c *gin.Context) {
	model, ok := a.modelCatalog().Get(c.Param("model_id"))
	if !ok {
		middleware.AbortJSON(c, http.StatusNotFound, middleware.OpenAIErrorPayload("Model '"+c.Param("model_id")+"' not found", "invalid_request_error", "model_not_found", "model"))
		return
	}
	c.JSON(http.StatusOK, modelObject(model.ID))
}

func modelObject(model string) modelResponse {
	return modelResponse{
		ID:      model,
		Object:  "model",
		Created: modelCreatedTimestamp,
		OwnedBy: "openai",
	}
}
