# Dev Testing Workspace

Local Terraform files for testing against a dev LiteLLM deployment can be placed here.

This directory is intentionally git-ignored so provider config, state, `.tfvars`, and any secrets do not get committed.

Suggested usage:

```bash
cd dev_testing
terraform init
terraform plan
terraform apply
```

Use environment variables or local `.tfvars` files for sensitive values.
