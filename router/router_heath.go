package router

import (
	"net/http"
	"github.com/gin-gonic/gin"
)

func getHealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
	})
}
