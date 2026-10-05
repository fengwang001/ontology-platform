// Package locrib 实现最优路径选择、导出规则与通告差量。
package locrib

import (
	"sort"
	"sync"

	"ontology/adjrib"
	"ontology/policy"
)

// 类型别名，便于调用方只导入本包。
type (
	Prefix     = policy.Prefix
	Attrs      = policy.Attrs
	Term       = policy.Term
	Match      = policy.Match
	Mods       = policy.Mods
	PrefixCond = policy.PrefixCond
	Action     = policy.Action
)

const (
	ActionAccept = policy.ActionAccept
	ActionReject = policy.ActionReject
	ActionNext   = policy.ActionNext
)

// 哨兵错误（与 adjrib 同源，errors.Is 可区分）。
var (
	ErrInvalidParam  = adjrib.ErrInvalidParam
	ErrPeerNotFound  = adjrib.ErrPeerNotFound
	ErrPeerExists    = adjrib.ErrPeerExists
	ErrRouteNotFound = adjrib.ErrRouteNotFound
	ErrPrefixLimit   = adjrib.ErrPrefixLimit
)

// Change 为一条通告差量：Withdraw 为真表示撤销，否则为通告（带新属性）。
type Change struct {
	Peer     uint32
	Prefix   Prefix
	Withdraw bool
	Attrs    Attrs
}

// ExportEntry 为 Export 全量中的一条。
type ExportEntry struct {
	Prefix Prefix
	Attrs  Attrs
}

type peerInfo struct {
	id       uint32
	as       uint32
	routerID uint32
	internal bool
	rib      *adjrib.PeerRIB
}

// Engine 为路径向量路由引擎。所有方法可并发调用，等价于某个串行顺序。
type Engine struct {
	mu       sync.Mutex
	localAS  uint32
	pmax     int
	peers    map[uint32]*peerInfo
	byPrefix map[Prefix]map[uint32]Attrs // 前缀 -> 邻居 -> 策略后路由
	exported map[uint32]map[Prefix]Attrs // 邻居 -> 前缀 -> 当前导出属性
	compared uint64
}

// New 创建引擎：localAS 为本机 AS，Pmax 为每邻居原始表容量。
func New(localAS uint32, pmax int) *Engine {
	return &Engine{
		localAS:  localAS,
		pmax:     pmax,
		peers:    make(map[uint32]*peerInfo),
		byPrefix: make(map[Prefix]map[uint32]Attrs),
		exported: make(map[uint32]map[Prefix]Attrs),
	}
}

// Compared 返回最优路径选择累计考察的策略后路由条数。
func (e *Engine) Compared() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compared
}

// AddPeer 添加邻居：id 1..10^6，routerID 非零；as 等于 localAS 为内部邻居。
func (e *Engine) AddPeer(id, as, routerID uint32) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == 0 || id > 1000000 || routerID == 0 {
		return ErrInvalidParam
	}
	if _, ok := e.peers[id]; ok {
		return ErrPeerExists
	}
	internal := as == e.localAS
	e.peers[id] = &peerInfo{
		id:       id,
		as:       as,
		routerID: routerID,
		internal: internal,
		rib:      adjrib.NewPeerRIB(e.localAS, as, internal, e.pmax),
	}
	e.exported[id] = make(map[Prefix]Attrs)
	return nil
}

// Update 接收邻居路由：先存原始表，再求策略后路由；返回通告差量。
func (e *Engine) Update(peer uint32, p Prefix, attrs Attrs) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !policy.ValidPrefix(p) || !policy.ValidAttrs(attrs) {
		return nil, ErrInvalidParam
	}
	pi, ok := e.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	if err := pi.rib.Update(p, attrs); err != nil {
		return nil, err
	}
	e.syncPrefix(pi, p)
	return e.recompute([]Prefix{p}), nil
}

// Withdraw 撤销邻居路由；原始表无此项报 ErrRouteNotFound。
func (e *Engine) Withdraw(peer uint32, p Prefix) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !policy.ValidPrefix(p) {
		return nil, ErrInvalidParam
	}
	pi, ok := e.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	if err := pi.rib.Withdraw(p); err != nil {
		return nil, err
	}
	e.syncPrefix(pi, p)
	return e.recompute([]Prefix{p}), nil
}

// SetPolicy 整体替换邻居导入策略，并用原始表对全部前缀重新求值。
func (e *Engine) SetPolicy(peer uint32, terms []Term) ([]Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !policy.ValidTerms(terms) {
		return nil, ErrInvalidParam
	}
	pi, ok := e.peers[peer]
	if !ok {
		return nil, ErrPeerNotFound
	}
	pi.rib.SetPolicy(terms)
	prefixes := pi.rib.RawPrefixes()
	for _, p := range prefixes {
		e.syncPrefix(pi, p)
	}
	return e.recompute(prefixes), nil
}

// syncPrefix 把邻居某前缀的策略后路由同步进 byPrefix 索引。
func (e *Engine) syncPrefix(pi *peerInfo, p Prefix) {
	post, ok := pi.rib.Post(p)
	m := e.byPrefix[p]
	if !ok {
		if m != nil {
			delete(m, pi.id)
			if len(m) == 0 {
				delete(e.byPrefix, p)
			}
		}
		return
	}
	if m == nil {
		m = make(map[uint32]Attrs)
		e.byPrefix[p] = m
	}
	m[pi.id] = post
}

// recompute 对受影响前缀重算最优与全部邻居的导出，与快照比较产生差量。
func (e *Engine) recompute(prefixes []Prefix) []Change {
	var changes []Change
	for _, p := range prefixes {
		best, hasBest := e.bestPath(p)
		for id := range e.peers {
			now, ok := e.exportTo(id, best, hasBest)
			old, had := e.exported[id][p]
			switch {
			case ok && had:
				if !policy.EqualAttrs(old, now) {
					changes = append(changes, Change{Peer: id, Prefix: p, Attrs: now})
					e.exported[id][p] = now
				}
			case ok:
				changes = append(changes, Change{Peer: id, Prefix: p, Attrs: now})
				e.exported[id][p] = now
			case had:
				changes = append(changes, Change{Peer: id, Prefix: p, Withdraw: true})
				delete(e.exported[id], p)
			}
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		a, b := changes[i], changes[j]
		if a.Peer != b.Peer {
			return a.Peer < b.Peer
		}
		if a.Prefix.Addr != b.Prefix.Addr {
			return a.Prefix.Addr < b.Prefix.Addr
		}
		return a.Prefix.Len < b.Prefix.Len
	})
	return changes
}

// Export 返回邻居当前应收到的全量通告，按（前缀地址，前缀长度）升序。
func (e *Engine) Export(peer uint32) ([]ExportEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.peers[peer]; !ok {
		return nil, ErrPeerNotFound
	}
	m := e.exported[peer]
	out := make([]ExportEntry, 0, len(m))
	for p, a := range m {
		out = append(out, ExportEntry{Prefix: p, Attrs: a})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Prefix.Addr != out[j].Prefix.Addr {
			return out[i].Prefix.Addr < out[j].Prefix.Addr
		}
		return out[i].Prefix.Len < out[j].Prefix.Len
	})
	return out, nil
}
