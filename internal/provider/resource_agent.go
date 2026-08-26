package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &AgentResource{}
var _ resource.ResourceWithImportState = &AgentResource{}
var _ resource.ResourceWithModifyPlan = &AgentResource{}

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
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Description  types.String `tfsdk:"description"`
	Tags         types.List   `tfsdk:"tags"`
	Examples     types.List   `tfsdk:"examples"`
	InputModes   types.List   `tfsdk:"input_modes"`
	OutputModes  types.List   `tfsdk:"output_modes"`
	Security     types.List   `tfsdk:"security"`
	SecurityJSON types.String `tfsdk:"security_json"`
}

type AgentCardSignatureModel struct {
	Protected  types.String `tfsdk:"protected"`
	Signature  types.String `tfsdk:"signature"`
	Header     types.String `tfsdk:"header"`
	HeaderJSON types.String `tfsdk:"header_json"`
}

type AgentObjectPermissionModel struct {
	MCPServers         types.List `tfsdk:"mcp_servers"`
	MCPAccessGroups    types.List `tfsdk:"mcp_access_groups"`
	MCPToolPermissions types.Map  `tfsdk:"mcp_tool_permissions"`
	Models             types.List `tfsdk:"models"`
	Agents             types.List `tfsdk:"agents"`
}

type AgentCardModel struct {
	Name                              types.String              `tfsdk:"name"`
	Description                       types.String              `tfsdk:"description"`
	URL                               types.String              `tfsdk:"url"`
	Version                           types.String              `tfsdk:"version"`
	ProtocolVersion                   types.String              `tfsdk:"protocol_version"`
	DefaultInputModes                 types.List                `tfsdk:"default_input_modes"`
	DefaultOutputModes                types.List                `tfsdk:"default_output_modes"`
	Capabilities                      *AgentCapabilitiesModel   `tfsdk:"capabilities"`
	Skills                            []AgentSkillModel         `tfsdk:"skills"`
	Provider                          *AgentProviderModel       `tfsdk:"provider"`
	PreferredTransport                types.String              `tfsdk:"preferred_transport"`
	IconURL                           types.String              `tfsdk:"icon_url"`
	DocumentationURL                  types.String              `tfsdk:"documentation_url"`
	SupportsAuthenticatedExtendedCard types.Bool                `tfsdk:"supports_authenticated_extended_card"`
	Signatures                        []AgentCardSignatureModel `tfsdk:"signatures"`
}

type AgentResourceModel struct {
	ID                types.String                `tfsdk:"id"`
	AgentName         types.String                `tfsdk:"agent_name"`
	AgentCard         *AgentCardModel             `tfsdk:"agent_card"`
	LiteLLMParams     types.Map                   `tfsdk:"litellm_params"`
	LiteLLMParamsJSON types.String                `tfsdk:"litellm_params_json"`
	ObjectPermission  *AgentObjectPermissionModel `tfsdk:"object_permission"`
	TPMLimit          types.Int64                 `tfsdk:"tpm_limit"`
	RPMLimit          types.Int64                 `tfsdk:"rpm_limit"`
	SessionTPMLimit   types.Int64                 `tfsdk:"session_tpm_limit"`
	SessionRPMLimit   types.Int64                 `tfsdk:"session_rpm_limit"`
	StaticHeaders     types.Map                   `tfsdk:"static_headers"`
	ExtraHeaders      types.List                  `tfsdk:"extra_headers"`
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
				Description: "Legacy literal string-only LiteLLM parameters. Values are never parsed or coerced. Use litellm_params_json for heterogeneous values.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"litellm_params_json": schema.StringAttribute{
				Description: "Lossless JSON-object bridge for arbitrary LiteLLM parameters. It merges with non-overlapping legacy map keys; overlapping values must be the identical JSON string value. Explicit configuration owns only its exact keys.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators:  []validator.String{agentJSONObjectValidator{}},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
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
				Description: "Static headers to send with agent requests. Marked sensitive because headers commonly contain authorization credentials.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
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
						Description: "A2A protocol version string. LiteLLM serves the 0.3 and 1.0 protocol families; the registry field itself is not narrowed to a provider-defined enum.",
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
					"supports_authenticated_extended_card": schema.BoolAttribute{
						Description: "Whether the agent supports an authenticated extended A2A card.",
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
					"signatures": schema.ListNestedBlock{
						Description: "Ordered JWS signatures. Duplicate entries are preserved. An empty list explicitly clears signatures.",
						NestedObject: schema.NestedBlockObject{Attributes: map[string]schema.Attribute{
							"protected":   schema.StringAttribute{Description: "JWS protected header.", Required: true, Sensitive: true},
							"signature":   schema.StringAttribute{Description: "JWS signature.", Required: true, Sensitive: true},
							"header":      schema.StringAttribute{Description: "Optional arbitrary non-null JWS header as a JSON object. Conflicts with header_json.", Optional: true, Sensitive: true, Validators: []validator.String{agentJSONObjectValidator{}}},
							"header_json": schema.StringAttribute{Description: "Strict JSON bridge for an arbitrary JWS header, including explicit JSON null. Conflicts with header.", Optional: true, Sensitive: true, Validators: []validator.String{agentJSONNullOrObjectValidator{}}},
						}},
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
								"security": schema.ListAttribute{
									Description: "Ordered non-null A2A security requirements. Each map value is an ordered list of scopes; duplicates are preserved. Conflicts with security_json.",
									Optional:    true,
									ElementType: types.MapType{ElemType: types.ListType{ElemType: types.StringType}},
								},
								"security_json": schema.StringAttribute{
									Description: "Strict JSON bridge for ordered A2A security requirements, including explicit JSON null. Conflicts with security.",
									Optional:    true,
									Sensitive:   true,
									Validators:  []validator.String{agentJSONNullOrSecurityValidator{}},
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
						Description: "Per-MCP-server tool permissions. The public type remains map(string): each value must be a JSON array containing only tool-name strings, such as jsonencode([\"list_issues\"]). Empty arrays and an empty map are valid explicit clears.",
						Optional:    true,
						ElementType: types.StringType,
						Validators:  []validator.Map{agentMCPToolPermissionsValidator{}},
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
	var planned AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	var config AgentResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Optional+Computed JSON is unknown in a legacy-only create plan. Request
	// construction uses the explicit configuration surface and never treats
	// that unknown as a structured value.
	if planned.LiteLLMParamsJSON.IsUnknown() {
		planned.LiteLLMParamsJSON = config.LiteLLMParamsJSON
	}
	agentReq, err := r.buildAgentRequest(&planned)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Agent Request", "The agent request could not be converted to the LiteLLM v1.98 wire shape.")
		return
	}

	var result map[string]interface{}
	accepted, createErr := r.client.doRequestWithResponse(ctx, "POST", "/v1/agents", agentReq, &result)
	if createErr != nil && !accepted {
		resp.Diagnostics.AddError("Agent Create Failed", "LiteLLM did not accept the agent create. No state was published.")
		return
	}

	agentID, ok := validReturnedAgentID(result)
	if createErr != nil || !ok {
		// A 2xx response proves acceptance but not identity. Recover only through
		// an exact-name, exact-fingerprint, unique list candidate; never guess.
		recoveredID, recoveryErr := r.recoverCreatedAgent(ctx, planned, config)
		if recoveryErr != nil {
			resp.Diagnostics.AddError("Agent Create Identity Unknown", "LiteLLM accepted the create, but bounded recovery did not find exactly one authoritative matching identity. No state was published.")
			return
		}
		agentID = recoveredID
	}
	planned.ID = types.StringValue(agentID)

	confirmed, err := r.confirmAgentMutation(ctx, planned, AgentResourceModel{}, config, nil, 8)
	if err != nil {
		setAgentIdentityOnlyCreateState(ctx, resp, types.StringValue(agentID))
		resp.Diagnostics.AddError("Agent Create Not Confirmed", "LiteLLM accepted the create, but authoritative read-back did not confirm a complete, current, fully known resource. Only the confirmed agent identity was retained for recovery.")
		return
	}
	resolveAgentUnknowns(&confirmed)
	if agentResourceHasUnknowns(confirmed) {
		setAgentIdentityOnlyCreateState(ctx, resp, types.StringValue(agentID))
		resp.Diagnostics.AddError("Agent Create Not Confirmed", "LiteLLM accepted the create, but authoritative read-back left unknown values. Only the confirmed agent identity was retained for recovery.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &confirmed)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(agentFieldSet{}))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipInitializedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(emptyAgentCollectionProvenance()))...)
	}
}

func setAgentIdentityOnlyCreateState(ctx context.Context, resp *resource.CreateResponse, confirmedAgentID types.String) {
	partial := emptyKnownAgentResourceModel()
	partial.ID = confirmedAgentID
	resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(agentFieldSet{}))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipInitializedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(emptyAgentCollectionProvenance()))...)
	}
}

