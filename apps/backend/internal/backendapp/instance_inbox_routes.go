package backendapp

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/common/logger"
	"github.com/kandev/kandev/internal/instances"
	"github.com/kandev/kandev/internal/instances/remoteinbox"
	"go.uber.org/zap"
)

// remoteInboxForwarder is the fan-out surface the instance inbox routes
// consume. *remoteinbox.Client satisfies it; tests substitute fakes so route
// behavior is pinned without the network.
type remoteInboxForwarder interface {
	ReadInbox(ctx context.Context, limit int) remoteinbox.ReadResult
	ReadHidden(ctx context.Context, limit int) remoteinbox.ReadResult
	ReadNeedsYou(ctx context.Context, limit int) remoteinbox.NeedsYouReadResult
	ReadNeedsYouHidden(ctx context.Context, limit int) remoteinbox.NeedsYouReadResult
	Respond(ctx context.Context, pendingID string, body []byte) (int, []byte, error)
	UpsertSidecar(ctx context.Context, pendingID, state, duration string) (int, error)
	DeleteSidecar(ctx context.Context, pendingID string) (int, error)
}

const (
	defaultInstanceInboxLimit = 50
	maxInstanceInboxLimit     = 200
	errRemoteUnreachable      = "remote_unreachable"
	maxRespondBodyBytes       = 1 << 20

	// errInstanceNotFoundMessage is the shared foreign/missing instance 404
	// body across the registry routes and the inbox forward routes.
	errInstanceNotFoundMessage = "instance not found"
	// instanceNameKey is the 502 body's instance label; logs use it too.
	instanceNameKey = "instance"
)

// instancePageAggregate is the aggregate block every composed instance page
// envelope carries. Embedded (not named) so the marshaled wire stays flat.
type instancePageAggregate struct {
	InstanceID       string  `json:"instance_id"`
	InstanceName     string  `json:"instance_name"`
	Status           string  `json:"status"`
	DegradedReason   string  `json:"degraded_reason,omitempty"`
	Count            int     `json:"count"`
	HiddenCount      int     `json:"hidden_count"`
	Total            int     `json:"total,omitempty"`
	NextSnoozeExpiry *string `json:"next_snooze_expiry"`
	HasMore          bool    `json:"has_more"`
}

// degradedInstancePageAggregates is a degraded read's aggregate block: the
// read request itself succeeded, so the degradation is instance-scoped state
// the Inbox renders and every aggregate is zeroed.
func degradedInstancePageAggregates(inst instances.Instance, reason string) instancePageAggregate {
	return instancePageAggregate{
		InstanceID:     inst.ID,
		InstanceName:   inst.Name,
		Status:         "degraded",
		DegradedReason: reason,
	}
}

// okInstancePageAggregates is a successful read's aggregate block.
func okInstancePageAggregates(inst instances.Instance, count, hiddenCount, total int, expiry *time.Time, hasMore bool) instancePageAggregate {
	return instancePageAggregate{
		InstanceID:       inst.ID,
		InstanceName:     inst.Name,
		Status:           "ok",
		Count:            count,
		HiddenCount:      hiddenCount,
		Total:            total,
		NextSnoozeExpiry: formatInstanceInboxTime(expiry),
		HasMore:          hasMore,
	}
}

// instanceInboxPageEnvelope is the composed per-instance page both
// clarification read routes return. A degraded read is still 200: the read
// request itself succeeded and the degradation is instance-scoped state the
// Inbox renders.
type instanceInboxPageEnvelope struct {
	instancePageAggregate
	Bundles []remoteinbox.Bundle `json:"bundles"`
}

// instanceNeedsYouPageEnvelope is the composed per-instance page the merged
// needs-you forward routes return: the same aggregate shape as
// instanceInboxPageEnvelope with kind-tagged rows carrying the remote's
// questions and manual-step waiting rows. Same degradation contract.
type instanceNeedsYouPageEnvelope struct {
	instancePageAggregate
	Rows []remoteinbox.NeedsYouRow `json:"rows"`
}

type instanceInboxSidecarBody struct {
	State          string `json:"state"`
	SnoozeDuration string `json:"snooze_duration"`
}

type instanceInboxHandlers struct {
	svc          *instances.Service
	forwarderFor func(instances.Instance) remoteInboxForwarder
	log          *logger.Logger
}

