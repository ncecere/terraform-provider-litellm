package litellm

// ProviderConfig holds the configuration for the LiteLLM provider.
type ProviderConfig struct {
	APIBase string
	APIKey  string
}

// ErrorResponse represents an error response from the API.
type ErrorResponse struct {
	Error struct {
		Message interface{} `json:"message"`
	} `json:"error"`
	Detail struct {
		Error string `json:"error"`
	} `json:"detail"`
}

// ModelResponse represents a response from the API containing model information.
type ModelResponse struct {
	ModelName     string                 `json:"model_name"`
	LiteLLMParams LiteLLMParams          `json:"litellm_params"`
	ModelInfo     ModelInfo              `json:"model_info"`
	Additional    map[string]interface{} `json:"additional"`
}

// ModelRequest represents a request to create or update a model.
type ModelRequest struct {
	ModelName     string                 `json:"model_name"`
	LiteLLMParams LiteLLMParams          `json:"litellm_params"`
	ModelInfo     ModelInfo              `json:"model_info"`
	Additional    map[string]interface{} `json:"additional"`
}

// TeamResponse represents a response from the API containing team information.
type TeamResponse struct {
	TeamID         string                 `json:"team_id,omitempty"`
	TeamAlias      string                 `json:"team_alias,omitempty"`
	OrganizationID string                 `json:"organization_id,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	TPMLimit       int                    `json:"tpm_limit,omitempty"`
	RPMLimit       int                    `json:"rpm_limit,omitempty"`
	MaxBudget      float64                `json:"max_budget,omitempty"`
	BudgetDuration string                 `json:"budget_duration,omitempty"`
	Models         []string               `json:"models"`
	Blocked        bool                   `json:"blocked,omitempty"`
}

// LiteLLMParams represents the parameters for LiteLLM.
type LiteLLMParams struct {
	CustomLLMProvider              string                 `json:"custom_llm_provider"`
	TPM                            int                    `json:"tpm,omitempty"`
	RPM                            int                    `json:"rpm,omitempty"`
	ReasoningEffort                string                 `json:"reasoning_effort,omitempty"`
	Thinking                       map[string]interface{} `json:"thinking,omitempty"`
	MergeReasoningContentInChoices bool                   `json:"merge_reasoning_content_in_choices,omitempty"`
	APIKey                         string                 `json:"api_key,omitempty"`
	APIBase                        string                 `json:"api_base,omitempty"`
	APIVersion                     string                 `json:"api_version,omitempty"`
	Model                          string                 `json:"model"`
	InputCostPerToken              float64                `json:"input_cost_per_token,omitempty"`
	OutputCostPerToken             float64                `json:"output_cost_per_token,omitempty"`
	InputCostPerPixel              float64                `json:"input_cost_per_pixel,omitempty"`
	OutputCostPerPixel             float64                `json:"output_cost_per_pixel,omitempty"`
	InputCostPerSecond             float64                `json:"input_cost_per_second,omitempty"`
	OutputCostPerSecond            float64                `json:"output_cost_per_second,omitempty"`
	AWSAccessKeyID                 string                 `json:"aws_access_key_id,omitempty"`
	AWSSecretAccessKey             string                 `json:"aws_secret_access_key,omitempty"`
	AWSRegionName                  string                 `json:"aws_region_name,omitempty"`
	VertexProject                  string                 `json:"vertex_project,omitempty"`
	VertexLocation                 string                 `json:"vertex_location,omitempty"`
	VertexCredentials              string                 `json:"vertex_credentials,omitempty"`
}

// ModelInfo represents information about a model.
type ModelInfo struct {
	ID        string `json:"id"`
	DBModel   bool   `json:"db_model"`
	BaseModel string `json:"base_model"`
	Tier      string `json:"tier"`
	Mode      string `json:"mode"`
}

// Key represents a LiteLLM API key.
type Key struct {
	Key                  string                 `json:"key,omitempty"`
	Models               []string               `json:"models"`
	Spend                float64                `json:"spend,omitempty"`
	MaxBudget            float64                `json:"max_budget,omitempty"`
	UserID               string                 `json:"user_id,omitempty"`
	TeamID               string                 `json:"team_id,omitempty"`
	MaxParallelRequests  int                    `json:"max_parallel_requests,omitempty"`
	Metadata             map[string]interface{} `json:"metadata,omitempty"`
	TPMLimit             int                    `json:"tpm_limit,omitempty"`
	RPMLimit             int                    `json:"rpm_limit,omitempty"`
	BudgetDuration       string                 `json:"budget_duration"`
	AllowedCacheControls []string               `json:"allowed_cache_controls,omitempty"`
	SoftBudget           float64                `json:"soft_budget,omitempty"`
	KeyAlias             string                 `json:"key_alias,omitempty"`
	Duration             string                 `json:"duration,omitempty"`
	Aliases              map[string]interface{} `json:"aliases,omitempty"`
	Config               map[string]interface{} `json:"config,omitempty"`
	Permissions          map[string]interface{} `json:"permissions,omitempty"`
	ModelMaxBudget       map[string]interface{} `json:"model_max_budget,omitempty"`
	ModelRPMLimit        map[string]interface{} `json:"model_rpm_limit,omitempty"`
	ModelTPMLimit        map[string]interface{} `json:"model_tpm_limit,omitempty"`
	Guardrails           []string               `json:"guardrails,omitempty"`
	Blocked              bool                   `json:"blocked"`
	Tags                 []string               `json:"tags,omitempty"`
}

// KeyResponse represents a response from the API containing key information.
type KeyResponse struct {
	Key string `json:"key"`
}
