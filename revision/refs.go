package revision

import "fmt"

// RefStore 管理直接引用、符号引用、每引用 reflog 与短名补全索引。
// 直接引用的值为对象标识；符号引用的值以 "ref:" 前缀标记，指向另一引用名。
// 并发由 Store 的 RWMutex 保证，本结构不自带锁。
type RefStore struct {
	values map[string]string   // 全名 -> 对象标识 或 "ref:目标引用名"
	logs   map[string][]string // 直接引用名 -> reflog（索引 0 为当前值）
	index  *strTrie            // 全部引用全名的精确/补全索引
}

const symbolicPrefix = "ref:"

func newRefStore(namespaces []string) *RefStore {
	return &RefStore{
		values: map[string]string{},
		logs:   map[string][]string{},
		index:  newStrTrie(),
	}
}

func validRefName(name string) bool {
	if name == "" || name[0] == '/' || name[len(name)-1] == '/' || name[0] == '.' {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c <= ' ' || c == 0x7f {
			return false
		}
		switch c {
		case '~', '^', ':', '?', '*', '[', '\\', '\'', '"', '@':
			return false
		}
	}
	return true
}

// setTarget 把 name 设为直接引用。name 原本若是符号引用则被覆盖；
// 每次调用都在该直接引用的 reflog 前追加新当前值。
func (r *RefStore) setTarget(name, targetID string, logValue string) error {
	if !validRefName(name) {
		return fmt.Errorf("invalid reference name %q", name)
	}
	if len(targetID) != IDFullLen {
		return fmt.Errorf("ref target must be a full object id")
	}
	if _, existed := r.values[name]; !existed {
		r.index.insert(name)
	}
	r.values[name] = targetID
	r.logs[name] = append([]string{logValue}, r.logs[name]...)
	return nil
}

// setSymbolic 把 name 设为指向 target 的符号引用。
// 若设置后会成环（含自指），整体拒绝且不改变任何既有状态。
func (r *RefStore) setSymbolic(name, target string) error {
	if !validRefName(name) || !validRefName(target) {
		return fmt.Errorf("invalid reference name")
	}
	prevVal, prevExisted := r.values[name]
	// 试写入后沿链探测，发现成环则回滚。
	if !prevExisted {
		r.index.insert(name)
	}
	r.values[name] = symbolicPrefix + target
	if r.cycleFrom(name) {
		if prevExisted {
			r.values[name] = prevVal
		} else {
			delete(r.values, name)
			r.index.remove(name)
		}
		return &ResolutionError{Code: ErrSymbolicCycle, SegIndex: -1,
			Detail: fmt.Sprintf("symbolic ref %q -> %q creates a cycle", name, target)}
	}
	return nil
}

// cycleFrom 从 start 沿符号引用链行走，回到 start 返回 true。
func (r *RefStore) cycleFrom(start string) bool {
	cur := start
	visited := map[string]bool{}
	for {
		v, ok := r.values[cur]
		if !ok || !isSymbolic(v) {
			return false
		}
		cur = v[len(symbolicPrefix):]
		if cur == start {
			return true
		}
		if visited[cur] {
			return true // 到达既有环（理论上不会发生，防御性处理）
		}
		visited[cur] = true
	}
}

func isSymbolic(v string) bool {
	return len(v) > len(symbolicPrefix) && v[:len(symbolicPrefix)] == symbolicPrefix
}

// refHit 描述一次短名解析的结果。
type refHit struct {
	fullName string
	exact    bool // 是否为全名直接命中
}

// lookupShort 先查全名直接命中；否则按命名空间次序尝试 ns+short。
// 复杂度为 O((len(short)+len(ns)) * 命名空间数)，与引用总数无关。
func (r *RefStore) lookupShort(short string, namespaces []string) (refHit, bool) {
	if r.index.has(short) {
		if v, ok := r.values[short]; ok && !isSymbolic(v) {
			return refHit{fullName: short, exact: true}, true
		}
		if v, ok := r.values[short]; ok && isSymbolic(v) {
			return refHit{fullName: short, exact: true}, true
		}
	}
	for _, ns := range namespaces {
		full := ns + short
		if _, ok := r.values[full]; ok {
			return refHit{fullName: full}, true
		}
	}
	return refHit{}, false
}

// resolve 沿符号引用链多级解析到直接引用，返回直接引用全名与目标标识。
func (r *RefStore) resolve(name string) (string, string, bool) {
	cur := name
	visited := map[string]bool{}
	for {
		v, ok := r.values[cur]
		if !ok {
			return "", "", false
		}
		if !isSymbolic(v) {
			return cur, v, true
		}
		cur = v[len(symbolicPrefix):]
		if visited[cur] {
			return "", "", false
		}
		visited[cur] = true
	}
}

// reflogBack 取直接引用 name 的第 n 次更早的值（n=0 为当前值）。
func (r *RefStore) reflogBack(name string, n int) (string, bool) {
	log := r.logs[name]
	if n < 0 || n >= len(log) {
		return "", false
	}
	return log[n], true
}

// strTrie 是引用全名上的普通字符串基数树，支持精确查找、插入与删除。
type strTrie struct {
	root *strNode
}

type strNode struct {
	edge     string
	children map[byte]*strNode
	end      bool
}

func newStrTrie() *strTrie { return &strTrie{root: &strNode{children: map[byte]*strNode{}}} }

func (t *strTrie) has(key string) bool {
	node := t.root
	rest := key
	for rest != "" {
		child := node.children[rest[0]]
		if child == nil {
			return false
		}
		c := commonStrPrefix(child.edge, rest)
		if c < len(child.edge) {
			return false
		}
		rest = rest[c:]
		node = child
	}
	return node.end
}

func (t *strTrie) insert(key string) {
	node := t.root
	rest := key
	for rest != "" {
		child := node.children[rest[0]]
		if child == nil {
			child = &strNode{edge: rest, end: true, children: map[byte]*strNode{}}
			node.children[rest[0]] = child
			return
		}
		c := commonStrPrefix(child.edge, rest)
		if c == len(child.edge) {
			node = child
			rest = rest[c:]
			continue
		}
		mid := &strNode{edge: child.edge[:c], children: map[byte]*strNode{}}
		node.children[rest[0]] = mid
		child.edge = child.edge[c:]
		mid.children[child.edge[0]] = child
		if c == len(rest) {
			mid.end = true
			return
		}
		leaf := &strNode{edge: rest[c:], end: true, children: map[byte]*strNode{}}
		mid.children[leaf.edge[0]] = leaf
		return
	}
	node.end = true
}

// remove 删除一个键；仅在引用名插入后回滚（成环拒绝）时使用。
func (t *strTrie) remove(key string) {
	type frame struct {
		n *strNode
		b byte
	}
	var path []frame
	node := t.root
	rest := key
	for rest != "" {
		child := node.children[rest[0]]
		if child == nil {
			return
		}
		c := commonStrPrefix(child.edge, rest)
		if c < len(child.edge) {
			return
		}
		path = append(path, frame{node, rest[0]})
		node = child
		rest = rest[c:]
	}
	if !node.end {
		return
	}
	node.end = false
	// 回收只有一个孩子的内部链节点。
	for i := len(path) - 1; i >= 0; i-- {
		f := path[i]
		child := f.n.children[f.b]
		if child.end || len(child.children) > 1 {
			break
		}
		if len(child.children) == 1 {
			var gc *strNode
			for _, v := range child.children {
				gc = v
			}
			gc.edge = child.edge + gc.edge
			f.n.children[f.b] = gc
			continue
		}
		delete(f.n.children, f.b)
	}
}

func commonStrPrefix(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}
