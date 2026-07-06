package managedcontainer

import "testing"

// primaryResource must forward the per-cloud identifiers (region, project,
// resourceGroup, …) alongside the URL, or the deploy workflow can't persist them to
// history and the console URL / logs command can't be rebuilt later.
func TestPrimaryResourceForwardsIdentifiers(t *testing.T) {
	res := DeployResult{
		ID: "arn:aws:apprunner:svc/app", Name: "app", URL: "https://app.awsapprunner.com",
		Identifiers: map[string]string{"region": "us-east-1", "account": "123456789012"},
	}
	crs := primaryResource(res, "apprunner_service")
	if len(crs) != 1 {
		t.Fatalf("want 1 resource, got %d", len(crs))
	}
	cr := crs[0]
	if cr.ID != res.ID || !cr.Primary || cr.Type != "apprunner_service" {
		t.Errorf("bad resource shell: %+v", cr)
	}
	if cr.Metadata["url"] != res.URL {
		t.Errorf("url not carried: %v", cr.Metadata["url"])
	}
	if cr.Metadata["region"] != "us-east-1" || cr.Metadata["account"] != "123456789012" {
		t.Errorf("identifiers not forwarded: %+v", cr.Metadata)
	}
}

// No identifiers (Cloud Run/App Runner may leave it nil) must not panic and must still
// carry the URL.
func TestPrimaryResourceNoIdentifiers(t *testing.T) {
	crs := primaryResource(DeployResult{ID: "svc", Name: "n", URL: "https://n"}, "cloudrun_service")
	if crs[0].Metadata["url"] != "https://n" {
		t.Errorf("url missing: %+v", crs[0].Metadata)
	}
}
