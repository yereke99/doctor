package httpserver

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func NewServer(handle *gin.Engine) *http.Server {
	return &http.Server{
		Handler:      handle,
		Addr:         ":8080",
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second,
	}
}
