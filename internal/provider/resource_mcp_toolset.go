package provider

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &MCPToolsetResource{}
var _ resource.ResourceWithImportState = &MCPToolsetResource{}

var mcpToolsetToolAttributeTypes = map[string]attr.Type{
	"server_id": types.StringType,
	"tool_name": types.StringType,
}

// mcpToolsetRecoveryBaseDelay seeds the accepted-mutation recovery backoff.
// Non-parallel protocol tests shorten it; production uses one second.
var mcpToolsetRecoveryBaseDelay = time.Second

// mcpToolsetAcceptedCreatePrivateKey marks name-bound partial state whose
// create was proven accepted by a 2xx, so Read may recover the identity by
// unique name. Its absence on identity-free state means the create outcome
// was never proven and only operator reconciliation is safe.
const mcpToolsetAcceptedCreatePrivateKey = "mcp_toolset_accepted_create"

func NewMCPToolsetResource() resource.Resource {
	return &MCPToolsetResource{createRecoveryDelay: mcpToolsetRecoveryBaseDelay}
}

type MCPToolsetResource struct {
	client *Client
	// createRecoveryDelay overrides the first accepted-create recovery
	// backoff step; zero selects the production one-second default.
	createRecoveryDelay time.Duration
}

type MCPToolsetToolModel struct {
	ServerID types.String `tfsdk:"server_id"`
	ToolName types.String `tfsdk:"tool_name"`
}

type MCPToolsetResourceModel struct {
	ToolsetID   types.String `tfsdk:"toolset_id"`
	ToolsetName types.String `tfsdk:"toolset_name"`
	Description types.String `tfsdk:"description"`
	Tools       types.Set    `tfsdk:"tools"`
}

type mcpToolsetTool struct {
	ServerID string `json:"server_id"`
	ToolName string `json:"tool_name"`
}

type mcpToolsetRequest struct {
	ToolsetID   string           `json:"toolset_id,omitempty"`
	ToolsetName string           `json:"toolset_name"`
	Description *string          `json:"description,omitempty"`
	Tools       []mcpToolsetTool `json:"tools"`
}

type mcpToolsetResponse struct {
	ToolsetID   string           `json:"toolset_id"`
	ToolsetName string           `json:"toolset_name"`
	Description *string          `json:"description"`
	Tools       []mcpToolsetTool `json:"tools"`
}

func (r *MCPToolsetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_toolset"
}

func (r *MCPToolsetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	toolType := types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}
	emptyTools, diags := types.SetValue(toolType, nil)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM MCP toolset definition.",
		Attributes: map[string]schema.Attribute{
			"toolset_id": schema.StringAttribute{
				Description: "The unique identifier for the MCP toolset.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"toolset_name": schema.StringAttribute{
				Description: "The unique name of the MCP toolset.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "A description of the MCP toolset.",
				Optional:    true,
			},
			"tools": schema.SetNestedAttribute{
				Description: "The unordered set of MCP server and tool pairs in the toolset.",
				Optional:    true,
				Computed:    true,
				Default:     setdefault.StaticValue(emptyTools),
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"server_id": schema.StringAttribute{
							Description: "The MCP server identifier.",
							Required:    true,
						},
						"tool_name": schema.StringAttribute{
							Description: "The tool name as exposed by the MCP server.",
							Required:    true,
						},
					},
				},
			},
		},
	}
}

func (r *MCPToolsetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}

	r.client = client
}

