// Package repl 编排多区域全互联双向复制：创建预检、入队、投递应用与分歧判定。
package repl

import (
	"errors"
	"sync"

	"ontology/link"
	"ontology/region"
)

// Mode 创建时的积压取舍模式。
type Mode int

const (
	// Strict 任一应入队链路超限则整体拒绝。
	Strict Mode = iota
	// Relaxed 本地照常创建，超限链路跳过并计丢失。
	Relaxed
)

// Params 集群参数。
type Params struct {
	MarkerRepl bool
	Mode       Mode
	Capacity   int64
	RetryLimit int
}

var (
	// ErrInvalidParam 参数非法。
	ErrInvalidParam = errors.New("参数非法")
	// ErrNoRegion 区域不存在。
	ErrNoRegion = errors.New("区域不存在")
	// ErrBacklog 积压超限。
	ErrBacklog = errors.New("积压超限")
)

// Cluster 一组全互联复制的区域。所有操作串行化，可并发调用。
type Cluster struct {
	mu     sync.Mutex
	params Params

	names   []string
	regions map[string]*region.Region
	links   map[edge]*link.Link
}

type edge struct{ src, dst string }

// New 构造 2–4 个互不相同的非空区域。
func New(names []string, p Params) (*Cluster, error) {
	if len(names) < 2 || len(names) > 4 || p.Capacity < 1 || p.Capacity > 1e12 ||
		p.RetryLimit < 1 || p.RetryLimit > 100 {
		return nil, ErrInvalidParam
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			return nil, ErrInvalidParam
		}
		seen[name] = true
	}
	c := &Cluster{
		params:  p,
		names:   append([]string(nil), names...),
		regions: make(map[string]*region.Region, len(names)),
		links:   make(map[edge]*link.Link),
	}
	for _, name := range names {
		c.regions[name] = region.New(name)
	}
	for _, src := range names {
		for _, dst := range names {
			if src != dst {
				c.links[edge{src, dst}] = link.New(p.Capacity, p.RetryLimit)
			}
		}
	}
	return c, nil
}

// Regions 返回区域名快照。
func (c *Cluster) Regions() []string { return append([]string(nil), c.names...) }

// Put 在区域 r 创建数据版本。
func (c *Cluster) Put(r, key string, size, ts int64) (region.Version, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateCreate(key, size, ts); err != nil {
		return region.Version{}, err
	}
	if !c.known(r) {
		return region.Version{}, ErrNoRegion
	}
	targets, err := c.plan(r, size, false)
	if err != nil {
		return region.Version{}, err
	}
	v := c.regions[r].Put(key, size, ts)
	c.fanOut(v, targets)
	return v, nil
}

// Delete 在区域 r 创建删除标记。
func (c *Cluster) Delete(r, key string, ts int64) (region.Version, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := validateCreate(key, 0, ts); err != nil {
		return region.Version{}, err
	}
	if !c.known(r) {
		return region.Version{}, ErrNoRegion
	}
	targets, err := c.plan(r, 0, true)
	if err != nil {
		return region.Version{}, err
	}
	v := c.regions[r].Delete(key, ts)
	c.fanOut(v, targets)
	return v, nil
}

// Get 读取区域 r 上 key 的当前版本。
func (c *Cluster) Get(r, key string) (region.Version, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r == "" || key == "" {
		return region.Version{}, ErrInvalidParam
	}
	reg, ok := c.regions[r]
	if !ok {
		return region.Version{}, ErrNoRegion
	}
	return reg.Get(key)
}

// Diverged 各区域当前版本标识不全相同为真（全部无该键为假）。
func (c *Cluster) Diverged(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if key == "" {
		return false
	}
	var cur region.VersionID
	present := false
	for _, name := range c.names {
		v, ok := c.regions[name].Current(key)
		if !ok {
			return true // 某区域无该键
		}
		if !present {
			cur, present = v.ID, true
		} else if v.ID != cur {
			return true
		}
	}
	return false // 全部无键（present=false）也返回假
}

