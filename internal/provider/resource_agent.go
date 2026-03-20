package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &AgentResource{}
var _ resource.ResourceWithImportState = &AgentResource{}

func NewAgentResource() resource.Resource {
	return &AgentResource{}
}

type AgentResource struct {
	client *Client
}

// --- Nested model types ---

type AgentProviderModel struct {
	Organization types.String `tfsdk:"organization"`
	URL          types.String `tfsdk:"url"`
}

type AgentCapabilitiesModel struct {
	Streaming              types.Bool `tfsdk:"streaming"`
	PushNotifications      types.Bool `tfsdk:"push_notifications"`
	StateTransitionHistory types.Bool `tfsdk:"state_transition_history"`
}

type AgentSkillModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Tags        types.List   `tfsdk:"tags"`
	Examples    types.List   `tfsdk:"examples"`
	InputModes  types.List   `tfsdk:"input_modes"`
	OutputModes types.List   `tfsdk:"output_modes"`
}

type AgentObjectPermissionModel struct {
	MCPServers         types.List `tfsdk:"mcp_servers"`
	MCPAccessGroups    types.List `tfsdk:"mcp_access_groups"`
	MCPToolPermissions types.Map  `tfsdk:"mcp_tool_permissions"`
	Models             types.List `tfsdk:"models"`
	Agents             types.List `tfsdk:"agents"`
}

type AgentCardModel struct {
	Name               types.String            `tfsdk:"name"`
	Description        types.String            `tfsdk:"description"`
	URL                types.String            `tfsdk:"url"`
	Version            types.String            `tfsdk:"version"`
	ProtocolVersion    types.String            `tfsdk:"protocol_version"`
	DefaultInputModes  types.List              `tfsdk:"default_input_modes"`
	DefaultOutputModes types.List              `tfsdk:"default_output_modes"`
	Capabilities       *AgentCapabilitiesModel `tfsdk:"capabilities"`
	Skills             []AgentSkillModel       `tfsdk:"skills"`
	Provider           *AgentProviderModel     `tfsdk:"provider"`
	PreferredTransport types.String            `tfsdk:"preferred_transport"`
	IconURL            types.String            `tfsdk:"icon_url"`
	DocumentationURL   types.String            `tfsdk:"documentation_url"`
}

type AgentResourceModel struct {
	ID               types.String                `tfsdk:"id"`
	AgentName        types.String                `tfsdk:"agent_name"`
	AgentCard        *AgentCardModel             `tfsdk:"agent_card"`
	LiteLLMParams    types.Map                   `tfsdk:"litellm_params"`
	ObjectPermission *AgentObjectPermissionModel `tfsdk:"object_permission"`
	TPMLimit         types.Int64                 `tfsdk:"tpm_limit"`
	RPMLimit         types.Int64                 `tfsdk:"rpm_limit"`
	SessionTPMLimit  types.Int64                 `tfsdk:"session_tpm_limit"`
	SessionRPMLimit  types.Int64                 `tfsdk:"session_rpm_limit"`
	StaticHeaders    types.Map                   `tfsdk:"static_headers"`
	ExtraHeaders     types.List                  `tfsdk:"extra_headers"`
	// Computed
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
	CreatedBy types.String `tfsdk:"created_by"`
	UpdatedBy types.String `tfsdk:"updated_by"`
}

