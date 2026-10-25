package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// The billable half of fakeAPI: apps, dedicated PostgreSQL, Redis and
// buckets. What it mirrors from the platform, because the provider relies on
// each of these:
//
//   - Database, Redis and bucket creates answer `{ deployed: true }` with no
//     id. Database and Redis names are not unique; bucket and app names are
//     (409 on a duplicate).
//   - A refused create (unsellable region or plan, wrong engine) is a 4xx and
//     leaves nothing behind, because the platform rolls back its own writes.
//   - Databases start CREATING and Redis CREATING; apps start DEPLOYING. Each
//     moves on only when its detail route is read, as the platform's status
//     sync runs on the read. Buckets are ACTIVE at once.
//   - A deleted database or Redis row is kept as TERMINATED: the detail route
//     still answers 200, the list leaves it out. Apps and buckets are deleted
//     outright. Every delete answers `{ data: {} }`.
//   - Connection URIs and the Redis password are on the detail read only.
//   - An app's stored region is where it was placed, not what the draft named;
//     its size is canonicalised, and a static site's is always shared-static.
//   - PATCH …/apps/{id} treats an absent field as unchanged and null as a
//     clear, except instances and imageDeployPolicy, which refuse null.
//   - The bucket tier and CDN routes refuse a change to the current value.
//
// Names steer failures, so a test can ask for one: "broken" ends in an error
// status, "stuck" never becomes ready, and "flaky" is created but answered
// with a 502, which leaves the provider not knowing whether it was.

const (
	// The one region databases and Redis are sold in.
	fakeSellableRegion = "se-sto"
	// Where the fake places every app, whatever the draft asked for.
	fakeAppPlacement = "placement-1"
	// Detail reads before a resource reports ready.
	fakeReadsUntilReady = 2
)

type billable struct {
	drafts    map[string]map[string]any
	apps      map[string]map[string]any
	databases map[string]map[string]any
	redis     map[string]map[string]any
	buckets   map[string]map[string]any
	// reads counts detail reads of a resource that is still provisioning.
	reads map[string]int
}

func newBillable() billable {
	return billable{
		drafts:    map[string]map[string]any{},
		apps:      map[string]map[string]any{},
		databases: map[string]map[string]any{},
		redis:     map[string]map[string]any{},
		buckets:   map[string]map[string]any{},
		reads:     map[string]int{},
	}
}

func (f *fakeAPI) billableCount() int {
	n := len(f.apps) + len(f.buckets)
	for _, rows := range []map[string]map[string]any{f.databases, f.redis} {
		for _, row := range rows {
			if row["status"] != "TERMINATED" {
				n++
			}
		}
	}
	return n
}

// ownedRow returns the row with id in the project, or nil.
func (f *fakeAPI) ownedRow(rows map[string]map[string]any, projectID, id string) map[string]any {
	if f.owner[id] != projectID {
		return nil
	}
	return rows[id]
}

// progress moves a provisioning row on after enough detail reads.
func (f *fakeAPI) progress(row map[string]any, pending, ready string) {
	if row["status"] != pending {
		return
	}
	id := row["id"].(string)
	name, _ := row["name"].(string)
	f.reads[id]++
	if f.reads[id] < fakeReadsUntilReady || strings.Contains(name, "stuck") {
		return
	}
	if strings.Contains(name, "broken") {
		row["status"] = "ERROR"
		return
	}
	row["status"] = ready
}

var fakeDNSLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

var fakeLegacyAppSizes = map[string]string{"k8s-xs": "shared-xs", "k8s-s": "shared-s", "k8s-m": "shared-m", "k8s-l": "shared-l", "k8s-xl": "shared-xl", "k8s-static": "shared-static"}

func fakeCanonicalSize(size string) string {
	if current, ok := fakeLegacyAppSizes[size]; ok {
		return current
	}
	return size
}

