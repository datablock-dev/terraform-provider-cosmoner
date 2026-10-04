package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ resource.ResourceWithConfigure   = (*variableResource)(nil)
	_ resource.ResourceWithImportState = (*variableResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*variableResource)(nil)
)

type variableResource struct {
	resourceBase
}

type variableModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	Name        types.String `tfsdk:"name"`
	Environment types.String `tfsdk:"environment"`
	Description types.String `tfsdk:"description"`
	Value       types.String `tfsdk:"value"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func newVariableResource() resource.Resource {
	return &variableResource{}
}

func (r *variableResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_variable"
}

func (r *variableResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A project variable: non-sensitive configuration, stored and returned in plaintext. " +
			"Anything worth hiding belongs in `cosmoner_secret`.\n\n" +
			"Needs `variables:read` and `variables:write`, and a key belonging to an owner or admin of the project.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Variable ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Uppercase letters, digits and underscores, starting with a letter, e.g. `LOG_LEVEL`. " +
					"Unique per environment. Changing it replaces the variable.",
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
					"Changing it replaces the variable.",
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("default"),
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, maxEntryDescriptionLength)},
			},
			"value": schema.StringAttribute{
				MarkdownDescription: "The variable's value.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, maxEntryValueLength)},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the variable was created, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "When the variable was last changed, as an RFC 3339 timestamp.",
				Computed:            true,
			},
		},
	}
}

func (r *variableResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan variableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	variable, err := r.data.client.CreateVariable(ctx, plan.ProjectID.ValueString(), client.CreateVariableInput{
		Name:        plan.Name.ValueString(),
		Value:       plan.Value.ValueString(),
		Description: stringPointer(plan.Description),
		Environment: plan.Environment.ValueString(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create variable", err)
		return
	}

	applyVariable(&plan, variable)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *variableResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state variableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	variable, err := r.data.client.GetVariable(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read variable", err)
		return
	}

	applyVariable(&state, variable)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *variableResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state variableModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
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

	variable, err := r.data.client.UpdateVariable(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), client.UpdateVariableInput{
		Value:       stringPointer(plan.Value),
		Description: description,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "update variable", err)
		return
	}

	applyVariable(&plan, variable)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *variableResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state variableModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.data.client.DeleteVariable(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete variable", err)
	}
}

func (r *variableResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

func applyVariable(model *variableModel, variable *client.Variable) {
	model.ID = types.StringValue(variable.ID)
	model.Name = types.StringValue(variable.Name)
	model.Environment = types.StringValue(variable.Environment)
	model.Description = optionalString(variable.Description)
	model.Value = types.StringValue(variable.Value)
	model.CreatedAt = types.StringValue(variable.CreatedAt)
	model.UpdatedAt = types.StringValue(variable.UpdatedAt)
}
