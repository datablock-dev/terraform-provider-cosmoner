package client

import (
	"context"
	"net/http"
)

// Secret is a secret's metadata. The value is absent on purpose: the API
// returns it only from the call that sets it, and no route decrypts one.
type Secret struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Environment string  `json:"environment"`
	// Version is bumped every time the value is replaced, from any client,
	// which is how the provider notices a value changed outside Terraform.
	Version   int64  `json:"version"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// CreateSecretInput is the body of POST …/secrets.
type CreateSecretInput struct {
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
	Environment string  `json:"environment,omitempty"`
}

// UpdateSecretInput is the body of PATCH …/secrets/{id}. The value is
// required by the API even when only the description changes.
type UpdateSecretInput struct {
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
}

func secretsPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "secrets"}, rest...)...)
}

// CreateSecret needs `secrets:write` and an owner or admin. The plaintext the
// API echoes back is discarded — the caller already has it.
func (c *Client) CreateSecret(ctx context.Context, projectID string, in CreateSecretInput) (*Secret, error) {
	var out Secret
	if err := c.Do(ctx, http.MethodPost, secretsPath(projectID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSecret needs `secrets:read`.
func (c *Client) GetSecret(ctx context.Context, projectID, secretID string) (*Secret, error) {
	var out Secret
	if err := c.Do(ctx, http.MethodGet, secretsPath(projectID, secretID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSecrets needs `secrets:read`.
func (c *Client) ListSecrets(ctx context.Context, projectID string) ([]Secret, error) {
	var out []Secret
	if err := c.Do(ctx, http.MethodGet, secretsPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSecret replaces the value and bumps the version.
func (c *Client) UpdateSecret(ctx context.Context, projectID, secretID string, in UpdateSecretInput) (*Secret, error) {
	var out Secret
	if err := c.Do(ctx, http.MethodPatch, secretsPath(projectID, secretID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteSecret answers 204.
func (c *Client) DeleteSecret(ctx context.Context, projectID, secretID string) error {
	return c.Do(ctx, http.MethodDelete, secretsPath(projectID, secretID), nil, nil, nil)
}