// registerInstanceInboxRoutes mounts the cross-instance inbox surface under
// /api/v1/instances/:id. Gated on both the instance registry (assembled only
// under features.multiInstance) and features.needsYouInbox: with either flag
// off every route here stays a plain 404.
func registerInstanceInboxRoutes(p routeParams) {
	if p.services == nil || p.services.Instances == nil || p.services.InstanceProxies == nil {
		return
	}
	proxies := p.services.InstanceProxies
	registerInstanceInboxRoutesWithForwarder(p, func(inst instances.Instance) remoteInboxForwarder {
		return remoteinbox.New(inst, proxies.Credential(inst), nil)
	})
}

func registerInstanceInboxRoutesWithForwarder(p routeParams, forwarderFor func(instances.Instance) remoteInboxForwarder) {
	if p.services == nil || p.services.Instances == nil || !p.features.NeedsYouInbox {
		return
	}
	h := &instanceInboxHandlers{svc: p.services.Instances, forwarderFor: forwarderFor, log: p.log}
	api := p.router.Group("/api/v1/instances")
	api.GET("/:id/clarification-inbox", h.pageRoute(false))
	api.GET("/:id/clarification-inbox/hidden", h.pageRoute(true))
	api.GET("/:id/needs-you-inbox", h.needsYouPageRoute(false))
	api.GET("/:id/needs-you-inbox/hidden", h.needsYouPageRoute(true))
	api.POST("/:id/clarification/:pendingID/respond", h.respondRoute)
	api.PUT("/:id/clarification-inbox/sidecar/:pendingID", h.upsertSidecarRoute)
	api.DELETE("/:id/clarification-inbox/sidecar/:pendingID", h.deleteSidecarRoute)
}

// resolve loads the caller's enabled instance row and its forwarder. Foreign,
// missing, and disabled IDs answer one indistinguishable 404, matching the
// registry routes' convention.
func (h *instanceInboxHandlers) resolve(c *gin.Context) (*instances.Instance, remoteInboxForwarder, bool) {
	inst := visibleInstance(c.Request.Context(), h.svc, instanceCallerUserID(c), c.Param("id"))
	if inst == nil || !inst.Enabled {
		c.JSON(http.StatusNotFound, gin.H{errKey: errInstanceNotFoundMessage})
		return nil, nil, false
	}
	return inst, h.forwarderFor(*inst), true
}

func (h *instanceInboxHandlers) pageRoute(hidden bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst, forwarder, ok := h.resolve(c)
		if !ok {
			return
		}
		limit, ok := parseInstanceInboxLimit(c.Query("limit"))
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "limit must be a positive integer"})
			return
		}
		// One fan-out per request: a hidden-page read must not also pay for
		// the inbox read it discards (design bounds reads at 1+W per request).
		var result remoteinbox.ReadResult
		if hidden {
			result = forwarder.ReadHidden(c.Request.Context(), limit)
		} else {
			result = forwarder.ReadInbox(c.Request.Context(), limit)
		}
		h.respondPage(c, *inst, result)
	}
}

// needsYouPageRoute backs the merged needs-you forward reads: one fan-out to
// the remote's own aggregated needs-you endpoint per request, with the same
// limit rules and per-instance degraded envelope as the clarification reads.
func (h *instanceInboxHandlers) needsYouPageRoute(hidden bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		inst, forwarder, ok := h.resolve(c)
		if !ok {
			return
		}
		limit, ok := parseInstanceInboxLimit(c.Query("limit"))
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{errKey: "limit must be a positive integer"})
			return
		}
		var result remoteinbox.NeedsYouReadResult
		if hidden {
			result = forwarder.ReadNeedsYouHidden(c.Request.Context(), limit)
		} else {
			result = forwarder.ReadNeedsYou(c.Request.Context(), limit)
		}
		h.respondNeedsYouPage(c, *inst, result)
	}
}

func (h *instanceInboxHandlers) respondPage(c *gin.Context, inst instances.Instance, result remoteinbox.ReadResult) {
	envelope := instanceInboxPageEnvelope{Bundles: []remoteinbox.Bundle{}}
	if result.Page == nil {
		envelope.instancePageAggregate = degradedInstancePageAggregates(inst, string(result.Degraded))
		c.JSON(http.StatusOK, envelope)
		return
	}
	bundles := result.Page.Bundles
	if bundles == nil {
		bundles = []remoteinbox.Bundle{}
	}
	// Stamp the owning instance on every row here rather than trusting the
	// forwarder: every cross-instance identifier the Inbox holds carries
	// instance identity (spec, remote-rows scenario).
	for i := range bundles {
		bundles[i].InstanceID = inst.ID
	}
	envelope.instancePageAggregate = okInstancePageAggregates(
		inst, result.Page.Count, result.Page.HiddenCount, result.Page.Total,
		result.Page.NextSnoozeExpiry, result.Page.HasMore)
	envelope.Bundles = bundles
	c.JSON(http.StatusOK, envelope)
}

