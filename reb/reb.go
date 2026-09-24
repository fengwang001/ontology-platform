// Package reb 实现带检查点的物化视图幂等重建状态机。依赖 agg。
package reb

import (
	"errors"
	"sync"

	"ontology/agg"
)

// 哨兵错误，四种故障互不相同。
var (
	ErrBadChunk    = errors.New("reb: chunk < 1")
	ErrBusy        = errors.New("reb: already building")
	ErrNotBuilding = errors.New("reb: not building")
	ErrIncomplete  = errors.New("reb: commit while incomplete")
)

type checkpoint struct {
	processed int
	shadow    map[string]int
}

// Rebuilder 持有 committed/gen/shadow/checkpoint/processed/building 全部状态。
// applied 为非导出计数器：累计应用到 shadow 的 src 项数（含续跑重复应用）。
type Rebuilder struct {
	mu        sync.RWMutex
	src       []string
	chunk     int
	committed map[string]int
	gen       int
	shadow    map[string]int
	cp        checkpoint
	processed int
	building  bool
	applied   int
}

// New 构造 Rebuilder；chunk < 1 拒绝（ErrBadChunk）。
func New(src []string, chunk int) (*Rebuilder, error) {
	if chunk < 1 {
		return nil, ErrBadChunk
	}
	return &Rebuilder{
		src:       src,
		chunk:     chunk,
		committed: map[string]int{},
		cp:        checkpoint{0, map[string]int{}},
	}, nil
}

// Start 开始/继续重建：从检查点恢复 shadow 与 processed。
func (r *Rebuilder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.building {
		return ErrBusy
	}
	r.building = true
	r.shadow = agg.Clone(r.cp.shadow)
	r.processed = r.cp.processed
	return nil
}

// Step 处理一个块并写检查点；已处理完则空操作返回 nil。
func (r *Rebuilder) Step() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.building {
		return ErrNotBuilding
	}
	if r.processed == len(r.src) {
		return nil
	}
	end := min(r.processed+r.chunk, len(r.src))
	agg.Apply(r.shadow, r.src[r.processed:end])
	r.applied += end - r.processed
	r.processed = end
	r.cp = checkpoint{r.processed, agg.Clone(r.shadow)}
	return nil
}

// Commit 处理完后原子切换：committed=copy(shadow)、gen++、building=false。
func (r *Rebuilder) Commit() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.building {
		return 0, ErrNotBuilding
	}
	if r.processed < len(r.src) {
		return 0, ErrIncomplete
	}
	r.committed = agg.Clone(r.shadow)
	r.gen++
	r.building = false
	r.shadow = nil
	return r.gen, nil
}

// Crash 模拟崩溃：building=false、shadow 丢弃，检查点保持不变。
func (r *Rebuilder) Crash() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.building {
		return ErrNotBuilding
	}
	r.building = false
	r.shadow = nil
	return nil
}

// View 返回 committed 的副本；重建中的 shadow 不可见。
func (r *Rebuilder) View() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return agg.Clone(r.committed)
}

// Gen 返回当前代数。
func (r *Rebuilder) Gen() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gen
}
