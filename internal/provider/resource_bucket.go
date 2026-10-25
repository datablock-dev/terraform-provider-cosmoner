package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*bucketResource)(nil)
	_ resource.ResourceWithImportState = (*bucketResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*bucketResource)(nil)
)

const (
	defaultBucketCreateTimeout = 10 * time.Minute
	defaultBucketDeleteTimeout = 10 * time.Minute
)

var (
	bucketNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*[a-z0-9]$`)
	// A folder in the bucket policy: segments ending in `/`, so `img/` cannot
	// also match `img-old/`. The API normalises other spellings into this one;
	// requiring it here keeps the configured value and the stored one equal.
	publicPathPattern = regexp.MustCompile(`^([A-Za-z0-9._-]+/)+$`)
)

type bucketResource struct {
	resourceBase
}

type bucketModel struct {
	ID           types.String   `tfsdk:"id"`
	ProjectID    types.String   `tfsdk:"project_id"`
	Name         types.String   `tfsdk:"name"`
	Region       types.String   `tfsdk:"region"`
	Tier         types.String   `tfsdk:"tier"`
	PublicAccess types.Bool     `tfsdk:"public_access"`
	PublicPaths  types.Set      `tfsdk:"public_paths"`
	Versioning   types.Bool     `tfsdk:"versioning"`
	CDNEnabled   types.Bool     `tfsdk:"cdn_enabled"`
	CORSRules    types.List     `tfsdk:"cors_rules"`
	BucketName   types.String   `tfsdk:"bucket_name"`
	Endpoint     types.String   `tfsdk:"endpoint"`
	CDNDomain    types.String   `tfsdk:"cdn_domain"`
	Status       types.String   `tfsdk:"status"`
	CreatedAt    types.String   `tfsdk:"created_at"`
	Timeouts     timeouts.Value `tfsdk:"timeouts"`
}

type corsRuleModel struct {
	AllowedOrigins types.List  `tfsdk:"allowed_origins"`
	AllowedMethods types.List  `tfsdk:"allowed_methods"`
	AllowedHeaders types.List  `tfsdk:"allowed_headers"`
	ExposeHeaders  types.List  `tfsdk:"expose_headers"`
	MaxAgeSeconds  types.Int64 `tfsdk:"max_age_seconds"`
}

var corsRuleAttrTypes = map[string]attr.Type{
	"allowed_origins": types.ListType{ElemType: types.StringType},
	"allowed_methods": types.ListType{ElemType: types.StringType},
	"allowed_headers": types.ListType{ElemType: types.StringType},
	"expose_headers":  types.ListType{ElemType: types.StringType},
	"max_age_seconds": types.Int64Type,
}

func newBucketResource() resource.Resource {
	return &bucketResource{}
}

func (r *bucketResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bucket"
}

func (r *bucketResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	emptyStrings := types.ListValueMust(types.StringType, []attr.Value{})

	resp.Schema = schema.Schema{
		MarkdownDescription: "An S3-compatible object storage bucket. **Billed from creation** by tier, plus metered overage " +
			"and, with `cdn_enabled`, metered CDN traffic.\n\n" +
			"The bucket is ready when the API answers, and `apply` waits for it to report `ACTIVE`, up to " +
			"`timeouts.create`. A create the API refuses leaves nothing behind.\n\n" +
			"`tier`, `public_access`, `public_paths`, `cors_rules` and `cdn_enabled` change in place; a tier change or " +
			"turning the CDN on is invoiced immediately. The API cannot change `versioning`, so changing it replaces the " +
			"bucket **and deletes every object in it**.\n\n" +
			"Access keys are managed separately. Needs `object-storage:read` and `object-storage:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Bucket ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Name, unique in the project: 3 to 33 lowercase letters, digits and inner hyphens.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators: []validator.String{
					stringvalidator.LengthBetween(3, 33),
					stringvalidator.RegexMatches(bucketNamePattern, "must be lowercase letters, digits or hyphens, and cannot start or end with a hyphen"),
				},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Region to create the bucket in, e.g. `eu-north-1`.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"tier": schema.StringAttribute{
				MarkdownDescription: "Plan: `STARTER` (the default), `GROWTH`, `SCALE` or `ENTERPRISE`. The API refuses a " +
					"downgrade below current usage.",
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("STARTER"),
			},
			"public_access": schema.BoolAttribute{
				MarkdownDescription: "Whether objects are publicly readable. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"public_paths": schema.SetAttribute{
				MarkdownDescription: "Folders that public reads and the CDN serve, each ending in `/`, e.g. `images/`. " +
					"Empty, the default, covers the whole bucket. Up to 10.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				Validators: []validator.Set{
					setvalidator.SizeAtMost(10),
					setvalidator.ValueStringsAre(
						stringvalidator.LengthBetween(1, 256),
						stringvalidator.RegexMatches(publicPathPattern, "must be folders of letters, digits, '.', '_' and '-', ending in '/'"),
						noDotSegments{},
					),
				},
			},
			"versioning": schema.BoolAttribute{
				MarkdownDescription: "Whether object versioning is on. Defaults to `false`. Changing it replaces the bucket and deletes its objects.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.RequiresReplace()},
			},
			"cdn_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the metered CDN add-on serves the bucket. Defaults to `false`. Needs `public_access`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"cors_rules": schema.ListNestedAttribute{
				MarkdownDescription: "CORS rules for browser access. Empty, the default, allows no cross-origin requests.",
				Optional:            true,
				Computed:            true,
				Default:             listdefault.StaticValue(types.ListValueMust(types.ObjectType{AttrTypes: corsRuleAttrTypes}, []attr.Value{})),
				Validators:          []validator.List{listvalidator.SizeAtMost(100)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"allowed_origins": schema.ListAttribute{
							MarkdownDescription: "Origins allowed to make requests; `*` allows any.",
							Required:            true,
							ElementType:         types.StringType,
							Validators: []validator.List{
								listvalidator.SizeBetween(1, 100),
								listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 253)),
							},
						},
						"allowed_methods": schema.ListAttribute{
							MarkdownDescription: "Methods allowed: `GET`, `PUT`, `POST`, `DELETE`, `HEAD`.",
							Required:            true,
							ElementType:         types.StringType,
							Validators: []validator.List{
								listvalidator.SizeAtLeast(1),
								listvalidator.ValueStringsAre(stringvalidator.OneOf("GET", "PUT", "POST", "DELETE", "HEAD")),
							},
						},
						"allowed_headers": schema.ListAttribute{
							MarkdownDescription: "Request headers allowed. Defaults to none.",
							Optional:            true,
							Computed:            true,
							ElementType:         types.StringType,
							Default:             listdefault.StaticValue(emptyStrings),
							Validators: []validator.List{
								listvalidator.SizeAtMost(100),
								listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 253)),
							},
						},
						"expose_headers": schema.ListAttribute{
							MarkdownDescription: "Response headers browsers may read. Defaults to none.",
							Optional:            true,
							Computed:            true,
							ElementType:         types.StringType,
							Default:             listdefault.StaticValue(emptyStrings),
							Validators: []validator.List{
								listvalidator.SizeAtMost(100),
								listvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 253)),
							},
						},
						"max_age_seconds": schema.Int64Attribute{
							MarkdownDescription: "How long browsers may cache the preflight response, up to 86400.",
							Optional:            true,
							Validators:          []validator.Int64{int64validator.Between(0, 86400)},
						},
					},
				},
			},
			"bucket_name": computedString("Name of the bucket on the S3-compatible endpoint, which clients address."),
			"endpoint":    computedString("S3-compatible endpoint URL."),
			"cdn_domain": schema.StringAttribute{
				MarkdownDescription: "Domain the CDN serves the bucket on while `cdn_enabled`, otherwise null.",
				Computed:            true,
			},
			"status":     computedString("`ACTIVE` once ready."),
			"created_at": computedString("When the bucket was created, as an RFC 3339 timestamp."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create:            true,
				Delete:            true,
				CreateDescription: "How long to wait for the bucket to become active. Defaults to `10m`.",
				DeleteDescription: "How long to wait for the bucket to be deleted. Defaults to `10m`.",
			}),
		},
	}
}

