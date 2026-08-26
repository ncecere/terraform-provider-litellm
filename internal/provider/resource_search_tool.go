package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
				Description: "Additional search tool configuration as a validated JSON object string.",
				Optional:    true,
				Validators:  []validator.String{jsonShapeStringValidator{shape: '{'}},
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

	searchToolBody, err := r.buildSearchToolRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Search Tool JSON", err.Error())
		return
	}
	// API expects {"search_tool": {...}}
	searchReq := map[string]interface{}{
		"search_tool": searchToolBody,
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/search_tools", searchReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create search tool: %s", err))
		return
	}

	// Extract search_tool_id from response (may be nested under "search_tool")
	searchToolResult := result
	if nested, ok := result["search_tool"].(map[string]interface{}); ok {
		searchToolResult = nested
	}
	if searchToolID, ok := searchToolResult["search_tool_id"].(string); ok && searchToolID != "" {
		data.SearchToolID = types.StringValue(searchToolID)
		data.ID = types.StringValue(searchToolID)
	} else {
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM accepted the search tool create but did not return a recoverable search_tool_id.")
		return
	}

	if err := r.readSearchTool(ctx, &data); err != nil {
		recovery := SearchToolResourceModel{ID: data.ID, SearchToolID: data.SearchToolID}
		resp.Diagnostics.Append(resp.State.Set(ctx, &recovery)...)
		resp.Diagnostics.AddError("Search Tool Create Not Confirmed", fmt.Sprintf("Search tool created but authoritative read-back failed: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *SearchToolResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data SearchToolResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readSearchToolWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read search tool: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
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

	searchToolBody, err := r.buildSearchToolRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Search Tool JSON", err.Error())
		return
	}
	searchToolBody["search_tool_id"] = data.SearchToolID.ValueString()
	// API expects {"search_tool": {...}}
	searchReq := map[string]interface{}{
		"search_tool": searchToolBody,
	}

	endpoint := endpointWithPathSegment("/search_tools/", data.SearchToolID.ValueString(), "")
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, searchReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update search tool: %s", err))
		return
	}

	if err := r.readSearchTool(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Search Tool Update Not Confirmed", fmt.Sprintf("Search tool updated but authoritative read-back failed: %s", err))
		return
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

	endpoint := endpointWithPathSegment("/search_tools/", searchToolID, "")
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
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

func (r *SearchToolResource) buildSearchToolRequest(ctx context.Context, data *SearchToolResourceModel) (map[string]interface{}, error) {
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
		searchToolInfo, err := decodeRequestJSONObject(data.SearchToolInfo.ValueString(), "search_tool_info")
		if err != nil {
			return nil, err
		}
		searchReq["search_tool_info"] = searchToolInfo
	}

	return searchReq, nil
}

func (r *SearchToolResource) readSearchTool(ctx context.Context, data *SearchToolResourceModel) error {
	return r.readSearchToolWithNumericOwnership(ctx, data, false)
}

func (r *SearchToolResource) readSearchToolWithNumericOwnership(ctx context.Context, data *SearchToolResourceModel, imported bool) error {
	searchToolID := data.SearchToolID.ValueString()
	if searchToolID == "" {
		searchToolID = data.ID.ValueString()
	}

	endpoint := endpointWithPathSegment("/search_tools/", searchToolID, "")

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}
	if err := validateSearchToolAPIObject(result, searchToolID); err != nil {
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
		timeoutOwned := imported || (!data.Timeout.IsNull() && !data.Timeout.IsUnknown())
		if err := updateFloat64FromAPI(&data.Timeout, litellmParams, timeoutOwned, timeoutOwned, "timeout"); err != nil {
			return err
		}
		retriesOwned := imported || (!data.MaxRetries.IsNull() && !data.MaxRetries.IsUnknown())
		if err := updateInt64FromAPI(&data.MaxRetries, litellmParams, retriesOwned, retriesOwned, "max_retries"); err != nil {
			return err
		}
		// Note: API key is not read back for security reasons
	}

	searchToolInfoOwned := imported || (!data.SearchToolInfo.IsNull() && !data.SearchToolInfo.IsUnknown())
	if err := updateJSONObjectStringState(&data.SearchToolInfo, result, "search_tool_info", searchToolInfoOwned); err != nil {
		return err
	}
	if !searchToolInfoOwned && data.SearchToolInfo.IsUnknown() {
		data.SearchToolInfo = types.StringNull()
	}

	return nil
}

func validateSearchToolAPIObject(result map[string]interface{}, expectedID string) error {
	actualID, ok := result["search_tool_id"].(string)
	if !ok || actualID == "" {
		return fmt.Errorf("search tool response omitted required search_tool_id")
	}
	if actualID != expectedID {
		return fmt.Errorf("search tool response identity did not match the requested search tool")
	}
	name, ok := result["search_tool_name"].(string)
	if !ok || name == "" {
		return fmt.Errorf("search tool response omitted required search_tool_name")
	}
	litellmParams, ok := result["litellm_params"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("search tool response omitted required litellm_params object")
	}
	provider, ok := litellmParams["search_provider"].(string)
	if !ok || provider == "" {
		return fmt.Errorf("search tool response omitted required litellm_params.search_provider")
	}
	return nil
}