func (r *AgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	prior := cloneAgentResourceModel(data)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	bundle, ownershipDiags := readAgentOwnershipBundle(ctx, req.Private)
	resp.Diagnostics.Append(ownershipDiags...)
	if resp.Diagnostics.HasError() {
		resp.State = req.State
		resp.Private = req.Private
		return
	}
	// Reads may use a working ownership copy to reconcile the public projection,
	// but an ordinary refresh has no configuration or planned transition to
	// confirm. It must therefore leave committed and pending provenance intact.
	apiOwned := cloneAgentFieldSet(bundle.committed)
	imported := string(importedMarker) == "true"

	var rawResult map[string]interface{}
	if err := r.readAgentWithOwnershipTransportCapture(ctx, &data, imported, apiOwned, false, &rawResult); err != nil {
		if IsAPIErrorStatus(err, 404) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Agent Read Failed", "LiteLLM did not return an authoritative agent response. Prior Terraform state was retained.")
		return
	}

	resolveAgentUnknowns(&data)
	if !imported {
		resp.Private = req.Private
		if agentModelsExactlyEqual(data, prior) {
			// State.Set canonicalizes framework collection/block representations.
			// When an authoritative read produced no public semantic change, retain
			// the incoming raw state so an ordinary refresh cannot manufacture one.
			resp.State = req.State
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !imported {
		// Only a successfully confirmed Create or Update may promote pending
		// ownership. Preserve ordinary-read private state byte-for-byte, including
		// a canonical pending transition whose API-owned field is omitted or null.
		return
	}
	if resp.Private != nil {
		apiOwned = agentImportedFieldsFromWire(data, rawResult)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(apiOwned))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipInitializedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(emptyAgentCollectionProvenance()))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *AgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var planned, state, config AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	bundle, ownershipDiags := readAgentOwnershipBundle(ctx, req.Private)
	resp.Diagnostics.Append(ownershipDiags...)
	if resp.Diagnostics.HasError() {
		resp.Private = req.Private
		resp.State = req.State
		return
	}
	importedFields := bundle.committed
	expectedOwnership := cloneAgentFieldSet(importedFields)
	for field := range agentConfiguredFields(config) {
		delete(expectedOwnership, field)
	}
	if (bundle.pending == nil && !agentFieldSetsEqual(expectedOwnership, importedFields)) || (bundle.pending != nil && !agentFieldSetsEqual(bundle.pending, expectedOwnership)) {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Invalid Agent Ownership State", "Provider-private pending agent ownership does not match the planned ownership transition. Prior public and private state was retained; no remote operation was attempted.")
		return
	}
	planned.ID = state.ID

	if err := validateAgentModelSkillIdentities(planned, state, config); err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Invalid Agent Skill Identity", err.Error())
		return
	}
	if err := validateAgentUpdateClears(planned, state, config, importedFields); err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Unsupported Agent Clear", err.Error())
		return
	}
	if bundle.migration && bundle.pending != nil && agentModelsExactlyEqual(planned, state) {
		// A legacy state can transfer ownership of equal configured values. The
		// unknown ID is only an apply trigger: verify two authoritative reads,
		// retain public state byte-for-byte, and commit only private provenance.
		_, err := r.confirmAgentMutation(ctx, planned, state, config, importedFields, 8)
		if err != nil {
			resp.Private = req.Private
			resp.State = req.State
			resp.Diagnostics.AddError("Agent Ownership Transfer Not Confirmed", "Authoritative read-back did not confirm an exact equal-value ownership transfer. Prior public and private state was retained; no remote mutation was attempted.")
			return
		}
		resp.Private = req.Private
		resp.State = req.State
		if resp.Private != nil {
			// pending already removes only the exact configured leaves that are
			// transferring to Terraform. Preserve every unrelated API-owned scope
			// marker so future remote additions remain correctly classified.
			committed := cloneAgentFieldSet(bundle.pending)
			collections := bundle.collections
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(committed))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(collections))...)
		}
		return
	}
	// Preserve #181's proxy-admin preflight. PATCH omission is independently
	// safe, but the preflight still recovers masked configured values and proves
	// secret-bearing remote state is readable before any mutation. Hydration is
	// wire-only: it must not replace explicit null/empty planned clears in state.
	wirePlanned := cloneAgentResourceModel(planned)
	if err := r.hydrateAgentUpdateFieldsWithOwnership(ctx, &wirePlanned, config, importedFields); err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Agent Update Preflight Failed", "The provider could not safely preserve masked or unmanaged agent configuration before mutation. The agent was not changed.")
		return
	}
	cardTouched := agentCardUpdateTouched(planned, state, config, importedFields)
	paramsTouched := agentParamsUpdateTouched(planned, state, config, importedFields)
	var preservation agentPatchPreservation
	if paramsTouched || cardTouched {
		// Every complete-object PATCH starts from two matching fresh-connection
		// samples. Terraform overlays only its exact owned keys/paths onto that
		// wire object; typed state is never authority for unowned values.
		paramsBase, cardBase, err := r.sampleFreshAgentUpdateBase(ctx, state, paramsTouched, cardTouched, 8)
		if err != nil {
			resp.Private = req.Private
			resp.State = req.State
			resp.Diagnostics.AddError("Agent Update Preflight Failed", "The provider could not obtain a stable fresh authoritative complete-object base. The agent was not changed.")
			return
		}
		if paramsTouched {
			preservation.paramsBase = paramsBase
			preservation.paramsPatch, err = overlayAgentParamsWire(paramsBase, state, config, importedFields)
			if err != nil {
				resp.Private = req.Private
				resp.State = req.State
				resp.Diagnostics.AddError("Invalid Agent Request", "The configured LiteLLM parameter overlay is not safe. The agent was not changed.")
				return
			}
		}
		if cardTouched {
			preservation.cardBase = cardBase
			preservation.cardPatch, err = overlayAgentCardRaw(cardBase, planned, state, config, importedFields)
			if err == nil {
				err = validateAgentCardV198RoundTrip(preservation.cardPatch)
			}
			if err != nil {
				resp.Private = req.Private
				resp.State = req.State
				resp.Diagnostics.AddError("Invalid Agent Request", "The configured agent-card overlay is not safe. The agent was not changed.")
				return
			}
		}
	}

	agentReq, err := r.buildAgentUpdateRequest(&wirePlanned, &state, &config, importedFields, false)
	if err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Invalid Agent Request", "The agent update could not be converted to the LiteLLM v1.98 wire shape. The agent was not changed.")
		return
	}
	if paramsTouched {
		agentReq["litellm_params"] = preservation.paramsPatch
	} else {
		delete(agentReq, "litellm_params")
	}
	if cardTouched {
		agentReq["agent_card_params"] = preservation.cardPatch
	} else {
		delete(agentReq, "agent_card_params")
	}
	endpoint := endpointWithPathSegment("/v1/agents/", planned.ID.ValueString(), "")
//line internal/provider/resource_agent.go:582
	if err := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, agentReq, nil); err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Agent Update Failed", "LiteLLM did not confirm the agent update. Prior Terraform state was retained.")
		return
	}

	confirmed, err := r.confirmAgentMutationWithPreservation(ctx, planned, state, config, importedFields, &preservation, 8)
	if err != nil {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Agent Update Not Confirmed", "LiteLLM accepted the update, but authoritative read-back did not confirm complete, current, stable planned values. Prior Terraform state was retained for recovery.")
		return
	}
	resolveAgentUnknowns(&confirmed)
	if agentResourceHasUnknowns(confirmed) {
		resp.Private = req.Private
		resp.State = req.State
		resp.Diagnostics.AddError("Agent Update Not Confirmed", "LiteLLM accepted the update, but authoritative read-back left unknown values. Prior Terraform state was retained for recovery.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &confirmed)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		committed := bundle.pending
		if committed == nil {
			committed = cloneAgentFieldSet(importedFields)
		}
		collections := bundle.collections
		if preservation.confirmedRaw != nil && planned.AgentCard != nil {
			collections = agentHiddenCollectionsFromRaw(preservation.confirmedRaw, planned)
			card, _ := preservation.confirmedRaw["agent_card_params"].(map[string]interface{})
			publicSkillIDs := map[string]bool{}
			for _, skill := range planned.AgentCard.Skills {
				if !skill.ID.IsNull() && !skill.ID.IsUnknown() {
					publicSkillIDs[skill.ID.ValueString()] = true
				}
			}
			for id, rawSkill := range agentSkillRawByID(card) {
				if !publicSkillIDs[id] && importedFields[agentScopeCardSkills] {
					markAgentSkillWireLeaves(committed, id, rawSkill)
				}
			}
			if importedFields[agentScopeCardSignatures] {
				for index, rawSignature := range agentWireObjectList(card["signatures"]) {
					if index < len(planned.AgentCard.Signatures) {
						continue
					}
					for _, field := range []string{"protected", "signature", "header"} {
						if _, present := rawSignature[field]; present {
							committed[agentSignatureLeaf(index, field)] = true
						}
					}
				}
			}
		}
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(committed))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(collections))...)
	}
}

