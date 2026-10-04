package adjrib

import (
	"sort"

	"ontology/policy"
)

// Route 是一条策略后路由及其来源信息。
type Route struct {
	PeerID   int
	PeerAS   uint32
	RouterID uint32
	Internal bool
	Prefix   policy.Prefix
	Attrs    policy.Attrs
}

// PeerRIB 保存一个邻居的原始表与策略后表。
type PeerRIB struct {
	Peer *policy.Peer
	raw  map[policy.Prefix]policy.Attrs
	post map[policy.Prefix]policy.Attrs
}

type Table struct {
	localAS uint32
	peers   map[int]*PeerRIB
}

func New(localAS uint32) *Table {
	return &Table{localAS: localAS, peers: map[int]*PeerRIB{}}
}

func (t *Table) AddPeer(p *policy.Peer) {
	t.peers[p.ID] = &PeerRIB{
		Peer: p,
		raw:  map[policy.Prefix]policy.Attrs{},
		post: map[policy.Prefix]policy.Attrs{},
	}
}

func (t *Table) peer(id int) *PeerRIB { return t.peers[id] }

// PeerExists 判断邻居是否存在。
func (t *Table) PeerExists(id int) bool { _, ok := t.peers[id]; return ok }

// LookupPeer 返回邻居定义（导出时取内外属性）。
func (t *Table) LookupPeer(id int) *policy.Peer {
	if pr, ok := t.peers[id]; ok {
		return pr.Peer
	}
	return nil
}

// AllPrefixes 返回全表（任意邻居原始表）出现过的前缀，供全量导出枚举。
func (t *Table) AllPrefixes() []policy.Prefix {
	seen := make(map[policy.Prefix]struct{})
	for _, pr := range t.peers {
		for p := range pr.post {
			seen[p] = struct{}{}
		}
	}
	out := make([]policy.Prefix, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Addr != out[j].Addr {
			return out[i].Addr < out[j].Addr
		}
		return out[i].Len < out[j].Len
	})
	return out
}

// ReplacePolicy 重设策略并用原始表重算全部前缀。
func (t *Table) ReplacePolicy(id int, terms []policy.Term) []policy.Prefix {
	pr := t.peer(id)
	pr.Peer.Policy = terms
	affected := make([]policy.Prefix, 0, len(pr.raw))
	for p, raw := range pr.raw {
		t.evalOne(pr, p, raw)
		affected = append(affected, p)
	}
	return affected
}

func (t *Table) evalOne(pr *PeerRIB, p policy.Prefix, raw policy.Attrs) {
	out, ok := policy.Apply(raw, p, pr.Peer.Policy, t.localAS, pr.Peer.AS, !pr.Peer.Internal)
	if ok {
		pr.post[p] = out
	} else {
		delete(pr.post, p)
	}
}

// RawHas 判断原始表是否持有前缀（Pmax 判定用）。
func (t *Table) RawHas(id int, p policy.Prefix) bool {
	_, ok := t.peer(id).raw[p]
	return ok
}

// RawCount 返回该邻居原始表条数（Pmax 检查用）。
func (t *Table) RawCount(id int) int { return len(t.peer(id).raw) }

// SetRaw 覆盖原始表一项。
func (t *Table) SetRaw(id int, p policy.Prefix, a policy.Attrs) {
	pr := t.peer(id)
	pr.raw[p] = a
	t.evalOne(pr, p, a)
}

// Delete 删除原始与策略后路由，返回是否曾有原始项。
func (t *Table) Delete(id int, p policy.Prefix) bool {
	pr := t.peer(id)
	if _, ok := pr.raw[p]; !ok {
		return false
	}
	delete(pr.raw, p)
	delete(pr.post, p)
	return true
}

// PostRoutes 返回某前缀当前全部策略后路由。
func (t *Table) PostRoutes(p policy.Prefix) []Route {
	out := make([]Route, 0, len(t.peers))
	for _, pr := range t.peers {
		if a, ok := pr.post[p]; ok {
			out = append(out, Route{
				PeerID: pr.Peer.ID, PeerAS: pr.Peer.AS, RouterID: pr.Peer.RouterID,
				Internal: pr.Peer.Internal, Prefix: p, Attrs: a,
			})
		}
	}
	return out
}

// HolderCount 返回持有某前缀策略后路由的邻居数。
func (t *Table) HolderCount(p policy.Prefix) int {
	n := 0
	for _, pr := range t.peers {
		if _, ok := pr.post[p]; ok {
			n++
		}
	}
	return n
}

// PeerIDs 返回全部邻居 id。
func (t *Table) PeerIDs() []int {
	ids := make([]int, 0, len(t.peers))
	for id := range t.peers {
		ids = append(ids, id)
	}
	return ids
}

// Prefixes 返回某邻居原始表的全部前缀（SetPolicy 重算用）。
func (t *Table) Prefixes(id int) []policy.Prefix {
	pr := t.peer(id)
	out := make([]policy.Prefix, 0, len(pr.raw))
	for p := range pr.raw {
		out = append(out, p)
	}
	return out
}
