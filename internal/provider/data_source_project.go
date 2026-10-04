package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSourceWithConfigure = (*projectDataSource)(nil)

type projectDataSource struct {
	data *providerData
}

type projectModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Slug         types.String `tfsdk:"slug"`
	BillingEmail types.String `tfsdk:"billing_email"`
	Blocked      types.Bool   `tfsdk:"blocked"`
}

func newProjectDataSource() datasource.DataSource {
	return &projectDataSource{}
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a project. Needs `projects:read`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Project ID. Defaults to the provider's `project_id`.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name.",
				Computed:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "URL slug.",
				Computed:            true,
			},
			"billing_email": schema.StringAttribute{
				MarkdownDescription: "Where invoices are sent, or null when none is set.",
				Computed:            true,
			},
			"blocked": schema.BoolAttribute{
				MarkdownDescription: "Whether the project is suspended. A suspended project refuses most writes.",
				Computed:            true,
			},
		},
	}
}

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T.", req.ProviderData))
		return
	}
	d.data = data
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config projectModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := config.ID.ValueString()
	if projectID == "" {
		projectID = d.data.projectID
	}
	if projectID == "" {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Missing project",
			"Set id on this data source, or project_id in the provider block, or the "+envProjectID+" environment variable.")
		return
	}

	project, err := d.data.client.GetProject(ctx, projectID)
	if err != nil {
		apiError(&resp.Diagnostics, "read project", err)
		return
	}

	state := projectModel{
		ID:           types.StringValue(project.ID),
		Name:         types.StringValue(project.Name),
		Slug:         types.StringValue(project.Slug),
		BillingEmail: optionalString(project.BillingEmail),
		Blocked:      types.BoolValue(project.BlockedAt != nil),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