func (r *AgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AgentResourceModel
	_, ownershipDiags := readAgentOwnershipBundle(ctx, req.Private)
	resp.Diagnostics.Append(ownershipDiags...)
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		resp.Private = req.Private
		resp.State = req.State
		return
	}

	endpoint := endpointWithPathSegment("/v1/agents/", data.ID.ValueString(), "")
//line internal/provider/resource_agent.go:622
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if IsAPIErrorStatus(err, 404) {
			return
		}
		resp.Diagnostics.AddError("Agent Delete Failed", "LiteLLM did not confirm deletion. Terraform state was retained so the operation can be retried safely.")
		return
	}
}

func (r *AgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipInitializedPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, nil)...)
	}
}

func agentMapContainsMaskedValues(value types.Map) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	for key, element := range value.Elements() {
		if stringValue, ok := element.(types.String); ok && !stringValue.IsNull() && !stringValue.IsUnknown() && isMaskedAgentAPIValue(key, stringValue.ValueString()) {
			return true
		}
	}
	return false
}

func hydrateAgentUpdateMap(planned types.Map, remote map[string]interface{}, excludeSyntheticIsPublic bool) (types.Map, error) {
	if planned.IsNull() || planned.IsUnknown() {
		return reconcileAgentStringMap(planned, remote, excludeSyntheticIsPublic)
	}
	values := make(map[string]attr.Value, len(planned.Elements()))
	for key, element := range planned.Elements() {
		if excludeSyntheticIsPublic && key == "is_public" {
			continue
		}
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			values[key] = element
			continue
		}
		if !isMaskedAgentAPIValue(key, stringValue.ValueString()) {
			// Preserve genuine planned updates; preflight only replaces masks.
			values[key] = stringValue
			continue
		}
		remoteValue, exists := remote[key]
		if !exists || isMaskedAgentAPIValue(key, remoteValue) {
			return planned, fmt.Errorf("LiteLLM did not return an unmasked value for agent configuration key %q during update preflight", key)
		}
		values[key] = types.StringValue(metadataValueToString(remoteValue))
	}
	value, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return planned, fmt.Errorf("build hydrated agent map: %v", diagnostics.Errors())
	}
	return value, nil
}

// hydrateUnmanagedAgentUpdateFields preserves the #181 secret preflight.
// PATCH omission no longer clears unmanaged fields, but configured masked
// values still require a PROXY_ADMIN read before mutation.
func mergeAgentAPIMapLeaves(planned types.Map, remote map[string]interface{}, prefix string, imported agentFieldSet) (types.Map, error) {
	values := map[string]attr.Value{}
	if !planned.IsNull() && !planned.IsUnknown() {
		for key, value := range planned.Elements() {
			values[key] = value
		}
	}
	for key, raw := range remote {
		if key == "is_public" && prefix == agentFieldParams {
			continue
		}
		if _, configured := values[key]; configured || !imported[agentLeaf(prefix, key)] {
			continue
		}
		if isMaskedAgentAPIValue(key, raw) {
			return planned, fmt.Errorf("API-owned agent map value is not recoverable")
		}
		values[key] = types.StringValue(metadataValueToString(raw))
	}
	value, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return planned, fmt.Errorf("build preserved agent map")
	}
	return value, nil
}

func (r *AgentResource) hydrateUnmanagedAgentUpdateFields(ctx context.Context, data *AgentResourceModel) error {
	return r.hydrateAgentUpdateFieldsWithOwnership(ctx, data, AgentResourceModel{}, nil)
}

func (r *AgentResource) hydrateAgentUpdateFieldsWithOwnership(ctx context.Context, data *AgentResourceModel, config AgentResourceModel, imported agentFieldSet) error {
	if imported == nil {
		imported = agentFieldSet{}
	}
	needsParams := data.LiteLLMParams.IsNull() || data.LiteLLMParams.IsUnknown() || agentMapContainsMaskedValues(data.LiteLLMParams) || (!config.LiteLLMParams.IsNull() && agentFieldSetHasPrefix(imported, agentFieldParams+"[")) || (imported[agentFieldParamsJSON] && config.LiteLLMParamsJSON.IsNull())
	needsHeaders := data.StaticHeaders.IsNull() || data.StaticHeaders.IsUnknown() || agentMapContainsMaskedValues(data.StaticHeaders) || (!config.StaticHeaders.IsNull() && agentFieldSetHasPrefix(imported, agentFieldStaticHeaders+"["))
	needsExtraHeaders := data.ExtraHeaders.IsNull() || data.ExtraHeaders.IsUnknown()
	if !needsParams && !needsHeaders && !needsExtraHeaders {
		return nil
	}

	endpoint := endpointWithPathSegment("/v1/agents/", data.ID.ValueString(), "")
	var result map[string]interface{}
//line internal/provider/resource_agent.go:730
	if err := r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}
	if err := validateImportedObjectIdentity(true, "agent", result, "agent_id", data.ID.ValueString()); err != nil {
		return err
	}
	if needsParams {
		rawParams, present := result["litellm_params"]
		params := map[string]interface{}{}
		if present && rawParams != nil {
			var ok bool
			params, ok = rawParams.(map[string]interface{})
			if !ok {
				return fmt.Errorf("LiteLLM returned malformed agent parameters during update preflight")
			}
		}
		if imported[agentFieldParamsJSON] && config.LiteLLMParamsJSON.IsNull() {
			if !present || rawParams == nil {
				return fmt.Errorf("LiteLLM omitted API-owned structured agent parameters during update preflight")
			}
			var err error
			data.LiteLLMParamsJSON, err = reconcileAgentJSONObject(data.LiteLLMParamsJSON, params)
			if err != nil {
				return err
			}
		}
		value, err := hydrateAgentUpdateMap(data.LiteLLMParams, params, true)
		if err != nil {
			return err
		}
		data.LiteLLMParams = value
		if !config.LiteLLMParams.IsNull() && !config.LiteLLMParams.IsUnknown() {
			data.LiteLLMParams, err = mergeAgentAPIMapLeaves(data.LiteLLMParams, params, agentFieldParams, imported)
			if err != nil {
				return err
			}
		}
	}
	if needsHeaders {
		headers, _ := result["static_headers"].(map[string]interface{})
		value, err := hydrateAgentUpdateMap(data.StaticHeaders, headers, false)
		if err != nil {
			return err
		}
		data.StaticHeaders = value
		if !config.StaticHeaders.IsNull() && !config.StaticHeaders.IsUnknown() {
			data.StaticHeaders, err = mergeAgentAPIMapLeaves(data.StaticHeaders, headers, agentFieldStaticHeaders, imported)
			if err != nil {
				return err
			}
		}
	}
	if needsExtraHeaders {
		if headers, ok := result["extra_headers"].([]interface{}); ok {
			data.ExtraHeaders = interfaceSliceToStringList(headers)
		} else {
			data.ExtraHeaders = types.ListValueMust(types.StringType, []attr.Value{})
		}
	}
	return nil
}

// --- Build request ---

func (r *AgentResource) buildAgentRequest(data *AgentResourceModel) (map[string]interface{}, error) {
	if err := validateAgentModelSkillIdentities(*data); err != nil {
		return nil, err
	}
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
		if !data.AgentCard.SupportsAuthenticatedExtendedCard.IsNull() && !data.AgentCard.SupportsAuthenticatedExtendedCard.IsUnknown() {
			card["supportsAuthenticatedExtendedCard"] = data.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool()
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

		// Signatures are an ordered complete-list replacement. Header is an
		// arbitrary JSON object and is decoded with UseNumber.
		if data.AgentCard.Signatures != nil {
			signatures := make([]map[string]interface{}, 0, len(data.AgentCard.Signatures))
			for _, configured := range data.AgentCard.Signatures {
				signature := map[string]interface{}{
					"protected": configured.Protected.ValueString(),
					"signature": configured.Signature.ValueString(),
				}
				if !configured.Header.IsNull() && !configured.Header.IsUnknown() {
					header, err := decodeAgentJSONObject(configured.Header.ValueString())
					if err != nil {
						return nil, err
					}
					signature["header"] = header
				}
				if !configured.HeaderJSON.IsNull() && !configured.HeaderJSON.IsUnknown() {
					header, err := decodeAgentNullOrObject(configured.HeaderJSON.ValueString())
					if err != nil {
						return nil, err
					}
					signature["header"] = header
				}
				signatures = append(signatures, signature)
			}
			card["signatures"] = signatures
		}

		// Skills. A non-nil empty list is an explicit complete-list replacement;
		// omission leaves the remote list untouched.
		if data.AgentCard.Skills != nil {
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
				if !s.Security.IsNull() && !s.Security.IsUnknown() {
					security, err := decodeAgentSecurity(s.Security)
					if err != nil {
						return nil, err
					}
					skill["security"] = security
				}
				if !s.SecurityJSON.IsNull() && !s.SecurityJSON.IsUnknown() {
					security, err := decodeAgentSecurityJSON(s.SecurityJSON.ValueString())
					if err != nil {
						return nil, err
					}
					skill["security"] = security
				}
				skills = append(skills, skill)
			}
			card["skills"] = skills
		}

		req["agent_card_params"] = card
	}

	// LiteLLM params. The legacy map is literal; only the additive JSON
	// attribute can introduce heterogeneous values.
	params, paramsConfigured, err := configuredAgentParams(data.LiteLLMParams, data.LiteLLMParamsJSON)
	if err != nil {
		return nil, err
	}
	if err := validateAgentCorePair(params); err != nil {
		return nil, err
	}
	if paramsConfigured && len(params) > 0 {
		req["litellm_params"] = params
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
			toolPerms, err := decodeConfiguredAgentMCPToolPermissions(data.ObjectPermission.MCPToolPermissions)
			if err != nil {
				return nil, err
			}
			// Preserve an explicitly configured empty map. LiteLLM distinguishes
			// omission from an empty object when permissions are being cleared.
			perm["mcp_tool_permissions"] = toolPerms
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

	return req, nil
}

