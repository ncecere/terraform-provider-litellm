package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func newListTestClient(server *httptest.Server) *Client {
	return &Client{APIBase: server.URL, APIKey: "test-admin-key", HTTPClient: server.Client()}
}

func TestListKeysPaginatesFullObjectsStringsAndSorts(t *testing.T) {
	t.Parallel()

	stringHashUpper := strings.Repeat("A1", 32)
	stringHashCanonical := strings.ToLower(stringHashUpper)
	objectHashOne := strings.Repeat("f", 64)
	objectHashTwo := strings.Repeat("8", 64)
	unsafeKeyName := "friendly-name-raw-suffix-abcd"
	var mu sync.Mutex
	var requestedPages []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/key/list" {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		page, _ := strconv.Atoi(query.Get("page"))
		mu.Lock()
		requestedPages = append(requestedPages, page)
		mu.Unlock()
		if query.Get("size") != "100" || query.Get("return_full_object") != "true" {
			t.Errorf("pagination/full-object query = %q", request.URL.RawQuery)
		}
		if query.Get("sort_by") != "token" || query.Get("sort_order") != "asc" {
			t.Errorf("sort query = %q", request.URL.RawQuery)
		}
		if got := query.Get("team_id"); got != "team & one+/%" {
			t.Errorf("team_id = %q", got)
		}
		if got := query.Get("user_id"); got != "user?one=two" {
			t.Errorf("user_id = %q", got)
		}

		writer.Header().Set("Content-Type", "application/json")
		switch page {
		case 1:
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"keys": []interface{}{
					map[string]interface{}{"token": objectHashOne, "key_name": unsafeKeyName, "blocked": true},
					stringHashUpper,
				},
				"total_count":  3,
				"current_page": 1,
				"total_pages":  2,
			})
		case 2:
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"keys":         []interface{}{map[string]interface{}{"token": objectHashTwo, "team_id": "team & one+/%"}},
				"total_count":  3,
				"current_page": 2,
				"total_pages":  2,
			})
		default:
			t.Errorf("unexpected page %d", page)
		}
	}))
	defer server.Close()

	filters := url.Values{"team_id": {"team & one+/%"}, "user_id": {"user?one=two"}}
	keys, err := listKeys(context.Background(), newListTestClient(server), filters)
	if err != nil {
		t.Fatalf("listKeys() error = %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("len(keys) = %d, want 3", len(keys))
	}
	gotNames := []string{keys[0].KeyName.ValueString(), keys[1].KeyName.ValueString(), keys[2].KeyName.ValueString()}
	wantNames := []string{stringHashCanonical, objectHashOne, objectHashTwo}
	sort.Strings(wantNames)
	if strings.Join(gotNames, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("key hashes = %v, want %v", gotNames, wantNames)
	}
	for _, value := range gotNames {
		if value == stringHashUpper || value == unsafeKeyName || strings.Contains(value, "abcd") {
			t.Fatalf("key inventory did not canonicalize a safe identity: %q", value)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requestedPages) != 2 || requestedPages[0] != 1 || requestedPages[1] != 2 {
		t.Fatalf("requested pages = %v", requestedPages)
	}
}

func TestListKeysRepresentationSwitchKeepsCanonicalState(t *testing.T) {
	t.Parallel()

	canonicalHash := strings.Repeat("a1", 32)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		var key interface{} = strings.ToUpper(canonicalHash)
		if requests == 2 {
			key = map[string]interface{}{"token": canonicalHash}
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"keys": []interface{}{key}, "total_count": 1, "current_page": 1, "total_pages": 1,
		})
	}))
	defer server.Close()

	fromString, err := listKeys(context.Background(), newListTestClient(server), url.Values{})
	if err != nil {
		t.Fatalf("listKeys(string representation) error = %v", err)
	}
	fromObject, err := listKeys(context.Background(), newListTestClient(server), url.Values{})
	if err != nil {
		t.Fatalf("listKeys(object representation) error = %v", err)
	}
	if !reflect.DeepEqual(fromString, fromObject) {
		t.Fatalf("representation switch changed state:\nstring: %#v\nobject: %#v", fromString, fromObject)
	}
	if got := fromString[0].KeyName.ValueString(); got != canonicalHash {
		t.Fatalf("uppercase management hash canonicalized to %q, want %q", got, canonicalHash)
	}
}

