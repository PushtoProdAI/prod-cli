package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// testActivities returns an Activities instance with a framework registry for testing
func testActivities() *Activities {
	return &Activities{
		frameworkRegistry: NewFrameworkRegistry(),
	}
}

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

func TestPatchPackageJSONCleanup(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		adapter        string
		wantScripts    map[string]string
		wantDependency string
	}{
		{
			name: "Netlify to Render - cleanup Netlify scripts",
			input: `{
  "dependencies": {
    "@sveltejs/adapter-netlify": "^4.3.0"
  },
  "scripts": {
    "build": "vite build && npm run netlify-functions-build",
    "netlify-functions-build": "netlify functions:build --functions .netlify/functions --src .netlify/functions-internal",
    "dev": "vite dev"
  }
}`,
			adapter: "@sveltejs/adapter-node",
			wantScripts: map[string]string{
				"build": "vite build",
				"dev":   "vite dev",
			},
			wantDependency: "@sveltejs/adapter-node",
		},
		{
			name: "Render to Netlify - add Netlify scripts",
			input: `{
  "dependencies": {
    "@sveltejs/adapter-node": "^5.2.0"
  },
  "scripts": {
    "build": "vite build",
    "dev": "vite dev"
  }
}`,
			adapter: "@sveltejs/adapter-netlify",
			wantScripts: map[string]string{
				"build":                   "vite build && npm run netlify-functions-build",
				"netlify-functions-build": "netlify functions:build --functions .netlify/functions --src .netlify/functions-internal",
				"dev":                     "vite dev",
			},
			wantDependency: "@sveltejs/adapter-netlify",
		},
		{
			name: "Clean package.json with only netlify-functions-build as build script",
			input: `{
  "dependencies": {
    "@sveltejs/adapter-netlify": "^4.3.0"
  },
  "scripts": {
    "build": "npm run netlify-functions-build",
    "netlify-functions-build": "netlify functions:build --functions .netlify/functions --src .netlify/functions-internal"
  }
}`,
			adapter: "@sveltejs/adapter-node",
			wantScripts: map[string]string{
				"build": "vite build",
			},
			wantDependency: "@sveltejs/adapter-node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := patchPackageJSON([]byte(tt.input), tt.adapter, "^5.2.0")
			if err != nil {
				t.Fatalf("patchPackageJSON() error = %v", err)
			}

			var pkg map[string]any
			if err := json.Unmarshal(result, &pkg); err != nil {
				t.Fatalf("Failed to unmarshal result: %v", err)
			}

			// Check scripts
			scripts, ok := pkg["scripts"].(map[string]any)
			if !ok {
				t.Fatal("scripts section not found")
			}

			for wantScript, wantValue := range tt.wantScripts {
				if got, exists := scripts[wantScript]; !exists {
					t.Errorf("script %q not found", wantScript)
				} else if got != wantValue {
					t.Errorf("script %q = %q, want %q", wantScript, got, wantValue)
				}
			}

			// Check that netlify-functions-build is removed when not using Netlify adapter
			if tt.adapter != "@sveltejs/adapter-netlify" {
				if _, exists := scripts["netlify-functions-build"]; exists {
					t.Error("netlify-functions-build script should be removed for non-Netlify adapters")
				}
			}

			// Check dependencies
			deps, ok := pkg["dependencies"].(map[string]any)
			if !ok {
				t.Fatal("dependencies section not found")
			}

			if _, exists := deps[tt.wantDependency]; !exists {
				t.Errorf("dependency %q not found", tt.wantDependency)
			}

			// Check that old adapters are removed
			oldAdapters := []string{"@sveltejs/adapter-auto", "@sveltejs/adapter-netlify", "@sveltejs/adapter-node"}
			for _, oldAdapter := range oldAdapters {
				if oldAdapter != tt.wantDependency {
					if _, exists := deps[oldAdapter]; exists {
						t.Errorf("old adapter %q should be removed", oldAdapter)
					}
				}
			}
		})
	}
}

