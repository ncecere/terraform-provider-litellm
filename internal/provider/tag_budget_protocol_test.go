package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestTagSchemaV0CompatibilityContract(t *testing.T) {
	ctx := context.Background()
	var schemaResponse resource.SchemaResponse
	resourceUnderTest := &TagResource{}
	resourceUnderTest.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Schema.Version != 0 {
		t.Fatalf("schema version=%d", schemaResponse.Schema.Version)
	}
	wantKinds := map[string]string{
		"id": "string", "name": "string", "description": "string", "models": "list", "budget_id": "string",
		"max_budget": "float", "soft_budget": "float", "max_parallel_requests": "int", "tpm_limit": "int", "rpm_limit": "int",
		"budget_duration": "string", "model_max_budget": "string",
	}
	if len(schemaResponse.Schema.Attributes) != len(wantKinds) {
		t.Fatalf("attributes=%d want=%d", len(schemaResponse.Schema.Attributes), len(wantKinds))
	}
	for name, kind := range wantKinds {
		attribute, exists := schemaResponse.Schema.Attributes[name]
		if !exists {
			t.Fatalf("missing attribute %q", name)
		}
		got := ""
		switch attribute.(type) {
		case resourceschema.StringAttribute:
			got = "string"
		case resourceschema.ListAttribute:
			got = "list"
			if name == "models" && !attribute.(resourceschema.ListAttribute).ElementType.Equal(types.StringType) {
				t.Fatalf("models element type=%s", attribute.(resourceschema.ListAttribute).ElementType)
			}
		case resourceschema.Float64Attribute:
			got = "float"
		case resourceschema.Int64Attribute:
			got = "int"
		default:
			got = fmt.Sprintf("%T", attribute)
		}
		if got != kind {
			t.Fatalf("attribute %s kind=%s want=%s", name, got, kind)
		}
	}
	var metadata resource.MetadataResponse
	resourceUnderTest.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "litellm"}, &metadata)
	if metadata.TypeName != "litellm_tag" {
		t.Fatalf("resource type=%q", metadata.TypeName)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_tag", ID: "plain:name/%"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import contract: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schemas.ResourceSchemas["litellm_tag"], imported.ImportedResources[0].State)
	var id, name string
	_ = attributes["id"].As(&id)
	_ = attributes["name"].As(&name)
	if id != "plain:name/%" || name != id {
		t.Fatalf("import identity id=%q name=%q", id, name)
	}
}

