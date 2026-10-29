package backendapp

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/auth/authn"
	"github.com/kandev/kandev/internal/instances"
	userstore "github.com/kandev/kandev/internal/user/store"
	"github.com/kandev/kandev/internal/webapp"
)

// registerInstanceRoutes mounts the remote-instance registry REST group on
// /api/v1/instances. The group exists only when features.multiInstance is on
// (the instance service is assembled by initInstanceProxies under the flag);
// with the flag off every route here is a plain 404. Caller scoping follows the
// settings convention: the global auth middleware guards the group and the
// owner is resolved per request, with the synthetic admin owning everything
// while auth is disabled.
func registerInstanceRoutes(p routeParams) {
	if p.services == nil || p.services.Instances == nil {
		return
	}
	svc := p.services.Instances
	api := p.router.Group("/api/v1/instances")
	api.GET("", listInstancesRoute(svc))
	api.POST("", createInstanceRoute(svc))
	api.PATCH("/:id", updateInstanceRoute(svc))
	api.DELETE("/:id", deleteInstanceRoute(svc))
	api.GET("/:id/health", instanceHealthRoute(svc))
	api.POST("/:id/select", selectInstanceRoute(svc, p.services.InstanceProxies))
}

// instanceCallerUserID resolves the owner scope for the instance routes: the
// authenticated caller, or the synthetic admin (DefaultUserID) when auth is
// disabled.
func instanceCallerUserID(c *gin.Context) string {
	identity, ok := authn.FromGin(c)
	if !ok || identity.Synthetic || identity.UserID == "" {
		return userstore.DefaultUserID
	}
	return identity.UserID
}