func (h *instanceInboxHandlers) respondNeedsYouPage(c *gin.Context, inst instances.Instance, result remoteinbox.NeedsYouReadResult) {
	envelope := instanceNeedsYouPageEnvelope{Rows: []remoteinbox.NeedsYouRow{}}
	if result.Page == nil {
		envelope.instancePageAggregate = degradedInstancePageAggregates(inst, string(result.Degraded))
		c.JSON(http.StatusOK, envelope)
		return
	}
	rows := result.Page.Rows
	if rows == nil {
		rows = []remoteinbox.NeedsYouRow{}
	}
	// Same instance-identity stamping as the clarification page: every
	// cross-instance row the Inbox holds carries instance identity.
	for i := range rows {
		rows[i].InstanceID = inst.ID
	}
	envelope.instancePageAggregate = okInstancePageAggregates(
		inst, result.Page.Count, result.Page.HiddenCount, result.Page.Total,
		result.Page.NextSnoozeExpiry, result.Page.HasMore)
	envelope.Rows = rows
	c.JSON(http.StatusOK, envelope)
}

func (h *instanceInboxHandlers) respondRoute(c *gin.Context) {
	inst, forwarder, ok := h.resolve(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxRespondBodyBytes))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid payload"})
		return
	}
	status, respBody, err := forwarder.Respond(c.Request.Context(), c.Param("pendingID"), body)
	if err != nil {
		h.warnInstance(inst.Name, "instance inbox respond forward failed")
		c.JSON(http.StatusBadGateway, gin.H{errKey: errRemoteUnreachable, instanceNameKey: inst.Name})
		return
	}
	respondForwarded(c, status, respBody)
}

func (h *instanceInboxHandlers) upsertSidecarRoute(c *gin.Context) {
	inst, forwarder, ok := h.resolve(c)
	if !ok {
		return
	}
	var body instanceInboxSidecarBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{errKey: "invalid payload"})
		return
	}
	status, err := forwarder.UpsertSidecar(c.Request.Context(), c.Param("pendingID"), body.State, body.SnoozeDuration)
	if err != nil {
		h.warnInstance(inst.Name, "instance inbox sidecar forward failed")
		c.JSON(http.StatusBadGateway, gin.H{errKey: errRemoteUnreachable, instanceNameKey: inst.Name})
		return
	}
	c.Status(status)
}

func (h *instanceInboxHandlers) deleteSidecarRoute(c *gin.Context) {
	inst, forwarder, ok := h.resolve(c)
	if !ok {
		return
	}
	status, err := forwarder.DeleteSidecar(c.Request.Context(), c.Param("pendingID"))
	if err != nil {
		h.warnInstance(inst.Name, "instance inbox sidecar forward failed")
		c.JSON(http.StatusBadGateway, gin.H{errKey: errRemoteUnreachable, instanceNameKey: inst.Name})
		return
	}
	c.Status(status)
}

// respondForwarded writes one forwarded respond outcome verbatim: the remote
// status and body bytes are the contract, so the local route adds nothing.
func respondForwarded(c *gin.Context, status int, body []byte) {
	if len(body) > 0 {
		c.Data(status, "application/json", body)
		return
	}
	c.Status(status)
}

// parseInstanceInboxLimit mirrors the local inbox limit rules: absent
// defaults to 50, non-numeric or <= 0 is rejected, > 200 clamps.
func parseInstanceInboxLimit(raw string) (int, bool) {
	if raw == "" {
		return defaultInstanceInboxLimit, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > maxInstanceInboxLimit {
		return maxInstanceInboxLimit, true
	}
	return n, true
}

func formatInstanceInboxTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// warnInstance logs one forwarding failure with the instance name only; the
// payload never carries credentials or pending identifiers.
func (h *instanceInboxHandlers) warnInstance(name, message string) {
	if h.log != nil {
		h.log.Warn(message, zap.String(instanceNameKey, name))
	}
}
