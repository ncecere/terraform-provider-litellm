package litellm

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func dataSourceLiteLLMSpendLogs() *schema.Resource {
	return &schema.Resource{
		ReadContext: dataSourceLiteLLMSpendLogsRead,
		Description: "Data source for querying LiteLLM spend logs and usage analytics",
		Schema: map[string]*schema.Schema{
			"start_date": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Start date for the query (RFC3339 format)",
			},
			"end_date": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "End date for the query (RFC3339 format)",
			},
			"filters": {
				Type:        schema.TypeMap,
				Optional:    true,
				Description: "Filters to apply to the query",
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"aggregation": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "none",
				Description: "Aggregation type (none, daily, weekly, monthly, by_user, by_team, by_model)",
			},
			"include_metadata": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				Description: "Include detailed metadata in results",
			},
			// Computed attributes
			"total_spend": {
				Type:        schema.TypeFloat,
				Computed:    true,
				Description: "Total spend for the period",
			},
			"total_tokens": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Total tokens used for the period",
			},
			"total_requests": {
				Type:        schema.TypeInt,
				Computed:    true,
				Description: "Total number of requests for the period",
			},
			"logs": {
				Type:        schema.TypeList,
				Computed:    true,
				Description: "Detailed spend logs",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"request_id": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Unique request ID",
						},
						"user_id": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "User ID who made the request",
						},
						"team_id": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Team ID associated with the request",
						},
						"api_key": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "API key used (masked)",
						},
						"model": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Model used for the request",
						},
						"spend": {
							Type:        schema.TypeFloat,
							Computed:    true,
							Description: "Cost of the request in USD",
						},
						"total_tokens": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "Total tokens used",
						},
						"prompt_tokens": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "Prompt tokens used",
						},
						"completion_tokens": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "Completion tokens used",
						},
						"created_at": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Timestamp of the request",
						},
						"metadata": {
							Type:        schema.TypeMap,
							Computed:    true,
							Description: "Additional metadata",
							Elem: &schema.Schema{
								Type: schema.TypeString,
							},
						},
					},
				},
			},
			"aggregated_data": {
				Type:        schema.TypeList,
				Computed:    true,
				Description: "Aggregated spend data (when aggregation is enabled)",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"period": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Period for aggregation",
						},
						"dimension": {
							Type:        schema.TypeString,
							Computed:    true,
							Description: "Dimension value (user_id, team_id, model, etc.)",
						},
						"total_spend": {
							Type:        schema.TypeFloat,
							Computed:    true,
							Description: "Total spend for this dimension/period",
						},
						"total_tokens": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "Total tokens for this dimension/period",
						},
						"total_requests": {
							Type:        schema.TypeInt,
							Computed:    true,
							Description: "Total requests for this dimension/period",
						},
						"average_cost_per_request": {
							Type:        schema.TypeFloat,
							Computed:    true,
							Description: "Average cost per request",
						},
					},
				},
			},
		},
	}
}

func dataSourceLiteLLMSpendLogsRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*Client)
	
	// Parse dates
	startDate, err := time.Parse(time.RFC3339, d.Get("start_date").(string))
	if err != nil {
		return diag.Errorf("Invalid start_date format: %s", err)
	}
	
	endDate, err := time.Parse(time.RFC3339, d.Get("end_date").(string))
	if err != nil {
		return diag.Errorf("Invalid end_date format: %s", err)
	}
	
	// Build query parameters
	queryParams := map[string]interface{}{
		"start_date":        startDate.Format(time.RFC3339),
		"end_date":          endDate.Format(time.RFC3339),
		"aggregation":       d.Get("aggregation").(string),
		"include_metadata":  d.Get("include_metadata").(bool),
	}
	
	// Add filters
	if filters, ok := d.GetOk("filters"); ok {
		queryParams["filters"] = filters.(map[string]interface{})
	}
	
	// Query spend logs
	result, err := client.GetSpendLogs(queryParams)
	if err != nil {
		return diag.Errorf("Error querying spend logs: %s", err)
	}
	
	// Set total statistics
	if totalSpend, ok := result["total_spend"].(float64); ok {
		d.Set("total_spend", totalSpend)
	}
	
	if totalTokens, ok := result["total_tokens"].(float64); ok {
		d.Set("total_tokens", int(totalTokens))
	}
	
	if totalRequests, ok := result["total_requests"].(float64); ok {
		d.Set("total_requests", int(totalRequests))
	}
	
	// Process detailed logs
	if logs, ok := result["logs"].([]interface{}); ok {
		processedLogs := make([]map[string]interface{}, 0, len(logs))
		for _, log := range logs {
			if logMap, ok := log.(map[string]interface{}); ok {
				processedLog := map[string]interface{}{
					"request_id":        logMap["request_id"],
					"user_id":           logMap["user_id"],
					"team_id":           logMap["team_id"],
					"model":             logMap["model"],
					"spend":             logMap["spend"],
					"total_tokens":      int(logMap["total_tokens"].(float64)),
					"prompt_tokens":     int(logMap["prompt_tokens"].(float64)),
					"completion_tokens": int(logMap["completion_tokens"].(float64)),
					"created_at":        logMap["created_at"],
				}
				
				// Mask API key
				if apiKey, ok := logMap["api_key"].(string); ok && len(apiKey) > 8 {
					processedLog["api_key"] = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
				}
				
				if metadata, ok := logMap["metadata"].(map[string]interface{}); ok {
					processedLog["metadata"] = metadata
				}
				
				processedLogs = append(processedLogs, processedLog)
			}
		}
		d.Set("logs", processedLogs)
	}
	
	// Process aggregated data
	if aggregatedData, ok := result["aggregated_data"].([]interface{}); ok {
		processedAggregated := make([]map[string]interface{}, 0, len(aggregatedData))
		for _, agg := range aggregatedData {
			if aggMap, ok := agg.(map[string]interface{}); ok {
				processed := map[string]interface{}{
					"period":                   aggMap["period"],
					"dimension":                aggMap["dimension"],
					"total_spend":              aggMap["total_spend"],
					"total_tokens":             int(aggMap["total_tokens"].(float64)),
					"total_requests":           int(aggMap["total_requests"].(float64)),
					"average_cost_per_request": aggMap["average_cost_per_request"],
				}
				processedAggregated = append(processedAggregated, processed)
			}
		}
		d.Set("aggregated_data", processedAggregated)
	}
	
	// Generate a unique ID for this data source instance
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s-%v", startDate, endDate, d.Get("aggregation").(string), d.Get("filters"))))
	d.SetId(fmt.Sprintf("%x", h.Sum(nil)))
	
	log.Printf("[INFO] Read spend logs from %s to %s", startDate, endDate)
	
	return nil
}

// Client method for querying spend logs
func (c *Client) GetSpendLogs(params map[string]interface{}) (map[string]interface{}, error) {
	// Convert params to query string
	query := "?"
	if startDate, ok := params["start_date"].(string); ok {
		query += fmt.Sprintf("start_date=%s&", startDate)
	}
	if endDate, ok := params["end_date"].(string); ok {
		query += fmt.Sprintf("end_date=%s&", endDate)
	}
	if aggregation, ok := params["aggregation"].(string); ok {
		query += fmt.Sprintf("aggregation=%s&", aggregation)
	}
	if includeMetadata, ok := params["include_metadata"].(bool); ok {
		query += fmt.Sprintf("include_metadata=%t&", includeMetadata)
	}
	
	// Add filters
	if filters, ok := params["filters"].(map[string]interface{}); ok {
		filterJSON, _ := json.Marshal(filters)
		query += fmt.Sprintf("filters=%s&", string(filterJSON))
	}
	
	return c.doRequest("GET", fmt.Sprintf("/spend/logs%s", query), nil)
}