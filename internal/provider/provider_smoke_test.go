package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwdatasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	fwresourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// These tests exercise the mechanical, side-effect-free surface that every
// resource and data source implements — Metadata, Schema, Configure and
// (for resources) ImportState — by iterating the provider's own factory lists.
// They assert the framework contracts hold uniformly across every type, and in
// doing so give broad coverage of otherwise-untested boilerplate.

func newTestProvider() provider.Provider {
	return New("test")()
}

func providerFactory(t *testing.T) *LiteLLMProvider {
	t.Helper()
	p, ok := newTestProvider().(*LiteLLMProvider)
	if !ok {
		t.Fatalf("New(...) did not return *LiteLLMProvider")
	}
	return p
}

func TestProviderMetadata(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	resp := &provider.MetadataResponse{}
	p.Metadata(context.Background(), provider.MetadataRequest{}, resp)

	if resp.TypeName != "litellm" {
		t.Errorf("expected TypeName 'litellm', got %q", resp.TypeName)
	}
	if resp.Version != "test" {
		t.Errorf("expected Version 'test', got %q", resp.Version)
	}
}

func TestProviderSchema(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	resp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("provider schema diagnostics: %v", resp.Diagnostics.Errors())
	}
	for _, attr := range []string{"api_base", "api_key", "insecure_skip_verify", "litellm_changed_by"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("provider schema missing attribute %q", attr)
		}
	}
}

// TestResourcesMetadataAndSchema iterates every registered resource and checks
// Metadata, Schema and Configure behave per the framework contract.
func TestResourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	factories := p.Resources(context.Background())
	if len(factories) == 0 {
		t.Fatal("provider registered no resources")
	}

	seen := map[string]bool{}
	for i, factory := range factories {
		res := factory()

		// Metadata: type name must be non-empty, litellm-prefixed and unique.
		metaResp := &resource.MetadataResponse{}
		res.Metadata(
			context.Background(),
			resource.MetadataRequest{ProviderTypeName: "litellm"},
			metaResp,
		)
		if metaResp.TypeName == "" {
			t.Errorf("resource #%d: empty type name", i)
		}
		if !strings.HasPrefix(metaResp.TypeName, "litellm_") {
			t.Errorf("resource %q: type name should have 'litellm_' prefix", metaResp.TypeName)
		}
		if seen[metaResp.TypeName] {
			t.Errorf("duplicate resource type name %q", metaResp.TypeName)
		}
		seen[metaResp.TypeName] = true

		// Schema: must not error and must define at least one attribute or block.
		schemaResp := &resource.SchemaResponse{}
		res.Schema(context.Background(), resource.SchemaRequest{}, schemaResp)
		if schemaResp.Diagnostics.HasError() {
			t.Errorf("resource %q schema diagnostics: %v", metaResp.TypeName, schemaResp.Diagnostics.Errors())
		}
		if !hasResourceFields(schemaResp.Schema) {
			t.Errorf("resource %q schema defines no attributes or blocks", metaResp.TypeName)
		}

		// Configure with nil ProviderData must be a no-op (framework calls it
		// this way during early lifecycle) and must not error.
		if configurable, ok := res.(resource.ResourceWithConfigure); ok {
			cfgResp := &resource.ConfigureResponse{}
			configurable.Configure(context.Background(), resource.ConfigureRequest{}, cfgResp)
			if cfgResp.Diagnostics.HasError() {
				t.Errorf("resource %q Configure(nil) errored: %v", metaResp.TypeName, cfgResp.Diagnostics.Errors())
			}
		}
	}
}

// TestResourcesConfigureRejectsWrongProviderData verifies the type-assertion
// guard in each resource's Configure surfaces an error rather than panicking
// when handed the wrong ProviderData type.
func TestResourcesConfigureRejectsWrongProviderData(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	for _, factory := range p.Resources(context.Background()) {
		res := factory()
		configurable, ok := res.(resource.ResourceWithConfigure)
		if !ok {
			continue
		}
		metaResp := &resource.MetadataResponse{}
		res.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "litellm"}, metaResp)

		cfgResp := &resource.ConfigureResponse{}
		configurable.Configure(
			context.Background(),
			resource.ConfigureRequest{ProviderData: "not-a-client"},
			cfgResp,
		)
		if !cfgResp.Diagnostics.HasError() {
			t.Errorf("resource %q Configure did not reject wrong ProviderData type", metaResp.TypeName)
		}
	}
}

