// Package plugin is the public contract for prod provider plugins — the SDK a
// third party implements to add a deploy target ("prod-provider-acme") without
// forking prod. A plugin is the cloud-service half of a managed-container deploy:
// prod (the host) keeps the LLM, the analyzer, and the docker build+push (to the
// registry the plugin names), and calls the plugin only to create/poll/roll-back the
// service. THE PLUGIN NEVER SEES THE USER'S SOURCE — it receives an image reference.
//
// This package imports nothing from prod's internal packages so it can be imported by
// external plugin modules. Implement Provider, then call Serve(provider) in main().
package plugin

import "context"

// ProtocolVersion is the plugin contract version. The host and plugin must agree on
// it (via the go-plugin handshake); bump it on any breaking change to Provider or the
// request/response types so old plugins are rejected with a clear message.
const ProtocolVersion = 1

// Provider is the interface a prod provider plugin implements. Every method may be
// called in a fresh subprocess, so implementations must be self-contained (resolve
// their own cloud credentials from the user's ambient config, like the built-in
// adapters read ~/.aws).
type Provider interface {
	// Metadata describes the platform: display name, natural-language aliases, the
	// hostname suffix for framework host allow-lists, and capabilities. Called first.
	Metadata(ctx context.Context) (Meta, error)

	// RegistryInfo returns where and how the host should push the built image. The
	// host runs the docker build+push with these credentials; the plugin then deploys
	// from the resulting image reference.
	RegistryInfo(ctx context.Context, project string) (RegistryInfo, error)

	// CheckAuth reports whether the user's credentials for this cloud are usable, so
	// prod can fail fast with a clear message before building.
	CheckAuth(ctx context.Context) (AuthStatus, error)

	// Deploy creates or updates the service from a pushed image and returns it once
	// serving. It owns all cloud-specific work: the service create/update, secrets,
	// public access, and readiness polling.
	Deploy(ctx context.Context, req DeployRequest) (DeployResult, error)

	// PreviousDeployment returns the deployment to roll back to (the one before the
	// current), or an empty ID if there's nothing to roll back to. Optional — a
	// provider without rollback returns an empty result and sets Meta.SupportsRollback
	// false.
	PreviousDeployment(ctx context.Context, appName string) (DeployInfo, error)

	// Rollback reverts the app to targetID (from PreviousDeployment). Optional.
	Rollback(ctx context.Context, appName, targetID string) error
}

// Meta describes a provider platform.
type Meta struct {
	Name             string   // display name, e.g. "Acme Cloud"
	Aliases          []string // lowercase natural-language matches, e.g. "acme", "acme-cloud"
	DomainSuffix     string   // default hostname suffix, e.g. ".acme.app" (framework host allow-lists)
	SupportsRollback bool
}

// RegistryInfo tells the host where to push the built image. The host tags and pushes
// as Host + "/" + Repository and authenticates with Username/Token.
type RegistryInfo struct {
	Host       string // registry host, e.g. "registry.acme.app"
	Repository string // namespaced repository for this app, e.g. "team/my-app"
	Username   string
	Token      string
}

// AuthStatus is the result of a credential check.
type AuthStatus struct {
	OK     bool
	Detail string // how it's configured, or why it isn't usable
}

// DeployRequest is what the host hands the plugin to create the service. It is the
// ONLY app data the plugin receives — an image reference plus this app's config.
type DeployRequest struct {
	ImageRef  string            // the pushed image, e.g. "registry.acme.app/team/my-app:1720000000"
	Name      string            // service name (sanitized)
	Port      int               // the container port the app listens on
	PlainEnv  map[string]string // non-sensitive env vars
	SecretEnv map[string]string // sensitive env vars (store as the cloud's secrets, not plain)
}

// DeployResult is the created service.
type DeployResult struct {
	ID   string // cloud resource id
	Name string
	URL  string // the public https URL
}

// DeployInfo describes a prior deployment (a rollback target).
type DeployInfo struct {
	ID     string
	Status string
}