func (r *AgentResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *AgentResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM Agent (A2A). Agents are AI-powered entities that can be discovered, invoked, and composed using the Agent-to-Agent protocol.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The agent ID (assigned by LiteLLM).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"agent_name": schema.StringAttribute{
				Description: "The name of the agent.",
				Required:    true,
			},
			"litellm_params": schema.MapAttribute{
				Description: "LiteLLM-specific parameters for the agent (e.g. model, api_key).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit for the agent.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit for the agent.",
				Optional:    true,
			},
			"session_tpm_limit": schema.Int64Attribute{
				Description: "Per-session tokens per minute limit.",
				Optional:    true,
			},
			"session_rpm_limit": schema.Int64Attribute{
				Description: "Per-session requests per minute limit.",
				Optional:    true,
			},
			"static_headers": schema.MapAttribute{
				Description: "Static headers to send with agent requests.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"extra_headers": schema.ListAttribute{
				Description: "Extra header names to forward from the incoming request.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the agent was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the agent was last updated.",
				Computed:    true,
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the agent.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "User who last updated the agent.",
				Computed:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"agent_card": schema.SingleNestedBlock{
				Description: "The A2A agent card — a self-describing manifest for the agent.",
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Description: "Display name of the agent.",
						Required:    true,
					},
					"description": schema.StringAttribute{
						Description: "Human-readable description of the agent.",
						Optional:    true,
					},
					"url": schema.StringAttribute{
						Description: "The URL endpoint for the agent.",
						Required:    true,
					},
					"version": schema.StringAttribute{
						Description: "Version of the agent.",
						Optional:    true,
					},
					"protocol_version": schema.StringAttribute{
						Description: "A2A protocol version (e.g. '0.2.6').",
						Optional:    true,
					},
					"default_input_modes": schema.ListAttribute{
						Description: "Default input MIME types (e.g. ['application/json', 'text/plain']).",
						Optional:    true,
						Computed:    true,
						ElementType: types.StringType,
					},
					"default_output_modes": schema.ListAttribute{
						Description: "Default output MIME types.",
						Optional:    true,
						Computed:    true,
						ElementType: types.StringType,
					},
					"preferred_transport": schema.StringAttribute{
						Description: "Preferred transport protocol (e.g. 'httpsse', 'websocket').",
						Optional:    true,
					},
					"icon_url": schema.StringAttribute{
						Description: "URL for the agent's icon.",
						Optional:    true,
					},
					"documentation_url": schema.StringAttribute{
						Description: "URL for the agent's documentation.",
						Optional:    true,
					},
				},
				Blocks: map[string]schema.Block{
					"capabilities": schema.SingleNestedBlock{
						Description: "Capabilities supported by the agent.",
						Attributes: map[string]schema.Attribute{
							"streaming": schema.BoolAttribute{
								Description: "Whether the agent supports streaming responses.",
								Optional:    true,
							},
							"push_notifications": schema.BoolAttribute{
								Description: "Whether the agent supports push notifications.",
								Optional:    true,
							},
							"state_transition_history": schema.BoolAttribute{
								Description: "Whether the agent supports state transition history.",
								Optional:    true,
							},
						},
					},
					"provider": schema.SingleNestedBlock{
						Description: "The service provider of the agent.",
						Attributes: map[string]schema.Attribute{
							"organization": schema.StringAttribute{
								Description: "Organization name of the agent provider.",
								Optional:    true,
							},
							"url": schema.StringAttribute{
								Description: "URL of the agent provider.",
								Optional:    true,
							},
						},
					},
					"skills": schema.ListNestedBlock{
						Description: "Skills the agent can perform.",
						NestedObject: schema.NestedBlockObject{
							Attributes: map[string]schema.Attribute{
								"id": schema.StringAttribute{
									Description: "Unique identifier for the skill.",
									Required:    true,
								},
								"name": schema.StringAttribute{
									Description: "Display name of the skill.",
									Required:    true,
								},
								"description": schema.StringAttribute{
									Description: "Description of what the skill does.",
									Optional:    true,
								},
								"tags": schema.ListAttribute{
									Description: "Tags for categorizing the skill.",
									Optional:    true,
									ElementType: types.StringType,
								},
								"examples": schema.ListAttribute{
									Description: "Example inputs for the skill.",
									Optional:    true,
									ElementType: types.StringType,
								},
								"input_modes": schema.ListAttribute{
									Description: "Supported input MIME types.",
									Optional:    true,
									ElementType: types.StringType,
								},
								"output_modes": schema.ListAttribute{
									Description: "Supported output MIME types.",
									Optional:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
				},
			},
			"object_permission": schema.SingleNestedBlock{
				Description: "Access control permissions for the agent.",
				Attributes: map[string]schema.Attribute{
					"mcp_servers": schema.ListAttribute{
						Description: "MCP server IDs the agent can access.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"mcp_access_groups": schema.ListAttribute{
						Description: "MCP access groups the agent belongs to.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"mcp_tool_permissions": schema.MapAttribute{
						Description: "Per-MCP-server tool permissions (map of server ID to list of allowed tools, JSON-encoded).",
						Optional:    true,
						ElementType: types.StringType,
					},
					"models": schema.ListAttribute{
						Description: "Model IDs the agent can use.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"agents": schema.ListAttribute{
						Description: "Other agent IDs this agent can invoke.",
						Optional:    true,
						ElementType: types.StringType,
					},
				},
			},
		},
	}
}

func (r *AgentResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *AgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentReq := r.buildAgentRequest(&data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/v1/agents", agentReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create agent: %s", err))
		return
	}

	if agentID, ok := result["agent_id"].(string); ok {
		data.ID = types.StringValue(agentID)
	}

	// Read back for full state
	if err := r.readAgent(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Agent created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readAgent(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read agent: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = state.ID

	agentReq := r.buildAgentRequest(&data)

	endpoint := fmt.Sprintf("/v1/agents/%s", url.PathEscape(data.ID.ValueString()))
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, agentReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update agent: %s", err))
		return
	}

	if err := r.readAgent(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Agent updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/v1/agents/%s", url.PathEscape(data.ID.ValueString()))
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete agent: %s", err))
		return
	}
}

