# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Enhanced team member functionality merged into core `litellm_team_member` resource
  - `update_user_record` flag for automatic user-level updates
  - `cascade_delete_keys` flag for cleaning up API keys on deletion
  - `cleanup_orphaned_user` flag for removing users with no team memberships
  - `user_max_budget` and `budget_duration` fields for user-level budget management
- New resources for enterprise management:
  - `litellm_model_config` - Advanced model configuration management
  - `litellm_budget_alert` - Budget monitoring and alerting
  - `litellm_monitoring_config` - Observability integrations
  - `litellm_router_config` - Advanced routing configuration
  - `litellm_api_key_enhanced` - Enterprise API key lifecycle management
- New data source:
  - `litellm_spend_logs` - Query and analyze usage data
- Supporting utilities:
  - `cascading_cleanup.go` - Handles resource cleanup operations
  - `client_user_methods.go` - User management API methods

### Changed
- Enhanced `litellm_team_member` Read operation to verify team membership
- Improved state management and drift detection across all resources
- All enhanced features default to `false` for backward compatibility

### Fixed
- Team member resource now properly updates user-level records when configured
- API keys and users are cleaned up appropriately on deletion
- State drift issues resolved with proper Read implementations

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
