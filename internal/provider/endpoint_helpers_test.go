package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

type endpointPathMode string

const (
	ordinaryPathMode endpointPathMode = "ordinary"
	capturePathMode  endpointPathMode = "capture"
)

func buildTestPath(mode endpointPathMode, prefix, value, suffix string) string {
	switch mode {
	case ordinaryPathMode:
		return endpointWithPathSegment(prefix, value, suffix)
	case capturePathMode:
		return endpointWithPathCapture(prefix, value, suffix)
	default:
		panic("unknown test path mode")
	}
}

func expectedEscapedSegment(value string) string {
	escaped := url.PathEscape(value)
	return hardenDotSegment(escaped)
}

func TestEndpointPathBuildersPreserveRepresentableGoURLSemanticsOnceOnly(t *testing.T) {
	valuesByMode := map[endpointPathMode][]string{
		ordinaryPathMode: {":", "%", "?", "#", "雪", "", ".", "..", "%2F"},
		capturePathMode:  {"slash/name", ":", "%", "?", "#", "雪", "", "%2F"},
	}
	for _, mode := range []endpointPathMode{ordinaryPathMode, capturePathMode} {
		for _, apiPrefix := range []string{"", "/api", "/nested/base"} {
			for _, value := range valuesByMode[mode] {
				name := fmt.Sprintf("%s/prefix=%q/value=%q", mode, apiPrefix, value)
				t.Run(name, func(t *testing.T) {
					endpoint := buildTestPath(mode, "/things/", value, "/info")
					wantEndpoint := "/things/" + expectedEscapedSegment(value) + "/info"
					if endpoint != wantEndpoint {
						t.Fatalf("endpoint = %q, want %q", endpoint, wantEndpoint)
					}

					client := &Client{APIBase: "https://example.invalid" + apiPrefix, APIKey: "admin"}
					request, _, err := client.prepareRequest(context.Background(), http.MethodGet, endpoint, nil)
					if err != nil {
						t.Fatal(err)
					}
					wantEscapedPath := apiPrefix + wantEndpoint
					wantPath := apiPrefix + "/things/" + value + "/info"
					if request.URL.Path != wantPath {
						t.Errorf("URL.Path = %q, want %q", request.URL.Path, wantPath)
					}
					if request.URL.EscapedPath() != wantEscapedPath {
						t.Errorf("EscapedPath = %q, want %q", request.URL.EscapedPath(), wantEscapedPath)
					}
					if request.URL.RequestURI() != wantEscapedPath {
						t.Errorf("RequestURI = %q, want %q", request.URL.RequestURI(), wantEscapedPath)
					}
					if request.URL.RawQuery != "" || request.URL.Fragment != "" {
						t.Errorf("identity changed URL components: query=%q fragment=%q", request.URL.RawQuery, request.URL.Fragment)
					}
					decoded, err := url.PathUnescape(strings.TrimSuffix(strings.TrimPrefix(request.URL.EscapedPath(), apiPrefix+"/things/"), "/info"))
					if err != nil || decoded != value {
						t.Errorf("escaped segment round trip = %q, %v; want %q", decoded, err, value)
					}
				})
			}
		}
	}
}

func TestOrdinaryPathSlashFailsLocallyWithoutIdentityDiagnostics(t *testing.T) {
	identity := "private/ordinary?token=%2F"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	endpoint := endpointWithQuery(endpointWithPathSegment("/things/", identity, ""), url.Values{"scope": []string{"safe"}})
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil)
	if err == nil || requests != 0 {
		t.Fatalf("ordinary slash result: err=%v requests=%d", err, requests)
	}
	message := err.Error()
	for _, forbidden := range []string{identity, url.PathEscape(identity), url.QueryEscape(identity)} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("local diagnostic exposed identity content %q: %q", forbidden, message)
		}
	}
	if !strings.Contains(message, "cannot be represented safely") {
		t.Fatalf("local diagnostic is not actionable: %q", message)
	}
}