func (r *MCPToolsetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MCPToolsetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	outcome, err := r.createMCPToolset(ctx, &data)
	if err != nil {
		switch outcome {
		case mcpToolsetCreateAccepted:
			// LiteLLM committed the uniquely named row, so retain a
			// name-bound partial state instead of orphaning it behind
			// its own name conflict. Refresh recovers the identity.
			recoveryCtx := context.WithoutCancel(ctx)
			partial := data
			partial.ToolsetID = types.StringNull()
			resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &partial)...)
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, mcpToolsetAcceptedCreatePrivateKey, []byte("true"))...)
			}
			resp.Diagnostics.AddError("MCP Toolset Create Not Confirmed", fmt.Sprintf("LiteLLM accepted the create but the toolset identity was not confirmed: %s. The name-bound partial state was retained; refresh converges once the toolset is readable.", err))
		case mcpToolsetCreateUncertain:
			// The request was dispatched but no status arrived, so the row
			// may or may not exist. Retain name-bound state without the
			// recovery marker: it blocks a blind duplicate create, and
			// refresh instructs reconciliation instead of adopting by name.
			recoveryCtx := context.WithoutCancel(ctx)
			partial := data
			partial.ToolsetID = types.StringNull()
			resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &partial)...)
			resp.Diagnostics.AddError("MCP Toolset Create Uncertain", fmt.Sprintf("The create request was dispatched but LiteLLM returned no response status: %s. The toolset may or may not exist. The name-bound state was retained to block a duplicate create; confirm the toolset in LiteLLM, then import it by ID if it exists or remove this resource from state.", err))
		default:
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create MCP toolset: %s", err))
		}
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPToolsetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MCPToolsetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ToolsetID.IsNull() || data.ToolsetID.ValueString() == "" {
		marker, markerDiags := req.Private.GetKey(ctx, mcpToolsetAcceptedCreatePrivateKey)
		resp.Diagnostics.Append(markerDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		if string(marker) != "true" {
			// The interrupted create never proved acceptance, so an
			// exact-name match could be a pre-existing toolset that must
			// not be adopted. Only the operator can reconcile this.
			resp.Diagnostics.AddError("MCP Toolset Identity Uncertain", fmt.Sprintf("An interrupted create never confirmed whether LiteLLM committed toolset %q. Confirm the toolset in LiteLLM, then import it by ID if it exists or remove this resource from state.", data.ToolsetName.ValueString()))
			return
		}
		// Name-bound partial state from an unconfirmed accepted create.
		// Recovery errors format with %s, never %w, so an embedded 404 can
		// never masquerade as direct absence proof and remove the state.
		recovered, err := r.recoverMCPToolsetByName(ctx, data.ToolsetName.ValueString(), nil, &data)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP toolset: recover the unconfirmed toolset %q by name: %s", data.ToolsetName.ValueString(), err))
			return
		}
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, mcpToolsetAcceptedCreatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &recovered)...)
		return
	}

	if err := r.readMCPToolset(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP toolset: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPToolsetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data MCPToolsetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MCPToolsetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ToolsetID = state.ToolsetID

	if err := r.updateMCPToolset(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update MCP toolset: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPToolsetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data MCPToolsetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ToolsetID.IsNull() || data.ToolsetID.ValueString() == "" {
		resp.Diagnostics.AddError("MCP Toolset Identity Not Confirmed", "The toolset identity was never confirmed after an accepted create. Refresh state to recover the identity, then destroy again.")
		return
	}

	if err := r.deleteMCPToolset(ctx, data.ToolsetID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP toolset: %s", err))
	}
}

func (r *MCPToolsetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("toolset_id"), req, resp)
}

func buildMCPToolsetRequest(ctx context.Context, data *MCPToolsetResourceModel, includeID bool) (mcpToolsetRequest, error) {
	// Unknown values must fail closed before dispatch; serializing them would
	// silently send empty or omitted wire values that LiteLLM persists.
	if data.ToolsetName.IsUnknown() {
		return mcpToolsetRequest{}, fmt.Errorf("toolset_name is unknown")
	}
	if data.Description.IsUnknown() {
		return mcpToolsetRequest{}, fmt.Errorf("description is unknown")
	}
	request := mcpToolsetRequest{
		ToolsetName: data.ToolsetName.ValueString(),
		Tools:       []mcpToolsetTool{},
	}
	if includeID {
		request.ToolsetID = data.ToolsetID.ValueString()
		if data.Description.IsNull() {
			empty := ""
			request.Description = &empty
		}
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		description := data.Description.ValueString()
		request.Description = &description
	}

	if !data.Tools.IsNull() {
		if data.Tools.IsUnknown() {
			return mcpToolsetRequest{}, fmt.Errorf("tools is unknown")
		}

		var tools []MCPToolsetToolModel
		if diags := data.Tools.ElementsAs(ctx, &tools, false); diags.HasError() {
			return mcpToolsetRequest{}, fmt.Errorf("failed to decode tools: %v", diags.Errors())
		}
		seen := make(map[mcpToolsetTool]struct{}, len(tools))
		for _, tool := range tools {
			if tool.ServerID.IsUnknown() || tool.ToolName.IsUnknown() {
				return mcpToolsetRequest{}, fmt.Errorf("tools contains an unknown server_id or tool_name")
			}
			apiTool := mcpToolsetTool{
				ServerID: tool.ServerID.ValueString(),
				ToolName: tool.ToolName.ValueString(),
			}
			if _, exists := seen[apiTool]; exists {
				continue
			}
			seen[apiTool] = struct{}{}
			request.Tools = append(request.Tools, apiTool)
		}
	}

	sort.Slice(request.Tools, func(i, j int) bool {
		if request.Tools[i].ServerID == request.Tools[j].ServerID {
			return request.Tools[i].ToolName < request.Tools[j].ToolName
		}
		return request.Tools[i].ServerID < request.Tools[j].ServerID
	})
	return request, nil
}

