package litellm

import (
	"encoding/json"
	"testing"
)

func TestDecodeJSONUseNumberPreservesLargeInteger(t *testing.T) {
	var value map[string]interface{}
	if err := decodeJSONUseNumber([]byte(`{"large":9007199254740993}`), &value); err != nil {
		t.Fatal(err)
	}
	number, ok := value["large"].(json.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("large = %#v", value["large"])
	}
}
