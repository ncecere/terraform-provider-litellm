package provider

import "testing"

func TestParseModelInfoResultWrappedData(t *testing.T) {
	t.Parallel()

	result := parseModelInfoResult(map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"model_name": "test-model"},
		},
	})

	if result["model_name"] != "test-model" {
		t.Fatalf("unexpected model info result: %#v", result)
	}
}
