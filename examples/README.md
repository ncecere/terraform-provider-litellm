# LiteLLM Terraform Provider Examples

This directory contains example configurations for the LiteLLM Terraform provider. Each example demonstrates different use cases and configurations.

## Examples Overview

| Directory | Description |
|-----------|-------------|
| [minimal](./minimal/) | Simplest possible setup with basic model, team, and key |
| [complete](./complete/) | Full enterprise setup with all resource types |
| [multi-provider](./multi-provider/) | Configuring multiple LLM providers (OpenAI, Anthropic, Azure, Bedrock) |
| [data-sources](./data-sources/) | Using data sources to reference existing resources |
| [mcp-servers](./mcp-servers/) | MCP server configurations (HTTP, SSE, OAuth, stdio) |
| [search-tools](./search-tools/) | Search tool configurations (Tavily, Serper, Bing, Google) |

## Prerequisites

Before running any example:

1. **Install Terraform** (>= 1.0)
2. **Have a running LiteLLM instance**
3. **Set environment variables**:
   ```bash
   export LITELLM_API_BASE="https://your-litellm-instance.com"
   export LITELLM_API_KEY="your-api-key"
   ```

## Quick Start

### Minimal Example

```bash
cd minimal
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your values
terraform init
terraform plan
terraform apply
```

### Complete Enterprise Example

```bash
cd complete
cp terraform.tfvars.example terraform.tfvars
# Edit terraform.tfvars with your values
terraform init
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

A full enterprise setup demonstrating:
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

## Variable Files

Each example includes a `variables.tf` file. Create a `terraform.tfvars` file with your values:

```hcl
# terraform.tfvars example
litellm_api_base = "https://litellm.example.com"
litellm_api_key  = "sk-your-api-key"
openai_api_key   = "sk-your-openai-key"
```

Or use environment variables:

```bash
export TF_VAR_openai_api_key="sk-your-openai-key"
```

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
