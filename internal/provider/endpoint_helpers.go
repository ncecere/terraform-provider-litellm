package provider

import (
	"net/url"
	"strings"
)

const invalidReviewedEndpoint = "/.terraform-provider-litellm-invalid-reviewed-endpoint"

// endpointWithPathSegment builds an endpoint whose dynamic value occupies one
// ordinary path segment. A decoded slash cannot be represented by a normal
// FastAPI {id} segment, so it fails locally through the fixed invalid endpoint
// marker rather than dispatching a request with changed identity semantics.
func endpointWithPathSegment(prefix, value, suffix string) string {
	if strings.Contains(value, "/") {
		return invalidReviewedEndpoint
	}
	escaped := url.PathEscape(value)
	return prefix + hardenDotSegment(escaped) + suffix
}

// endpointWithPathCapture is separately named so the contract inventory keeps
// LiteLLM's {credential_name:path} routes distinct from ordinary path
// parameters. Slash-bearing names are supported by that route. Complete dot
// components fail locally: no single wire representation can preserve their
// identity through both direct routing and an intermediary decode-and-normalize
// pass.
func endpointWithPathCapture(prefix, value, suffix string) string {
	if containsDotPathComponent(value) {
		return invalidReviewedEndpoint
	}
	escaped := url.PathEscape(value)
	return prefix + escaped + suffix
}

// endpointWithFallbackPathSegment is the sole reviewed exception for the
// merged #206 fallback contract. LiteLLM v1.98 returns a route-level 404 for a
// decoded slash in /fallback/{model}; retaining exact-once wire escaping keeps
// that documented server behavior without broadening ordinary segment use.
func endpointWithFallbackPathSegment(prefix, value, suffix string) string {
	escaped := url.PathEscape(value)
	return prefix + hardenDotSegment(escaped) + suffix
}

func containsDotPathComponent(value string) bool {
	for _, component := range strings.Split(value, "/") {
		if component == "." || component == ".." {
			return true
		}
	}
	return false
}

func hardenDotSegment(escaped string) string {
	switch escaped {
	case ".":
		return "%2E"
	case "..":
		return "%2E%2E"
	default:
		return escaped
	}
}

// endpointWithQuery accepts an unqueried endpoint and raw query values. Encode
// supplies canonical key/value ordering and performs the only query escaping.
// Existing query or fragment delimiters are rejected because merging them
// would make ownership of query keys ambiguous. Production callers are also
// statically restricted to reviewed path shapes by the contract extractor.
func endpointWithQuery(path string, values url.Values) string {
	if strings.HasPrefix(path, invalidReviewedEndpoint) {
		return invalidReviewedEndpoint
	}
	if strings.ContainsAny(path, "?#") {
		panic("endpoint path must not contain a query or fragment")
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
