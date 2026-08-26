package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

const (
	maxSuccessResponseBody = int64(128 << 20) // 128 MiB
	maxErrorResponseBody   = int64(64 << 10)  // 64 KiB
	maxDiagnosticDetail    = 1024
	maxRequestIDLength     = 128
	maxDiagnosticDepth     = 16
	maxDiagnosticNumber    = 128
	maxCollectedSecrets    = 128
)

var (
	safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
	authorizationPattern  = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[^\s,"']+`)
	apiTokenPattern       = regexp.MustCompile(`(?i)\b(?:sk|pk|rk)-[A-Za-z0-9_./+=%-]{4,}`)
)

type requestSafety struct {
	suppressDetail  bool
	requestIDUnsafe bool
	secrets         []string
}

type safeTransportRetryCategory uint8

const (
	safeTransportRetryNone safeTransportRetryCategory = iota
	safeTransportRetryTimeout
	safeTransportRetryTemporary
	safeTransportRetryConnectionReset
)

type safeTransportError struct {
	kind          string
	identity      error
	timeout       bool
	canceled      bool
	deadline      bool
	retryCategory safeTransportRetryCategory
	dispatched    bool
}

func (e *safeTransportError) Error() string   { return e.kind }
func (e *safeTransportError) Unwrap() error   { return e.identity }
func (e *safeTransportError) Timeout() bool   { return e.timeout }
func (e *safeTransportError) Temporary() bool { return e.Retryable() }
func (e *safeTransportError) Retryable() bool { return e.retryCategory != safeTransportRetryNone }

// safeDispatchedTransportFailure records that an HTTP transport was given the
// request. Unless the failure proves a terminal TLS/protocol problem, a create
// may have reached LiteLLM before the transport lost its response.
func safeDispatchedTransportFailure(err error) error {
	safeErr := safeTransportFailure(err)
	var transportErr *safeTransportError
	if errors.As(safeErr, &transportErr) {
		transportErr.dispatched = true
	}
	return safeErr
}

type safeResponseError struct {
	statusCode int
	requestID  string
	kind       string
	identity   error
	retryable  bool
	dispatched bool
	accepted   bool
}

func (e *safeResponseError) Error() string {
	message := e.kind
	if e.statusCode != 0 {
		message = fmt.Sprintf("%s (HTTP %d)", message, e.statusCode)
	}
	if e.requestID != "" {
		message += fmt.Sprintf(" (request ID %q)", e.requestID)
	}
	return message
}
func (e *safeResponseError) Unwrap() error   { return e.identity }
func (e *safeResponseError) Temporary() bool { return e.retryable }

func safeTransportFailure(err error) error {
	kind := "LiteLLM HTTP transport request failed"
	var identity error
	timedOut := false
	canceled := false
	deadline := false
	retryCategory := safeTransportRetryNone
	switch {
	case errors.Is(err, context.Canceled):
		kind = "LiteLLM HTTP request was canceled"
		identity = context.Canceled
		canceled = true
	case errors.Is(err, context.DeadlineExceeded):
		kind = "LiteLLM HTTP request timed out"
		identity = context.DeadlineExceeded
		timedOut = true
		deadline = true
		retryCategory = safeTransportRetryTimeout
	default:
		var netErr net.Error
		var certErr x509.UnknownAuthorityError
		var hostErr x509.HostnameError
		var invalidCertErr x509.CertificateInvalidError
		var rootsErr x509.SystemRootsError
		var verificationErr *tls.CertificateVerificationError
		var recordErr tls.RecordHeaderError
		switch {
		case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &invalidCertErr), errors.As(err, &rootsErr), errors.As(err, &verificationErr), errors.As(err, &recordErr):
			// TLS verification and local trust/configuration failures are terminal.
			kind = "LiteLLM TLS verification failed"
		case errors.As(err, &netErr) && netErr.Timeout():
			kind = "LiteLLM HTTP request timed out"
			identity = context.DeadlineExceeded
			timedOut = true
			retryCategory = safeTransportRetryTimeout
		case errors.As(err, &netErr) && netErr.Temporary():
			retryCategory = safeTransportRetryTemporary
		case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF),
			errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNABORTED),
			errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EPIPE),
			errors.Is(err, syscall.ENETDOWN), errors.Is(err, syscall.ENETUNREACH),
			errors.Is(err, syscall.EHOSTUNREACH):
			retryCategory = safeTransportRetryConnectionReset
		}
	}
	return &safeTransportError{kind: kind, identity: identity, timeout: timedOut, canceled: canceled, deadline: deadline, retryCategory: retryCategory}
}

func safeTemporaryResponseFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary() || errors.Is(err, syscall.ECONNRESET))
}

func safeErrorIdentity(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func readBoundedBody(reader io.Reader, limit int64) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return body[:limit], true, nil
	}
	return body, false, nil
}

func classifyRequestSafety(request *http.Request, relativePath string, body []byte, providerKey string) requestSafety {
	safety := requestSafety{}
	if providerKey != "" {
		safety.addSecret(providerKey)
	}

	requestPath := request.URL.Path
	if relativeURL, err := url.Parse(relativePath); err == nil && relativeURL.Path != "" {
		requestPath = relativeURL.Path
	}
	requestPath = strings.ToLower(requestPath)
	for _, prefix := range []string{
		"/key", "/model", "/credentials", "/v1/agents", "/v1/mcp/server",
		"/search_tools", "/prompts", "/vector_store", "/guardrails",
	} {
		if strings.HasPrefix(requestPath, prefix) {
			safety.suppressDetail = true
			break
		}
	}

	for name, values := range request.URL.Query() {
		if isSensitiveField(name) {
			safety.suppressDetail = true
			for _, value := range values {
				safety.addSecret(value)
			}
		}
	}

	if len(body) == 0 {
		return safety
	}
	var value interface{}
	if err := decodeJSONUseNumber(body, &value); err != nil {
		// A request body the classifier cannot understand must fail closed.
		safety.suppressDetail = true
		return safety
	}
	if inspectRequestValue(value, "", 0, false, &safety) {
		safety.suppressDetail = true
	}
	return safety
}

func inspectRequestValue(value interface{}, key string, depth int, inheritedSensitive bool, safety *requestSafety) bool {
	if depth > maxDiagnosticDepth {
		return true
	}
	sensitive := inheritedSensitive || isSensitiveField(key) || isOpaqueContainer(key)
	switch typed := value.(type) {
	case map[string]interface{}:
		for childKey, childValue := range typed {
			if inspectRequestValue(childValue, childKey, depth+1, sensitive, safety) {
				sensitive = true
			}
		}
	case []interface{}:
		for _, childValue := range typed {
			if inspectRequestValue(childValue, key, depth+1, sensitive, safety) {
				sensitive = true
			}
		}
	case string:
		if sensitive {
			safety.addSecret(typed)
		}
	case float64:
		if sensitive {
			safety.addSecret(strconv.FormatFloat(typed, 'g', -1, 64))
		}
	}
	return sensitive
}

func normalizeFieldName(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(value)
}

func isSensitiveField(key string) bool {
	normalized := normalizeFieldName(key)
	if normalized == "" {
		return false
	}
	for _, exact := range []string{
		"key", "apikey", "accesskey", "secretkey", "token", "accesstoken", "refreshtoken",
		"sessiontoken", "password", "passwd", "secret", "clientsecret", "privatekey",
		"authorization", "proxyauthorization", "cookie", "setcookie", "vertexcredentials",
	} {
		if normalized == exact {
			return true
		}
	}
	return strings.Contains(normalized, "secretaccesskey") ||
		strings.HasSuffix(normalized, "apikey") ||
		strings.HasSuffix(normalized, "token") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.HasSuffix(normalized, "password") ||
		strings.HasSuffix(normalized, "credential") ||
		strings.HasSuffix(normalized, "privatekey")
}

func isOpaqueContainer(key string) bool {
	normalized := normalizeFieldName(key)
	if normalized == "" {
		return false
	}
	for _, exact := range []string{
		"metadata", "headers", "staticheaders", "extraheaders", "credentials",
		"credentialvalues", "credentialinfo", "env", "litellmparams", "modelinfo",
		"params", "config", "info",
	} {
		if normalized == exact {
			return true
		}
	}
	return strings.HasSuffix(normalized, "metadata") || strings.HasSuffix(normalized, "headers")
}

func (s *requestSafety) addSecret(value string) {
	if value == "" {
		return
	}
	if len(s.secrets) >= maxCollectedSecrets {
		s.requestIDUnsafe = true
		return
	}
	for _, existing := range s.secrets {
		if existing == value {
			return
		}
	}
	s.secrets = append(s.secrets, value)
}

