package provider

import (
	"encoding/json"
	"testing"
)

func TestDecodeListObjectPreservesExactIntegerLexemes(t *testing.T) {
	t.Parallel()
	object, err := decodeListObject(json.RawMessage(`{"integer":9007199254740993,"scientific":9.007199254740993e15}`), "/test/list", "test item")
	if err != nil {
		t.Fatal(err)
	}
	integer, ok := object["integer"].(json.Number)
	if !ok || integer.String() != "9007199254740993" {
		t.Fatalf("integer = %#v", object["integer"])
	}
	scientific, ok := object["scientific"].(json.Number)
	if !ok || scientific.String() != "9.007199254740993e15" {
		t.Fatalf("scientific = %#v", object["scientific"])
	}
	value, err := exactInt64FromAPI(scientific)
	if err != nil || value != 9007199254740993 {
		t.Fatalf("scientific exact value = %d, %v", value, err)
	}
}
