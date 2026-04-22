package middleware

import "github.com/gin-gonic/gin"

func AbortJSON(c *gin.Context, status int, payload any) {
	c.AbortWithStatusJSON(status, payload)
}

type OpenAIErrorResponse struct {
	Error OpenAIErrorBody `json:"error"`
}

type OpenAIErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
	Param   string `json:"param,omitempty"`
}

func OpenAIErrorPayload(message, typ, code, param string) OpenAIErrorResponse {
	return OpenAIErrorResponse{
		Error: OpenAIErrorBody{
			Message: message,
			Type:    typ,
			Code:    code,
			Param:   param,
		},
	}
}

type AdminErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func AdminErrorPayload(code, message string) AdminErrorResponse {
	return AdminErrorResponse{
		Error:   code,
		Message: message,
	}
}