func (r *bucketResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultBucketCreateTimeout)
	resp.Diagnostics.Append(diags...)
	paths, rules := bucketInputs(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, name := plan.ProjectID.ValueString(), plan.Name.ValueString()

	createErr := r.data.client.CreateBucket(ctx, projectID, client.CreateBucketInput{
		Name:         name,
		Region:       plan.Region.ValueString(),
		Tier:         plan.Tier.ValueString(),
		PublicAccess: plan.PublicAccess.ValueBool(),
		PublicPaths:  paths,
		Versioning:   plan.Versioning.ValueBool(),
		CORSRules:    rules,
		CDNEnabled:   plan.CDNEnabled.ValueBool(),
	})
	if createErr != nil && !outcomeUnknown(createErr) {
		apiError(&resp.Diagnostics, "create bucket", createErr)
		return
	}

	// No id in the response; names are unique in a project.
	created, err := r.find(ctx, projectID, func(b client.Bucket) bool { return b.Name == name })
	switch {
	case err != nil && createErr != nil:
		apiError(&resp.Diagnostics, "create bucket", fmt.Errorf("%w (and checking whether it was created failed: %s)", createErr, err))
		return
	case err != nil:
		apiError(&resp.Diagnostics, "find the created bucket", err)
		return
	case created == nil && createErr != nil:
		apiError(&resp.Diagnostics, "create bucket", createErr)
		return
	case created == nil:
		resp.Diagnostics.AddError("Created bucket not found",
			fmt.Sprintf("The API accepted the bucket, but no bucket named %q is listed in the project.", name))
		return
	}

	latest := created
	waitErr := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		b, err := r.find(ctx, projectID, func(b client.Bucket) bool { return b.ID == created.ID })
		if err != nil {
			return false, err
		}
		if b == nil {
			return false, &errNotReady{status: "missing", reason: "the bucket is no longer listed"}
		}
		latest = b
		switch b.Status {
		case "ACTIVE":
			return true, nil
		case "SUSPENDED", "DELETING":
			return false, &errNotReady{status: b.Status}
		}
		return false, nil
	})

	resp.Diagnostics.Append(applyBucket(ctx, &plan, latest)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if waitErr != nil {
		notReadyError(&resp.Diagnostics, "bucket", waitErr)
	}
}

