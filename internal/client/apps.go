package client

import (
	"context"
	"net/http"
)

// App is an app as the detail and list routes return it. The API sends many
// more fields; only the ones the provider manages or reports are decoded.
type App struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Subdomain string  `json:"subdomain"`
	Status    string  `json:"status"`
	URL       *string `json:"url"`
	// Region is where the app runs, which the platform decides: it is not
	// necessarily the region the draft named.
	Region       string  `json:"region"`
	InstanceSize string  `json:"instanceSize"`
	AppType      *string `json:"appType"`

	GitProvider   *string `json:"gitProvider"`
	GitRepo       *string `json:"gitRepo"`
	GitBranch     *string `json:"gitBranch"`
	SourceDir     *string `json:"sourceDir"`
	BuildStrategy *string `json:"buildStrategy"`
	BuildCommand  *string `json:"buildCommand"`
	RunCommand    *string `json:"runCommand"`
	OutputDir     *string `json:"outputDir"`
	PublicPort    *int64  `json:"publicPort"`
	InternalPort  *int64  `json:"internalPort"`
	AutoDeploy    bool    `json:"autoDeploy"`

	ContainerRegistry   *string `json:"containerRegistry"`
	ContainerImage      *string `json:"containerImage"`
	ContainerPublicPort *string `json:"containerPublicPort"`
	ImageDeployPolicy   string  `json:"imageDeployPolicy"`

	Instances *int64 `json:"instances"`
	// ProvisionError is why the runtime could not create the app, when it
	// could not.
	ProvisionError *string `json:"provisionError"`
	CreatedAt      string  `json:"createdAt"`
}

// AppDraftInput is the body of POST …/apps/draft. The provider only creates
// apps on a generated Cosmoner subdomain, so domainType is always "cosmoner".
type AppDraftInput struct {
	Name                string  `json:"name"`
	Size                string  `json:"size"`
	Region              string  `json:"region"`
	DomainType          string  `json:"domainType"`
	AppType             *string `json:"appType,omitempty"`
	GitProvider         *string `json:"gitProvider,omitempty"`
	GitRepo             *string `json:"gitRepo,omitempty"`
	GitBranch           *string `json:"gitBranch,omitempty"`
	SourceDir           *string `json:"sourceDir,omitempty"`
	BuildStrategy       *string `json:"buildStrategy,omitempty"`
	BuildCommand        *string `json:"buildCommand,omitempty"`
	RunCommand          *string `json:"runCommand,omitempty"`
	OutputDir           *string `json:"outputDir,omitempty"`
	PublicPort          *int64  `json:"publicPort,omitempty"`
	InternalPort        *int64  `json:"internalPort,omitempty"`
	AutoDeploy          *bool   `json:"autoDeploy,omitempty"`
	ContainerRegistry   *string `json:"containerRegistry,omitempty"`
	ContainerImage      *string `json:"containerImage,omitempty"`
	ContainerPublicPort *string `json:"containerPublicPort,omitempty"`
	ImageDeployPolicy   *string `json:"imageDeployPolicy,omitempty"`
	Instances           *int64  `json:"instances,omitempty"`
}

// UpdateAppInput is the body of PATCH …/apps/{id}. The nullable settings are
// always sent, as null to clear them. Instances and the image policy are not
// nullable on this route, so they are left out when unset.
type UpdateAppInput struct {
	Name              string  `json:"name"`
	BuildCommand      *string `json:"buildCommand"`
	RunCommand        *string `json:"runCommand"`
	OutputDir         *string `json:"outputDir"`
	PublicPort        *int64  `json:"publicPort"`
	InternalPort      *int64  `json:"internalPort"`
	AutoDeploy        bool    `json:"autoDeploy"`
	ImageDeployPolicy *string `json:"imageDeployPolicy,omitempty"`
	Instances         *int64  `json:"instances,omitempty"`
}

func appsPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "apps"}, rest...)...)
}

// CreateAppDraft saves what an app should be, creating and billing nothing.
// Needs `apps:write`. The API refuses a name already used in the project.
func (c *Client) CreateAppDraft(ctx context.Context, projectID string, in AppDraftInput) (string, error) {
	var out struct {
		DraftID string `json:"draftId"`
	}
	if err := c.Do(ctx, http.MethodPost, appsPath(projectID, "draft"), nil, in, &out); err != nil {
		return "", err
	}
	return out.DraftID, nil
}

// CreateAppFromDraft turns a draft into an app and starts deploying it. This
// is the billed step. Needs `apps:write`.
func (c *Client) CreateAppFromDraft(ctx context.Context, projectID, draftID, size string) (string, error) {
	var out struct {
		AppID string `json:"appId"`
	}
	body := map[string]string{"draftId": draftID, "size": size}
	if err := c.Do(ctx, http.MethodPost, appsPath(projectID), nil, body, &out); err != nil {
		return "", err
	}
	return out.AppID, nil
}

// GetApp needs `apps:read`. Reading an app that is deploying also moves its
// status on, so polling it is how a deploy is followed.
func (c *Client) GetApp(ctx context.Context, projectID, appID string) (*App, error) {
	var out App
	if err := c.Do(ctx, http.MethodGet, appsPath(projectID, appID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListApps needs `apps:read`.
func (c *Client) ListApps(ctx context.Context, projectID string) ([]App, error) {
	var out []App
	if err := c.Do(ctx, http.MethodGet, appsPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateApp changes an app's settings and applies them to the running app.
// A 502 means they were saved but the runtime refused them.
func (c *Client) UpdateApp(ctx context.Context, projectID, appID string, in UpdateAppInput) (*App, error) {
	var out App
	if err := c.Do(ctx, http.MethodPatch, appsPath(projectID, appID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResizeApp moves an app to another size and invoices the difference at once.
func (c *Client) ResizeApp(ctx context.Context, projectID, appID, size string) error {
	return c.Do(ctx, http.MethodPatch, appsPath(projectID, appID, "size"), nil, map[string]string{"size": size}, nil)
}

// DeleteApp tears down the app's runtime, routing and billing, then the app.
// It is synchronous: a later read answers 404.
func (c *Client) DeleteApp(ctx context.Context, projectID, appID string) error {
	return c.Do(ctx, http.MethodDelete, appsPath(projectID, appID), nil, nil, nil)
}