// Deliver 在 src→dst 链路上从队首起至多 n 次 Send。
func (c *Cluster) Deliver(src, dst string, n int, send func(link.Item) (bool, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n < 1 || n > 1000 || src == "" || dst == "" || src == dst ||
		!c.known(src) || !c.known(dst) {
		return ErrInvalidParam
	}
	if send == nil {
		return ErrInvalidParam
	}
	l := c.links[edge{src, dst}]
	reg := c.regions[dst]
	l.DeliverN(n, send, func(it link.Item) bool {
		return reg.Apply(region.Version{
			ID:     it.ID,
			Key:    it.Key,
			Size:   it.Size,
			TS:     it.TS,
			Marker: it.Marker,
		})
	})
	return nil
}

// Retry 把失败项重新放回 src→dst 链路队尾。
func (c *Cluster) Retry(src, dst string, id region.VersionID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 次序：参数非法 > 不存在 > 积压超限。
	if src == "" || dst == "" || src == dst || id.Origin == "" || id.Seq < 1 ||
		!c.known(src) || !c.known(dst) {
		return ErrInvalidParam
	}
	l := c.links[edge{src, dst}]
	ok, found := l.Retry(id)
	if !found {
		return link.ErrNotFound
	}
	if !ok {
		return ErrBacklog
	}
	return nil
}

// Backlog 返回指定链路的当前积压字节。
func (c *Cluster) Backlog(src, dst string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.link(src, dst)
	if err != nil {
		return 0, err
	}
	return l.Backlog(), nil
}

// Lost 返回指定链路的 Relaxed 丢失计数。
func (c *Cluster) Lost(src, dst string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.link(src, dst)
	if err != nil {
		return 0, err
	}
	return l.Lost(), nil
}

// Duplicates 返回区域 r 的重复 Apply 计数。
func (c *Cluster) Duplicates(r string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reg, ok := c.regions[r]
	if !ok {
		return 0, false
	}
	return reg.Duplicates(), true
}

// ApplyProbes 返回区域 r 的 Apply 触碰记录数。
func (c *Cluster) ApplyProbes(r string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reg, ok := c.regions[r]
	if !ok {
		return 0, false
	}
	return reg.ApplyProbes(), true
}

// LinkStats 返回指定链路的恒等式四项计数（用于测试不变量）。
func (c *Cluster) LinkStats(src, dst string) (enqueued, delivered, failedTransfers, pending int64, backlog int64, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, e := c.link(src, dst)
	if e != nil {
		return 0, 0, 0, 0, 0, e
	}
	return l.Enqueued(), l.Delivered(), l.FailedTransfers(), int64(l.Pending()), l.Backlog(), nil
}

// FailedIDs 返回指定链路失败列表中的版本标识快照。
func (c *Cluster) FailedIDs(src, dst string) ([]region.VersionID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	l, err := c.link(src, dst)
	if err != nil {
		return nil, err
	}
	failed := l.Failed()
	ids := make([]region.VersionID, 0, len(failed))
	for _, it := range failed {
		ids = append(ids, it.ID)
	}
	return ids, nil
}

func (c *Cluster) known(r string) bool {
	_, ok := c.regions[r]
	return ok
}

func (c *Cluster) link(src, dst string) (*link.Link, error) {
	if src == "" || dst == "" || src == dst || !c.known(src) || !c.known(dst) {
		return nil, ErrInvalidParam
	}
	return c.links[edge{src, dst}], nil
}

func validateCreate(key string, size, ts int64) error {
	if key == "" || size < 0 || size > 1e9 || ts < 0 || ts > 1e12 {
		return ErrInvalidParam
	}
	return nil
}

// plan 执行创建前的全链路预算决策，返回允许入队的目的区域。
// marker=true 且 MarkerRepl 关闭时无应入队链路。要求调用方持有 c.mu。
func (c *Cluster) plan(origin string, size int64, marker bool) ([]string, error) {
	if marker && !c.params.MarkerRepl {
		return nil, nil
	}
	targets := c.targets(origin)
	if c.params.Mode == Strict {
		for _, dst := range targets {
			if c.links[edge{origin, dst}].Backlog()+size > c.params.Capacity {
				return nil, ErrBacklog
			}
		}
		return targets, nil
	}
	accepted := make([]string, 0, len(targets))
	for _, dst := range targets {
		l := c.links[edge{origin, dst}]
		if l.Backlog()+size > c.params.Capacity {
			l.AddLost() // Relaxed：该版本永不复制到该链路。
		} else {
			accepted = append(accepted, dst)
		}
	}
	return accepted, nil
}

// fanOut 把已创建的本地版本直连复制到预算放行的各目的链路。
// 预算在 plan 阶段已确认；持锁期间积压未变，Enqueue 必成功。
func (c *Cluster) fanOut(v region.Version, targets []string) {
	for _, dst := range targets {
		c.links[edge{v.ID.Origin, dst}].Enqueue(link.Item{
			ID:     v.ID,
			Key:    v.Key,
			Size:   v.Size,
			TS:     v.TS,
			Marker: v.Marker,
		})
	}
}

func (c *Cluster) targets(origin string) []string {
	out := make([]string, 0, len(c.names)-1)
	for _, name := range c.names {
		if name != origin {
			out = append(out, name)
		}
	}
	return out
}
