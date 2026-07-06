package deployment

import "fmt"

// hoursPerMonth is the conventional billing month (730h) used for monthly estimates.
const hoursPerMonth = 730

// managedContainerRate holds a managed-container cloud's approximate public list
// prices (USD per vCPU-hour and per GB-hour) and the size prod deploys by default.
// alwaysOn distinguishes App Runner (billed continuously) from the scale-to-zero
// clouds (Cloud Run, Azure Container Apps), where the estimate is an upper bound.
type managedContainerRate struct {
	vCPUHour    float64
	gbHour      float64
	defaultVCPU float64
	defaultGB   float64
	alwaysOn    bool
}

// managedContainerRates is keyed by the lowercased Platform string. Prices are
// approximate list prices and will drift — this is a preview estimate, not a bill.
var managedContainerRates = map[string]managedContainerRate{
	"aws":            {vCPUHour: 0.064, gbHour: 0.007, defaultVCPU: 1, defaultGB: 2, alwaysOn: true},      // App Runner (active)
	"googlecloudrun": {vCPUHour: 0.0864, gbHour: 0.009, defaultVCPU: 1, defaultGB: 0.5, alwaysOn: false},  // Cloud Run
	"azure":          {vCPUHour: 0.0864, gbHour: 0.0108, defaultVCPU: 0.5, defaultGB: 1, alwaysOn: false}, // Container Apps
}

// EstimateManagedContainerCost returns a rough monthly estimate for a managed-
// container platform: one instance at prod's default size, billed continuously. For
// the scale-to-zero clouds (Cloud Run, Azure Container Apps) this is an UPPER BOUND —
// a low-traffic app costs far less, often within the free tier. Honest-but-rough, so
// the preview shows a real number instead of $0; the second return is false for
// platforms with no rate (fall back to no estimate).
func EstimateManagedContainerCost(platformKey string) (CostEstimate, bool) {
	r, ok := managedContainerRates[platformKey]
	if !ok {
		return CostEstimate{}, false
	}
	monthly := (r.defaultVCPU*r.vCPUHour + r.defaultGB*r.gbHour) * hoursPerMonth

	plan := fmt.Sprintf("est. ~1 always-on instance (%.3g vCPU / %.3g GB)", r.defaultVCPU, r.defaultGB)
	if !r.alwaysOn {
		plan += "; scales to zero — less at low traffic"
	}

	return CostEstimate{
		Total: monthly,
		Services: []CostService{{
			Service: Service{Name: "web", Provider: "container"},
			Plan:    plan,
			Cost:    monthly,
		}},
	}, true
}
