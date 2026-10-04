package client

import (
	"context"
	"net/http"
)

// ResourceGroup is a box in the project graph that other resources and groups
// are drawn inside. It changes layout only, never what a resource does.
type ResourceGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// ParentID is the group this one is drawn inside, or nil at the top level.
	ParentID *string `json:"parentId"`
}

// CreateResourceGroupInput is the body of POST …/resource-groups.
type CreateResourceGroupInput struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parentId"`
}

// UpdateResourceGroupInput is the body of PATCH …/resource-groups/{id}.
// ParentID is always sent, as null to move the group to the top level.
type UpdateResourceGroupInput struct {
	Name     string  `json:"name"`
	ParentID *string `json:"parentId"`
}

func resourceGroupsPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "resource-groups"}, rest...)...)
}

// CreateResourceGroup needs `projects:write`.
func (c *Client) CreateResourceGroup(ctx context.Context, projectID string, in CreateResourceGroupInput) (*ResourceGroup, error) {
	var out ResourceGroup
	if err := c.Do(ctx, http.MethodPost, resourceGroupsPath(projectID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListResourceGroups needs `projects:read`. There is no route for a single
// group, so reads go through the list.
func (c *Client) ListResourceGroups(ctx context.Context, projectID string) ([]ResourceGroup, error) {
	var out []ResourceGroup
	if err := c.Do(ctx, http.MethodGet, resourceGroupsPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateResourceGroup renames a group, moves it, or both.
func (c *Client) UpdateResourceGroup(ctx context.Context, projectID, groupID string, in UpdateResourceGroupInput) (*ResourceGroup, error) {
	var out ResourceGroup
	if err := c.Do(ctx, http.MethodPatch, resourceGroupsPath(projectID, groupID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteResourceGroup dissolves a group. What was inside moves up a level;
// no resource is deleted.
func (c *Client) DeleteResourceGroup(ctx context.Context, projectID, groupID string) error {
	return c.Do(ctx, http.MethodDelete, resourceGroupsPath(projectID, groupID), nil, nil, nil)
}