func (r *AgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Build request ---

func (r *AgentResource) buildAgentRequest(data *AgentResourceModel) map[string]interface{} {
	req := map[string]interface{}{
		"agent_name": data.AgentName.ValueString(),
	}

	// Agent card
	if data.AgentCard != nil {
		card := map[string]interface{}{
			"name": data.AgentCard.Name.ValueString(),
			"url":  data.AgentCard.URL.ValueString(),
		}
		if !data.AgentCard.Description.IsNull() && !data.AgentCard.Description.IsUnknown() {
			card["description"] = data.AgentCard.Description.ValueString()
		}
		if !data.AgentCard.Version.IsNull() && !data.AgentCard.Version.IsUnknown() {
			card["version"] = data.AgentCard.Version.ValueString()
		}
		if !data.AgentCard.ProtocolVersion.IsNull() && !data.AgentCard.ProtocolVersion.IsUnknown() {
			card["protocolVersion"] = data.AgentCard.ProtocolVersion.ValueString()
		}
		if !data.AgentCard.PreferredTransport.IsNull() && !data.AgentCard.PreferredTransport.IsUnknown() {
			card["preferredTransport"] = data.AgentCard.PreferredTransport.ValueString()
		}
		if !data.AgentCard.IconURL.IsNull() && !data.AgentCard.IconURL.IsUnknown() {
			card["iconUrl"] = data.AgentCard.IconURL.ValueString()
		}
		if !data.AgentCard.DocumentationURL.IsNull() && !data.AgentCard.DocumentationURL.IsUnknown() {
			card["documentationUrl"] = data.AgentCard.DocumentationURL.ValueString()
		}
		if !data.AgentCard.DefaultInputModes.IsNull() && !data.AgentCard.DefaultInputModes.IsUnknown() {
			card["defaultInputModes"] = listToStringSlice(data.AgentCard.DefaultInputModes)
		}
		if !data.AgentCard.DefaultOutputModes.IsNull() && !data.AgentCard.DefaultOutputModes.IsUnknown() {
			card["defaultOutputModes"] = listToStringSlice(data.AgentCard.DefaultOutputModes)
		}

		// Capabilities
		if data.AgentCard.Capabilities != nil {
			caps := map[string]interface{}{}
			if !data.AgentCard.Capabilities.Streaming.IsNull() && !data.AgentCard.Capabilities.Streaming.IsUnknown() {
				caps["streaming"] = data.AgentCard.Capabilities.Streaming.ValueBool()
			}
			if !data.AgentCard.Capabilities.PushNotifications.IsNull() && !data.AgentCard.Capabilities.PushNotifications.IsUnknown() {
				caps["pushNotifications"] = data.AgentCard.Capabilities.PushNotifications.ValueBool()
			}
			if !data.AgentCard.Capabilities.StateTransitionHistory.IsNull() && !data.AgentCard.Capabilities.StateTransitionHistory.IsUnknown() {
				caps["stateTransitionHistory"] = data.AgentCard.Capabilities.StateTransitionHistory.ValueBool()
			}
			if len(caps) > 0 {
				card["capabilities"] = caps
			}
		}

		// Provider
		if data.AgentCard.Provider != nil {
			prov := map[string]interface{}{}
			if !data.AgentCard.Provider.Organization.IsNull() && !data.AgentCard.Provider.Organization.IsUnknown() {
				prov["organization"] = data.AgentCard.Provider.Organization.ValueString()
			}
			if !data.AgentCard.Provider.URL.IsNull() && !data.AgentCard.Provider.URL.IsUnknown() {
				prov["url"] = data.AgentCard.Provider.URL.ValueString()
			}
			if len(prov) > 0 {
				card["provider"] = prov
			}
		}

		// Skills
		if len(data.AgentCard.Skills) > 0 {
			skills := make([]map[string]interface{}, 0, len(data.AgentCard.Skills))
			for _, s := range data.AgentCard.Skills {
				skill := map[string]interface{}{
					"id":   s.ID.ValueString(),
					"name": s.Name.ValueString(),
				}
				if !s.Description.IsNull() && !s.Description.IsUnknown() {
					skill["description"] = s.Description.ValueString()
				}
				if !s.Tags.IsNull() && !s.Tags.IsUnknown() {
					skill["tags"] = listToStringSlice(s.Tags)
				}
				if !s.Examples.IsNull() && !s.Examples.IsUnknown() {
					skill["examples"] = listToStringSlice(s.Examples)
				}
				if !s.InputModes.IsNull() && !s.InputModes.IsUnknown() {
					skill["inputModes"] = listToStringSlice(s.InputModes)
				}
				if !s.OutputModes.IsNull() && !s.OutputModes.IsUnknown() {
					skill["outputModes"] = listToStringSlice(s.OutputModes)
				}
				skills = append(skills, skill)
			}
			card["skills"] = skills
		}

		req["agent_card_params"] = card
	}

	// LiteLLM params
	if !data.LiteLLMParams.IsNull() && !data.LiteLLMParams.IsUnknown() {
		params := map[string]interface{}{}
		for k, v := range data.LiteLLMParams.Elements() {
			if sv, ok := v.(types.String); ok {
				params[k] = sv.ValueString()
			}
		}
		if len(params) > 0 {
			req["litellm_params"] = params
		}
	}

	// Object permission
	if data.ObjectPermission != nil {
		perm := map[string]interface{}{}
		if !data.ObjectPermission.MCPServers.IsNull() && !data.ObjectPermission.MCPServers.IsUnknown() {
			perm["mcp_servers"] = listToStringSlice(data.ObjectPermission.MCPServers)
		}
		if !data.ObjectPermission.MCPAccessGroups.IsNull() && !data.ObjectPermission.MCPAccessGroups.IsUnknown() {
			perm["mcp_access_groups"] = listToStringSlice(data.ObjectPermission.MCPAccessGroups)
		}
		if !data.ObjectPermission.Models.IsNull() && !data.ObjectPermission.Models.IsUnknown() {
			perm["models"] = listToStringSlice(data.ObjectPermission.Models)
		}
		if !data.ObjectPermission.Agents.IsNull() && !data.ObjectPermission.Agents.IsUnknown() {
			perm["agents"] = listToStringSlice(data.ObjectPermission.Agents)
		}
		if !data.ObjectPermission.MCPToolPermissions.IsNull() && !data.ObjectPermission.MCPToolPermissions.IsUnknown() {
			// MCPToolPermissions is map(string) where values are JSON-encoded arrays
			toolPerms := map[string]interface{}{}
			for k, v := range data.ObjectPermission.MCPToolPermissions.Elements() {
				if sv, ok := v.(types.String); ok {
					toolPerms[k] = sv.ValueString()
				}
			}
			if len(toolPerms) > 0 {
				perm["mcp_tool_permissions"] = toolPerms
			}
		}
		if len(perm) > 0 {
			req["object_permission"] = perm
		}
	}

	// Rate limits
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		req["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		req["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.SessionTPMLimit.IsNull() && !data.SessionTPMLimit.IsUnknown() {
		req["session_tpm_limit"] = data.SessionTPMLimit.ValueInt64()
	}
	if !data.SessionRPMLimit.IsNull() && !data.SessionRPMLimit.IsUnknown() {
		req["session_rpm_limit"] = data.SessionRPMLimit.ValueInt64()
	}

	// Headers
	if !data.StaticHeaders.IsNull() && !data.StaticHeaders.IsUnknown() {
		headers := map[string]interface{}{}
		for k, v := range data.StaticHeaders.Elements() {
			if sv, ok := v.(types.String); ok {
				headers[k] = sv.ValueString()
			}
		}
		if len(headers) > 0 {
			req["static_headers"] = headers
		}
	}
	if !data.ExtraHeaders.IsNull() && !data.ExtraHeaders.IsUnknown() {
		req["extra_headers"] = listToStringSlice(data.ExtraHeaders)
	}

	return req
}

// --- Read agent ---

func (r *AgentResource) readAgent(ctx context.Context, data *AgentResourceModel) error {
	agentID := data.ID.ValueString()
	if agentID == "" {
		return fmt.Errorf("agent ID is empty, cannot read agent")
	}

	endpoint := fmt.Sprintf("/v1/agents/%s", url.PathEscape(agentID))

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Top-level fields
	if v, ok := result["agent_id"].(string); ok {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["agent_name"].(string); ok {
		data.AgentName = types.StringValue(v)
	}

	// Computed timestamps
	if v, ok := result["created_at"].(string); ok && v != "" {
		data.CreatedAt = types.StringValue(v)
	}
	if v, ok := result["updated_at"].(string); ok && v != "" {
		data.UpdatedAt = types.StringValue(v)
	}
	if v, ok := result["created_by"].(string); ok && v != "" {
		data.CreatedBy = types.StringValue(v)
	}
	if v, ok := result["updated_by"].(string); ok && v != "" {
		data.UpdatedBy = types.StringValue(v)
	}

	// Rate limits
	if v, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(v))
	} else if !data.TPMLimit.IsNull() {
		// preserve user config
	}
	if v, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(v))
	}
	if v, ok := result["session_tpm_limit"].(float64); ok {
		data.SessionTPMLimit = types.Int64Value(int64(v))
	}
	if v, ok := result["session_rpm_limit"].(float64); ok {
		data.SessionRPMLimit = types.Int64Value(int64(v))
	}

	// LiteLLM params
	if params, ok := result["litellm_params"].(map[string]interface{}); ok && len(params) > 0 {
		paramMap := map[string]attr.Value{}
		for k, v := range params {
			paramMap[k] = types.StringValue(fmt.Sprintf("%v", v))
		}
		data.LiteLLMParams, _ = types.MapValue(types.StringType, paramMap)
	} else if data.LiteLLMParams.IsUnknown() {
		data.LiteLLMParams = types.MapNull(types.StringType)
	}

	// Static headers
	if headers, ok := result["static_headers"].(map[string]interface{}); ok && len(headers) > 0 {
		headerMap := map[string]attr.Value{}
		for k, v := range headers {
			headerMap[k] = types.StringValue(fmt.Sprintf("%v", v))
		}
		data.StaticHeaders, _ = types.MapValue(types.StringType, headerMap)
	} else if data.StaticHeaders.IsUnknown() {
		data.StaticHeaders = types.MapNull(types.StringType)
	}

	// Extra headers
	if headers, ok := result["extra_headers"].([]interface{}); ok && len(headers) > 0 {
		vals := make([]attr.Value, 0, len(headers))
		for _, h := range headers {
			if s, ok := h.(string); ok {
				vals = append(vals, types.StringValue(s))
			}
		}
		data.ExtraHeaders, _ = types.ListValue(types.StringType, vals)
	} else if data.ExtraHeaders.IsUnknown() {
		data.ExtraHeaders = types.ListNull(types.StringType)
	}

	// Agent card
	if cardRaw, ok := result["agent_card_params"].(map[string]interface{}); ok {
		r.readAgentCard(cardRaw, data)
	}

	// Object permission
	if permRaw, ok := result["object_permission"].(map[string]interface{}); ok {
		r.readObjectPermission(permRaw, data)
	}

	return nil
}

