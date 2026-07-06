package deployment

import (
	"strings"
	"testing"
)

func TestEstimateManagedContainerCost(t *testing.T) {
	for _, key := range []string{"aws", "googlecloudrun", "azure"} {
		ce, ok := EstimateManagedContainerCost(key)
		if !ok {
			t.Fatalf("%s: expected an estimate", key)
		}
		if ce.Total <= 0 {
			t.Errorf("%s: total %v should be positive", key, ce.Total)
		}
		if len(ce.Services) != 1 || ce.Services[0].Cost != ce.Total {
			t.Errorf("%s: expected one service whose cost equals the total, got %+v", key, ce.Services)
		}
		if !strings.HasPrefix(ce.Services[0].Plan, "est.") {
			t.Errorf("%s: plan should be labeled an estimate, got %q", key, ce.Services[0].Plan)
		}
	}

	// A non-container platform has no managed-container estimate (falls back to none).
	if _, ok := EstimateManagedContainerCost("fly"); ok {
		t.Error("fly should not resolve a managed-container estimate")
	}

	// Scale-to-zero clouds are honestly labeled as upper bounds.
	gcr, _ := EstimateManagedContainerCost("googlecloudrun")
	if !strings.Contains(gcr.Services[0].Plan, "scales to zero") {
		t.Errorf("Cloud Run estimate should note scale-to-zero, got %q", gcr.Services[0].Plan)
	}
	aws, _ := EstimateManagedContainerCost("aws")
	if strings.Contains(aws.Services[0].Plan, "scales to zero") {
		t.Errorf("App Runner is always-on, should not claim scale-to-zero, got %q", aws.Services[0].Plan)
	}
}
