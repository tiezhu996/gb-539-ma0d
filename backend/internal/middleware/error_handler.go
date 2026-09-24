package middleware

import (
	"errors"
	"github.com/gin-gonic/gin"
	"timber-kiln-drying-optimizer/backend/internal/service"
)

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if len(c.Errors) == 0 || c.Writer.Written() {
			return
		}
		err := c.Errors.Last().Err
		var pending *service.PendingTriageError
		if errors.As(err, &pending) {
			c.JSON(409, gin.H{"error": gin.H{"code": "pending_reading_triage", "message": err.Error(), "request_id": c.GetString("request_id"), "affected": pending.Readings}})
			return
		}
		status := 500
		code := "internal_error"
		if errors.Is(err, service.ErrValidation) {
			status = 422
			code = "validation_error"
		}
		if errors.Is(err, service.ErrConflict) {
			status = 409
			code = "conflict"
		}
		if errors.Is(err, service.ErrForbidden) {
			status = 403
			code = "forbidden"
		}
		if errors.Is(err, service.ErrNotFound) {
			status = 404
			code = "not_found"
		}
		c.JSON(status, gin.H{"error": gin.H{"code": code, "message": err.Error(), "request_id": c.GetString("request_id")}})
	}
}
