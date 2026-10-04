// Package routing 定义工艺路线与返工回流点。
package routing

import (
	"errors"
	"sync"
)

// 全系统共享的拒绝原因，按拒绝次序排列，调用方用 errors.Is 区分。
var (
	ErrInvalid   = errors.New("invalid argument")
	ErrForbidden = errors.New("not authorized")
	ErrNotFound  = errors.New("route or work order not found")
	ErrState     = errors.New("work order state conflict")
	ErrOverflow  = errors.New("quantity exceeds queue cell")
	ErrReworkCap = errors.New("rework limit exceeded")
)

// Route 是一条已定义的工艺路线。
// 工序编号对外为 1..N；内部切片下标 0..N-1。
type Route struct {
	N    int
	Back []int
	Insp []bool
	R    int
}

// Manager 管理路线注册表。
type Manager struct {
	mu     sync.Mutex
	routes map[string]*Route
}

// NewManager 创建空的路线管理器。
func NewManager() *Manager {
	return &Manager{routes: make(map[string]*Route)}
}

// Define 定义一条路线。route 为空或路线号重复均拒绝。
// n 须在 1..32，R 在 0..1000；back/insp 长度均须为 n，
// 且对每个工序 i（1 基）满足 1 <= back[i] <= i。
func (m *Manager) Define(route string, n int, back []int, insp []bool, R int) error {
	if route == "" || n < 1 || n > 32 || R < 0 || R > 1000 ||
		len(back) != n || len(insp) != n {
		return ErrInvalid
	}
	cpBack := make([]int, n)
	cpInsp := make([]bool, n)
	for i := 0; i < n; i++ {
		if back[i] < 1 || back[i] > i+1 {
			return ErrInvalid
		}
		cpBack[i] = back[i]
		cpInsp[i] = insp[i]
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.routes[route]; ok {
		return ErrState
	}
	m.routes[route] = &Route{N: n, Back: cpBack, Insp: cpInsp, R: R}
	return nil
}

// Get 返回路线的拷贝；不存在返回 ErrNotFound。
func (m *Manager) Get(route string) (*Route, error) {
	if route == "" {
		return nil, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[route]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *r
	cp.Back = append([]int(nil), r.Back...)
	cp.Insp = append([]bool(nil), r.Insp...)
	return &cp, nil
}

// Lookup 返回内部路线指针（路线一经 Define 不可变，可安全直接读）；
// 不存在或 route 为空时返回 nil。
func (m *Manager) Lookup(route string) *Route {
	if route == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.routes[route]
}