func TestTagDataSourcesNestedParityAndMalformedResponsesProtocol(t *testing.T) {
	ctx := context.Background()
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		budget := `"budget_id":"budget","tpm_limit":9007199254740993,"model_max_budget":{"z":{"rpm_limit":7,"max_budget":2.5},"a":{"time_period":"1d","budget_limit":1}}`
		if malformed.Load() {
			budget = `"tpm_limit":true`
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprintf(writer, `{"stored":{"name":"stored","description":"tag","models":["b","a"],"litellm_budget_table":{%s}}}`, budget)
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprintf(writer, `[{"name":"stored","description":"tag","models":["b","a"],"litellm_budget_table":{%s}},{"name":"dynamic","description":"historical","models":null}]`, budget)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_tag"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"name": "stored"}))
	listSchema := schemas.DataSourceSchemas["litellm_tags"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	singleRead, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_tag", Config: singleConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(singleRead.Diagnostics) {
		t.Fatalf("single read: err=%v diagnostics=%v", err, singleRead.Diagnostics)
	}
	listRead, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_tags", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(listRead.Diagnostics) {
		t.Fatalf("list read: err=%v diagnostics=%v", err, listRead.Diagnostics)
	}
	singleAttributes := protocolAttributeMap(t, singleSchema, singleRead.State)
	listAttributes := protocolAttributeMap(t, listSchema, listRead.State)
	var elements []tftypes.Value
	if err := listAttributes["tags"].As(&elements); err != nil || len(elements) != 2 {
		t.Fatalf("list tags: err=%v count=%d", err, len(elements))
	}
	items := map[string]map[string]tftypes.Value{}
	orderedNames := make([]string, 0, len(elements))
	for _, element := range elements {
		var item map[string]tftypes.Value
		if err := element.As(&item); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := item["name"].As(&name); err != nil {
			t.Fatal(err)
		}
		items[name] = item
		orderedNames = append(orderedNames, name)
	}
	if orderedNames[0] != "dynamic" || orderedNames[1] != "stored" {
		t.Fatalf("tag ordering=%v", orderedNames)
	}
	stored, dynamic := items["stored"], items["dynamic"]
	for _, field := range []string{"budget_id", "tpm_limit", "model_max_budget", "models"} {
		if stored == nil || !singleAttributes[field].Equal(stored[field]) || stored[field].IsNull() {
			t.Fatalf("single/list %s parity failed: single=%s list=%s", field, singleAttributes[field], stored[field])
		}
	}
	var budgetID string
	if err := stored["budget_id"].As(&budgetID); err != nil || budgetID != "budget" {
		t.Fatalf("nested budget ID=%q err=%v", budgetID, err)
	}
	var tpm big.Float
	if err := stored["tpm_limit"].As(&tpm); err != nil {
		t.Fatalf("exact tpm decode: %v", err)
	}
	tpmInt, accuracy := tpm.Int64()
	if accuracy != big.Exact || tpmInt != 9007199254740993 {
		t.Fatalf("exact tpm=%d accuracy=%v", tpmInt, accuracy)
	}
	var modelJSON string
	if err := stored["model_max_budget"].As(&modelJSON); err != nil || modelJSON != `{"a":{"budget_limit":1,"time_period":"1d"},"z":{"max_budget":2.5,"rpm_limit":7}}` {
		t.Fatalf("model JSON=%q err=%v", modelJSON, err)
	}
	var modelValues []tftypes.Value
	if err := stored["models"].As(&modelValues); err != nil || len(modelValues) != 2 {
		t.Fatalf("stored models: err=%v count=%d", err, len(modelValues))
	}
	var first, second string
	_ = modelValues[0].As(&first)
	_ = modelValues[1].As(&second)
	if first != "a" || second != "b" {
		t.Fatalf("stored model ordering=%q,%q", first, second)
	}
	for _, field := range []string{"budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
		if dynamic == nil || !dynamic[field].IsNull() {
			t.Fatalf("dynamic %s=%s, want null", field, dynamic[field])
		}
	}
	malformed.Store(true)
	for name, test := range map[string]struct {
		typeName string
		config   *tfprotov6.DynamicValue
	}{
		"single": {"litellm_tag", singleConfig}, "list": {"litellm_tags", listConfig},
	} {
		t.Run(name+" malformed", func(t *testing.T) {
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: test.config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed read: err=%v diagnostics=%v", err, read.Diagnostics)
			}
		})
	}
}

func TestTagModelBudgetValidationProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	for name, test := range map[string]struct {
		value     string
		wantError bool
	}{
		"empty":                       {value: `{}`, wantError: true},
		"unknown nested field":        {value: `{"model":{"ignored":1}}`, wantError: true},
		"invalid nested integer":      {value: `{"model":{"tpm_limit":1.5}}`, wantError: true},
		"duplicate budget alias":      {value: `{"model":{"max_budget":2,"budget_limit":3}}`, wantError: true},
		"duplicate duration alias":    {value: `{"model":{"budget_duration":"1d","time_period":"2d"}}`, wantError: true},
		"budget alias only":           {value: `{"model":{"budget_limit":2.5}}`},
		"duration alias only":         {value: `{"model":{"time_period":"1d"}}`},
		"structured":                  {value: `{"model":{"max_budget":2.5,"budget_duration":"1d","tpm_limit":10}}`},
		"legacy scalar compatibility": {value: `{"model":2.5}`},
	} {
		t.Run(name, func(t *testing.T) {
			configValues := map[string]interface{}{"name": "tag", "model_max_budget": test.value}
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
			validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: typeName, Config: config})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) != test.wantError {
				t.Fatalf("validation: err=%v diagnostics=%v", err, validated.Diagnostics)
			}
		})
	}
}

