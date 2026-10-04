package client

import (
	"context"
	"net/http"
)

// WebhookEndpoint is a configured endpoint. The signing secret is returned in
// full only by CreateWebhookEndpoint; reads carry its last four characters.
type WebhookEndpoint struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Enabled     bool     `json:"enabled"`
	SecretHint  string   `json:"secretHint"`
	// DisabledReason is MANUAL or CONSECUTIVE_FAILURES while Enabled is false.
	DisabledReason *string `json:"disabledReason"`
	CreatedAt      string  `json:"createdAt"`
	UpdatedAt      string  `json:"updatedAt"`
	// Secret is set only on the create response.
	Secret string `json:"secret,omitempty"`
}

// CreateWebhookEndpointInput is the body of POST …/webhooks.
type CreateWebhookEndpointInput struct {
	Name        string   `json:"name"`
	Description *string  `json:"description,omitempty"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
}

// UpdateWebhookEndpointInput is the body of PATCH …/webhooks/{id}. Every
// field is sent: Description is a JSON null to clear it, which the API accepts
// here (unlike on secrets and variables).
type UpdateWebhookEndpointInput struct {
	Name        string   `json:"name"`
	Description *string  `json:"description"`
	URL         string   `json:"url"`
	Events      []string `json:"events"`
	Enabled     bool     `json:"enabled"`
}

func webhooksPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "webhooks"}, rest...)...)
}

// CreateWebhookEndpoint needs `webhooks:write`.
func (c *Client) CreateWebhookEndpoint(ctx context.Context, projectID string, in CreateWebhookEndpointInput) (*WebhookEndpoint, error) {
	var out WebhookEndpoint
	if err := c.Do(ctx, http.MethodPost, webhooksPath(projectID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetWebhookEndpoint needs `webhooks:read`. The route wraps the endpoint with
// its delivery stats, which the provider has no use for.
func (c *Client) GetWebhookEndpoint(ctx context.Context, projectID, endpointID string) (*WebhookEndpoint, error) {
	var out struct {
		Endpoint WebhookEndpoint `json:"endpoint"`
	}
	if err := c.Do(ctx, http.MethodGet, webhooksPath(projectID, endpointID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out.Endpoint, nil
}

// UpdateWebhookEndpoint needs `webhooks:write`. Setting Enabled on a paused
// endpoint is a resume: the API clears its failure streak too.
func (c *Client) UpdateWebhookEndpoint(ctx context.Context, projectID, endpointID string, in UpdateWebhookEndpointInput) (*WebhookEndpoint, error) {
	var out WebhookEndpoint
	if err := c.Do(ctx, http.MethodPatch, webhooksPath(projectID, endpointID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWebhookEndpoint needs `webhooks:write`.
func (c *Client) DeleteWebhookEndpoint(ctx context.Context, projectID, endpointID string) error {
	return c.Do(ctx, http.MethodDelete, webhooksPath(projectID, endpointID), nil, nil, nil)
}
