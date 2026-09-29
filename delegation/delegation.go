package delegation

import (
	"log"
	"sync"
	"time"
)

// Manager 管理权限委托图，所有方法均可被并发调用。
type Manager struct {
	mu    sync.RWMutex
	edges map[string]*Edge
	roots map[Subject]map[Permission]bool
	now   func() time.Time
	logf  func(string, ...any)
}

// NewManager 创建委托管理器。roots 声明各主体直接持有（非委托获得）的权限。
func NewManager(roots map[Subject][]Permission) *Manager {
	rootSet := make(map[Subject]map[Permission]bool)
	for s, perms := range roots {
		set := make(map[Permission]bool, len(perms))
		for _, p := range perms {
			set[p] = true
		}
		rootSet[s] = set
	}
	return &Manager{
		edges: make(map[string]*Edge),
		roots: rootSet,
		now:   time.Now,
		logf:  func(format string, args ...any) { log.Printf(format, args...) },
	}
}

// WithClock 注入自定义时钟（主要用于测试有效期），返回管理器自身。
func (m *Manager) WithClock(now func() time.Time) *Manager {
	m.now = now
	return m
}

// WithLogger 注入自定义日志函数，传入 nil 可关闭日志，返回管理器自身。
func (m *Manager) WithLogger(logf func(string, ...any)) *Manager {
	if logf == nil {
		m.logf = func(string, ...any) {}
	} else {
		m.logf = logf
	}
	return m
}

// GrantRoot 声明主体直接持有某项权限（非委托来源），可作为委托链起点。
func (m *Manager) GrantRoot(s Subject, p Permission) {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, ok := m.roots[s]
	if !ok {
		set = make(map[Permission]bool)
		m.roots[s] = set
	}
	set[p] = true
}

func (m *Manager) log(format string, args ...any) {
	m.logf(format, args...)
}
