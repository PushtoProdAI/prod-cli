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

// TestEveryEnumPlatformRegistered asserts no real platform is left out of the
// catalog (which would make it undispatchable).
func TestEveryEnumPlatformRegistered(t *testing.T) {
	for p := Platform(0); p < UnknownPlatform; p++ {
		if _, ok := LookupPlatform(p); !ok {
			t.Errorf("platform %v (%d) is not registered in the catalog", p, p)
		}
	}
}
