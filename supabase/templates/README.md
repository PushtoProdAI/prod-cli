# PushToProd Email Templates

This directory contains custom email templates for PushToProd's Supabase authentication flows, branded according to our official style guide.

## Available Templates

### 1. Confirmation Email (`confirmation.html`)
- **Purpose**: Sent when a user signs up and needs to confirm their email
- **Subject**: "Welcome to PushToProd - Confirm Your Email"
- **Variables Available**:
  - `{{ .ConfirmationURL }}` - The confirmation link
  - `{{ .Token }}` - The confirmation token (for manual entry)
  - `{{ .SiteURL }}` - Your site URL
  - `{{ .Email }}` - User's email address

### 2. Password Recovery (`recovery.html`)
- **Purpose**: Sent when a user requests a password reset
- **Subject**: "Reset Your PushToProd Password"
- **Variables Available**:
  - `{{ .ConfirmationURL }}` - The password reset link
  - `{{ .Token }}` - The reset token
  - `{{ .SiteURL }}` - Your site URL
  - `{{ .Email }}` - User's email address

### 3. Invitation Email (`invite.html`)
- **Purpose**: Sent when inviting a user to join
- **Subject**: "You've Been Invited to PushToProd"
- **Variables Available**:
  - `{{ .ConfirmationURL }}` - The invitation acceptance link
  - `{{ .Token }}` - The invitation token
  - `{{ .SiteURL }}` - Your site URL
  - `{{ .Email }}` - User's email address

## Configuration

These templates are configured in `supabase/config.toml`:

```toml
[auth.email.template.confirmation]
subject = "Welcome to PushToProd - Confirm Your Email"
content_path = "./supabase/templates/confirmation.html"

[auth.email.template.recovery]
subject = "Reset Your PushToProd Password"
content_path = "./supabase/templates/recovery.html"

[auth.email.template.invite]
subject = "You've Been Invited to PushToProd"
content_path = "./supabase/templates/invite.html"
```

## Customization

To customize these templates:

1. Edit the HTML files in this directory
2. Use the available template variables (see above)
3. Maintain the responsive design and accessibility features
4. Test the templates across different email clients

## Testing

For local development, emails are captured by Inbucket (email testing server) and can be viewed at:
- URL: `http://localhost:54324`
- This allows you to see how emails render without actually sending them

## Production Considerations

For production use with a remote Supabase instance:

1. **Custom SMTP**: Set up a custom SMTP provider (SendGrid, Postmark, etc.)
2. **Template Variables**: Ensure all template variables are properly escaped
3. **Email Deliverability**: Test with real email addresses to ensure proper delivery
4. **Branding**: Update colors, logos, and content to match your brand

## Security Notes

- Never expose sensitive information in email templates
- Use HTTPS URLs for all links
- Consider email prefetching issues (some providers scan links)
- Include alternative confirmation methods (like OTP tokens)