// confirmMCPToolsetDefinition proves a decodable response describes exactly
// the requested definition. A response with a different name, description, or
// tool membership is evidence of another row or a partial write and must not
// enter state as the requested toolset.
func confirmMCPToolsetDefinition(request mcpToolsetRequest, result mcpToolsetResponse) error {
	if result.ToolsetName != request.ToolsetName {
		return fmt.Errorf("LiteLLM returned toolset_name %q for requested name %q", result.ToolsetName, request.ToolsetName)
	}
	requestedDescription := ""
	if request.Description != nil {
		requestedDescription = *request.Description
	}
	returnedDescription := ""
	if result.Description != nil {
		returnedDescription = *result.Description
	}
	if requestedDescription != returnedDescription {
		return fmt.Errorf("LiteLLM returned a different description for toolset %q", request.ToolsetName)
	}
	returnedTools := make([]mcpToolsetTool, 0, len(result.Tools))
	seen := make(map[mcpToolsetTool]struct{}, len(result.Tools))
	for _, tool := range result.Tools {
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		returnedTools = append(returnedTools, tool)
	}
	sort.Slice(returnedTools, func(i, j int) bool {
		if returnedTools[i].ServerID == returnedTools[j].ServerID {
			return returnedTools[i].ToolName < returnedTools[j].ToolName
		}
		return returnedTools[i].ServerID < returnedTools[j].ServerID
	})
	if len(returnedTools) != len(request.Tools) {
		return fmt.Errorf("LiteLLM returned %d tools for toolset %q, requested %d", len(returnedTools), request.ToolsetName, len(request.Tools))
	}
	for index := range returnedTools {
		if returnedTools[index] != request.Tools[index] {
			return fmt.Errorf("LiteLLM returned a different tool membership for toolset %q", request.ToolsetName)
		}
	}
	return nil
}

// mcpToolsetCreateOutcome records what the create mutation proved. Rejected
// means LiteLLM answered without committing, accepted means a 2xx proved the
// uniquely named row exists, and uncertain means the request was dispatched
// but no response status arrived, so the row may or may not exist.
type mcpToolsetCreateOutcome int

const (
	mcpToolsetCreateRejected mcpToolsetCreateOutcome = iota
	mcpToolsetCreateAccepted
	mcpToolsetCreateUncertain
)

// createMCPToolset posts the new toolset and confirms its identity. When the
// outcome is accepted and err is non-nil, the uniquely named row exists but
// its identity was not confirmed; when the outcome is uncertain, only
// name-bound state without automatic recovery is safe, because a dispatched
// request without a status could equally have hit a pre-existing-name
// conflict that the provider must never adopt.
func (r *MCPToolsetResource) createMCPToolset(ctx context.Context, data *MCPToolsetResourceModel) (mcpToolsetCreateOutcome, error) {
	request, err := buildMCPToolsetRequest(ctx, data, false)
	if err != nil {
		return mcpToolsetCreateRejected, err
	}

	var result mcpToolsetResponse
	accepted, err := r.client.doRequestWithResponse(ctx, http.MethodPost, "/v1/mcp/toolset", request, &result)
	if err == nil {
		err = confirmMCPToolsetDefinition(request, result)
	}
	if err == nil {
		err = applyMCPToolsetResponse(ctx, result, "", data)
	}
	if err == nil {
		return mcpToolsetCreateAccepted, nil
	}
	if !accepted {
		if classification := ClassifyHTTPFailure(err); classification.RequestDispatched && classification.StatusCode == 0 {
			return mcpToolsetCreateUncertain, err
		}
		return mcpToolsetCreateRejected, err
	}
	// LiteLLM accepted the create, so the toolset exists even though the
	// response body was unusable. toolset_name is unique in LiteLLM v1.98, so
	// recover the stored identity from the collection instead of stranding a
	// row that blocks every retry with a name conflict.
	recovered, recoverErr := r.recoverMCPToolsetByName(ctx, request.ToolsetName, &request, data)
	if recoverErr != nil {
		return mcpToolsetCreateAccepted, fmt.Errorf("LiteLLM accepted the create but returned an unusable response (%s); recovery by name failed: %s", err, recoverErr)
	}
	*data = recovered
	return mcpToolsetCreateAccepted, nil
}