func TestTrimmedInvalidReviewedEndpointNeverDispatches(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	endpoint := endpointWithPathSegment("/things/", "private/slash-id", "")
	endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil)
	classification := ClassifyHTTPFailure(err)
	if err == nil || requests != 0 || classification.RequestDispatched {
		t.Fatalf("trimmed sentinel dispatched: endpoint=%q requests=%d classification=%#v error=%v", endpoint, requests, classification, err)
	}
}

func TestEndpointWithQueryCanonicalRawRoundTrip(t *testing.T) {
	values := []string{"/", ":", "%", "?", "#", "雪", "", ".", "..", "%2F"}
	for _, value := range values {
		t.Run(fmt.Sprintf("value=%q", value), func(t *testing.T) {
			query := url.Values{
				"identity": []string{value},
				"repeat":   []string{"z", value},
			}
			endpoint := endpointWithQuery(endpointWithPathSegment("/things/", "representable", ""), query)
			request, err := http.NewRequest(http.MethodGet, "https://example.invalid/api"+endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			if request.URL.RequestURI() != "/api"+endpoint {
				t.Errorf("RequestURI = %q, want %q", request.URL.RequestURI(), "/api"+endpoint)
			}
			if request.URL.EscapedPath() != "/api/things/representable" {
				t.Errorf("EscapedPath = %q", request.URL.EscapedPath())
			}
			if request.URL.Query().Get("identity") != value {
				t.Errorf("identity query = %q, want raw value", request.URL.Query().Get("identity"))
			}
			if got := request.URL.Query()["repeat"]; len(got) != 2 || got[0] != "z" || got[1] != value {
				t.Errorf("repeated query round trip = %#v", got)
			}
			if strings.Contains(request.URL.RawQuery, "#") {
				t.Errorf("fragment delimiter leaked into RawQuery: %q", request.URL.RawQuery)
			}
		})
	}
}

func TestEndpointWithQueryRejectsExistingQueryAndFragmentWithoutContent(t *testing.T) {
	for _, path := range []string{"/things?private=value", "/things#private-value"} {
		t.Run(path[:7], func(t *testing.T) {
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_ = endpointWithQuery(path, url.Values{"identity": []string{"private-value"}})
			}()
			message, ok := recovered.(string)
			if !ok || message != "endpoint path must not contain a query or fragment" {
				t.Fatalf("panic = %#v", recovered)
			}
			if strings.Contains(message, "private") || strings.Contains(message, url.QueryEscape("private-value")) {
				t.Fatalf("panic exposed endpoint content: %q", message)
			}
		})
	}
}

