package provider

import (
	"net/url"
	"strings"
)

// endpointWithPathSegment builds an endpoint whose dynamic value occupies one
// ordinary path segment. LiteLLM v1.98 routes that use ordinary path
// parameters cannot route an identity containing a decoded slash, but the
// provider still sends that identity as one safely escaped segment rather than
// changing its meaning or path structure.
func endpointWithPathSegment(prefix, value, suffix string) string {
	escaped := url.PathEscape(value)
	return prefix + hardenDotSegment(escaped) + suffix
}

// endpointWithPathCapture is separately named so the contract inventory keeps
// LiteLLM's {credential_name:path} routes distinct from ordinary path
// parameters. Its wire escaping is intentionally identical.
func endpointWithPathCapture(prefix, value, suffix string) string {
	escaped := url.PathEscape(value)
	return prefix + hardenDotSegment(escaped) + suffix
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
	if strings.ContainsAny(path, "?#") {
		panic("endpoint path must not contain a query or fragment")
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
