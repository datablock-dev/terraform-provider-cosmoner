package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*redisDatabaseResource)(nil)
	_ resource.ResourceWithImportState = (*redisDatabaseResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*redisDatabaseResource)(nil)
)

const (
	defaultRedisCreateTimeout = 15 * time.Minute
	defaultRedisDeleteTimeout = 15 * time.Minute
)

type redisDatabaseResource struct {
	resourceBase
}

type redisDatabaseModel struct {
	ID          types.String   `tfsdk:"id"`
	ProjectID   types.String   `tfsdk:"project_id"`
	Name        types.String   `tfsdk:"name"`
	Plan        types.String   `tfsdk:"plan"`
	Region      types.String   `tfsdk:"region"`
	Persistence types.String   `tfsdk:"persistence"`
	Engine      types.String   `tfsdk:"engine"`
	Version     types.String   `tfsdk:"engine_version"`
	MemoryMB    types.Int64    `tfsdk:"memory_mb"`
	Status      types.String   `tfsdk:"status"`
	Host        types.String   `tfsdk:"host"`
	Port        types.Int64    `tfsdk:"port"`
	Password    types.String   `tfsdk:"password"`
	CreatedAt   types.String   `tfsdk:"created_at"`
	Timeouts    timeouts.Value `tfsdk:"timeouts"`
}

func newRedisDatabaseResource() resource.Resource {
	return &redisDatabaseResource{}
}

func (r *redisDatabaseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_redis_database"
}

func (r *redisDatabaseResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A managed Redis-compatible (Valkey) database. **Billed from creation**, to the project's saved card.\n\n" +
			"`apply` waits until the database is active, up to `timeouts.create` (default 15 minutes). A database " +
			"that ends in an error, or is still starting at the deadline, is kept in state as tainted, so the next " +
			"apply replaces it; the provider never deletes it on its own. A create the API refuses leaves nothing behind.\n\n" +
			"The API has no route to change a database, so changing any argument replaces it, **and its data with " +
			"it**.\n\n" +
			"Needs `redis:read` and `redis:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Database ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Name: lowercase letters, digits and inner hyphens, up to 40 characters.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 40),
					stringvalidator.RegexMatches(dnsLabelPattern, "must be lowercase letters, digits or hyphens, and cannot start or end with a hyphen"),
				},
			},
			"plan": schema.StringAttribute{
				MarkdownDescription: "Plan slug from the Redis catalogue, e.g. `valkey-1gb`. The plan decides the memory and what runs the database.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(catalogueSlugPattern, "must be a catalogue slug"),
				},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Region slug from the Redis catalogue. The API refuses a region the plan is not sold in.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"persistence": schema.StringAttribute{
				MarkdownDescription: "How data survives a restart: `NONE` (the default), `AOF_EVERY_WRITE`, `AOF_EVERY_1_SECOND`, " +
					"or `SNAPSHOT_EVERY_1_HOUR`, `_6_HOURS`, `_12_HOURS`. The API refuses a mode the plan does not offer.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("NONE"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"engine":         computedString("`VALKEY` or `REDIS`."),
			"engine_version": computedString("Engine version the database runs."),
			"memory_mb": schema.Int64Attribute{
				MarkdownDescription: "Memory the plan provides, in MB.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"status": computedString("`ACTIVE` once ready; otherwise `CREATING` or `ERROR`."),
			"host":   computedString("Host to connect to."),
			"port": schema.Int64Attribute{
				MarkdownDescription: "Port to connect on.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password to authenticate with. Readable with `redis:read`.",
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
				CreateDescription: "How long to wait for the database to become active. Defaults to `15m`.",
				DeleteDescription: "How long to wait for the database to be deleted. Defaults to `15m`.",
			}),
		},
	}
}

