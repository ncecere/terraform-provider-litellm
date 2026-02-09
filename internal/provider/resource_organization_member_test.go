package provider

import "testing"

func TestMatchOrganizationMember(t *testing.T) {
	t.Parallel()

	t.Run("matches by user_id when provided", func(t *testing.T) {
		if !matchOrganizationMember("user-1", "a@example.com", "user-1", "") {
			t.Fatal("expected match by user_id")
		}
		if matchOrganizationMember("user-2", "a@example.com", "user-1", "") {
			t.Fatal("did not expect match for different user_id")
		}
	})

	t.Run("matches by user_email when user_id not provided", func(t *testing.T) {
		if !matchOrganizationMember("user-1", "a@example.com", "", "a@example.com") {
			t.Fatal("expected match by user_email")
		}
		if matchOrganizationMember("user-1", "b@example.com", "", "a@example.com") {
			t.Fatal("did not expect match for different user_email")
		}
	})

	t.Run("does not match when both targets are empty", func(t *testing.T) {
		if matchOrganizationMember("user-1", "a@example.com", "", "") {
			t.Fatal("did not expect match with empty target identifiers")
		}
	})
}