// instanceAPI is the REST shape of one registered remote instance. It omits
// the internal owner column and the proxy port: the shell injects the port
// server-side and the browser never needs it.
type instanceAPI struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	BaseURL            string    `json:"base_url"`
	CredentialSecretID string    `json:"credential_secret_id,omitempty"`
	Enabled            bool      `json:"enabled"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// instanceHealthAPI is the REST shape of one instance's last health report.
// A zero LastCheckedAt means the poller has not run yet.
type instanceHealthAPI struct {
	Reachable     bool      `json:"reachable"`
	Version       string    `json:"version,omitempty"`
	Error         string    `json:"error,omitempty"`
	Supported     bool      `json:"supported"`
	LastCheckedAt time.Time `json:"lastCheckedAt"`
}

func instanceToAPI(item *instances.Instance) instanceAPI {
	return instanceAPI{
		ID:                 item.ID,
		Name:               item.Name,
		BaseURL:            item.BaseURL,
		CredentialSecretID: item.CredentialSecretID,
		Enabled:            item.Enabled,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
	}
}

// instanceWriteResult maps instance service errors onto the REST surface. A
// foreign or missing ID is one indistinguishable 404; failures map to 500.
func instanceWriteResult(c *gin.Context, err error) {
	if errors.Is(err, instances.ErrInstanceNotFound) {
		c.JSON(http.StatusNotFound, gin.H{errKey: errInstanceNotFoundMessage})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{errKey: "instance operation failed"})
	}
}

func listInstancesRoute(svc *instances.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := svc.ListInstances(c.Request.Context(), instanceCallerUserID(c))
		if err != nil {
			instanceWriteResult(c, err)
			return
		}
		out := make([]instanceAPI, 0, len(items))
		for _, item := range items {
			out = append(out, instanceToAPI(item))
		}
		c.JSON(http.StatusOK, out)
	}
}

type instanceCreateRequest struct {
	Name               string `json:"name"`
	BaseURL            string `json:"base_url"`
	CredentialSecretID string `json:"credential_secret_id"`
	Enabled            *bool  `json:"enabled"`
}

func createInstanceRoute(svc *instances.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body instanceCreateRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid instance payload"})
			return
		}
		if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.BaseURL) == "" {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "instance name and base URL are required"})
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		item, err := svc.CreateInstance(
			c.Request.Context(), instanceCallerUserID(c),
			body.Name, body.BaseURL, body.CredentialSecretID, enabled)
		if err != nil {
			instanceWriteResult(c, err)
			return
		}
		c.JSON(http.StatusCreated, instanceToAPI(item))
	}
}

type instanceUpdateRequest struct {
	Name               *string `json:"name"`
	BaseURL            *string `json:"base_url"`
	CredentialSecretID *string `json:"credential_secret_id"`
	Enabled            *bool   `json:"enabled"`
}

func updateInstanceRoute(svc *instances.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body instanceUpdateRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid instance payload"})
			return
		}
		if (body.Name != nil && strings.TrimSpace(*body.Name) == "") ||
			(body.BaseURL != nil && strings.TrimSpace(*body.BaseURL) == "") {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "instance name and base URL are required"})
			return
		}
		item, err := svc.UpdateInstance(
			c.Request.Context(), instanceCallerUserID(c), c.Param("id"), instances.InstanceUpdate{
				Name:               body.Name,
				BaseURL:            body.BaseURL,
				CredentialSecretID: body.CredentialSecretID,
				Enabled:            body.Enabled,
			})
		if err != nil {
			instanceWriteResult(c, err)
			return
		}
		c.JSON(http.StatusOK, instanceToAPI(item))
	}
}

func deleteInstanceRoute(svc *instances.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := svc.DeleteInstance(c.Request.Context(), instanceCallerUserID(c), c.Param("id")); err != nil {
			instanceWriteResult(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func instanceHealthRoute(svc *instances.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		report, err := svc.GetHealth(c.Request.Context(), instanceCallerUserID(c), c.Param("id"))
		if err != nil {
			instanceWriteResult(c, err)
			return
		}
		c.JSON(http.StatusOK, instanceHealthAPI{
			Reachable:     report.Reachable,
			Version:       report.Version,
			Error:         report.Error,
			Supported:     report.Supported,
			LastCheckedAt: report.LastCheckedAt,
		})
	}
}

// visibleInstance returns the caller's instance record for id, or nil when id
// names no instance visible to that owner. The service owns the registry and
// its per-owner reads, so foreign IDs read exactly like missing ones.
func visibleInstance(ctx context.Context, svc *instances.Service, owner, id string) *instances.Instance {
	items, err := svc.ListInstances(ctx, owner)
	if err != nil {
		return nil
	}
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	return nil
}

// localInstanceID is the instance picker's sentinel for the serving instance.
// Registry ids are UUIDs, so the sentinel can never name a registry row; the
// select route treats it as "clear the selection" rather than a lookup.
const localInstanceID = "local"

// selectInstanceRoute implements the switch contract: it verifies the instance
// is visible to the caller, records the selection in the kandev_instance
// cookie the shell re-reads on the next full-page load, mints the bridge
// capability cookie through the live proxy server, and returns the reload
// target. The bridge mint happens only after both checks above it. The local
// sentinel id clears the selection instead: no registry lookup, no bridge.
func selectInstanceRoute(svc *instances.Service, proxies *ProxyManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if id == localInstanceID {
			clearInstanceSelection(c.Writer, c.Request)
			c.JSON(http.StatusOK, gin.H{"href": "/"})
			return
		}
		if visibleInstance(c.Request.Context(), svc, instanceCallerUserID(c), id) == nil {
			c.JSON(http.StatusNotFound, gin.H{errKey: errInstanceNotFoundMessage})
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     webapp.InstanceCookieName,
			Value:    id,
			Path:     "/",
			HttpOnly: true,
			Secure:   c.Request.TLS != nil || strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https"),
			SameSite: http.SameSiteLaxMode,
		})
		if proxies != nil {
			if server := proxies.Server(id); server != nil {
				server.SetCapabilityCookie(c.Writer, c.Request)
			}
		}
		c.JSON(http.StatusOK, gin.H{"href": "/"})
	}
}

// clearInstanceSelection deletes the selection cookie with the same
// attributes it was set with, so a query-less reload boots the local
// instance. MaxAge<0 serializes as Max-Age=0.
func clearInstanceSelection(w http.ResponseWriter, req *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     webapp.InstanceCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
