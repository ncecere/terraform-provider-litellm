package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestIsNotFoundError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not found phrase", errors.New("resource not found"), true},
		{"404 status", errors.New("request failed with status 404"), true},
		{"does not exist", errors.New("the key does not exist"), true},
		{"unrelated", errors.New("internal server error"), false},
	}
	for _, tc := range cases {
		if got := IsNotFoundError(tc.err); got != tc.want {
			t.Errorf("%s: IsNotFoundError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestNormalizeAdditionalParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Null / unknown pass through unchanged.
	if got := normalizeAdditionalParams(ctx, types.MapNull(types.StringType)); !got.IsNull() {
		t.Errorf("null map should stay null, got %v", got)
	}
	if got := normalizeAdditionalParams(ctx, types.MapUnknown(types.StringType)); !got.IsUnknown() {
		t.Errorf("unknown map should stay unknown, got %v", got)
	}

	in, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"temperature": types.StringValue("1.50"),
		"max_tokens":  types.StringValue("500"),
		"model":       types.StringValue("gpt-4o"),
	})
	out := normalizeAdditionalParams(ctx, in)

	var got map[string]string
	if diags := out.ElementsAs(ctx, &got, false); diags.HasError() {
		t.Fatalf("decoding result: %v", diags)
	}
	if got["temperature"] != "1.5" {
		t.Errorf("temperature = %q, want 1.5", got["temperature"])
	}
	if got["max_tokens"] != "500" {
		t.Errorf("max_tokens = %q, want 500", got["max_tokens"])
	}
	if got["model"] != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", got["model"])
	}
}

func TestMemberObjectType(t *testing.T) {
	t.Parallel()

	ot := MemberObjectType()
	for _, attrName := range []string{"user_id", "user_email", "role"} {
		if _, ok := ot.AttrTypes[attrName]; !ok {
			t.Errorf("MemberObjectType missing attribute %q", attrName)
		}
	}
	if len(ot.AttrTypes) != 3 {
		t.Errorf("MemberObjectType has %d attrs, want 3", len(ot.AttrTypes))
	}
}