func TestDecodeKeyListItemRejectsUnsafeValuesWithoutEchoingThem(t *testing.T) {
	t.Parallel()

	unexpectedStrings := []string{
		"sk-unexpected-token-suffix-abcd",
		"sk-...redacted-abcd",
		strings.Repeat("a", 63),
		strings.Repeat("g", 64),
		"",
	}
	for _, unexpected := range unexpectedStrings {
		unexpected := unexpected
		t.Run("string", func(t *testing.T) {
			_, err := decodeKeyListItem(json.RawMessage(strconv.Quote(unexpected)))
			if err == nil || !strings.Contains(err.Error(), "valid SHA256 management hash") {
				t.Fatalf("decodeKeyListItem(string) error = %v, want invalid-management-hash error", err)
			}
			if unexpected != "" && strings.Contains(err.Error(), unexpected) {
				t.Fatalf("key string error exposed response value: %v", err)
			}
			if strings.Contains(err.Error(), "abcd") {
				t.Fatalf("key string error exposed a suffix: %v", err)
			}
		})
	}

	unsafeKeyName := "customer-key-last-four-wxyz"
	unsafeToken := "sk-raw-token-value-abcd"
	unsafeBodyValue := "private-response-value"
	body := json.RawMessage(`{"token":"` + unsafeToken + `","key_name":"` + unsafeKeyName + `","private":"` + unsafeBodyValue + `"}`)
	_, err := decodeKeyListItem(body)
	if err == nil || !strings.Contains(err.Error(), "valid token management hash") {
		t.Fatalf("decodeKeyListItem(object) error = %v, want invalid-token-hash error", err)
	}
	for _, unsafe := range []string{unsafeKeyName, "wxyz", unsafeToken, "abcd", unsafeBodyValue} {
		if strings.Contains(err.Error(), unsafe) {
			t.Fatalf("key object error exposed response value: %v", err)
		}
	}
}

func TestDecodeKeyListItemCanonicalizesUppercaseObjectToken(t *testing.T) {
	t.Parallel()

	upper := strings.Repeat("BC", 32)
	item, err := decodeKeyListItem(json.RawMessage(`{"token":"` + upper + `","key_name":"unsafe-suffix-abcd"}`))
	if err != nil {
		t.Fatalf("decodeKeyListItem(object) error = %v", err)
	}
	if got, want := item.KeyName.ValueString(), strings.ToLower(upper); got != want {
		t.Fatalf("object token = %q, want canonical %q", got, want)
	}
}

func TestListKeysEmptyPage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"keys":         []interface{}{},
			"total_count":  0,
			"current_page": 1,
			"total_pages":  0,
		})
	}))
	defer server.Close()

	keys, err := listKeys(context.Background(), newListTestClient(server), url.Values{})
	if err != nil {
		t.Fatalf("listKeys() error = %v", err)
	}
	if keys == nil || len(keys) != 0 {
		t.Fatalf("keys = %#v, want non-nil empty list", keys)
	}
}

func TestListKeysRejectsMalformedAndRepeatedPages(t *testing.T) {
	t.Parallel()

	t.Run("missing keys", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, _ = writer.Write([]byte(`{"total_count":0,"current_page":1,"total_pages":0}`))
		}))
		defer server.Close()
		if _, err := listKeys(context.Background(), newListTestClient(server), url.Values{}); err == nil {
			t.Fatal("listKeys() accepted missing keys field")
		}
	})

	t.Run("repeated", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			page, _ := strconv.Atoi(request.URL.Query().Get("page"))
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"keys":         []interface{}{map[string]interface{}{"token": strings.Repeat("a", 64)}},
				"total_count":  2,
				"current_page": page,
				"total_pages":  2,
			})
		}))
		defer server.Close()
		if _, err := listKeys(context.Background(), newListTestClient(server), url.Values{}); err == nil || !strings.Contains(err.Error(), "repeated") {
			t.Fatalf("listKeys() error = %v, want repeated-page error", err)
		}
	})
}

func TestListKeysRetriesConcurrentCountShiftFromPageOne(t *testing.T) {
	t.Parallel()

	attempt := 0
	var mu sync.Mutex
	var requestedPages []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		mu.Lock()
		defer mu.Unlock()
		requestedPages = append(requestedPages, page)
		if page == 1 {
			attempt++
		}
		total := 2
		if attempt == 1 && page == 2 {
			total = 3 // Simulate an insert between the page query and count.
		}
		token := strings.Repeat("1", 64)
		if page == 2 {
			token = strings.Repeat("2", 64)
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"keys":         []interface{}{map[string]interface{}{"token": token}},
			"total_count":  total,
			"current_page": page,
			"total_pages":  2,
		})
	}))
	defer server.Close()

	keys, err := listKeys(context.Background(), newListTestClient(server), url.Values{})
	if err != nil {
		t.Fatalf("listKeys() error = %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("len(keys) = %d, want stable two-item result", len(keys))
	}
	mu.Lock()
	defer mu.Unlock()
	if got, want := fmt.Sprint(requestedPages), "[1 2 1 2]"; got != want {
		t.Fatalf("requested pages = %s, want restart %s", got, want)
	}
}

