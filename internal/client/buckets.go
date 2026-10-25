package client

import (
	"context"
	"net/http"
)

// Bucket is an object storage bucket as the list returns it. There is no
// route for a single bucket, so every read goes through the list.
type Bucket struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	Tier         string     `json:"tier"`
	PublicAccess bool       `json:"publicAccess"`
	PublicPaths  []string   `json:"publicPaths"`
	Versioning   bool       `json:"versioning"`
	CORSRules    []CORSRule `json:"corsRules"`
	Status       string     `json:"status"`
	CDNEnabled   bool       `json:"cdnEnabled"`
	CDNDomain    *string    `json:"cdnDomain"`
	// The S3-compatible coordinates clients connect with.
	S3BucketName *string `json:"s3BucketName"`
	S3Endpoint   *string `json:"s3Endpoint"`
	S3Region     *string `json:"s3Region"`
	CreatedAt    string  `json:"createdAt"`
}

// CORSRule is one rule of a bucket's CORS configuration.
type CORSRule struct {
	AllowedOrigins []string `json:"allowedOrigins"`
	AllowedMethods []string `json:"allowedMethods"`
	AllowedHeaders []string `json:"allowedHeaders"`
	ExposeHeaders  []string `json:"exposeHeaders"`
	MaxAgeSeconds  *int64   `json:"maxAgeSeconds,omitempty"`
}

// CreateBucketInput is the body of POST …/storage/object-storage. The
// provider field is left to the API's default.
type CreateBucketInput struct {
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	Tier         string     `json:"tier"`
	PublicAccess bool       `json:"publicAccess"`
	PublicPaths  []string   `json:"publicPaths"`
	Versioning   bool       `json:"versioning"`
	CORSRules    []CORSRule `json:"corsRules,omitempty"`
	CDNEnabled   bool       `json:"cdnEnabled"`
}

func bucketsPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "storage", "object-storage"}, rest...)...)
}

// CreateBucket creates and bills a bucket. Needs `object-storage:write`. The
// bucket is ready when this returns; the response carries no id, but names
// are unique in a project, so the caller finds it by name.
func (c *Client) CreateBucket(ctx context.Context, projectID string, in CreateBucketInput) error {
	return c.Do(ctx, http.MethodPost, bucketsPath(projectID), nil, in, nil)
}

// ListBuckets needs `object-storage:read`.
func (c *Client) ListBuckets(ctx context.Context, projectID string) ([]Bucket, error) {
	var out []Bucket
	if err := c.Do(ctx, http.MethodGet, bucketsPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateBucketTier moves a bucket to another tier and invoices the proration.
// The API refuses a move to the tier the bucket is already on.
func (c *Client) UpdateBucketTier(ctx context.Context, projectID, bucketID, tier string) error {
	return c.Do(ctx, http.MethodPatch, bucketsPath(projectID, bucketID, "tier"), nil, map[string]string{"tier": tier}, nil)
}

// UpdateBucketAccess sets public reads and the folders they cover.
func (c *Client) UpdateBucketAccess(ctx context.Context, projectID, bucketID string, publicAccess bool, publicPaths []string) error {
	body := map[string]any{"publicAccess": publicAccess, "publicPaths": publicPaths}
	return c.Do(ctx, http.MethodPatch, bucketsPath(projectID, bucketID, "access"), nil, body, nil)
}

// UpdateBucketCORS replaces the CORS rules; an empty list clears them.
func (c *Client) UpdateBucketCORS(ctx context.Context, projectID, bucketID string, rules []CORSRule) error {
	body := map[string]any{"corsRules": rules}
	return c.Do(ctx, http.MethodPatch, bucketsPath(projectID, bucketID, "cors"), nil, body, nil)
}

// UpdateBucketCDN turns the metered CDN add-on on or off. The API refuses a
// toggle to the state the bucket is already in.
func (c *Client) UpdateBucketCDN(ctx context.Context, projectID, bucketID string, enabled bool) error {
	return c.Do(ctx, http.MethodPatch, bucketsPath(projectID, bucketID, "cdn"), nil, map[string]bool{"cdnEnabled": enabled}, nil)
}

// DeleteBucket deletes the bucket with every object in it and stops its
// billing. Synchronous: the bucket is gone from the list afterwards.
func (c *Client) DeleteBucket(ctx context.Context, projectID, bucketID string) error {
	return c.Do(ctx, http.MethodDelete, bucketsPath(projectID, bucketID), nil, nil, nil)
}
