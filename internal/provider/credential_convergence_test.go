package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// shortenCredentialCreateConvergence keeps the create-convergence wait short in
// one test; the production window spans LiteLLM's 30-second credential reload
// interval. Callers must not run in parallel.
func shortenCredentialCreateConvergence(t *testing.T) {
	t.Helper()
	window, initial, maximum := credentialCreateConvergenceWindow, credentialCreateConvergenceInitialDelay, credentialCreateConvergenceMaxDelay
	credentialCreateConvergenceWindow = 300 * time.Millisecond
	credentialCreateConvergenceInitialDelay = 5 * time.Millisecond
	credentialCreateConvergenceMaxDelay = 20 * time.Millisecond
	t.Cleanup(func() {
		credentialCreateConvergenceWindow, credentialCreateConvergenceInitialDelay, credentialCreateConvergenceMaxDelay = window, initial, maximum
	})
}

// Issue #298: on a multi-replica proxy only the worker that handled the create
// has the credential in its process-local cache until the others reload, so
// the first fresh-connection probes can all miss it. Create must keep sampling
// within the bounded window instead of retaining uncertain-ownership state.
func TestCredentialCreateWaitsForWorkerConvergence(t *testing.T) {
	shortenCredentialCreateConvergence(t)
	var mu sync.Mutex
	posts, gets := 0, 0
	var stored []byte
	staleGetsAfterCreate := 3 * credentialProbeSampleSize
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts++
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			values, _ := body["credential_values"].(map[string]interface{})
			stored, _ = json.Marshal(map[string]interface{}{"credential_name": body["credential_name"], "credential_info": body["credential_info"], "credential_values": maskCredentialWorkerValues(values)})
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential created successfully"}`))
			return
		}
		gets++
		if posts == 0 || staleGetsAfterCreate > 0 {
			if posts > 0 {
				staleGetsAfterCreate--
			}
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write(stored)
	}))
	defer server.Close()
	schema := credentialTestSchema(t)
	model := credentialTestModel("converging", nil, map[string]string{"api_key": "secret"})
	model.ID = types.StringUnknown()
	plan := credentialTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if response.Diagnostics.HasError() || posts != 1 {
		t.Fatalf("converging create diagnostics=%v posts=%d gets=%d", response.Diagnostics, posts, gets)
	}
}
