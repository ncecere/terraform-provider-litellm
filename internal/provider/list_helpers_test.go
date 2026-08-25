package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCollectNumberedPagesMultipleAndPartialFinalPage(t *testing.T) {
	t.Parallel()

	pages := map[int]numberedListPage[int]{
		1: {Items: []int{4, 2}, Number: 1, TotalPages: 2, TotalCount: 3},
		2: {Items: []int{1}, Number: 2, TotalPages: 2, TotalCount: 3},
	}
	items, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[int], error) {
		return pages[page], nil
	})
	if err != nil {
		t.Fatalf("collectNumberedPages() error = %v", err)
	}
	if got, want := len(items), 3; got != want {
		t.Fatalf("len(items) = %d, want %d", got, want)
	}
}

func TestCollectNumberedPagesEmptyResult(t *testing.T) {
	t.Parallel()

	items, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[string], error) {
		return numberedListPage[string]{Items: []string{}, Number: page, TotalPages: 0, TotalCount: 0}, nil
	})
	if err != nil {
		t.Fatalf("collectNumberedPages() error = %v", err)
	}
	if items == nil || len(items) != 0 {
		t.Fatalf("items = %#v, want non-nil empty list", items)
	}
}

func TestCollectNumberedPagesRejectsRepeatedPage(t *testing.T) {
	t.Parallel()

	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[string], error) {
		return numberedListPage[string]{Items: []string{"same"}, Number: page, TotalPages: 2, TotalCount: 2}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("error = %v, want repeated-page error", err)
	}
}

func TestCollectNumberedPagesRejectsSilentTruncation(t *testing.T) {
	t.Parallel()

	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[int], error) {
		return numberedListPage[int]{Items: []int{1}, Number: page, TotalPages: 1, TotalCount: 2}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "declared 2") {
		t.Fatalf("error = %v, want declared-count mismatch", err)
	}
}

func TestCollectNumberedPagesRejectsEmptyIntermediatePage(t *testing.T) {
	t.Parallel()

	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[int], error) {
		return numberedListPage[int]{Items: []int{}, Number: page, TotalPages: 2, TotalCount: 1}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "empty page") {
		t.Fatalf("error = %v, want empty-page error", err)
	}
}

func TestCollectNumberedPagesRejectsEndpointBeyondBound(t *testing.T) {
	t.Parallel()

	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[int], error) {
		return numberedListPage[int]{Items: []int{1}, Number: page, TotalPages: maxListPages + 1, TotalCount: maxListPages + 1}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("error = %v, want bounded-pagination error", err)
	}
}

func TestCollectNumberedPagesRestartsAfterConcurrentCountAndRowShifts(t *testing.T) {
	t.Parallel()

	attempt := 0
	var requestedPages []int
	items, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[string], error) {
		requestedPages = append(requestedPages, page)
		if page == 1 {
			attempt++
		}
		switch attempt {
		case 1: // Concurrent insert changes the count between page queries.
			if page == 1 {
				return numberedListPage[string]{Items: []string{"a"}, Number: 1, TotalPages: 2, TotalCount: 2}, nil
			}
			return numberedListPage[string]{Items: []string{"b"}, Number: 2, TotalPages: 2, TotalCount: 3}, nil
		case 2: // Concurrent delete shifts the first row onto both pages.
			return numberedListPage[string]{Items: []string{"a"}, Number: page, TotalPages: 2, TotalCount: 2}, nil
		default:
			value := "a"
			if page == 2 {
				value = "b"
			}
			return numberedListPage[string]{Items: []string{value}, Number: page, TotalPages: 2, TotalCount: 2}, nil
		}
	})
	if err != nil {
		t.Fatalf("collectNumberedPages() error = %v", err)
	}
	if got := strings.Join(items, ","); got != "a,b" {
		t.Fatalf("items = %q, want stable complete result", got)
	}
	if got, want := fmt.Sprint(requestedPages), "[1 2 1 2 1 2]"; got != want {
		t.Fatalf("requested pages = %s, want %s", got, want)
	}
}

