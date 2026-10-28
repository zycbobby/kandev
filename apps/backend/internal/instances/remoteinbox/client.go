package remoteinbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/kandev/kandev/internal/instances"
)

const (
	// readDeadline bounds one whole instance read (workspace discovery plus
	// every per-workspace page) so a hung remote cannot stall the caller.
	readDeadline = 10 * time.Second
	// writeDeadline bounds one forwarded write.
	writeDeadline = 10 * time.Second
	// maxPageBodyBytes bounds one decoded remote page; a real page is a few
	// hundred kilobytes at the default limit.
	maxPageBodyBytes = 32 << 20

	workspacesPath = "/api/v1/workspaces"
	inboxPath      = "/api/v1/clarification-inbox"
	hiddenPath     = "/api/v1/clarification-inbox/hidden"
)

// remoteWorkspace is the id-bearing subset of one remote workspace row.
type remoteWorkspace struct {
	ID string `json:"id"`
}

// remoteWorkspacesResponse is GET /api/v1/workspaces' response envelope.
type remoteWorkspacesResponse struct {
	Workspaces []remoteWorkspace `json:"workspaces"`
}

// remoteBundle is one row of a remote inbox page, decoded only in the fields
// aggregation needs; Messages stays raw.
type remoteBundle struct {
	PendingID    string          `json:"pending_id"`
	TaskID       string          `json:"task_id"`
	SessionID    string          `json:"session_id"`
	SessionState string          `json:"session_state"`
	TaskTitle    string          `json:"task_title"`
	CreatedAt    string          `json:"created_at"`
	Context      string          `json:"context"`
	Messages     json.RawMessage `json:"messages"`
	State        string          `json:"state"`
	SnoozeUntil  *string         `json:"snooze_until"`
}

// remoteInboxPage is one remote per-workspace page. NextCursor presence is
// that workspace's has_more signal.
type remoteInboxPage struct {
	Bundles          []remoteBundle `json:"bundles"`
	Count            int            `json:"count"`
	HiddenCount      int            `json:"hidden_count"`
	Total            int            `json:"total"`
	NextSnoozeExpiry *string        `json:"next_snooze_expiry"`
	NextCursor       string         `json:"next_cursor"`
}

// workspacePage pairs one fetched page with the workspace it came from.
type workspacePage struct {
	workspaceID string
	page        remoteInboxPage
}

type sidecarPutBody struct {
	State          string `json:"state"`
	SnoozeDuration string `json:"snooze_duration"`
}

// Client reads and forwards one remote instance's own clarification-inbox
// surface over its registered credential.
type Client struct {
	inst       instances.Instance
	credential func() (string, error)
	client     *http.Client
}

// New builds the per-instance client. A nil credential is treated as "no PAT"
// (auth-off remotes still dial) and a nil http.Client falls back to a
// default with the package's write deadline.
func New(inst instances.Instance, credential func() (string, error), client *http.Client) *Client {
	if credential == nil {
		credential = func() (string, error) { return "", nil }
	}
	if client == nil {
		client = &http.Client{Timeout: writeDeadline}
	}
	return &Client{inst: inst, credential: credential, client: client}
}

// ReadInbox aggregates the remote's answerable bundles across every
// credential-visible workspace into one instance page.
func (c *Client) ReadInbox(ctx context.Context, limit int) ReadResult {
	return c.read(ctx, inboxPath, limit)
}

// ReadHidden aggregates the remote's hidden-bundle enumeration the same way;
// rows additionally carry sidecar state and the page carries the instance-
// wide total.
func (c *Client) ReadHidden(ctx context.Context, limit int) ReadResult {
	return c.read(ctx, hiddenPath, limit)
}

func (c *Client) read(ctx context.Context, path string, limit int) ReadResult {
	ctx, cancel := context.WithTimeout(ctx, readDeadline)
	defer cancel()
	token, err := c.credential()
	if err != nil {
		return ReadResult{Degraded: ReasonUnauthorized}
	}
	workspaces, reason := c.fetchWorkspaces(ctx, token)
	if reason != "" {
		return ReadResult{Degraded: reason}
	}
	pages := make([]workspacePage, 0, len(workspaces))
	for _, workspace := range workspaces {
		page, reason := c.fetchPage(ctx, token, path, workspace.ID, limit)
		if reason != "" {
			return ReadResult{Degraded: reason}
		}
		pages = append(pages, page)
	}
	return ReadResult{Page: aggregatePages(c.inst.ID, pages, limit)}
}

