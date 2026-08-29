package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

func TestTeamDataSourceAccessGroupIDsAreComputedUnordered(t *testing.T) {
	t.Parallel()

	var response datasource.SchemaResponse
	(&TeamDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes["access_group_ids"].(datasourceschema.SetAttribute)
	if !ok {
		t.Fatalf("access_group_ids schema type = %T, want schema.SetAttribute", response.Schema.Attributes["access_group_ids"])
	}
	if !attribute.Computed || attribute.Optional || attribute.Required {
		t.Fatalf("data source access_group_ids must be Computed-only: %#v", attribute)
	}
}
