package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type cancelOnErrCallContext struct {
	context.Context
	calls    int
	cancelOn int
}

func (ctx *cancelOnErrCallContext) Err() error {
	ctx.calls++
	if ctx.calls >= ctx.cancelOn {
		return context.Canceled
	}
	return nil
}

func mustParseSemanticDictionary(t *testing.T, raw string) map[string]interface{} {
	t.Helper()
	object, err := parseSemanticDictionary(context.Background(), raw)
	if err != nil {
		t.Fatalf("parse semantic dictionary: %v", err)
	}
	return object
}

func TestSemanticDictionaryParsingAndCanonicalization(t *testing.T) {
	t.Parallel()

	raw := ` { "string_number":"001", "large":9007199254740993, "exponent":1e3, "close":1.0000000000000001, "nested":{"null":null,"list":[true,2.50,{}]} } `
	object := mustParseSemanticDictionary(t, raw)
	canonical, err := canonicalSemanticDictionary(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"close":1.0000000000000001,"exponent":1000,"large":9007199254740993,"nested":{"list":[true,2.5,{}],"null":null},"string_number":"001"}`
	if canonical != want {
		t.Fatalf("canonical dictionary = %s, want %s", canonical, want)
	}
	reparsed := mustParseSemanticDictionary(t, canonical)
	equal, err := semanticDictionaryValuesEqual(context.Background(), object, reparsed)
	if err != nil || !equal {
		t.Fatalf("canonical round trip changed exact values: %#v != %#v (%v)", object, reparsed, err)
	}

	configuredSpelling := types.StringValue(`{ "exponent" : 1e3 }`)
	reconciled, err := reconcileSemanticDictionaryString(context.Background(), configuredSpelling, map[string]interface{}{"exponent": json.Number("1000")})
	if err != nil || !reconciled.Equal(configuredSpelling) {
		t.Fatalf("semantic reconciliation = %#v, %v; want configured spelling", reconciled, err)
	}
	drifted, err := reconcileSemanticDictionaryString(context.Background(), configuredSpelling, map[string]interface{}{"exponent": json.Number("1001")})
	if err != nil || drifted.ValueString() != `{"exponent":1001}` {
		t.Fatalf("drift reconciliation = %#v, %v", drifted, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if reconciled, err := reconcileSemanticDictionaryString(ctx, configuredSpelling, object); !errors.Is(err, context.Canceled) || !reconciled.IsNull() {
		t.Fatalf("canceled reconciliation = %#v, %v", reconciled, err)
	}

	for name, invalid := range map[string]string{
		"duplicate": `{"secret":1,"secret":2}`,
		"trailing":  `{} {}`,
		"array":     `[]`,
		"scalar":    `true`,
		"null":      `null`,
		"malformed": `{"a":`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSemanticDictionary(context.Background(), invalid); err == nil {
				t.Fatalf("invalid dictionary %q was accepted", invalid)
			}
		})
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	if _, err := parseSemanticDictionary(ctx, `{}`); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled parse error = %v, want context.Canceled", err)
	}
}

func TestSemanticDictionaryLeafPathsAreExactAndPrefixFree(t *testing.T) {
	t.Parallel()

	object := mustParseSemanticDictionary(t, `{
		"scalar":1,
		"nested":{"slash/key":{"tilde~key":true},"empty":{}},
		"array":[{"inside":"atomic"}],
		"null":null
	}`)
	paths, err := semanticDictionaryLeafPaths(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/array", "/nested/empty", "/nested/slash~1key/tilde~0key", "/null", "/scalar"}
	got := sortedSemanticDictionaryPaths(paths)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("leaf paths = %#v, want %#v", got, want)
	}
	for _, pointer := range got {
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			t.Fatalf("decode %q: %v", pointer, err)
		}
		canonical, err := encodeSemanticDictionaryPointer(members)
		if err != nil || canonical != pointer {
			t.Fatalf("pointer round trip = %q, %v; want %q", canonical, err, pointer)
		}
	}

	emptyPaths, err := semanticDictionaryLeafPaths(context.Background(), map[string]interface{}{})
	if err != nil || len(emptyPaths) != 0 {
		t.Fatalf("root empty paths = %#v, %v; want empty", emptyPaths, err)
	}
	for _, invalid := range []string{"", "not-a-pointer", "/bad~2escape", "/bad~", strings.Repeat("/x", semanticDictionaryMaxPointerDepth+1)} {
		if _, err := decodeSemanticDictionaryPointer(invalid); err == nil {
			t.Fatalf("invalid pointer %q was accepted", invalid)
		}
	}
}

func TestSemanticDictionaryApplyIsAtomicAndPreservesUnownedPaths(t *testing.T) {
	t.Parallel()

	base := mustParseSemanticDictionary(t, `{
		"owned":{"old":1,"keep_api":"preserve"},
		"api":{"nested":true},
		"transition":"scalar",
		"untouched":[1,2]
	}`)
	configured := mustParseSemanticDictionary(t, `{
		"owned":{"new":2},
		"transition":{"native":true},
		"explicit_empty":{}
	}`)
	priorOwned := semanticDictionaryPathSet{"/owned/old": true, "/transition": true}
	result, owned, err := applySemanticDictionary(context.Background(), base, configured, priorOwned)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalSemanticDictionary(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"api":{"nested":true},"explicit_empty":{},"owned":{"keep_api":"preserve","new":2},"transition":{"native":true},"untouched":[1,2]}`
	if canonical != want {
		t.Fatalf("applied dictionary = %s, want %s", canonical, want)
	}
	wantOwned := []string{"/explicit_empty", "/owned/new", "/transition/native"}
	if got := sortedSemanticDictionaryPaths(owned); strings.Join(got, "\n") != strings.Join(wantOwned, "\n") {
		t.Fatalf("owned paths = %#v, want %#v", got, wantOwned)
	}
	// Inputs remain immutable.
	baseCanonical, _ := canonicalSemanticDictionary(context.Background(), base)
	if strings.Contains(baseCanonical, `"new"`) || !strings.Contains(baseCanonical, `"old"`) {
		t.Fatalf("base was mutated: %s", baseCanonical)
	}

	malformedBase := mustParseSemanticDictionary(t, `{"owned":"shape-changed"}`)
	before, _ := canonicalSemanticDictionary(context.Background(), malformedBase)
	if partial, partialOwned, err := applySemanticDictionary(context.Background(), malformedBase, configured, semanticDictionaryPathSet{"/owned/old": true}); err == nil || partial != nil || partialOwned != nil {
		t.Fatalf("invalid traversal returned partial output: %#v %#v %v", partial, partialOwned, err)
	}
	after, _ := canonicalSemanticDictionary(context.Background(), malformedBase)
	if after != before {
		t.Fatalf("failed apply mutated base: %s != %s", after, before)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if partial, partialOwned, err := applySemanticDictionary(ctx, base, configured, priorOwned); !errors.Is(err, context.Canceled) || partial != nil || partialOwned != nil {
		t.Fatalf("canceled apply = %#v %#v %v", partial, partialOwned, err)
	}
	ownershipCancellation := &cancelOnErrCallContext{Context: context.Background(), cancelOn: 3}
	if partial, partialOwned, err := applySemanticDictionary(ownershipCancellation, map[string]interface{}{}, map[string]interface{}{}, semanticDictionaryPathSet{}); !errors.Is(err, context.Canceled) || partial != nil || partialOwned != nil {
		t.Fatalf("ownership-validation cancellation = %#v %#v %v", partial, partialOwned, err)
	}
}

func TestSemanticDictionaryMaskRestorationIsShapeBound(t *testing.T) {
	t.Parallel()

	var visitedPaths []string
	maskPolicy := func(path []string, value string) bool {
		visitedPaths = append(visitedPaths, strings.Join(path, "/"))
		return value == "********" || value == "REDACTED" || value == "****"
	}
	prior := mustParseSemanticDictionary(t, `{"auth":{"client_secret":"plaintext","token":"token-value"},"items":[{"secret":"array-secret"}],"drift":1}`)
	observed := mustParseSemanticDictionary(t, `{"auth":{"client_secret":"********","token":"REDACTED"},"items":[{"secret":"****"}],"drift":2}`)
	restored, err := restoreSemanticDictionaryMaskedValues(context.Background(), prior, observed, true, maskPolicy)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := canonicalSemanticDictionary(context.Background(), restored)
	want := `{"auth":{"client_secret":"plaintext","token":"token-value"},"drift":2,"items":[{"secret":"array-secret"}]}`
	if canonical != want {
		t.Fatalf("restored dictionary = %s, want %s", canonical, want)
	}
	for _, wantPath := range []string{"auth/client_secret", "auth/token", "items/0/secret"} {
		if !strings.Contains(strings.Join(visitedPaths, "\n"), wantPath) {
			t.Fatalf("mask policy did not receive path %q: %#v", wantPath, visitedPaths)
		}
	}

	cases := []struct {
		name          string
		prior         string
		observed      string
		authoritative bool
	}{
		{name: "masked import", prior: `{}`, observed: `{"secret":"****"}`, authoritative: false},
		{name: "missing prior leaf", prior: `{}`, observed: `{"secret":"****"}`, authoritative: true},
		{name: "masked parent", prior: `{"secret":{"value":"plain"}}`, observed: `{"secret":"****"}`, authoritative: true},
		{name: "shape change", prior: `{"secret":"plain","other":1}`, observed: `{"secret":"****"}`, authoritative: true},
		{name: "masked prior", prior: `{"secret":"****"}`, observed: `{"secret":"****"}`, authoritative: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if partial, err := restoreSemanticDictionaryMaskedValues(context.Background(), mustParseSemanticDictionary(t, test.prior), mustParseSemanticDictionary(t, test.observed), test.authoritative, maskPolicy); err == nil || partial != nil {
				t.Fatalf("unsafe mask recovery returned %#v, %v", partial, err)
			}
		})
	}

	unmasked := mustParseSemanticDictionary(t, `{"remote":{"changed":true},"literal":"redacted-but-not-this-endpoint-sentinel"}`)
	adopted, err := restoreSemanticDictionaryMaskedValues(context.Background(), nil, unmasked, false, maskPolicy)
	equal, compareErr := semanticDictionaryValuesEqual(context.Background(), unmasked, adopted)
	if err != nil || compareErr != nil || !equal {
		t.Fatalf("unmasked adoption = %#v, %v, compare %v", adopted, err, compareErr)
	}
	pathSpecific := func(path []string, value string) bool {
		return strings.Join(path, "/") == "secret" && value == "****"
	}
	literalSentinel := mustParseSemanticDictionary(t, `{"description":"****"}`)
	literalResult, err := restoreSemanticDictionaryMaskedValues(context.Background(), nil, literalSentinel, false, pathSpecific)
	equal, compareErr = semanticDictionaryValuesEqual(context.Background(), literalSentinel, literalResult)
	if err != nil || compareErr != nil || !equal {
		t.Fatalf("path-specific literal adoption = %#v, %v, compare %v", literalResult, err, compareErr)
	}
	if partial, err := restoreSemanticDictionaryMaskedValues(context.Background(), prior, observed, true, nil); !errors.Is(err, errSemanticDictionaryMaskPolicy) || partial != nil {
		t.Fatalf("missing mask policy returned %#v, %v", partial, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if partial, err := restoreSemanticDictionaryMaskedValues(ctx, prior, observed, true, maskPolicy); !errors.Is(err, context.Canceled) || partial != nil {
		t.Fatalf("canceled mask recovery returned %#v, %v", partial, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	cancelDuringPolicy := func(_ []string, _ string) bool {
		cancel()
		return false
	}
	if partial, err := restoreSemanticDictionaryMaskedValues(ctx, nil, literalSentinel, false, cancelDuringPolicy); !errors.Is(err, context.Canceled) || partial != nil {
		t.Fatalf("mid-policy cancellation returned %#v, %v", partial, err)
	}
}

func TestSemanticDictionaryOverlapDiagnosticsAreContentFree(t *testing.T) {
	t.Parallel()

	sensitiveKey := "private-customer-secret-name"
	object := map[string]interface{}{sensitiveKey: "value"}
	err := semanticDictionaryTopLevelOverlap(context.Background(), object, []string{sensitiveKey}, nil)
	if !errors.Is(err, errSemanticDictionaryOverlap) {
		t.Fatalf("overlap error = %v", err)
	}
	if strings.Contains(err.Error(), sensitiveKey) || strings.Contains(err.Error(), "value") {
		t.Fatalf("overlap error exposed sensitive content: %v", err)
	}
	if err := semanticDictionaryTopLevelOverlap(context.Background(), object, []string{"other"}, []string{"reserved"}); err != nil {
		t.Fatalf("disjoint keys rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := semanticDictionaryTopLevelOverlap(ctx, object, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled overlap error = %v", err)
	}
}

func TestSemanticDictionaryProvenanceRoundTripAndCorruption(t *testing.T) {
	t.Parallel()

	value := semanticDictionaryProvenance{
		Initialized:           true,
		Configured:            true,
		TerraformOwned:        semanticDictionaryPathSet{"/configured/a": true, "/empty": true},
		APIOwned:              semanticDictionaryPathSet{"/remote": true},
		PendingTerraformOwned: semanticDictionaryPathSet{"/configured/b": true},
		PendingAPIOwned:       semanticDictionaryPathSet{"/new_remote": true},
		PendingRemovals:       semanticDictionaryPathSet{"/configured/a": true},
	}
	encoded, err := encodeSemanticDictionaryProvenance(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSemanticDictionaryProvenance(context.Background(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	if reencoded, err := encodeSemanticDictionaryProvenance(context.Background(), decoded); err != nil || !json.Valid(reencoded) || string(reencoded) != string(encoded) {
		t.Fatalf("provenance round trip = %s, %v; want %s", reencoded, err, encoded)
	}
	cloned, err := cloneSemanticDictionaryProvenance(context.Background(), decoded)
	if err != nil {
		t.Fatal(err)
	}
	cloned.TerraformOwned["/new"] = true
	if decoded.TerraformOwned["/new"] {
		t.Fatal("provenance clone aliased source path set")
	}

	invalidValues := []semanticDictionaryProvenance{
		{TerraformOwned: semanticDictionaryPathSet{}, APIOwned: semanticDictionaryPathSet{}, PendingTerraformOwned: semanticDictionaryPathSet{}, PendingAPIOwned: semanticDictionaryPathSet{}, PendingRemovals: semanticDictionaryPathSet{}, Configured: true},
		{Initialized: true, TerraformOwned: semanticDictionaryPathSet{"/same": true}, APIOwned: semanticDictionaryPathSet{"/same/child": true}, PendingTerraformOwned: semanticDictionaryPathSet{}, PendingAPIOwned: semanticDictionaryPathSet{}, PendingRemovals: semanticDictionaryPathSet{}},
		{Initialized: true, TerraformOwned: semanticDictionaryPathSet{}, APIOwned: semanticDictionaryPathSet{}, PendingTerraformOwned: semanticDictionaryPathSet{}, PendingAPIOwned: semanticDictionaryPathSet{}, PendingRemovals: semanticDictionaryPathSet{"/not-owned": true}},
		{Initialized: true, TerraformOwned: semanticDictionaryPathSet{"/parent": true, "/parent/child": true}, APIOwned: semanticDictionaryPathSet{}, PendingTerraformOwned: semanticDictionaryPathSet{}, PendingAPIOwned: semanticDictionaryPathSet{}, PendingRemovals: semanticDictionaryPathSet{}},
	}
	for index, invalid := range invalidValues {
		if encoded, err := encodeSemanticDictionaryProvenance(context.Background(), invalid); err == nil || encoded != nil {
			t.Fatalf("invalid provenance %d encoded as %s, %v", index, encoded, err)
		}
	}

	corrupt := [][]byte{
		nil,
		append([]byte(" "), encoded...),
		[]byte(`{"version":1,"version":1,"initialized":false,"configured":false,"terraform_owned":[],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`),
		[]byte(`{"version":2,"initialized":false,"configured":false,"terraform_owned":[],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`),
		[]byte(`{"version":1,"initialized":true,"configured":false,"terraform_owned":["/z","/a"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`),
		[]byte(`{"version":1,"initialized":true,"configured":false,"terraform_owned":["/a","/a"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`),
		[]byte(`{"version":1,"initialized":false,"configured":false,"terraform_owned":[],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[],"unknown":true}`),
	}
	for index, raw := range corrupt {
		if decoded, err := decodeSemanticDictionaryProvenance(context.Background(), raw); err == nil {
			t.Fatalf("corrupt provenance %d decoded as %#v", index, decoded)
		}
	}

	tooMany := emptySemanticDictionaryProvenance()
	tooMany.Initialized = true
	for index := 0; index <= semanticDictionaryMaxPointers/2; index++ {
		tooMany.TerraformOwned["/terraform"+strconv.Itoa(index)] = true
		tooMany.APIOwned["/api"+strconv.Itoa(index)] = true
	}
	if encoded, err := encodeSemanticDictionaryProvenance(context.Background(), tooMany); err == nil || encoded != nil {
		t.Fatalf("aggregate pointer limit encoded %d paths as %s, %v", len(tooMany.TerraformOwned)+len(tooMany.APIOwned), encoded, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateSemanticDictionaryProvenance(ctx, emptySemanticDictionaryProvenance()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled empty provenance validation = %v", err)
	}
	if encoded, err := encodeSemanticDictionaryProvenance(ctx, value); !errors.Is(err, context.Canceled) || encoded != nil {
		t.Fatalf("canceled provenance encode = %s, %v", encoded, err)
	}
	if decoded, err := decodeSemanticDictionaryProvenance(ctx, encoded); !errors.Is(err, context.Canceled) || decoded.Initialized {
		t.Fatalf("canceled provenance decode = %#v, %v", decoded, err)
	}
}
