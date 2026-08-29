# litellm_model - JSON-valued additional_litellm_params
#
# Regression fixture for: https://github.com/ncecere/terraform-provider-litellm/issues/126
#
# A JSON object/array value in additional_litellm_params must survive a
# plan/apply round-trip even when the config's JSON formatting differs from
# Go's compact, key-sorted json.Marshal output. Before the fix, apply failed:
#
#   Error: Provider produced inconsistent result after apply
#   .additional_litellm_params["input_schema"]:
#     was cty.StringVal("{\"inputs\": \"{prompt}\"}"),
#     but now cty.StringVal("{\"inputs\":\"{prompt}\"}").
#
# The values below use non-canonical formatting on purpose: a space after the
# colon, out-of-order keys, and an array — so `make smoke` catches a regression.

resource "litellm_model" "json_params" {
  model_name          = "test-model-json-params"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"

  additional_litellm_params = {
    # space after colon (differs from compact marshal)
    input_schema = "{\"inputs\": \"{prompt}\"}"
    # keys deliberately out of alphabetical order
    ordering = "{\"b\": 2, \"a\": 1}"
    # a JSON array value
    stop_sequences = "[\"</end>\", \"STOP\"]"
  }
}

output "model_json_params_id" {
  value = litellm_model.json_params.id
}
