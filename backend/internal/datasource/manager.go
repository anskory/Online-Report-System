// Package datasource 连接池管理器：按数据源 ID 缓存 *sql.DB，懒加载、可失效。
package datasource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

type poolEntry struct {
	db        *sql.DB
	version   string
	createdAt time.Time
}

// Manager 连接池注册表
type Manager struct {
	mu    sync.RWMutex
	pools map[int64]*poolEntry
}

func NewManager() *Manager {
	return &Manager{pools: map[int64]*poolEntry{}}
}

// Get 获取（或建立）数据源连接，返回连接与数据库版本描述
func (m *Manager) Get(ctx context.Context, dbType string, cfg ConnConfig, dsID int64) (*sql.DB, string, error) {
	m.mu.RLock()
	entry, ok := m.pools[dsID]
	m.mu.RUnlock()
	if ok {
		return entry.db, entry.version, nil
	}

	driver, err := Get(dbType)
	if err != nil {
		return nil, "", err
	}
	dsn, err := driver.BuildDSN(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("构建 DSN 失败: %w", err)
	}
	db, err := driver.Open(dsn, cfg)
	if err != nil {
		return nil, "", fmt.Errorf("打开连接失败: %w", err)
	}
	// 探测连通性与版本
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	version, err := driver.ServerVersion(pingCtx, db)
	if err != nil {
		db.Close()
		return nil, "", fmt.Errorf("连接测试失败: %w", wrapPingErr(err))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// 双检：并发加载时复用先到者
	if e, ok := m.pools[dsID]; ok {
		db.Close()
		return e.db, e.version, nil
	}
	m.pools[dsID] = &poolEntry{db: db, version: version, createdAt: time.Now()}
	return db, version, nil
}

// Invalidate 数据源配置变更/删除后失效对应连接池
func (m *Manager) Invalidate(dsID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.pools[dsID]; ok {
		e.db.Close()
		delete(m.pools, dsID)
	}
}

// pingTimeout 连接探测超时。首次连接 Oracle（加载 Instant Client + TNS 握手 + 认证）
// 在跨网段/VPN 环境下可能超过 8 秒，故放宽到 15 秒。
const pingTimeout = 15 * time.Second

// tcpPrecheck 建库连接前的 TCP 预检：快速区分「网络不通」与「数据库握手/认证问题」。
// host 为空（如 SQLite 嵌入式）时跳过。
func tcpPrecheck(ctx context.Context, cfg ConnConfig) error {
	if cfg.Host == "" {
		return nil
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("TCP 无法连通 %s（请检查主机地址、端口、防火墙或 VPN）: %w", addr, err)
	}
	_ = conn.Close()
	return nil
}

// wrapPingErr 把探测错误转成带排查提示的可读错误
func wrapPingErr(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("连接超时（15 秒内未完成握手：可能是网络慢、数据库监听器未启动、或服务名/端口不对）")
	}
	return err
}

// TestConnection 使用临时连接测试连通性（不占用池），返回延迟与版本
func TestConnection(ctx context.Context, dbType string, cfg ConnConfig) (latencyMs int64, version string, err error) {
	driver, err := Get(dbType)
	if err != nil {
		return 0, "", err
	}
	dsn, err := driver.BuildDSN(cfg)
	if err != nil {
		return 0, "", err
	}
	if err := tcpPrecheck(ctx, cfg); err != nil {
		return 0, "", err
	}
	db, err := driver.Open(dsn, cfg)
	if err != nil {
		return 0, "", err
	}
	defer db.Close()

	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err = db.PingContext(pingCtx); err != nil {
		return 0, "", fmt.Errorf("连接失败: %w", wrapPingErr(err))
	}
	latencyMs = time.Since(start).Milliseconds()
	version, err = driver.ServerVersion(pingCtx, db)
	return latencyMs, version, err
}

// CloseAll 服务退出时关闭全部连接池
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.pools {
		e.db.Close()
		delete(m.pools, id)
	}
}