// reconcileAgentStringMap keeps configured map keys selectively owned while
// allowing imports/unconfigured Optional+Computed maps to adopt API values.
// LiteLLM injects fields such as litellm_params.is_public and can mask
// credential values, neither of which should create false drift or expose
// secrets in normal CLI output.
func isMaskedAgentAPIValue(key string, rawValue interface{}) bool {
	value, ok := rawValue.(string)
	if !ok {
		return false
	}
	if isMaskedMetadataAPIString(value) {
		return true
	}
	lowerKey := strings.ToLower(key)
	sensitiveKey := false
	for _, keyword := range []string{"authorization", "token", "key", "secret", "vertex_credentials", "credentials", "password", "passwd"} {
		if strings.Contains(lowerKey, keyword) {
			sensitiveKey = true
			break
		}
	}
	if !sensitiveKey {
		return false
	}
	if value == "*****" {
		return true
	}
	runes := []rune(value)
	return len(runes) == 8 && string(runes[2:6]) == "****"
}

func reconcileAgentStringMap(current types.Map, raw map[string]interface{}, excludeSyntheticIsPublic bool) (types.Map, error) {
	managed := !current.IsNull() && !current.IsUnknown()
	configured := map[string]string{}
	if managed {
		if diagnostics := current.ElementsAs(context.Background(), &configured, false); diagnostics.HasError() {
			return current, fmt.Errorf("decode configured agent map: %v", diagnostics.Errors())
		}
	}
	observed := make(map[string]attr.Value)
	for key, rawValue := range raw {
		// LiteLLM injects is_public into response litellm_params, but it is not
		// part of the persisted/user-managed request map.
		if excludeSyntheticIsPublic && key == "is_public" {
			continue
		}
		configuredValue, owned := configured[key]
		if managed && !owned {
			continue
		}
		if isMaskedAgentAPIValue(key, rawValue) {
			if !owned {
				return current, fmt.Errorf("LiteLLM returned masked agent configuration without a prior Terraform value; use a PROXY_ADMIN credential for import/read")
			}
			observed[key] = types.StringValue(configuredValue)
			continue
		}
		value := metadataValueToString(rawValue)
		if owned {
			value = metadataValueToStringPreservingMasked(rawValue, configuredValue)
		}
		observed[key] = types.StringValue(value)
	}
	if stringMapMatchesAttrValues(current, observed) {
		return current, nil
	}
	value, diagnostics := types.MapValue(types.StringType, observed)
	if diagnostics.HasError() {
		return current, fmt.Errorf("build agent map state: %v", diagnostics.Errors())
	}
	return value, nil
}

func reconcileAgentStringMapWithOwnership(current types.Map, raw map[string]interface{}, excludeSyntheticIsPublic bool, prefix string, importAll bool, apiOwned agentFieldSet) (types.Map, error) {
	configured := map[string]string{}
	if !current.IsNull() && !current.IsUnknown() {
		if diagnostics := current.ElementsAs(context.Background(), &configured, false); diagnostics.HasError() {
			return current, fmt.Errorf("decode agent map")
		}
	}
	observed := make(map[string]attr.Value)
	for key, rawValue := range raw {
		if excludeSyntheticIsPublic && key == "is_public" {
			continue
		}
		marker := agentLeaf(prefix, key)
		prior, priorPresent := configured[key]
		scope := agentScopeParams
		if prefix == agentFieldStaticHeaders {
			scope = agentScopeStaticHeaders
		}
		ownedByAPI := importAll || apiOwned[marker]
		if !importAll && !priorPresent && apiOwned[marker] {
			// Already-adopted API leaf omitted from plan-consistent public state.
			// Keep its private/raw ownership without recreating perpetual drift.
			continue
		}
		if !priorPresent && !ownedByAPI && apiOwned[scope] {
			apiOwned[marker] = true
			ownedByAPI = true
		}
		if !priorPresent && isMaskedAgentAPIValue(key, rawValue) && (importAll || apiOwned[scope] || current.IsUnknown()) {
			return current, fmt.Errorf("masked agent map value is not recoverable; use a PROXY_ADMIN credential")
		}
		if !priorPresent && !ownedByAPI {
			continue
		}
		if isMaskedAgentAPIValue(key, rawValue) {
			if !priorPresent {
				return current, fmt.Errorf("masked agent map value is not recoverable; use a PROXY_ADMIN credential")
			}
			observed[key] = types.StringValue(prior)
			continue
		}
		// Preserve the historical import/state projection. Private JSON
		// provenance prevents these compatibility strings from becoming wire
		// authority on a later unrelated update.
		remoteString, isString := rawValue.(string)
		if !isString {
			value := metadataValueToString(rawValue)
			if priorPresent {
				value = metadataValueToStringPreservingMasked(rawValue, prior)
			}
			observed[key] = types.StringValue(value)
			continue
		}
		value := remoteString
		if priorPresent && !ownedByAPI && (prior == value || jsonSemanticallyEqual(prior, value)) {
			value = prior
		}
		observed[key] = types.StringValue(value)
	}
	for key := range configured {
		if _, present := raw[key]; !present && apiOwned[agentLeaf(prefix, key)] {
			delete(apiOwned, agentLeaf(prefix, key))
		}
	}
	if len(observed) == 0 && current.IsNull() {
		return current, nil
	}
	if stringMapMatchesAttrValues(current, observed) {
		return current, nil
	}
	value, diagnostics := types.MapValue(types.StringType, observed)
	if diagnostics.HasError() {
		return current, fmt.Errorf("build agent map state")
	}
	return value, nil
}

// --- Read agent ---

func (r *AgentResource) readAgent(ctx context.Context, data *AgentResourceModel) error {
	return r.readAgentWithOwnership(ctx, data, false, nil)
}

func (r *AgentResource) readAgentWithNumericOwnership(ctx context.Context, data *AgentResourceModel, imported bool) error {
	return r.readAgentWithOwnership(ctx, data, imported, nil)
}

func (r *AgentResource) readAgentWithOwnership(ctx context.Context, data *AgentResourceModel, imported bool, apiOwned agentFieldSet) error {
	return r.readAgentWithOwnershipTransport(ctx, data, imported, apiOwned, false)
}

func (r *AgentResource) readAgentFreshWithOwnership(ctx context.Context, data *AgentResourceModel, imported bool, apiOwned agentFieldSet) error {
	return r.readAgentWithOwnershipTransport(ctx, data, imported, apiOwned, true)
}

func (r *AgentResource) readAgentWithOwnershipTransport(ctx context.Context, data *AgentResourceModel, imported bool, apiOwned agentFieldSet, freshConnection bool) error {
	return r.readAgentWithOwnershipTransportCapture(ctx, data, imported, apiOwned, freshConnection, nil)
}

