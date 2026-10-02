package mvgc

import (
	"math/rand/v2"
	"sort"
	"sync"
)

const (
	// TypePut 写入一个值（value 允许为空串）。
	TypePut byte = 'P'
	// TypeDelete 墓碑：使此前版本不可见。
	TypeDelete byte = 'D'
	// TypeLock 锁记录：Get 时跳过，可被安全点回收。
	TypeLock byte = 'L'
	// TypeRollback 回滚记录：Get 时跳过，可被安全点回收。
	TypeRollback byte = 'R'
)

const maxTS int64 = 1_000_000_000_000_000

// rec 是一条写记录。
type rec struct {
	ts    int64
	typ   byte
	value string
}

// Store 是带记录类型与安全点的多版本键值垃圾回收器。
type Store struct {
	mu        sync.Mutex
	limit     int
	count     int
	root      *treapNode
	sp        int64
	cursor    string
	haveCur   bool
	snaps     map[int]int64
	nextSnap  int
	minSnapTS int64
	access    int64
}

// New 创建记录总数上限为 R 的回收器。
func New(limit int) *Store {
	s := &Store{
		limit:     limit,
		snaps:     make(map[int]int64),
		minSnapTS: maxTS + 1,
	}
	return s
}

// Write 写入一条 P/D/L/R 记录。
func (s *Store) Write(key string, typ byte, ts int64, value string) error {
	if key == "" || (typ != TypePut && typ != TypeDelete && typ != TypeLock && typ != TypeRollback) ||
		ts < 1 || ts > maxTS || (typ != TypePut && value != "") {
		return reject(ErrInvalidArgument, "invalid write arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts <= s.sp {
		return reject(ErrExpired, "write ts not greater than safe point")
	}
	node := treapFind(s.root, key)
	if node != nil {
		if i := sort.Search(len(node.recs), func(i int) bool { return node.recs[i].ts >= ts }); i < len(node.recs) && node.recs[i].ts == ts {
			return reject(ErrDuplicate, "record with same key and ts exists")
		}
	}
	if s.count >= s.limit {
		return reject(ErrFull, "record limit reached")
	}
	r := rec{ts: ts, typ: typ, value: value}
	if node == nil {
		var n *treapNode
		s.root, n, _ = treapInsert(s.root, key, rand.Uint64())
		n.recs = append(n.recs, r)
	} else {
		i := sort.Search(len(node.recs), func(i int) bool { return node.recs[i].ts >= ts })
		node.recs = append(node.recs, rec{})
		copy(node.recs[i+1:], node.recs[i:])
		node.recs[i] = r
	}
	s.count++
	return nil
}

// Get 返回 key 在 ts 时刻可见的 P 值；D 或无可见 P 时 ok=false。
func (s *Store) Get(key string, ts int64) (value string, ok bool, err error) {
	if key == "" || ts < 1 || ts > maxTS {
		return "", false, reject(ErrInvalidArgument, "invalid get arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.sp {
		return "", false, reject(ErrExpired, "get ts below safe point")
	}
	node := treapFind(s.root, key)
	if node == nil {
		return "", false, nil
	}
	i := sort.Search(len(node.recs), func(i int) bool { return node.recs[i].ts > ts }) - 1
	for ; i >= 0; i-- {
		if node.recs[i].typ == TypePut {
			return node.recs[i].value, true, nil
		}
		if node.recs[i].typ == TypeDelete {
			return "", false, nil
		}
	}
	return "", false, nil
}

// OpenSnapshot 登记读快照并返回快照号。
func (s *Store) OpenSnapshot(ts int64) (int, error) {
	if ts < 1 || ts > maxTS {
		return 0, reject(ErrInvalidArgument, "invalid snapshot arguments")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.sp {
		return 0, reject(ErrExpired, "snapshot ts below safe point")
	}
	s.nextSnap++
	id := s.nextSnap
	s.snaps[id] = ts
	if ts < s.minSnapTS {
		s.minSnapTS = ts
	}
	return id, nil
}

// CloseSnapshot 关闭指定快照。
func (s *Store) CloseSnapshot(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.snaps[id]
	if !ok {
		return reject(ErrSnapshotNotFound, "snapshot not found")
	}
	delete(s.snaps, id)
	if old == s.minSnapTS {
		s.recomputeMinSnap()
	}
	return nil
}

func (s *Store) recomputeMinSnap() {
	min := maxTS + 1
	for _, ts := range s.snaps {
		if ts < min {
			min = ts
		}
	}
	s.minSnapTS = min
}

// SetSafePoint 单调推进安全点并开启新一轮回收。
func (s *Store) SetSafePoint(sp int64) error {
	if sp < 0 || sp > maxTS {
		return reject(ErrInvalidArgument, "invalid safe point")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sp < s.sp {
		return reject(ErrRollback, "safe point moved backwards")
	}
	if sp > s.minSnapTS {
		return reject(ErrSnapshotBlocked, "safe point exceeds oldest snapshot ts")
	}
	s.sp = sp
	// 开启新一轮回收：游标置空，此前未完成的一轮作废。
	s.haveCur = false
	s.cursor = ""
	return nil
}

// SafePoint 返回当前安全点。
func (s *Store) SafePoint() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sp
}

// GCStep 按字节序处理至多 n 个大于游标的键，返回实际处理键数。
func (s *Store) GCStep(n int) (int, error) {
	if n < 1 {
		return 0, reject(ErrInvalidArgument, "n must be >= 1")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var nodes []*treapNode
	if s.haveCur {
		treapCollectAfter(s.root, s.cursor, n, &nodes)
	} else {
		treapCollectAfter(s.root, "", n, &nodes)
		// 空串不是合法键，所有键都严格大于 ""。
	}
	if len(nodes) == 0 {
		// 没有可处理的键：本轮结束。游标保持，后续新写入的更小键留待下一轮。
		return 0, nil
	}
	for _, node := range nodes {
		s.processKey(node)
	}
	last := nodes[len(nodes)-1]
	s.cursor = last.key
	s.haveCur = true
	if len(last.recs) == 0 {
		s.root = treapDelete(s.root, last.key)
	}
	return len(nodes), nil
}

// processKey 按当前安全点回收单个键的旧版本。
// 访问数恰等于该键 ts 不大于安全点的记录条数；大于安全点的记录不被访问。
func (s *Store) processKey(node *treapNode) {
	recs := node.recs
	cut := sort.Search(len(recs), func(i int) bool { return recs[i].ts > s.sp })
	s.access += int64(cut)

	// 1) 删除全部 ts <= sp 的 L 与 R 记录。
	// 2) 取 ts <= sp 中 ts 最大的 P 或 D 记为 X：
	//    X 为 P 则保留 X，删除所有 ts < X.ts（含更小的 P/D/L/R）；
	//    X 为 D 则删除 X 本身及所有 ts < X.ts；
	//    无 X 则仅保留“删除 L/R”的结果。
	var xTS int64
	hasX := false
	xIsPut := false
	for i := cut - 1; i >= 0; i-- {
		if recs[i].typ == TypePut || recs[i].typ == TypeDelete {
			hasX = true
			xTS = recs[i].ts
			xIsPut = recs[i].typ == TypePut
			break
		}
	}

	kept := recs[cut:]
	if hasX {
		i := sort.Search(cut, func(i int) bool { return recs[i].ts >= xTS })
		if xIsPut {
			kept = append(append([]rec{}, recs[i:i+1]...), kept...)
		}
	}
	removed := len(recs) - len(kept)
	node.recs = kept
	s.count -= removed
}

// Count 返回当前记录总数。
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

// AccessCount 返回 GCStep 处理记录时访问的记录条数总和。
func (s *Store) AccessCount() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.access
}