// mcpToolsetRecoveryFailureIsTerminal reports whether a recovery probe
// failure is authoritative and must stop the bounded retry loop: an explicit
// status-bearing terminal answer (401/403/404), a canceled context, or an
// expired deadline. Transient transport and contract failures stay
// inconclusive and retry.
func mcpToolsetRecoveryFailureIsTerminal(err error) bool {
	switch ClassifyHTTPFailure(err).Kind {
	case HTTPFailureTerminalResponse, HTTPFailureCanceled, HTTPFailureDeadline:
		return true
	default:
		return false
	}
}

// recoverMCPToolsetByName recovers an accepted mutation whose response body
// was unusable. It requires positive exact-name evidence from the collection
// listing, then confirms the row through the direct-ID endpoint before
// returning it. When definition is non-nil (create recovery), the confirmed
// row must also match the requested definition exactly; a nil definition
// (read repair) adopts the remote definition as ordinary drift. An empty
// collection is never absence authority because LiteLLM's pinned
// list_mcp_toolsets converts database failures to an empty list, so every
// inconclusive read retries with context-aware bounded backoff on the
// fresh-connection probe path.
func (r *MCPToolsetResource) recoverMCPToolsetByName(ctx context.Context, name string, definition *mcpToolsetRequest, data *MCPToolsetResourceModel) (MCPToolsetResourceModel, error) {
	delay := r.createRecoveryDelay
	if delay <= 0 {
		delay = time.Second
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr == nil {
					lastErr = ctx.Err()
				}
				return MCPToolsetResourceModel{}, lastErr
			case <-timer.C:
			}
			delay *= 2
		}
		var toolsets []mcpToolsetResponse
		if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, "/v1/mcp/toolset", nil, &toolsets); err != nil {
			if mcpToolsetRecoveryFailureIsTerminal(err) {
				return MCPToolsetResourceModel{}, err
			}
			lastErr = err
			continue
		}
		var match *mcpToolsetResponse
		ambiguous := false
		for index := range toolsets {
			if toolsets[index].ToolsetName != name {
				continue
			}
			if match != nil {
				ambiguous = true
				break
			}
			match = &toolsets[index]
		}
		if ambiguous {
			return MCPToolsetResourceModel{}, fmt.Errorf("multiple toolsets named %q", name)
		}
		if match == nil {
			lastErr = fmt.Errorf("no positive collection evidence for toolset %q", name)
			continue
		}
		matchID := mcpToolsetRecoveredIdentifier(*match)
		var confirmed mcpToolsetResponse
		if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, mcpToolsetEndpoint(matchID), nil, &confirmed); err != nil {
			if mcpToolsetRecoveryFailureIsTerminal(err) {
				return MCPToolsetResourceModel{}, fmt.Errorf("confirm toolset %q through the direct endpoint: %s", matchID, err)
			}
			lastErr = fmt.Errorf("confirm toolset %q through the direct endpoint: %s", matchID, err)
			continue
		}
		if definition != nil {
			// A confirmed row that does not match the accepted request is
			// authoritative evidence of a different row; do not adopt it.
			if err := confirmMCPToolsetDefinition(*definition, confirmed); err != nil {
				return MCPToolsetResourceModel{}, err
			}
		}
		next := *data
		if err := applyMCPToolsetResponse(ctx, confirmed, matchID, &next); err != nil {
			lastErr = err
			continue
		}
		return next, nil
	}
	return MCPToolsetResourceModel{}, lastErr
}

