package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPatchSvelteConfig(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		adapter     string
		wantImports string
		wantOther   []string
	}{
		{
			name: "JS replace adapter import",
			input: `import adapter from '@sveltejs/adapter-auto';

const config = {
  kit: {
    adapter: adapter()
  }
};

export default config;`,
			adapter:     "@sveltejs/adapter-vercel",
			wantImports: "import adapter from '@sveltejs/adapter-vercel';",
			wantOther:   []string{"adapter: adapter()"},
		},
		{
			name: "JS inject adapter if missing",
			input: `import adapter from '@sveltejs/adapter-auto';

const config = {
  kit: {
  }
};

export default config;`,
			adapter:     "@sveltejs/adapter-vercel",
			wantImports: "import adapter from '@sveltejs/adapter-vercel';",
			wantOther:   []string{"adapter: adapter(),"},
		},
		{
			name: "TS with satisfies, replace adapter",
			input: `import adapter from '@sveltejs/adapter-auto';
import { sveltekit } from '@sveltejs/kit/vite';

const config = {
  kit: {
    adapter: adapter()
  }
} satisfies import('@sveltejs/kit').Config;

export default config;`,
			adapter:     "@sveltejs/adapter-vercel",
			wantImports: "import adapter from '@sveltejs/adapter-vercel';",
			wantOther:   []string{"adapter: adapter()", "} satisfies import('@sveltejs/kit').Config;"},
		},
		{
			name: "TS with satisfies, inject adapter",
			input: `import adapter from '@sveltejs/adapter-auto';
import { sveltekit } from '@sveltejs/kit/vite';

const config = {
  kit: {
  }
} satisfies import('@sveltejs/kit').Config;

export default config;`,
			adapter:     "@sveltejs/adapter-netlify",
			wantImports: "import adapter from '@sveltejs/adapter-netlify';",
			wantOther:   []string{"adapter: adapter(),", "} satisfies import('@sveltejs/kit').Config;"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := patchSvelteConfig([]byte(tt.input), tt.adapter)
			outStr := string(out)

			// import line should be replaced
			if !strings.Contains(outStr, tt.wantImports) {
				t.Errorf("expected import line %q\nGot:\n%s", tt.wantImports, outStr)
			}

			// other expectations
			for _, want := range tt.wantOther {
				if !strings.Contains(outStr, want) {
					t.Errorf("expected output to contain %q\nGot:\n%s", want, outStr)
				}
			}
		})
	}
}

func TestPatchPackageJSON(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		adapter      string
		version      string
		wantContains []string
	}{
		{
			name: "add adapter to existing devDependencies",
			input: `{
  "name": "my-app",
  "devDependencies": {
    "@sveltejs/kit": "next"
  }
}`,
			adapter: "@sveltejs/adapter-vercel",
			version: "^5.2.0",
			wantContains: []string{
				`"@sveltejs/adapter-vercel": "^5.2.0"`,
			},
		},
		{
			name: "update adapter version if already present",
			input: `{
  "name": "my-app",
  "devDependencies": {
    "@sveltejs/kit": "next",
    "@sveltejs/adapter-vercel": "^1.0.0"
  }
}`,
			adapter: "@sveltejs/adapter-vercel",
			version: "^5.2.0",
			wantContains: []string{
				`"@sveltejs/adapter-vercel": "^5.2.0"`,
			},
		},
		{
			name: "create devDependencies if missing",
			input: `{
  "name": "my-app",
  "dependencies": {
    "express": "^4.18.2"
  }
}`,
			adapter: "@sveltejs/adapter-netlify",
			version: "^5.3.0",
			wantContains: []string{
				`"@sveltejs/adapter-netlify": "^5.3.0"`,
			},
		},
		{
			name: "dependencies has adapter, but we enforce devDependencies",
			input: `{
  "name": "my-app",
  "dependencies": {
    "@sveltejs/adapter-vercel": "^1.0.0"
  }
}`,
			adapter: "@sveltejs/adapter-vercel",
			version: "^5.2.0",
			wantContains: []string{
				`"@sveltejs/adapter-vercel": "^5.2.0"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := patchPackageJSON([]byte(tt.input), tt.adapter, tt.version)
			if err != nil {
				t.Fatal(err)
			}
			outStr := string(out)

			// check expectations
			for _, want := range tt.wantContains {
				if !strings.Contains(outStr, want) {
					t.Errorf("expected output to contain %q\nGot:\n%s", want, outStr)
				}
			}

			// also check valid JSON
			var tmp map[string]any
			if err := json.Unmarshal(out, &tmp); err != nil {
				t.Errorf("output is not valid JSON: %v\nGot:\n%s", err, outStr)
			}
		})
	}
}
