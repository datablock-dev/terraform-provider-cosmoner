package client

import (
	"context"
	"net/http"
)

// RedisDatabase is a managed Redis-compatible database. Password is only on
// the detail read.
type RedisDatabase struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Engine          string  `json:"engine"`
	EngineVersion   *string `json:"engineVersion"`
	PlanSlug        string  `json:"planSlug"`
	MemoryMB        int64   `json:"memoryMb"`
	Region          string  `json:"region"`
	DataPersistence string  `json:"dataPersistence"`
	Status          string  `json:"status"`
	Host            *string `json:"host"`
	Port            *int64  `json:"port"`
	Password        *string `json:"password"`
	CreatedAt       string  `json:"createdAt"`
}

// CreateRedisInput is the body of POST …/redis.
type CreateRedisInput struct {
	Name            string `json:"name"`
	PlanSlug        string `json:"planSlug"`
	Region          string `json:"region"`
	DataPersistence string `json:"dataPersistence,omitempty"`
}

func redisPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "redis"}, rest...)...)
}

// CreateRedis orders a database and bills it. Needs `redis:write`. Like
// CreateDatabase, the response carries no id, and a refusal leaves nothing.
func (c *Client) CreateRedis(ctx context.Context, projectID string, in CreateRedisInput) error {
	return c.Do(ctx, http.MethodPost, redisPath(projectID), nil, in, nil)
}

// ListRedis needs `redis:read`. Deleted databases are not listed.
func (c *Client) ListRedis(ctx context.Context, projectID string) ([]RedisDatabase, error) {
	var out []RedisDatabase
	if err := c.Do(ctx, http.MethodGet, redisPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetRedis needs `redis:read`. A deleted database is still returned, with
// status TERMINATED.
func (c *Client) GetRedis(ctx context.Context, projectID, redisID string) (*RedisDatabase, error) {
	var out RedisDatabase
	if err := c.Do(ctx, http.MethodGet, redisPath(projectID, redisID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteRedis destroys the database and its data and stops its billing.
func (c *Client) DeleteRedis(ctx context.Context, projectID, redisID string) error {
	return c.Do(ctx, http.MethodDelete, redisPath(projectID, redisID), nil, nil, nil)
}
