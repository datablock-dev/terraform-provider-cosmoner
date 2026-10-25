package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*postgresDatabaseResource)(nil)
	_ resource.ResourceWithImportState = (*postgresDatabaseResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*postgresDatabaseResource)(nil)
)

const (
	defaultDatabaseCreateTimeout = 30 * time.Minute
	defaultDatabaseDeleteTimeout = 15 * time.Minute
	postgresEngine               = "POSTGRESQL"
)

// The platform's name rule for servers, databases and Redis: a DNS label.
var dnsLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// The catalogue's slug rule, which plan and size slugs follow.
var catalogueSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type postgresDatabaseResource struct {
	resourceBase
}

type postgresDatabaseModel struct {
	ID                  types.String   `tfsdk:"id"`
	ProjectID           types.String   `tfsdk:"project_id"`
	Name                types.String   `tfsdk:"name"`
	Size                types.String   `tfsdk:"size"`
	Version             types.String   `tfsdk:"version"`
	Region              types.String   `tfsdk:"region"`
	Status              types.String   `tfsdk:"status"`
	Host                types.String   `tfsdk:"host"`
	Port                types.Int64    `tfsdk:"port"`
	PublicHost          types.String   `tfsdk:"public_host"`
	Database            types.String   `tfsdk:"database"`
	Username            types.String   `tfsdk:"username"`
	ConnectionURI       types.String   `tfsdk:"connection_uri"`
	PublicConnectionURI types.String   `tfsdk:"public_connection_uri"`
	CreatedAt           types.String   `tfsdk:"created_at"`
	Timeouts            timeouts.Value `tfsdk:"timeouts"`
}

func newPostgresDatabaseResource() resource.Resource {
	return &postgresDatabaseResource{}
}

func (r *postgresDatabaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_postgres_database"
}

func (r *postgresDatabaseResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A dedicated PostgreSQL database. **Billed from creation**, to the project's saved card.\n\n" +
			"`apply` waits until the database is online, up to `timeouts.create` (default 30 minutes). A database " +
			"that ends in an error, or is still starting at the deadline, is kept in state as tainted, so the next " +
			"apply replaces it; the provider never deletes it on its own. A create the API refuses (an unavailable " +
			"size, region or version, or a project that cannot be billed) leaves nothing behind.\n\n" +
			"The API has no route to change a database, so changing any argument replaces it, **and its data with " +
			"it**. Consider `lifecycle { prevent_destroy = true }`.\n\n" +
			"Needs `databases:read` and `databases:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Database ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Name: lowercase letters, digits and inner hyphens, up to 63 characters. " +
					"Names are not unique; the API tells databases apart by ID.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 63),
					stringvalidator.RegexMatches(dnsLabelPattern, "must be lowercase letters, digits or hyphens, and cannot start or end with a hyphen"),
				},
			},
			// Sizes, regions and versions are the catalogue's, and change
			// without a provider release, so the API is left to judge them.
			"size": schema.StringAttribute{
				MarkdownDescription: "Size slug from the database catalogue, e.g. `postgres-cosmoner-1vcpu-1gb`. Changing it replaces the database.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(catalogueSlugPattern, "must be a catalogue slug"),
				},
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "PostgreSQL major version, e.g. `\"17\"`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Region slug from the database catalogue. The API refuses a region the size is not sold in.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"status": computedString("`ONLINE` once ready. `CREATING`, `ERROR`, `RESIZING` and `MIGRATING` are the other states the API reports."),
			"host":   computedString("Host apps on Cosmoner connect to. Reachable only from inside the platform."),
			"port": schema.Int64Attribute{
				MarkdownDescription: "Port to connect on.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"public_host": computedString("Host to connect to from outside Cosmoner, on port 5432 with TLS required. Null where there is none."),
			"database":    computedString("Name of the database created in the cluster."),
			"username":    computedString("User to connect as."),
			"connection_uri": schema.StringAttribute{
				MarkdownDescription: "Connection URI for `host`, **including the password**. Readable with `databases:read`.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"public_connection_uri": schema.StringAttribute{
				MarkdownDescription: "Connection URI for `public_host` with `sslmode=require`, **including the password**. Null where there is no public host.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": computedString("When the database was created, as an RFC 3339 timestamp."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create:            true,
				Delete:            true,
				CreateDescription: "How long to wait for the database to come online. Defaults to `30m`.",
				DeleteDescription: "How long to wait for the database to be deleted. Defaults to `15m`.",
			}),
		},
	}
}