func TestCollectNumberedPagesPersistentChurnExhaustsBoundedAttempts(t *testing.T) {
	t.Parallel()

	var requestedPages []int
	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, page int) (numberedListPage[string], error) {
		requestedPages = append(requestedPages, page)
		items := []string{"row"}
		if page == 2 {
			items = []string{}
		}
		return numberedListPage[string]{Items: items, Number: page, TotalPages: 2, TotalCount: 2}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "3 bounded attempts") || !strings.Contains(err.Error(), "shifted") {
		t.Fatalf("error = %v, want bounded persistent-churn error", err)
	}
	if got, want := len(requestedPages), maxListAttempts*2; got != want {
		t.Fatalf("request count = %d, want %d (%v)", got, want, requestedPages)
	}
	for index, page := range requestedPages {
		if want := index%2 + 1; page != want {
			t.Fatalf("requested pages = %v, attempt did not restart at page 1", requestedPages)
		}
	}
}

func TestCollectNumberedPagesDoesNotRetrySemanticErrors(t *testing.T) {
	t.Parallel()

	requests := 0
	semanticErr := fmt.Errorf("malformed item")
	_, err := collectNumberedPages(context.Background(), "/test/list", func(_ context.Context, _ int) (numberedListPage[string], error) {
		requests++
		return numberedListPage[string]{}, semanticErr
	})
	if err != semanticErr {
		t.Fatalf("error = %v, want semantic error", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one request without retry", requests)
	}
}

func TestEndpointWithQueryEscapesAndCanonicalizesValues(t *testing.T) {
	t.Parallel()

	values := url.Values{}
	values.Set("role", "admin & viewer+/%")
	values.Set("teamId", "team?one=two")
	endpoint := endpointWithQuery("/test/list", values)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("url.Parse() error = %v", err)
	}
	if got := parsed.Query().Get("role"); got != "admin & viewer+/%" {
		t.Fatalf("decoded role = %q", got)
	}
	if got := parsed.Query().Get("teamId"); got != "team?one=two" {
		t.Fatalf("decoded teamId = %q", got)
	}
	if strings.Contains(parsed.RawQuery, "admin &") || strings.Contains(parsed.RawQuery, "team?") {
		t.Fatalf("query was not escaped: %q", parsed.RawQuery)
	}
}

func TestSafeListDiagnosticRedactsCanonicalFilterValues(t *testing.T) {
	t.Parallel()

	secretFilter := "secret-looking/filter&value"
	diagnostic := safeListDiagnostic(fmt.Errorf("server rejected %s", secretFilter), url.Values{"role": {secretFilter}})
	if strings.Contains(diagnostic, secretFilter) {
		t.Fatalf("diagnostic exposed filter value: %q", diagnostic)
	}
}

func TestCanonicalFilterAliases(t *testing.T) {
	t.Parallel()

	if filters := userListFilters(types.StringValue("proxy_admin")); filters.Get("role") != "proxy_admin" || filters.Get("user_role") != "" {
		t.Fatalf("user filters = %#v, want role only", filters)
	}
	if filters := modelListFilters(types.StringValue("team & one")); filters.Get("teamId") != "team & one" || filters.Get("team_id") != "" {
		t.Fatalf("model filters = %#v, want teamId only", filters)
	}
	if filters := keyListFilters(types.StringValue("team"), types.StringValue("user")); filters.Get("team_id") != "team" || filters.Get("user_id") != "user" {
		t.Fatalf("key filters = %#v", filters)
	}
	if filters := teamListFilters(types.StringValue("org & one")); filters.Get("organization_id") != "org & one" {
		t.Fatalf("team filters = %#v", filters)
	}
	if filters := organizationListFilters(types.StringValue("alias + one")); filters.Get("org_alias") != "alias + one" {
		t.Fatalf("organization filters = %#v", filters)
	}
	if filters := userListFilters(types.StringUnknown()); len(filters) != 0 {
		t.Fatalf("unknown user filter was included: %#v", filters)
	}
	if filters := modelListFilters(types.StringNull()); len(filters) != 0 {
		t.Fatalf("null model filter was included: %#v", filters)
	}
}

