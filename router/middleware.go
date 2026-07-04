package router

import (
	"net/http"
	"sync"
	"time"
	"github.com/gin-gonic/gin"

	"github.com/pelican-dev/wings/router/middleware"
	"github.com/pelican-dev/wings/server"
)

// ExtractServer returns the server instance from the gin context. If there is
// no server set in the context (e.g. calling from a controller not protected
// by ServerExists) this function will panic.
//
// This function is deprecated. Use middleware.ExtractServer.
func ExtractServer(c *gin.Context) *server.Server {
	return middleware.ExtractServer(c)
}

// RateLimit returns a Gin middleware that limits each client
// to a maximum number of requests within a fixed time window.
// If the limit is exceeded, the request is rejected with HTTP 429 (Too Many Requests).
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	var mu sync.Mutex
	clients := make(map[string][]time.Time)

	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()

		mu.Lock()
		defer mu.Unlock()

		times := clients[ip]

		// remove old requests
		valid := times[:0]
		for _, t := range times {
			if now.Sub(t) < window {
				valid = append(valid, t)
			}
		}

		if len(valid) >= limit {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}

		valid = append(valid, now)
		clients[ip] = valid

		c.Next()
	}
}