func TestListUsersUsesRoleAliasPaginatesAndSorts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		if request.URL.Path != "/user/list" {
			http.NotFound(writer, request)
			return
		}
		if query.Get("user_role") != "" {
			t.Errorf("ignored user_role alias was sent: %q", request.URL.RawQuery)
		}
		if got := query.Get("role"); got != "proxy admin&viewer+/%" {
			t.Errorf("role = %q", got)
		}
		if query.Get("page_size") != "100" {
			t.Errorf("page_size = %q", query.Get("page_size"))
		}
		page, _ := strconv.Atoi(query.Get("page"))
		users := []interface{}{map[string]interface{}{"user_id": "z-user"}}
		if page == 2 {
			users = []interface{}{map[string]interface{}{"user_id": "a-user"}}
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users":       users,
			"total":       2,
			"page":        page,
			"page_size":   100,
			"total_pages": 2,
		})
	}))
	defer server.Close()

	filters := url.Values{"role": {"proxy admin&viewer+/%"}}
	users, err := listUsers(context.Background(), newListTestClient(server), filters)
	if err != nil {
		t.Fatalf("listUsers() error = %v", err)
	}
	if len(users) != 2 || users[0].UserID.ValueString() != "a-user" || users[1].UserID.ValueString() != "z-user" {
		t.Fatalf("users = %#v", users)
	}
}

func TestListUsersPersistentDeleteShiftExhaustsBoundedRetries(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var requestedPages []int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		page, _ := strconv.Atoi(request.URL.Query().Get("page"))
		mu.Lock()
		defer mu.Unlock()
		requestedPages = append(requestedPages, page)
		users := []interface{}{map[string]interface{}{"user_id": "user-a"}}
		if page == 2 {
			users = []interface{}{} // Simulate a delete shifting the final page empty.
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"users": users, "total": 2, "page": page, "page_size": 100, "total_pages": 2,
		})
	}))
	defer server.Close()

	_, err := listUsers(context.Background(), newListTestClient(server), url.Values{})
	if err == nil || !strings.Contains(err.Error(), "3 bounded attempts") {
		t.Fatalf("listUsers() error = %v, want bounded churn exhaustion", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got, want := fmt.Sprint(requestedPages), "[1 2 1 2 1 2]"; got != want {
		t.Fatalf("requested pages = %s, want retries from page 1: %s", got, want)
	}
}

func TestListUsersRejectsDefaultLimitAndMalformedItems(t *testing.T) {
	t.Parallel()

	t.Run("server ignored requested maximum", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"users": []interface{}{}, "total": 0, "page": 1, "page_size": 25, "total_pages": 0,
			})
		}))
		defer server.Close()
		if _, err := listUsers(context.Background(), newListTestClient(server), url.Values{}); err == nil || !strings.Contains(err.Error(), "page_size") {
			t.Fatalf("listUsers() error = %v", err)
		}
	})

	t.Run("non-object item", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests++
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"users": []interface{}{"not-an-object"}, "total": 1, "page": 1, "page_size": 100, "total_pages": 1,
			})
		}))
		defer server.Close()
		if _, err := listUsers(context.Background(), newListTestClient(server), url.Values{}); err == nil {
			t.Fatal("listUsers() accepted non-object user item")
		}
		if requests != 1 {
			t.Fatalf("semantic malformed item was retried %d times", requests)
		}
	})
}

func TestFetchEnvelopeListObjectsUsesSingleSnapshot(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		_, _ = writer.Write([]byte(`{"search_tools":[{"search_tool_name":"one"}]}`))
	}))
	defer server.Close()

	items, err := fetchEnvelopeListObjects(context.Background(), newListTestClient(server), "/search_tools/list", "search_tools", "search tool item")
	if err != nil {
		t.Fatalf("fetchEnvelopeListObjects() error = %v", err)
	}
	if requests != 1 || len(items) != 1 {
		t.Fatalf("requests = %d, items = %#v", requests, items)
	}
}