func (r *postgresDatabaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan postgresDatabaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultDatabaseCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID := plan.ProjectID.ValueString()

	// The create response carries no id and names are not unique, so the new
	// database is the one with this name that was not there before.
	existing, err := r.data.client.ListDatabases(ctx, projectID)
	if err != nil {
		apiError(&resp.Diagnostics, "list databases", err)
		return
	}

	createErr := r.data.client.CreateDatabase(ctx, projectID, client.CreateDatabaseInput{
		Name:    plan.Name.ValueString(),
		Engine:  postgresEngine,
		Version: plan.Version.ValueString(),
		Slug:    plan.Size.ValueString(),
		Region:  plan.Region.ValueString(),
	})
	if createErr != nil && !outcomeUnknown(createErr) {
		apiError(&resp.Diagnostics, "create database", createErr)
		return
	}

	created, err := r.findCreated(ctx, projectID, plan.Name.ValueString(), existing)
	switch {
	case err != nil && createErr != nil:
		apiError(&resp.Diagnostics, "create database", fmt.Errorf("%w (and checking whether it was created failed: %s)", createErr, err))
		return
	case err != nil:
		apiError(&resp.Diagnostics, "find the created database", err)
		return
	case created == nil && createErr != nil:
		apiError(&resp.Diagnostics, "create database", createErr)
		return
	case created == nil:
		resp.Diagnostics.AddError("Created database not found",
			fmt.Sprintf("The API accepted the database, but no new database named %q is listed in the project. "+
				"Check the control panel before applying again, or a second one will be ordered.", plan.Name.ValueString()))
		return
	}

	latest := created
	waitErr := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		db, err := r.data.client.GetDatabase(ctx, projectID, created.ID)
		if err != nil {
			return false, err
		}
		latest = db
		switch db.Status {
		case "ONLINE":
			return true, nil
		case "ERROR", "TERMINATED":
			return false, &errNotReady{status: db.Status}
		}
		return false, nil
	})

	applyPostgresDatabase(&plan, latest)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if waitErr != nil {
		notReadyError(&resp.Diagnostics, "database", waitErr)
	}
}

// findCreated returns the database named name that is not in before, or nil.
// Two new ones means another client ordered one with the same name at the
// same moment; the newest is taken, since that is the later order.
func (r *postgresDatabaseResource) findCreated(ctx context.Context, projectID, name string, before []client.Database) (*client.Database, error) {
	seen := make(map[string]bool, len(before))
	for _, db := range before {
		seen[db.ID] = true
	}
	after, err := r.data.client.ListDatabases(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var found *client.Database
	for i := range after {
		db := &after[i]
		if db.Name != name || seen[db.ID] {
			continue
		}
		if found == nil || db.CreatedAt > found.CreatedAt {
			found = db
		}
	}
	return found, nil
}

func (r *postgresDatabaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state postgresDatabaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	db, err := r.data.client.GetDatabase(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	// A deleted database's row is kept, with this status.
	if client.IsNotFound(err) || (err == nil && db.Status == "TERMINATED") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read database", err)
		return
	}
	if db.Engine != postgresEngine {
		resp.Diagnostics.AddError("Not a PostgreSQL database",
			fmt.Sprintf("Database %s runs %s, which cosmoner_postgres_database does not manage.", db.ID, db.Engine))
		return
	}

	applyPostgresDatabase(&state, db)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only ever changes timeouts: every other argument replaces.
func (r *postgresDatabaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan postgresDatabaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *postgresDatabaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state postgresDatabaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultDatabaseDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := state.ProjectID.ValueString(), state.ID.ValueString()

	if err := r.data.client.DeleteDatabase(ctx, projectID, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiError(&resp.Diagnostics, "delete database", err)
		return
	}

	err := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		db, err := r.data.client.GetDatabase(ctx, projectID, id)
		if client.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return db.Status == "TERMINATED", nil
	})
	if err != nil {
		deleteWaitError(&resp.Diagnostics, "database", err)
	}
}

func (r *postgresDatabaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

func applyPostgresDatabase(model *postgresDatabaseModel, db *client.Database) {
	model.ID = types.StringValue(db.ID)
	model.Name = types.StringValue(db.Name)
	model.Size = types.StringValue(db.Size)
	if db.Version != nil {
		model.Version = types.StringValue(*db.Version)
	}
	model.Region = types.StringValue(db.Region)
	model.Status = types.StringValue(db.Status)
	model.Host = optionalString(db.Host)
	model.Port = optionalInt64(db.Port)
	model.PublicHost = optionalString(db.PublicHost)
	model.Database = optionalString(db.DefaultDB)
	model.Username = optionalString(db.DefaultUser)
	model.ConnectionURI = optionalString(db.ConnectionURI)
	model.PublicConnectionURI = optionalString(db.PublicConnectionURI)
	model.CreatedAt = types.StringValue(db.CreatedAt)
}
