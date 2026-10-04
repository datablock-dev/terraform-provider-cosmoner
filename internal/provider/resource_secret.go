package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure        = (*secretResource)(nil)
	_ resource.ResourceWithImportState      = (*secretResource)(nil)
	_ resource.ResourceWithModifyPlan       = (*secretResource)(nil)
	_ resource.ResourceWithConfigValidators = (*secretResource)(nil)
)

// entryNamePattern is the rule the API enforces on secret and variable names,
// mirrored so a bad name fails at plan time instead of halfway through apply.
var entryNamePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// Lengths the API stores, shared by secrets and variables.
const (
	maxEntryNameLength        = 100
	maxEntryValueLength       = 10_000
	maxEntryDescriptionLength = 500
)

type secretResource struct {
	resourceBase
}

type secretModel struct {
	ID             types.String `tfsdk:"id"`
	ProjectID      types.String `tfsdk:"project_id"`
	Name           types.String `tfsdk:"name"`
	Environment    types.String `tfsdk:"environment"`
	Description    types.String `tfsdk:"description"`
	Value          types.String `tfsdk:"value"`
	ValueWO        types.String `tfsdk:"value_wo"`
	ValueWOVersion types.Int64  `tfsdk:"value_wo_version"`
	Version        types.Int64  `tfsdk:"version"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

func newSecretResource() resource.Resource {
	return &secretResource{}
}

func (r *secretResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *secretResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An encrypted project secret.\n\n" +
			"The API never returns a secret's value after setting it, so the provider cannot read it back. " +
			"It notices a value changed outside Terraform from the secret's `version`, and sets the configured " +
			"value again on the next apply.\n\n" +
			"Set the value with `value_wo` on Terraform 1.11 and later to keep it out of state entirely; " +
			"`value` works everywhere but is stored in state, marked sensitive.\n\n" +
			"Needs `secrets:read` and `secrets:write`, and a key belonging to an owner or admin of the project. " +
			"The API allows 100 secret creations per project every 10 minutes, shared by every key and machine " +
			"working on the project; an apply that creates more stops at a rate-limit error, and applying again " +
			"after the window resets carries on from there.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Secret ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Uppercase letters, digits and underscores, starting with a letter, e.g. `DB_PASSWORD`. " +
					"Unique per environment. Changing it replaces the secret.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthAtMost(maxEntryNameLength),
					stringvalidator.RegexMatches(entryNamePattern, "must be uppercase letters, digits or underscores, and start with a letter"),
				},
			},
			"environment": schema.StringAttribute{
				MarkdownDescription: "`default`, `development`, `staging` or `production`. A `default` entry applies " +
					"everywhere unless an environment-specific one of the same name exists. Defaults to `default`. " +
					"Changing it replaces the secret.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("default"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description. Changing it sets the value again, because the API " +
					"updates both together, which bumps `version`.",
				Optional:   true,
				Validators: []validator.String{stringvalidator.LengthBetween(1, maxEntryDescriptionLength)},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "The secret's value, stored in state. Exactly one of `value` and `value_wo` is required.",
				Optional:            true,
				Sensitive:           true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, maxEntryValueLength),
				},
			},
			"value_wo": schema.StringAttribute{
				MarkdownDescription: "The secret's value, never stored in state or plan. Needs Terraform 1.11 or later. " +
					"Terraform cannot see a change to a write-only value, so bump `value_wo_version` to set a new one.",
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, maxEntryValueLength),
					stringvalidator.AlsoRequires(path.MatchRoot("value_wo_version")),
				},
			},
			"value_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Any number; changing it sets `value_wo` again. Required with `value_wo`.",
				Optional:            true,
				Validators:          []validator.Int64{int64validator.AlsoRequires(path.MatchRoot("value_wo"))},
			},
			"version": schema.Int64Attribute{
				MarkdownDescription: "Incremented by the API each time the value is set, from anywhere.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the secret was created, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the secret was last changed, as an RFC 3339 timestamp.",
				Computed:            true,
			},
		},
	}
}

func (r *secretResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("value"), path.MatchRoot("value_wo")),
	}
}

func (r *secretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	created, err := r.data.client.CreateSecret(ctx, plan.ProjectID.ValueString(), client.CreateSecretInput{
		Name:        plan.Name.ValueString(),
		Value:       secretValue(plan, config),
		Description: stringPointer(plan.Description),
		Environment: plan.Environment.ValueString(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create secret", err)
		return
	}

	// A failed read-back in refresh only warns, so the secret always reaches
	// state and is never orphaned.
	plan.ID = types.StringValue(created.ID)
	r.refresh(ctx, &plan, created, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret, err := r.data.client.GetSecret(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read secret", err)
		return
	}

	// The value cannot be read back, but every write bumps the version. A
	// version other than the one this provider last saw means someone set the
	// value elsewhere, so forget what was written: the configured value then
	// differs from state, and the next plan sets it again.
	if !state.Version.IsNull() && secret.Version != state.Version.ValueInt64() {
		state.Value = types.StringNull()
		state.ValueWOVersion = types.Int64Null()
	}

	state.Name = types.StringValue(secret.Name)
	state.Environment = types.StringValue(secret.Environment)
	state.Description = optionalString(secret.Description)
	state.Version = types.Int64Value(secret.Version)
	state.CreatedAt = types.StringValue(secret.CreatedAt)
	state.UpdatedAt = types.StringValue(secret.UpdatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *secretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config, state secretModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The API keeps the stored description when none is sent, so removing it
	// from configuration has to send an empty one.
	description := stringPointer(plan.Description)
	if description == nil && !state.Description.IsNull() {
		description = new(string)
	}

	updated, err := r.data.client.UpdateSecret(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), client.UpdateSecretInput{
		Value:       secretValue(plan, config),
		Description: description,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "update secret", err)
		return
	}

	r.refresh(ctx, &plan, updated, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *secretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state secretModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.data.client.DeleteSecret(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete secret", err)
	}
}

func (r *secretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

// refresh fills the computed attributes after a write. Neither write returns
// both timestamps, so the secret is read back; if that read fails, the write's
// own response fills what it can and the next refresh corrects the rest.
func (r *secretResource) refresh(ctx context.Context, model *secretModel, written *client.Secret, diags *diag.Diagnostics) {
	secret, err := r.data.client.GetSecret(ctx, model.ProjectID.ValueString(), model.ID.ValueString())
	if err != nil {
		diags.AddWarning("Could not read the secret back", err.Error())
		secret = written
		if secret.CreatedAt == "" {
			secret.CreatedAt = model.CreatedAt.ValueString()
		}
		if secret.UpdatedAt == "" {
			secret.UpdatedAt = secret.CreatedAt
		}
	}

	model.Version = types.Int64Value(secret.Version)
	model.CreatedAt = types.StringValue(secret.CreatedAt)
	model.UpdatedAt = types.StringValue(secret.UpdatedAt)
	// Write-only attributes are always null in state.
	model.ValueWO = types.StringNull()
}

// secretValue is the configured value, from whichever attribute holds it.
// Write-only values exist only in configuration, never in the plan.
func secretValue(plan, config secretModel) string {
	if !config.ValueWO.IsNull() {
		return config.ValueWO.ValueString()
	}
	return plan.Value.ValueString()
}