func (r *AgentResource) readAgentWithOwnershipTransportCapture(ctx context.Context, data *AgentResourceModel, imported bool, apiOwned agentFieldSet, freshConnection bool, capture *map[string]interface{}) error {
	if apiOwned == nil {
		apiOwned = agentFieldSet{}
	}
	agentID := data.ID.ValueString()
	manageAgentCard := imported || data.AgentCard != nil
	manageObjectPermission := imported || data.ObjectPermission != nil || apiOwned[agentScopePermission]
	if agentID == "" {
		return fmt.Errorf("agent ID is empty, cannot read agent")
	}

	endpoint := endpointWithPathSegment("/v1/agents/", agentID, "")

	var result map[string]interface{}
	var err error
	if freshConnection {
//line internal/provider/resource_agent.go:1204
		err = r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &result)
	} else {
//line internal/provider/resource_agent.go:1206
		err = r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result)
	}
	if err != nil {
		return err
	}
	if err := validateImportedObjectIdentity(true, "agent", result, "agent_id", agentID); err != nil {
		return err
	}
	if err := requireImportedStringField(true, "agent", result, "agent_name"); err != nil {
		return err
	}
	if capture != nil {
		*capture = cloneAgentWireObject(result)
	}

	// Identity is authoritative on every read, not only import. A successful
	// response for a different object must never overwrite requested state.
	data.ID = types.StringValue(result["agent_id"].(string))
	if name, ok := result["agent_name"].(string); ok && name != "" {
		data.AgentName = types.StringValue(name)
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

	// Rate limits are Optional-only. Imported state adopts visible values once;
	// ordinary lifecycle reads refresh only values already owned by Terraform.
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit},
		{"rpm_limit", &data.RPMLimit},
		{"session_tpm_limit", &data.SessionTPMLimit},
		{"session_rpm_limit", &data.SessionRPMLimit},
	} {
		owned := imported || apiOwned[field.name] || (!field.target.IsNull() && !field.target.IsUnknown())
		if err := updateInt64FromAPI(field.target, result, owned, owned, field.name); err != nil {
			return err
		}
	}

	// LiteLLM params. Present malformed values fail closed. Omission may be
	// role sanitization, so prior resource state is retained. Imports and an
	// existing/configured JSON bridge adopt the complete heterogeneous object;
	// legacy-only resources remain legacy-only and are never silently migrated.
	if rawParams, present := result["litellm_params"]; present && rawParams != nil {
		params, ok := rawParams.(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains malformed LiteLLM parameters")
		}
		value, err := reconcileAgentStringMapWithOwnership(data.LiteLLMParams, params, true, agentFieldParams, imported, apiOwned)
		if err != nil {
			return err
		}
		data.LiteLLMParams = value
		adoptJSON := imported || apiOwned[agentFieldParamsJSON] || (!data.LiteLLMParamsJSON.IsNull() && !data.LiteLLMParamsJSON.IsUnknown())
		if adoptJSON {
			structuredParams := params
			if !imported && !apiOwned[agentFieldParamsJSON] && !data.LiteLLMParamsJSON.IsNull() && !data.LiteLLMParamsJSON.IsUnknown() {
				priorObject, decodeErr := decodeAgentJSONObject(data.LiteLLMParamsJSON.ValueString())
				if decodeErr != nil {
					return decodeErr
				}
				structuredParams = make(map[string]interface{}, len(priorObject))
				for key := range priorObject {
					if value, present := params[key]; present {
						structuredParams[key] = value
					}
				}
			}
			data.LiteLLMParamsJSON, err = reconcileAgentJSONObject(data.LiteLLMParamsJSON, structuredParams)
			if err != nil {
				return fmt.Errorf("agent read response contains unrecoverable structured LiteLLM parameters")
			}
		}
	} else {
		if data.LiteLLMParams.IsUnknown() {
			data.LiteLLMParams = types.MapNull(types.StringType)
		}
		if data.LiteLLMParamsJSON.IsUnknown() {
			data.LiteLLMParamsJSON = types.StringNull()
		}
	}

	// Static headers
	if rawHeaders, present := result["static_headers"]; present && rawHeaders != nil {
		headers, ok := rawHeaders.(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains malformed static headers")
		}
		for _, raw := range headers {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("agent read response contains malformed static headers")
			}
		}
		value, err := reconcileAgentStringMapWithOwnership(data.StaticHeaders, headers, false, agentFieldStaticHeaders, imported, apiOwned)
		if err != nil {
			return err
		}
		data.StaticHeaders = value
	} else if data.StaticHeaders.IsUnknown() || (!data.StaticHeaders.IsNull() && len(data.StaticHeaders.Elements()) > 0) {
		data.StaticHeaders = types.MapNull(types.StringType)
	}

	// Extra headers
	if rawHeaders, present := result["extra_headers"]; present && rawHeaders != nil {
		headers, ok := rawHeaders.([]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains malformed extra headers")
		}
		for _, raw := range headers {
			if _, ok := raw.(string); !ok {
				return fmt.Errorf("agent read response contains malformed extra headers")
			}
		}
		data.ExtraHeaders = interfaceSliceToStringList(headers)
	} else if data.ExtraHeaders.IsUnknown() || (!data.ExtraHeaders.IsNull() && len(data.ExtraHeaders.Elements()) > 0) {
		data.ExtraHeaders = types.ListNull(types.StringType)
	}

	// Agent card
	if rawCard, present := result["agent_card_params"]; present && rawCard != nil {
		cardRaw, ok := rawCard.(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
		if err := validateAgentCardResponse(cardRaw, imported || data.AgentCard != nil); err != nil {
			return err
		}
		if manageAgentCard && (len(cardRaw) > 0 || data.AgentCard != nil) {
			if imported {
				r.readAgentCard(cardRaw, data)
			} else if err := r.reconcileAgentCardWithOwnership(cardRaw, data, apiOwned); err != nil {
				return err
			}
		}
	}

	// Object permission. A null or omitted object makes its MCP tool
	// permission absent, but must not alter the other independently scoped
	// nested fields.
	rawObjectPermission, objectPermissionPresent := result["object_permission"]
	if permRaw, ok := rawObjectPermission.(map[string]interface{}); ok {
		if manageObjectPermission {
			if err := r.readObjectPermissionWithOwnership(permRaw, data, imported, apiOwned); err != nil {
				return err
			}
		} else {
			// Validate present API data without adopting an unconfigured optional
			// block into state. This keeps omission/import ownership stable while
			// still failing closed on malformed permission responses.
			temporary := emptyKnownAgentResourceModel()
			if err := r.readObjectPermission(permRaw, &temporary); err != nil {
				return err
			}
		}
	} else if objectPermissionPresent && rawObjectPermission == nil {
		if data.ObjectPermission != nil && apiOwned[agentScopePermission] {
			// Explicit parent null is authoritative for imported leaves. Preserve
			// independently Terraform-owned siblings whose leaf markers transferred.
			for _, item := range []struct {
				field  string
				target *types.List
			}{
				{agentFieldPermissionServers, &data.ObjectPermission.MCPServers},
				{agentFieldPermissionGroups, &data.ObjectPermission.MCPAccessGroups},
				{agentFieldPermissionModels, &data.ObjectPermission.Models},
				{agentFieldPermissionAgents, &data.ObjectPermission.Agents},
			} {
				if apiOwned[item.field] {
					*item.target = types.ListNull(types.StringType)
					delete(apiOwned, item.field)
				}
			}
			if apiOwned[agentFieldPermissionTools] {
				data.ObjectPermission.MCPToolPermissions = types.MapNull(types.StringType)
				delete(apiOwned, agentFieldPermissionTools)
			}
			if data.ObjectPermission.MCPServers.IsNull() && data.ObjectPermission.MCPAccessGroups.IsNull() &&
				data.ObjectPermission.MCPToolPermissions.IsNull() && data.ObjectPermission.Models.IsNull() && data.ObjectPermission.Agents.IsNull() {
				data.ObjectPermission = nil
			}
		} else {
			reconcileAbsentAgentMCPToolPermissions(data)
			delete(apiOwned, agentFieldPermissionTools)
		}
	} else if !objectPermissionPresent {
		// Whole-object omission can be role sanitization. Preserve independently
		// scoped sibling lists while retaining #197's authoritative tool absence.
		reconcileAbsentAgentMCPToolPermissions(data)
		delete(apiOwned, agentFieldPermissionTools)
	} else {
		return invalidAgentMCPToolPermissionsResponseError{}
	}

	return nil
}

