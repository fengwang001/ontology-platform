package hypcheck

import "sort"

// stamped 给树值附上该键最近一次写入的生效时刻（审计记录用，避免线性回溯版本数组）。
type stamped[T any] struct {
	v  T
	at Timestamp
}

// avlNode 是不可变（路径复制）AVL 树节点：每次写入只复制根到叶子一条路径。
type avlNode[T any] struct {
	key    string
	val    stamped[T]
	left   *avlNode[T]
	right  *avlNode[T]
	height int8
}

func avlHeight[T any](n *avlNode[T]) int8 {
	if n == nil {
		return 0
	}
	return n.height
}

func avlBalance[T any](n *avlNode[T]) int8 {
	return avlHeight(n.left) - avlHeight(n.right)
}

func avlWith[T any](n *avlNode[T], l, r *avlNode[T]) *avlNode[T] {
	h := avlHeight(l)
	if avlHeight(r) > h {
		h = avlHeight(r)
	}
	return &avlNode[T]{key: n.key, val: n.val, left: l, right: r, height: h + 1}
}

func avlRotateRight[T any](n *avlNode[T]) *avlNode[T] {
	x := n.left
	return avlWith(x, x.left, avlWith(n, x.right, n.right))
}

func avlRotateLeft[T any](n *avlNode[T]) *avlNode[T] {
	x := n.right
	return avlWith(x, avlWith(n, n.left, x.left), x.right)
}

func avlRebalance[T any](n *avlNode[T]) *avlNode[T] {
	b := avlBalance(n)
	switch {
	case b > 1:
		if avlBalance(n.left) < 0 {
			n = avlWith(n, avlRotateLeft(n.left), n.right)
		}
		return avlRotateRight(n)
	case b < -1:
		if avlBalance(n.right) > 0 {
			n = avlWith(n, n.left, avlRotateRight(n.right))
		}
		return avlRotateLeft(n)
	default:
		return n
	}
}

func avlPut[T any](n *avlNode[T], key string, val T, at Timestamp) *avlNode[T] {
	if n == nil {
		return &avlNode[T]{key: key, val: stamped[T]{v: val, at: at}, height: 1}
	}
	switch {
	case key < n.key:
		return avlRebalance(avlWith(n, avlPut(n.left, key, val, at), n.right))
	case key > n.key:
		return avlRebalance(avlWith(n, n.left, avlPut(n.right, key, val, at)))
	default:
		return avlWith(n, n.left, n.right).withVal(stamped[T]{v: val, at: at})
	}
}

func (n *avlNode[T]) withVal(val stamped[T]) *avlNode[T] {
	n.val = val
	return n
}

func avlGet[T any](n *avlNode[T], key string) (stamped[T], bool) {
	var zero stamped[T]
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n.val, true
		}
	}
	return zero, false
}

// snapshotRef 是一个不可变树版本及其提交时刻。
type snapshotRef[T any] struct {
	at   Timestamp
	root *avlNode[T]
}

// snapshotMap 是满足成本要求的版本化键值存储：
// 每个事件最多产生一个 O(log K) 路径复制的新树版本（K 为当前键数量）；
// as-of(t) 在版本数组上二分（O(log V)，V 为事件数）后以 O(log K) 读键。
// 因而单次快照重建成本不随“系统累计版本演进总次数”线性增长。
type snapshotMap[T any] struct {
	refs   []snapshotRef[T]
	baseAt Timestamp // 压实保留下界；baseAt <= t 早于首版本点时回退到最早保留根
}

func newSnapshotMap[T any]() *snapshotMap[T] { return &snapshotMap[T]{} }

// view 是一次 as-of 演算的只读视图：绑定局部探针统计，保证并发预检无共享写。
type mapView[T any] struct {
	m     *snapshotMap[T]
	stats *ProbeStats
}

func (m *snapshotMap[T]) with(stats *ProbeStats) *mapView[T] { return &mapView[T]{m: m, stats: stats} }

func (v *mapView[T]) rootAt(t Timestamp) (*avlNode[T], bool) { return v.m.rootAt(t, v.stats) }

func (v *mapView[T]) asOf(key string, t Timestamp) (T, Timestamp, bool, bool) {
	return v.m.asOf(key, t, v.stats)
}

func (m *snapshotMap[T]) commit(at Timestamp, key string, val T) {
	var root *avlNode[T]
	if len(m.refs) > 0 {
		root = m.refs[len(m.refs)-1].root
		if m.refs[len(m.refs)-1].at == at {
			m.refs = m.refs[:len(m.refs)-1]
		}
	}
	m.refs = append(m.refs, snapshotRef[T]{at: at, root: avlPut(root, key, val, at)})
}

func (m *snapshotMap[T]) tick(at Timestamp) {
	var root *avlNode[T]
	if len(m.refs) > 0 {
		root = m.refs[len(m.refs)-1].root
		if m.refs[len(m.refs)-1].at == at {
			return
		}
	}
	m.refs = append(m.refs, snapshotRef[T]{at: at, root: root})
}

func (m *snapshotMap[T]) current() *avlNode[T] {
	if len(m.refs) == 0 {
		return nil
	}
	return m.refs[len(m.refs)-1].root
}

func (m *snapshotMap[T]) versionCount() int { return len(m.refs) }

func (m *snapshotMap[T]) truncate(horizon Timestamp) {
	i := sort.Search(len(m.refs), func(i int) bool { return m.refs[i].at >= horizon })
	if i == 0 {
		m.baseAt = horizon
		return
	}
	base := m.refs[i-1]
	m.refs = append([]snapshotRef[T]{base}, m.refs[i:]...)
	m.baseAt = horizon
}

func (m *snapshotMap[T]) rootAt(t Timestamp, stats *ProbeStats) (*avlNode[T], bool) {
	if stats != nil {
		stats.probe()
	}
	if len(m.refs) == 0 || t < m.refs[0].at {
		if len(m.refs) > 0 && m.baseAt > 0 && t >= m.baseAt {
			return m.refs[0].root, true
		}
		return nil, false
	}
	i := sort.Search(len(m.refs), func(i int) bool { return m.refs[i].at > t }) - 1
	return m.refs[i].root, true
}

func (m *snapshotMap[T]) asOf(key string, t Timestamp, stats *ProbeStats) (val T, at Timestamp, covered, exists bool) {
	root, covered := m.rootAt(t, stats)
	if !covered {
		var zero T
		return zero, 0, false, false
	}
	s, exists := avlGet(root, key)
	return s.v, s.at, true, exists
}

// ProbeStats 统计一次快照重建中版本记录被探测的次数，供成本独立验证。
type ProbeStats struct{ N int }

func (s *ProbeStats) probe()     { s.N++ }
func (s *ProbeStats) reset()     { s.N = 0 }
func (s *ProbeStats) count() int { return s.N }
