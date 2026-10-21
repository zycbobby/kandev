package client

import (
	"context"
	"encoding/json"

	"github.com/kandev/kandev/internal/task/models"
)

// SetCreationBases replaces the per-repo creation-base map on an agentctl
// instance. An empty map clears every tracker's creation base.
func (c *Client) SetCreationBases(ctx context.Context, bases map[string]models.CreationBase) error {
	body, err := json.Marshal(struct {
		CreationBases map[string]models.CreationBase `json:"creation_bases"`
	}{CreationBases: bases})
	if err != nil {
		return err
	}
	return c.postWorkspaceControlJSON(ctx, "creation-bases", "set creation bases", body)
}
