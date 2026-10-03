package gate

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cluster"
	"ontology/meta"
)

var (
	ErrInvalid     = errors.New("参数非法")
	ErrExists      = errors.New("已存在")
	ErrNoNode      = errors.New("无节点")
	ErrNotFound    = errors.New("不存在")
	ErrReadOnly    = errors.New("节点只读")
	ErrUnsupported = errors.New("字段不支持")
	ErrNodeBehind  = errors.New("节点落后")
	ErrAckFailed   = errors.New("确认失败")
	ErrResidual    = errors.New("有残留")
)

// AckFunc 在格式级别提升时对每个在册节点发起确认。
// 实现方不得回调本服务的任何方法。
type AckFunc func(node string, target int) error

// ReleaseFunc 在提升失败时对已确认节点逆序撤销；返回值被忽略。
type ReleaseFunc func(node string, target int)

// PutFields 表达一次写入的字段集合。指针/map 为 nil 表示“缺省”，
// 指向空串或空 map 表示“显式提供了空值”，二者语义不同。
type PutFields struct {
	Size int64
	Etag *string
	Tags map[string]string
}

// GetResult 是按读取节点 max 过滤后的视图。
type GetResult struct {
	Level    int
	Size     int64
	HasSize  bool
	Etag     *string
	Tags     map[string]string
	Degraded bool // 记录级别高于该节点能读的最高级别
}

// Stats 为集群级别与各级别记录条数。
type Stats struct {
	G          int
	CountByLvl [3]int
}

type Gate struct {
	mu      sync.RWMutex
	g       int
	nodes   *cluster.Registry
	store   *meta.Store
	ack     AckFunc
	release ReleaseFunc
}

// New 创建闸门。ack 必须注入；release 为 nil 时视为空操作。
func New(ack AckFunc, release ReleaseFunc) *Gate {
	if release == nil {
		release = func(string, int) {}
	}
	return &Gate{
		g:       1,
		nodes:   cluster.New(),
		store:   meta.New(),
		ack:     ack,
		release: release,
	}
}

// NodeBehindError 指明落后的节点。
type NodeBehindError struct {
	Node string
}

func (e *NodeBehindError) Error() string { return fmt.Sprintf("节点落后（%s）", e.Node) }
func (e *NodeBehindError) Unwrap() error { return ErrNodeBehind }

// AckFailureError 指明首个 Ack 失败的节点（整体确认为“确认失败”）。
type AckFailureError struct {
	Node string
	Err  error
}

func (e *AckFailureError) Error() string { return fmt.Sprintf("确认失败（%s）", e.Node) }
func (e *AckFailureError) Unwrap() error { return ErrAckFailed }

// ResidualError 带残留条数与其中的最高级别。
type ResidualError struct {
	Count     int
	HighLevel int
}

func (e *ResidualError) Error() string {
	return fmt.Sprintf("有残留（%d 条，最高级别 %d）", e.Count, e.HighLevel)
}
func (e *ResidualError) Unwrap() error { return ErrResidual }

func (g *Gate) Join(name string, max int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return mapClusterErr(g.nodes.Join(name, max))
}

func (g *Gate) Leave(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return mapClusterErr(g.nodes.Leave(name)) // Leave 不改变 G
}

func mapClusterErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cluster.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, cluster.ErrExists):
		return ErrExists
	case errors.Is(err, cluster.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, cluster.ErrNoNode):
		return ErrNoNode
	default:
		return err
	}
}

func (g *Gate) currentG() int { return g.g }

// Put 以当前 G 写入记录。降级节点拒绝写；提供了最低级别>G 的字段拒绝写。
func (g *Gate) Put(node, key string, f PutFields) error {
	if key == "" || f.Size < 0 || f.Size > 1e12 {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	n, ok := g.nodes.Get(node)
	if !ok {
		return ErrNotFound
	}
	if n.Max < g.g {
		return ErrReadOnly
	}
	if f.Etag != nil && g.g < meta.LevelEtag {
		return ErrUnsupported
	}
	if f.Tags != nil && g.g < meta.LevelTags {
		return ErrUnsupported
	}
	return g.store.Put(key, meta.Record{
		Level: g.g,
		Size:  f.Size,
		Etag:  f.Etag,
		Tags:  f.Tags,
	})
}

// Get 返回字段集合 = 记录中最低级别不大于 node.max 的字段；
// 记录级别大于 node.max 时 Degraded=true。
func (g *Gate) Get(node, key string) (GetResult, error) {
	if key == "" || node == "" {
		return GetResult{}, ErrInvalid
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes.Get(node)
	if !ok {
		return GetResult{}, ErrNotFound
	}
	rec, ok := g.store.Get(key)
	if !ok {
		return GetResult{}, ErrNotFound
	}
	res := GetResult{Level: rec.Level, Degraded: rec.Level > n.Max}
	if meta.LevelSize <= n.Max {
		res.Size, res.HasSize = rec.Size, true
	}
	if rec.Etag != nil && meta.LevelEtag <= n.Max {
		res.Etag = rec.Etag
	}
	if rec.Tags != nil && meta.LevelTags <= n.Max {
		res.Tags = rec.Tags
	}
	return res, nil
}

// Delete 任何在册节点都可执行，不要求读得懂记录。
func (g *Gate) Delete(node, key string) error {
	if key == "" || node == "" {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes.Get(node); !ok {
		return ErrNotFound
	}
	if err := g.store.Delete(key); err != nil {
		if errors.Is(err, meta.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// Raise 单步提升到 G+1，两阶段确认，任一 Ack 失败则逆序 Release，
// G 与一切状态不变。整段持锁，故并发 Join/Leave/Put 只能排在整体之前
// 或之后；Ack 针对调用时刻的在册节点快照。
func (g *Gate) Raise(target int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if target != g.g+1 || target > 3 {
		return ErrInvalid
	}
	snap := g.nodes.Snapshot()
	if len(snap) == 0 {
		return ErrNoNode
	}
	minMax, minName, _ := g.nodes.MinMax()
	if target > minMax {
		return &NodeBehindError{Node: minName}
	}
	acked := make([]string, 0, len(snap))
	for _, n := range snap {
		if err := g.ack(n.Name, target); err != nil {
			for i := len(acked) - 1; i >= 0; i-- {
				g.release(acked[i], target) // 返回值忽略
			}
			return &AckFailureError{Node: n.Name, Err: err}
		}
		acked = append(acked, n.Name)
	}
	g.g = target
	return nil
}

// Rollback 将 G 降回 target（1≤target<G）；存在 level>target 的记录时
// 以级别计数 O(1) 判定为有残留并拒绝，任何状态不变。
func (g *Gate) Rollback(target int) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if target < 1 || target >= g.g {
		return ErrInvalid
	}
	if res, dirty := g.store.Residue(target); dirty {
		return &ResidualError{Count: res.Count, HighLevel: res.HighLevel}
	}
	g.g = target
	return nil
}

func (g *Gate) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	st := g.store.Stats()
	return Stats{G: g.g, CountByLvl: st.CountByLvl}
}

// Scanned 返回底层存储朴素扫描路径的累计扫描数。正式回退路径只读
// 级别计数器，因此在任意规模下恒为 0。
func (g *Gate) Scanned() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.store.Scanned()
}
