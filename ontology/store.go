package ontology

import (
	"sort"
	"sync"
)

// Snapshot 是图在某一固定时点的不可变快照（写时复制产生）。
// 续读标记锚定某个快照后，后续遍历永远基于该快照，不受之后变更影响。
type Snapshot struct {
	epoch       uint64
	objects     map[ObjectID]Object
	objectTypes map[string]ObjectType
	linkTypes   map[LinkTypeID]LinkType
	// outgoing[id] 为 from == id 的链接；incoming[id] 为 to == id 的链接。
	outgoing map[ObjectID][]Link
	incoming map[ObjectID][]Link
}

// Epoch 返回快照的单调版本号。
func (s *Snapshot) Epoch() uint64 { return s.epoch }

func (s *Snapshot) linkType(id LinkTypeID) LinkType {
	if lt, ok := s.linkTypes[id]; ok {
		return lt
	}
	// 未注册的链接类型按“代价 0、仅正向”处理，保证图仍可遍历。
	return LinkType{ID: id, Direction: DirectionOut, Cost: 0}
}

func (s *Snapshot) clone(epoch uint64) *Snapshot {
	objects := make(map[ObjectID]Object, len(s.objects))
	for k, v := range s.objects {
		objects[k] = v
	}
	objectTypes := make(map[string]ObjectType, len(s.objectTypes))
	for k, v := range s.objectTypes {
		objectTypes[k] = v
	}
	linkTypes := make(map[LinkTypeID]LinkType, len(s.linkTypes))
	for k, v := range s.linkTypes {
		linkTypes[k] = v
	}
	outgoing := make(map[ObjectID][]Link, len(s.outgoing))
	for k, v := range s.outgoing {
		outgoing[k] = v
	}
	incoming := make(map[ObjectID][]Link, len(s.incoming))
	for k, v := range s.incoming {
		incoming[k] = v
	}
	return &Snapshot{
		epoch:       epoch,
		objects:     objects,
		objectTypes: objectTypes,
		linkTypes:   linkTypes,
		outgoing:    outgoing,
		incoming:    incoming,
	}
}

// Store 是支持多版本快照的图存储。每次变更产生新快照；
// 历史快照被保留以供续读标记锚定（可用 PruneHistory 释放）。
type Store struct {
	mu        sync.RWMutex
	policy    Policy
	current   *Snapshot
	history   map[uint64]*Snapshot
	nextEpoch uint64
}

// NewStore 创建空存储，policy 为该存储使用的权限模型。
func NewStore(policy Policy) *Store {
	root := &Snapshot{
		epoch:       0,
		objects:     map[ObjectID]Object{},
		objectTypes: map[string]ObjectType{},
		linkTypes:   map[LinkTypeID]LinkType{},
		outgoing:    map[ObjectID][]Link{},
		incoming:    map[ObjectID][]Link{},
	}
	if policy == nil {
		policy = AllowAllPolicy{}
	}
	return &Store{
		policy:    policy,
		current:   root,
		history:   map[uint64]*Snapshot{0: root},
		nextEpoch: 1,
	}
}

// Policy 返回存储绑定的权限模型。
func (s *Store) Policy() Policy { return s.policy }

// Current 返回当前最新快照。
func (s *Store) Current() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SnapshotAt 返回指定版本的历史快照。
func (s *Store) SnapshotAt(epoch uint64) (*Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.history[epoch]
	return snap, ok
}

// mutate 在写锁内串行执行“克隆当前快照 → 修改 → 提交”，
// 使所有图变更等价于某个全局串行顺序。
func (s *Store) mutate(apply func(snap *Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := s.current.clone(s.nextEpoch)
	apply(snap)
	s.current = snap
	s.history[snap.epoch] = snap
	s.nextEpoch = snap.epoch + 1
}

// AddObjectType 注册对象类型。
func (s *Store) AddObjectType(t ObjectType) {
	s.mutate(func(snap *Snapshot) { snap.objectTypes[t.ID] = t })
}

// AddLinkType 注册链接类型。
func (s *Store) AddLinkType(t LinkType) {
	s.mutate(func(snap *Snapshot) { snap.linkTypes[t.ID] = t })
}

// AddObject 在新快照中添加对象；对象已存在时为幂等更新。
func (s *Store) AddObject(o Object) {
	s.mutate(func(snap *Snapshot) { snap.objects[o.ID] = o })
}

// AddLink 在新快照中添加链接。邻接表按目标对象标识保持有序，
// 使遍历器能在取得 fanout+1 个有效后继后提前停止扫描。
func (s *Store) AddLink(l Link) {
	s.mutate(func(snap *Snapshot) {
		snap.outgoing[l.From] = append(snap.outgoing[l.From], l)
		snap.incoming[l.To] = append(snap.incoming[l.To], l)
		sortAdjacency(snap.outgoing[l.From], true)
		sortAdjacency(snap.incoming[l.To], false)
	})
}

func sortAdjacency(links []Link, asOutgoing bool) {
	sort.Slice(links, func(i, j int) bool {
		ti, tj := links[i].To, links[j].To
		if !asOutgoing {
			ti, tj = links[i].From, links[j].From
		}
		if ti != tj {
			return ti < tj
		}
		if links[i].Type != links[j].Type {
			return links[i].Type < links[j].Type
		}
		return links[i].From < links[j].From ||
			(links[i].From == links[j].From && links[i].To < links[j].To)
	})
}

// DeleteObject 在新快照中删除对象及其全部关联链接。
func (s *Store) DeleteObject(id ObjectID) {
	s.mutate(func(snap *Snapshot) {
		delete(snap.objects, id)

		filter := func(links []Link) []Link {
			kept := links[:0]
			for _, l := range links {
				if l.From != id && l.To != id {
					kept = append(kept, l)
				}
			}
			return kept
		}

		outgoing := make(map[ObjectID][]Link, len(snap.outgoing))
		for node, links := range snap.outgoing {
			if node == id {
				continue
			}
			if kept := filter(append([]Link(nil), links...)); len(kept) > 0 {
				outgoing[node] = kept
			}
		}
		incoming := make(map[ObjectID][]Link, len(snap.incoming))
		for node, links := range snap.incoming {
			if node == id {
				continue
			}
			if kept := filter(append([]Link(nil), links...)); len(kept) > 0 {
				incoming[node] = kept
			}
		}
		snap.outgoing = outgoing
		snap.incoming = incoming
	})
}

// PruneHistory 释放早于 keepEpoch 的历史快照；当前快照永不释放。
// 锚定被释放快照的续读标记之后将按“不可追溯”处理。
func (s *Store) PruneHistory(keepEpoch uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for epoch := range s.history {
		if epoch < keepEpoch && epoch != s.current.epoch {
			delete(s.history, epoch)
		}
	}
}
