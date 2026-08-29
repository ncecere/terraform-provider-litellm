package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestMCPInfoStage2Schema(t *testing.T) {
	var response resource.SchemaResponse
	(&MCPServerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if response.Schema.Version != 7 {
		t.Fatalf("schema version = %d", response.Schema.Version)
	}
	whole, ok := response.Schema.Attributes["mcp_info_json"].(schema.StringAttribute)
	if !ok || !whole.Optional || !whole.Computed || !whole.Sensitive {
		t.Fatalf("mcp_info_json schema = %#v", response.Schema.Attributes["mcp_info_json"])
	}
	overrides, ok := response.Schema.Attributes["mcp_info_overrides_json"].(schema.StringAttribute)
	if !ok || !overrides.Optional || overrides.Computed || !overrides.Sensitive {
		t.Fatalf("mcp_info_overrides_json schema = %#v", response.Schema.Attributes["mcp_info_overrides_json"])
	}
	clears, ok := response.Schema.Attributes["mcp_info_clear_paths"].(schema.ListAttribute)
	if !ok || !clears.Optional || clears.Computed || !clears.Sensitive {
		t.Fatalf("mcp_info_clear_paths schema = %#v", response.Schema.Attributes["mcp_info_clear_paths"])
	}
	generation, ok := response.Schema.Attributes["mcp_info_ownership_generation"].(schema.Int64Attribute)
	if !ok || !generation.Computed || generation.Optional || generation.Sensitive {
		t.Fatalf("mcp_info_ownership_generation schema = %#v", response.Schema.Attributes["mcp_info_ownership_generation"])
	}
	fieldGeneration, ok := response.Schema.Attributes["field_ownership_generation"].(schema.Int64Attribute)
	if !ok || !fieldGeneration.Computed || fieldGeneration.Optional || fieldGeneration.Sensitive {
		t.Fatalf("field_ownership_generation schema = %#v", response.Schema.Attributes["field_ownership_generation"])
	}
	block, ok := response.Schema.Blocks["mcp_info"].(schema.SingleNestedBlock)
	if !ok || len(block.Attributes) != 3 || len(block.Blocks) != 1 {
		t.Fatalf("fixed mcp_info block changed: %#v", response.Schema.Blocks["mcp_info"])
	}
}

func TestMCPInfoV1MapsLosslesslyToV2Pointers(t *testing.T) {
	private := canonicalMCPInfoPrivateMap()
	private[mcpInfoTerraformOwnedPrivateKey] = encodeMCPInfoLeafSet(mcpInfoLeafSet{
		mcpInfoServerNameLeaf: true, mcpInfoDescriptionLeaf: true, mcpInfoLogoURLLeaf: true,
		mcpInfoDefaultCostLeaf: true, mcpInfoToolCostsLeaf: true,
	})
	provenance, diagnostics := readMCPInfoProvenance(context.Background(), private)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	want := mcpInfoPointerSet{}
	for _, pointer := range mcpInfoFixedPointers {
		want[pointer] = true
	}
	if provenance.V2 || provenance.Generation != 0 || provenance.Mode != mcpInfoModeSelective || !mcpInfoPointerSetsEqual(provenance.Fixed, want) {
		t.Fatalf("v1 mapping = %#v", provenance)
	}
}

func TestMCPInfoV2CanonicalCommittedAndPendingBundles(t *testing.T) {
	ctx := context.Background()
	private := mcpInfoMapPrivate{}
	committed := emptyMCPInfoProvenance()
	committed.Generation = 7
	committed.Mode = mcpInfoModeSelective
	committed.Fixed[mcpInfoDescriptionPointer] = true
	committed.Terraform[mcpInfoDescriptionLeaf] = true
	committed.Overrides["/owner/team"] = true
	committed.Clears["/obsolete"] = true
	committed.API[mcpInfoDefaultCostLeaf] = true
	if diagnostics := writeMCPInfoProvenance(ctx, private, committed); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if string(private[mcpInfoOwnershipVersionKey]) != "2" || string(private[mcpInfoGenerationPrivateKey]) != "7" || string(private[mcpInfoModePrivateKey]) != `"selective"` {
		t.Fatalf("noncanonical v2 scalar bundle: %#v", private)
	}
	if got := string(private[mcpInfoFixedOwnedPrivateKey]); got != `["/description"]` {
		t.Fatalf("fixed encoding = %s", got)
	}
	if got := string(private[mcpInfoOverrideOwnedPrivateKey]); got != `["/owner/team"]` {
		t.Fatalf("override encoding = %s", got)
	}
	if got := string(private[mcpInfoClearOwnedPrivateKey]); got != `["/obsolete"]` {
		t.Fatalf("clear encoding = %s", got)
	}
	for _, legacy := range []string{mcpInfoTerraformOwnedPrivateKey, mcpInfoAPIOwnedPrivateKey, mcpInfoPendingTerraformKey, mcpInfoPendingAPIKey} {
		if private[legacy] != nil {
			t.Fatalf("legacy key %q survived v2 commit", legacy)
		}
	}
	pending := cloneMCPInfoProvenance(committed)
	pending.Generation = 8
	pending.Clears = mcpInfoPointerSet{"/retired": true}
	if diagnostics := writePendingMCPInfoProvenance(ctx, private, pending); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	readCommitted, diagnostics := readMCPInfoProvenance(ctx, private)
	if diagnostics.HasError() || readCommitted.Generation != 7 {
		t.Fatalf("committed changed by pending write: %#v %v", readCommitted, diagnostics)
	}
	readPending, diagnostics := readPendingMCPInfoProvenance(ctx, private, readCommitted)
	if diagnostics.HasError() || readPending.Generation != 8 || !readPending.Clears["/retired"] {
		t.Fatalf("pending = %#v %v", readPending, diagnostics)
	}
}

func TestMCPInfoV2RejectsMixedPartialDuplicateAndOverlappingBundles(t *testing.T) {
	ctx := context.Background()
	base := mcpInfoMapPrivate{}
	if diagnostics := writeMCPInfoProvenance(ctx, base, emptyMCPInfoProvenance()); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	cases := map[string]func(mcpInfoMapPrivate){
		"partial":      func(p mcpInfoMapPrivate) { delete(p, mcpInfoModePrivateKey) },
		"duplicate":    func(p mcpInfoMapPrivate) { p[mcpInfoFixedOwnedPrivateKey] = []byte(`["/description","/description"]`) },
		"noncanonical": func(p mcpInfoMapPrivate) { p[mcpInfoFixedOwnedPrivateKey] = []byte(`[ ]`) },
		"unsupported":  func(p mcpInfoMapPrivate) { p[mcpInfoOwnershipVersionKey] = []byte("3") },
		"mixed": func(p mcpInfoMapPrivate) {
			p[mcpInfoTerraformOwnedPrivateKey] = []byte(`[]`)
			p[mcpInfoAPIOwnedPrivateKey] = []byte(`[]`)
		},
		"overlap": func(p mcpInfoMapPrivate) {
			p[mcpInfoModePrivateKey] = []byte(`"selective"`)
			p[mcpInfoOverrideOwnedPrivateKey] = []byte(`["/owner"]`)
			p[mcpInfoClearOwnedPrivateKey] = []byte(`["/owner/team"]`)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			private := cloneMCPInfoPrivateMap(base)
			corrupt(private)
			if _, diagnostics := readMCPInfoProvenance(ctx, private); !diagnostics.HasError() {
				t.Fatal("corrupt v2 bundle accepted")
			}
		})
	}
}

