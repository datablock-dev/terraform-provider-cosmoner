package client

import (
	"context"
	"net/http"
)

// SSHKey is a public key registered for installing on new servers.
type SSHKey struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PublicKey is the authorized_keys line, whitespace-normalised by the API.
	PublicKey string `json:"publicKey"`
	// Fingerprint is the OpenSSH SHA256 form, unique within a project.
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
}

// CreateSSHKeyInput is the body of POST …/ssh-keys.
type CreateSSHKeyInput struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

func sshKeysPath(projectID string, rest ...string) string {
	return Path(append([]string{"v1", "projects", projectID, "ssh-keys"}, rest...)...)
}

// CreateSSHKey needs `servers:write`. A key already registered on the project
// is a 409.
func (c *Client) CreateSSHKey(ctx context.Context, projectID string, in CreateSSHKeyInput) (*SSHKey, error) {
	var out SSHKey
	if err := c.Do(ctx, http.MethodPost, sshKeysPath(projectID), nil, in, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSSHKeys needs `servers:read`. There is no route for a single key, so
// reads go through the list.
func (c *Client) ListSSHKeys(ctx context.Context, projectID string) ([]SSHKey, error) {
	var out []SSHKey
	if err := c.Do(ctx, http.MethodGet, sshKeysPath(projectID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteSSHKey needs `servers:write`. It does not revoke the key on servers
// it was already installed on.
func (c *Client) DeleteSSHKey(ctx context.Context, projectID, keyID string) error {
	return c.Do(ctx, http.MethodDelete, sshKeysPath(projectID, keyID), nil, nil, nil)
}
