package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestSearchToolDataSourceLocalEndpointFailureOmitsIdentityAndRequestDetails(t *testing.T) {
	identity := "private/search-tool?token=%2F"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	ctx := context.Background()
	dataSource := &SearchToolDataSource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	var schemaResponse datasource.SchemaResponse
	dataSource.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("search tool data source schema: %v", schemaResponse.Diagnostics)
	}
	raw, err := tftypes.ValueFromJSON([]byte(fmt.Sprintf(`{
		"id":null,
		"search_tool_id":%q,
		"search_tool_name":null,
		"search_provider":null,
		"api_base":null,
		"timeout":null,
		"max_retries":null,
		"search_tool_info":null
	}`, identity)), schemaResponse.Schema.Type().TerraformType(ctx))
	if err != nil {
		t.Fatalf("build search tool data source config: %v", err)
	}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
	dataSource.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}}, response)
	if !response.Diagnostics.HasError() || requests != 0 {
		t.Fatalf("local identity failure: diagnostics=%v requests=%d", response.Diagnostics, requests)
	}

	rendered := fmt.Sprint(response.Diagnostics)
	endpoint := endpointWithPathSegment("/search_tools/", identity, "")
	for _, forbidden := range []string{identity, url.PathEscape(identity), url.QueryEscape(identity), endpoint, server.URL} {
		if forbidden != "" && strings.Contains(rendered, forbidden) {
			t.Fatalf("diagnostic exposed identity, endpoint, or URL content %q: %q", forbidden, rendered)
		}
	}
}
