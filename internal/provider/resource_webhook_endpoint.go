package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*webhookEndpointResource)(nil)
	_ resource.ResourceWithImportState = (*webhookEndpointResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*webhookEndpointResource)(nil)
)

// The API refuses plaintext endpoints.
var httpsURLPattern = regexp.MustCompile(`^https://\S+$`)

type webhookEndpointResource struct {
	resourceBase
}

type webhookEndpointModel struct {
	ID             types.String `tfsdk:"id"`
	ProjectID      types.String `tfsdk:"project_id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	URL            types.String `tfsdk:"url"`
	Events         types.Set    `tfsdk:"events"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	SigningSecret  types.String `tfsdk:"signing_secret"`
	SecretHint     types.String `tfsdk:"secret_hint"`
	DisabledReason types.String `tfsdk:"disabled_reason"`
	CreatedAt      types.String `tfsdk:"created_at"`
}

func newWebhookEndpointResource() resource.Resource {
	return &webhookEndpointResource{}
}

func (r *webhookEndpointResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook_endpoint"
}

func (r *webhookEndpointResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An HTTPS endpoint that receives the project's events, signed with a per-endpoint secret.\n\n" +
			"After 10 deliveries in a row fail, the API pauses the endpoint. With `enabled = true`, the next apply " +
			"turns it back on, which also clears the failure streak.\n\n" +
			"Needs `webhooks:read` and `webhooks:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Endpoint ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 100)},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Free-text description.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 500)},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "Where events are delivered. Must be HTTPS.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.RegexMatches(httpsURLPattern, "must be an https:// URL")},
			},
			// Not validated against a list: the API owns the event vocabulary, and
			// a new event should not need a provider release to subscribe to.
			"events": schema.SetAttribute{
				MarkdownDescription: "Event types to deliver, e.g. `app.deployed`, `email.bounced`, `domain.verified`. " +
					"See the webhooks documentation for the full list.",
				Required:    true,
				ElementType: types.StringType,
				Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether events are delivered. Defaults to `true`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"signing_secret": schema.StringAttribute{
				MarkdownDescription: "Secret for verifying delivery signatures. The API returns it only when the endpoint " +
					"is created, so it is null for an imported endpoint.",
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret_hint": schema.StringAttribute{
				MarkdownDescription: "Last four characters of the signing secret.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"disabled_reason": schema.StringAttribute{
				MarkdownDescription: "`MANUAL` or `CONSECUTIVE_FAILURES` while the endpoint is disabled, otherwise null.",
				Computed:            true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "When the endpoint was created, as an RFC 3339 timestamp.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *webhookEndpointResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookEndpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var events []string
	resp.Diagnostics.Append(plan.Events.ElementsAs(ctx, &events, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, err := r.data.client.CreateWebhookEndpoint(ctx, plan.ProjectID.ValueString(), client.CreateWebhookEndpointInput{
		Name:        plan.Name.ValueString(),
		Description: stringPointer(plan.Description),
		URL:         plan.URL.ValueString(),
		Events:      events,
	})
	if err != nil {
		apiError(&resp.Diagnostics, "create webhook endpoint", err)
		return
	}
	plan.SigningSecret = types.StringValue(endpoint.Secret)

	// New endpoints always start enabled; the create route takes no flag.
	if !plan.Enabled.ValueBool() {
		disabled, err := r.data.client.UpdateWebhookEndpoint(ctx, plan.ProjectID.ValueString(), endpoint.ID, updateWebhookInput(plan, events))
		if err != nil {
			// Saved anyway so the endpoint is not orphaned. Terraform marks it
			// tainted, and the next apply replaces it.
			plan.Enabled = types.BoolValue(endpoint.Enabled)
			resp.Diagnostics.Append(applyWebhookEndpoint(ctx, &plan, endpoint)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
			apiError(&resp.Diagnostics, "disable webhook endpoint", err)
			return
		}
		endpoint = disabled
	}

	resp.Diagnostics.Append(applyWebhookEndpoint(ctx, &plan, endpoint)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *webhookEndpointResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookEndpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, err := r.data.client.GetWebhookEndpoint(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read webhook endpoint", err)
		return
	}

	resp.Diagnostics.Append(applyWebhookEndpoint(ctx, &state, endpoint)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *webhookEndpointResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan webhookEndpointModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var events []string
	resp.Diagnostics.Append(plan.Events.ElementsAs(ctx, &events, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint, err := r.data.client.UpdateWebhookEndpoint(ctx, plan.ProjectID.ValueString(), plan.ID.ValueString(), updateWebhookInput(plan, events))
	if err != nil {
		apiError(&resp.Diagnostics, "update webhook endpoint", err)
		return
	}

	resp.Diagnostics.Append(applyWebhookEndpoint(ctx, &plan, endpoint)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *webhookEndpointResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookEndpointModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.data.client.DeleteWebhookEndpoint(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		apiError(&resp.Diagnostics, "delete webhook endpoint", err)
	}
}

func (r *webhookEndpointResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

// updateWebhookInput sends every field, so the endpoint ends up exactly as
// configured whatever was changed elsewhere. A null description clears it.
func updateWebhookInput(plan webhookEndpointModel, events []string) client.UpdateWebhookEndpointInput {
	return client.UpdateWebhookEndpointInput{
		Name:        plan.Name.ValueString(),
		Description: stringPointer(plan.Description),
		URL:         plan.URL.ValueString(),
		Events:      events,
		Enabled:     plan.Enabled.ValueBool(),
	}
}

// applyWebhookEndpoint copies the API's view into the model. The signing
// secret is left alone: only the create response carries it.
func applyWebhookEndpoint(ctx context.Context, model *webhookEndpointModel, endpoint *client.WebhookEndpoint) diag.Diagnostics {
	events, diags := types.SetValueFrom(ctx, types.StringType, endpoint.Events)

	model.ID = types.StringValue(endpoint.ID)
	model.Name = types.StringValue(endpoint.Name)
	model.Description = optionalString(endpoint.Description)
	model.URL = types.StringValue(endpoint.URL)
	model.Events = events
	model.Enabled = types.BoolValue(endpoint.Enabled)
	model.SecretHint = types.StringValue(endpoint.SecretHint)
	model.DisabledReason = optionalString(endpoint.DisabledReason)
	model.CreatedAt = types.StringValue(endpoint.CreatedAt)
	if model.SigningSecret.IsUnknown() {
		model.SigningSecret = types.StringNull()
	}
	return diags
}
