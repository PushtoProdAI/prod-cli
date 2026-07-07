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
