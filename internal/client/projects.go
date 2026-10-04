package client

import (
	"context"
	"net/http"
)

// Project is a project's overview. A project is the API's name for what the
// database calls an organization, and every other resource lives inside one.
type Project struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	BillingEmail *string `json:"billingEmail"`
	// BlockedAt is set while the project is suspended, which stops most writes.
	BlockedAt     *string `json:"blockedAt"`
	BlockedReason *string `json:"blockedReason"`
}

// GetProject needs `projects:read`.
func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	var out Project
	if err := c.Do(ctx, http.MethodGet, Path("v1", "projects", projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
