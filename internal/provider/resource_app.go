package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/datablock-dev/terraform-provider-cosmoner/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = (*appResource)(nil)
	_ resource.ResourceWithImportState = (*appResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*appResource)(nil)
)

const (
	defaultAppCreateTimeout = 20 * time.Minute
	defaultAppDeleteTimeout = 10 * time.Minute
	// The size every static site is stored with, whatever the draft said.
	staticAppSize = "shared-static"
)

// legacyAppSizes are retired slugs the API still accepts and stores under
// their current name (canonicalAppSku in the platform).
var legacyAppSizes = map[string]string{
	"k8s-xs":     "shared-xs",
	"k8s-s":      "shared-s",
	"k8s-m":      "shared-m",
	"k8s-l":      "shared-l",
	"k8s-xl":     "shared-xl",
	"k8s-static": "shared-static",
}

// The API trims display names, so a name it would change is refused here.
var trimmedPattern = regexp.MustCompile(`^\S(.*\S)?$`)

type appResource struct {
	resourceBase
}

type appModel struct {
	ID                types.String   `tfsdk:"id"`
	ProjectID         types.String   `tfsdk:"project_id"`
	Name              types.String   `tfsdk:"name"`
	Size              types.String   `tfsdk:"size"`
	Region            types.String   `tfsdk:"region"`
	AppType           types.String   `tfsdk:"app_type"`
	GitProvider       types.String   `tfsdk:"git_provider"`
	GitRepo           types.String   `tfsdk:"git_repo"`
	GitBranch         types.String   `tfsdk:"git_branch"`
	SourceDir         types.String   `tfsdk:"source_dir"`
	BuildStrategy     types.String   `tfsdk:"build_strategy"`
	ContainerRegistry types.String   `tfsdk:"container_registry"`
	ContainerImage    types.String   `tfsdk:"container_image"`
	ContainerPort     types.Int64    `tfsdk:"container_port"`
	BuildCommand      types.String   `tfsdk:"build_command"`
	RunCommand        types.String   `tfsdk:"run_command"`
	OutputDir         types.String   `tfsdk:"output_dir"`
	PublicPort        types.Int64    `tfsdk:"public_port"`
	InternalPort      types.Int64    `tfsdk:"internal_port"`
	AutoDeploy        types.Bool     `tfsdk:"auto_deploy"`
	ImageDeployPolicy types.String   `tfsdk:"image_deploy_policy"`
	Instances         types.Int64    `tfsdk:"instances"`
	Subdomain         types.String   `tfsdk:"subdomain"`
	URL               types.String   `tfsdk:"url"`
	Status            types.String   `tfsdk:"status"`
	CreatedAt         types.String   `tfsdk:"created_at"`
	Timeouts          timeouts.Value `tfsdk:"timeouts"`
}

