package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &SearchToolResource{}
var _ resource.ResourceWithImportState = &SearchToolResource{}

func NewSearchToolResource() resource.Resource {
	return &SearchToolResource{}
}

type SearchToolResource struct {
	client *Client
}

type SearchToolResourceModel struct {
	ID             types.String  `tfsdk:"id"`
	SearchToolID   types.String  `tfsdk:"search_tool_id"`
	SearchToolName types.String  `tfsdk:"search_tool_name"`
	SearchProvider types.String  `tfsdk:"search_provider"`
	APIKey         types.String  `tfsdk:"api_key"`
	APIBase        types.String  `tfsdk:"api_base"`
	Timeout        types.Float64 `tfsdk:"timeout"`
	MaxRetries     types.Int64   `tfsdk:"max_retries"`
	SearchToolInfo types.String  `tfsdk:"search_tool_info"`
}

func (r *SearchToolResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_search_tool"
}

func (r *SearchToolResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM search tool configuration.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this search tool (same as search_tool_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"search_tool_id": schema.StringAttribute{
				Description: "Unique identifier for the search tool.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"search_tool_name": schema.StringAttribute{
				Description: "Name of the search tool.",
				Required:    true,
			},
			"search_provider": schema.StringAttribute{
				Description: "The search provider to use (e.g., 'tavily', 'serper', 'bing', 'google').",
				Required:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "API key for the search provider.",
				Optional:    true,
				Sensitive:   true,
			},
			"api_base": schema.StringAttribute{
				Description: "Base URL for the search API.",
				Optional:    true,
			},
			"timeout": schema.Float64Attribute{
				Description: "Timeout in seconds for search requests.",
				Optional:    true,
			},
			"max_retries": schema.Int64Attribute{
				Description: "Maximum number of retries for failed requests.",
				Optional:    true,
			},
			"search_tool_info": schema.StringAttribute{
				Description: "Additional search tool configuration as a JSON string.",
				Optional:    true,
			},
		},
	}
}

func (r *SearchToolResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *SearchToolResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data SearchToolResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	searchReq := r.buildSearchToolRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/search_tools", searchReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create search tool: %s", err))
		return
	}

	// Extract search_tool_id from response
	if searchToolID, ok := result["search_tool_id"].(string); ok {
		data.SearchToolID = types.StringValue(searchToolID)
		data.ID = types.StringValue(searchToolID)
	}

	// Read back for full state
	if err := r.readSearchTool(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Search tool created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SearchToolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data SearchToolResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readSearchTool(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read search tool: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SearchToolResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data SearchToolResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state SearchToolResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the IDs
	data.ID = state.ID
	data.SearchToolID = state.SearchToolID

	searchReq := r.buildSearchToolRequest(ctx, &data)

	endpoint := fmt.Sprintf("/search_tools/%s", data.SearchToolID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, searchReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update search tool: %s", err))
		return
	}

	// Read back for full state
	if err := r.readSearchTool(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Search tool updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SearchToolResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data SearchToolResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	searchToolID := data.SearchToolID.ValueString()
	if searchToolID == "" {
		searchToolID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/search_tools/%s", searchToolID)
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete search tool: %s", err))
			return
		}
	}
}

func (r *SearchToolResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("search_tool_id"), req.ID)...)
}

func (r *SearchToolResource) buildSearchToolRequest(ctx context.Context, data *SearchToolResourceModel) map[string]interface{} {
	searchReq := map[string]interface{}{
		"search_tool_name": data.SearchToolName.ValueString(),
	}

	// Build litellm_params for the search tool
	litellmParams := map[string]interface{}{
		"search_provider": data.SearchProvider.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.APIKey.IsNull() && !data.APIKey.IsUnknown() && data.APIKey.ValueString() != "" {
		litellmParams["api_key"] = data.APIKey.ValueString()
	}

	if !data.APIBase.IsNull() && !data.APIBase.IsUnknown() && data.APIBase.ValueString() != "" {
		litellmParams["api_base"] = data.APIBase.ValueString()
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.Timeout.IsNull() && !data.Timeout.IsUnknown() {
		litellmParams["timeout"] = data.Timeout.ValueFloat64()
	}

	if !data.MaxRetries.IsNull() && !data.MaxRetries.IsUnknown() {
		litellmParams["max_retries"] = data.MaxRetries.ValueInt64()
	}

	searchReq["litellm_params"] = litellmParams

	if !data.SearchToolInfo.IsNull() && !data.SearchToolInfo.IsUnknown() && data.SearchToolInfo.ValueString() != "" {
		searchReq["search_tool_info"] = data.SearchToolInfo.ValueString()
	}

	return searchReq
}

func (r *SearchToolResource) readSearchTool(ctx context.Context, data *SearchToolResourceModel) error {
	searchToolID := data.SearchToolID.ValueString()
	if searchToolID == "" {
		searchToolID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/search_tools/%s", searchToolID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if stID, ok := result["search_tool_id"].(string); ok {
		data.SearchToolID = types.StringValue(stID)
		data.ID = types.StringValue(stID)
	}

	if searchToolName, ok := result["search_tool_name"].(string); ok {
		data.SearchToolName = types.StringValue(searchToolName)
	}

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if searchProvider, ok := litellmParams["search_provider"].(string); ok {
			data.SearchProvider = types.StringValue(searchProvider)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok {
			data.APIBase = types.StringValue(apiBase)
		}
		if timeout, ok := litellmParams["timeout"].(float64); ok {
			data.Timeout = types.Float64Value(timeout)
		}
		if maxRetries, ok := litellmParams["max_retries"].(float64); ok {
			data.MaxRetries = types.Int64Value(int64(maxRetries))
		}
		// Note: API key is not read back for security reasons
	}

	if searchToolInfo, ok := result["search_tool_info"].(string); ok {
		data.SearchToolInfo = types.StringValue(searchToolInfo)
	}

	return nil
}
