package ontology

import (
	"math/big"
	"math/rand"
)

// node 是读数 Treap 的一个节点。
//
// 除自身读数外，节点缓存以其为根的子树聚合：
//   - sumRaw: 子树中按时刻排序的相邻读数区间表显用电量之和（即首读到末读的表显增量）；
//   - estEdges: 两端点均在子树内、且至少一端为估算读数的相邻区间条数。
//
// 聚合自底向上由 pull 维护，split/merge/insert/remove 均为 O(log n)。
type node struct {
	key   int64
	r     Reading
	left  *node
	right *node
	prio  uint64

	sumRaw   *big.Int
	estEdges int
}

type readingTree struct {
	root *node
	rng  *rand.Rand
	mod  uint64
}

func newReadingTree(mod uint64, rng *rand.Rand) *readingTree {
	return &readingTree{rng: rng, mod: mod}
}

// bridge 计算同一棵树内相邻两条读数构成的区间聚合：
// 左子树最右读数 -> 节点读数 -> 右子树最左读数 的连接段。
func (t *readingTree) bridge(n *node) (sumRaw *big.Int, estEdges int) {
	sumRaw = big.NewInt(0)
	if n.left != nil {
		lr := rightmost(n.left)
		d, _ := rawDelta(lr.r.Value, n.r.Value, t.mod)
		sumRaw.Add(sumRaw, new(big.Int).SetUint64(d))
		if lr.r.Kind == Estimated || n.r.Kind == Estimated {
			estEdges++
		}
	}
	if n.right != nil {
		rl := leftmost(n.right)
		d, _ := rawDelta(n.r.Value, rl.r.Value, t.mod)
		sumRaw.Add(sumRaw, new(big.Int).SetUint64(d))
		if n.r.Kind == Estimated || rl.r.Kind == Estimated {
			estEdges++
		}
	}
	return sumRaw, estEdges
}

// pull 根据子节点缓存重算 n 的缓存。
func (t *readingTree) pull(n *node) {
	sum := big.NewInt(0)
	est := 0
	if n.left != nil {
		sum.Add(sum, n.left.sumRaw)
		est += n.left.estEdges
	}
	if n.right != nil {
		sum.Add(sum, n.right.sumRaw)
		est += n.right.estEdges
	}
	b, e := t.bridge(n)
	sum.Add(sum, b)
	est += e
	n.sumRaw = sum
	n.estEdges = est
}

// split 以 key 为界拆树：lt 中 key < x，ge 中 key >= x。
func (t *readingTree) split(n *node, x int64) (lt, ge *node) {
	if n == nil {
		return nil, nil
	}
	if n.key < x {
		a, b := t.split(n.right, x)
		n.right = a
		t.pull(n)
		return n, b
	}
	a, b := t.split(n.left, x)
	n.left = b
	t.pull(n)
	return a, n
}

func (t *readingTree) merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		a.right = t.merge(a.right, b)
		t.pull(a)
		return a
	}
	b.left = t.merge(a, b.left)
	t.pull(b)
	return b
}

func leftmost(n *node) *node {
	for n.left != nil {
		n = n.left
	}
	return n
}

func rightmost(n *node) *node {
	for n.right != nil {
		n = n.right
	}
	return n
}

// insert 插入读数；调用方须保证 key 不存在。
func (t *readingTree) insert(r Reading) {
	n := &node{key: r.Time, r: r, prio: t.rng.Uint64(), sumRaw: big.NewInt(0)}
	t.pull(n)
	lt, ge := t.split(t.root, r.Time)
	t.root = t.merge(t.merge(lt, n), ge)
}

// get 返回某时刻的读数。
func (t *readingTree) get(key int64) (Reading, bool) {
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n.r, true
		}
	}
	return Reading{}, false
}

// delete 删除某时刻的读数节点，返回是否存在。
func (t *readingTree) delete(key int64) bool {
	lt, ge := t.split(t.root, key)
	eq, gt := t.split(ge, key+1)
	if eq == nil {
		t.root = t.merge(lt, ge)
		return false
	}
	t.root = t.merge(lt, gt)
	return true
}

// predecessor 返回严格小于 key 的最大读数。
func (t *readingTree) predecessor(key int64) (Reading, bool) {
	lt, ge := t.split(t.root, key)
	var found bool
	var r Reading
	if lt != nil {
		r = rightmost(lt).r
		found = true
	}
	t.root = t.merge(lt, ge)
	return r, found
}

