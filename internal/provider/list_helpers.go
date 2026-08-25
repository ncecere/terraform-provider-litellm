package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
)

const (
	maxListPages    = 1000
	maxListAttempts = 3
)

type numberedListPage[T any] struct {
	Items      []T
	Number     int
	TotalPages int
	TotalCount int
}

// listChurnError identifies inconsistencies that can be caused by LiteLLM's
// non-transactional page query and count operations. Only these errors are
// retried; malformed metadata, malformed items, and request failures remain
// immediately actionable.
type listChurnError struct {
	message string
}

func (e *listChurnError) Error() string {
	return e.message
}

func listChurnErrorf(format string, args ...interface{}) error {
	return &listChurnError{message: fmt.Sprintf(format, args...)}
}

// collectNumberedPages exhausts a 1-based page endpoint while enforcing the
// endpoint's page metadata. A detected concurrent insert/delete/count shift
// restarts the entire bounded listing at page 1. The hard page and attempt
// bounds prevent infinite loops and partial Terraform inventory state.
func collectNumberedPages[T any](
	ctx context.Context,
	endpoint string,
	fetch func(context.Context, int) (numberedListPage[T], error),
) ([]T, error) {
	var lastChurn error
	for attempt := 1; attempt <= maxListAttempts; attempt++ {
		items, err := collectNumberedPagesAttempt(ctx, endpoint, fetch)
		if err == nil {
			return items, nil
		}
		var churn *listChurnError
		if !errors.As(err, &churn) {
			return nil, err
		}
		lastChurn = err
	}
	return nil, fmt.Errorf("%s did not return a stable result after %d bounded attempts: %w", endpoint, maxListAttempts, lastChurn)
}

func collectNumberedPagesAttempt[T any](
	ctx context.Context,
	endpoint string,
	fetch func(context.Context, int) (numberedListPage[T], error),
) ([]T, error) {
	var all []T
	seenPages := make(map[[sha256.Size]byte]int)
	seenItems := make(map[[sha256.Size]byte]int)
	expectedTotalPages := -1
	expectedTotalCount := -1

	for requestedPage := 1; requestedPage <= maxListPages; requestedPage++ {
		page, err := fetch(ctx, requestedPage)
		if err != nil {
			return nil, err
		}
		if page.Number != requestedPage {
			return nil, fmt.Errorf("%s returned page %d while page %d was requested", endpoint, page.Number, requestedPage)
		}
		if page.TotalPages < 0 || page.TotalCount < 0 {
			return nil, fmt.Errorf("%s returned invalid pagination metadata", endpoint)
		}
		if page.TotalPages > maxListPages {
			return nil, fmt.Errorf("%s requires %d pages, exceeding the provider safety limit of %d", endpoint, page.TotalPages, maxListPages)
		}
		if expectedTotalPages == -1 {
			expectedTotalPages = page.TotalPages
			expectedTotalCount = page.TotalCount
		} else if page.TotalPages != expectedTotalPages || page.TotalCount != expectedTotalCount {
			return nil, listChurnErrorf("%s pagination metadata changed while listing results", endpoint)
		}

		if page.TotalPages == 0 {
			if requestedPage != 1 || len(page.Items) != 0 || page.TotalCount != 0 {
				return nil, listChurnErrorf("%s returned inconsistent empty-page metadata", endpoint)
			}
			return []T{}, nil
		}
		if requestedPage > page.TotalPages {
			return nil, listChurnErrorf("%s total page metadata shifted before the result set was complete", endpoint)
		}
		if len(page.Items) == 0 {
			return nil, listChurnErrorf("%s returned an empty page after the result set shifted", endpoint)
		}

		pageFingerprint, itemFingerprints, err := listPageFingerprints(page.Items)
		if err != nil {
			return nil, fmt.Errorf("%s returned a page the provider could not validate", endpoint)
		}
		if previousPage, ok := seenPages[pageFingerprint]; ok {
			return nil, listChurnErrorf("%s repeated the contents of page %d on page %d", endpoint, previousPage, requestedPage)
		}
		seenPages[pageFingerprint] = requestedPage
		for _, fingerprint := range itemFingerprints {
			if previousPage, ok := seenItems[fingerprint]; ok {
				return nil, listChurnErrorf("%s repeated an item from page %d on page %d", endpoint, previousPage, requestedPage)
			}
			seenItems[fingerprint] = requestedPage
		}
		all = append(all, page.Items...)
		if len(all) > expectedTotalCount {
			return nil, listChurnErrorf("%s returned more results than its declared count", endpoint)
		}

		if requestedPage == page.TotalPages {
			if len(all) != page.TotalCount {
				return nil, listChurnErrorf("%s returned %d results but declared %d", endpoint, len(all), page.TotalCount)
			}
			return all, nil
		}
	}

	return nil, fmt.Errorf("%s exceeded the provider safety limit of %d pages", endpoint, maxListPages)
}

type listItemIdentity interface {
	listItemIdentity() string
}

