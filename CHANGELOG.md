# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.4] - 2026-02-11

### Fixed
- **All resources**: Resolved "Provider produced inconsistent result after apply" and "unknown values after apply" errors caused by three systemic bug patterns across the provider ([#53](https://github.com/ncecere/terraform-provider-litellm/issues/53)):

  **Pattern 1 — API response nesting:** Read functions accessed fields from the top-level response, but the LiteLLM API nests data under wrapper keys (e.g., `/key/info` returns `{"info": {...}}`, `/vector_store/info` returns `{"vector_store": {...}}`). Added unwrapping logic to all affected resources and datasources.
  - `litellm_key`, `litellm_team`, `litellm_organization` (resources and datasources)
  - `litellm_vector_store`

  **Pattern 2 — Else-clause zeroing (`!IsNull()` → `IsUnknown()`):** When the API didn't echo back a field, `else if !data.X.IsNull()` clauses zeroed out user-configured values to empty lists/maps, contradicting the planned value. Changed all such clauses to `else if data.X.IsUnknown()` so concrete config values are preserved.
  - `litellm_organization`: `models`, `tags`, `metadata`, `model_rpm_limit`, `model_tpm_limit`
  - `litellm_mcp_server`: `mcp_access_groups`, `args`, `env`, `credentials`, `allowed_tools`, `extra_headers`, `static_headers`, `tool_name_to_cost_per_query`
  - `litellm_vector_store`: `vector_store_metadata`, `litellm_params`
  - `litellm_key`: `model_rpm_limit`, `model_tpm_limit`
  - `litellm_team`: `model_aliases`, `model_rpm_limit`, `model_tpm_limit`
  - `litellm_model`: `access_groups`

  **Pattern 3 — API-injected defaults appearing in state:** The API returns default values for fields the user never configured (e.g., `budget_id`, `alias`, `allow_all_keys`, `mcp_info`, server-injected metadata keys). These caused "was null, but now has value" errors. Fixed by only setting these fields in state when the user originally configured them.
  - `litellm_key`: `budget_id`, `metadata` (filters server-injected `tpm_limit_type`/`rpm_limit_type`)
  - `litellm_team`: `metadata` (same filtering)
  - `litellm_organization`: `budget_id`
  - `litellm_mcp_server`: `alias`, `description`, `command`, `allow_all_keys`, `authorization_url`, `token_url`, `registration_url`, `mcp_info` block
  - `litellm_guardrail`: `default_on`, `litellm_params`
  - `litellm_vector_store`: `litellm_params` (filters server-injected keys)

- **`litellm_key`**: Fixed scalar `Optional+Computed` fields (`max_budget`, `tpm_limit`, `rpm_limit`, `max_parallel_requests`, `soft_budget`, `blocked`) remaining Unknown after apply when API returned null. Added explicit Unknown-to-Null resolution.
- **`litellm_organization`**: Fixed `blocked` remaining Unknown after apply.
- **`litellm_vector_store`**: Fixed create failing with "`where.vector_store_id`: A value is required" by generating a UUID client-side. Fixed create failing with `'litellm_params'` error by always sending `litellm_params` (even if empty) as the API requires it.
- **`litellm_search_tool`**: Fixed create/update requests not wrapped in `{"search_tool": {...}}` as the API requires. Fixed response parsing to unwrap nested `"search_tool"` key.
- **`litellm_tag`**: Fixed read function to handle changed API response format (`/tag/info` returns `{"tag-name": {...}}` map instead of array).
- **`litellm_key_block`**, **`litellm_team_block`**: Added `UseStateForUnknown` plan modifiers for immutable computed attributes (`created_at`, `created_by`, `key`, `blocked`).

### Removed
- **All resources**: Removed server-side runtime metrics from resource schemas (`spend`, `updated_at`, `status`, `budget_reset_at`, `models_updated`) that change outside Terraform and cause perpetual drift. These remain available in datasources.

### Added
- Regression tests for key and team readback behavior with nested API responses.
- Internal testing infrastructure (`internal_testing/`) with Docker Compose stack (LiteLLM proxy + Postgres 16) and Terraform test files for all 19 resources and 27 datasources.

## [1.0.3] - 2026-02-09

### Fixed
- **`litellm_team`**: Fixed "Provider returned invalid result object after apply" for omitted optional attributes by fully populating all `Optional + Computed` list/map fields in read state (`models`, `tags`, `guardrails`, `prompts`, `metadata`, `model_aliases`, `model_rpm_limit`, `model_tpm_limit`, `team_member_permissions`) ([#53](https://github.com/ncecere/terraform-provider-litellm/issues/53))
- **`litellm_model`**: Fixed "Provider returned invalid result object after apply" for omitted optional attributes by resolving unknown `access_groups` and `additional_litellm_params` values during readback ([#53](https://github.com/ncecere/terraform-provider-litellm/issues/53))
- **`litellm_key`**: Fixed incomplete readback for `Optional + Computed` fields that could leave unknown values after apply (`models`, `allowed_routes`, `allowed_passthrough_routes`, `allowed_cache_controls`, `guardrails`, `prompts`, `enforced_params`, `tags`, `metadata`, `aliases`, `config`, `permissions`, `model_max_budget`, `model_rpm_limit`, `model_tpm_limit`) and added update readback refresh.
- **`litellm_mcp_server`**: Fixed nested `Optional + Computed` readback for `mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query` so unknown values are resolved.
- **`litellm_organization_member`**: Fixed `user_id` (`Optional + Computed`) hydration when membership is created via `user_email`, by matching on email during reads and persisting the resolved user ID in state.

### Added
- Regression tests for team/model/key/MCP server readback behavior and organization member matching to ensure optional+computed attributes are always known after apply.

## [1.0.2] - 2026-02-07

### Changed
- **`litellm_model`**: Aligned `mode` values with the LiteLLM proxy API across validation and documentation. Supported values are now: `chat`, `completion`, `embedding`, `audio_speech`, `audio_transcription`, `image_generation`, `video_generation`, `batch`, `rerank`, `realtime`, `responses`, `ocr`, `moderation` ([#52](https://github.com/ncecere/terraform-provider-litellm/pull/52))
- **Documentation**: Updated `mode` list formatting in `docs/resources/model.md` for better readability and consistency

### Contributors
- Nick Silva (`@antisilent`) for [#52](https://github.com/ncecere/terraform-provider-litellm/pull/52)

## [1.0.1] - 2026-02-06

### Fixed
- **`litellm_user`**: Fixed "Provider produced inconsistent result after apply" error when creating a user without specifying `teams`, `models`, or `metadata` ([#51](https://github.com/ncecere/terraform-provider-litellm/issues/51))
- **All resources with optional list/map attributes**: Applied the same null-preservation fix across all affected resources to prevent empty API responses from overwriting null state values with empty collections
  - `litellm_user`: `teams`, `models`, `metadata`
  - `litellm_team`: `models`, `tags`, `guardrails`, `prompts`, `metadata`, `model_aliases`, `model_rpm_limit`, `model_tpm_limit`, `team_member_permissions`
  - `litellm_organization`: `models`, `tags`, `metadata`, `model_rpm_limit`, `model_tpm_limit`
  - `litellm_key`: `models`, `allowed_routes`, `allowed_passthrough_routes`, `metadata`, `allowed_cache_controls`, `aliases`, `config`, `permissions`, `model_max_budget`, `model_rpm_limit`, `model_tpm_limit`, `guardrails`, `prompts`, `enforced_params`, `tags`
  - `litellm_tag`: `models`
  - `litellm_model`: `access_groups`, `additional_litellm_params`
  - `litellm_mcp_server`: `mcp_access_groups`, `args`, `env`, `credentials`, `allowed_tools`, `extra_headers`, `static_headers`, `tool_name_to_cost_per_query`
  - `litellm_credential`: `credential_info`
  - `litellm_vector_store`: `vector_store_metadata`, `litellm_params`

### Changed
- All optional list and map attributes across all resources are now marked as `Optional + Computed` instead of `Optional` only, allowing the provider to correctly manage state when these attributes are omitted from configuration

### Added
- Unit tests for null-preservation logic validating correct behavior for list and map attributes

## [1.0.0] - 2026-02-05

### Added
- **Complete Provider Rewrite**: Migrated from terraform-plugin-sdk to terraform-plugin-framework v1.17.0
- **19 Resources**: model, key, key_block, team, team_block, team_member, team_member_add, mcp_server, credential, vector_store, organization, organization_member, user, budget, tag, access_group, prompt, guardrail, search_tool
- **26 Data Sources**: Single and list versions for all resources (model/models, key/keys, team/teams, etc.)
- **`litellm_model`**: Added `access_groups` field to assign models to access groups for team/key-based access control
- **New Resources**:
  - `litellm_budget` - Manage budget configurations
  - `litellm_tag` - Manage tags for cost tracking
  - `litellm_access_group` - Manage access groups for model access control
  - `litellm_prompt` - Manage prompt templates
  - `litellm_guardrail` - Manage guardrails for content moderation
  - `litellm_search_tool` - Manage search tools for RAG
  - `litellm_organization` - Manage organizations
  - `litellm_organization_member` - Manage organization memberships
  - `litellm_user` - Manage users
  - `litellm_key_block` - Block/unblock API keys
  - `litellm_team_block` - Block/unblock teams
- **Comprehensive Examples**: Added examples/ directory with minimal, complete, multi-provider, data-sources, mcp-servers, and search-tools configurations

### Changed
- Provider now uses terraform-plugin-framework for improved type safety and better Terraform integration
- Reorganized provider code into internal/provider/ package structure

## [0.3.16] - 2025-12-01

### Added
- `litellm_key`: Support service account keys (calls `/key/service-account/generate`), `allowed_routes`/`allowed_passthrough_routes`, and auto team-all-models default when `team_id` is set without `models`. (Issues #32, #28 context)
- `litellm_model`: Allow `mode = "batch"` for batch-capable models. (Issue #37)
- `litellm_team`: Metadata now accepts nested maps/lists instead of string-only values. (Issue #38)

### Fixed
- `litellm_model`: Added a safer post-create read retry that treats transient 404/not-found responses as retryable instead of clearing state, reducing "inconsistent result after apply" errors under concurrent creates. (Issue #41)

### Fixed
- `litellm_key`: Preserve state/readback for new key fields; optional fields remain backwards-compatible.
- `litellm_team_member_add`: Removing `max_budget_in_team` now clears the budget instead of setting it to `0`, avoiding accidental lockouts. (Issue #36)

## [0.3.14] - 2025-08-24

### Added
- **Enhanced JSON Parsing**: Added support for JSON string parsing in `additional_litellm_params`
  - JSON objects and arrays (starting with `{` or `[`) are now automatically parsed
  - Maintains backward compatibility with existing string-to-type conversion
  - Enables complex nested parameter configurations
- **Parameter Dropping Feature**: Added `additional_drop_params` special parameter
  - Allows removal of unwanted parameters from final `litellm_params` before API submission
  - Specified as JSON array string: `"additional_drop_params" = "[\"reasoningEffort\"]"`
  - Useful for overriding or removing built-in parameters when needed
- **Enhanced Examples**: Updated `examples/model_additional_params.tf` with comprehensive JSON parsing examples
  - Demonstrates all supported value types (boolean, integer, float, string, JSON objects/arrays)
  - Includes real-world Azure model configuration with parameter dropping
  - Shows both simple and complex use cases

### Changed
- **Documentation Enhancement**: Updated `docs/resources/model.md` with detailed JSON parsing documentation
  - Added comprehensive explanation of conversion rules and behavior
  - Included special `additional_drop_params` parameter documentation
  - Enhanced examples showing all supported parameter types and JSON parsing capabilities

### Technical Details
- Enhanced parameter processing logic in `createOrUpdateModel()` function
- Added JSON detection and parsing for string values starting with `[` or `{`
- Implemented parameter filtering system for `additional_drop_params`
- Maintains full backward compatibility with existing configurations

## [0.3.13] - 2025-08-24

### Changed
- Documentation: Performed a documentation audit and improvements across resources and data-sources. Added missing argument references, clarified types/defaults, documented implementation behaviors (e.g., additional_litellm_params parsing and state-preservation), and added an `examples/` directory with runnable HCL examples (starting with `examples/model_additional_params.tf`).
- Docs: Updated `docs/resources/model.md` with missing fields (`vertex_*`, pixel/second cost fields, and `additional_litellm_params`) and added conversion rules and an example.
- Docs Index: Added references to the new `examples/` directory in `docs/index.md`.

## [0.3.12] - 2025-08-13

### Added
- **New AWS Parameters**: Added `aws_session_name` and `aws_role_name` to model resource for cross-account access scenarios
  - Support for AWS session names in cross-account access configurations
  - Support for AWS IAM role names for cross-account access
  - Enhanced AWS Bedrock integration capabilities

### Changed
- **Documentation Overhaul**: Comprehensive update to all provider documentation
  - Updated provider source references from `bitop/litellm` to `registry.terraform.io/ncecere/litellm`
  - Consolidated all scattered example files into organized documentation structure
  - Enhanced all resource documentation with multiple real-world examples
  - Added comprehensive cross-resource integration examples
- **Vector Store Documentation**: Updated to reflect only officially supported LiteLLM providers
  - Removed unsupported providers (Pinecone, Weaviate, Chroma, Qdrant, Milvus, FAISS)
  - Added accurate examples for supported providers: AWS Bedrock Knowledge Bases, OpenAI Vector Stores, Azure Vector Stores, Vertex AI RAG Engine, PG Vector
  - Updated provider-specific parameters with correct configurations
  - Added references to official LiteLLM documentation
- **Project Organization**: Cleaned up project structure
  - Removed scattered example files from root directory
  - Consolidated all examples into comprehensive documentation
  - Updated README.md to reflect current capabilities and structure

### Fixed
- Corrected vector store provider documentation to match LiteLLM's official capabilities
- Updated all documentation links and references for accuracy

## [0.3.11] - 2025-08-10

### Added
- **New Resource**: `litellm_credential` - Manage credentials for secure authentication
  - Support for storing sensitive credential values (API keys, tokens, etc.)
  - Non-sensitive credential information storage
  - Model ID association for credentials
  - Secure handling of sensitive data with Terraform's sensitive attribute
- **New Resource**: `litellm_vector_store` - Manage vector stores for embeddings and RAG
  - Support for multiple vector store providers (Pinecone, Weaviate, Chroma, Qdrant, etc.)
  - Integration with credential management for secure authentication
  - Configurable metadata and provider-specific parameters
  - Full CRUD operations for vector store lifecycle management
- **New Data Source**: `litellm_credential` - Retrieve information about existing credentials
  - Read-only access to credential metadata (sensitive values excluded for security)
  - Support for model ID filtering
  - Cross-stack and cross-configuration referencing capabilities
- **New Data Source**: `litellm_vector_store` - Retrieve information about existing vector stores
  - Complete vector store information retrieval
  - Support for monitoring, validation, and cross-referencing use cases
  - Metadata-based conditional logic support
- Enhanced API response handling for credential and vector store operations
- Comprehensive documentation and examples for new resources and data sources
- Example Terraform configurations for common use cases

### Changed
- Extended `utils.go` with specialized API response handlers for credentials and vector stores
- Updated provider configuration to include new resources and data sources
- Enhanced error handling for credential and vector store not found scenarios

## [0.3.10] - 2025-08-10

### Added
- **New Resource**: `litellm_mcp_server` - Manage MCP (Model Context Protocol) servers
  - Support for HTTP, SSE, and stdio transport types
  - Configurable authentication types (none, bearer, basic)
  - MCP access groups for permission management
  - Cost tracking configuration for MCP tools
  - Environment variables and command arguments for stdio transport
  - Health check status monitoring
  - Comprehensive documentation and examples

### Changed
- Updated provider to support MCP server management functionality
- Enhanced API response handling for MCP-specific operations

## [0.3.9] - 2025-08-10

### Fixed
- Fixed issue where omitting `budget_duration` in key resource caused API error "Invalid duration format"
- Added missing `omitempty` JSON tag to `BudgetDuration` field in Key struct to prevent sending empty strings to API

## [0.3.8] - 2025-08-08

### Added
- Added `additional_litellm_params` field to model resource for custom parameters beyond standard ones
- Support for passing custom parameters like `drop_params`, `timeout`, `max_retries`, `organization`, etc.
- Automatic type conversion for string values to appropriate types (boolean, integer, float)
- Full backward compatibility with existing model configurations
- Comprehensive example demonstrating various use cases with different providers

## [0.3.7] - 2025-08-08

### Fixed
- Fixed issue where changing max_budget_in_team didn't update existing team members with new budget
- Added budget change detection using d.HasChange to update ALL existing members when budget changes
- Implemented tracking to avoid duplicate API calls for members already updated
- Enhanced debug logging for budget update operations

## [0.3.6] - 2025-08-08

### Fixed
- Fixed issue where models deleted from LiteLLM proxy caused terraform plan to fail instead of planning recreation
- Enhanced ErrorResponse struct to properly parse LiteLLM proxy error format with Detail field
- Improved isModelNotFoundError function to detect "not found on litellm proxy" messages in Detail.Error field

## [0.3.5] - 2025-08-08

### Fixed
- Fixed team member update behavior to use member_update endpoint instead of delete/re-add
- Restored team_member_permissions functionality to litellm_team resource
- Enhanced team resource with proper permissions management endpoints

## [0.3.0] - 2025-04-23

### Fixed
- Implemented retry mechanism with exponential backoff for model read operations
- Added detailed logging for retry attempts
- Improved error handling for "model not found" errors

## [0.2.9] - 2025-04-23

### Fixed
- Increased delay after model creation from 2 to 5 seconds to fix "model not found" errors
- Added logging to confirm delay is working properly

## [0.2.8] - 2025-04-23

### Fixed
- Added delay after model creation to fix "model not found" errors when the LiteLLM proxy hasn't fully registered the model yet

## [0.2.7] - 2025-04-23

### Fixed
- Fixed issue where `thinking_enabled` and `merge_reasoning_content_in_choices` values were not being preserved in state, causing Terraform to want to modify them on every run

## [0.2.6] - 2025-03-13

### Added
- Added new `merge_reasoning_content_in_choices` option to model resource

## [0.2.5] - 2025-03-13

### Fixed
- Fixed issue where `thinking_budget_tokens` was being added to models that don't have `thinking_enabled = true`

## [0.2.4] - 2025-03-13

### Added
- Added new `thinking` capability to model resource with configurable parameters:
  - `thinking_enabled` - Boolean to enable/disable thinking capability (default: false)
  - `thinking_budget_tokens` - Integer to set token budget for thinking (default: 1024)

## [0.2.2] - 2025-02-06

### Added
- Added new `reasoning_effort` parameter to model resource with values: "low", "medium", "high"
- Added "chat" mode to model resource

### Changed
- Updated model mode options to: "completion", "embedding", "image_generation", "chat", "moderation", "audio_transcription"

## [1.0.0] - 2024-01-17

### Added
- Initial release of the LiteLLM Terraform Provider
- Support for managing LiteLLM models
- Support for managing teams and team members
- Comprehensive documentation for all resources