func (r *redisDatabaseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan redisDatabaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultRedisCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, name := plan.ProjectID.ValueString(), plan.Name.ValueString()

	// No id in the create response, and names are not unique: see the
	// PostgreSQL resource, which finds its database the same way.
	existing, err := r.data.client.ListRedis(ctx, projectID)
	if err != nil {
		apiError(&resp.Diagnostics, "list Redis databases", err)
		return
	}

	createErr := r.data.client.CreateRedis(ctx, projectID, client.CreateRedisInput{
		Name:            name,
		PlanSlug:        plan.Plan.ValueString(),
		Region:          plan.Region.ValueString(),
		DataPersistence: plan.Persistence.ValueString(),
	})
	if createErr != nil && !outcomeUnknown(createErr) {
		apiError(&resp.Diagnostics, "create Redis database", createErr)
		return
	}

	created, err := r.findCreated(ctx, projectID, name, existing)
	switch {
	case err != nil && createErr != nil:
		apiError(&resp.Diagnostics, "create Redis database", fmt.Errorf("%w (and checking whether it was created failed: %s)", createErr, err))
		return
	case err != nil:
		apiError(&resp.Diagnostics, "find the created Redis database", err)
		return
	case created == nil && createErr != nil:
		apiError(&resp.Diagnostics, "create Redis database", createErr)
		return
	case created == nil:
		resp.Diagnostics.AddError("Created Redis database not found",
			fmt.Sprintf("The API accepted the database, but no new database named %q is listed in the project. "+
				"Check the control panel before applying again, or a second one will be ordered.", name))
		return
	}

	latest := created
	waitErr := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		db, err := r.data.client.GetRedis(ctx, projectID, created.ID)
		if err != nil {
			return false, err
		}
		latest = db
		switch db.Status {
		case "ACTIVE":
			return true, nil
		case "ERROR", "TERMINATED":
			return false, &errNotReady{status: db.Status}
		}
		return false, nil
	})

	applyRedisDatabase(&plan, latest)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if waitErr != nil {
		notReadyError(&resp.Diagnostics, "Redis database", waitErr)
	}
}

func (r *redisDatabaseResource) findCreated(ctx context.Context, projectID, name string, before []client.RedisDatabase) (*client.RedisDatabase, error) {
	seen := make(map[string]bool, len(before))
	for _, db := range before {
		seen[db.ID] = true
	}
	after, err := r.data.client.ListRedis(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var found *client.RedisDatabase
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

func (r *redisDatabaseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state redisDatabaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	db, err := r.data.client.GetRedis(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) || (err == nil && db.Status == "TERMINATED") {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read Redis database", err)
		return
	}

	applyRedisDatabase(&state, db)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update only ever changes timeouts: every other argument replaces.
func (r *redisDatabaseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan redisDatabaseModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *redisDatabaseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state redisDatabaseModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultRedisDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := state.ProjectID.ValueString(), state.ID.ValueString()

	if err := r.data.client.DeleteRedis(ctx, projectID, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiError(&resp.Diagnostics, "delete Redis database", err)
		return
	}

	err := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		db, err := r.data.client.GetRedis(ctx, projectID, id)
		if client.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return db.Status == "TERMINATED", nil
	})
	if err != nil {
		deleteWaitError(&resp.Diagnostics, "Redis database", err)
	}
}

func (r *redisDatabaseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

func applyRedisDatabase(model *redisDatabaseModel, db *client.RedisDatabase) {
	model.ID = types.StringValue(db.ID)
	model.Name = types.StringValue(db.Name)
	model.Plan = types.StringValue(db.PlanSlug)
	model.Region = types.StringValue(db.Region)
	model.Persistence = types.StringValue(db.DataPersistence)
	model.Engine = types.StringValue(db.Engine)
	model.Version = optionalString(db.EngineVersion)
	model.MemoryMB = types.Int64Value(db.MemoryMB)
	model.Status = types.StringValue(db.Status)
	model.Host = optionalString(db.Host)
	model.Port = optionalInt64(db.Port)
	// The list carries no password; keep what the detail read gave.
	if db.Password != nil || model.Password.IsUnknown() {
		model.Password = optionalString(db.Password)
	}
	model.CreatedAt = types.StringValue(db.CreatedAt)
}
