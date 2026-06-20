# terraform/ses.tf

# Sandbox sender identity. Recipient addresses are verified manually at onboarding
# (one-click via the SES verification email) — out of band, not managed here.
resource "aws_ses_email_identity" "sender" {
  email = var.sender_email
}
