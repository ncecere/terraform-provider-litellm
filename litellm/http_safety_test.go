package litellm

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyClientOmitsRequestAndResponseBodies(t *testing.T) {
	secret := "sk-legacy-echo-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"detail":"` + secret + `"}`))
	}))
	defer server.Close()

	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)

	client := NewClient(server.URL, "admin-secret")
	_, err := client.sendRequest(http.MethodPost, "/key/generate?key="+secret, map[string]interface{}{"key": secret})
	if err == nil {
		t.Fatal("expected API error")
	}
	combined := err.Error() + logs.String()
	for _, forbidden := range []string{secret, "admin-secret", server.URL, "/key/generate", `"key"`} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("legacy diagnostics exposed %q: %q", forbidden, combined)
		}
	}
}

func TestLegacyHandlersIgnoreUntrustedReasonPhrases(t *testing.T) {
	secret := "sk-untrusted-reason"
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Status:     "400 " + secret,
		Body:       io.NopCloser(strings.NewReader(`{"detail":"` + secret + `"}`)),
	}
	err := handleMCPAPIResponse(response, nil)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "400") {
		t.Fatalf("unsafe legacy reason-phrase diagnostic: %v", err)
	}
}

func TestLegacyResponseReaderIsBounded(t *testing.T) {
	body, err := readLegacyResponseBodyLimit(strings.NewReader("1234"), 4)
	if err != nil || string(body) != "1234" {
		t.Fatalf("exact limit: body=%q err=%v", body, err)
	}
	body, err = readLegacyResponseBodyLimit(strings.NewReader("12345"), 4)
	if err != errLegacyResponseTooLarge || body != nil {
		t.Fatalf("over limit: body=%q err=%v", body, err)
	}
}
