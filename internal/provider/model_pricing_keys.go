package provider

import "regexp"

// liteLLMModelPricingKeys is LiteLLM's server-derived pricing field set
// (litellm.types.utils.SERVER_DERIVED_PRICING_FIELDS) at the pinned release.
// LiteLLM 1.102.0 and later silently drop these keys from model_info on
// /model/new and on every model PATCH; custom pricing must be sent in
// litellm_params instead. TestModelPricingKeysMatchPinnedContract derives the
// same set from the pinned openapi.json so a re-pin cannot drift silently.
var liteLLMModelPricingKeys = map[string]struct{}{
	"annotation_cost_per_page":                                   {},
	"annotation_cost_per_page_batches":                           {},
	"cache_creation_input_audio_token_cost":                      {},
	"cache_creation_input_token_cost":                            {},
	"cache_creation_input_token_cost_above_1hr":                  {},
	"cache_creation_input_token_cost_above_200k_tokens":          {},
	"cache_creation_input_token_cost_above_200k_tokens_batches":  {},
	"cache_creation_input_token_cost_above_272k_tokens":          {},
	"cache_creation_input_token_cost_above_272k_tokens_batches":  {},
	"cache_creation_input_token_cost_above_272k_tokens_flex":     {},
	"cache_creation_input_token_cost_above_272k_tokens_priority": {},
	"cache_creation_input_token_cost_batches":                    {},
	"cache_creation_input_token_cost_flex":                       {},
	"cache_creation_input_token_cost_priority":                   {},
	"cache_creation_input_token_cost_ultrafast":                  {},
	"cache_read_input_audio_token_cost":                          {},
	"cache_read_input_image_token_cost":                          {},
	"cache_read_input_token_cost":                                {},
	"cache_read_input_token_cost_above_200k_tokens":              {},
	"cache_read_input_token_cost_above_200k_tokens_priority":     {},
	"cache_read_input_token_cost_above_272k_tokens":              {},
	"cache_read_input_token_cost_above_272k_tokens_batches":      {},
	"cache_read_input_token_cost_above_272k_tokens_flex":         {},
	"cache_read_input_token_cost_above_272k_tokens_priority":     {},
	"cache_read_input_token_cost_above_512k_tokens":              {},
	"cache_read_input_token_cost_batches":                        {},
	"cache_read_input_token_cost_flex":                           {},
	"cache_read_input_token_cost_priority":                       {},
	"cache_read_input_token_cost_ultrafast":                      {},
	"citation_cost_per_token":                                    {},
	"google_maps_grounding_cost_per_query":                       {},
	"input_cost_per_audio_per_second":                            {},
	"input_cost_per_audio_per_second_above_128k_tokens":          {},
	"input_cost_per_audio_token":                                 {},
	"input_cost_per_audio_token_batches":                         {},
	"input_cost_per_character":                                   {},
	"input_cost_per_character_above_128k_tokens":                 {},
	"input_cost_per_image":                                       {},
	"input_cost_per_image_above_128k_tokens":                     {},
	"input_cost_per_image_token":                                 {},
	"input_cost_per_image_token_batches":                         {},
	"input_cost_per_pixel":                                       {},
	"input_cost_per_query":                                       {},
	"input_cost_per_second":                                      {},
	"input_cost_per_token":                                       {},
	"input_cost_per_token_above_128k_tokens":                     {},
	"input_cost_per_token_above_200k_tokens":                     {},
	"input_cost_per_token_above_200k_tokens_priority":            {},
	"input_cost_per_token_above_272k_tokens":                     {},
	"input_cost_per_token_above_272k_tokens_batches":             {},
	"input_cost_per_token_above_272k_tokens_flex":                {},
	"input_cost_per_token_above_272k_tokens_priority":            {},
	"input_cost_per_token_above_512k_tokens":                     {},
	"input_cost_per_token_batches":                               {},
	"input_cost_per_token_cache_hit":                             {},
	"input_cost_per_token_flex":                                  {},
	"input_cost_per_token_priority":                              {},
	"input_cost_per_token_ultrafast":                             {},
	"input_cost_per_video_per_second":                            {},
	"input_cost_per_video_per_second_above_128k_tokens":          {},
	"input_cost_per_video_per_second_above_15s_interval":         {},
	"input_cost_per_video_per_second_above_8s_interval":          {},
	"input_cost_per_video_token":                                 {},
	"input_cost_per_video_token_batches":                         {},
	"ocr_cost_per_credit":                                        {},
	"ocr_cost_per_page":                                          {},
	"ocr_cost_per_page_batches":                                  {},
	"output_cost_per_audio_per_second":                           {},
	"output_cost_per_audio_token":                                {},
	"output_cost_per_character":                                  {},
	"output_cost_per_character_above_128k_tokens":                {},
	"output_cost_per_image":                                      {},
	"output_cost_per_image_1024":                                 {},
	"output_cost_per_image_1536":                                 {},
	"output_cost_per_image_512":                                  {},
	"output_cost_per_image_token":                                {},
	"output_cost_per_pixel":                                      {},
	"output_cost_per_reasoning_token":                            {},
	"output_cost_per_reasoning_token_flex":                       {},
	"output_cost_per_reasoning_token_priority":                   {},
	"output_cost_per_second":                                     {},
	"output_cost_per_second_1080p":                               {},
	"output_cost_per_second_2k":                                  {},
	"output_cost_per_second_480p":                                {},
	"output_cost_per_second_4k":                                  {},
	"output_cost_per_second_720p":                                {},
	"output_cost_per_second_768p":                                {},
	"output_cost_per_token":                                      {},
	"output_cost_per_token_above_128k_tokens":                    {},
	"output_cost_per_token_above_200k_tokens":                    {},
	"output_cost_per_token_above_200k_tokens_priority":           {},
	"output_cost_per_token_above_272k_tokens":                    {},
	"output_cost_per_token_above_272k_tokens_batches":            {},
	"output_cost_per_token_above_272k_tokens_flex":               {},
	"output_cost_per_token_above_272k_tokens_priority":           {},
	"output_cost_per_token_above_512k_tokens":                    {},
	"output_cost_per_token_batches":                              {},
	"output_cost_per_token_flex":                                 {},
	"output_cost_per_token_priority":                             {},
	"output_cost_per_token_ultrafast":                            {},
	"output_cost_per_video_per_second":                           {},
	"output_cost_per_video_token":                                {},
	"regional_endpoint_uplift_multiplier":                        {},
	"regional_processing_uplift_multiplier_eu":                   {},
	"regional_processing_uplift_multiplier_us":                   {},
	"search_context_cost_per_query":                              {},
	"tiered_pricing":                                             {},
}

