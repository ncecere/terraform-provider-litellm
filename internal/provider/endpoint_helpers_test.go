package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestEndpointPathBuildersPreserveGoURLSemanticsOnceOnly(t *testing.T) {
	values := []string{"/", ":", "%", "?", "#", "雪", "", ".", "..", "%2F"}
	for _, mode := range []endpointPathMode{ordinaryPathMode, capturePathMode} {
		for _, apiPrefix := range []string{"", "/api", "/nested/base"} {
			for _, value := range values {
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

func TestEndpointWithQueryCanonicalRawRoundTrip(t *testing.T) {
	values := []string{"/", ":", "%", "?", "#", "雪", "", ".", "..", "%2F"}
	for _, value := range values {
		t.Run(fmt.Sprintf("value=%q", value), func(t *testing.T) {
			query := url.Values{
				"identity": []string{value},
				"repeat":   []string{"z", value},
			}
			endpoint := endpointWithQuery(endpointWithPathSegment("/things/", value, ""), query)
			request, err := http.NewRequest(http.MethodGet, "https://example.invalid/api"+endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			if request.URL.RequestURI() != "/api"+endpoint {
				t.Errorf("RequestURI = %q, want %q", request.URL.RequestURI(), "/api"+endpoint)
			}
			if request.URL.EscapedPath() != "/api/things/"+expectedEscapedSegment(value) {
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
	identity := "raw/%?#雪%2F"
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

func TestEndpointBuilderDiagnosticsExcludeRawAndEncodedValues(t *testing.T) {
	identity := "private/%?#雪%2F"
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

func TestEndpointBuilderCaptureProxyRequestURI(t *testing.T) {
	values := []string{"slash/name", "%", "?", "#", "雪", ".", "..", "%2F"}
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
