package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	jwtKeyMappingInfoPath   = "/jwt/key/mapping/info"
	jwtKeyMappingListPath   = "/jwt/key/mapping/list"
	jwtKeyMappingCreatePath = "/jwt/key/mapping/new"
	jwtKeyMappingUpdatePath = "/jwt/key/mapping/update"
	jwtKeyMappingDeletePath = "/jwt/key/mapping/delete"
)

type jwtKeyMappingObject struct {
	ID          string
	ClaimName   string
	ClaimValue  string
	Description *string
	IsActive    bool
	CreatedAt   string
	UpdatedAt   string
	CreatedBy   *string
	UpdatedBy   *string
}

func (m jwtKeyMappingObject) listItemIdentity() string { return m.ID }

func canonicalJWTKeyMappingID(value string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", fmt.Errorf("mapping ID must be a canonical lowercase UUID")
	}
	return value, nil
}

func jwtKeyMappingInfoEndpoint(id string) string {
	query := url.Values{}
	query.Set("id", id)
	return endpointWithQuery(jwtKeyMappingInfoPath, query)
}

func decodeJWTKeyMappingObject(raw json.RawMessage) (jwtKeyMappingObject, error) {
	var result jwtKeyMappingObject
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return result, fmt.Errorf("JWT key mapping response must be a JSON object")
	}
	var object map[string]json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &object); err != nil || object == nil {
		return result, fmt.Errorf("JWT key mapping response must be a valid JSON object")
	}
	allowed := map[string]struct{}{
		"id": {}, "jwt_claim_name": {}, "jwt_claim_value": {}, "description": {}, "is_active": {},
		"created_at": {}, "updated_at": {}, "created_by": {}, "updated_by": {},
	}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return result, fmt.Errorf("JWT key mapping response contained unsupported field %q", key)
		}
	}
	var err error
	if result.ID, err = requiredJSONString(object, "id"); err != nil {
		return result, err
	}
	if _, err = canonicalJWTKeyMappingID(result.ID); err != nil {
		return result, fmt.Errorf("JWT key mapping response returned an invalid id")
	}
	if result.ClaimName, err = requiredJSONString(object, "jwt_claim_name"); err != nil || result.ClaimName == "" {
		return result, fmt.Errorf("JWT key mapping response omitted a non-empty jwt_claim_name")
	}
	if result.ClaimValue, err = requiredJSONString(object, "jwt_claim_value"); err != nil || result.ClaimValue == "" {
		return result, fmt.Errorf("JWT key mapping response omitted a non-empty jwt_claim_value")
	}
	if result.Description, err = nullableJSONString(object, "description"); err != nil {
		return result, err
	}
	if result.IsActive, err = requiredJSONBool(object, "is_active"); err != nil {
		return result, err
	}
	if result.CreatedAt, err = requiredJSONString(object, "created_at"); err != nil {
		return result, err
	}
	if result.UpdatedAt, err = requiredJSONString(object, "updated_at"); err != nil {
		return result, err
	}
	if _, err = time.Parse(time.RFC3339Nano, result.CreatedAt); err != nil {
		return result, fmt.Errorf("JWT key mapping response returned an invalid created_at timestamp")
	}
	if _, err = time.Parse(time.RFC3339Nano, result.UpdatedAt); err != nil {
		return result, fmt.Errorf("JWT key mapping response returned an invalid updated_at timestamp")
	}
	if result.CreatedBy, err = nullableJSONString(object, "created_by"); err != nil {
		return result, err
	}
	if result.UpdatedBy, err = nullableJSONString(object, "updated_by"); err != nil {
		return result, err
	}
	return result, nil
}

func requiredJSONString(object map[string]json.RawMessage, field string) (string, error) {
	raw, ok := object[field]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", fmt.Errorf("JWT key mapping response omitted required %s", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("JWT key mapping response returned invalid %s", field)
	}
	return value, nil
}

func nullableJSONString(object map[string]json.RawMessage, field string) (*string, error) {
	raw, ok := object[field]
	if !ok {
		return nil, fmt.Errorf("JWT key mapping response omitted required nullable %s", field)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("JWT key mapping response returned invalid %s", field)
	}
	return &value, nil
}

func requiredJSONBool(object map[string]json.RawMessage, field string) (bool, error) {
	raw, ok := object[field]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, fmt.Errorf("JWT key mapping response omitted required %s", field)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("JWT key mapping response returned invalid %s", field)
	}
	return value, nil
}

