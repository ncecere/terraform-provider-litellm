package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestNumericMapSchemasRejectNullElementsAtPlanTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resources := []struct {
		name       string
		resource   resource.Resource
		attributes []string
	}{
		{"key", &KeyResource{}, []string{"model_max_budget", "model_rpm_limit", "model_tpm_limit"}},
		{"team", &TeamResource{}, []string{"model_rpm_limit", "model_tpm_limit"}},
		{"project", &ProjectResource{}, []string{"model_max_budget", "model_rpm_limit", "model_tpm_limit"}},
		{"organization", &OrganizationResource{}, []string{"model_rpm_limit", "model_tpm_limit"}},
	}
	for _, item := range resources {
		item := item
		t.Run(item.name, func(t *testing.T) {
			var schemaResponse resource.SchemaResponse
			item.resource.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
			if schemaResponse.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", schemaResponse.Diagnostics)
			}
			for _, name := range item.attributes {
				attribute, ok := schemaResponse.Schema.Attributes[name].(resourceschema.MapAttribute)
				if !ok || len(attribute.Validators) == 0 {
					t.Fatalf("%s has no map validator", name)
				}
				var null attr.Value
				switch attribute.ElementType {
				case types.Int64Type:
					null = types.Int64Null()
				case types.Float64Type:
					null = types.Float64Null()
				default:
					t.Fatalf("unexpected element type for %s", name)
				}
				configured := types.MapValueMust(attribute.ElementType, map[string]attr.Value{"bad": null})
				for _, mapValidator := range attribute.Validators {
					var response validator.MapResponse
					mapValidator.ValidateMap(ctx, validator.MapRequest{Path: path.Root(name), ConfigValue: configured}, &response)
					if !response.Diagnostics.HasError() {
						t.Fatalf("%s accepted a null numeric map element", name)
					}
				}
			}
		})
	}
}

func TestNumericMapRequestBuildersRejectNullAndUnknownElements(t *testing.T) {
	t.Parallel()
	nullInt := types.MapValueMust(types.Int64Type, map[string]attr.Value{"bad": types.Int64Null()})
	unknownInt := types.MapValueMust(types.Int64Type, map[string]attr.Value{"bad": types.Int64Unknown()})
	nullFloat := types.MapValueMust(types.Float64Type, map[string]attr.Value{"bad": types.Float64Null()})
	tests := []struct {
		name, field string
		build       func() error
	}{
		{"key null float", "model_max_budget", func() error {
			_, err := (&KeyResource{}).buildKeyRequest(context.Background(), &KeyResourceModel{ModelMaxBudget: nullFloat})
			return err
		}},
		{"key unknown integer", "model_rpm_limit", func() error {
			_, err := (&KeyResource{}).buildKeyRequest(context.Background(), &KeyResourceModel{ModelRPMLimit: unknownInt})
			return err
		}},
		{"team null integer", "model_rpm_limit", func() error {
			_, err := (&TeamResource{}).buildTeamRequest(context.Background(), &TeamResourceModel{TeamAlias: types.StringValue("team"), ModelRPMLimit: nullInt}, "team-1")
			return err
		}},
		{"project null integer", "model_tpm_limit", func() error {
			_, err := (&ProjectResource{}).buildProjectRequest(context.Background(), &ProjectResourceModel{ModelTPMLimit: nullInt})
			return err
		}},
		{"organization null integer", "model_tpm_limit", func() error {
			_, err := (&OrganizationResource{}).buildOrganizationRequest(context.Background(), &OrganizationResourceModel{OrganizationAlias: types.StringValue("org"), ModelTPMLimit: nullInt})
			return err
		}},
		{"MCP null cost", "tool_name_to_cost_per_query", func() error {
			data := MCPServerResourceModel{MCPInfo: &MCPInfoModel{MCPServerCostInfo: &MCPServerCostInfoModel{ToolNameToCostPerQuery: nullFloat}}}
			resolved, err := resolveMCPInfoCreateDocument(context.Background(), data)
			if err != nil {
				return fmt.Errorf("mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query: %w", err)
			}
			_, err = (&MCPServerResource{}).buildMCPServerRequest(context.Background(), &data, resolved.Document, resolved.Present)
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.build()
			if err == nil || !strings.Contains(err.Error(), test.field) || strings.Contains(err.Error(), "<unknown>") {
				t.Fatalf("error = %v, want safe field-specific diagnostic", err)
			}
		})
	}
}
