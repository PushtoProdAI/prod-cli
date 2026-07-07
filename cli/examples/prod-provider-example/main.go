// Command prod-provider-example is a reference prod provider plugin. Copy it to build
// a real provider: implement the six plugin.Provider methods against your cloud's API
// and call plugin.Serve. prod discovers the built binary (named prod-provider-*) via
// `prod plugin install` and drives it over a subprocess — no fork of prod required.
//
// This example "deploys" by echoing a fake URL; a real provider would create a service
// from req.ImageRef against its cloud.
package main

import (
	"context"
	"fmt"

	plugin "github.com/pushtoprodai/prod-plugin-sdk"
)

type exampleProvider struct{}

func (exampleProvider) Metadata(context.Context) (plugin.Meta, error) {
	return plugin.Meta{
		Name:             "Example",
		Aliases:          []string{"example", "example-cloud"},
		DomainSuffix:     ".example.dev",
		SupportsRollback: false,
	}, nil
}

func (exampleProvider) RegistryInfo(_ context.Context, project string) (plugin.RegistryInfo, error) {
	// A real provider returns its container registry + push credentials; the host
	// builds and pushes the image there before calling Deploy.
	return plugin.RegistryInfo{
		Host:       "registry.example.dev",
		Repository: "apps/" + project,
		Username:   "example",
		Token:      "example-token",
	}, nil
}

func (exampleProvider) CheckAuth(context.Context) (plugin.AuthStatus, error) {
	return plugin.AuthStatus{OK: true, Detail: "example: no real credentials required"}, nil
}

func (exampleProvider) Deploy(_ context.Context, req plugin.DeployRequest) (plugin.DeployResult, error) {
	// A real provider creates/updates a service from req.ImageRef and waits until it
	// serves. Here we just echo a URL.
	return plugin.DeployResult{
		ID:   "example-" + req.Name,
		Name: req.Name,
		URL:  fmt.Sprintf("https://%s.example.dev", req.Name),
	}, nil
}

func (exampleProvider) PreviousDeployment(context.Context, string) (plugin.DeployInfo, error) {
	return plugin.DeployInfo{}, nil
}

func (exampleProvider) Rollback(context.Context, string, string) error {
	return fmt.Errorf("the example provider does not support rollback")
}

func main() {
	plugin.Serve(exampleProvider{})
}
