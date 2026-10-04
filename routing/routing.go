// Package routing 定义工艺路线与返工回流点。
package routing

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrInvalidParam 参数非法：标识为空、n/R 越界、back/insp 长度或取值不符。
	ErrInvalidParam = errors.New("routing: invalid parameter")
	// ErrNotFound 路线不存在。
	ErrNotFound = errors.New("routing: route not found")
	// ErrConflict 路线号重复。
	ErrConflict = errors.New("routing: route id conflict")
)

const (
	// MaxOps 工序数上限。
	MaxOps = 32
	// MaxReworkLimit 每件返工次数上限的最大值。
	MaxReworkLimit = 1000
)

// Route 工艺路线：工序编号 1..N。
// Back[i-1] 为工序 i 判返工时的回流工序（1 ≤ Back[i-1] ≤ i）；
// Insp[i-1] 标记工序 i 是否为检验点；R 为每件允许的返工次数上限。
// Route 创建后不可变，调用方不得修改其切片字段。
type Route struct {
	ID   string
	N    int
	Back []int
	Insp []bool
	R    int
}

// Registry 并发安全的路线注册表。
type Registry struct {
	mu     sync.RWMutex
	routes map[string]*Route
}

// NewRegistry 创建空的路线注册表。
func NewRegistry() *Registry {
	return &Registry{routes: make(map[string]*Route)}
}

// Define 定义一条路线。路线号重复报 ErrConflict。
func (r *Registry) Define(id string, n int, back []int, insp []bool, R int) error {
	if id == "" || n < 1 || n > MaxOps || len(back) != n || len(insp) != n ||
		R < 0 || R > MaxReworkLimit {
		return fmt.Errorf("%w: id=%q n=%d R=%d", ErrInvalidParam, id, n, R)
	}
	for j, b := range back {
		if b < 1 || b > j+1 {
			return fmt.Errorf("%w: back[%d]=%d, need 1..%d", ErrInvalidParam, j, b, j+1)
		}
	}
	rt := &Route{
		ID:   id,
		N:    n,
		Back: append([]int(nil), back...),
		Insp: append([]bool(nil), insp...),
		R:    R,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[id]; ok {
		return fmt.Errorf("%w: %q", ErrConflict, id)
	}
	r.routes[id] = rt
	return nil
}

// Get 按路线号查询路线。
func (r *Registry) Get(id string) (*Route, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rt, ok := r.routes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return rt, nil
}