// successor 返回严格大于 key 的最小读数。
func (t *readingTree) successor(key int64) (Reading, bool) {
	lt, ge := t.split(t.root, key+1)
	var found bool
	var r Reading
	if ge != nil {
		r = leftmost(ge).r
		found = true
	}
	t.root = t.merge(lt, ge)
	return r, found
}

// predInTour 返回挂接期 [lo, hi] 内严格小于 key 的最大读数；
// succInTour 返回挂接期 [lo, hi] 内严格大于 key 的最小读数。
// 挂接期两端（安装衔接点 lo 与拆除衔接点 hi）本身都是实抄读数，
// 乱序插入与区间重算时它们必须作为相邻读数参与；传入的 hi 为拆除时刻。
// 拆表后同一电表可被再次挂接到新的 tour，不得跨越 tour 间隙取到另一 tour 的读数。
func (t *readingTree) predInTour(key, lo, hi int64) (Reading, bool) {
	r, ok := t.predecessor(key)
	if ok && r.Time >= lo && r.Time <= hi {
		return r, true
	}
	return Reading{}, false
}

func (t *readingTree) succInTour(key, lo, hi int64) (Reading, bool) {
	r, ok := t.successor(key)
	if ok && r.Time >= lo && r.Time <= hi {
		return r, true
	}
	return Reading{}, false
}

func (t *readingTree) last() (Reading, bool) {
	if t.root == nil {
		return Reading{}, false
	}
	return rightmost(t.root).r, true
}

// rangeAgg 返回闭区间 [lo, hi] 内读数子序列的聚合（两端须均存在于树中）。
//
// 纯只读，可在读锁下与其他查询并发。做法是对 key 空间做带剪枝的中序遍历：
// key < lo 的整棵左子树与 key > hi 的整棵右子树直接剪枝；整棵落在 [lo,hi]
// 内的子树直接消费其缓存 sumRaw/estEdges，只补一条“区间前驱->子树最左读数”
// 的连接区间。逐条访问的节点只出现在 lo/hi 两条边界路径上（O(log n) 个），
// 其余区间内读数以整棵子树缓存的方式 O(1) 消费，因此开销只取决于树高与
// 命中的整含子树数量，与区间外读数、其他供电点读数完全无关。
func (t *readingTree) rangeAgg(lo, hi int64) (sumRaw *big.Int, estEdges int) {
	sumRaw = big.NewInt(0)
	var prevKey int64
	var prevValue uint64
	var prevKind ReadingKind
	havePrev := false
	addEdge := func(from, to Reading) {
		d, _ := rawDelta(from.Value, to.Value, t.mod)
		sumRaw.Add(sumRaw, new(big.Int).SetUint64(d))
		if from.Kind == Estimated || to.Kind == Estimated {
			estEdges++
		}
	}
	// consumeSubtree 消费一棵其全部节点都在 [lo,hi] 内的子树。
	consumeSubtree := func(n *node) {
		if havePrev {
			addEdge(Reading{Time: prevKey, Value: prevValue, Kind: prevKind}, leftmost(n).r)
		}
		sumRaw.Add(sumRaw, n.sumRaw)
		estEdges += n.estEdges
		lr := rightmost(n).r
		prevKey, prevValue, prevKind, havePrev = lr.Time, lr.Value, lr.Kind, true
	}
	wholeInside := func(n *node) bool {
		return n.key >= lo && n.key <= hi &&
			(n.left == nil || leftmost(n.left).key >= lo) &&
			(n.right == nil || rightmost(n.right).key <= hi)
	}
	var walk func(n *node)
	walk = func(n *node) {
		if n == nil {
			return
		}
		if wholeInside(n) {
			consumeSubtree(n)
			return
		}
		if n.key >= lo { // 左子树才可能含 >= lo 的节点
			walk(n.left)
		}
		if n.key >= lo && n.key <= hi {
			if havePrev {
				addEdge(Reading{Time: prevKey, Value: prevValue, Kind: prevKind}, n.r)
			}
			prevKey, prevValue, prevKind, havePrev = n.r.Time, n.r.Value, n.r.Kind, true
		}
		if n.key <= hi { // 右子树才可能含 <= hi 的节点
			walk(n.right)
		}
	}
	walk(t.root)
	return sumRaw, estEdges
}

// len 用于测试断言与复杂度论证的辅助统计。
func (t *readingTree) len() int {
	var count func(n *node) int
	count = func(n *node) int {
		if n == nil {
			return 0
		}
		return 1 + count(n.left) + count(n.right)
	}
	return count(t.root)
}
