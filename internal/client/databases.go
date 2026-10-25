package client

import (
	"context"
	"net/http"
)

// Database is a dedicated database cluster. The connection URIs carry the
// password and are only on the detail read; the list leaves them null.
type Database struct {
	ID                  string  `json:"id"`
	Name                string  `json:"name"`
	Engine              string  `json:"engine"`
	Version             *string `json:"version"`
	Region              string  `json:"region"`
	Size                string  `json:"size"`
	Status              string  `json:"status"`
	Host                *string `json:"host"`
	Port                *int64  `json:"port"`
	PublicHost          *string `json:"publicHost"`
	DefaultDB           *string `json:"defaultDb"`
	DefaultUser         *string `json:"defaultUser"`
	ConnectionURI       *string `json:"connectionUri"`
	PublicConnectionURI *string `json:"publicConnectionUri"`
	CreatedAt           string  `json:"createdAt"`
}

// CreateDatabaseInput is the body of POST …/databases/dedicated.
type CreateDatabaseInput struct {
	Name    string `json:"name"`
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Slug    string `json:"slug"`
	Region  string `json:"region"`
}

func databasesPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "databases", "dedicated"}, rest...)...)
}

// CreateDatabase orders a cluster and bills it. Needs `databases:write`.
//
// The response carries no id, and names are not unique, so the caller finds
// the new cluster by listing — see the database resource. A refused request
// leaves nothing behind: the API rolls back everything it created.
func (c *Client) CreateDatabase(ctx context.Context, projectID string, in CreateDatabaseInput) error {
	return c.Do(ctx, http.MethodPost, databasesPath(projectID), nil, in, nil)
}

// ListDatabases needs `databases:read`. Deleted clusters are not listed.
func (c *Client) ListDatabases(ctx context.Context, projectID string) ([]Database, error) {
	var out []Database
	if err := c.Do(ctx, http.MethodGet, databasesPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetDatabase needs `databases:read`. A deleted cluster is still returned,
// with status TERMINATED; the row is kept.
func (c *Client) GetDatabase(ctx context.Context, projectID, databaseID string) (*Database, error) {
	var out Database
	if err := c.Do(ctx, http.MethodGet, databasesPath(projectID, databaseID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDatabase destroys the cluster and its data and stops its billing.
// Needs `databases:write`.
func (c *Client) DeleteDatabase(ctx context.Context, projectID, databaseID string) error {
	return c.Do(ctx, http.MethodDelete, databasesPath(projectID, databaseID), nil, nil, nil)
}