func requiredJSONInt(object map[string]json.RawMessage, field string) (int, error) {
	raw, ok := object[field]
	if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return 0, fmt.Errorf("JWT key mapping list response omitted required %s", field)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("JWT key mapping list response returned invalid %s", field)
	}
	value, err := strconv.Atoi(number.String())
	if err != nil || strconv.Itoa(value) != number.String() {
		return 0, fmt.Errorf("JWT key mapping list response returned non-integer %s", field)
	}
	return value, nil
}

func readJWTKeyMapping(ctx context.Context, client *Client, id string) (jwtKeyMappingObject, error) {
	var raw json.RawMessage
	if err := client.DoRequestWithResponse(ctx, http.MethodGet, jwtKeyMappingInfoEndpoint(id), nil, &raw); err != nil {
		return jwtKeyMappingObject{}, err
	}
	mapping, err := decodeJWTKeyMappingObject(raw)
	if err != nil {
		return jwtKeyMappingObject{}, err
	}
	if mapping.ID != id {
		return jwtKeyMappingObject{}, fmt.Errorf("JWT key mapping response identity did not match the requested UUID")
	}
	return mapping, nil
}

func decodeJWTKeyMappingListPage(raw json.RawMessage, requestedPage int) (numberedListPage[jwtKeyMappingObject], error) {
	var page numberedListPage[jwtKeyMappingObject]
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return page, fmt.Errorf("JWT key mapping list response must be a JSON object")
	}
	var object map[string]json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &object); err != nil || object == nil {
		return page, fmt.Errorf("JWT key mapping list response must be a valid JSON object")
	}
	allowed := map[string]struct{}{"mappings": {}, "total_count": {}, "current_page": {}, "total_pages": {}}
	for key := range object {
		if _, ok := allowed[key]; !ok {
			return page, fmt.Errorf("JWT key mapping list response contained unsupported field %q", key)
		}
	}
	itemsRaw, ok := object["mappings"]
	if !ok {
		return page, fmt.Errorf("JWT key mapping list response omitted required mappings")
	}
	rawItems, err := decodeNamedList(itemsRaw, jwtKeyMappingListPath, "mappings")
	if err != nil {
		return page, err
	}
	if len(rawItems) > 100 {
		return page, fmt.Errorf("JWT key mapping list response exceeded the requested page size")
	}
	page.Items = make([]jwtKeyMappingObject, 0, len(rawItems))
	for _, itemRaw := range rawItems {
		item, err := decodeJWTKeyMappingObject(itemRaw)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, item)
	}
	if page.TotalCount, err = requiredJSONInt(object, "total_count"); err != nil {
		return page, err
	}
	if page.Number, err = requiredJSONInt(object, "current_page"); err != nil {
		return page, err
	}
	if page.TotalPages, err = requiredJSONInt(object, "total_pages"); err != nil {
		return page, err
	}
	if page.Number != requestedPage {
		return page, fmt.Errorf("JWT key mapping list response did not honor the requested page")
	}
	expectedPages := 0
	if page.TotalCount > 0 {
		expectedPages = (page.TotalCount + 99) / 100
	}
	if page.TotalPages != expectedPages {
		return page, fmt.Errorf("JWT key mapping list response returned inconsistent total_pages")
	}
	return page, nil
}

func listJWTKeyMappings(ctx context.Context, client *Client) ([]jwtKeyMappingObject, error) {
	mappings, err := collectNumberedPages(ctx, jwtKeyMappingListPath, func(ctx context.Context, page int) (numberedListPage[jwtKeyMappingObject], error) {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("size", "100")
		var raw json.RawMessage
		if err := client.DoRequestWithResponse(ctx, http.MethodGet, endpointWithQuery(jwtKeyMappingListPath, query), nil, &raw); err != nil {
			return numberedListPage[jwtKeyMappingObject]{}, err
		}
		return decodeJWTKeyMappingListPage(raw, page)
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].ID < mappings[j].ID })
	return mappings, nil
}

func mappingMutationDiagnostic(action string, err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while attempting to %s the JWT key mapping. Response details were omitted because they may contain sensitive claim or key data.", apiErr.StatusCode, action)
	}
	return fmt.Sprintf("The JWT key mapping %s request failed. Error details were omitted because an intermediary may contain sensitive claim or key data.", action)
}
