package xray

import (
	"context"
	"fmt"

	"remnawave-node-lite-go/internal/xtls"
)

func (m *Manager) handlerAPI(ctx context.Context) (*xtls.HandlerAPI, func(), error) {
	m.mu.RLock()
	online := m.xrayOnline
	socket := m.xtlsSocket
	m.mu.RUnlock()

	if !online {
		return nil, nil, fmt.Errorf("xray is not online")
	}

	client, err := xtls.NewClient(socket)
	if err != nil {
		return nil, nil, err
	}

	api := xtls.NewHandlerAPI(client.Conn())
	return api, func() { _ = client.Close() }, nil
}

func (m *Manager) HandlerAddVlessUser(ctx context.Context, tag, username, uuid, flow string, level uint32) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.AddVlessUser(ctx, tag, username, uuid, flow, level)
}

func (m *Manager) HandlerAddTrojanUser(ctx context.Context, tag, username, password string, level uint32) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.AddTrojanUser(ctx, tag, username, password, level)
}

func (m *Manager) HandlerAddShadowsocksUser(ctx context.Context, tag, username, password string, cipherType int, ivCheck bool, level uint32) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.AddShadowsocksUser(ctx, tag, username, password, cipherType, ivCheck, level)
}

func (m *Manager) HandlerAddShadowsocks2022User(ctx context.Context, tag, username, key string, level uint32) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.AddShadowsocks2022User(ctx, tag, username, key, level)
}

func (m *Manager) HandlerAddHysteriaUser(ctx context.Context, tag, username, auth string, level uint32) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.AddHysteriaUser(ctx, tag, username, auth, level)
}

func (m *Manager) HandlerRemoveOutbound(ctx context.Context, tag string) error {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	return api.RemoveOutbound(ctx, tag)
}

func (m *Manager) HandlerRemoveUser(ctx context.Context, tag, username string) xtls.HandlerResult {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.RemoveUser(ctx, tag, username)
}

func (m *Manager) HandlerGetInboundUsers(ctx context.Context, tag string) ([]xtls.InboundUser, xtls.HandlerResult) {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return nil, xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.GetInboundUsers(ctx, tag)
}

func (m *Manager) HandlerGetInboundUsersCount(ctx context.Context, tag string) (int64, xtls.HandlerResult) {
	api, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return 0, xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.GetInboundUsersCount(ctx, tag)
}

func (m *Manager) RemoveTorrentBlockerOutbound() error {
	return m.HandlerRemoveOutbound(context.Background(), torrentBlockerOutboundTag)
}

func (m *Manager) StopIfOnline() bool {
	m.mu.RLock()
	online := m.xrayOnline
	m.mu.RUnlock()
	if !online {
		return false
	}
	return m.Stop(false).IsStopped
}