func safeRequestID(headers http.Header, safety requestSafety) string {
	if safety.suppressDetail || safety.requestIDUnsafe {
		return ""
	}
	for _, name := range []string{
		"X-Request-ID", "X-LiteLLM-Request-ID", "X-LiteLLM-Call-ID", "LiteLLM-Call-ID", "X-Correlation-ID", "Request-ID",
	} {
		value := strings.TrimSpace(headers.Get(name))
		if value == "" || len(value) > maxRequestIDLength || !safeIdentifierPattern.MatchString(value) {
			continue
		}
		if containsKnownSecret(value, safety.secrets) || looksLikeSecret(value) {
			continue
		}
		return value
	}
	return ""
}

func containsKnownSecret(value string, secrets []string) bool {
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}

func looksLikeSecret(value string) bool {
	return authorizationPattern.MatchString(value) || apiTokenPattern.MatchString(value)
}

func classifyFallbackNotReadyBody(body []byte) bool {
	text := strings.ToLower(string(body))
	// Preserve only the fallback propagation behavior used by existing resource
	// writes. Absence classification never consults response content.
	return strings.Contains(text, "invalid fallback models") || strings.Contains(text, "not found in router")
}

func safeResponseDetail(body []byte, contentType string, safety requestSafety) (string, bool) {
	if safety.suppressDetail || len(bytes.TrimSpace(body)) == 0 {
		return "", true
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.EqualFold(mediaType, "text/plain") {
		return sanitizeDiagnosticString(string(body), safety.secrets), false
	}

	var decoded interface{}
	if err := decodeJSONUseNumber(body, &decoded); err != nil {
		return "", true
	}
	root, ok := decoded.(map[string]interface{})
	if !ok {
		return "", true
	}
	var selected interface{}
	for _, key := range []string{"detail", "error", "message"} {
		if value, exists := root[key]; exists {
			selected = value
			break
		}
	}
	if selected == nil {
		return "", true
	}
	sanitized, ok := sanitizeDiagnosticValue(selected, "", 0, safety.secrets)
	if !ok {
		return "", true
	}
	var rendered string
	if text, ok := sanitized.(string); ok {
		rendered = text
	} else if encoded, err := json.Marshal(sanitized); err == nil {
		rendered = string(encoded)
	}
	// Re-sanitize the rendered representation as defense in depth for JSON map
	// keys and escaping behavior.
	rendered = sanitizeDiagnosticString(rendered, safety.secrets)
	return rendered, rendered == ""
}

func sanitizeDiagnosticValue(value interface{}, key string, depth int, secrets []string) (interface{}, bool) {
	if depth > maxDiagnosticDepth {
		return nil, false
	}
	if isSensitiveField(key) || isOpaqueContainer(key) {
		return "[REDACTED]", true
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(typed))
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			child, ok := sanitizeDiagnosticValue(typed[childKey], childKey, depth+1, secrets)
			if !ok {
				return nil, false
			}
			safeKey := sanitizeDiagnosticString(childKey, secrets)
			if safeKey == "" {
				safeKey = "[REDACTED]"
			}
			if safeKey != childKey {
				child = "[REDACTED]"
			}
			result[safeKey] = child
		}
		return result, true
	case []interface{}:
		result := make([]interface{}, 0, len(typed))
		for _, childValue := range typed {
			child, ok := sanitizeDiagnosticValue(childValue, key, depth+1, secrets)
			if !ok {
				return nil, false
			}
			result = append(result, child)
		}
		return result, true
	case string:
		return sanitizeDiagnosticString(typed, secrets), true
	case json.Number:
		// json.Number is string-backed. Accept only a bounded valid JSON number
		// and preserve its exact lexical value without canonical expansion.
		if len(typed.String()) == 0 || len(typed.String()) > maxDiagnosticNumber || !apiJSONNumberPattern.MatchString(typed.String()) {
			return nil, false
		}
		return typed, true
	case nil, bool, float64:
		return typed, true
	default:
		return nil, false
	}
}

func sanitizeDiagnosticString(value string, secrets []string) string {
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\x1b' || (unicode.IsControl(r) && r != '\t') {
			return ' '
		}
		return r
	}, value)
	// Replace exact known secrets first. Pattern redaction may otherwise consume
	// only a token prefix and prevent the full value (including punctuation and
	// a high-entropy suffix) from matching afterward.
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	value = authorizationPattern.ReplaceAllString(value, "[REDACTED]")
	value = apiTokenPattern.ReplaceAllString(value, "[REDACTED]")
	return truncateUTF8(strings.TrimSpace(value), maxDiagnosticDetail)
}

func truncateUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	for maximum > 0 && !utf8.RuneStart(value[maximum]) {
		maximum--
	}
	return value[:maximum]
}
