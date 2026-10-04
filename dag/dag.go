package dag

import (
	"errors"
	"sort"
	"sync"
)

// 哨兵错误：所有包共用 dag 包中的定义，errors.Is 可直接区分。
var (
	ErrInvalid         = errors.New("dag: invalid argument")
	ErrFrozen          = errors.New("dag: graph frozen after first Land or Evaluate")
	ErrExists          = errors.New("dag: dataset already exists")
	ErrNoDataset       = errors.New("dag: dataset not found")
	ErrParentNotFound  = errors.New("dag: parent dataset not found")
	ErrTooManyDatasets = errors.New("dag: dataset limit exceeded")
	ErrClockRewind     = errors.New("fresh: clock cannot go backwards")
	ErrAlready         = errors.New("fresh: period already landed")
	ErrOutOfOrder      = errors.New("fresh: period landed out of order")
	ErrTooEarly        = errors.New("fresh: land time earlier than period start")
	ErrUpstreamMissing = errors.New("fresh: upstream parent period not landed yet")
	ErrNotAlerted      = errors.New("blame: period never alerted")
)

const MaxDatasets = 10000

// Dataset 是一个已登记数据集的静态描述。
type Dataset struct {
	Name    string
	Off     int64
	Dur     int64
	Parents []string // 按名字节序升序保存
}

// Deadline 返回数据集 d 第 k 期的截止时刻 k*T+off。
func (g *Graph) Deadline(name string, k int64) int64 {
	return k*g.T + g.ds[name].Off
}

// Graph 保存数据集拓扑。Parents 只能引用更早登记的数据集，故天然无环。
type Graph struct {
	mu     sync.RWMutex
	T      int64
	order  []string
	ds     map[string]*Dataset
	frozen bool
}

// NewGraph 构造周期长度为 T 秒的图；T 越界返回 ErrInvalid。
func NewGraph(T int64) (*Graph, error) {
	if T < 1 || T > 1_000_000 {
		return nil, ErrInvalid
	}
	return &Graph{T: T, ds: make(map[string]*Dataset)}, nil
}

// Lock/Unlock 供嵌入 Graph 的上层（fresh/blame）在复合操作期间持锁，
// 使 Land/Evaluate 等跨结构操作等价于某个串行顺序。
func (g *Graph) Lock()   { g.mu.Lock() }
func (g *Graph) Unlock() { g.mu.Unlock() }

// Frozen 报告图是否已因第一次 Land/Evaluate 被冻结。
func (g *Graph) Frozen() bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.frozen
}

// Freeze 在第一次被接受的 Land/Evaluate 时调用。
func (g *Graph) Freeze() {
	g.mu.Lock()
	g.frozen = true
	g.mu.Unlock()
}

// FreezeLocked 是 Freeze 的不加锁版本，调用方须持有 Graph 锁。
func (g *Graph) FreezeLocked() {
	g.frozen = true
}

// AddDataset 登记一个数据集。拒绝次序：参数非法 > ErrFrozen > 已存在 > 父不存在 > 超限。
// 被拒绝时不修改任何状态。
func (g *Graph) AddDataset(name string, off, dur int64, parents []string) error {
	if name == "" || off < 1 || off > g.T || dur < 0 || dur > g.T || len(parents) > 8 {
		return ErrInvalid
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.frozen {
		return ErrFrozen
	}
	if _, ok := g.ds[name]; ok {
		return ErrExists
	}
	for i, p := range parents {
		if p == "" {
			return ErrInvalid
		}
		if i > 0 && parents[i] == parents[i-1] {
			return ErrInvalid // 重复父（调用方可能未排序，先做无序查重）
		}
		if _, ok := g.ds[p]; !ok {
			return ErrParentNotFound
		}
	}
	// 无序重复父的补充查重。
	seen := make(map[string]struct{}, len(parents))
	for _, p := range parents {
		if _, dup := seen[p]; dup {
			return ErrInvalid
		}
		seen[p] = struct{}{}
	}
	if len(g.order) >= MaxDatasets {
		return ErrTooManyDatasets
	}

	ps := append([]string(nil), parents...)
	sort.Strings(ps)
	g.ds[name] = &Dataset{Name: name, Off: off, Dur: dur, Parents: ps}
	g.order = append(g.order, name)
	return nil
}

// Dataset 返回某数据集的静态描述，不存在时第二返回值为 false。
func (g *Graph) Dataset(name string) (Dataset, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.datasetLocked(name)
}

// DatasetLocked 是 Dataset 的不加锁版本，调用方须持有 Graph 锁。
func (g *Graph) DatasetLocked(name string) (Dataset, bool) {
	return g.datasetLocked(name)
}

func (g *Graph) datasetLocked(name string) (Dataset, bool) {
	d, ok := g.ds[name]
	if !ok {
		return Dataset{}, false
	}
	return *d, true
}

// Has 报告数据集是否已登记。
func (g *Graph) Has(name string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.hasLocked(name)
}

// HasLocked 是 Has 的不加锁版本。
func (g *Graph) HasLocked(name string) bool {
	return g.hasLocked(name)
}

func (g *Graph) hasLocked(name string) bool {
	_, ok := g.ds[name]
	return ok
}

// Names 返回按登记顺序排列的全部数据集名。
func (g *Graph) Names() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.namesLocked()
}

// NamesLocked 是 Names 的不加锁版本，调用方须持有 Graph 锁。
func (g *Graph) NamesLocked() []string {
	return g.namesLocked()
}

func (g *Graph) namesLocked() []string {
	return append([]string(nil), g.order...)
}
