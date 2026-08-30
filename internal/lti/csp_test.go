package lti

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFrameAncestorsMiddleware(t *testing.T) {
	t.Run("defaults to self for lti paths", func(t *testing.T) {
		r := gin.New()
		r.Use(FrameAncestorsMiddleware(&config.LTIConfig{FrameAncestors: "'self'"}))
		r.GET("/lti/launch", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/lti/launch", nil)
		r.ServeHTTP(w, req)
		require.Equal(t, "frame-ancestors 'self'", w.Header().Get("Content-Security-Policy"))
	})

	t.Run("uses configured origins", func(t *testing.T) {
		r := gin.New()
		r.Use(FrameAncestorsMiddleware(&config.LTIConfig{FrameAncestors: "https://platform.example.com"}))
		r.GET("/lti/launch", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/lti/launch", nil)
		r.ServeHTTP(w, req)
		require.Equal(t, "frame-ancestors https://platform.example.com", w.Header().Get("Content-Security-Policy"))
	})

	t.Run("leaves non-lti paths untouched", func(t *testing.T) {
		r := gin.New()
		r.Use(FrameAncestorsMiddleware(&config.LTIConfig{FrameAncestors: "'self'"}))
		r.GET("/api/v1/health", func(c *gin.Context) { c.Status(http.StatusOK) })

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		r.ServeHTTP(w, req)
		require.Empty(t, w.Header().Get("Content-Security-Policy"))
	})
}
