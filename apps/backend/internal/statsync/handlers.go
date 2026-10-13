package statsync

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/common/logger"
)

// maxIngestBodyBytes bounds one ingest payload. A nightly delta of raw rows
// without message content stays far below this; the bound only exists so a
// runaway sender cannot exhaust memory.
const maxIngestBodyBytes = 32 << 20

// responseErrorKey is the JSON field every error response uses.
const responseErrorKey = "error"

// RegisterRoutes mounts the ingest endpoint. Callers register nothing when
// the feature is disabled (no shared secret configured), so an unconfigured
// instance has no route to probe.
func RegisterRoutes(router *gin.Engine, svc *Service, log *logger.Logger) {
	h := &Handler{svc: svc, log: log}
	router.POST("/api/v1/statsync/ingest", h.Handle)
}

// Handler serves the versioned ingest endpoint. It holds no business logic:
// authentication, validation, identity resolution, and the write path all
// belong to the service.
type Handler struct {
	svc *Service
	log *logger.Logger
}

// Handle processes one ingest POST.
func (h *Handler) Handle(c *gin.Context) {
	if err := h.svc.Authorize(c.GetHeader(HeaderName)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{responseErrorKey: "invalid or missing statsync secret"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxIngestBodyBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{responseErrorKey: "failed to read body"})
		return
	}
	if len(body) > maxIngestBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{responseErrorKey: "payload too large"})
		return
	}
	var env Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{responseErrorKey: "payload is not valid JSON"})
		return
	}

	result, err := h.svc.Ingest(c.Request.Context(), &env)
	switch {
	case err == nil:
	case isPayloadError(err):
		c.JSON(http.StatusBadRequest, gin.H{responseErrorKey: err.Error()})
		return
	case errors.Is(err, ErrUnmappedSource):
		c.JSON(http.StatusForbidden, gin.H{responseErrorKey: err.Error()})
		return
	default:
		h.log.Error("statsync ingest failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{responseErrorKey: "ingest failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"upserts": result.Upserts, "deleted": result.Deleted})
}

// isPayloadError reports whether err is a contract violation the sender can
// fix, mapped to 400 rather than 500.
func isPayloadError(err error) bool {
	return errors.Is(err, ErrUnsupportedSchemaVersion) ||
		errors.Is(err, ErrMissingSourceInstance) ||
		errors.Is(err, ErrInvalidMode) ||
		errors.Is(err, ErrMissingInventory) ||
		errors.Is(err, ErrInvalidRow)
}
