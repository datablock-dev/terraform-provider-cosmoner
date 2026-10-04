package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testAPIKey    = "cos_test_key"
	testProjectID = "proj_test"
	// otherProjectID exists too, for resources that override the provider's project.
	otherProjectID = "proj_other"
)

// fakeAPI is an in-memory stand-in for the routes the provider calls. It
// answers with the platform's status codes and envelopes — 201 on create, 204
// on a secret or variable delete, `{ success, data }` everywhere else — and
// enforces the rules the provider has to work with: values never returned for
// secrets, descriptions kept unless one is sent, keys whitespace-normalised.
type fakeAPI struct {
	t      *testing.T
	server *httptest.Server

	mu        sync.Mutex
	nextID    int
	projects  map[string]map[string]any
	secrets   map[string]*fakeSecret
	variables map[string]map[string]any
	webhooks  map[string]map[string]any
	sshKeys   map[string]map[string]any
	groups    map[string]map[string]any
	// owner maps every resource id to the project it lives in.
	owner map[string]string
}

type fakeSecret struct {
	meta  map[string]any
	value string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{
		t: t,
		projects: map[string]map[string]any{
			testProjectID:  {"id": testProjectID, "name": "Test project", "slug": "test-project", "billingEmail": "billing@example.com", "blockedAt": nil},
			otherProjectID: {"id": otherProjectID, "name": "Other project", "slug": otherProjectID, "billingEmail": nil, "blockedAt": "2026-09-01T00:00:00.000Z"},
		},
		secrets:   map[string]*fakeSecret{},
		variables: map[string]map[string]any{},
		webhooks:  map[string]map[string]any{},
		sshKeys:   map[string]map[string]any{},
		groups:    map[string]map[string]any{},
		owner:     map[string]string{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

var routePattern = regexp.MustCompile(`^/v1/projects/([^/]+)(?:/([a-z-]+))?(?:/([^/]+))?$`)

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer "+testAPIKey {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid API key")
		return
	}

	m := routePattern.FindStringSubmatch(r.URL.Path)
	if m == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
		return
	}
	projectID, collection, id := m[1], m[2], m[3]
	if _, ok := f.projects[projectID]; !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Project not found")
		return
	}
	// An id from another project is as invisible as one that does not exist.
	if id != "" && f.owner[id] != projectID {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Not found")
		return
	}

	var body map[string]any
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid JSON")
			return
		}
	}

	switch collection {
	case "":
		writeData(w, http.StatusOK, f.projects[projectID])
	case "secrets":
		f.serveSecrets(w, r.Method, projectID, id, body)
	case "variables":
		f.serveVariables(w, r.Method, projectID, id, body)
	case "webhooks":
		f.serveWebhooks(w, r.Method, projectID, id, body)
	case "ssh-keys":
		f.serveSSHKeys(w, r.Method, projectID, id, body)
	case "resource-groups":
		f.serveGroups(w, r.Method, projectID, id, body)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (f *fakeAPI) serveSecrets(w http.ResponseWriter, method, projectID, id string, body map[string]any) {
	now := timestamp()
	switch {
	case method == http.MethodPost && id == "":
		name, _ := body["name"].(string)
		env := stringOr(body["environment"], "default")
		for _, s := range f.secrets {
			if f.owner[s.meta["id"].(string)] == projectID && s.meta["name"] == name && s.meta["environment"] == env {
				writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("A secret named %q already exists in the %s environment", name, env))
				return
			}
		}
		newID := f.newID("sec", projectID)
		s := &fakeSecret{
			meta: map[string]any{
				"id": newID, "name": name, "description": body["description"], "environment": env,
				"version": 1, "createdAt": now, "updatedAt": now,
			},
			value: body["value"].(string),
		}
		f.secrets[newID] = s
		// Like the platform: the create response has no updatedAt.
		writeData(w, http.StatusCreated, map[string]any{
			"id": newID, "name": name, "description": s.meta["description"], "environment": env,
			"version": 1, "createdAt": now, "value": s.value, "maskedValue": "••••",
		})
	case method == http.MethodGet && id != "":
		writeData(w, http.StatusOK, f.secrets[id].meta)
	case method == http.MethodPatch && id != "":
		s := f.secrets[id]
		value, ok := body["value"].(string)
		if !ok || value == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "value is required")
			return
		}
		s.value = value
		s.meta["version"] = s.meta["version"].(int) + 1
		s.meta["updatedAt"] = now
		if description, sent := body["description"]; sent {
			s.meta["description"] = description
		}
		writeData(w, http.StatusOK, map[string]any{
			"id": id, "name": s.meta["name"], "description": s.meta["description"], "environment": s.meta["environment"],
			"version": s.meta["version"], "updatedAt": now, "value": value, "maskedValue": "••••",
		})
	case method == http.MethodDelete && id != "":
		delete(f.secrets, id)
		delete(f.owner, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", method)
	}
}