func TestTagLegacyScalarUnchangedAndStructuredMigrationPlanProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "legacy", "name": "legacy", "budget_id": "budget", "model_max_budget": `{"model":2}`}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	for name, configured := range map[string]string{
		"unchanged scalar":     `{"model":2}`,
		"structured migration": `{"model":{"max_budget":2}}`,
	} {
		t.Run(name, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"name": "legacy", "model_max_budget": configured}))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"model_max_budget": configured})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
		})
	}
}

func TestTagChangedLegacyScalarModelBudgetRejectedAtPlanProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "legacy", "name": "legacy", "budget_id": "budget", "model_max_budget": `{"model":2}`}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	configValues := map[string]interface{}{"name": "legacy", "model_max_budget": `{"model":3}`}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"model_max_budget": `{"model":3}`})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("legacy scalar update: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
}

func TestTagPreviousProviderStateDefaultsToUnmanagedBudgetProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{
		"id": "legacy", "name": "legacy", "budget_id": "budget", "max_budget": 42.0, "soft_budget": 11.0,
		"tpm_limit": int64(100), "budget_duration": "30d", "model_max_budget": `{"model":2}`,
	}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"name": "legacy"}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, state, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("legacy omission plan: err=%v diagnostics=%v action=%s", err, planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, state, planned))
	}
	if !protocolPrivateHasKey(t, planned.PlannedPrivate, tagBudgetOwnershipInitializedKey) || !protocolPrivateHasKey(t, planned.PlannedPrivate, tagImportedBudgetOmissionsPrivateKey) {
		t.Fatalf("legacy provenance was not initialized: %s", planned.PlannedPrivate)
	}
	privateValues := map[string]json.RawMessage{}
	if err := json.Unmarshal(planned.PlannedPrivate, &privateValues); err != nil {
		t.Fatal(err)
	}
	var encoded []byte
	if err := json.Unmarshal(privateValues[tagImportedBudgetOmissionsPrivateKey], &encoded); err != nil {
		t.Fatal(err)
	}
	fields, err := decodeTagFieldSet(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"max_budget": true, "soft_budget": true, "tpm_limit": true, "budget_duration": true, "model_max_budget": true}
	if len(fields) != len(want) {
		t.Fatalf("legacy omission fields=%v", sortedTagFieldNames(fields))
	}
	for name := range want {
		if !fields[name] {
			t.Fatalf("legacy omission missing %s: %v", name, sortedTagFieldNames(fields))
		}
	}
}

func TestTagCreateRefusesVisibleExistingNameBeforePostProtocol(t *testing.T) {
	ctx := context.Background()
	var creates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/tag/list" {
			_, _ = fmt.Fprint(writer, `[{"name":"exists","description":"historical","models":null}]`)
			return
		}
		if request.Method == http.MethodPost && request.URL.Path == "/tag/new" {
			creates.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "exists"}
	proposedValues := map[string]interface{}{"name": "exists"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || creates.Load() != 0 {
		t.Fatalf("duplicate create: err=%v diagnostics=%v creates=%d", err, applied.Diagnostics, creates.Load())
	}
}

