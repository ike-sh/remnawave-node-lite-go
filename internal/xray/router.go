package xray

import (
	"context"
	"fmt"

	"remnawave-node-lite-go/internal/xtls"
)

func (m *Manager) RouterAddSrcIPRule(ctx context.Context, ip string, appendRule bool) error {
	api, closeFn, err := m.routerAPI(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	return api.AddSrcIPRule(ctx, ip, xtls.HashIPRuleTag(ip), appendRule)
}

func (m *Manager) RouterRemoveRuleByIP(ctx context.Context, ip string) error {
	api, closeFn, err := m.routerAPI(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	return api.RemoveRuleByTag(ctx, xtls.HashIPRuleTag(ip))
}

func (m *Manager) routerAPI(ctx context.Context) (*xtls.RouterAPI, func(), error) {
	m.mu.RLock()
	online := m.xrayOnline
	port := m.xtlsAPIPort
	certs := m.internalCerts
	m.mu.RUnlock()

	if !online {
		return nil, nil, errXrayOffline()
	}

	client, err := xtls.NewClient(clientAddr(port), xtls.TLSCredentials{
		CACertPEM:     certs.CACertPEM,
		ClientCertPEM: certs.ClientCertPEM,
		ClientKeyPEM:  certs.ClientKeyPEM,
	})
	if err != nil {
		return nil, nil, err
	}

	api := xtls.NewRouterAPI(client.Conn())
	return api, func() { _ = client.Close() }, nil
}

func errXrayOffline() error {
	return fmt.Errorf("xray is not online")
}

func clientAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}