func (r *AgentResource) readAgentCard(cardRaw map[string]interface{}, data *AgentResourceModel) {
	if data.AgentCard == nil {
		data.AgentCard = &AgentCardModel{}
	}
	card := data.AgentCard

	if v, ok := cardRaw["name"].(string); ok {
		card.Name = types.StringValue(v)
	}
	if v, ok := cardRaw["description"].(string); ok && v != "" {
		card.Description = types.StringValue(v)
	}
	if v, ok := cardRaw["url"].(string); ok {
		card.URL = types.StringValue(v)
	}
	if v, ok := cardRaw["version"].(string); ok && v != "" {
		card.Version = types.StringValue(v)
	}
	if v, ok := cardRaw["protocolVersion"].(string); ok && v != "" {
		card.ProtocolVersion = types.StringValue(v)
	}
	if v, ok := cardRaw["preferredTransport"].(string); ok && v != "" {
		card.PreferredTransport = types.StringValue(v)
	}
	if v, ok := cardRaw["iconUrl"].(string); ok && v != "" {
		card.IconURL = types.StringValue(v)
	}
	if v, ok := cardRaw["documentationUrl"].(string); ok && v != "" {
		card.DocumentationURL = types.StringValue(v)
	}

	// Default modes
	if modes, ok := cardRaw["defaultInputModes"].([]interface{}); ok && len(modes) > 0 {
		card.DefaultInputModes = interfaceSliceToStringList(modes)
	} else if card.DefaultInputModes.IsUnknown() {
		card.DefaultInputModes = types.ListNull(types.StringType)
	}
	if modes, ok := cardRaw["defaultOutputModes"].([]interface{}); ok && len(modes) > 0 {
		card.DefaultOutputModes = interfaceSliceToStringList(modes)
	} else if card.DefaultOutputModes.IsUnknown() {
		card.DefaultOutputModes = types.ListNull(types.StringType)
	}

	// Capabilities
	if capsRaw, ok := cardRaw["capabilities"].(map[string]interface{}); ok {
		if card.Capabilities == nil {
			card.Capabilities = &AgentCapabilitiesModel{}
		}
		if v, ok := capsRaw["streaming"].(bool); ok {
			card.Capabilities.Streaming = types.BoolValue(v)
		}
		if v, ok := capsRaw["pushNotifications"].(bool); ok {
			card.Capabilities.PushNotifications = types.BoolValue(v)
		}
		if v, ok := capsRaw["stateTransitionHistory"].(bool); ok {
			card.Capabilities.StateTransitionHistory = types.BoolValue(v)
		}
	}

	// Provider
	if provRaw, ok := cardRaw["provider"].(map[string]interface{}); ok {
		if card.Provider == nil {
			card.Provider = &AgentProviderModel{}
		}
		if v, ok := provRaw["organization"].(string); ok {
			card.Provider.Organization = types.StringValue(v)
		}
		if v, ok := provRaw["url"].(string); ok {
			card.Provider.URL = types.StringValue(v)
		}
	}

	// Skills
	if skillsRaw, ok := cardRaw["skills"].([]interface{}); ok && len(skillsRaw) > 0 {
		skills := make([]AgentSkillModel, 0, len(skillsRaw))
		for _, sRaw := range skillsRaw {
			if s, ok := sRaw.(map[string]interface{}); ok {
				skill := AgentSkillModel{}
				if v, ok := s["id"].(string); ok {
					skill.ID = types.StringValue(v)
				}
				if v, ok := s["name"].(string); ok {
					skill.Name = types.StringValue(v)
				}
				if v, ok := s["description"].(string); ok {
					skill.Description = types.StringValue(v)
				}
				if v, ok := s["tags"].([]interface{}); ok {
					skill.Tags = interfaceSliceToStringList(v)
				} else {
					skill.Tags = types.ListNull(types.StringType)
				}
				if v, ok := s["examples"].([]interface{}); ok {
					skill.Examples = interfaceSliceToStringList(v)
				} else {
					skill.Examples = types.ListNull(types.StringType)
				}
				if v, ok := s["inputModes"].([]interface{}); ok {
					skill.InputModes = interfaceSliceToStringList(v)
				} else {
					skill.InputModes = types.ListNull(types.StringType)
				}
				if v, ok := s["outputModes"].([]interface{}); ok {
					skill.OutputModes = interfaceSliceToStringList(v)
				} else {
					skill.OutputModes = types.ListNull(types.StringType)
				}
				skills = append(skills, skill)
			}
		}
		card.Skills = skills
	}
}

