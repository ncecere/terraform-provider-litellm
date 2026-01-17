# litellm_prompt Resource

Manages a LiteLLM prompt template. Prompts allow you to store and version system prompts, templates, and other reusable text that can be used across different models and applications.

## Example Usage

### Minimal Example

```hcl
resource "litellm_prompt" "basic" {
  prompt_name = "greeting"
  prompt      = "Hello! How can I help you today?"
}
```

### Full Example

```hcl
resource "litellm_prompt" "customer_support" {
  prompt_name = "customer-support-agent"
  prompt      = <<-EOT
    You are a helpful customer support agent for Acme Corp.
    
    Guidelines:
    - Be polite and professional
    - Provide accurate information about our products
    - Escalate complex issues to human support
    - Never share sensitive customer data
    
    Available actions:
    - Check order status
    - Process returns
    - Answer product questions
    - Schedule callbacks
  EOT
  
  description = "System prompt for customer support chatbot"
  
  metadata = jsonencode({
    version     = "1.2"
    department  = "support"
    last_review = "2024-01-15"
  })
}
```

### Versioned Prompts

```hcl
resource "litellm_prompt" "assistant_v1" {
  prompt_name = "assistant-v1"
  prompt      = "You are a helpful assistant."
  description = "Version 1 - Basic assistant"
}

resource "litellm_prompt" "assistant_v2" {
  prompt_name = "assistant-v2"
  prompt      = <<-EOT
    You are a helpful assistant with expertise in coding.
    
    When answering questions:
    1. Provide clear, concise answers
    2. Include code examples when relevant
    3. Explain your reasoning
  EOT
  description = "Version 2 - Enhanced with coding focus"
}
```

### Multi-language Prompts

```hcl
locals {
  languages = {
    en = "You are a helpful assistant. Respond in English."
    es = "Eres un asistente útil. Responde en español."
    fr = "Vous êtes un assistant utile. Répondez en français."
    de = "Sie sind ein hilfreicher Assistent. Antworten Sie auf Deutsch."
  }
}

resource "litellm_prompt" "multilingual" {
  for_each    = local.languages
  prompt_name = "assistant-${each.key}"
  prompt      = each.value
  description = "Assistant prompt for ${upper(each.key)} language"
}
```

### Prompt with Variables

```hcl
resource "litellm_prompt" "templated" {
  prompt_name = "product-expert"
  prompt      = <<-EOT
    You are an expert on {{product_name}}.
    
    Your knowledge includes:
    - Product features and specifications
    - Pricing and availability
    - Troubleshooting common issues
    - Integration guides
    
    Always recommend checking the official documentation at {{docs_url}} for the most up-to-date information.
  EOT
  
  description = "Template for product-specific assistants"
  
  metadata = jsonencode({
    template_vars = ["product_name", "docs_url"]
  })
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `prompt_name` - (Required) Unique name for the prompt.
* `prompt` - (Required) The prompt text content.

### Optional Arguments

* `description` - (Optional) Description of the prompt's purpose.
* `metadata` - (Optional) JSON string containing additional metadata.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this prompt.
* `prompt_id` - The prompt ID (same as id).
* `created_at` - Timestamp when the prompt was created.
* `updated_at` - Timestamp when the prompt was last updated.

## Import

Prompts can be imported using the prompt name:

```shell
terraform import litellm_prompt.example customer-support-agent
```

## Notes

- Prompt names must be unique within the LiteLLM instance
- Use heredoc syntax (<<-EOT) for multi-line prompts
- Prompts can include template variables using {{variable}} syntax
- Version prompts by including version in the name (e.g., "prompt-v1", "prompt-v2")
- Store prompt version history in metadata for tracking changes