// Respond forwards one answer/rejection submission to the remote's own
// respond endpoint and passes the remote status and body back verbatim; the
// returned error is non-nil only for transport failures.
func (c *Client) Respond(ctx context.Context, pendingID string, body []byte) (int, []byte, error) {
	path, err := url.JoinPath("/api/v1/clarification", pendingID, "respond")
	if err != nil {
		return 0, nil, err
	}
	return c.forward(ctx, http.MethodPost, path, body)
}

// UpsertSidecar forwards a dismiss/snooze write and returns the remote status.
func (c *Client) UpsertSidecar(ctx context.Context, pendingID, state, duration string) (int, error) {
	body, err := json.Marshal(sidecarPutBody{State: state, SnoozeDuration: duration})
	if err != nil {
		return 0, err
	}
	path, err := url.JoinPath(inboxPath, "sidecar", pendingID)
	if err != nil {
		return 0, err
	}
	status, _, err := c.forward(ctx, http.MethodPut, path, body)
	return status, err
}

// DeleteSidecar forwards a restore (sidecar delete) and returns the remote
// status.
func (c *Client) DeleteSidecar(ctx context.Context, pendingID string) (int, error) {
	path, err := url.JoinPath(inboxPath, "sidecar", pendingID)
	if err != nil {
		return 0, err
	}
	status, _, err := c.forward(ctx, http.MethodDelete, path, nil)
	return status, err
}

// forward performs one credentialed request against the remote, passing the
// remote status and raw body back verbatim.
func (c *Client) forward(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, writeDeadline)
	defer cancel()
	token, err := c.credential()
	if err != nil {
		return 0, nil, err
	}
	target, err := c.target(path, nil)
	if err != nil {
		return 0, nil, err
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	authorize(req.Header, token)
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBodyBytes))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, payload, nil
}

func (c *Client) fetchWorkspaces(ctx context.Context, token string) ([]remoteWorkspace, Reason) {
	target, err := c.target(workspacesPath, nil)
	if err != nil {
		return nil, ReasonUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, ReasonError
	}
	authorize(req.Header, token)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, ReasonUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		// A 404 here is not an inbox-disabled remote; the workspaces list
		// predates the inbox surface, so any non-200 is a generic error.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, ReasonUnauthorized
		}
		return nil, ReasonError
	}
	var decoded remoteWorkspacesResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPageBodyBytes)).Decode(&decoded); err != nil {
		return nil, ReasonError
	}
	return decoded.Workspaces, ""
}

func (c *Client) fetchPage(ctx context.Context, token, path, workspaceID string, limit int) (workspacePage, Reason) {
	query := url.Values{}
	query.Set("workspace_id", workspaceID)
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	target, err := c.target(path, query)
	if err != nil {
		return workspacePage{}, ReasonUnreachable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return workspacePage{}, ReasonError
	}
	authorize(req.Header, token)
	resp, err := c.client.Do(req)
	if err != nil {
		return workspacePage{}, ReasonUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	if reason := pageStatusReason(resp.StatusCode); reason != "" {
		return workspacePage{}, reason
	}
	var page remoteInboxPage
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPageBodyBytes)).Decode(&page); err != nil {
		return workspacePage{}, ReasonError
	}
	return workspacePage{workspaceID: workspaceID, page: page}, ""
}

// pageStatusReason maps one per-workspace page status to the degradation
// closed set; an empty return means the status was fine.
func pageStatusReason(status int) Reason {
	switch status {
	case http.StatusOK:
		return ""
	case http.StatusUnauthorized, http.StatusForbidden:
		return ReasonUnauthorized
	case http.StatusNotFound:
		return ReasonInboxDisabled
	default:
		return ReasonError
	}
}

func authorize(header http.Header, token string) {
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
}

// target builds one request URL from the registered base origin, replacing
// its path wholesale the way the relay and proxy dialers do.
func (c *Client) target(path string, query url.Values) (string, error) {
	u, err := url.Parse(c.inst.BaseURL)
	if err != nil {
		return "", err
	}
	u.Path = path
	u.RawQuery = query.Encode()
	return u.String(), nil
}
