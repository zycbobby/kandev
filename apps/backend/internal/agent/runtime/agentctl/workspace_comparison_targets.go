package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/kandev/kandev/internal/task/models"
)

// SetComparisonTargets replaces the agentctl instance's per-repository
// provider-qualified comparison target map. The backend calls this after a
// task attachment changes; agentctl materializes each target and keeps an
// explicit unavailable state when a target cannot be fetched.
func (c *Client) SetComparisonTargets(ctx context.Context, targets map[string]models.ComparisonTarget) error {
	body, err := json.Marshal(struct {
		ComparisonTargets map[string]models.ComparisonTarget `json:"comparison_targets"`
	}{ComparisonTargets: targets})
	if err != nil {
		return err
	}
	return c.postWorkspaceControlJSON(ctx, "comparison-targets", "set comparison targets", body)
}

// postWorkspaceControlJSON posts one internal workspace-control payload to
// this instance's agentctl server and treats any non-2xx response as an
// error. label names the operation in the returned error.
func (c *Client) postWorkspaceControlJSON(ctx context.Context, endpoint, label string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/workspace/"+endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := readResponseBody(resp)
		return fmt.Errorf("%s failed with status %d: %s", label, resp.StatusCode, string(respBody))
	}
	return nil
}
