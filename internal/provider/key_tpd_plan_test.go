package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestKeyTPDLimitChangesPlanAnUpdate guards the key ModifyPlan shortcut that
// returns prior state when no listed field changed: tpd_limit must be listed.
func TestKeyTPDLimitChangesPlanAnUpdate(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	schema := schemas.ResourceSchemas["litellm_key"]
	value := func(withIdentity bool, tpd interface{}) *tfprotov6.DynamicValue {
		overrides := map[string]tftypes.Value{"key_alias": tftypes.NewValue(tftypes.String, "alias"), "tpd_limit": tftypes.NewValue(tftypes.Number, tpd)}
		if withIdentity {
			overrides["id"] = tftypes.NewValue(tftypes.String, hashKeyForID("sk-tpd"))
			overrides["key"] = tftypes.NewValue(tftypes.String, "sk-tpd")
		}
		return keyNumericProtocolDynamic(t, schema, keyNumericProtocolValue(t, schema, false, overrides))
	}
	prior := value(true, 100000)
	for _, test := range []struct {
		name string
		tpd  interface{}
		want organizationProjectProtocolAction
	}{
		{"unchanged", 100000, organizationProjectProtocolActionNoOp},
		{"changed", 250000, organizationProjectProtocolActionUpdate},
		{"removed", nil, organizationProjectProtocolActionUpdate},
	} {
		t.Run(test.name, func(t *testing.T) {
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_key", Config: value(false, test.tpd), PriorState: prior, ProposedNewState: value(true, test.tpd)})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			if action := organizationProjectProtocolPlannedAction(t, schema, prior, planned); action != test.want {
				t.Fatalf("action=%s want %s", action, test.want)
			}
		})
	}
}
