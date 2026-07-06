package agent

import "testing"

// TestPlatformCatalogCompleteness is the anti-silent-miss guard: every registered
// platform must resolve the factories every dispatch path needs. Before the
// PlatformCatalog, adding a cloud meant editing ~10 separate switches, and missing
// one (e.g. getProjectDetector) compiled fine but broke every deploy at runtime.
// Now there is one registration and this test asserts it is complete.
func TestPlatformCatalogCompleteness(t *testing.T) {
	specs := RegisteredPlatforms()
	if len(specs) == 0 {
		t.Fatal("no platforms registered")
	}
	for _, s := range specs {
		if s.Name == "" {
			t.Errorf("%v: empty Name", s.Platform)
		}
		if s.Platform == UnknownPlatform {
			t.Errorf("%q: registered as UnknownPlatform", s.Name)
		}
		// Required factories (NewDetector is optional → noopProjectDetector).
		if s.NewDeployable == nil {
			t.Errorf("%q: nil NewDeployable", s.Name)
		}
		if s.NewAuthProvider == nil {
			t.Errorf("%q: nil NewAuthProvider", s.Name)
		}
		// The enum must round-trip through the catalog.
		if got, ok := LookupPlatform(s.Platform); !ok || got.Platform != s.Platform {
			t.Errorf("%q: LookupPlatform(%v) failed", s.Name, s.Platform)
		}
		// The display name and every alias must resolve back to this platform, so
		// an LLM- or menu-produced string maps to what we deploy.
		if got, ok := PlatformByString(s.Name); !ok || got != s.Platform {
			t.Errorf("%q: Name does not resolve back (got %v, ok %v)", s.Name, got, ok)
		}
		for _, alias := range s.Aliases {
			if got, ok := PlatformByString(alias); !ok || got != s.Platform {
				t.Errorf("%q: alias %q resolves to %v (ok %v), want %v", s.Name, alias, got, ok, s.Platform)
			}
		}
	}
}

// TestDjangoDomainsDeriveFromCatalog asserts the Django host/CSRF allow-lists are
// derived from each platform's DomainSuffix (the L1b fold).
func TestDjangoDomainsDeriveFromCatalog(t *testing.T) {
	h := &DjangoHandler{}
	for _, s := range RegisteredPlatforms() {
		if s.DomainSuffix == "" {
			t.Errorf("%q: no DomainSuffix (Django hosts would be empty)", s.Name)
			continue
		}
		if got := h.getDomainPatterns(s.Platform); len(got) != 1 || got[0] != s.DomainSuffix {
			t.Errorf("%q: getDomainPatterns = %v, want [%q]", s.Name, got, s.DomainSuffix)
		}
		want := "https://*" + s.DomainSuffix
		if got := h.getCsrfOrigins(s.Platform); len(got) != 1 || got[0] != want {
			t.Errorf("%q: getCsrfOrigins = %v, want [%q]", s.Name, got, want)
		}
	}
}

// TestRollbackGateDerivesFromCatalog asserts the friendly rollback-unsupported gate
// tracks the SupportsRollback flag (the L1b fold): AWS/Cloud Run gated, others not.
func TestRollbackGateDerivesFromCatalog(t *testing.T) {
	for _, s := range RegisteredPlatforms() {
		msg, gated := unsupportedLocalPlatform(s.Platform)
		if gated == s.SupportsRollback {
			t.Errorf("%q: gated=%v but SupportsRollback=%v (should be opposite)", s.Name, gated, s.SupportsRollback)
		}
		if gated && msg == "" {
			t.Errorf("%q: gated but empty message", s.Name)
		}
	}
}

// TestManagedContainerPlatforms pins which platforms use the shared container
// workflow. A ManagedContainer platform's Deployable MUST mark its service resource
// Primary (deployContainer finds the URL via CreatedResource.Primary) — so adding
// ManagedContainer here without setting Primary would break its deploy.
func TestManagedContainerPlatforms(t *testing.T) {
	want := map[Platform]bool{AWS: true, GoogleCloudRun: true}
	for _, s := range RegisteredPlatforms() {
		if s.ManagedContainer != want[s.Platform] {
			t.Errorf("%q: ManagedContainer=%v, want %v", s.Name, s.ManagedContainer, want[s.Platform])
		}
	}
}

// TestEveryEnumPlatformRegistered asserts no real platform is left out of the
// catalog (which would make it undispatchable).
func TestEveryEnumPlatformRegistered(t *testing.T) {
	for p := Platform(0); p < UnknownPlatform; p++ {
		if _, ok := LookupPlatform(p); !ok {
			t.Errorf("platform %v (%d) is not registered in the catalog", p, p)
		}
	}
}