func (f *fakeAPI) serveApps(w http.ResponseWriter, method, projectID string, path []string, body map[string]any) {
	switch {
	case method == http.MethodGet && len(path) == 0:
		writeData(w, http.StatusOK, f.list(f.apps, projectID))

	case method == http.MethodPost && len(path) == 1 && path[0] == "draft":
		name := strings.TrimSpace(stringOr(body["name"], ""))
		if name == "" || stringOr(body["region"], "") == "" || stringOr(body["size"], "") == "" {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "name, size and region are required")
			return
		}
		for id, app := range f.apps {
			if f.owner[id] == projectID && app["name"] == name {
				writeError(w, http.StatusConflict, "CONFLICT", fmt.Sprintf("An app named %q already exists in this project", name))
				return
			}
		}
		body["name"] = name
		draftID := f.newID("draft", projectID)
		f.drafts[draftID] = body
		writeData(w, http.StatusCreated, map[string]any{"draftId": draftID})

	case method == http.MethodPost && len(path) == 0:
		draftID, _ := body["draftId"].(string)
		draft := f.ownedRow(f.drafts, projectID, draftID)
		if draft == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Draft not found or expired")
			return
		}
		delete(f.drafts, draftID)
		delete(f.owner, draftID)

		id := f.newID("app", projectID)
		name := draft["name"].(string)
		size := fakeCanonicalSize(stringOr(body["size"], ""))
		if draft["appType"] == "static" {
			size = "shared-static"
		}
		app := map[string]any{
			"id": id, "name": name, "subdomain": name + "-x7k2", "status": "DEPLOYING", "url": nil,
			"region": fakeAppPlacement, "instanceSize": size, "appType": draft["appType"],
			"gitProvider": draft["gitProvider"], "gitRepo": draft["gitRepo"], "gitBranch": draft["gitBranch"],
			"sourceDir": draft["sourceDir"], "buildStrategy": draft["buildStrategy"],
			"buildCommand": draft["buildCommand"], "runCommand": draft["runCommand"], "outputDir": draft["outputDir"],
			"publicPort": draft["publicPort"], "internalPort": draft["internalPort"],
			"autoDeploy":        draft["autoDeploy"] == true,
			"containerRegistry": draft["containerRegistry"], "containerImage": draft["containerImage"],
			"containerPublicPort": draft["containerPublicPort"],
			"imageDeployPolicy":   stringOr(draft["imageDeployPolicy"], "TAG"),
			"instances":           draft["instances"], "provisionError": nil,
			"envVars": []any{}, "createdAt": timestamp(), "updatedAt": timestamp(),
		}
		f.apps[id] = app
		if strings.Contains(name, "flaky") {
			writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "Upstream timed out")
			return
		}
		writeData(w, http.StatusOK, map[string]any{"deployed": true, "appId": id})

	case len(path) >= 1:
		app := f.ownedRow(f.apps, projectID, path[0])
		if app == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "App not found")
			return
		}
		switch {
		case method == http.MethodGet && len(path) == 1:
			f.progress(app, "DEPLOYING", "RUNNING")
			switch app["status"] {
			case "RUNNING":
				app["url"] = "https://" + app["subdomain"].(string) + ".cosmoner.app"
			case "ERROR":
				app["provisionError"] = "The image could not be pulled"
			}
			writeData(w, http.StatusOK, app)

		case method == http.MethodPatch && len(path) == 1:
			for _, field := range []string{"instances", "imageDeployPolicy"} {
				if v, sent := body[field]; sent && v == nil {
					writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", field+": expected a value, received null")
					return
				}
			}
			for _, field := range []string{"name", "buildCommand", "runCommand", "outputDir", "publicPort", "internalPort", "autoDeploy", "imageDeployPolicy", "instances"} {
				if v, sent := body[field]; sent {
					app[field] = v
				}
			}
			if name, ok := app["name"].(string); ok {
				app["name"] = strings.TrimSpace(name)
			}
			writeData(w, http.StatusOK, app)

		case method == http.MethodPatch && len(path) == 2 && path[1] == "size":
			size := fakeCanonicalSize(stringOr(body["size"], ""))
			switch {
			case app["appType"] == "static":
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Static sites do not run on a sized instance and cannot be resized")
			case app["instanceSize"] == size:
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "App is already on this size")
			case app["status"] == "DEPLOYING":
				writeError(w, http.StatusConflict, "CONFLICT", "This app is still deploying. Wait for it to finish before changing its size.")
			default:
				app["instanceSize"] = size
				writeData(w, http.StatusOK, map[string]any{"instanceSize": size})
			}

		case method == http.MethodDelete && len(path) == 1:
			delete(f.apps, path[0])
			delete(f.owner, path[0])
			writeData(w, http.StatusOK, map[string]any{})

		default:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
		}

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

// databaseFields are the keys the list route returns; the detail adds the URIs.
var databaseFields = []string{"id", "name", "engine", "version", "provider", "region", "size", "numNodes", "storageGb", "status", "host", "port", "publicHost", "defaultDb", "defaultUser", "createdAt"}

