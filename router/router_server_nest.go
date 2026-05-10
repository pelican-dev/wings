package router

import (
	"context"
	"net/http"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gin-gonic/gin"

	"github.com/pelican-dev/wings/environment"
	"github.com/pelican-dev/wings/router/middleware"
	"github.com/pelican-dev/wings/server/nest"
)

// postServerNestCapture handles POST /api/servers/{uuid}/nest/capture. answers
// 202 immediately and runs the streaming capture in a goroutine, the panel
// learns the result via the callback url it provided. refuses with 409 when
// the runtime is not offline, walking a live volume reads inconsistent files
// mid write and os.RemoveAll under a running container would yank files out
// from underneath it.
func postServerNestCapture(c *gin.Context) {
	s := middleware.ExtractServer(c)
	if s == nil {
		return
	}

	if s.Environment.State() != environment.ProcessOfflineState {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "server must be offline before nest capture",
		})
		return
	}

	var req nest.CaptureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	volumePath := s.Filesystem().Path()
	logger := middleware.ExtractLogger(c)

	go func(logger *log.Entry) {
		if err := nest.Capture(context.Background(), volumePath, req.PresignedUrl, req.CallbackUrl); err != nil {
			logger.WithField("error", errors.WithStackIf(err)).Error("router: nest capture callback delivery failed")
		}
	}(logger)

	c.Status(http.StatusAccepted)
}

// postServerNestRestore handles POST /api/servers/{uuid}/nest/restore. the
// panel polls server.view and server.resources during the hydrating window
// and waits for the callback for the terminal state. same offline guard as
// capture, restore writes into a destination directory the runtime would
// otherwise have bind mounted.
func postServerNestRestore(c *gin.Context) {
	s := middleware.ExtractServer(c)
	if s == nil {
		return
	}

	if s.Environment.State() != environment.ProcessOfflineState {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{
			"error": "server must be offline before nest restore",
		})
		return
	}

	var req nest.RestoreRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	volumePath := s.Filesystem().Path()
	logger := middleware.ExtractLogger(c)

	go func(logger *log.Entry) {
		if err := nest.Restore(context.Background(), volumePath, req.PresignedUrl, req.ExpectedSha256, req.CallbackUrl); err != nil {
			logger.WithField("error", errors.WithStackIf(err)).Error("router: nest restore callback delivery failed")
		}
	}(logger)

	c.Status(http.StatusAccepted)
}
