package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSendInviteEmailSchemasAreCreateOnlyWriteOnly(t *testing.T) {
	t.Parallel()

	for name, resourceUnderTest := range map[string]resource.Resource{
		"key":  &KeyResource{},
		"user": &UserResource{},
	} {
		t.Run(name, func(t *testing.T) {
			var response resource.SchemaResponse
			resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %v", response.Diagnostics)
			}
			attribute, ok := response.Schema.Attributes["send_invite_email"]
			if !ok {
				t.Fatal("send_invite_email schema attribute is missing")
			}
			if !attribute.IsOptional() || !attribute.IsWriteOnly() || attribute.IsComputed() {
				t.Fatalf("send_invite_email must be Optional+WriteOnly and never Computed: %#v", attribute)
			}
		})
	}
}

func TestAddSendInviteEmailToCreateRequest(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		value       types.Bool
		wantPresent bool
		wantValue   bool
		wantError   bool
	}{
		"omitted": {value: types.BoolNull()},
		"false":   {value: types.BoolValue(false), wantPresent: true},
		"true":    {value: types.BoolValue(true), wantPresent: true, wantValue: true},
		"unknown": {value: types.BoolUnknown(), wantError: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			request := map[string]interface{}{"unchanged": true}
			err := addSendInviteEmailToCreateRequest(request, test.value)
			if test.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				if _, present := request["send_invite_email"]; present {
					t.Fatalf("invalid value mutated request: %#v", request)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			value, present := request["send_invite_email"]
			if present != test.wantPresent {
				t.Fatalf("presence = %t, want %t: %#v", present, test.wantPresent, request)
			}
			if present && value != test.wantValue {
				t.Fatalf("value = %#v, want %t", value, test.wantValue)
			}
		})
	}
}

func TestSendInviteEmailRecipientValidation(t *testing.T) {
	t.Parallel()

	trueValue := types.BoolValue(true)
	falseValue := types.BoolValue(false)
	validKey := &KeyResourceModel{UserID: types.StringValue("user-123")}
	if err := validateKeySendInviteEmail(validKey, trueValue); err != nil {
		t.Fatalf("valid key invitation: %v", err)
	}
	if err := validateKeySendInviteEmail(&KeyResourceModel{}, trueValue); err == nil {
		t.Fatal("key invitation without user_id must fail")
	}
	if err := validateKeySendInviteEmail(&KeyResourceModel{
		UserID:           types.StringValue("user-123"),
		ServiceAccountID: types.StringValue("service-account"),
	}, trueValue); err == nil {
		t.Fatal("service-account key invitation must fail")
	}
	if err := validateKeySendInviteEmail(&KeyResourceModel{
		UserID:           types.StringValue("user-123"),
		ServiceAccountID: types.StringUnknown(),
	}, trueValue); err == nil {
		t.Fatal("unknown service_account_id must fail when invitation is requested")
	}
	if err := validateKeySendInviteEmail(&KeyResourceModel{}, falseValue); err != nil {
		t.Fatalf("false key invitation should not require a recipient: %v", err)
	}

	if err := validateUserSendInviteEmail(&UserResourceModel{UserEmail: types.StringValue("person@example.com")}, trueValue); err != nil {
		t.Fatalf("valid user invitation: %v", err)
	}
	if err := validateUserSendInviteEmail(&UserResourceModel{}, trueValue); err == nil {
		t.Fatal("user invitation without user_email must fail")
	}
	if err := validateUserSendInviteEmail(&UserResourceModel{UserEmail: types.StringValue("not-an-email")}, trueValue); err == nil {
		t.Fatal("user invitation with malformed user_email must fail")
	}
	if err := validateUserSendInviteEmail(&UserResourceModel{}, falseValue); err != nil {
		t.Fatalf("false user invitation should not require email: %v", err)
	}
}

func TestKeyInviteRecipientPreflight(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		responseStatus int
		response       map[string]interface{}
		wantError      string
	}{
		"exact user with email": {
			responseStatus: http.StatusOK,
			response: map[string]interface{}{"user_info": map[string]interface{}{
				"user_id":    "user/id #1",
				"user_email": "person@example.com",
			}},
		},
		"missing email": {
			responseStatus: http.StatusOK,
			response: map[string]interface{}{"user_info": map[string]interface{}{
				"user_id": "user/id #1",
			}},
			wantError: "non-empty user_email",
		},
		"mismatched identity": {
			responseStatus: http.StatusOK,
			response: map[string]interface{}{"user_info": map[string]interface{}{
				"user_id":    "other-user",
				"user_email": "person@example.com",
			}},
			wantError: "exact configured user_id",
		},
		"lookup not found": {
			responseStatus: http.StatusNotFound,
			response:       map[string]interface{}{"detail": "user/id #1 must not be echoed"},
			wantError:      "HTTP 404",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/user/info" || request.URL.Query().Get("user_id") != "user/id #1" {
					http.Error(writer, "unexpected recipient lookup", http.StatusBadRequest)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.responseStatus)
				_ = json.NewEncoder(writer).Encode(test.response)
			}))
			defer server.Close()

			keyResource := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
			err := keyResource.validateKeyInviteRecipient(context.Background(), &KeyResourceModel{
				UserID: types.StringValue("user/id #1"),
			}, types.BoolValue(true))
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected preflight error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v, want substring %q", err, test.wantError)
			}
			if strings.Contains(err.Error(), "must not be echoed") {
				t.Fatalf("preflight diagnostic exposed API response body: %v", err)
			}
		})
	}
}

func TestUpdateRequestBuildersNeverIncludeSendInviteEmail(t *testing.T) {
	t.Parallel()

	keyRequest, err := (&KeyResource{}).buildKeyRequest(context.Background(), &KeyResourceModel{
		SendInviteEmail: types.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("build key request: %v", err)
	}
	if _, present := keyRequest["send_invite_email"]; present {
		t.Fatalf("key update-capable request builder included create-only action: %#v", keyRequest)
	}

	userRequest, diagnostics := (&UserResource{}).buildUserRequest(context.Background(), &UserResourceModel{
		SendInviteEmail: types.BoolValue(true),
	})
	if diagnostics.HasError() {
		t.Fatalf("build user request: %v", diagnostics)
	}
	if _, present := userRequest["send_invite_email"]; present {
		t.Fatalf("user update-capable request builder included create-only action: %#v", userRequest)
	}
}

func TestExistingUserWithInviteMustNotBeAdoptedAsInvited(t *testing.T) {
	t.Parallel()

	if !sendInviteEmailRequested(types.BoolValue(true)) {
		t.Fatal("true create action must trigger existing-user rejection")
	}
	for _, value := range []types.Bool{types.BoolNull(), types.BoolValue(false), types.BoolUnknown()} {
		if sendInviteEmailRequested(value) {
			t.Fatalf("non-true value incorrectly triggers invitation semantics: %#v", value)
		}
	}
}
