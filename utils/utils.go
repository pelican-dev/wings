package utils

import (
	"github.com/apex/log"
	"io"
)

func CloseResponseBodyWithErrorHandling(body io.ReadCloser) {
	err := body.Close()
	if err != nil {
		log.WithError(err).Error("failed to close response body")
	}
}
