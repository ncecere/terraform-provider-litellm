# litellm_credential (Resource)

Manages a LiteLLM credential for storing sensitive authentication information. Credentials can be used to securely store API keys, tokens, and other sensitive data that can be referenced by models.

## Example Usage

### Minimal Example

```hcl
resource "litellm_credential" "minimal" {
  credential_name = "my-openai-cred"

  credential_info = {
    "description" = "OpenAI API credential"
  }

  credential_values = {
    "api_key" = "sk-your-api-key"
  }
}
```

### Full Example with Model Reference

```hcl
resource "litellm_credential" "openai" {
  credential_name = "openai-production"
  model_id        = "gpt-4o"

  credential_info = {
    "provider"    = "openai"
    "environment" = "production"
  }

  credential_values = {
    "api_key" = var.openai_api_key
    "org_id"  = var.openai_org_id
  }
}
```

### Azure OpenAI Credential

```hcl
resource "litellm_credential" "azure" {
  credential_name = "azure-openai-cred"

  credential_info = {
    "provider" = "azure"
    "service"  = "openai"
  }

  credential_values = {
    "api_key"     = var.azure_openai_key
    "api_base"    = var.azure_openai_endpoint
    "api_version" = "2024-02-15-preview"
  }
}
```

## Argument Reference

The following arguments are supported:

### Required

* `credential_name` - (Required, ForceNew) Name of the credential. Changing this forces creation of a new resource.
* `credential_values` - (Required, Sensitive) Map of sensitive credential values such as API keys and tokens. These values are **not** read back from the API and are preserved only in Terraform state.

### Optional

* `model_id` - (Optional) Model ID of an existing model registered in LiteLLM to associate with this credential.
* `credential_info` - (Optional, Computed) Map of additional non-sensitive metadata about the credential.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The identifier of the credential.

## Import

Credentials can be imported using their name:

```shell
terraform import litellm_credential.example "credential-name"
```

~> **Note:** Because `credential_values` is sensitive and not returned by the API, imported credentials will have empty credential values in state. You must re-apply with the correct values after import.

## Security Considerations

* The `credential_values` field is marked as sensitive and will not be displayed in Terraform plan output or logs.
* Credential values are not read back from the LiteLLM API for security reasons; they are preserved only in the Terraform state file.
* Ensure your Terraform state backend is properly secured (e.g., encrypted at rest) when using this resource.