func (r *bucketResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	b, err := r.find(ctx, state.ProjectID.ValueString(), func(b client.Bucket) bool { return b.ID == id })
	if err != nil {
		apiError(&resp.Diagnostics, "read bucket", err)
		return
	}
	if b == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyBucket(ctx, &state, b)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update sends one request per setting that changed, since each has its own
// route and the tier and CDN routes refuse a no-op. If one fails partway, the
// bucket is read back so state records what did change.
func (r *bucketResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state bucketModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	paths, rules := bucketInputs(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := plan.ProjectID.ValueString(), plan.ID.ValueString()
	c := r.data.client

	// Each step runs only if the earlier ones succeeded.
	var err error
	var action string
	step := func(changed bool, what string, call func() error) {
		if err == nil && changed {
			action, err = what, call()
		}
	}
	step(!plan.Tier.Equal(state.Tier), "change bucket tier", func() error {
		return c.UpdateBucketTier(ctx, projectID, id, plan.Tier.ValueString())
	})
	step(!plan.PublicAccess.Equal(state.PublicAccess) || !plan.PublicPaths.Equal(state.PublicPaths), "change bucket access", func() error {
		return c.UpdateBucketAccess(ctx, projectID, id, plan.PublicAccess.ValueBool(), paths)
	})
	step(!plan.CORSRules.Equal(state.CORSRules), "change bucket CORS rules", func() error {
		if rules == nil {
			rules = []client.CORSRule{} // an empty list clears them; null is refused
		}
		return c.UpdateBucketCORS(ctx, projectID, id, rules)
	})
	step(!plan.CDNEnabled.Equal(state.CDNEnabled), "change bucket CDN", func() error {
		return c.UpdateBucketCDN(ctx, projectID, id, plan.CDNEnabled.ValueBool())
	})

	b, readErr := r.find(ctx, projectID, func(b client.Bucket) bool { return b.ID == id })
	switch {
	case readErr != nil:
		apiError(&resp.Diagnostics, "read bucket", readErr)
	case b == nil:
		resp.Diagnostics.AddError("Bucket not found", fmt.Sprintf("Bucket %s is no longer listed in the project.", id))
	default:
		target := plan
		if err != nil {
			target = state
		}
		target.Timeouts = plan.Timeouts
		resp.Diagnostics.Append(applyBucket(ctx, &target, b)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, target)...)
	}
	if err != nil {
		apiError(&resp.Diagnostics, action, err)
	}
}

func (r *bucketResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state bucketModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultBucketDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := state.ProjectID.ValueString(), state.ID.ValueString()

	if err := r.data.client.DeleteBucket(ctx, projectID, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiError(&resp.Diagnostics, "delete bucket", err)
		return
	}

	err := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		b, err := r.find(ctx, projectID, func(b client.Bucket) bool { return b.ID == id })
		return err == nil && b == nil, err
	})
	if err != nil {
		deleteWaitError(&resp.Diagnostics, "bucket", err)
	}
}

