package heroku

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateProcfile(t *testing.T) {
	tests := []struct {
		name             string
		existingProcfile string
		startCommand     string
		migrationCommand string
		wantContent      string
		wantReplaced     bool
	}{
		{
			name:             "Create new Procfile",
			existingProcfile: "",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			wantReplaced:     false,
		},
		{
			name:             "Create Procfile with migration",
			existingProcfile: "",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "python manage.py migrate",
			wantContent:      "release: python manage.py migrate\nweb: gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			wantReplaced:     false,
		},
		{
			name:             "Replace runserver command",
			existingProcfile: "web: python manage.py runserver",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			wantReplaced:     true,
		},
		{
			name:             "Replace runserver with port",
			existingProcfile: "web: python manage.py runserver 0.0.0.0:8000",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			wantReplaced:     true,
		},
		{
			name:             "Keep existing gunicorn command",
			existingProcfile: "web: gunicorn app:app --bind 0.0.0.0:$PORT",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: gunicorn app:app --bind 0.0.0.0:$PORT",
			wantReplaced:     false,
		},
		{
			name:             "Keep existing uvicorn command",
			existingProcfile: "web: uvicorn main:app --host 0.0.0.0 --port $PORT",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: uvicorn main:app --host 0.0.0.0 --port $PORT",
			wantReplaced:     false,
		},
		{
			name:             "Replace custom script without production server",
			existingProcfile: "web: python app.py",
			startCommand:     "gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			migrationCommand: "",
			wantContent:      "web: gunicorn myproject.wsgi:application --bind 0.0.0.0:$PORT",
			wantReplaced:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp directory
			tmpDir := t.TempDir()

			// Create existing Procfile if specified
			procfilePath := filepath.Join(tmpDir, "Procfile")
			if tt.existingProcfile != "" {
				if err := os.WriteFile(procfilePath, []byte(tt.existingProcfile), 0o644); err != nil {
					t.Fatalf("Failed to create existing Procfile: %v", err)
				}
			}

			// Create GitDeployStep
			step := &GitDeployStep{
				BuildContext:     tmpDir,
				StartCommand:     tt.startCommand,
				MigrationCommand: tt.migrationCommand,
			}

			// Execute createProcfile
			if err := step.createProcfile(); err != nil {
				t.Fatalf("createProcfile() error = %v", err)
			}

			// Read the resulting Procfile
			content, err := os.ReadFile(procfilePath)
			if err != nil {
				t.Fatalf("Failed to read Procfile: %v", err)
			}

			contentStr := strings.TrimSpace(string(content))
			wantStr := strings.TrimSpace(tt.wantContent)

			if contentStr != wantStr {
				t.Errorf("Procfile content mismatch\nGot:\n%s\n\nWant:\n%s", contentStr, wantStr)
			}
		})
	}
}
