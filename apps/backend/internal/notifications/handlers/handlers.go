package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/notifications/controller"
	"github.com/kandev/kandev/internal/notifications/dto"
	"github.com/kandev/kandev/internal/notifications/service"
	"go.uber.org/zap"
)

type Handlers struct {
	controller *controller.Controller
	logger     *logger.Logger
}

func RegisterRoutes(router *gin.Engine, ctrl *controller.Controller, log *logger.Logger) {
	h := &Handlers{
		controller: ctrl,
		logger:     log.WithFields(zap.String("component", "notification-handlers")),
	}
	api := router.Group("/api/v1")
	api.GET("/notification-providers", h.httpListProviders)
	api.POST("/notification-providers", h.httpCreateProvider)
	api.PATCH("/notification-providers/:id", h.httpUpdateProvider)
	api.DELETE("/notification-providers/:id", h.httpDeleteProvider)
	api.POST("/notification-providers/:id/test", h.httpTestProvider)
	api.GET("/notifications/occurrences", h.httpListOccurrences)
}

func (h *Handlers) httpListProviders(c *gin.Context) {
	resp, err := h.controller.ListProviders(c.Request.Context())
	if err != nil {
		h.logger.Error("failed to list notification providers", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list notification providers"})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) httpCreateProvider(c *gin.Context) {
	var body dto.UpsertProviderRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	resp, err := h.controller.CreateProvider(c.Request.Context(), body)
	if err != nil {
		h.logger.Error("failed to create notification provider", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) httpUpdateProvider(c *gin.Context) {
	providerID := c.Param("id")
	var body dto.UpdateProviderRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload"})
		return
	}
	resp, err := h.controller.UpdateProvider(c.Request.Context(), providerID, body)
	if err != nil {
		// A provider owned by another user reaches here as ErrProviderNotFound,
		// so it is answered exactly like an ID that does not exist.
		if errors.Is(err, service.ErrProviderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
		h.logger.Error("failed to update notification provider", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handlers) httpTestProvider(c *gin.Context) {
	providerID := c.Param("id")
	if err := h.controller.TestProvider(c.Request.Context(), providerID); err != nil {
		if errors.Is(err, service.ErrProviderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
		h.logger.Error("test notification failed", zap.String("provider_id", providerID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to send test notification"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *Handlers) httpDeleteProvider(c *gin.Context) {
	providerID := c.Param("id")
	if err := h.controller.DeleteProvider(c.Request.Context(), providerID); err != nil {
		if errors.Is(err, service.ErrProviderNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "provider not found"})
			return
		}
		h.logger.Error("failed to delete notification provider", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete notification provider"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// httpListOccurrences serves the occurrence reconciliation feed: dispatches
// newer than the RFC3339 since cursor, oldest first, with the limit clamped
// to the contract (default 100, ceiling 200) by the service.
func (h *Handlers) httpListOccurrences(c *gin.Context) {
	var since time.Time
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid since parameter"})
			return
		}
		since = parsed
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
			return
		}
		limit = parsed
	}
	occurrences, err := h.controller.ListOccurrences(c.Request.Context(), since, limit)
	if err != nil {
		h.logger.Error("failed to list notification occurrences", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list notification occurrences"})
		return
	}
	c.JSON(http.StatusOK, occurrences)
}
