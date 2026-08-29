package provider

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// addSendInviteEmailToCreateRequest handles LiteLLM's create-only invitation
// action flag. It must never be called by Update because v1.98.0 silently
// ignores the field there and invitation delivery has no persistent state.
func addSendInviteEmailToCreateRequest(request map[string]interface{}, value types.Bool) error {
	if value.IsUnknown() {
		return fmt.Errorf("send_invite_email must be known during apply")
	}
	if value.IsNull() {
		return nil
	}
	request["send_invite_email"] = value.ValueBool()
	return nil
}

func sendInviteEmailRequested(value types.Bool) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueBool()
}

func validateKeySendInviteEmail(data *KeyResourceModel, value types.Bool) error {
	if !sendInviteEmailRequested(value) {
		return nil
	}
	if data.ServiceAccountID.IsUnknown() {
		return fmt.Errorf("send_invite_email requires service_account_id to be known during apply")
	}
	if !data.ServiceAccountID.IsNull() && data.ServiceAccountID.ValueString() != "" {
		return fmt.Errorf("send_invite_email cannot be true for a service-account key because LiteLLM ignores user_id on that create endpoint")
	}
	if data.UserID.IsNull() || data.UserID.IsUnknown() || data.UserID.ValueString() == "" {
		return fmt.Errorf("send_invite_email requires user_id so LiteLLM can resolve a recipient email address")
	}
	return nil
}

func validateInviteEmailAddress(value string) error {
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return fmt.Errorf("must be a valid email address without a display name")
	}
	return nil
}

func validateUserSendInviteEmail(data *UserResourceModel, value types.Bool) error {
	if !sendInviteEmailRequested(value) {
		return nil
	}
	if data.UserEmail.IsNull() || data.UserEmail.IsUnknown() || data.UserEmail.ValueString() == "" {
		return fmt.Errorf("send_invite_email requires user_email so LiteLLM has a recipient address")
	}
	if err := validateInviteEmailAddress(data.UserEmail.ValueString()); err != nil {
		return fmt.Errorf("send_invite_email user_email %s", err)
	}
	return nil
}

func keyInviteRecipientLookupError(err error) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("LiteLLM returned HTTP %d while verifying the invitation recipient", apiErr.StatusCode)
	}
	return fmt.Errorf("the invitation recipient lookup failed at the transport layer")
}

func (r *KeyResource) validateKeyInviteRecipient(ctx context.Context, data *KeyResourceModel, value types.Bool) error {
	if err := validateKeySendInviteEmail(data, value); err != nil {
		return err
	}
	if !sendInviteEmailRequested(value) {
		return nil
	}

	expectedUserID := data.UserID.ValueString()
	query := url.Values{"user_id": []string{expectedUserID}}
	endpoint := endpointWithQuery("/user/info", query)
	var response map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &response); err != nil {
		return keyInviteRecipientLookupError(err)
	}
	userInfo := response
	if nested, ok := response["user_info"].(map[string]interface{}); ok {
		userInfo = nested
	}
	observedUserID, idOK := userInfo["user_id"].(string)
	if !idOK || observedUserID == "" || observedUserID != expectedUserID {
		return fmt.Errorf("LiteLLM did not return the exact configured user_id during invitation recipient verification")
	}
	email, emailOK := userInfo["user_email"].(string)
	if !emailOK || email == "" {
		return fmt.Errorf("the configured user_id does not have a non-empty user_email in LiteLLM")
	}
	if err := validateInviteEmailAddress(email); err != nil {
		return fmt.Errorf("the configured user's user_email %s", err)
	}
	return nil
}
