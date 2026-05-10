package router

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/pelican-dev/wings/router/middleware"
	"github.com/pelican-dev/wings/server/nest"
)

// postServerNestCapture handles POST /api/servers/{uuid}/nest/capture. answers
// 202 immediately and runs the streaming capture in a goroutine, the panel
// learns the result via the callback url it provided.
func postServerNestCapture(c *gin.Context) {
	s := middleware.ExtractServer(c)
	if s == nil {
		return
	}

	var req nest.CaptureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	volumePath := s.Filesystem().Path()

	go func() {
		_ = nest.Capture(context.Background(), volumePath, req.PresignedUrl, req.CallbackUrl)
	}()

	c.Status(http.StatusAccepted)
}

// postServerNestRestore handles POST /api/servers/{uuid}/nest/restore. same
// shape as capture, the panel polls server.view and server.resources during
// the hydrating window and waits for the callback for the terminal state.
func postServerNestRestore(c *gin.Context) {
	s := middleware.ExtractServer(c)
	if s == nil {
		return
	}

	var req nest.RestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	volumePath := s.Filesystem().Path()

	go func() {
		_ = nest.Restore(context.Background(), volumePath, req.PresignedUrl, req.ExpectedSha256, req.CallbackUrl)
	}()

	c.Status(http.StatusAccepted)
}