func TestAddKnownStringFilterNullUnknownAndKnown(t *testing.T) {
	t.Parallel()

	values := url.Values{}
	addKnownStringFilter(values, "role", types.StringNull())
	addKnownStringFilter(values, "teamId", types.StringUnknown())
	if len(values) != 0 {
		t.Fatalf("null/unknown filters were included: %#v", values)
	}
	addKnownStringFilter(values, "role", types.StringValue("proxy_admin"))
	if got := values.Get("role"); got != "proxy_admin" {
		t.Fatalf("known filter = %q", got)
	}
}

func TestNestedListObjectUsesCanonicalBudgetRelation(t *testing.T) {
	t.Parallel()

	item := map[string]interface{}{
		"max_budget":           1.0,
		"litellm_budget_table": map[string]interface{}{"max_budget": 2.0, "budget_id": "budget-1"},
	}
	budget := nestedListObject(item, "litellm_budget_table")
	if budget["max_budget"] != 2.0 || budget["budget_id"] != "budget-1" {
		t.Fatalf("nested budget = %#v", budget)
	}
	legacy := map[string]interface{}{"max_budget": 3.0}
	if got := nestedListObject(legacy, "litellm_budget_table")["max_budget"]; got != 3.0 {
		t.Fatalf("legacy flat budget = %#v", got)
	}
}

func TestExactListShapeDecoders(t *testing.T) {
	t.Parallel()

	if items, err := decodeTopLevelList(json.RawMessage(`[]`), "/array"); err != nil || items == nil {
		t.Fatalf("decodeTopLevelList(empty) = %#v, %v", items, err)
	}
	if _, err := decodeTopLevelList(json.RawMessage(`null`), "/array"); err == nil {
		t.Fatal("decodeTopLevelList accepted null")
	}
	if _, err := decodeEnvelopeList(json.RawMessage(`{"data":[]}`), "/envelope", "items"); err == nil {
		t.Fatal("decodeEnvelopeList accepted missing required field")
	}
	if _, err := decodeEnvelopeList(json.RawMessage(`{"items":{}}`), "/envelope", "items"); err == nil {
		t.Fatal("decodeEnvelopeList accepted non-array field")
	}
}

func TestModelInfoDataNormalizesArrayAndUserModelObject(t *testing.T) {
	t.Parallel()

	arrayItems, err := decodeEnvelopeListOrObject(json.RawMessage(`{"data":[{"model_name":"one"}]}`), "/model/info", "data")
	if err != nil || len(arrayItems) != 1 {
		t.Fatalf("array data = %#v, %v", arrayItems, err)
	}
	objectItems, err := decodeEnvelopeListOrObject(json.RawMessage(`{"data":{"model_name":"one"}}`), "/model/info", "data")
	if err != nil || len(objectItems) != 1 {
		t.Fatalf("object data = %#v, %v", objectItems, err)
	}
}

func TestModelInfoDataRejectsOtherShapesWithoutEchoingValues(t *testing.T) {
	t.Parallel()

	secret := "sk-model-response-secret"
	_, err := decodeEnvelopeListOrObject(json.RawMessage(`{"data":"`+secret+`"}`), "/model/info", "data")
	if err == nil {
		t.Fatal("decodeEnvelopeListOrObject accepted string data")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("shape error exposed response value: %v", err)
	}
}

func TestShapeErrorsDoNotEchoResponseValues(t *testing.T) {
	t.Parallel()

	secret := "sk-response-secret-value"
	_, err := decodeEnvelopeList(json.RawMessage(`{"unexpected":"`+secret+`"}`), "/safe/list", "items")
	if err == nil {
		t.Fatal("decodeEnvelopeList accepted malformed envelope")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("shape error exposed response value: %v", err)
	}
}