func listPageFingerprints[T any](items []T) ([sha256.Size]byte, [][sha256.Size]byte, error) {
	encodedItems := make([]string, 0, len(items))
	itemFingerprints := make([][sha256.Size]byte, 0, len(items))
	for _, item := range items {
		var encoded []byte
		var err error
		if identified, ok := any(item).(listItemIdentity); ok {
			encoded, err = json.Marshal(identified.listItemIdentity())
		} else {
			encoded, err = json.Marshal(item)
		}
		if err != nil {
			return [sha256.Size]byte{}, nil, err
		}
		encodedItems = append(encodedItems, string(encoded))
		itemFingerprints = append(itemFingerprints, sha256.Sum256(encoded))
	}
	// Page order is intentionally ignored so a broken endpoint cannot evade
	// the repeated-page guard merely by shuffling the same rows.
	sort.Strings(encodedItems)
	encoded, err := json.Marshal(encodedItems)
	if err != nil {
		return [sha256.Size]byte{}, nil, err
	}
	return sha256.Sum256(encoded), itemFingerprints, nil
}

func endpointWithQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, entries := range values {
		cloned[key] = append([]string(nil), entries...)
	}
	return cloned
}

// safeListDiagnostic prevents an API error that echoes a query filter from
// copying that value into Terraform diagnostics.
func safeListDiagnostic(err error, filters url.Values) string {
	if err == nil {
		return ""
	}
	secrets := make([]string, 0, len(filters))
	for _, values := range filters {
		secrets = append(secrets, values...)
	}
	return sanitizeDiagnosticString(err.Error(), secrets)
}

func addKnownStringFilter(values url.Values, name string, value interface {
	IsNull() bool
	IsUnknown() bool
	ValueString() string
}) {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return
	}
	values.Set(name, value.ValueString())
}

func decodeTopLevelList(raw json.RawMessage, endpoint string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s returned an unsupported response shape; expected a JSON array", endpoint)
	}
	var items []json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &items); err != nil {
		return nil, fmt.Errorf("%s returned a malformed JSON array", endpoint)
	}
	if items == nil {
		return nil, fmt.Errorf("%s returned a null result array", endpoint)
	}
	return items, nil
}

func decodeEnvelopeList(raw json.RawMessage, endpoint, field string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s returned an unsupported response shape; expected a JSON object", endpoint)
	}
	var envelope map[string]json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("%s returned a malformed JSON object", endpoint)
	}
	itemsRaw, ok := envelope[field]
	if !ok {
		return nil, fmt.Errorf("%s response omitted required %q field", endpoint, field)
	}
	return decodeNamedList(itemsRaw, endpoint, field)
}

func decodeNamedList(raw json.RawMessage, endpoint, field string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' {
		return nil, fmt.Errorf("%s returned an invalid %q field; expected a JSON array", endpoint, field)
	}
	var items []json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &items); err != nil {
		return nil, fmt.Errorf("%s returned a malformed %q array", endpoint, field)
	}
	if items == nil {
		return nil, fmt.Errorf("%s returned a null %q array", endpoint, field)
	}
	return items, nil
}

// decodeEnvelopeListOrObject accepts /model/info's two documented user modes:
// a list in the normal mode or one model object in user_model mode.
func decodeEnvelopeListOrObject(raw json.RawMessage, endpoint, field string) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s returned an unsupported response shape; expected a JSON object", endpoint)
	}
	var envelope map[string]json.RawMessage
	if err := decodeJSONUseNumber(trimmed, &envelope); err != nil {
		return nil, fmt.Errorf("%s returned a malformed JSON object", endpoint)
	}
	value, ok := envelope[field]
	if !ok {
		return nil, fmt.Errorf("%s response omitted required %q field", endpoint, field)
	}
	value = bytes.TrimSpace(value)
	if len(value) == 0 || bytes.Equal(value, []byte("null")) {
		return nil, fmt.Errorf("%s returned an invalid %q field; expected a JSON array or object", endpoint, field)
	}
	switch value[0] {
	case '[':
		return decodeNamedList(value, endpoint, field)
	case '{':
		if _, err := decodeListObject(value, endpoint, "model item"); err != nil {
			return nil, err
		}
		return []json.RawMessage{value}, nil
	default:
		return nil, fmt.Errorf("%s returned an invalid %q field; expected a JSON array or object", endpoint, field)
	}
}

func fetchTopLevelListObjects(ctx context.Context, client *Client, endpoint, itemName string) ([]map[string]interface{}, error) {
	var raw json.RawMessage
	if err := client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &raw); err != nil {
		return nil, err
	}
	rawItems, err := decodeTopLevelList(raw, endpoint)
	if err != nil {
		return nil, err
	}
	return decodeListObjects(rawItems, endpoint, itemName)
}

func fetchEnvelopeListObjects(ctx context.Context, client *Client, endpoint, field, itemName string) ([]map[string]interface{}, error) {
	var raw json.RawMessage
	if err := client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &raw); err != nil {
		return nil, err
	}
	rawItems, err := decodeEnvelopeList(raw, endpoint, field)
	if err != nil {
		return nil, err
	}
	return decodeListObjects(rawItems, endpoint, itemName)
}

func decodeListObjects(rawItems []json.RawMessage, endpoint, itemName string) ([]map[string]interface{}, error) {
	items := make([]map[string]interface{}, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, err := decodeListObject(rawItem, endpoint, itemName)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func nestedListObject(item map[string]interface{}, field string) map[string]interface{} {
	if nested, ok := item[field].(map[string]interface{}); ok && nested != nil {
		return nested
	}
	return item
}

func decodeListObject(raw json.RawMessage, endpoint, itemName string) (map[string]interface{}, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s returned an invalid %s; expected a JSON object", endpoint, itemName)
	}
	var item map[string]interface{}
	if err := decodeJSONUseNumber(trimmed, &item); err != nil || item == nil {
		return nil, fmt.Errorf("%s returned a malformed %s object", endpoint, itemName)
	}
	return item, nil
}