func TestTagAmbiguousCreateRetainsBlockedUncertainStateProtocol(t *testing.T) {
	ctx := context.Background()
	var created atomic.Bool
	var creates, reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			creates.Add(1)
			created.Store(true)
			http.Error(writer, "post-commit deployment failure", http.StatusInternalServerError)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info" && created.Load():
			reads.Add(1)
			_, _ = fmt.Fprint(writer, `{"recovered":{"name":"recovered","description":"managed","models":[],"litellm_budget_table":{"budget_id":"budget-recovered","max_budget":5}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "recovered", "description": "managed", "max_budget": 5.0}
	proposedValues := map[string]interface{}{"name": "recovered", "description": "managed", "max_budget": 5.0}
	for _, name := range []string{"id", "models", "budget_id", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || creates.Load() != 1 || reads.Load() != 1 || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("uncertain create: err=%v diagnostics=%v creates=%d reads=%d private=%s", err, applied.Diagnostics, creates.Load(), reads.Load(), applied.Private)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	if attributes["id"].IsNull() || attributes["budget_id"].IsNull() || attributes["max_budget"].IsNull() {
		t.Fatalf("uncertain partial state incomplete: %#v", attributes)
	}
	blocked, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: applied.NewState, ProposedNewState: applied.NewState, PriorPrivate: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blocked.Diagnostics) {
		t.Fatalf("uncertain state was mutable: err=%v diagnostics=%v", err, blocked.Diagnostics)
	}
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || reads.Load() != 1 {
		t.Fatalf("uncertain refresh was not blocked: err=%v diagnostics=%v reads=%d", err, refreshed.Diagnostics, reads.Load())
	}
}

func TestTagCreateRejectsConfiguredBudgetAssociationMismatchProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprint(writer, `{"mismatch":{"name":"mismatch","description":null,"models":[],"litellm_budget_table":{"budget_id":"budget-b"}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "mismatch", "budget_id": "budget-a"}
	proposedValues := map[string]interface{}{"name": "mismatch", "budget_id": "budget-a"}
	for _, name := range []string{"id", "models", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("mismatched association: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
}

func TestTagSuccessfulCreateMismatchMarksOwnershipUncertainProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprint(writer, `{"mismatch":{"name":"mismatch","description":"other","models":[]}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{"name": "mismatch", "description": "planned"}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	proposedValues := map[string]interface{}{"name": "mismatch", "description": "planned"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("mismatch: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
}

func TestTagCreateResetFailurePersistsRetryProtocol(t *testing.T) {
	ctx := context.Background()
	var budgetUpdates, infoReads atomic.Int64
	var resetApplied atomic.Bool
	var associated atomic.Value
	associated.Store("budget-retry")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			infoReads.Add(1)
			duration := ""
			if resetApplied.Load() {
				duration = `,"budget_duration":"30d"`
			}
			_, _ = fmt.Fprintf(writer, `{"retry":{"name":"retry","description":null,"models":[],"litellm_budget_table":{"budget_id":%q%s}}}`, associated.Load().(string), duration)
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			if payload["budget_duration"] != "30d" {
				t.Errorf("reset duration payload=%#v", payload)
			}
			if budgetUpdates.Add(1) == 1 {
				http.Error(writer, "transient reset failure", http.StatusInternalServerError)
				return
			}
			resetApplied.Store(true)
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget-retry"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "retry", "budget_duration": "30d"}
	proposedValues := map[string]interface{}{"name": "retry", "budget_duration": "30d"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || !protocolPrivateHasKey(t, created.Private, tagBudgetResetPendingPrivateKey) {
		t.Fatalf("failed create reset: err=%v diagnostics=%v private=%s", err, created.Diagnostics, created.Private)
	}
	privateValues := map[string]json.RawMessage{}
	if err := json.Unmarshal(created.Private, &privateValues); err != nil {
		t.Fatal(err)
	}
	var pendingRaw []byte
	if err := json.Unmarshal(privateValues[tagBudgetResetPendingPrivateKey], &pendingRaw); err != nil {
		t.Fatal(err)
	}
	resetPending, err := decodeTagBudgetResetPending(pendingRaw)
	if err != nil || resetPending.BudgetID != "budget-retry" || resetPending.BudgetDuration != "30d" {
		t.Fatalf("pending reset=%#v err=%v", resetPending, err)
	}
	for name, configured := range map[string]map[string]interface{}{
		"changed duration": {"name": "retry", "budget_duration": "7d"},
		"removed duration": {"name": "retry"},
	} {
		t.Run(name, func(t *testing.T) {
			blockedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configured))
			blockedProposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"budget_duration": configured["budget_duration"]})
			blocked, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: blockedConfig, PriorState: created.NewState, ProposedNewState: blockedProposed, PriorPrivate: created.Private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(blocked.Diagnostics) || budgetUpdates.Load() != 1 {
				t.Fatalf("blocked reset change: err=%v diagnostics=%v updates=%d", err, blocked.Diagnostics, budgetUpdates.Load())
			}
		})
	}
	associated.Store("other-budget")
	unsafeRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: created.NewState, Private: created.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(unsafeRead.Diagnostics) || budgetUpdates.Load() != 1 {
		t.Fatalf("reassociated pending refresh: err=%v diagnostics=%v updates=%d", err, unsafeRead.Diagnostics, budgetUpdates.Load())
	}
	associated.Store("budget-retry")
	retryProposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"budget_duration": "30d"})
	retryPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: created.NewState, ProposedNewState: retryProposed, PriorPrivate: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retryPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, created.NewState, retryPlan) != organizationProjectProtocolActionUpdate || !protocolPrivateHasKey(t, retryPlan.PlannedPrivate, tagBudgetResetPendingPrivateKey) {
		t.Fatalf("retry plan: err=%v diagnostics=%v action=%s private=%s", err, retryPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, created.NewState, retryPlan), retryPlan.PlannedPrivate)
	}
	retried, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: created.NewState, PlannedState: retryPlan.PlannedState, PlannedPrivate: retryPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retried.Diagnostics) || budgetUpdates.Load() != 2 || protocolPrivateHasKey(t, retried.Private, tagBudgetResetPendingPrivateKey) {
		t.Fatalf("retry apply: err=%v diagnostics=%v updates=%d private=%s", err, retried.Diagnostics, budgetUpdates.Load(), retried.Private)
	}
}

func TestTagCreateResetAdoptsDurationOmittedBeforeInitializationProtocol(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			duration := ""
			if reads.Add(1) > 1 {
				duration = `,"budget_duration":"30d"`
			}
			_, _ = fmt.Fprintf(writer, `{"adopt":{"name":"adopt","description":null,"models":[],"litellm_budget_table":{"budget_id":"budget"%s}}}`, duration)
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "adopt", "budget_duration": "30d"}
	proposedValues := map[string]interface{}{"name": "adopt", "budget_duration": "30d"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("apply: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
	var duration string
	if err := protocolAttributeMap(t, schema, applied.NewState)["budget_duration"].As(&duration); err != nil || duration != "30d" {
		t.Fatalf("duration=%q err=%v", duration, err)
	}
}

func TestTagCreateResetVerificationFailureMarksOwnershipUncertainProtocol(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			if reads.Add(1) == 1 {
				_, _ = fmt.Fprint(writer, `{"verify":{"name":"verify","description":null,"models":[],"litellm_budget_table":{"budget_id":"budget","budget_duration":"30d"}}}`)
			} else {
				http.Error(writer, "verification unavailable", http.StatusInternalServerError)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "verify", "budget_duration": "30d"}
	proposedValues := map[string]interface{}{"name": "verify", "budget_duration": "30d"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("verification failure: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
}

func TestTagCreateResetRejectsConcurrentReassociationProtocol(t *testing.T) {
	ctx := context.Background()
	var associated atomic.Value
	associated.Store("budget-a")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/tag/list":
			_, _ = fmt.Fprint(writer, `[]`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprintf(writer, `{"race":{"name":"race","description":null,"models":[],"litellm_budget_table":{"budget_id":%q,"budget_duration":"30d"}}}`, associated.Load().(string))
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			associated.Store("budget-b")
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget-a"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"name": "race", "budget_duration": "30d"}
	proposedValues := map[string]interface{}{"name": "race", "budget_duration": "30d"}
	for _, name := range []string{"id", "models", "budget_id", "max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "model_max_budget"} {
		proposedValues[name] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("reassociation create: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
}

func TestTagNonNullBudgetUpdateUsesVerifiedBudgetIDProtocol(t *testing.T) {
	ctx := context.Background()
	var maxBudget atomic.Int64
	maxBudget.Store(5)
	var tagWrites, budgetWrites atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprintf(writer, `{"managed":{"name":"managed","description":"keep","models":[],"litellm_budget_table":{"budget_id":"budget","max_budget":%d}}}`, maxBudget.Load())
		case request.Method == http.MethodPost && request.URL.Path == "/tag/update":
			tagWrites.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			budgetWrites.Add(1)
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			if payload["budget_id"] != "budget" {
				t.Errorf("budget identity payload=%#v", payload)
			}
			value, ok := payload["max_budget"].(json.Number)
			if !ok || value.String() != "7" {
				t.Errorf("max budget payload=%#v", payload)
			} else {
				maxBudget.Store(7)
			}
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "managed", "name": "managed", "description": "keep", "budget_id": "budget", "max_budget": 5.0}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	configValues := map[string]interface{}{"name": "managed", "description": "keep", "max_budget": 7.0}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"max_budget": 7.0})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || tagWrites.Load() != 0 || budgetWrites.Load() != 1 {
		t.Fatalf("apply: err=%v diagnostics=%v tag=%d budget=%d", err, applied.Diagnostics, tagWrites.Load(), budgetWrites.Load())
	}
}

func TestTagBudgetUpdateCannotRedirectAfterAssociationRaceProtocol(t *testing.T) {
	ctx := context.Background()
	var associated atomic.Value
	associated.Store("budget-a")
	var updatedID atomic.Value
	updatedID.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			_, _ = fmt.Fprintf(writer, `{"managed":{"name":"managed","description":null,"models":[],"litellm_budget_table":{"budget_id":%q,"max_budget":5}}}`, associated.Load().(string))
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			// Simulate reassociation after the provider's verification read but
			// before the mutation reaches LiteLLM.
			associated.Store("budget-b")
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			updatedID.Store(payload["budget_id"].(string))
			_, _ = fmt.Fprintf(writer, `{"budget_id":%q}`, payload["budget_id"])
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "managed", "name": "managed", "budget_id": "budget-a", "max_budget": 5.0}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	configValues := map[string]interface{}{"name": "managed", "max_budget": 7.0}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"max_budget": 7.0})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updatedID.Load().(string) != "budget-a" {
		t.Fatalf("race apply: err=%v diagnostics=%v updated=%q", err, applied.Diagnostics, updatedID.Load().(string))
	}
}

func TestTagExistingUnbudgetedCanAttachDedicatedBudgetProtocol(t *testing.T) {
	ctx := context.Background()
	var attached atomic.Bool
	var writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			if attached.Load() {
				_, _ = fmt.Fprint(writer, `{"attach":{"name":"attach","description":"keep","models":[],"litellm_budget_table":{"budget_id":"dedicated"}}}`)
			} else {
				_, _ = fmt.Fprint(writer, `{"attach":{"name":"attach","description":"keep","models":[]}}`)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/tag/update":
			writes.Add(1)
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			if payload["budget_id"] != "dedicated" || payload["description"] != "keep" {
				t.Errorf("attach payload=%#v", payload)
			}
			attached.Store(true)
			_, _ = fmt.Fprint(writer, `{}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "attach", "name": "attach", "description": "keep"}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	configValues := map[string]interface{}{"name": "attach", "description": "keep", "budget_id": "dedicated"}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"budget_id": "dedicated"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || writes.Load() != 1 {
		t.Fatalf("attach apply: err=%v diagnostics=%v writes=%d", err, applied.Diagnostics, writes.Load())
	}
	if protocolAttributeMap(t, schema, applied.NewState)["budget_id"].IsNull() {
		t.Fatal("attached budget ID was not persisted")
	}
}

func TestTagDedicatedBudgetAttachmentRejectsMismatchedReadbackProtocol(t *testing.T) {
	ctx := context.Background()
	var attached atomic.Bool
	var deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			if attached.Load() {
				_, _ = fmt.Fprint(writer, `{"attach":{"name":"attach","description":null,"models":[],"litellm_budget_table":{"budget_id":"budget-b"}}}`)
			} else {
				_, _ = fmt.Fprint(writer, `{"attach":{"name":"attach","description":null,"models":[]}}`)
			}
		case request.Method == http.MethodPost && request.URL.Path == "/tag/update":
			attached.Store(true)
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPost && request.URL.Path == "/tag/delete":
			deletes.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": "attach", "name": "attach"}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"name": "attach", "budget_id": "budget-a"}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"budget_id": "budget-a"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !attached.Load() || !protocolPrivateHasKey(t, applied.Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("mismatched attach: err=%v diagnostics=%v private=%s", err, applied.Diagnostics, applied.Private)
	}
	blockedRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedRead.Diagnostics) {
		t.Fatalf("blocked read: err=%v diagnostics=%v", err, blockedRead.Diagnostics)
	}
	blockedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: applied.NewState, ProposedNewState: proposed, PriorPrivate: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedPlan.Diagnostics) {
		t.Fatalf("blocked plan: err=%v diagnostics=%v", err, blockedPlan.Diagnostics)
	}
	deleted, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, PriorState: applied.NewState, PlannedState: accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil)), PlannedPrivate: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(deleted.Diagnostics) || deletes.Load() != 0 {
		t.Fatalf("blocked delete: err=%v diagnostics=%v deletes=%d", err, deleted.Diagnostics, deletes.Load())
	}
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "attach"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 || protocolPrivateHasKey(t, imported.ImportedResources[0].Private, tagUncertainCreatePrivateKey) {
		t.Fatalf("import reconciliation: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
}

func TestTagUpdateRejectsInlineBudgetCreationWithoutAssociationProtocol(t *testing.T) {
	ctx := context.Background()
	var writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost && request.URL.Path == "/tag/info" {
			_, _ = fmt.Fprint(writer, `{"race":{"name":"race","description":null,"models":[],"litellm_budget_table":{"budget_id":"shared","max_budget":9}}}`)
			return
		}
		if request.Method == http.MethodPost && (request.URL.Path == "/tag/update" || request.URL.Path == "/budget/update") {
			writes.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "race", "name": "race"}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	configValues := map[string]interface{}{"name": "race", "max_budget": 10.0}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"max_budget": 10.0, "budget_id": tftypes.UnknownValue})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || writes.Load() != 0 {
		t.Fatalf("unsafe inline creation plan: err=%v diagnostics=%v writes=%d", err, planned.Diagnostics, writes.Load())
	}
}

