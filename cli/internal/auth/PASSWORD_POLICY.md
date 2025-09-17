# Password Security Policy

This document outlines the comprehensive password security requirements implemented in the Prod CLI authentication system.

## Password Requirements

### Minimum Requirements
- **Length**: Minimum 8 characters (recommended 12+)
- **Uppercase Letters**: At least one uppercase letter (A-Z)
- **Lowercase Letters**: At least one lowercase letter (a-z)
- **Numbers**: At least one digit (0-9)
- **Special Characters**: At least one special character (!@#$%^&*()_+-=[]{}|;':",./<>?)

### Password Strength Scoring

The system uses a comprehensive scoring algorithm that evaluates:

1. **Base Requirements (40 points)**
   - Length requirement: 8 points
   - Uppercase letter: 8 points
   - Lowercase letter: 8 points
   - Number: 8 points
   - Special character: 8 points

2. **Length Bonus (30 points)**
   - 12+ characters: 15 points
   - 10-11 characters: 10 points
   - 8-9 characters: 5 points

3. **Complexity Bonus (30 points)**
   - 12+ unique characters: 15 points
   - 8-11 unique characters: 10 points
   - 6-7 unique characters: 5 points

4. **Pattern Detection (Penalties)**
   - Repeated characters (3+): -10 points
   - Common sequences (123, abc, etc.): -15 points
   - Common words (password, admin, etc.): -20 points

### Strength Levels
- **Very Weak**: 0-29 points (Red)
- **Weak**: 30-49 points (Orange)
- **Fair**: 50-69 points (Yellow)
- **Good**: 70-84 points (Green)
- **Strong**: 85-100 points (Emerald)

## Security Features

### 1. Password History
- **Purpose**: Prevents users from reusing recent passwords
- **History Count**: Last 5 passwords are remembered
- **Enforcement**: New passwords cannot match any password in history

### 2. Account Lockout
- **Failed Attempts**: Maximum 5 failed login attempts
- **Lockout Duration**: 30 minutes after reaching the limit
- **Reset**: Lockout is automatically cleared after the duration expires

### 3. Password Expiration
- **Default Age**: 90 days
- **Notification**: Users are warned before expiration
- **Enforcement**: Password must be changed before expiration

### 4. Breach Detection
- **Service**: Integration with HaveIBeenPwned API
- **Method**: SHA-1 hash prefix matching (secure)
- **Enforcement**: Passwords found in breaches are rejected

### 5. First Login Password Change
- **Purpose**: Ensures users set their own secure passwords
- **Enforcement**: Required on first login after account creation
- **Bypass**: Not applicable for OAuth providers

## Implementation Details

### Client-Side Validation
- Real-time password strength feedback
- Visual indicators for each requirement
- Breach checking with user-friendly warnings
- Form submission prevention for weak passwords

### Server-Side Enforcement
- Database-level password history tracking
- Account lockout management
- Password expiration monitoring
- Secure password hashing and storage

### API Integration
- HaveIBeenPwned API for breach detection
- SHA-1 hashing for secure prefix matching
- Rate limiting and error handling

## User Experience

### Visual Feedback
- Color-coded strength indicator
- Checkmark icons for met requirements
- Warning messages for security issues
- Progress bars for strength visualization

### Error Messages
- Clear, actionable error messages
- Specific requirement failures
- Breach detection warnings
- Lockout notifications

### Accessibility
- Screen reader compatible
- Keyboard navigation support
- High contrast color schemes
- Clear visual indicators

## Configuration

### Environment Variables
```bash
# Password policy settings
PASSWORD_MIN_LENGTH=8
PASSWORD_REQUIRE_UPPERCASE=true
PASSWORD_REQUIRE_LOWERCASE=true
PASSWORD_REQUIRE_NUMBERS=true
PASSWORD_REQUIRE_SYMBOLS=true
PASSWORD_MAX_AGE_DAYS=90
PASSWORD_HISTORY_COUNT=5
PASSWORD_MAX_FAILED_ATTEMPTS=5
PASSWORD_LOCKOUT_DURATION_MINUTES=30
PASSWORD_ENABLE_BREACH_CHECK=true
PASSWORD_REQUIRE_FIRST_CHANGE=true
```

### Database Configuration
The password security features are configured through the `password_policies` table:

```sql
-- Update password policy
UPDATE password_policies SET
    min_length = 12,
    max_age_days = 60,
    history_count = 10,
    max_failed_attempts = 3,
    lockout_duration_minutes = 60
WHERE id = (SELECT id FROM password_policies LIMIT 1);
```

## Security Considerations

### Password Storage
- Passwords are never stored in plain text
- Secure hashing algorithms are used
- Salt is applied to prevent rainbow table attacks

### Breach Detection
- Only password hash prefixes are sent to external APIs
- Full password hashes are never transmitted
- API calls are rate-limited and cached

### Account Protection
- Progressive lockout delays prevent brute force attacks
- Failed attempt tracking across sessions
- Automatic lockout expiration

### Privacy
- No personal information is sent to external services
- Breach checking is done anonymously
- User data is protected by database security policies

## Compliance

### Industry Standards
- NIST SP 800-63B guidelines
- OWASP password security recommendations
- PCI DSS requirements for password complexity

### Best Practices
- Regular security audits
- User education and training
- Incident response procedures
- Security monitoring and alerting

## Troubleshooting

### Common Issues

1. **Password Rejected Despite Meeting Requirements**
   - Check for breach detection warnings
   - Verify all requirements are met
   - Ensure password is not in history

2. **Account Locked Out**
   - Wait for lockout period to expire
   - Contact support if lockout persists
   - Check for multiple failed attempts

3. **Password Expiration Warnings**
   - Change password before expiration
   - Use password manager for secure storage
   - Follow password complexity guidelines

### Support
For password-related issues, users can:
- Use the password reset functionality
- Contact support for account recovery
- Review this documentation for guidance

## Updates and Maintenance

### Regular Updates
- Password policy reviews (quarterly)
- Security feature updates (as needed)
- User feedback incorporation (ongoing)

### Monitoring
- Failed login attempt tracking
- Password strength distribution
- Breach detection effectiveness
- User experience metrics

This password security policy ensures that the Prod CLI maintains the highest standards of account security while providing a user-friendly experience.