func newAppResource() resource.Resource {
	return &appResource{}
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	port := []validator.Int64{int64validator.Between(1, 65535)}

	resp.Schema = schema.Schema{
		MarkdownDescription: "An app built from a git repository or run from a container image, served on a generated " +
			"Cosmoner subdomain. **Billed from creation** at its size, to the project's saved card.\n\n" +
			"`apply` waits until the first deploy is running, up to `timeouts.create` (default 20 minutes). An app " +
			"whose deploy fails, or is still deploying at the deadline, is kept in state as tainted, so the next " +
			"apply replaces it; the provider never deletes it on its own, so its logs stay readable. A create the API " +
			"refuses (a name already in use, a source it cannot deploy, a project that cannot be billed) leaves " +
			"nothing behind.\n\n" +
			"The settings and `size` change in place; a size change is invoiced immediately. The source and the app " +
			"type cannot be changed by the API, so changing them replaces the app.\n\n" +
			"Environment variables, custom domains and private registry credentials are not managed yet. " +
			"Needs `apps:read` and `apps:write`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "App ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": r.projectIDAttribute(),
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name, unique in the project. The subdomain is generated from it at creation.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 100),
					stringvalidator.RegexMatches(trimmedPattern, "must not start or end with whitespace"),
				},
			},
			"size": schema.StringAttribute{
				MarkdownDescription: "Size slug from the app catalogue, e.g. `shared-s`. Changing it resizes the app in place " +
					"and invoices the difference. Static sites always run as `shared-static` and cannot be resized.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(catalogueSlugPattern, "must be a catalogue slug"),
				},
			},
			"region": schema.StringAttribute{
				MarkdownDescription: "Region slug from the app catalogue. The platform decides where the app runs, so " +
					"this is what was asked for at creation; an imported app reads back where it runs.",
				Required:      true,
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"app_type": schema.StringAttribute{
				MarkdownDescription: "`service` (a long-running process, the default) or `static` (built files served as a site).",
				Optional:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.OneOf("service", "static")},
			},
			"git_provider": schema.StringAttribute{
				MarkdownDescription: "Where `git_repo` lives, e.g. `github`. The project needs that integration connected.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"git_repo": schema.StringAttribute{
				MarkdownDescription: "Repository to build, as `owner/repo`. Set this or `container_image`.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators: []validator.String{
					stringvalidator.ExactlyOneOf(path.MatchRoot("container_image")),
				},
			},
			"git_branch": schema.StringAttribute{
				MarkdownDescription: "Branch to build.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("git_repo"))},
			},
			"source_dir": schema.StringAttribute{
				MarkdownDescription: "Directory in the repository to build from, for monorepos.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("git_repo"))},
			},
			"build_strategy": schema.StringAttribute{
				MarkdownDescription: "How the repository is built, e.g. `nixpacks` or `docker`.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("git_repo"))},
			},
			"container_registry": schema.StringAttribute{
				MarkdownDescription: "Registry `container_image` is pulled from, e.g. `dockerhub`, `ghcr` or `cosmoner`.",
				Optional:            true,
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.AlsoRequires(path.MatchRoot("container_image"))},
			},
			"container_image": schema.StringAttribute{
				MarkdownDescription: "Image to run, e.g. `nginx:1.27`. Set this or `git_repo`.",
				Optional:            true,
				PlanModifiers:       replace,
			},
			"container_port": schema.Int64Attribute{
				MarkdownDescription: "Port the image listens on. The API cannot change it, so changing it replaces the app.",
				Optional:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:          append([]validator.Int64{int64validator.AlsoRequires(path.MatchRoot("container_image"))}, port...),
			},
			"build_command": schema.StringAttribute{MarkdownDescription: "Command that builds the app.", Optional: true},
			"run_command":   schema.StringAttribute{MarkdownDescription: "Command that starts the app.", Optional: true},
			"output_dir":    schema.StringAttribute{MarkdownDescription: "Directory a static site's build writes to.", Optional: true},
			"public_port": schema.Int64Attribute{
				MarkdownDescription: "Port a repository-built app listens on for public traffic.",
				Optional:            true,
				Validators:          port,
			},
			"internal_port": schema.Int64Attribute{
				MarkdownDescription: "Port for traffic from other apps in the project.",
				Optional:            true,
				Validators:          port,
			},
			"auto_deploy": schema.BoolAttribute{
				MarkdownDescription: "Whether a push to `git_branch` deploys the app. Defaults to `false`.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"image_deploy_policy": schema.StringAttribute{
				MarkdownDescription: "What a push to the image's repository does: `TAG`, `NEWEST` or `MANUAL`. The API " +
					"defaults it to `TAG`. Only meaningful for a project registry image.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"instances": schema.Int64Attribute{
				MarkdownDescription: "How many instances run. The API refuses it for a runtime that runs one, and has no " +
					"way to unset it, so removing it from configuration keeps the current count.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
			},
			"subdomain":  computedString("Generated subdomain the app is served on."),
			"url":        computedString("Public URL, once the app is live."),
			"status":     computedString("`RUNNING` once deployed; otherwise `DEPLOYING`, `ERROR` or `STOPPED`."),
			"created_at": computedString("When the app was created, as an RFC 3339 timestamp."),
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create:            true,
				Delete:            true,
				CreateDescription: "How long to wait for the first deploy to be running. Defaults to `20m`.",
				DeleteDescription: "How long to wait for the app to be deleted. Defaults to `10m`.",
			}),
		},
	}
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultAppCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, name := plan.ProjectID.ValueString(), plan.Name.ValueString()

	var containerPort *string
	if p := int64Pointer(plan.ContainerPort); p != nil {
		s := strconv.FormatInt(*p, 10)
		containerPort = &s
	}
	autoDeploy := plan.AutoDeploy.ValueBool()

	// The draft is free and expires on its own; only the second call bills.
	draftID, err := r.data.client.CreateAppDraft(ctx, projectID, client.AppDraftInput{
		Name:                name,
		Size:                plan.Size.ValueString(),
		Region:              plan.Region.ValueString(),
		DomainType:          "cosmoner",
		AppType:             stringPointer(plan.AppType),
		GitProvider:         stringPointer(plan.GitProvider),
		GitRepo:             stringPointer(plan.GitRepo),
		GitBranch:           stringPointer(plan.GitBranch),
		SourceDir:           stringPointer(plan.SourceDir),
		BuildStrategy:       stringPointer(plan.BuildStrategy),
		BuildCommand:        stringPointer(plan.BuildCommand),
		RunCommand:          stringPointer(plan.RunCommand),
		OutputDir:           stringPointer(plan.OutputDir),
		PublicPort:          int64Pointer(plan.PublicPort),
		InternalPort:        int64Pointer(plan.InternalPort),
		AutoDeploy:          &autoDeploy,
		ContainerRegistry:   stringPointer(plan.ContainerRegistry),
		ContainerImage:      stringPointer(plan.ContainerImage),
		ContainerPublicPort: containerPort,
		ImageDeployPolicy:   stringPointer(plan.ImageDeployPolicy),
		Instances:           int64Pointer(plan.Instances),
	})
	if err != nil {
		apiError(&resp.Diagnostics, "save the app draft", err)
		return
	}

	appID, createErr := r.data.client.CreateAppFromDraft(ctx, projectID, draftID, plan.Size.ValueString())
	if createErr != nil {
		if !outcomeUnknown(createErr) {
			apiError(&resp.Diagnostics, "create app", createErr)
			return
		}
		// App names are unique in a project, so an app by this name is ours.
		apps, err := r.data.client.ListApps(ctx, projectID)
		if err != nil {
			apiError(&resp.Diagnostics, "create app", fmt.Errorf("%w (and checking whether it was created failed: %s)", createErr, err))
			return
		}
		for _, app := range apps {
			if app.Name == name {
				appID = app.ID
			}
		}
		if appID == "" {
			apiError(&resp.Diagnostics, "create app", createErr)
			return
		}
	}

	var latest *client.App
	waitErr := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		app, err := r.data.client.GetApp(ctx, projectID, appID)
		if err != nil {
			return false, err
		}
		latest = app
		switch app.Status {
		case "RUNNING":
			return true, nil
		case "DEPLOYING":
			return false, nil
		}
		reason := ""
		if app.ProvisionError != nil {
			reason = *app.ProvisionError
		}
		return false, &errNotReady{status: app.Status, reason: reason}
	})

	if latest == nil {
		// Not one read succeeded; record the id so the app is not orphaned.
		plan.ID = types.StringValue(appID)
		nullUnknownApp(&plan)
	} else {
		applyApp(&plan, latest)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if waitErr != nil {
		notReadyError(&resp.Diagnostics, "app", waitErr)
	}
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	app, err := r.data.client.GetApp(ctx, state.ProjectID.ValueString(), state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		apiError(&resp.Diagnostics, "read app", err)
		return
	}

	applyApp(&state, app)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state appModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := plan.ProjectID.ValueString(), plan.ID.ValueString()

	settingsChanged := !plan.Name.Equal(state.Name) ||
		!plan.BuildCommand.Equal(state.BuildCommand) ||
		!plan.RunCommand.Equal(state.RunCommand) ||
		!plan.OutputDir.Equal(state.OutputDir) ||
		!plan.PublicPort.Equal(state.PublicPort) ||
		!plan.InternalPort.Equal(state.InternalPort) ||
		!plan.AutoDeploy.Equal(state.AutoDeploy) ||
		!plan.ImageDeployPolicy.Equal(state.ImageDeployPolicy) ||
		!plan.Instances.Equal(state.Instances)

	var err error
	action := ""
	if settingsChanged {
		action = "update app"
		_, err = r.data.client.UpdateApp(ctx, projectID, id, client.UpdateAppInput{
			Name:              plan.Name.ValueString(),
			BuildCommand:      stringPointer(plan.BuildCommand),
			RunCommand:        stringPointer(plan.RunCommand),
			OutputDir:         stringPointer(plan.OutputDir),
			PublicPort:        int64Pointer(plan.PublicPort),
			InternalPort:      int64Pointer(plan.InternalPort),
			AutoDeploy:        plan.AutoDeploy.ValueBool(),
			ImageDeployPolicy: stringPointer(plan.ImageDeployPolicy),
			Instances:         int64Pointer(plan.Instances),
		})
	}
	if err == nil && canonicalAppSize(plan.Size.ValueString()) != canonicalAppSize(state.Size.ValueString()) {
		action = "resize app"
		err = r.data.client.ResizeApp(ctx, projectID, id, plan.Size.ValueString())
	}

	// Read back either way, so a failure partway leaves state describing the
	// app as it now is.
	app, readErr := r.data.client.GetApp(ctx, projectID, id)
	if readErr != nil {
		apiError(&resp.Diagnostics, "read app", readErr)
	} else {
		target := plan
		if err != nil {
			target = state
			target.Timeouts = plan.Timeouts
		}
		applyApp(&target, app)
		resp.Diagnostics.Append(resp.State.Set(ctx, target)...)
	}
	if err != nil {
		apiError(&resp.Diagnostics, action, err)
	}
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultAppDeleteTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectID, id := state.ProjectID.ValueString(), state.ID.ValueString()

	if err := r.data.client.DeleteApp(ctx, projectID, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		apiError(&resp.Diagnostics, "delete app", err)
		return
	}

	err := waitFor(ctx, timeout, func(ctx context.Context) (bool, error) {
		_, err := r.data.client.GetApp(ctx, projectID, id)
		if client.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err != nil {
		deleteWaitError(&resp.Diagnostics, "app", err)
	}
}

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	r.importProjectScoped(ctx, req, resp)
}

func canonicalAppSize(slug string) string {
	if current, ok := legacyAppSizes[slug]; ok {
		return current
	}
	return slug
}

// applyApp copies the API's view into the model. Two values keep their
// configured spelling when the API stores another one meaning the same: the
// region, which records where the app runs rather than what was asked for,
// and the size, which the API canonicalises and forces for static sites.
func applyApp(model *appModel, app *client.App) {
	model.ID = types.StringValue(app.ID)
	model.Name = types.StringValue(app.Name)

	configured := model.Size.ValueString()
	static := app.AppType != nil && *app.AppType == "static"
	sameSize := !model.Size.IsNull() && !model.Size.IsUnknown() &&
		(canonicalAppSize(configured) == app.InstanceSize || (static && app.InstanceSize == staticAppSize))
	if !sameSize {
		model.Size = types.StringValue(app.InstanceSize)
	}
	if model.Region.IsNull() || model.Region.IsUnknown() {
		model.Region = types.StringValue(app.Region)
	}

	model.AppType = optionalString(app.AppType)
	model.GitProvider = optionalString(app.GitProvider)
	model.GitRepo = optionalString(app.GitRepo)
	model.GitBranch = optionalString(app.GitBranch)
	model.SourceDir = optionalString(app.SourceDir)
	model.BuildStrategy = optionalString(app.BuildStrategy)
	model.ContainerRegistry = optionalString(app.ContainerRegistry)
	model.ContainerImage = optionalString(app.ContainerImage)
	model.ContainerPort = types.Int64Null()
	if app.ContainerPublicPort != nil {
		if p, err := strconv.ParseInt(*app.ContainerPublicPort, 10, 64); err == nil {
			model.ContainerPort = types.Int64Value(p)
		}
	}
	model.BuildCommand = optionalString(app.BuildCommand)
	model.RunCommand = optionalString(app.RunCommand)
	model.OutputDir = optionalString(app.OutputDir)
	model.PublicPort = optionalInt64(app.PublicPort)
	model.InternalPort = optionalInt64(app.InternalPort)
	model.AutoDeploy = types.BoolValue(app.AutoDeploy)
	model.ImageDeployPolicy = types.StringValue(app.ImageDeployPolicy)
	model.Instances = optionalInt64(app.Instances)
	model.Subdomain = types.StringValue(app.Subdomain)
	model.URL = optionalString(app.URL)
	model.Status = types.StringValue(app.Status)
	model.CreatedAt = types.StringValue(app.CreatedAt)
}

// nullUnknownApp makes a model storable when no read of the app succeeded:
// state cannot hold unknown values. The next refresh fills them in.
func nullUnknownApp(model *appModel) {
	for _, s := range []*types.String{&model.ImageDeployPolicy, &model.Subdomain, &model.URL, &model.Status, &model.CreatedAt} {
		if s.IsUnknown() {
			*s = types.StringNull()
		}
	}
	if model.Instances.IsUnknown() {
		model.Instances = types.Int64Null()
	}
}
