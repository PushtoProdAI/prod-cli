package agent

import "testing"

func TestDisplayEnvValueMasksSecrets(t *testing.T) {
	if got := displayEnvValue(true, "sk-ant-secret-key"); got != "(set, hidden)" {
		t.Errorf("sensitive value must be masked, got %q", got)
	}
	if got := displayEnvValue(false, "production"); got != "production" {
		t.Errorf("non-sensitive value shown, got %q", got)
	}
	if got := displayEnvValue(true, ""); got != "(empty)" {
		t.Errorf("empty shown as (empty), got %q", got)
	}
}

func TestIsPlatformManagedVar(t *testing.T) {
	for _, v := range []string{"NODE_ENV", "NEXT_RUNTIME", "NEXT_PHASE", "VERCEL", "VERCEL_URL", "VERCEL_GIT_COMMIT_SHA", "ci"} {
		if !isPlatformManagedVar(v) {
			t.Errorf("%s should be platform-managed (skipped)", v)
		}
	}
	for _, v := range []string{"DATABASE_URL", "ANTHROPIC_API_KEY", "BETTER_AUTH_SECRET", "MY_APP_FLAG"} {
		if isPlatformManagedVar(v) {
			t.Errorf("%s must NOT be treated as platform-managed", v)
		}
	}
}

func TestIsSelfURLVar(t *testing.T) {
	for _, v := range []string{"BETTER_AUTH_URL", "NEXTAUTH_URL", "APP_URL", "NEXT_PUBLIC_BASE_URL", "SITE_URL", "APP_ORIGIN"} {
		if !isSelfURLVar(v) {
			t.Errorf("%s should be recognized as a self-URL var", v)
		}
	}
	for _, v := range []string{"DATABASE_URL", "REDIS_URL", "API_KEY"} {
		if isSelfURLVar(v) {
			t.Errorf("%s is not the app's own origin", v)
		}
	}
}
