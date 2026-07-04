package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRouteDetection_Node(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		expectedRoutes []RouteCandidate
	}{
		{
			name: "Express.js routes",
			files: map[string]string{
				"app.js": `const express = require('express');
const app = express();

app.get('/users', (req, res) => {
  res.json({ users: [] });
});

app.post('/users', (req, res) => {
  res.json({ created: true });
});`,
			},
			expectedRoutes: []RouteCandidate{
				{Method: "GET", Path: "/users"},
				{Method: "POST", Path: "/users"},
			},
		},
		{
			name: "Router patterns",
			files: map[string]string{
				"routes.js": `const router = express.Router();

router.get('/api/orders', handleGetOrders);
router.delete('/api/orders/:id', handleDeleteOrder);`,
			},
			expectedRoutes: []RouteCandidate{
				{Method: "GET", Path: "/api/orders"},
				{Method: "DELETE", Path: "/api/orders/:id"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "route-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			fs := os.DirFS(tmpDir)
			projectFS := projectFS{FS: fs, rootPath: tmpDir}

			re := regexp.MustCompile(nodeRouteRegex)
			processor := NewDefaultRouteProcessor()
			routes, err := walkProjectForRoutes(projectFS, []string{".js", ".ts"}, []string{"node_modules"}, re, processor, 2, 2)
			if err != nil {
				t.Fatal(err)
			}

			if len(routes) != len(tt.expectedRoutes) {
				t.Errorf("Expected %d routes, got %d", len(tt.expectedRoutes), len(routes))
				for i, route := range routes {
					t.Logf("Route %d: Method=%s, Path=%s", i, route.Method, route.Path)
				}
				return
			}

			// Check that each expected route is present (order doesn't matter)
			for _, expectedRoute := range tt.expectedRoutes {
				found := false
				for _, actualRoute := range routes {
					if actualRoute.Method == expectedRoute.Method && actualRoute.Path == expectedRoute.Path {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected route not found: Method=%s, Path=%s", expectedRoute.Method, expectedRoute.Path)
				}
			}
		})
	}
}

func TestRouteDetection_Python(t *testing.T) {
	tests := []struct {
		name           string
		files          map[string]string
		expectedRoutes []RouteCandidate
	}{
		{
			name: "Flask routes",
			files: map[string]string{
				"app.py": `from flask import Flask
app = Flask(__name__)

@app.route('/users')
def get_users():
    return {'users': []}

@app.route('/users', methods=['POST'])
def create_user():
    return {'created': True}`,
			},
			expectedRoutes: []RouteCandidate{
				{Method: "GET", Path: "/users"}, // Flask @app.route without explicit method defaults to GET
				{Method: "POST", Path: "/users"},
			},
		},
		{
			name: "FastAPI routes",
			files: map[string]string{
				"main.py": `from fastapi import FastAPI
app = FastAPI()

@app.get('/items')
def get_items():
    return []

@app.post('/items')
def create_item():
    return {'created': True}`,
			},
			expectedRoutes: []RouteCandidate{
				{Method: "GET", Path: "/items"},
				{Method: "POST", Path: "/items"},
			},
		},
		{
			name: "Django routes",
			files: map[string]string{
				"urls.py": `from django.urls import path
from . import views

urlpatterns = [
    path('', views.home, name='home'),
    path('cars/', views.car_list, name='car_list'),
    path('cars/<int:pk>/', views.car_detail, name='car_detail'),
]`,
			},
			expectedRoutes: []RouteCandidate{
				{Method: "GET", Path: "/"}, // Root path - Django empty path converted to /
				{Method: "GET", Path: "cars/"},
				{Method: "GET", Path: "cars/<int:pk>/"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "route-test")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			for filename, content := range tt.files {
				filePath := filepath.Join(tmpDir, filename)
				if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			fs := os.DirFS(tmpDir)
			projectFS := projectFS{FS: fs, rootPath: tmpDir}

			re := regexp.MustCompile(pyRouteRegex)
			processor := NewPythonRouteProcessor()
			routes, err := walkProjectForRoutes(projectFS, []string{".py"}, []string{"__pycache__"}, re, processor, 2, 2)
			if err != nil {
				t.Fatal(err)
			}

			if len(routes) != len(tt.expectedRoutes) {
				t.Errorf("Expected %d routes, got %d", len(tt.expectedRoutes), len(routes))
				for i, route := range routes {
					t.Logf("Route %d: Method=%s, Path=%s", i, route.Method, route.Path)
				}
				return
			}

			// Check that each expected route is present (order doesn't matter)
			for _, expectedRoute := range tt.expectedRoutes {
				found := false
				for _, actualRoute := range routes {
					if actualRoute.Method == expectedRoute.Method && actualRoute.Path == expectedRoute.Path {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected route not found: Method=%s, Path=%s", expectedRoute.Method, expectedRoute.Path)
				}
			}
		})
	}
}
