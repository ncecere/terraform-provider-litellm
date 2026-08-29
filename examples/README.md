# LiteLLM Terraform Provider Examples

This directory contains example configurations for the LiteLLM Terraform provider. Each example demonstrates different use cases and configurations.

## Examples Overview

| Directory | Description |
|-----------|-------------|
| [minimal](./minimal/) | Simplest possible setup with basic model, team, and key |
| [complete](./complete/) | Broad enterprise-oriented setup across common resource types |
| [multi-provider](./multi-provider/) | Configuring multiple LLM providers (OpenAI, Anthropic, Azure, Bedrock) |
| [data-sources](./data-sources/) | Using data sources to reference existing resources |
| [mcp-servers](./mcp-servers/) | MCP server configurations (HTTP, SSE, OAuth, stdio) |
| [search-tools](./search-tools/) | Search tool configurations (Tavily, Serper, Bing, Google) |

## Prerequisites and Compatibility

Every runnable example pins the published source `registry.terraform.io/ncecere/litellm`, requires Terraform >= 1.1.0, and constrains the provider to `>= 2.0.1, < 3.0.0`. OpenTofu >= 1.6.0 is also supported. Provider development requires Go >= 1.24.0. The provider is tested against exactly LiteLLM 1.98.0.

The global client baseline does not require 1.11.0. Only configurations using the optional write-only `key_wo` or key/user `send_invite_email` attributes require Terraform or OpenTofu >= 1.11.0.

Before running any example:

1. **Install Terraform >= 1.1.0 or OpenTofu >= 1.6.0**
2. **Have a running LiteLLM 1.98.0 instance for the tested backend combination**
3. **Set environment variables**:
   ```bash
   export LITELLM_API_BASE="https://your-litellm-instance.com"
   export LITELLM_API_KEY="your-api-key"
   ```

## Quick Start

### Minimal Example

```bash
cd minimal
export TF_VAR_openai_api_key="your-openai-api-key"
terraform init -upgrade
terraform plan
terraform apply
```

### Complete Enterprise Example

```bash
cd complete
# Inputs are declared in variables.tf. These values are needed for the full example.
export TF_VAR_litellm_api_base="https://your-litellm-instance.com"
export TF_VAR_litellm_api_key="your-litellm-admin-key"
export TF_VAR_openai_api_key="your-openai-api-key"
export TF_VAR_anthropic_api_key="your-anthropic-api-key"
export TF_VAR_github_token="your-github-token"
export TF_VAR_tavily_api_key="your-tavily-api-key"
terraform init -upgrade
terraform plan
terraform apply
```

## Example Details

### Minimal (`minimal/`)

The simplest configuration to get started:
- Single model configuration
- One team
- One API key

Perfect for testing and development.

### Complete (`complete/`)

A broad enterprise-oriented setup demonstrating selected resource types, including:
- Credential management
- Multiple model configurations
- Organization and team hierarchy
- User management
- Access groups
- Tags for organization
- Prompts for system messages
- Guardrails for content safety
- MCP servers for tool access
- Search tools for web search
- Vector stores for RAG

### Multi-Provider (`multi-provider/`)

Demonstrates configuring models from multiple providers:
- OpenAI (GPT-4, GPT-4 Turbo, Embeddings)
- Anthropic (Claude 3 Opus, Sonnet, Haiku)
- Azure OpenAI
- AWS Bedrock

Includes access groups organized by provider and capability.

### Data Sources (`data-sources/`)

Shows how to use data sources for:
- Listing and analyzing existing models
- Calculating spend across teams
- Conditional resource creation
- Cross-stack references

### MCP Servers (`mcp-servers/`)

Complete examples of MCP server configurations:
- **HTTP transport**: GitHub integration
- **SSE transport**: Zapier automation
- **OAuth**: Enterprise API with OAuth2
- **Stdio**: Local development tools

### Search Tools (`search-tools/`)

Search tool configurations for different providers:
- Tavily (basic and advanced)
- Serper (Google search alternative)
- Bing Search API
- Google Custom Search
- Primary/fallback search strategy

## Best Practices

1. **Use variables for sensitive data**: Never hardcode API keys
2. **Use credentials resource**: Store provider API keys in LiteLLM credentials
3. **Organize with tags**: Use tags for cost allocation and filtering
4. **Set budget limits**: Always configure max_budget on teams and keys
5. **Use access groups**: Simplify model access management
6. **Configure guardrails**: Protect against harmful content

## Input Variables

Example inputs are declared in each example's `.tf` files. Supply required values with `TF_VAR_*` environment variables or create your own untracked `terraform.tfvars` file. Examples with an empty `provider "litellm" {}` block use `LITELLM_API_BASE` and `LITELLM_API_KEY`; examples that set `api_base` and `api_key` from variables require the corresponding `TF_VAR_litellm_api_base` and `TF_VAR_litellm_api_key` values instead.

```bash
export LITELLM_API_BASE="https://litellm.example.com"
export LITELLM_API_KEY="sk-your-api-key"
export TF_VAR_openai_api_key="sk-your-openai-key"
```

Do not commit credentials or local variable files.

## Troubleshooting

### Common Issues

1. **Connection refused**: Verify `LITELLM_API_BASE` is correct
2. **Unauthorized**: Check `LITELLM_API_KEY` is valid
3. **Model not found**: Ensure the base_model name is correct for the provider
4. **Rate limited**: Check TPM/RPM limits on models and keys

### Getting Help

- Check the [provider documentation](../docs/)
- Review the [LiteLLM documentation](https://docs.litellm.ai/)
- Open an issue on GitHub