func (f *fakeAPI) serveVariables(w http.ResponseWriter, method, projectID, id string, body map[string]any) {
	now := timestamp()
	switch {
	case method == http.MethodPost && id == "":
		newID := f.newID("var", projectID)
		v := map[string]any{
			"id": newID, "name": body["name"], "description": body["description"], "value": body["value"],
			"environment": stringOr(body["environment"], "default"), "createdAt": now, "updatedAt": now,
		}
		f.variables[newID] = v
		writeData(w, http.StatusCreated, v)
	case method == http.MethodGet && id != "":
		writeData(w, http.StatusOK, f.variables[id])
	case method == http.MethodPatch && id != "":
		v := f.variables[id]
		if _, ok := body["value"]; ok {
			v["value"] = body["value"]
		}
		if _, ok := body["description"]; ok {
			v["description"] = body["description"]
		}
		v["updatedAt"] = now
		writeData(w, http.StatusOK, v)
	case method == http.MethodDelete && id != "":
		delete(f.variables, id)
		delete(f.owner, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", method)
	}
}

func (f *fakeAPI) serveWebhooks(w http.ResponseWriter, method, projectID, id string, body map[string]any) {
	now := timestamp()
	switch {
	case method == http.MethodPost && id == "":
		newID := f.newID("wh", projectID)
		secret := "whsec_" + newID + "abcd"
		e := map[string]any{
			"id": newID, "name": body["name"], "description": body["description"], "url": body["url"],
			"events": body["events"], "enabled": true, "secretHint": secret[len(secret)-4:],
			"consecutiveFailures": 0, "disabledAt": nil, "disabledReason": nil, "createdAt": now, "updatedAt": now,
		}
		f.webhooks[newID] = e
		created := map[string]any{"secret": secret}
		for k, v := range e {
			created[k] = v
		}
		writeData(w, http.StatusCreated, created)
	case method == http.MethodGet && id != "":
		writeData(w, http.StatusOK, map[string]any{"endpoint": f.webhooks[id], "stats": map[string]int{"succeeded": 0, "failed": 0, "pending": 0}})
	case method == http.MethodPatch && id != "":
		e := f.webhooks[id]
		for _, field := range []string{"name", "description", "url", "events"} {
			if v, ok := body[field]; ok {
				e[field] = v
			}
		}
		if enabled, ok := body["enabled"].(bool); ok {
			e["enabled"] = enabled
			if enabled {
				e["disabledReason"], e["consecutiveFailures"] = nil, 0
			} else {
				e["disabledReason"] = "MANUAL"
			}
		}
		e["updatedAt"] = now
		writeData(w, http.StatusOK, e)
	case method == http.MethodDelete && id != "":
		delete(f.webhooks, id)
		delete(f.owner, id)
		writeData(w, http.StatusOK, nil)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", method)
	}
}

func (f *fakeAPI) serveSSHKeys(w http.ResponseWriter, method, projectID, id string, body map[string]any) {
	switch {
	case method == http.MethodGet && id == "":
		writeData(w, http.StatusOK, f.list(f.sshKeys, projectID))
	case method == http.MethodPost && id == "":
		publicKey := strings.Join(strings.Fields(body["publicKey"].(string)), " ")
		fingerprint := "SHA256:" + strings.Fields(publicKey)[1]
		for keyID, k := range f.sshKeys {
			if f.owner[keyID] == projectID && k["fingerprint"] == fingerprint {
				writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("This key is already registered as %q", k["name"]))
				return
			}
		}
		newID := f.newID("key", projectID)
		k := map[string]any{
			"id": newID, "organizationId": projectID, "name": strings.TrimSpace(body["name"].(string)),
			"publicKey": publicKey, "fingerprint": fingerprint, "createdAt": timestamp(), "updatedAt": timestamp(),
		}
		f.sshKeys[newID] = k
		writeData(w, http.StatusCreated, k)
	case method == http.MethodDelete && id != "":
		delete(f.sshKeys, id)
		delete(f.owner, id)
		writeData(w, http.StatusOK, map[string]int{"stillAuthorisedOn": 0})
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (f *fakeAPI) serveGroups(w http.ResponseWriter, method, projectID, id string, body map[string]any) {
	parentAllowed := func(parent any) bool {
		parentID, ok := parent.(string)
		return parent == nil || (ok && parentID != id && f.owner[parentID] == projectID && f.groups[parentID] != nil)
	}

	switch {
	case method == http.MethodGet && id == "":
		writeData(w, http.StatusOK, f.list(f.groups, projectID))
	case method == http.MethodPost && id == "":
		if !parentAllowed(body["parentId"]) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Parent group not found")
			return
		}
		newID := f.newID("grp", projectID)
		g := map[string]any{"id": newID, "name": strings.TrimSpace(body["name"].(string)), "parentId": body["parentId"]}
		f.groups[newID] = g
		writeData(w, http.StatusCreated, g)
	case method == http.MethodPatch && id != "":
		g := f.groups[id]
		if name, ok := body["name"].(string); ok {
			g["name"] = strings.TrimSpace(name)
		}
		if parent, ok := body["parentId"]; ok {
			if !parentAllowed(parent) {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Parent group not found")
				return
			}
			g["parentId"] = parent
		}
		writeData(w, http.StatusOK, g)
	case method == http.MethodDelete && id != "":
		// Children move up into the deleted group's parent.
		for _, g := range f.groups {
			if g["parentId"] == id {
				g["parentId"] = f.groups[id]["parentId"]
			}
		}
		delete(f.groups, id)
		delete(f.owner, id)
		writeData(w, http.StatusOK, nil)
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

// Helpers the tests use to change things behind Terraform's back.

func (f *fakeAPI) rotateSecretElsewhere(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.secrets {
		if s.meta["name"] == name {
			s.value = "set-in-the-dashboard"
			s.meta["version"] = s.meta["version"].(int) + 1
			return
		}
	}
	f.t.Fatalf("no secret named %s", name)
}

func (f *fakeAPI) secretValue(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.secrets {
		if s.meta["name"] == name {
			return s.value
		}
	}
	return ""
}

func (f *fakeAPI) pauseWebhooks() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.webhooks {
		e["enabled"], e["disabledReason"], e["consecutiveFailures"] = false, "CONSECUTIVE_FAILURES", 10
	}
}

func (f *fakeAPI) deleteAllVariables() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.variables {
		delete(f.owner, id)
	}
	f.variables = map[string]map[string]any{}
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.secrets) + len(f.variables) + len(f.webhooks) + len(f.sshKeys) + len(f.groups)
}

func (f *fakeAPI) newID(prefix, projectID string) string {
	f.nextID++
	id := fmt.Sprintf("%s_%d", prefix, f.nextID)
	f.owner[id] = projectID
	return id
}

func (f *fakeAPI) list(items map[string]map[string]any, projectID string) []map[string]any {
	out := []map[string]any{}
	for id, item := range items {
		if f.owner[id] == projectID {
			out = append(out, item)
		}
	}
	return out
}

func writeData(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": map[string]any{"code": code, "message": message}})
}

func stringOr(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	return fallback
}

func timestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}
