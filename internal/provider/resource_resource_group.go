package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*resourceGroupResource)(nil)
	_ resource.ResourceWithImportState = (*resourceGroupResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*resourceGroupResource)(nil)
)

type resourceGroupResource struct {
	resourceBase
}

type resourceGroupModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	Name      types.String `tfsdk:"name"`
	ParentID  types.String `tfsdk:"parent_id"`
}

func newResourceGroupResource() resource.Resource {
	return &resourceGroupResource{}
}

func (r *resourceGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resource_group"
}

func (r *resourceGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group that resources are drawn inside on the project graph. Groups change layout " +
			"only, never what a resource does.\n\n" +
			"Deleting a group moves whatever was inside it up a level; no resource is deleted with it.\n\n" +
			"Needs `projects:read` and `projects:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Resource group ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"parent_id": schema.StringAttribute{
				MarkdownDescription: "ID of the group this one is drawn inside. Omit for a top-level group.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
		},
	}
}

func (r *resourceGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan resourceGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.data.client.CreateResourceGroup(ctx, plan.ProjectID.ValueString(), client.CreateResourceGroupInput{
		Name:     plan.Name.ValueString(),
		ParentID: stringPointer(plan.ParentID),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create resource group", err)
		return
	}

	applyResourceGroup(&plan, group)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *resourceGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state resourceGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groups, err := r.data.client.ListResourceGroups(ctx, state.ProjectID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list resource groups", err)
		return
	}

	for i := range groups {
		if groups[i].ID == state.ID.ValueString() {
			applyResourceGroup(&state, &groups[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *resourceGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan resourceGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.data.client.UpdateResourceGroup(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), client.UpdateResourceGroupInput{
		Name:     plan.Name.ValueString(),
		ParentID: stringPointer(plan.ParentID),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "update resource group", err)
		return
	}

	applyResourceGroup(&plan, group)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *resourceGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state resourceGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.data.client.DeleteResourceGroup(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete resource group", err)
	}
}

func (r *resourceGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

// applyResourceGroup copies the API's view into the model, keeping the
// configured name when it differs only by the whitespace the API trims.
func applyResourceGroup(model *resourceGroupModel, group *client.ResourceGroup) {
	model.ID = types.StringValue(group.ID)
	if model.Name.IsNull() || model.Name.IsUnknown() || strings.TrimSpace(model.Name.ValueString()) != group.Name {
		model.Name = types.StringValue(group.Name)
	}
	model.ParentID = optionalString(group.ParentID)
}
