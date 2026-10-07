package pathlock

import (
	"container/heap"
	"strings"
)

// pathTrie 按路径段组织的字典树，用于祖先/后代关系判断。
// 每个节点记录子树内的锁数量 count，使「是否有后代锁」为 O(1)，
// 并让冲突搜索只访问与目标路径相关的子树。
type pathTrie struct {
	root   *trieNode
	visits int // 性能插桩：累计访问的节点数，仅供测试验证复杂度
}

type trieNode struct {
	children map[string]*trieNode
	lock     *Lock // 恰好锁在本节点路径上的锁，无则 nil
	count    int   // 子树（含本节点）内的锁总数
}

func newPathTrie() *pathTrie {
	return &pathTrie{root: &trieNode{}}
}

func splitPath(p string) []string { return strings.Split(p, "/") }

// findExact 返回恰好锁在 path 上的锁，无则 nil。开销 O(路径深度)。
func (t *pathTrie) findExact(path string) *Lock {
	n := t.root
	for _, seg := range splitPath(path) {
		t.visits++
		n = n.children[seg]
		if n == nil {
			return nil
		}
	}
	return n.lock
}

// add 在 path 处挂锁，并维护沿途子树计数。调用方保证 path 上无锁。
func (t *pathTrie) add(path string, l *Lock) {
	n := t.root
	n.count++
	for _, seg := range splitPath(path) {
		t.visits++
		c := n.children[seg]
		if c == nil {
			c = &trieNode{}
			if n.children == nil {
				n.children = make(map[string]*trieNode)
			}
			n.children[seg] = c
		}
		c.count++
		n = c
	}
	n.lock = l
}

// remove 摘除 path 处的锁并维护计数，回收空节点。调用方保证锁存在。
func (t *pathTrie) remove(path string) {
	t.removeRec(t.root, splitPath(path), 0)
}

func (t *pathTrie) removeRec(n *trieNode, segs []string, depth int) bool {
	n.count--
	if depth == len(segs) {
		n.lock = nil
	} else {
		t.visits++
		if t.removeRec(n.children[segs[depth]], segs, depth+1) {
			delete(n.children, segs[depth])
		}
	}
	return n.count == 0
}

// findConflict 在 path 的所有祖先与所有后代中，寻找他人持有的锁；
// 找到多把时返回路径最短（按字节长度）者，长度相同取字节序最小者。
// 找不到返回 nil。path 上的精确锁由调用方先行处理，不在此搜索。
// 开销只与路径深度及 path 子树内的锁数量有关，与全库锁总数无关。
func (t *pathTrie) findConflict(path, user string) *Lock {
	segs := splitPath(path)
	n := t.root
	// 祖先：自浅向深走，第一把他人持有的锁即最短者；
	// 任何祖先锁都短于任何后代锁，找到即可直接返回。
	for _, seg := range segs[:len(segs)-1] {
		t.visits++
		n = n.children[seg]
		if n == nil {
			return nil // 路径不存在于树中，下方也不可能有锁
		}
		if n.lock != nil && n.lock.Owner != user {
			return n.lock
		}
	}
	// 后代：以 (路径长度, 字节序) 为键做最佳优先搜索，
	// 第一把被弹出的他人持有的锁即所求。
	t.visits++
	at := n.children[segs[len(segs)-1]]
	if at == nil || at.count == 0 {
		return nil
	}
	h := &conflictHeap{}
	pushChildren(h, at, path)
	for h.Len() > 0 {
		it := heap.Pop(h).(conflictItem)
		if it.node.lock != nil && it.node.lock.Owner != user {
			return it.node.lock
		}
		pushChildren(h, it.node, it.path)
	}
	return nil
}

// collectConflicts 把 path 的祖先、path 自身及所有后代上
// 由他人持有的锁全部收入 out（键为锁路径，用于去重）。
func (t *pathTrie) collectConflicts(path, user string, out map[string]*Lock) {
	n := t.root
	cur := ""
	for i, seg := range splitPath(path) {
		if i > 0 {
			cur += "/"
		}
		cur += seg
		t.visits++
		n = n.children[seg]
		if n == nil {
			return
		}
		if n.lock != nil && n.lock.Owner != user {
			out[cur] = n.lock
		}
	}
	t.collectRec(n, cur, user, out)
}

func (t *pathTrie) collectRec(n *trieNode, path, user string, out map[string]*Lock) {
	for seg, c := range n.children {
		t.visits++
		if c.count == 0 {
			continue
		}
		cp := path + "/" + seg
		if c.lock != nil && c.lock.Owner != user {
			out[cp] = c.lock
		}
		t.collectRec(c, cp, user, out)
	}
}

func pushChildren(h *conflictHeap, n *trieNode, path string) {
	for seg, c := range n.children {
		if c.count > 0 {
			heap.Push(h, conflictItem{node: c, path: path + "/" + seg})
		}
	}
}

// conflictItem 是最佳优先搜索的堆元素，按 (路径字节长度, 字节序) 排序。
type conflictItem struct {
	node *trieNode
	path string
}

type conflictHeap []conflictItem

func (h conflictHeap) Len() int { return len(h) }

func (h conflictHeap) Less(i, j int) bool {
	a, b := h[i].path, h[j].path
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

func (h conflictHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *conflictHeap) Push(x any) { *h = append(*h, x.(conflictItem)) }

func (h *conflictHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