func TestEndpointBuilderSafeReadRetriesUseIdenticalURI(t *testing.T) {
	identity := "raw-%?#雪%2F"
	endpoint := endpointWithQuery(
		endpointWithPathSegment("/guardrails/", identity, "/info"),
		url.Values{"scope": []string{identity}},
	)
	var uris []string
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		uris = append(uris, request.RequestURI)
		attempt++
		writer.Header().Set("Content-Type", "application/json")
		if attempt < 3 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"detail":"retry"}`))
			return
		}
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL + "/api", APIKey: "admin", HTTPClient: server.Client()}
	policy := testReadPolicy(3)
	if err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, endpoint, nil, nil, policy, noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if len(uris) != 3 {
		t.Fatalf("attempt URIs = %v", uris)
	}
	for _, uri := range uris {
		if uri != "/api"+endpoint {
			t.Fatalf("retry URI = %q, want %q", uri, "/api"+endpoint)
		}
	}
}

func TestInvalidReviewedEndpointDerivativesNeverDispatch(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		http.Error(writer, "must not dispatch", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	const secret = "sentinel-derived-private-content"

	if got := endpointWithQuery(invalidReviewedEndpoint+"/versions", url.Values{"scope": []string{secret}}); got != invalidReviewedEndpoint {
		t.Fatalf("query wrapper did not preserve invalid endpoint: %q", got)
	}
	for _, endpoint := range []string{
		invalidReviewedEndpoint,
		invalidReviewedEndpoint + "?scope=" + secret,
		invalidReviewedEndpoint + "/versions?scope=" + secret,
		invalidReviewedEndpoint + "#" + secret,
		invalidReviewedEndpoint + "-" + secret,
	} {
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil)
		classification := ClassifyHTTPFailure(err)
		if err == nil || requests != 0 || classification.RequestDispatched || classification.ResponseAccepted {
			t.Fatalf("sentinel derivative dispatched: requests=%d classification=%#v error=%v", requests, classification, err)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), endpoint) {
			t.Fatal("local sentinel diagnostic exposed endpoint content")
		}
	}
}

func TestEndpointBuilderDiagnosticsExcludeRawAndEncodedValues(t *testing.T) {
	identity := "private-%?#雪%2F"
	endpoint := endpointWithQuery(
		endpointWithPathSegment("/guardrails/", identity, "/info"),
		url.Values{"scope": []string{identity}},
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(fmt.Sprintf(`{"detail":%q}`, identity+" "+request.RequestURI)))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil)
	if err == nil {
		t.Fatal("expected API error")
	}
	rendered := err.Error()
	for _, forbidden := range []string{identity, url.PathEscape(identity), url.QueryEscape(identity), endpoint, server.URL} {
		if forbidden != "" && strings.Contains(rendered, forbidden) {
			t.Fatalf("diagnostic exposed identity or URL content %q: %q", forbidden, rendered)
		}
	}
}

func TestCapturePathProxyRequestURIAndDotComponentFailClosed(t *testing.T) {
	var backendURI string
	backendRequests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		backendRequests++
		backendURI = request.RequestURI
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{}`))
	}))
	defer backend.Close()
	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(backendURL))
	defer proxy.Close()
	client := &Client{APIBase: proxy.URL, APIKey: "admin", HTTPClient: proxy.Client()}

	safeIdentity := "a/team/name"
	safeEndpoint := endpointWithPathCapture("/credentials/by_name/", safeIdentity, "")
	if err := client.DoRequestWithResponse(context.Background(), http.MethodGet, safeEndpoint, nil, nil); err != nil {
		t.Fatal(err)
	}
	if backendURI != "/credentials/by_name/a%2Fteam%2Fname" {
		t.Fatalf("proxy backend RequestURI = %q", backendURI)
	}

	for _, identity := range []string{".", "..", "a/./b", "a/../b", "./a", "a/.."} {
		before := backendRequests
		endpoint := endpointWithPathCapture("/credentials/by_name/", identity, "")
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil)
		if err == nil || backendRequests != before {
			t.Fatalf("dot identity %q was dispatched: err=%v requests=%d", identity, err, backendRequests)
		}
		if strings.Contains(err.Error(), identity) || strings.Contains(err.Error(), url.PathEscape(identity)) {
			t.Fatalf("dot identity diagnostic exposed content: %q", err)
		}
	}
}

func TestEndpointBuilderCaptureRequestURI(t *testing.T) {
	values := []string{"slash/name", "%", "?", "#", "雪", "%2F"}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			var capturedURI string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				capturedURI = request.RequestURI
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{}`))
			}))
			defer server.Close()

			query := url.Values{"identity": []string{value}}
			endpoint := endpointWithQuery(endpointWithPathCapture("/credentials/by_name/", value, ""), query)
			client := &Client{APIBase: server.URL + "/api", APIKey: "admin", HTTPClient: server.Client()}
			if err := client.DoRequestWithResponse(context.Background(), http.MethodGet, endpoint, nil, nil); err != nil {
				t.Fatal(err)
			}
			if capturedURI != "/api"+endpoint {
				t.Fatalf("captured RequestURI = %q, want %q", capturedURI, "/api"+endpoint)
			}
		})
	}
}