func (r *MCPToolsetResource) readMCPToolset(ctx context.Context, data *MCPToolsetResourceModel) error {
	toolsetID := data.ToolsetID.ValueString()
	var result mcpToolsetResponse
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, mcpToolsetEndpoint(toolsetID), nil, &result); err != nil {
		return err
	}

	return applyMCPToolsetResponse(ctx, result, toolsetID, data)
}

func (r *MCPToolsetResource) updateMCPToolset(ctx context.Context, data *MCPToolsetResourceModel) error {
	toolsetID := data.ToolsetID.ValueString()
	if data.ToolsetID.IsNull() || toolsetID == "" {
		return fmt.Errorf("the toolset identity was never confirmed; refresh state to recover it before updating")
	}

	request, err := buildMCPToolsetRequest(ctx, data, true)
	if err != nil {
		return err
	}

	var result mcpToolsetResponse
	if accepted, updateErr := r.client.doRequestWithResponse(ctx, http.MethodPut, "/v1/mcp/toolset", request, &result); updateErr != nil && !accepted {
		return updateErr
	}
	// The direct singular endpoint is the mandatory readback authority for an
	// accepted update; the mutation envelope is used only for acceptance. The
	// confirmed row must match the planned definition exactly before it is
	// published as the update result.
	var confirmed mcpToolsetResponse
	if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, mcpToolsetEndpoint(toolsetID), nil, &confirmed); err != nil {
		return fmt.Errorf("LiteLLM accepted the update but direct readback failed: %s", err)
	}
	if err := confirmMCPToolsetDefinition(request, confirmed); err != nil {
		return fmt.Errorf("LiteLLM accepted the update but readback did not match the planned definition: %s", err)
	}
	return applyMCPToolsetResponse(ctx, confirmed, toolsetID, data)
}

func (r *MCPToolsetResource) deleteMCPToolset(ctx context.Context, toolsetID string) error {
	err := r.client.DoRequestWithResponse(ctx, http.MethodDelete, mcpToolsetEndpoint(toolsetID), nil, nil)
	if IsNotFoundError(err) {
		return nil
	}
	return err
}

func mcpToolsetEndpoint(toolsetID string) string {
	return endpointWithPathSegment("/v1/mcp/toolset/", toolsetID, "")
}

// mcpToolsetRecoveredIdentifier is the reviewed raw-identity boundary for
// accepted-mutation recovery: the identity of the exact-name collection match
// that the direct endpoint must confirm before it enters state.
func mcpToolsetRecoveredIdentifier(match mcpToolsetResponse) string {
	return match.ToolsetID
}

func applyMCPToolsetResponse(ctx context.Context, result mcpToolsetResponse, expectedID string, data *MCPToolsetResourceModel) error {
	if result.ToolsetID == "" {
		return fmt.Errorf("LiteLLM response omitted toolset_id")
	}
	if expectedID != "" && result.ToolsetID != expectedID {
		return fmt.Errorf("LiteLLM returned toolset_id %q for %q", result.ToolsetID, expectedID)
	}

	toolValues := make([]attr.Value, 0, len(result.Tools))
	seen := make(map[mcpToolsetTool]struct{}, len(result.Tools))
	for _, tool := range result.Tools {
		if _, exists := seen[tool]; exists {
			continue
		}
		seen[tool] = struct{}{}
		value, diags := types.ObjectValue(mcpToolsetToolAttributeTypes, map[string]attr.Value{
			"server_id": types.StringValue(tool.ServerID),
			"tool_name": types.StringValue(tool.ToolName),
		})
		if diags.HasError() {
			return fmt.Errorf("failed to encode tool %q from server %q: %v", tool.ToolName, tool.ServerID, diags.Errors())
		}
		toolValues = append(toolValues, value)
	}
	tools, diags := types.SetValue(types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}, toolValues)
	if diags.HasError() {
		return fmt.Errorf("failed to encode tools: %v", diags.Errors())
	}

	next := *data
	next.ToolsetID = types.StringValue(result.ToolsetID)
	next.ToolsetName = types.StringValue(result.ToolsetName)
	if result.Description == nil || (*result.Description == "" && data.Description.IsNull()) {
		next.Description = types.StringNull()
	} else {
		next.Description = types.StringValue(*result.Description)
	}
	next.Tools = tools
	*data = next
	return nil
}
