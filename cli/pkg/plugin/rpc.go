package plugin

import (
	"context"
	"errors"
	"net/rpc"

	goplugin "github.com/hashicorp/go-plugin"
)

// pluginKey is the dispensed plugin name in the go-plugin PluginSet.
const pluginKey = "provider"

// Handshake is the go-plugin handshake config. Host and plugin must agree on the
// magic cookie and protocol version, or the launch is rejected with a clear message.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "PROD_PLUGIN",
	MagicCookieValue: "prod-provider",
}

// pluginSet is the go-plugin plugin map for a provider implementation.
func pluginSet(impl Provider) goplugin.PluginSet {
	return goplugin.PluginSet{pluginKey: &ProviderPlugin{Impl: impl}}
}

// ProviderPlugin is the go-plugin net/rpc Plugin for a Provider. Impl is set on the
// plugin (server) side; the host side leaves it nil and gets a client.
type ProviderPlugin struct{ Impl Provider }

func (p *ProviderPlugin) Server(*goplugin.MuxBroker) (interface{}, error) {
	return &providerRPCServer{impl: p.Impl}, nil
}

func (p *ProviderPlugin) Client(_ *goplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &providerRPCClient{client: c}, nil
}

// Note: net/rpc does not carry context.Context. The plugin's methods run to
// completion; the host bounds its wait with the deploy context. Cancellation is not
// propagated into the subprocess (a v1 limitation; the gRPC transport would fix it).

// --- request/reply envelopes (gob-encoded; errors travel as strings) ---

type NoArgs struct{}

type MetaReply struct {
	Meta Meta
	Err  string
}
type (
	RegistryArgs  struct{ Project string }
	RegistryReply struct {
		Info RegistryInfo
		Err  string
	}
)

type AuthReply struct {
	Status AuthStatus
	Err    string
}
type DeployReply struct {
	Result DeployResult
	Err    string
}
type (
	PrevArgs  struct{ AppName string }
	PrevReply struct {
		Info DeployInfo
		Err  string
	}
)

type (
	RollbackArgs struct{ AppName, TargetID string }
	ErrReply     struct{ Err string }
)

func encodeErr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func decodeErr(s string) error {
	if s != "" {
		return errors.New(s)
	}
	return nil
}

// --- host-side client: implements Provider by calling over net/rpc ---

type providerRPCClient struct{ client *rpc.Client }

var _ Provider = (*providerRPCClient)(nil)

// callCtx runs a net/rpc call but honors ctx. net/rpc carries no context, so a plain
// blocking Call lets a hung or runaway plugin block a deploy forever. We fire the call
// async and race it against ctx cancellation/deadline, so the host stops waiting when the
// deploy context expires. (The subprocess itself is reaped by the host's Kill on shutdown;
// here we just bound the wait — which also caps the time a plugin streaming an oversized
// gob reply can consume, the practical bound since go-plugin builds the rpc codec for us.)
func callCtx(ctx context.Context, client *rpc.Client, method string, args, reply any) error {
	call := client.Go(method, args, reply, make(chan *rpc.Call, 1))
	select {
	case <-ctx.Done():
		return ctx.Err()
	case done := <-call.Done:
		return done.Error
	}
}

func (c *providerRPCClient) Metadata(ctx context.Context) (Meta, error) {
	var r MetaReply
	if err := callCtx(ctx, c.client, "Plugin.Metadata", NoArgs{}, &r); err != nil {
		return Meta{}, err
	}
	return r.Meta, decodeErr(r.Err)
}

func (c *providerRPCClient) RegistryInfo(ctx context.Context, project string) (RegistryInfo, error) {
	var r RegistryReply
	if err := callCtx(ctx, c.client, "Plugin.RegistryInfo", RegistryArgs{Project: project}, &r); err != nil {
		return RegistryInfo{}, err
	}
	return r.Info, decodeErr(r.Err)
}

func (c *providerRPCClient) CheckAuth(ctx context.Context) (AuthStatus, error) {
	var r AuthReply
	if err := callCtx(ctx, c.client, "Plugin.CheckAuth", NoArgs{}, &r); err != nil {
		return AuthStatus{}, err
	}
	return r.Status, decodeErr(r.Err)
}

func (c *providerRPCClient) Deploy(ctx context.Context, req DeployRequest) (DeployResult, error) {
	var r DeployReply
	if err := callCtx(ctx, c.client, "Plugin.Deploy", req, &r); err != nil {
		return DeployResult{}, err
	}
	return r.Result, decodeErr(r.Err)
}

func (c *providerRPCClient) PreviousDeployment(ctx context.Context, appName string) (DeployInfo, error) {
	var r PrevReply
	if err := callCtx(ctx, c.client, "Plugin.PreviousDeployment", PrevArgs{AppName: appName}, &r); err != nil {
		return DeployInfo{}, err
	}
	return r.Info, decodeErr(r.Err)
}

func (c *providerRPCClient) Rollback(ctx context.Context, appName, targetID string) error {
	var r ErrReply
	if err := callCtx(ctx, c.client, "Plugin.Rollback", RollbackArgs{AppName: appName, TargetID: targetID}, &r); err != nil {
		return err
	}
	return decodeErr(r.Err)
}

// --- plugin-side server: net/rpc methods delegating to the real Provider ---

type providerRPCServer struct{ impl Provider }

func (s *providerRPCServer) Metadata(_ NoArgs, r *MetaReply) error {
	m, err := s.impl.Metadata(context.Background())
	r.Meta, r.Err = m, encodeErr(err)
	return nil
}

func (s *providerRPCServer) RegistryInfo(a RegistryArgs, r *RegistryReply) error {
	info, err := s.impl.RegistryInfo(context.Background(), a.Project)
	r.Info, r.Err = info, encodeErr(err)
	return nil
}

func (s *providerRPCServer) CheckAuth(_ NoArgs, r *AuthReply) error {
	st, err := s.impl.CheckAuth(context.Background())
	r.Status, r.Err = st, encodeErr(err)
	return nil
}

func (s *providerRPCServer) Deploy(req DeployRequest, r *DeployReply) error {
	res, err := s.impl.Deploy(context.Background(), req)
	r.Result, r.Err = res, encodeErr(err)
	return nil
}

func (s *providerRPCServer) PreviousDeployment(a PrevArgs, r *PrevReply) error {
	info, err := s.impl.PreviousDeployment(context.Background(), a.AppName)
	r.Info, r.Err = info, encodeErr(err)
	return nil
}

func (s *providerRPCServer) Rollback(a RollbackArgs, r *ErrReply) error {
	r.Err = encodeErr(s.impl.Rollback(context.Background(), a.AppName, a.TargetID))
	return nil
}
