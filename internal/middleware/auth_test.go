package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIKeyAcceptsXAPIKeyHeader(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.Use(APIKey("secret"))
	engine.GET("/private", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.Header.Set("X-API-Key", "secret")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestAPIKeyAcceptsBearerAuthorizationHeader(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.Use(APIKey("secret"))
	engine.GET("/private", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	request.Header.Set("Authorization", "Bearer secret")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
}

func TestAPIKeyRejectsMissingKey(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	engine := gin.New()
	engine.Use(APIKey("secret"))
	engine.GET("/private", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	request := httptest.NewRequest(http.MethodGet, "/private", nil)
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}
