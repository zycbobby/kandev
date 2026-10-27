package backendapp

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/instances"
	userstore "github.com/kandev/kandev/internal/user/store"
)

func remoteNotificationOwner(c *gin.Context) string {
	identity, ok := authn.IdentityFromContext(c.Request.Context())
	if !ok || identity.Synthetic || identity.UserID == "" {
		return userstore.DefaultUserID
	}
	return identity.UserID
}
func registerRemoteNotificationRoutes(router *gin.Engine, services *Services) {
	if services.InstanceRelays == nil {
		return
	}
	m := services.InstanceRelays
	api := router.Group("/api/v1/remote-notifications")
	api.GET("", m.listNotifications)
	api.POST("/:id/read", m.markRead)
	api.POST("/:id/dismiss", m.dismiss)
	api.POST("/read-all", m.readAll)
}
func (m *RelayManager) listNotifications(c *gin.Context) {
	var since time.Time
	if raw := c.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid since parameter"})
			return
		}
		since = parsed
	}
	limit := 100
	if raw := c.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid limit parameter"})
			return
		}
		limit = parsed
	}
	rows, err := m.store.ListOccurrences(c.Request.Context(), remoteNotificationOwner(c), since, limit)
	if err != nil {
		notificationWriteResult(c, err)
		return
	}
	c.JSON(http.StatusOK, rows)
}
func (m *RelayManager) markRead(c *gin.Context) {
	notificationWriteResult(c, m.store.MarkOccurrenceRead(c.Request.Context(), remoteNotificationOwner(c), c.Param("id")))
}
func (m *RelayManager) dismiss(c *gin.Context) {
	notificationWriteResult(c, m.store.MarkOccurrenceDismissed(c.Request.Context(), remoteNotificationOwner(c), c.Param("id")))
}
func (m *RelayManager) readAll(c *gin.Context) {
	notificationWriteResult(c, m.store.MarkAllOccurrencesRead(c.Request.Context(), remoteNotificationOwner(c)))
}
func notificationWriteResult(c *gin.Context, err error) {
	if errors.Is(err, instances.ErrInstanceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{errKey: "notification not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{errKey: "remote notification operation failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