func (f *fakeAPI) serveDatabases(w http.ResponseWriter, method, projectID string, path []string, body map[string]any) {
	switch {
	case method == http.MethodGet && len(path) == 0:
		out := []map[string]any{}
		for _, db := range f.list(f.databases, projectID) {
			if db["status"] != "TERMINATED" {
				out = append(out, pick(db, databaseFields))
			}
		}
		writeData(w, http.StatusOK, out)

	case method == http.MethodPost && len(path) == 0:
		name := stringOr(body["name"], "")
		switch {
		case !fakeDNSLabel.MatchString(name) || len(name) > 63:
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name must be lowercase letters, numbers, or hyphens")
			return
		case body["engine"] != "POSTGRESQL":
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", fmt.Sprintf("%v is not available; always-on databases are PostgreSQL", body["engine"]))
			return
		case body["region"] != fakeSellableRegion:
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "This plan is not available in the selected region")
			return
		}
		id := f.newID("db", projectID)
		f.databases[id] = map[string]any{
			"id": id, "name": name, "engine": "POSTGRESQL", "version": body["version"], "provider": "COSMONER",
			"region": fakeSellableRegion, "size": body["slug"], "numNodes": 1, "storageGb": nil, "status": "CREATING",
			"host": "pg-" + id + ".internal", "port": 5432, "publicHost": "pg-" + id + ".db.example.com",
			"defaultDb": "app", "defaultUser": "app", "createdAt": timestamp(),
		}
		if strings.Contains(name, "flaky") {
			writeError(w, http.StatusBadGateway, "BAD_GATEWAY", "Upstream timed out")
			return
		}
		writeData(w, http.StatusOK, map[string]any{"deployed": true})

	case len(path) == 1:
		db := f.ownedRow(f.databases, projectID, path[0])
		if db == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Database cluster not found")
			return
		}
		switch method {
		case http.MethodGet:
			f.progress(db, "CREATING", "ONLINE")
			detail := pick(db, databaseFields)
			detail["connectionUri"], detail["publicConnectionUri"] = nil, nil
			if db["status"] != "TERMINATED" {
				detail["connectionUri"] = fmt.Sprintf("postgresql://app:secret@%s:5432/app", db["host"])
				detail["publicConnectionUri"] = fmt.Sprintf("postgresql://app:secret@%s:5432/app?sslmode=require", db["publicHost"])
			}
			writeData(w, http.StatusOK, detail)
		case http.MethodDelete:
			db["status"] = "TERMINATED"
			writeData(w, http.StatusOK, map[string]any{})
		default:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
		}

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (f *fakeAPI) serveRedis(w http.ResponseWriter, method, projectID string, path []string, body map[string]any) {
	serialize := func(db map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range db {
			if k != "password" {
				out[k] = v
			}
		}
		return out
	}

	switch {
	case method == http.MethodGet && len(path) == 0:
		out := []map[string]any{}
		for _, db := range f.list(f.redis, projectID) {
			if db["status"] != "TERMINATED" {
				out = append(out, serialize(db))
			}
		}
		writeData(w, http.StatusOK, out)

	case method == http.MethodPost && len(path) == 0:
		name, plan := stringOr(body["name"], ""), stringOr(body["planSlug"], "")
		switch {
		case !fakeDNSLabel.MatchString(name) || len(name) > 40:
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "Name must be lowercase letters, numbers, or hyphens")
			return
		case !strings.HasPrefix(plan, "valkey-"):
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "This plan is no longer available")
			return
		case body["region"] != fakeSellableRegion:
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "This plan is not available in the selected region")
			return
		}
		id := f.newID("redis", projectID)
		f.redis[id] = map[string]any{
			"id": id, "name": name, "provider": "COSMONER", "engine": "VALKEY", "engineVersion": "8.1.1",
			"planSlug": plan, "planType": "RAM", "memoryMb": 1024, "throughputOps": nil, "cloudProvider": nil,
			"region": fakeSellableRegion, "replication": false, "dataPersistence": stringOr(body["dataPersistence"], "NONE"),
			"status": "CREATING", "host": "valkey-" + id + ".internal", "port": 6379, "password": "redis-secret",
			"createdAt": timestamp(),
		}
		writeData(w, http.StatusOK, map[string]any{"deployed": true})

	case len(path) == 1:
		db := f.ownedRow(f.redis, projectID, path[0])
		if db == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Redis database not found")
			return
		}
		switch method {
		case http.MethodGet:
			f.progress(db, "CREATING", "ACTIVE")
			detail := serialize(db)
			detail["password"] = db["password"]
			writeData(w, http.StatusOK, detail)
		case http.MethodDelete:
			db["status"], db["password"] = "TERMINATED", nil
			writeData(w, http.StatusOK, map[string]any{})
		default:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
		}

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (f *fakeAPI) serveBuckets(w http.ResponseWriter, method, projectID string, path []string, body map[string]any) {
	switch {
	case method == http.MethodGet && len(path) == 0:
		writeData(w, http.StatusOK, f.list(f.buckets, projectID))

	case method == http.MethodPost && len(path) == 0:
		name := stringOr(body["name"], "")
		for id, b := range f.buckets {
			if f.owner[id] == projectID && b["name"] == name {
				writeError(w, http.StatusConflict, "CONFLICT", "An object storage with that name already exists in this project")
				return
			}
		}
		id := f.newID("bkt", projectID)
		b := map[string]any{
			"id": id, "name": name, "provider": "AWS_S3", "region": body["region"], "tier": stringOr(body["tier"], "STARTER"),
			"publicAccess": body["publicAccess"] == true, "publicPaths": dedupe(body["publicPaths"]),
			"versioning": body["versioning"] == true, "corsRules": nil, "status": "ACTIVE",
			"s3BucketName": "cos-" + id + "-" + name, "s3Endpoint": fmt.Sprintf("https://s3.%v.example.com", body["region"]),
			"s3Region": body["region"], "cdnEnabled": false, "cdnDomain": nil, "createdAt": timestamp(),
		}
		// Never sent means a null column; the zod defaults fill each rule.
		if rules, ok := body["corsRules"]; ok {
			b["corsRules"] = withCORSDefaults(rules)
		}
		setCDN(b, body["cdnEnabled"] == true)
		f.buckets[id] = b
		writeData(w, http.StatusOK, map[string]any{"deployed": true})

	case len(path) >= 1:
		b := f.ownedRow(f.buckets, projectID, path[0])
		if b == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Bucket not found")
			return
		}
		switch {
		case method == http.MethodDelete && len(path) == 1:
			delete(f.buckets, path[0])
			delete(f.owner, path[0])
			writeData(w, http.StatusOK, map[string]any{})
		case method == http.MethodPatch && len(path) == 2 && path[1] == "tier":
			if b["tier"] == body["tier"] {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", "Bucket is already on this tier")
				return
			}
			b["tier"] = body["tier"]
			writeData(w, http.StatusOK, map[string]any{"tier": b["tier"]})
		case method == http.MethodPatch && len(path) == 2 && path[1] == "access":
			b["publicAccess"], b["publicPaths"] = body["publicAccess"] == true, dedupe(body["publicPaths"])
			writeData(w, http.StatusOK, map[string]any{"publicAccess": b["publicAccess"], "publicPaths": b["publicPaths"]})
		case method == http.MethodPatch && len(path) == 2 && path[1] == "cors":
			rules, ok := body["corsRules"].([]any)
			if !ok {
				writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "corsRules: expected array")
				return
			}
			b["corsRules"] = withCORSDefaults(rules)
			writeData(w, http.StatusOK, map[string]any{"corsRules": b["corsRules"]})
		case method == http.MethodPatch && len(path) == 2 && path[1] == "cdn":
			enabled := body["cdnEnabled"] == true
			if b["cdnEnabled"] == enabled {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", map[bool]string{true: "CDN is already enabled", false: "CDN is not enabled"}[enabled])
				return
			}
			setCDN(b, enabled)
			writeData(w, http.StatusOK, map[string]any{"cdnEnabled": enabled, "cdnDomain": b["cdnDomain"]})
		default:
			writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
		}

	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func setCDN(b map[string]any, enabled bool) {
	b["cdnEnabled"], b["cdnDomain"] = enabled, nil
	if enabled {
		b["cdnDomain"] = b["id"].(string) + ".cdn.example.com"
	}
}

// withCORSDefaults applies the platform schema's defaults and trimming.
func withCORSDefaults(raw any) []any {
	rules, _ := raw.([]any)
	out := []any{}
	for _, r := range rules {
		rule := map[string]any{}
		for k, v := range r.(map[string]any) {
			rule[k] = v
		}
		for _, k := range []string{"allowedHeaders", "exposeHeaders"} {
			if rule[k] == nil {
				rule[k] = []any{}
			}
		}
		if origins, ok := rule["allowedOrigins"].([]any); ok {
			for i, o := range origins {
				origins[i] = strings.TrimSpace(o.(string))
			}
		}
		out = append(out, rule)
	}
	return out
}

func dedupe(raw any) []any {
	values, _ := raw.([]any)
	out := []any{}
	for _, v := range values {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func pick(row map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		out[k] = row[k]
	}
	return out
}

// Helpers the tests use to change things behind Terraform's back.

func (f *fakeAPI) databaseCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, db := range f.databases {
		if db["name"] == name && db["status"] != "TERMINATED" {
			n++
		}
	}
	return n
}
