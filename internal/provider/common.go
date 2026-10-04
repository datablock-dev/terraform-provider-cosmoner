package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// resourceBase carries the configured client into a resource.
type resourceBase struct {
	data *providerData
}

func (b *resourceBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Nil while Terraform validates configuration before the provider block
	// is configured; every method that needs the client runs after that.
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T.", req.ProviderData))
		return
	}
	b.data = data
}

// projectIDAttribute is the `project_id` every project-scoped resource has.
// ModifyPlan fills it in when configuration leaves it out.
func (b *resourceBase) projectIDAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Project the resource belongs to. Defaults to the provider's `project_id`. " +
			"Changing it replaces the resource.",
		Optional:      true,
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// ModifyPlan defaults `project_id` to the provider's project at plan time,
// rather than leaving it unknown until apply, so the plan shows where a
// resource will be created and a change to the provider's project shows up as
// the replacement it causes.
//
// This is resource-level rather than an attribute plan modifier because only
// the configured resource instance can see the provider's project: the
// framework builds the schema, and so the attribute modifiers, on another one.
func (b *resourceBase) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // destroying
	}

	var configured types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project_id"), &configured)...)
	if resp.Diagnostics.HasError() || !configured.IsNull() {
		return
	}

	var prior types.String
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("project_id"), &prior)...)
	}

	var planned types.String
	switch {
	case b.data != nil && b.data.projectID != "":
		planned = types.StringValue(b.data.projectID)
	case !prior.IsNull():
		// Imported without a provider default: keep the project the import named.
		planned = prior
	case b.data == nil:
		// The provider is not configured yet, e.g. its own settings are
		// unknown until apply; leave the decision to the next plan.
		return
	default:
		resp.Diagnostics.AddAttributeError(path.Root("project_id"), "Missing project",
			"Set project_id on this resource, or project_id in the provider block, or the "+envProjectID+" environment variable.")
		return
	}

	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("project_id"), planned)...)
	if !prior.IsNull() && prior.ValueString() != planned.ValueString() {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("project_id"))
	}
}

// importProjectScoped accepts `<project_id>/<id>`, or a bare `<id>` when the
// provider has a default project.
func (b *resourceBase) importProjectScoped(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	projectID, id, found := strings.Cut(req.ID, "/")
	if !found {
		id = projectID
		projectID = ""
		if b.data != nil {
			projectID = b.data.projectID
		}
	}
	if projectID == "" || id == "" || strings.Contains(id, "/") {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected \"<project_id>/<id>\", or \"<id>\" when the provider sets a project_id. Got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), projectID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// apiError reports a failed API call as a diagnostic.
func apiError(diags *diag.Diagnostics, action string, err error) {
	diags.AddError("Cosmoner API error", fmt.Sprintf("Could not %s: %s", action, err))
}

// optionalString maps an API string that is absent, null or empty to a null
// attribute. The API stores an empty description when one is cleared, and
// null when none was ever set; configuration cannot tell the two apart, so
// state does not either.
func optionalString(value *string) types.String {
	if value == nil || *value == "" {
		return types.StringNull()
	}
	return types.StringValue(*value)
}

// stringPointer is the inverse for request bodies: nil for null or unknown.
func stringPointer(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	s := value.ValueString()
	return &s
}
