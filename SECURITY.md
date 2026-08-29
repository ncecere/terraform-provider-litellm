# Security Policy

## Supported versions

Security fixes are provided for the latest published release. Upgrade to the latest release before reporting an issue that may already be resolved.

## Reporting a vulnerability

Do not open a public issue for suspected vulnerabilities, exposed credentials, or customer data.

Use GitHub's private vulnerability reporting for this repository:

<https://github.com/ncecere/terraform-provider-litellm/security/advisories/new>

Include only the minimum information needed to reproduce the issue:

- affected provider and LiteLLM versions;
- the impacted resource or data source;
- a redacted configuration and reproduction sequence;
- expected and observed behavior; and
- the potential security impact.

Never submit live API keys, tokens, private Terraform state, customer identifiers, or unredacted logs. Replace sensitive values consistently so maintainers can still follow identity relationships.

You should receive an acknowledgement after the report is reviewed. Public disclosure and remediation timing will be coordinated based on severity and release readiness.

## Scope

Reports about this provider's credential handling, state exposure, diagnostics, request construction, lifecycle authorization, release artifacts, or dependency use are in scope. Vulnerabilities in LiteLLM itself should also be reported to the LiteLLM maintainers; explain any provider impact in the private report here.