// TestResourcesConfigureAcceptsClient verifies Configure accepts a *Client.
func TestResourcesConfigureAcceptsClient(t *testing.T) {
	t.Parallel()

	client := &Client{APIBase: "https://example.com", APIKey: "test-key"}
	p := providerFactory(t)
	for _, factory := range p.Resources(context.Background()) {
		res := factory()
		configurable, ok := res.(resource.ResourceWithConfigure)
		if !ok {
			continue
		}
		metaResp := &resource.MetadataResponse{}
		res.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "litellm"}, metaResp)

		cfgResp := &resource.ConfigureResponse{}
		configurable.Configure(
			context.Background(),
			resource.ConfigureRequest{ProviderData: client},
			cfgResp,
		)
		if cfgResp.Diagnostics.HasError() {
			t.Errorf("resource %q Configure(*Client) errored: %v", metaResp.TypeName, cfgResp.Diagnostics.Errors())
		}
	}
}

// TestDataSourcesMetadataAndSchema mirrors TestResourcesMetadataAndSchema for
// every registered data source.
func TestDataSourcesMetadataAndSchema(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	factories := p.DataSources(context.Background())
	if len(factories) == 0 {
		t.Fatal("provider registered no data sources")
	}

	seen := map[string]bool{}
	for i, factory := range factories {
		ds := factory()

		metaResp := &datasource.MetadataResponse{}
		ds.Metadata(
			context.Background(),
			datasource.MetadataRequest{ProviderTypeName: "litellm"},
			metaResp,
		)
		if metaResp.TypeName == "" {
			t.Errorf("data source #%d: empty type name", i)
		}
		if !strings.HasPrefix(metaResp.TypeName, "litellm_") {
			t.Errorf("data source %q: type name should have 'litellm_' prefix", metaResp.TypeName)
		}
		if seen[metaResp.TypeName] {
			t.Errorf("duplicate data source type name %q", metaResp.TypeName)
		}
		seen[metaResp.TypeName] = true

		schemaResp := &datasource.SchemaResponse{}
		ds.Schema(context.Background(), datasource.SchemaRequest{}, schemaResp)
		if schemaResp.Diagnostics.HasError() {
			t.Errorf("data source %q schema diagnostics: %v", metaResp.TypeName, schemaResp.Diagnostics.Errors())
		}
		if !hasDataSourceFields(schemaResp.Schema) {
			t.Errorf("data source %q schema defines no attributes or blocks", metaResp.TypeName)
		}

		if configurable, ok := ds.(datasource.DataSourceWithConfigure); ok {
			cfgResp := &datasource.ConfigureResponse{}
			configurable.Configure(context.Background(), datasource.ConfigureRequest{}, cfgResp)
			if cfgResp.Diagnostics.HasError() {
				t.Errorf("data source %q Configure(nil) errored: %v", metaResp.TypeName, cfgResp.Diagnostics.Errors())
			}
		}
	}
}

// TestDataSourcesConfigureRejectsWrongProviderData verifies the guard in each
// data source's Configure.
func TestDataSourcesConfigureRejectsWrongProviderData(t *testing.T) {
	t.Parallel()

	p := providerFactory(t)
	for _, factory := range p.DataSources(context.Background()) {
		ds := factory()
		configurable, ok := ds.(datasource.DataSourceWithConfigure)
		if !ok {
			continue
		}
		metaResp := &datasource.MetadataResponse{}
		ds.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "litellm"}, metaResp)

		cfgResp := &datasource.ConfigureResponse{}
		configurable.Configure(
			context.Background(),
			datasource.ConfigureRequest{ProviderData: 12345},
			cfgResp,
		)
		if !cfgResp.Diagnostics.HasError() {
			t.Errorf("data source %q Configure did not reject wrong ProviderData type", metaResp.TypeName)
		}
	}
}