func TestTagDestroyBypassesModelBudgetClearSafetyProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "tag", "name": "tag", "budget_id": "budget", "model_max_budget": `{"model":2}`}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"name": "tag"}))
	fullyNull := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: fullyNull})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Logf("planned attributes: %#v", protocolAttributeMap(t, schema, planned.PlannedState))
		for _, diagnostic := range planned.Diagnostics {
			t.Logf("diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
		t.Fatalf("destroy plan: err=%v action=%s", err, organizationProjectProtocolPlannedAction(t, schema, state, planned))
	}
}

func TestTagImportedBudgetOmissionAndIndependentOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	var maxBudget atomic.Value
	maxBudget.Store("42")
	var budgetWrites atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/tag/info":
			max := maxBudget.Load().(string)
			_, _ = fmt.Fprintf(writer, `{"imported-tag":{"name":"imported-tag","description":null,"models":[],"litellm_budget_table":{"budget_id":"budget-tag","max_budget":%s,"soft_budget":11,"tpm_limit":9007199254740993,"model_max_budget":{"model":{"max_budget":2}}}}}`, max)
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			budgetWrites.Add(1)
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
				http.Error(writer, `{}`, http.StatusBadRequest)
				return
			}
			if payload["budget_id"] != "budget-tag" {
				t.Errorf("budget identity payload=%#v", payload)
			}
			if value, exists := payload["max_budget"]; exists && value == nil {
				maxBudget.Store("null")
			}
			_, _ = fmt.Fprint(writer, `{"budget_id":"budget-tag"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_tag"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "imported-tag"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	if got := attributes["max_budget"]; !got.IsKnown() || got.IsNull() {
		t.Fatalf("import did not adopt nested max_budget: %s", got)
	}
	if got := attributes["tpm_limit"]; !got.IsKnown() || got.IsNull() {
		t.Fatalf("import did not adopt exact nested tpm_limit: %s", got)
	}

	plan := func(configValues map[string]interface{}, prior, proposed *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private})
		if planErr != nil {
			t.Fatal(planErr)
		}
		return response
	}

	omittedConfig := map[string]interface{}{"name": "imported-tag"}
	omitted := plan(omittedConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp || budgetWrites.Load() != 0 {
		t.Fatalf("import omission: diagnostics=%v action=%s writes=%d", omitted.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted), budgetWrites.Load())
	}

	explicitConfig := map[string]interface{}{"name": "imported-tag", "max_budget": 42.0}
	explicitProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"max_budget": 42.0})
	explicit := plan(explicitConfig, read.NewState, explicitProposed, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(explicit.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, explicit) != organizationProjectProtocolActionUpdate {
		t.Fatalf("equal ownership transition: diagnostics=%v action=%s", explicit.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, explicit))
	}
	explicitConfigValue := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, explicitConfig))
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: explicitConfigValue, PriorState: read.NewState, PlannedState: explicit.PlannedState, PlannedPrivate: explicit.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || budgetWrites.Load() != 0 {
		t.Fatalf("ownership apply: err=%v diagnostics=%v writes=%d", err, applied.Diagnostics, budgetWrites.Load())
	}

	removalProposed := organizationProjectProtocolReplace(t, schema, applied.NewState, map[string]interface{}{"max_budget": tftypes.UnknownValue})
	removal := plan(omittedConfig, applied.NewState, removalProposed, applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal) != organizationProjectProtocolActionUpdate {
		t.Fatalf("owned removal: diagnostics=%v action=%s", removal.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal))
	}
	omittedConfigValue := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, omittedConfig))
	cleared, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: omittedConfigValue, PriorState: applied.NewState, PlannedState: removal.PlannedState, PlannedPrivate: removal.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleared.Diagnostics) || budgetWrites.Load() != 1 {
		for _, diagnostic := range cleared.Diagnostics {
			t.Logf("diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
		t.Fatalf("clear apply: err=%v writes=%d", err, budgetWrites.Load())
	}
	clearedAttributes := protocolAttributeMap(t, schema, cleared.NewState)
	if !clearedAttributes["max_budget"].IsNull() || clearedAttributes["soft_budget"].IsNull() {
		t.Fatalf("clear was not field-selective: max=%s soft=%s", clearedAttributes["max_budget"], clearedAttributes["soft_budget"])
	}
	steady := plan(omittedConfig, cleared.NewState, cleared.NewState, cleared.Private)
	if accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, cleared.NewState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady: diagnostics=%v action=%s", steady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, cleared.NewState, steady))
	}
}
