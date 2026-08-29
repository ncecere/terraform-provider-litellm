# Create-only invitation actions. The pinned loopback acceptance backend has no
# external email transport, so this validates API/action wiring without sending
# mail. A no-drift plan also proves the write-only action does not become
# persistent state or trigger an Update-time resend.
resource "litellm_user" "invite_email" {
  user_alias        = "smoke-invite-user"
  user_email        = "terraform-provider@example.invalid"
  auto_create_key   = false
  send_invite_email = true
}

resource "litellm_key" "invite_email" {
  key_alias         = "smoke-invite-key"
  user_id           = litellm_user.invite_email.id
  send_invite_email = true
}
