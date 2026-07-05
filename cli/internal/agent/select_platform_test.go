package agent

import "testing"

func TestParseDeployPlatform(t *testing.T) {
	cases := []struct {
		in   string
		want Platform
	}{
		// by name (what a vibe coder actually types)
		{"fly", FlyIO},
		{"fly.io", FlyIO},
		{"FLYIO", FlyIO},
		{"render", Render},
		{" Vercel ", Vercel},
		{"netlify", Netlify},
		{"heroku", Heroku},
		{"aws", AWS},
		// by menu index (0-based, the TUI select convention)
		{"0", FlyIO},
		{"5", AWS},
		// invalid
		{"", UnknownPlatform},
		{"gcp", UnknownPlatform},
		{"99", UnknownPlatform},
	}
	for _, c := range cases {
		if got := parseDeployPlatform(c.in); got != c.want {
			t.Errorf("parseDeployPlatform(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDeployPlatformNamesMatchList(t *testing.T) {
	names := deployPlatformNames()
	if len(names) != len(deployPlatforms) {
		t.Fatalf("names %d != platforms %d", len(names), len(deployPlatforms))
	}
	// Every menu entry must round-trip back to its platform, so the numbered
	// choice a user picks maps to what we deploy.
	for i, name := range names {
		if got := parseDeployPlatform(name); got != deployPlatforms[i] {
			t.Errorf("menu entry %q (index %d) parses to %v, want %v", name, i, got, deployPlatforms[i])
		}
	}
	// None of the deploy choices is UnknownPlatform.
	for _, p := range deployPlatforms {
		if p == UnknownPlatform {
			t.Error("deployPlatforms must not contain UnknownPlatform")
		}
	}
}