func (r *AgentResource) readAgentCard(cardRaw map[string]interface{}, data *AgentResourceModel) {
	populateAll := data.AgentCard == nil
	if data.AgentCard == nil {
		data.AgentCard = &AgentCardModel{}
	}
	card := data.AgentCard

	if v, ok := cardRaw["name"].(string); ok {
		card.Name = types.StringValue(v)
	}
	if v, ok := cardRaw["description"].(string); ok {
		card.Description = types.StringValue(v)
	} else if populateAll || !card.Description.IsNull() {
		card.Description = types.StringNull()
	}
	if v, ok := cardRaw["url"].(string); ok {
		card.URL = types.StringValue(v)
	}
	readOptionalCardString := func(rawName string, target *types.String) {
		if v, ok := cardRaw[rawName].(string); ok && (populateAll || !target.IsNull()) {
			*target = types.StringValue(v)
		} else if populateAll || !target.IsNull() {
			*target = types.StringNull()
		}
	}
	readOptionalCardString("version", &card.Version)
	readOptionalCardString("protocolVersion", &card.ProtocolVersion)
	readOptionalCardString("preferredTransport", &card.PreferredTransport)
	readOptionalCardString("iconUrl", &card.IconURL)
	readOptionalCardString("documentationUrl", &card.DocumentationURL)
	if populateAll {
		if value, ok := cardRaw["supportsAuthenticatedExtendedCard"].(bool); ok {
			card.SupportsAuthenticatedExtendedCard = types.BoolValue(value)
		}
	} else if !card.SupportsAuthenticatedExtendedCard.IsNull() {
		value, _ := cardRaw["supportsAuthenticatedExtendedCard"].(bool)
		card.SupportsAuthenticatedExtendedCard = types.BoolValue(value)
	}

	// Default modes
	if modes, ok := cardRaw["defaultInputModes"].([]interface{}); ok && (populateAll || !card.DefaultInputModes.IsNull()) {
		card.DefaultInputModes = interfaceSliceToStringList(modes)
	} else if populateAll || card.DefaultInputModes.IsUnknown() || !card.DefaultInputModes.IsNull() {
		card.DefaultInputModes = types.ListNull(types.StringType)
	}
	if modes, ok := cardRaw["defaultOutputModes"].([]interface{}); ok && (populateAll || !card.DefaultOutputModes.IsNull()) {
		card.DefaultOutputModes = interfaceSliceToStringList(modes)
	} else if populateAll || card.DefaultOutputModes.IsUnknown() || !card.DefaultOutputModes.IsNull() {
		card.DefaultOutputModes = types.ListNull(types.StringType)
	}

	// Capabilities are authoritative when the block/field is managed. LiteLLM
	// may accept unsupported flags but omit them from subsequent reads; an
	// omitted managed key therefore means false, not "preserve planned state".
	capsRaw, hasCapabilities := cardRaw["capabilities"].(map[string]interface{})
	if populateAll && hasCapabilities && len(capsRaw) > 0 {
		card.Capabilities = &AgentCapabilitiesModel{
			Streaming:              types.BoolNull(),
			PushNotifications:      types.BoolNull(),
			StateTransitionHistory: types.BoolNull(),
		}
		if value, present := capsRaw["streaming"].(bool); present {
			card.Capabilities.Streaming = types.BoolValue(value)
		}
		if value, present := capsRaw["pushNotifications"].(bool); present {
			card.Capabilities.PushNotifications = types.BoolValue(value)
		}
		if value, present := capsRaw["stateTransitionHistory"].(bool); present {
			card.Capabilities.StateTransitionHistory = types.BoolValue(value)
		}
	} else if !populateAll && card.Capabilities != nil {
		if !card.Capabilities.Streaming.IsNull() {
			card.Capabilities.Streaming = types.BoolValue(agentCapabilityValue(capsRaw, "streaming"))
		}
		if !card.Capabilities.PushNotifications.IsNull() {
			card.Capabilities.PushNotifications = types.BoolValue(agentCapabilityValue(capsRaw, "pushNotifications"))
		}
		if !card.Capabilities.StateTransitionHistory.IsNull() {
			card.Capabilities.StateTransitionHistory = types.BoolValue(agentCapabilityValue(capsRaw, "stateTransitionHistory"))
		}
	}

	// Provider
	if provRaw, ok := cardRaw["provider"].(map[string]interface{}); ok && (!populateAll || len(provRaw) > 0) && (populateAll || card.Provider != nil) {
		if card.Provider == nil {
			card.Provider = &AgentProviderModel{}
		}
		if v, ok := provRaw["organization"].(string); ok && (populateAll || !card.Provider.Organization.IsNull()) {
			card.Provider.Organization = types.StringValue(v)
		} else if populateAll || !card.Provider.Organization.IsNull() {
			card.Provider.Organization = types.StringNull()
		}
		if v, ok := provRaw["url"].(string); ok && (populateAll || !card.Provider.URL.IsNull()) {
			card.Provider.URL = types.StringValue(v)
		} else if populateAll || !card.Provider.URL.IsNull() {
			card.Provider.URL = types.StringNull()
		}
	} else if !populateAll && card.Provider != nil {
		card.Provider = nil
	}

	// Signatures preserve wire order and duplicates.
	if signaturesRaw, ok := cardRaw["signatures"].([]interface{}); ok && (populateAll || card.Signatures != nil) {
		signatures := make([]AgentCardSignatureModel, 0, len(signaturesRaw))
		for _, raw := range signaturesRaw {
			object, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			signature := AgentCardSignatureModel{
				Protected: types.StringNull(), Signature: types.StringNull(),
				Header: types.StringNull(), HeaderJSON: types.StringNull(),
			}
			if value, ok := object["protected"].(string); ok {
				signature.Protected = types.StringValue(value)
			}
			if value, ok := object["signature"].(string); ok {
				signature.Signature = types.StringValue(value)
			}
			if header, present := object["header"]; present {
				if header == nil {
					signature.HeaderJSON = types.StringValue("null")
				} else if objectHeader, ok := header.(map[string]interface{}); ok {
					encoded, _ := canonicalAgentJSON(objectHeader)
					signature.Header = types.StringValue(encoded)
				}
			}
			signatures = append(signatures, signature)
		}
		card.Signatures = signatures
	} else if !populateAll && card.Signatures != nil {
		card.Signatures = []AgentCardSignatureModel{}
	}

	// Skills
	if skillsRaw, ok := cardRaw["skills"].([]interface{}); ok && (populateAll || card.Skills != nil) {
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
				skill.Security = types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}})
				skill.SecurityJSON = types.StringNull()
				if v, present := s["security"]; present {
					if v == nil {
						skill.SecurityJSON = types.StringValue("null")
					} else {
						skill.Security, _ = readAgentSecurity(v)
					}
				}
				skills = append(skills, skill)
			}
		}
		if populateAll && len(skills) == 0 {
			card.Skills = nil
		} else {
			card.Skills = skills
		}
	} else if !populateAll && card.Skills != nil {
		card.Skills = []AgentSkillModel{}
	}
}

