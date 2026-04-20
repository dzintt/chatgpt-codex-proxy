package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAPIKeyAuthentication(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		prepare    func(*http.Request)
		wantStatus int
	}{
		{
			name: "accepts X-API-Key header",
			prepare: func(request *http.Request) {
				request.Header.Set("X-API-Key", "secret")
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "accepts bearer authorization header",
			prepare: func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer secret")
			},
			wantStatus: http.StatusOK,
		},
		{
			name:       "rejects missing key",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			recorder := httptest.NewRecorder()
			engine := gin.New()
			engine.Use(APIKey("secret"))
			engine.GET("/private", func(c *gin.Context) {
				c.Status(http.StatusOK)
			})

			request := httptest.NewRequest(http.MethodGet, "/private", nil)
			if tc.prepare != nil {
				tc.prepare(request)
			}
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, tc.wantStatus)
			}
		})
	}
}