func (r *AgentResource) readObjectPermission(permRaw map[string]interface{}, data *AgentResourceModel) {
	if data.ObjectPermission == nil {
		data.ObjectPermission = &AgentObjectPermissionModel{}
	}
	perm := data.ObjectPermission

	if v, ok := permRaw["mcp_servers"].([]interface{}); ok {
		perm.MCPServers = interfaceSliceToStringList(v)
	}
	if v, ok := permRaw["mcp_access_groups"].([]interface{}); ok {
		perm.MCPAccessGroups = interfaceSliceToStringList(v)
	}
	if v, ok := permRaw["models"].([]interface{}); ok {
		perm.Models = interfaceSliceToStringList(v)
	}
	if v, ok := permRaw["agents"].([]interface{}); ok {
		perm.Agents = interfaceSliceToStringList(v)
	}
	if v, ok := permRaw["mcp_tool_permissions"].(map[string]interface{}); ok {
		toolMap := map[string]attr.Value{}
		for k, val := range v {
			toolMap[k] = types.StringValue(fmt.Sprintf("%v", val))
		}
		perm.MCPToolPermissions, _ = types.MapValue(types.StringType, toolMap)
	}
}

// --- Helpers ---

func listToStringSlice(l types.List) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	elems := l.Elements()
	result := make([]string, 0, len(elems))
	for _, e := range elems {
		if sv, ok := e.(types.String); ok {
			result = append(result, sv.ValueString())
		}
	}
	return result
}

func interfaceSliceToStringList(items []interface{}) types.List {
	vals := make([]attr.Value, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			vals = append(vals, types.StringValue(s))
		}
	}
	if len(vals) == 0 {
		v, _ := types.ListValue(types.StringType, []attr.Value{})
		return v
	}
	v, _ := types.ListValue(types.StringType, vals)
	return v
}