func (r *AgentResource) reconcileAgentCardWithOwnership(cardRaw map[string]interface{}, data *AgentResourceModel, apiOwned agentFieldSet) error {
	observedData := emptyKnownAgentResourceModel()
	r.readAgentCard(cardRaw, &observedData)
	observed := observedData.AgentCard
	if observed == nil {
		return nil
	}
	if data.AgentCard == nil {
		data.AgentCard = observed
		for field := range agentImportedFieldsFromState(observedData) {
			apiOwned[field] = true
		}
		return nil
	}
	prior := data.AgentCard
	out := cloneAgentResourceModel(*data).AgentCard
	reconcileString := func(field string, target *types.String, remote types.String) {
		old := *target
		if old.IsNull() && !apiOwned[field] {
			return
		}
		if !apiOwned[field] && !old.IsNull() && !old.IsUnknown() && !remote.IsNull() && !remote.IsUnknown() && old.ValueString() == remote.ValueString() {
			return
		}
		*target = remote
		if apiOwned[field] && remote.IsNull() {
			delete(apiOwned, field)
		}
	}
	reconcileBool := func(field string, target *types.Bool, remote types.Bool) {
		old := *target
		if old.IsNull() && !apiOwned[field] {
			return
		}
		if !apiOwned[field] && old.Equal(remote) {
			return
		}
		*target = remote
		if apiOwned[field] && remote.IsNull() {
			delete(apiOwned, field)
		}
	}
	reconcileList := func(field string, target *types.List, remote types.List, setLike bool) {
		old := *target
		if old.IsNull() && !apiOwned[field] {
			return
		}
		equal := old.Equal(remote)
		if setLike {
			equal = agentStringListSetEqual(old, remote)
		}
		if !apiOwned[field] && equal {
			return
		}
		*target = remote
		if apiOwned[field] && remote.IsNull() {
			delete(apiOwned, field)
		}
	}
	reconcileString(agentFieldCardName, &out.Name, observed.Name)
	reconcileString(agentFieldCardURL, &out.URL, observed.URL)
	reconcileString(agentFieldCardDescription, &out.Description, observed.Description)
	reconcileString(agentFieldCardVersion, &out.Version, observed.Version)
	reconcileString(agentFieldCardProtocol, &out.ProtocolVersion, observed.ProtocolVersion)
	reconcileList(agentFieldCardInputModes, &out.DefaultInputModes, observed.DefaultInputModes, false)
	reconcileList(agentFieldCardOutputModes, &out.DefaultOutputModes, observed.DefaultOutputModes, false)
	reconcileString(agentFieldCardTransport, &out.PreferredTransport, observed.PreferredTransport)
	reconcileString(agentFieldCardIcon, &out.IconURL, observed.IconURL)
	reconcileString(agentFieldCardDocumentation, &out.DocumentationURL, observed.DocumentationURL)
	reconcileBool(agentFieldCardAuthenticated, &out.SupportsAuthenticatedExtendedCard, observed.SupportsAuthenticatedExtendedCard)
	_, signaturesWirePresent := cardRaw["signatures"]
	if prior.Signatures != nil {
		// ListNestedBlock cardinality is configuration/state authority. A normal
		// Read refreshes only the previously public prefix and never resurrects
		// privately preserved or concurrently API-added signature entries.
		count := len(prior.Signatures)
		if count > len(observed.Signatures) {
			count = len(observed.Signatures)
		}
		out.Signatures = append([]AgentCardSignatureModel(nil), observed.Signatures[:count]...)
		if !apiOwned[agentFieldCardSignatures] && len(prior.Signatures) == len(out.Signatures) {
			for index := range out.Signatures {
				oldHeader, newHeader := prior.Signatures[index].Header, out.Signatures[index].Header
				if prior.Signatures[index].Protected.Equal(out.Signatures[index].Protected) && prior.Signatures[index].Signature.Equal(out.Signatures[index].Signature) &&
					!oldHeader.IsNull() && !oldHeader.IsUnknown() && !newHeader.IsNull() && !newHeader.IsUnknown() && jsonSemanticallyEqual(oldHeader.ValueString(), newHeader.ValueString()) {
					out.Signatures[index].Header = oldHeader
				}
			}
		}
		if !signaturesWirePresent {
			out.Signatures = make([]AgentCardSignatureModel, 0)
		}
	}

	capsRaw, _ := cardRaw["capabilities"].(map[string]interface{})
	if prior.Capabilities == nil {
		// Structural scope controls wire preservation, not public projection.
		// Only the initial imported read may materialize a previously absent
		// SingleNestedBlock; ordinary reads preserve prior block cardinality.
		out.Capabilities = nil
	} else {
		capabilitiesTerraformOwned := (!prior.Capabilities.Streaming.IsNull() && !apiOwned[agentFieldCardCapStreaming]) ||
			(!prior.Capabilities.PushNotifications.IsNull() && !apiOwned[agentFieldCardCapPush]) ||
			(!prior.Capabilities.StateTransitionHistory.IsNull() && !apiOwned[agentFieldCardCapHistory])
		if out.Capabilities == nil {
			out.Capabilities = &AgentCapabilitiesModel{}
		}
		remote := &AgentCapabilitiesModel{Streaming: types.BoolNull(), PushNotifications: types.BoolNull(), StateTransitionHistory: types.BoolNull()}
		if observed.Capabilities != nil {
			remote = observed.Capabilities
		}
		reconcileCapability := func(field, wire string, target *types.Bool, value types.Bool) {
			_, explicitlyPresent := capsRaw[wire]
			if !target.IsNull() && !target.IsUnknown() && !apiOwned[field] && !explicitlyPresent {
				value = types.BoolValue(false)
			}
			if target.IsNull() && !apiOwned[field] && apiOwned[agentScopeCardCapabilities] && explicitlyPresent {
				apiOwned[field] = true
			}
			if target.IsNull() && !apiOwned[field] {
				return
			}
			reconcileBool(field, target, value)
		}
		reconcileCapability(agentFieldCardCapStreaming, "streaming", &out.Capabilities.Streaming, remote.Streaming)
		reconcileCapability(agentFieldCardCapPush, "pushNotifications", &out.Capabilities.PushNotifications, remote.PushNotifications)
		reconcileCapability(agentFieldCardCapHistory, "stateTransitionHistory", &out.Capabilities.StateTransitionHistory, remote.StateTransitionHistory)
		if capsRaw == nil && apiOwned[agentScopeCardCapabilities] && !capabilitiesTerraformOwned {
			out.Capabilities = nil
		}
	}
	provRaw, _ := cardRaw["provider"].(map[string]interface{})
	if prior.Provider == nil {
		// LiteLLM-generated provider metadata remains remotely observable and is
		// preserved by structural scope, but cannot expand configured/legacy state.
		out.Provider = nil
	} else {
		providerTerraformOwned := (!prior.Provider.Organization.IsNull() && !apiOwned[agentFieldCardProviderOrg]) ||
			(!prior.Provider.URL.IsNull() && !apiOwned[agentFieldCardProviderURL])
		if out.Provider == nil {
			out.Provider = &AgentProviderModel{}
		}
		remote := &AgentProviderModel{Organization: types.StringNull(), URL: types.StringNull()}
		if observed.Provider != nil {
			remote = observed.Provider
		}
		if out.Provider.Organization.IsNull() && !apiOwned[agentFieldCardProviderOrg] && apiOwned[agentScopeCardProvider] {
			if _, present := provRaw["organization"]; present {
				apiOwned[agentFieldCardProviderOrg] = true
			}
		}
		if out.Provider.URL.IsNull() && !apiOwned[agentFieldCardProviderURL] && apiOwned[agentScopeCardProvider] {
			if _, present := provRaw["url"]; present {
				apiOwned[agentFieldCardProviderURL] = true
			}
		}
		reconcileString(agentFieldCardProviderOrg, &out.Provider.Organization, remote.Organization)
		reconcileString(agentFieldCardProviderURL, &out.Provider.URL, remote.URL)
		if provRaw == nil && apiOwned[agentScopeCardProvider] && !providerTerraformOwned {
			out.Provider = nil
		}
	}

	remoteByID := map[string]AgentSkillModel{}
	for _, skill := range observed.Skills {
		remoteByID[skill.ID.ValueString()] = skill
	}
	skills := make([]AgentSkillModel, 0, len(observed.Skills))
	seen := map[string]bool{}
	for _, old := range prior.Skills {
		id := old.ID.ValueString()
		remote, present := remoteByID[id]
		if !present {
			for _, leaf := range []string{"id", "name", "description", "tags", "examples", "input_modes", "output_modes", "security"} {
				delete(apiOwned, agentSkillLeaf(id, leaf))
			}
			continue
		}
		current := old
		reconcileString(agentSkillLeaf(id, "name"), &current.Name, remote.Name)
		// An omitted optional leaf in prior public block state remains omitted.
		// Its API-owned raw value is provenance for the next overlay, not license
		// for an ordinary Read to expand the public block after convergence.
		if !current.Description.IsNull() {
			reconcileString(agentSkillLeaf(id, "description"), &current.Description, remote.Description)
		}
		for _, leaf := range []struct {
			name    string
			target  *types.List
			value   types.List
			setLike bool
		}{
			{"tags", &current.Tags, remote.Tags, true},
			{"examples", &current.Examples, remote.Examples, false},
			{"input_modes", &current.InputModes, remote.InputModes, false},
			{"output_modes", &current.OutputModes, remote.OutputModes, false},
		} {
			if !leaf.target.IsNull() {
				reconcileList(agentSkillLeaf(id, leaf.name), leaf.target, leaf.value, leaf.setLike)
			}
		}
		securityField := agentSkillLeaf(id, "security")
		if !current.Security.IsNull() || !current.SecurityJSON.IsNull() {
			current.Security = remote.Security
			current.SecurityJSON = remote.SecurityJSON
			if remote.Security.IsNull() && remote.SecurityJSON.IsNull() {
				delete(apiOwned, securityField)
			}
		}
		skills = append(skills, current)
		seen[id] = true
	}
	// Remote identities absent from prior public state remain hidden. Initial
	// import uses readAgentCard above and is the sole full-resource projection;
	// ordinary Reads never adopt API additions into ListNestedBlock state.
	if prior.Skills == nil && len(skills) == 0 {
		out.Skills = nil
	} else {
		out.Skills = skills
	}
	data.AgentCard = out
	return nil
}

func agentCapabilityValue(capabilities map[string]interface{}, key string) bool {
	value, _ := capabilities[key].(bool)
	return value
}

func changedAgentCapabilityFieldsNotConverged(planned, prior, observed AgentResourceModel) []string {
	if planned.AgentCard == nil {
		return nil
	}

	var priorCard, observedCard *AgentCardModel
	priorCard = prior.AgentCard
	observedCard = observed.AgentCard
	stale := make([]string, 0, 4)
	check := func(name string, plannedValue types.Bool, priorValue types.Bool, priorPresent bool, observedValue types.Bool, observedPresent bool) {
		if plannedValue.IsNull() || plannedValue.IsUnknown() {
			return
		}
		if priorPresent && plannedValue.Equal(priorValue) {
			return
		}
		if !observedPresent || !plannedValue.Equal(observedValue) {
			stale = append(stale, name)
		}
	}

	check(
		"agent_card.supports_authenticated_extended_card",
		planned.AgentCard.SupportsAuthenticatedExtendedCard,
		priorAuthValue(priorCard),
		priorCard != nil,
		priorAuthValue(observedCard),
		observedCard != nil,
	)

	plannedCapabilities := planned.AgentCard.Capabilities
	if plannedCapabilities == nil {
		return stale
	}
	var priorCapabilities, observedCapabilities *AgentCapabilitiesModel
	if priorCard != nil {
		priorCapabilities = priorCard.Capabilities
	}
	if observedCard != nil {
		observedCapabilities = observedCard.Capabilities
	}
	checkCapability := func(name string, plannedValue types.Bool, priorValue types.Bool, observedValue types.Bool) {
		check(name, plannedValue, priorValue, priorCapabilities != nil, observedValue, observedCapabilities != nil)
	}
	checkCapability("agent_card.capabilities.streaming", plannedCapabilities.Streaming, capabilityStreaming(priorCapabilities), capabilityStreaming(observedCapabilities))
	checkCapability("agent_card.capabilities.push_notifications", plannedCapabilities.PushNotifications, capabilityPushNotifications(priorCapabilities), capabilityPushNotifications(observedCapabilities))
	checkCapability("agent_card.capabilities.state_transition_history", plannedCapabilities.StateTransitionHistory, capabilityStateTransitionHistory(priorCapabilities), capabilityStateTransitionHistory(observedCapabilities))
	return stale
}

func priorAuthValue(card *AgentCardModel) types.Bool {
	if card == nil {
		return types.BoolNull()
	}
	return card.SupportsAuthenticatedExtendedCard
}