// liteLLMTieredPricingKeyPattern matches the tiered "*_above_<N>[k]_tokens"
// rates that LiteLLM also treats as pricing although no model declares them.
var liteLLMTieredPricingKeyPattern = regexp.MustCompile(`_above_\d+k?_tokens$`)

// liteLLMModelInfoGeneratedPricingKeys are generated by /model/info. A stored
// model_info "key" marks the row as a copied /model/info response, which makes
// LiteLLM 1.103.0 and later ignore that row's model_info pricing entirely.
var liteLLMModelInfoGeneratedPricingKeys = map[string]struct{}{
	"key":               {},
	"pricing_overrides": {},
}

// isLiteLLMModelInfoPricingKey reports whether a model_info key cannot carry a
// durable value on LiteLLM 1.102.0 and later because it is pricing.
func isLiteLLMModelInfoPricingKey(key string) bool {
	if _, ok := liteLLMModelPricingKeys[key]; ok {
		return true
	}
	if _, ok := liteLLMModelInfoGeneratedPricingKeys[key]; ok {
		return true
	}
	return liteLLMTieredPricingKeyPattern.MatchString(key)
}

const modelInfoPricingKeyDiagnostic = "LiteLLM 1.102.0 and later ignore pricing sent in model_info: the model is saved without it, spend falls back to LiteLLM's catalog price, and Terraform then reports an inconsistent result. Set custom pricing with the dedicated cost attributes (for example input_cost_per_million_tokens) or in additional_litellm_params / additional_litellm_params_json instead. The model_info keys \"key\" and \"pricing_overrides\" are generated by LiteLLM and cannot be managed."
