package analyzer

import (
	"os"
	"testing"
)

func javaAnalyzer(dir string) *JavaAnalyzer {
	return NewJavaAnalyzer(projectFS{FS: os.DirFS(dir), rootPath: dir}).(*JavaAnalyzer)
}

func TestJavaAnalyzer_CanHandle(t *testing.T) {
	t.Run("pom.xml present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"pom.xml": "<project></project>"})
		if ok, err := javaAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("build.gradle present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"build.gradle": "plugins {}"})
		if ok, err := javaAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("build.gradle.kts present", func(t *testing.T) {
		dir := writeRubyFixture(t, map[string]string{"build.gradle.kts": "plugins {}"})
		if ok, err := javaAnalyzer(dir).CanHandle(); err != nil || !ok {
			t.Fatalf("CanHandle = %v, %v; want true", ok, err)
		}
	})
	t.Run("bare .java file is NOT enough", func(t *testing.T) {
		// A stray .java isn't a deployable project; requiring a build manifest avoids mislabeling
		// non-Java repos that happen to carry an incidental source file.
		dir := writeRubyFixture(t, map[string]string{"App.java": "class App {}"})
		if ok, _ := javaAnalyzer(dir).CanHandle(); ok {
			t.Error("a bare .java file (no pom.xml/build.gradle) should NOT be handled as Java")
		}
	})
}

func TestJavaAnalyzer_MavenSpringBoot(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
    <modelVersion>4.0.0</modelVersion>
    <parent>
        <groupId>org.springframework.boot</groupId>
        <artifactId>spring-boot-starter-parent</artifactId>
        <version>3.2.0</version>
    </parent>
    <groupId>com.example</groupId>
    <artifactId>widget-service</artifactId>
    <version>0.0.1-SNAPSHOT</version>
    <dependencies>
        <dependency>
            <groupId>org.springframework.boot</groupId>
            <artifactId>spring-boot-starter-web</artifactId>
        </dependency>
        <dependency>
            <groupId>org.springframework.boot</groupId>
            <artifactId>spring-boot-starter-data-jpa</artifactId>
        </dependency>
        <dependency>
            <groupId>org.postgresql</groupId>
            <artifactId>postgresql</artifactId>
        </dependency>
        <dependency>
            <groupId>org.flywaydb</groupId>
            <artifactId>flyway-core</artifactId>
        </dependency>
    </dependencies>
</project>
`,
		"src/main/java/com/example/WidgetController.java": `package com.example;

import org.springframework.web.bind.annotation.*;
import org.springframework.beans.factory.annotation.Value;

@RestController
@RequestMapping("/api")
public class WidgetController {
    @Value("${API_KEY}")
    private String apiKey;

    @GetMapping("/health")
    public String health() {
        String db = System.getenv("DATABASE_URL");
        return "ok" + db;
    }

    @PostMapping("/widgets")
    public String create() {
        return "created";
    }
}
`,
	})

	spec, err := javaAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "java" {
		t.Errorf("Language = %q, want java", spec.Language)
	}
	if spec.Name != "widget-service" {
		t.Errorf("Name = %q, want widget-service (<artifactId>)", spec.Name)
	}
	if spec.BuildCommand != "mvn -q -DskipTests package" {
		t.Errorf("BuildCommand = %q, want the Maven package command (no wrapper)", spec.BuildCommand)
	}
	if !hasFramework(spec.ServiceRequirements, "spring-boot") {
		t.Errorf("expected a spring-boot framework marker; got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("postgresql/JPA should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if !hasEnv(spec.EnvVars, "DATABASE_URL") || !hasEnv(spec.EnvVars, "API_KEY") {
		t.Errorf("expected System.getenv and @Value env vars; got %+v", spec.EnvVars)
	}
	if !hasRoute(spec.Routes, "GET", "/api") {
		t.Errorf("expected class-level @RequestMapping GET /api; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "GET", "/health") {
		t.Errorf("expected GET /health from @GetMapping; got %+v", spec.Routes)
	}
	if !hasRoute(spec.Routes, "POST", "/widgets") {
		t.Errorf("expected POST /widgets from @PostMapping; got %+v", spec.Routes)
	}
	// A web app must not be mislabeled a worker.
	if spec.DetectedShape == "worker" {
		t.Errorf("a Spring Boot web app should not be detected as a worker")
	}
	// Spring auto-runs migrations, so no MigrationCommand.
	if spec.MigrationCommand != "" {
		t.Errorf("expected empty MigrationCommand (Spring auto-migrate); got %q", spec.MigrationCommand)
	}
	// Flyway on the classpath should still be reported as a configured tool.
	foundTool := false
	for _, tool := range spec.MigrationContext.ORMTools {
		if tool == "flyway" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("expected flyway in configured ORM tools; got %+v", spec.MigrationContext.ORMTools)
	}
}

func TestJavaAnalyzer_GradleSpringBoot(t *testing.T) {
	dir := writeRubyFixture(t, map[string]string{
		"settings.gradle": `rootProject.name = 'gradle-svc'`,
		"build.gradle": `plugins {
    id 'org.springframework.boot' version '3.2.0'
    id 'java'
}

dependencies {
    implementation 'org.springframework.boot:spring-boot-starter-web'
    implementation 'org.springframework.boot:spring-boot-starter-data-jpa'
    runtimeOnly 'org.postgresql:postgresql'
}
`,
	})

	spec, err := javaAnalyzer(dir).Analyze()
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if spec.Language != "java" {
		t.Errorf("Language = %q, want java", spec.Language)
	}
	if spec.Name != "gradle-svc" {
		t.Errorf("Name = %q, want gradle-svc (rootProject.name)", spec.Name)
	}
	if spec.BuildCommand != "gradle bootJar" {
		t.Errorf("BuildCommand = %q, want the Gradle bootJar command (no wrapper)", spec.BuildCommand)
	}
	if !hasFramework(spec.ServiceRequirements, "spring-boot") {
		t.Errorf("expected a spring-boot framework marker (via plugin id); got %+v", spec.ServiceRequirements)
	}
	if !hasService(spec.ServiceRequirements, ServicePostgres) {
		t.Errorf("postgresql should imply Postgres; got %+v", spec.ServiceRequirements)
	}
	if spec.DetectedShape == "worker" {
		t.Errorf("a Spring Boot web app should not be detected as a worker")
	}
}

// The Java analyzer must not claim a Node/Python/Go/Ruby/Rust project.
func TestJavaAnalyzer_NoFalseMatch(t *testing.T) {
	cases := map[string]map[string]string{
		"node":   {"package.json": `{"name":"x"}`, "index.js": "console.log(1)"},
		"python": {"requirements.txt": "flask\n", "app.py": "print(1)"},
		"go":     {"go.mod": "module x\n", "main.go": "package main\nfunc main(){}"},
		"ruby":   {"Gemfile": "source 'https://rubygems.org'\n", "app.rb": "puts 1"},
		"rust":   {"Cargo.toml": "[package]\nname = \"x\"\n", "src/main.rs": "fn main(){}"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeRubyFixture(t, files)
			if ok, _ := javaAnalyzer(dir).CanHandle(); ok {
				t.Errorf("Java analyzer should not handle a %s project", name)
			}
		})
	}
}