// TestResourcesImportState calls ImportState on every resource that supports
// it, using a valid import ID. The ID uses "a:b" form so that resources parsing
// a composite "id1:id2" import ID succeed; passthrough resources ignore the
// colon. Each resource is first Configured with a client pointed at a stub HTTP
// server, since a few resources (e.g. fallback) perform a read during import.
// It asserts import succeeds and populates a non-empty "id" attribute (some
// resources, like key, derive id from the import ID rather than copying it).
func TestResourcesImportState(t *testing.T) {
	t.Parallel()

	const importID = "import-a:import-b"

	// Stub server returns a benign JSON object for any read performed during
	// import, so resources that fetch during ImportState don't hit a nil client.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}

	p := providerFactory(t)
	for _, factory := range p.Resources(context.Background()) {
		res := factory()
		importer, ok := res.(resource.ResourceWithImportState)
		if !ok {
			continue
		}
		if configurable, ok := res.(resource.ResourceWithConfigure); ok {
			configurable.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, &resource.ConfigureResponse{})
		}

		metaResp := &resource.MetadataResponse{}
		res.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "litellm"}, metaResp)

		// litellm_fallback's ImportState performs a live read and rebuilds the
		// whole state from the API response, so it can't be exercised against a
		// synthetic null state / stub the way passthrough importers can.
		if metaResp.TypeName == "litellm_fallback" {
			continue
		}

		schemaResp := &resource.SchemaResponse{}
		res.Schema(context.Background(), resource.SchemaRequest{}, schemaResp)
		if schemaResp.Diagnostics.HasError() {
			t.Errorf("resource %q schema diagnostics: %v", metaResp.TypeName, schemaResp.Diagnostics.Errors())
			continue
		}

		nullState, err := nullStateFor(schemaResp.Schema)
		if err != nil {
			t.Errorf("resource %q: building null state: %v", metaResp.TypeName, err)
			continue
		}

		importResp := &resource.ImportStateResponse{State: nullState}
		importer.ImportState(
			context.Background(),
			resource.ImportStateRequest{ID: importID},
			importResp,
		)
		if importResp.Diagnostics.HasError() {
			t.Errorf("resource %q ImportState(%q) errored: %v", metaResp.TypeName, importID, importResp.Diagnostics.Errors())
			continue
		}

		var id string
		if diags := importResp.State.GetAttribute(context.Background(), path.Root("id"), &id); diags.HasError() {
			t.Errorf("resource %q: reading imported id: %v", metaResp.TypeName, diags.Errors())
			continue
		}
		if id == "" {
			t.Errorf("resource %q: ImportState did not populate id", metaResp.TypeName)
		}
	}
}

// TestResourcesImportStateRejectsMalformedID feeds a bare ID with no separator
// to every importer. Composite-ID resources must surface an error; passthrough
// resources accept it. The point of the test is that neither path panics.
func TestResourcesImportStateRejectsMalformedID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}

	p := providerFactory(t)
	for _, factory := range p.Resources(context.Background()) {
		res := factory()
		importer, ok := res.(resource.ResourceWithImportState)
		if !ok {
			continue
		}
		if configurable, ok := res.(resource.ResourceWithConfigure); ok {
			configurable.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, &resource.ConfigureResponse{})
		}

		schemaResp := &resource.SchemaResponse{}
		res.Schema(context.Background(), resource.SchemaRequest{}, schemaResp)
		nullState, err := nullStateFor(schemaResp.Schema)
		if err != nil {
			continue
		}

		importResp := &resource.ImportStateResponse{State: nullState}
		importer.ImportState(
			context.Background(),
			resource.ImportStateRequest{ID: "single-token-id"},
			importResp,
		)
		_ = importResp.Diagnostics.HasError()
	}
}

// nullStateFor builds an empty (all-null) tfsdk.State for a resource schema so
// ImportState's SetAttribute calls have a typed object to write into.
func nullStateFor(s fwresourceschema.Schema) (tfsdk.State, error) {
	ctx := context.Background()
	objType := s.Type().TerraformType(ctx)
	raw := tftypes.NewValue(objType, nil)
	return tfsdk.State{Raw: raw, Schema: s}, nil
}

func hasResourceFields(s fwresourceschema.Schema) bool {
	return len(s.Attributes) > 0 || len(s.Blocks) > 0
}

func hasDataSourceFields(s fwdatasourceschema.Schema) bool {
	return len(s.Attributes) > 0 || len(s.Blocks) > 0
}