func TestPatchPackageJSONForPlatformWithFrameworkDetection(t *testing.T) {
	tests := []struct {
		name            string
		framework       string // runtime framework from ServiceRequirements
		platform        Platform
		wantPackageJSON string
		wantChanged     bool
	}{
		{
			name:      "SvelteKit project gets patched for Vercel",
			framework: "SvelteKit",
			platform:  Vercel,
			wantPackageJSON: `{
  "dependencies": {
    "@sveltejs/adapter-vercel": "^5.10.2"
  }
}`,
			wantChanged: true,
		},
		{
			name:      "Next.js project does NOT get patched for Vercel",
			framework: "Next.js",
			platform:  Vercel,
			wantPackageJSON: `{
  "name": "test"
}`,
			wantChanged: false,
		},
		{
			name:      "No runtime framework does NOT get patched",
			framework: "",
			platform:  Vercel,
			wantPackageJSON: `{
  "name": "test"
}`,
			wantChanged: false,
		},
		{
			name:      "Vite build tool only does NOT get patched for Vercel",
			framework: "", // No runtime framework, just build tool
			platform:  Vercel,
			wantPackageJSON: `{
  "name": "test"
}`,
			wantChanged: false,
		},
		{
			name:      "Angular project does NOT get patched for Vercel",
			framework: "Angular",
			platform:  Vercel,
			wantPackageJSON: `{
  "name": "test"
}`,
			wantChanged: false,
		},
		{
			name:      "Vue project does NOT get patched for Vercel",
			framework: "Vue",
			platform:  Vercel,
			wantPackageJSON: `{
  "name": "test"
}`,
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Original package.json
			origPackageJSON := `{
  "name": "test"
}`

			// Test the function
			a := testActivities()
			updatedJSON, changed, err := a.patchPackageJSONForPlatform([]byte(origPackageJSON), tt.platform, tt.framework)
			if err != nil {
				t.Fatalf("patchPackageJSONForPlatform failed: %v", err)
			}

			if changed != tt.wantChanged {
				t.Errorf("expected changed=%v, got changed=%v", tt.wantChanged, changed)
			}

			// Parse the result to check the content
			var result map[string]any
			if err := json.Unmarshal(updatedJSON, &result); err != nil {
				t.Fatalf("failed to parse result JSON: %v", err)
			}

			var expected map[string]any
			if err := json.Unmarshal([]byte(tt.wantPackageJSON), &expected); err != nil {
				t.Fatalf("failed to parse expected JSON: %v", err)
			}

			// Check if the result matches expectations
			if tt.wantChanged {
				// For SvelteKit projects, check that the adapter was added
				deps, ok := result["dependencies"].(map[string]any)
				if !ok {
					t.Fatal("expected dependencies section in result")
				}
				expectedDeps := expected["dependencies"].(map[string]any)
				for adapter := range expectedDeps {
					if _, exists := deps[adapter]; !exists {
						t.Errorf("expected adapter %q to be added", adapter)
					}
				}
			} else {
				// For non-SvelteKit projects, check that no adapters were added
				if deps, ok := result["dependencies"].(map[string]any); ok {
					svelteAdapters := []string{
						"@sveltejs/adapter-auto", "@sveltejs/adapter-netlify",
						"@sveltejs/adapter-node", "@sveltejs/adapter-vercel",
					}
					for _, adapter := range svelteAdapters {
						if _, exists := deps[adapter]; exists {
							t.Errorf("unexpected adapter %q was added to non-SvelteKit project", adapter)
						}
					}
				}
			}
		})
	}
}

func TestPatchRemixViteConfigForNetlify(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "Add netlify plugin to basic vite config",
			input: `import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";

export default defineConfig({
  plugins: [
    remix(),
    tsconfigPaths()
  ]
});`,
			expected: []string{
				`import { netlifyPlugin } from "@netlify/remix-adapter/plugin"`,
				"netlifyPlugin()",
			},
		},
		{
			name: "Config already has netlify plugin",
			input: `import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";
import { netlifyPlugin } from "@netlify/remix-adapter/plugin";

export default defineConfig({
  plugins: [
    remix(),
    tsconfigPaths(),
    netlifyPlugin()
  ]
});`,
			expected: []string{
				`import { netlifyPlugin } from "@netlify/remix-adapter/plugin"`,
				"netlifyPlugin()",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := patchRemixViteConfigForNetlify([]byte(tt.input))
			resultStr := string(result)

			for _, expected := range tt.expected {
				if !strings.Contains(resultStr, expected) {
					t.Errorf("expected %q to be in result:\n%s", expected, resultStr)
				}
			}
		})
	}
}

