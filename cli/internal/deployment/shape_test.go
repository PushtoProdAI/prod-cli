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
