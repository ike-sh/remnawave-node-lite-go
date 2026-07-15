package xray

import (
	"context"
	"fmt"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/xtls"
)

func (m *Manager) handlerAPI(ctx context.Context) (*xtls.HandlerAPI, uint64, func(), error) {
	m.mu.RLock()
	online := m.state == lifecycleRunning
	socket := m.xtlsSocket
	generation := m.generation
	m.mu.RUnlock()

	if !online {
		return nil, 0, nil, fmt.Errorf("xray is not online")
	}

	client, err := xtls.NewClient(socket)
	if err != nil {
		return nil, 0, nil, err
	}

	api := xtls.NewHandlerAPI(client.Conn())
	return api, generation, func() { _ = client.Close() }, nil
}

func (m *Manager) HandlerAddVlessUser(ctx context.Context, tag, username, uuid, flow string, level uint32) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.AddVlessUser(ctx, tag, username, uuid, flow, level), generation)
}

func (m *Manager) HandlerAddTrojanUser(ctx context.Context, tag, username, password string, level uint32) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.AddTrojanUser(ctx, tag, username, password, level), generation)
}

func (m *Manager) HandlerAddShadowsocksUser(ctx context.Context, tag, username, password string, cipherType int, ivCheck bool, level uint32) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.AddShadowsocksUser(ctx, tag, username, password, cipherType, ivCheck, level), generation)
}

func (m *Manager) HandlerAddShadowsocks2022User(ctx context.Context, tag, username, key string, level uint32) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.AddShadowsocks2022User(ctx, tag, username, key, level), generation)
}

func (m *Manager) HandlerAddHysteriaUser(ctx context.Context, tag, username, auth string, level uint32) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.AddHysteriaUser(ctx, tag, username, auth, level), generation)
}

func (m *Manager) HandlerRemoveOutbound(ctx context.Context, tag string) error {
	api, _, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return err
	}
	defer closeFn()
	return api.RemoveOutbound(ctx, tag)
}

func (m *Manager) HandlerRemoveUser(ctx context.Context, tag, username string) xtls.HandlerResult {
	api, generation, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return withGeneration(api.RemoveUser(ctx, tag, username), generation)
}

func (m *Manager) HandlerGetInboundUsers(ctx context.Context, tag string) ([]xtls.InboundUser, xtls.HandlerResult) {
	api, _, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return nil, xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.GetInboundUsers(ctx, tag)
}

func (m *Manager) HandlerGetInboundUsersCount(ctx context.Context, tag string) (int64, xtls.HandlerResult) {
	api, _, closeFn, err := m.handlerAPI(ctx)
	if err != nil {
		return 0, xtls.HandlerResult{OK: false, Message: err.Error()}
	}
	defer closeFn()
	return api.GetInboundUsersCount(ctx, tag)
}

func withGeneration(result xtls.HandlerResult, generation uint64) xtls.HandlerResult {
	result.Generation = generation
	return result
}

func (m *Manager) RemoveTorrentBlockerOutbound() error {
	m.mu.RLock()
	online := m.state == lifecycleRunning
	m.mu.RUnlock()
	if !online {
		return nil
	}
	return m.HandlerRemoveOutbound(context.Background(), torrentBlockerOutboundTag)
}

func (m *Manager) StopIfOnline() error {
	m.mu.RLock()
	stopped := m.state == lifecycleStopped
	m.mu.RUnlock()
	if stopped {
		return nil
	}
	if !m.Stop().IsStopped {
		return fmt.Errorf("stop rw-core: process did not stop")
	}
	return nil
}