func (r *bucketResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

// find returns the first listed bucket that match accepts, or nil. There is
// no route for one bucket.
func (r *bucketResource) find(ctx context.Context, projectID string, match func(client.Bucket) bool) (*client.Bucket, error) {
	buckets, err := r.data.client.ListBuckets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range buckets {
		if match(buckets[i]) {
			return &buckets[i], nil
		}
	}
	return nil, nil
}

// bucketInputs converts the collection attributes into request values.
func bucketInputs(ctx context.Context, plan bucketModel, diags *diag.Diagnostics) ([]string, []client.CORSRule) {
	paths := []string{}
	diags.Append(plan.PublicPaths.ElementsAs(ctx, &paths, false)...)

	var models []corsRuleModel
	diags.Append(plan.CORSRules.ElementsAs(ctx, &models, false)...)
	var rules []client.CORSRule
	for _, m := range models {
		rule := client.CORSRule{MaxAgeSeconds: int64Pointer(m.MaxAgeSeconds)}
		diags.Append(m.AllowedOrigins.ElementsAs(ctx, &rule.AllowedOrigins, false)...)
		diags.Append(m.AllowedMethods.ElementsAs(ctx, &rule.AllowedMethods, false)...)
		diags.Append(m.AllowedHeaders.ElementsAs(ctx, &rule.AllowedHeaders, false)...)
		diags.Append(m.ExposeHeaders.ElementsAs(ctx, &rule.ExposeHeaders, false)...)
		rules = append(rules, rule)
	}
	return paths, rules
}

func applyBucket(ctx context.Context, model *bucketModel, b *client.Bucket) diag.Diagnostics {
	var diags diag.Diagnostics

	paths := b.PublicPaths
	if paths == nil {
		paths = []string{}
	}
	pathSet, d := types.SetValueFrom(ctx, types.StringType, paths)
	diags.Append(d...)

	rules := make([]corsRuleModel, 0, len(b.CORSRules))
	for _, rule := range b.CORSRules {
		m := corsRuleModel{MaxAgeSeconds: optionalInt64(rule.MaxAgeSeconds)}
		m.AllowedOrigins, d = types.ListValueFrom(ctx, types.StringType, nonNil(rule.AllowedOrigins))
		diags.Append(d...)
		m.AllowedMethods, d = types.ListValueFrom(ctx, types.StringType, nonNil(rule.AllowedMethods))
		diags.Append(d...)
		m.AllowedHeaders, d = types.ListValueFrom(ctx, types.StringType, nonNil(rule.AllowedHeaders))
		diags.Append(d...)
		m.ExposeHeaders, d = types.ListValueFrom(ctx, types.StringType, nonNil(rule.ExposeHeaders))
		diags.Append(d...)
		rules = append(rules, m)
	}
	ruleList, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: corsRuleAttrTypes}, rules)
	diags.Append(d...)

	model.ID = types.StringValue(b.ID)
	model.Name = types.StringValue(b.Name)
	model.Region = types.StringValue(b.Region)
	model.Tier = types.StringValue(b.Tier)
	model.PublicAccess = types.BoolValue(b.PublicAccess)
	model.PublicPaths = pathSet
	model.Versioning = types.BoolValue(b.Versioning)
	model.CDNEnabled = types.BoolValue(b.CDNEnabled)
	model.CORSRules = ruleList
	model.BucketName = optionalString(b.S3BucketName)
	model.Endpoint = optionalString(b.S3Endpoint)
	model.CDNDomain = optionalString(b.CDNDomain)
	model.Status = types.StringValue(b.Status)
	model.CreatedAt = types.StringValue(b.CreatedAt)
	return diags
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// noDotSegments refuses `.` and `..` as folders, which the API refuses too:
// they are not folders, and in a policy path they would reach outside one.
type noDotSegments struct{}

func (noDotSegments) Description(context.Context) string {
	return "must not contain '.' or '..' as a folder"
}

func (v noDotSegments) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v noDotSegments) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, segment := range strings.Split(req.ConfigValue.ValueString(), "/") {
		if segment == "." || segment == ".." {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid public path", "The path "+v.Description(ctx)+".")
			return
		}
	}
}