func TestRemoveNetlifyPluginFromRemixConfig(t *testing.T) {
	input := `import { vitePlugin as remix } from "@remix-run/dev";
import { defineConfig } from "vite";
import tsconfigPaths from "vite-tsconfig-paths";
import { netlifyPlugin } from "@netlify/remix-adapter/plugin";

export default defineConfig({
  plugins: [
    remix(),
    tsconfigPaths(),
    netlifyPlugin()
  ]
});`

	result := removeNetlifyPluginFromRemixConfig([]byte(input))
	resultStr := string(result)

	// Should not contain netlify import or plugin call
	if strings.Contains(resultStr, `import { netlifyPlugin } from "@netlify/remix-adapter/plugin"`) {
		t.Error("netlify import should be removed")
	}
	if strings.Contains(resultStr, "netlifyPlugin()") {
		t.Error("netlifyPlugin() call should be removed")
	}

	// Should still contain other imports and plugins
	if !strings.Contains(resultStr, "remix()") {
		t.Error("remix() plugin should remain")
	}
	if !strings.Contains(resultStr, "tsconfigPaths()") {
		t.Error("tsconfigPaths() plugin should remain")
	}
}

func TestPatchPackageJSONForRemixPlatforms(t *testing.T) {
	basePackageJSON := `{
  "name": "remix-app",
  "scripts": {
    "build": "remix build",
    "start": "remix-serve build/index.js"
  },
  "dependencies": {
    "@remix-run/node": "^2.0.0",
    "@remix-run/react": "^2.0.0"
  },
  "devDependencies": {
    "@remix-run/dev": "^2.0.0",
    "vite": "^4.4.2"
  }
}`

	tests := []struct {
		name            string
		platform        Platform
		framework       string
		wantChanged     bool
		expectedDeps    []string
		notExpectedDeps []string
	}{
		{
			name:            "Remix project for Netlify platform",
			platform:        Netlify,
			framework:       "Remix",
			wantChanged:     true,
			expectedDeps:    []string{"@netlify/remix-adapter"},
			notExpectedDeps: []string{"@remix-run/serve"},
		},
		{
			name:            "Remix project for Render platform",
			platform:        Render,
			framework:       "Remix",
			wantChanged:     true,
			expectedDeps:    []string{"@remix-run/serve"},
			notExpectedDeps: []string{"@netlify/remix-adapter"},
		},
		{
			name:            "Remix project for FlyIO platform",
			platform:        FlyIO,
			framework:       "Remix",
			wantChanged:     true,
			expectedDeps:    []string{"@remix-run/serve"},
			notExpectedDeps: []string{"@netlify/remix-adapter", "@vercel/remix"},
		},
		{
			name:            "Remix project for Heroku platform",
			platform:        Heroku,
			framework:       "Remix",
			wantChanged:     true,
			expectedDeps:    []string{"@remix-run/serve"},
			notExpectedDeps: []string{"@netlify/remix-adapter", "@vercel/remix"},
		},
		{
			name:            "Remix project for Vercel platform",
			platform:        Vercel,
			framework:       "Remix",
			wantChanged:     true,
			expectedDeps:    []string{"@vercel/remix"},
			notExpectedDeps: []string{"@netlify/remix-adapter", "@remix-run/serve"},
		},
		{
			name:            "Non-Remix project should not be changed",
			platform:        Netlify,
			framework:       "Next.js",
			wantChanged:     false,
			expectedDeps:    []string{},
			notExpectedDeps: []string{"@netlify/remix-adapter", "@remix-run/serve"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testActivities()
			updatedPackageJSON, changed, err := a.patchPackageJSONForPlatform([]byte(basePackageJSON), tt.platform, tt.framework)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if changed != tt.wantChanged {
				t.Errorf("expected changed=%v, got %v", tt.wantChanged, changed)
			}

			if tt.wantChanged {
				var result map[string]any
				if err := json.Unmarshal(updatedPackageJSON, &result); err != nil {
					t.Fatalf("failed to parse result JSON: %v", err)
				}

				// Check expected dependencies are present
				for _, expectedDep := range tt.expectedDeps {
					found := false
					for _, section := range []string{"dependencies", "devDependencies"} {
						if deps, ok := result[section].(map[string]any); ok {
							if _, exists := deps[expectedDep]; exists {
								found = true
								break
							}
						}
					}
					if !found {
						t.Errorf("expected dependency %q to be added", expectedDep)
					}
				}

				// Check unwanted dependencies are not present
				for _, notExpectedDep := range tt.notExpectedDeps {
					for _, section := range []string{"dependencies", "devDependencies"} {
						if deps, ok := result[section].(map[string]any); ok {
							if _, exists := deps[notExpectedDep]; exists {
								t.Errorf("unexpected dependency %q was added", notExpectedDep)
							}
						}
					}
				}
			}
		})
	}
}