func TestMCPServerStateUpgradersProduceCompleteV2ShapeAndPreserveBlock(t *testing.T) {
	ctx := context.Background()
	block := map[string]interface{}{"server_name": "nested", "description": "kept", "logo_url": nil, "mcp_server_cost_info": map[string]interface{}{"default_cost_per_query": 1.25, "tool_name_to_cost_per_query": map[string]interface{}{"search": 2.5}}}
	for _, version := range []int64{0, 1} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			state := map[string]interface{}{"id": "srv", "server_id": "srv", "server_name": "top", "transport": "http", "mcp_info": block}
			if version == 0 {
				state["extra_headers"] = map[string]string{"z": "ignored", "a": "ignored"}
			} else {
				state["extra_headers"] = []string{"z", "a"}
			}
			raw, _ := json.Marshal(state)
			response := resource.UpgradeStateResponse{}
			(&MCPServerResource{}).UpgradeState(ctx)[version].StateUpgrader(ctx, resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
			if response.Diagnostics.HasError() || response.DynamicValue == nil {
				t.Fatalf("upgrade: %v", response.Diagnostics)
			}
			var got map[string]interface{}
			if err := json.Unmarshal(response.DynamicValue.JSON, &got); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"mcp_info_json", "mcp_info_overrides_json", "mcp_info_clear_paths"} {
				if value, exists := got[name]; !exists || value != nil {
					t.Fatalf("%s = %#v", name, value)
				}
			}
			if got["mcp_info_ownership_generation"] != float64(0) {
				t.Fatalf("generation = %#v", got["mcp_info_ownership_generation"])
			}
			if !reflect.DeepEqual(got["mcp_info"], block) {
				t.Fatalf("fixed block changed: got=%#v want=%#v", got["mcp_info"], block)
			}
			if version == 0 && !reflect.DeepEqual(got["extra_headers"], []interface{}{"a", "z"}) {
				t.Fatalf("v0 headers = %#v", got["extra_headers"])
			}
			if version == 1 && !reflect.DeepEqual(got["extra_headers"], []interface{}{"z", "a"}) {
				t.Fatalf("v1 headers changed = %#v", got["extra_headers"])
			}
		})
	}
}
