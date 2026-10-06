package gate

import (
	"encoding/binary"
	"hash/fnv"
)

// segKey 唯一标识某登机口上的一段占用（同一登机口上区间互不重叠）。
type segKey struct {
	start int
	fid   string
	seg   SegKind
}

func (a segKey) less(b segKey) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	if a.fid != b.fid {
		return a.fid < b.fid
	}
	return a.seg < b.seg
}

// prio 取键的 FNV 哈希，确定性、无随机数，保证重放形状一致。
func (k segKey) prio() uint64 {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(k.start))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(k.seg))
	h := fnv.New64a()
	h.Write(buf[:])
	h.Write([]byte(k.fid))
	return h.Sum64()
}

type tnode struct {
	key    segKey
	end    int
	ref    *segment
	prio   uint64
	left   *tnode
	right  *tnode
	maxEnd int
}

func (n *tnode) recalc() {
	n.maxEnd = n.end
	if n.left != nil && n.left.maxEnd > n.maxEnd {
		n.maxEnd = n.left.maxEnd
	}
	if n.right != nil && n.right.maxEnd > n.maxEnd {
		n.maxEnd = n.right.maxEnd
	}
}

func rotateRight(n *tnode) *tnode {
	l := n.left
	n.left = l.right
	l.right = n
	n.recalc()
	l.recalc()
	return l
}

func rotateLeft(n *tnode) *tnode {
	r := n.right
	n.right = r.left
	r.left = n
	n.recalc()
	r.recalc()
	return r
}

// itree 是按起点排序、带 maxEnd 增广的区间 Treap。
// 查询/插入/删除开销为 O(log n + k)，n 为当前未过期区间数，
// k 为结果数；已过期区间会被物理删除，开销不随历史总量增长。
type itree struct {
	root *tnode
	size int
}

func (t *itree) insert(key segKey, end int, ref *segment) {
	t.root = insertNode(t.root, &tnode{key: key, end: end, ref: ref, prio: key.prio(), maxEnd: end})
	t.size++
}

func insertNode(n, m *tnode) *tnode {
	if n == nil {
		return m
	}
	if m.key.less(n.key) {
		n.left = insertNode(n.left, m)
		n.recalc()
		if n.left.prio < n.prio {
			return rotateRight(n)
		}
		return n
	}
	n.right = insertNode(n.right, m)
	n.recalc()
	if n.right.prio < n.prio {
		return rotateLeft(n)
	}
	return n
}

func (t *itree) remove(key segKey) {
	var found bool
	t.root, found = removeNode(t.root, key)
	if found {
		t.size--
	}
}

func removeNode(n *tnode, key segKey) (*tnode, bool) {
	if n == nil {
		return nil, false
	}
	if key.less(n.key) {
		var f bool
		n.left, f = removeNode(n.left, key)
		if f {
			n.recalc()
		}
		return n, f
	}
	if n.key.less(key) {
		var f bool
		n.right, f = removeNode(n.right, key)
		if f {
			n.recalc()
		}
		return n, f
	}
	return merge(n.left, n.right), true
}

// merge 合并两棵键互不交叉的 Treap（l 中所有键小于 r 中所有键）。
func merge(l, r *tnode) *tnode {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio < r.prio {
		l.right = merge(l.right, r)
		l.recalc()
		return l
	}
	r.left = merge(l, r.left)
	r.recalc()
	return r
}

// overlap 返回与 [s, e) 相交（端点相接不算相交）的所有分段。
func (t *itree) overlap(s, e int, visits *int64) []*segment {
	var out []*segment
	overlapWalk(t.root, s, e, &out, visits)
	return out
}

func overlapWalk(n *tnode, s, e int, out *[]*segment, visits *int64) {
	if n == nil || n.maxEnd <= s {
		return
	}
	*visits++
	overlapWalk(n.left, s, e, out, visits)
	if n.key.start < e && n.end > s {
		*out = append(*out, n.ref)
	}
	if n.key.start < e {
		overlapWalk(n.right, s, e, out, visits)
	}
}

// deleteExpired 物理删除所有 end <= now 的区间（左闭右开，端点可复用）。
func (t *itree) deleteExpired(now int, visits *int64) {
	var keys []segKey
	expiredWalk(t.root, now, &keys, visits)
	for _, k := range keys {
		t.remove(k)
	}
}

func expiredWalk(n *tnode, now int, out *[]segKey, visits *int64) {
	if n == nil {
		return
	}
	*visits++
	if n.maxEnd <= now {
		collectKeys(n, out)
		return
	}
	if n.end <= now {
		*out = append(*out, n.key)
	}
	expiredWalk(n.left, now, out, visits)
	expiredWalk(n.right, now, out, visits)
}

func collectKeys(n *tnode, out *[]segKey) {
	if n == nil {
		return
	}
	*out = append(*out, n.key)
	collectKeys(n.left, out)
	collectKeys(n.right, out)
}
