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
	_ resource.ResourceWithConfigure   = (*sshKeyResource)(nil)
	_ resource.ResourceWithImportState = (*sshKeyResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*sshKeyResource)(nil)
)

type sshKeyResource struct {
	resourceBase
}

type sshKeyModel struct {
	ID          types.String `tfsdk:"id"`
	ProjectID   types.String `tfsdk:"project_id"`
	Name        types.String `tfsdk:"name"`
	PublicKey   types.String `tfsdk:"public_key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func newSSHKeyResource() resource.Resource {
	return &sshKeyResource{}
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A public key installed on dedicated servers created after it is registered.\n\n" +
			"Keys cannot be edited, so any change replaces the key. Removing a key does not revoke it on servers " +
			"it was already installed on.\n\n" +
			"Needs `servers:read` and `servers:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "SSH key ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Label for the key, e.g. the machine it belongs to.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"public_key": schema.StringAttribute{
				MarkdownDescription: "The key in `authorized_keys` form, e.g. `ssh-ed25519 AAAA… me@laptop`. " +
					"`file(\"~/.ssh/id_ed25519.pub\")` works as is. A key can be registered once per project.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"fingerprint": schema.StringAttribute{
				MarkdownDescription: "OpenSSH SHA256 fingerprint of the key.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the key was registered, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key, err := r.data.client.CreateSSHKey(ctx, plan.ProjectID.ValueString(), client.CreateSSHKeyInput{
		Name:      plan.Name.ValueString(),
		PublicKey: plan.PublicKey.ValueString(),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "register SSH key", err)
		return
	}

	applySSHKey(&plan, key)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keys, err := r.data.client.ListSSHKeys(ctx, state.ProjectID.ValueString())
	if err != nil {
		apiError(&resp.Diagnostics, "list SSH keys", err)
		return
	}

	for i := range keys {
		if keys[i].ID == state.ID.ValueString() {
			applySSHKey(&state, &keys[i])
			resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

// Update is never called: every configurable attribute requires replacement.
func (r *sshKeyResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.data.client.DeleteSSHKey(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete SSH key", err)
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

// applySSHKey copies the API's view into the model.
//
// The API trims the name and collapses whitespace in the key, so the key read
// from a .pub file comes back without its trailing newline. Configuration is
// kept when it means the same thing, or every apply would report a change and
// every plan would replace the key.
func applySSHKey(model *sshKeyModel, key *client.SSHKey) {
	model.ID = types.StringValue(key.ID)
	if model.Name.IsNull() || model.Name.IsUnknown() || strings.TrimSpace(model.Name.ValueString()) != key.Name {
		model.Name = types.StringValue(key.Name)
	}
	if model.PublicKey.IsNull() || model.PublicKey.IsUnknown() || normalizeSSHPublicKey(model.PublicKey.ValueString()) != key.PublicKey {
		model.PublicKey = types.StringValue(key.PublicKey)
	}
	model.Fingerprint = types.StringValue(key.Fingerprint)
	model.CreatedAt = types.StringValue(key.CreatedAt)
}

// normalizeSSHPublicKey matches the API's normalisation.
func normalizeSSHPublicKey(publicKey string) string {
	return strings.Join(strings.Fields(publicKey), " ")
}
