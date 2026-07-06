package plugin

import (
	"errors"

	goplugin "github.com/hashicorp/go-plugin"
)

// Serve runs a provider plugin, blocking until the host disconnects. A plugin's
// main() implements Provider and calls plugin.Serve(myProvider).
func Serve(impl Provider) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         pluginSet(impl),
	})
}

// HostPlugins is the go-plugin PluginSet the host uses to dispense a Provider client
// (the Impl is nil host-side).
func HostPlugins() goplugin.PluginSet {
	return goplugin.PluginSet{pluginKey: &ProviderPlugin{}}
}

// Dispense returns the Provider RPC client from a launched plugin connection.
func Dispense(conn goplugin.ClientProtocol) (Provider, error) {
	raw, err := conn.Dispense(pluginKey)
	if err != nil {
		return nil, err
	}
	prov, ok := raw.(Provider)
	if !ok {
		return nil, errors.New("plugin does not implement the provider interface")
	}
	return prov, nil
}
