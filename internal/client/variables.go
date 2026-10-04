package client

import (
	"context"
	"net/http"
)

// Variable is non-sensitive project configuration, returned in full on every
// read — which is the whole difference between a variable and a secret.
type Variable struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Value       string  `json:"value"`
	Environment string  `json:"environment"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

// CreateVariableInput is the body of POST …/variables.
type CreateVariableInput struct {
	Name        string  `json:"name"`
	Value       string  `json:"value"`
	Description *string `json:"description,omitempty"`
	Environment string  `json:"environment,omitempty"`
}

// UpdateVariableInput is the body of PATCH …/variables/{id}. The API needs at
// least one of the two.
type UpdateVariableInput struct {
	Value       *string `json:"value,omitempty"`
	Description *string `json:"description,omitempty"`
}

func variablesPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "variables"}, rest...)...)
}

// CreateVariable needs `variables:write` and an owner or admin.
func (c *Client) CreateVariable(ctx context.Context, projectID string, in CreateVariableInput) (*Variable, error) {
	var out Variable
	if err := c.Do(ctx, http.MethodPost, variablesPath(projectID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetVariable needs `variables:read`.
func (c *Client) GetVariable(ctx context.Context, projectID, variableID string) (*Variable, error) {
	var out Variable
	if err := c.Do(ctx, http.MethodGet, variablesPath(projectID, variableID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListVariables needs `variables:read`.
func (c *Client) ListVariables(ctx context.Context, projectID string) ([]Variable, error) {
	var out []Variable
	if err := c.Do(ctx, http.MethodGet, variablesPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateVariable changes the value, the description, or both.
func (c *Client) UpdateVariable(ctx context.Context, projectID, variableID string, in UpdateVariableInput) (*Variable, error) {
	var out Variable
	if err := c.Do(ctx, http.MethodPatch, variablesPath(projectID, variableID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteVariable answers 204.
func (c *Client) DeleteVariable(ctx context.Context, projectID, variableID string) error {
	return c.Do(ctx, http.MethodDelete, variablesPath(projectID, variableID), nil, nil, nil)
}
