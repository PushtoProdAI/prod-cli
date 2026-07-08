package deployment

import "testing"

func TestParseShape(t *testing.T) {
	cases := []struct {
		in   string
		want DeployShape
	}{
		{"web", ShapeWeb},
		{"mcp-server", ShapeMCPServer},
		{"mcp", ShapeMCPServer},
		{"MCPServer", ShapeMCPServer},
		{"worker", ShapeWorker},
		{" Cron ", ShapeCron},
		// unknown/empty default to web so existing behavior is unchanged
		{"", ShapeWeb},
		{"unknown", ShapeWeb},
		{"gpu-agent", ShapeWeb},
	}
	for _, c := range cases {
		if got := ParseShape(c.in); got != c.want {
			t.Errorf("ParseShape(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestHTTPShaped(t *testing.T) {
	http := map[DeployShape]bool{
		ShapeWeb:       true,
		ShapeMCPServer: true,
		ShapeWorker:    false,
		ShapeCron:      false,
	}
	for shape, want := range http {
		if got := shape.HTTPShaped(); got != want {
			t.Errorf("%v.HTTPShaped() = %v, want %v", shape, got, want)
		}
	}
}

func TestIsValidCron(t *testing.T) {
	valid := []string{"0 2 * * *", "0 * * * *", "*/15 * * * *", "0 9 * * 1", "30 4 1,15 * 5"}
	for _, s := range valid {
		if !IsValidCron(s) {
			t.Errorf("IsValidCron(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "every night", "0 2 * *", "0 2 * * * *", "abc 2 * * *", "0 2 * * ?"}
	for _, s := range invalid {
		if IsValidCron(s) {
			t.Errorf("IsValidCron(%q) = true, want false", s)
		}
	}
}