func capabilityStreaming(capabilities *AgentCapabilitiesModel) types.Bool {
	if capabilities == nil {
		return types.BoolNull()
	}
	return capabilities.Streaming
}

func capabilityPushNotifications(capabilities *AgentCapabilitiesModel) types.Bool {
	if capabilities == nil {
		return types.BoolNull()
	}
	return capabilities.PushNotifications
}

func capabilityStateTransitionHistory(capabilities *AgentCapabilitiesModel) types.Bool {
	if capabilities == nil {
		return types.BoolNull()
	}
	return capabilities.StateTransitionHistory
}

func (r *AgentResource) readAgentCapabilitiesAfterUpdate(ctx context.Context, data *AgentResourceModel, planned, prior AgentResourceModel, maxRetries int) error {
	if maxRetries < 1 {
		return fmt.Errorf("maxRetries must be at least 1")
	}

	delay := 250 * time.Millisecond
	maxDelay := 2 * time.Second
	var lastErr error
	var staleFields []string
	consecutiveMatches := 0
	for attempt := 0; attempt < maxRetries; attempt++ {
		candidate := cloneAgentResourceModel(planned)
		lastErr = r.readAgent(ctx, &candidate)
		if lastErr == nil {
			staleFields = changedAgentCapabilityFieldsNotConverged(planned, prior, candidate)
			if len(staleFields) == 0 {
				consecutiveMatches++
				if consecutiveMatches >= 2 {
					*data = candidate
					return nil
				}
			} else {
				consecutiveMatches = 0
			}
		} else if !IsAPIErrorStatus(lastErr, 404) {
			return lastErr
		} else {
			consecutiveMatches = 0
		}

		if attempt == maxRetries-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("capability fields did not remain at their planned values after %d reads: %s", maxRetries, strings.Join(staleFields, ", "))
}

func cloneAgentResourceModel(source AgentResourceModel) AgentResourceModel {
	cloned := source
	if source.AgentCard != nil {
		card := *source.AgentCard
		cloned.AgentCard = &card
		if source.AgentCard.Capabilities != nil {
			capabilities := *source.AgentCard.Capabilities
			cloned.AgentCard.Capabilities = &capabilities
		}
		if source.AgentCard.Provider != nil {
			provider := *source.AgentCard.Provider
			cloned.AgentCard.Provider = &provider
		}
		cloned.AgentCard.Skills = append([]AgentSkillModel(nil), source.AgentCard.Skills...)
		cloned.AgentCard.Signatures = append([]AgentCardSignatureModel(nil), source.AgentCard.Signatures...)
	}
	if source.ObjectPermission != nil {
		permission := *source.ObjectPermission
		cloned.ObjectPermission = &permission
	}
	return cloned
}

func (r *AgentResource) readObjectPermissionWithOwnership(permRaw map[string]interface{}, data *AgentResourceModel, imported bool, apiOwned agentFieldSet) error {
	if imported {
		return r.readObjectPermission(permRaw, data)
	}
	if data.ObjectPermission == nil {
		if !apiOwned[agentScopePermission] {
			return nil
		}
		if err := r.readObjectPermission(permRaw, data); err != nil {
			return err
		}
		for field, wire := range map[string]string{
			agentFieldPermissionServers: "mcp_servers", agentFieldPermissionGroups: "mcp_access_groups",
			agentFieldPermissionTools: "mcp_tool_permissions", agentFieldPermissionModels: "models", agentFieldPermissionAgents: "agents",
		} {
			if value, present := permRaw[wire]; present && value != nil {
				apiOwned[field] = true
			}
		}
		return nil
	}
	remote := emptyKnownAgentResourceModel()
	if err := r.readObjectPermission(permRaw, &remote); err != nil {
		return err
	}
	if remote.ObjectPermission == nil {
		return nil
	}
	current := data.ObjectPermission
	copyList := func(field, wire string, target *types.List, observed types.List) {
		raw, present := permRaw[wire]
		if !present {
			// Omission may be role sanitization and cannot prove removal.
			return
		}
		if raw != nil {
			if target.IsNull() && !apiOwned[field] && apiOwned[agentScopePermission] {
				apiOwned[field] = true
			}
			if apiOwned[field] || !target.IsNull() {
				*target = observed
			}
			return
		}
		// Explicit null is authoritative absence for an imported/API-owned sibling
		// and real drift for a configured sibling. An unconfigured normal resource
		// remains unmanaged because it has neither marker nor state value.
		if apiOwned[field] || apiOwned[agentScopePermission] || !target.IsNull() {
			*target = types.ListNull(types.StringType)
			delete(apiOwned, field)
		}
	}
	copyList(agentFieldPermissionServers, "mcp_servers", &current.MCPServers, remote.ObjectPermission.MCPServers)
	copyList(agentFieldPermissionGroups, "mcp_access_groups", &current.MCPAccessGroups, remote.ObjectPermission.MCPAccessGroups)
	copyList(agentFieldPermissionModels, "models", &current.Models, remote.ObjectPermission.Models)
	copyList(agentFieldPermissionAgents, "agents", &current.Agents, remote.ObjectPermission.Agents)
	if current.MCPToolPermissions.IsNull() && !apiOwned[agentFieldPermissionTools] && apiOwned[agentScopePermission] {
		if value, present := permRaw["mcp_tool_permissions"]; present && value != nil {
			apiOwned[agentFieldPermissionTools] = true
		}
	}
	if apiOwned[agentFieldPermissionTools] || !current.MCPToolPermissions.IsNull() {
		if remote.ObjectPermission.MCPToolPermissions.IsNull() {
			reconcileAbsentAgentMCPToolPermissions(data)
			delete(apiOwned, agentFieldPermissionTools)
		} else {
			observed, err := decodeConfiguredAgentMCPToolPermissions(remote.ObjectPermission.MCPToolPermissions)
			if err != nil {
				return err
			}
			current.MCPToolPermissions, err = reconcileAgentMCPToolPermissions(current.MCPToolPermissions, observed)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *AgentResource) readObjectPermission(permRaw map[string]interface{}, data *AgentResourceModel) error {
	populateAll := data.ObjectPermission == nil
	if data.ObjectPermission == nil {
		data.ObjectPermission = &AgentObjectPermissionModel{
			MCPServers:         types.ListNull(types.StringType),
			MCPAccessGroups:    types.ListNull(types.StringType),
			MCPToolPermissions: types.MapNull(types.StringType),
			Models:             types.ListNull(types.StringType),
			Agents:             types.ListNull(types.StringType),
		}
	}
	perm := data.ObjectPermission

	readPermissionList := func(name string, target *types.List) error {
		raw, present := permRaw[name]
		if present && raw != nil {
			items, ok := raw.([]interface{})
			if !ok {
				return fmt.Errorf("agent read response contains a malformed object permission collection")
			}
			for _, item := range items {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("agent read response contains a malformed object permission collection")
				}
			}
			*target = interfaceSliceToStringList(items)
			return nil
		}
		// Omission leaves this independently scoped sibling unmanaged. Explicit
		// mutation confirmation decodes into an empty imported model, so a
		// requested set/clear still cannot be falsely confirmed.
		return nil
	}
	if err := readPermissionList("mcp_servers", &perm.MCPServers); err != nil {
		return err
	}
	if err := readPermissionList("mcp_access_groups", &perm.MCPAccessGroups); err != nil {
		return err
	}
	if err := readPermissionList("models", &perm.Models); err != nil {
		return err
	}
	if err := readPermissionList("agents", &perm.Agents); err != nil {
		return err
	}

	rawToolPermissions, present := permRaw["mcp_tool_permissions"]
	if !present || rawToolPermissions == nil {
		reconcileAbsentAgentMCPToolPermissions(data)
		return nil
	}
	observed, err := decodeObservedAgentMCPToolPermissions(rawToolPermissions)
	if err != nil {
		return err
	}
	// A configured non-null map owns the field, including an explicit empty
	// map. A nil block is import/API-owned and adopts deterministic JSON.
	owned := !perm.MCPToolPermissions.IsNull() && !perm.MCPToolPermissions.IsUnknown()
	if !owned && !populateAll {
		return nil
	}
	perm.MCPToolPermissions, err = reconcileAgentMCPToolPermissions(perm.MCPToolPermissions, observed)
	return err
}

// reconcileAbsentAgentMCPToolPermissions applies authoritative absence without
// disturbing sibling object_permission fields. An explicit empty map is the
// successful clear representation and remains stable. Any nonempty prior map
// becomes null so apply consistency checks and ordinary refresh expose that
// LiteLLM did not retain the requested permission.
func reconcileAbsentAgentMCPToolPermissions(data *AgentResourceModel) {
	if data.ObjectPermission == nil {
		return
	}
	current := data.ObjectPermission.MCPToolPermissions
	if current.IsNull() {
		return
	}
	if !current.IsUnknown() && len(current.Elements()) == 0 {
		return
	}
	data.ObjectPermission.MCPToolPermissions = types.MapNull(types.StringType)
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
