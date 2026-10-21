package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/kandev/kandev/internal/task/models"
)

// SetCreationBasesRequest replaces the per-repo creation-base map. Keys are
// workspace repository subpaths; the empty key addresses the root /
// single-repository tracker. An empty (or missing) map clears every tracker's
// creation base, restoring the origin-first default.
type SetCreationBasesRequest struct {
	CreationBases map[string]models.CreationBase `json:"creation_bases"`
}

// errInvalidJSONBody is the shared malformed-body rejection message.
const errInvalidJSONBody = "invalid JSON body"

func (s *Server) handleSetCreationBases(c *gin.Context) {
	var req SetCreationBasesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{errKey: errInvalidJSONBody})
		return
	}
	for repositoryName, base := range req.CreationBases {
		if !validComparisonTargetRepositoryKey(repositoryName) {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid creation base repository key"})
			return
		}
		if err := base.Validate(); err != nil {
			s.logger.Warn("creation base request rejected", zap.String("repository", repositoryName), zap.Error(err))
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid creation base"})
			return
		}
	}
	s.procMgr.UpdateCreationBases(c.Request.Context(), req.CreationBases)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
