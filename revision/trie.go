package revision

// IDFullLen 是对象十六进制标识的固定长度。
const IDFullLen = 40

// hexTrie 是十六进制字母表上的基数树（压缩路径前缀树）。
// 所有键均为等长标识，因此终端节点不承载额外负载。
// 每个节点缓存其子树内终端个数 size；对象只增不删，size 单调不减。
// insert / countPrefix / shortestUnique 的复杂度均为 O(IDFullLen)，
// 与对象总数无关。
type hexTrie struct {
	root *trieNode
}

type trieNode struct {
	edge     string        // 从父节点进入本节点的压缩边标签
	children [16]*trieNode // 按半字节索引的分支
	end      bool          // 是否有标识在此终止
	size     int           // 子树（含自身）内终端个数
}

func newHexTrie() *hexTrie { return &hexTrie{root: &trieNode{}} }

func hexVal(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

// insert 加入一个完整标识，重复加入幂等。
func (t *hexTrie) insert(id string) {
	if len(id) != IDFullLen {
		return
	}
	// path 记录从根到插入点途经的节点；新键使其全部 size+1。
	path := []*trieNode{t.root}
	node := t.root
	rest := id
	for rest != "" {
		h, _ := hexVal(rest[0])
		child := node.children[h]
		if child == nil {
			leaf := &trieNode{edge: rest, end: true, size: 1}
			node.children[h] = leaf
			for _, an := range path {
				an.size++
			}
			return
		}
		common := commonPrefixLen(child.edge, rest)
		if common == len(child.edge) {
			node = child
			path = append(path, node)
			rest = rest[common:]
			continue
		}
		// 在压缩边中间分叉：拆出中间节点。
		mid := &trieNode{edge: child.edge[:common], size: child.size + 1}
		node.children[h] = mid
		child.edge = child.edge[common:]
		ch, _ := hexVal(child.edge[0])
		mid.children[ch] = child
		if common == len(rest) {
			mid.end = true
		} else {
			newLeaf := &trieNode{edge: rest[common:], end: true, size: 1}
			nh, _ := hexVal(newLeaf.edge[0])
			mid.children[nh] = newLeaf
		}
		for _, an := range path {
			an.size++
		}
		return
	}
	if !node.end {
		node.end = true
		for _, an := range path {
			an.size++
		}
	}
}

func commonPrefixLen(a, b string) int {
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

// countPrefix 返回以 p 为前缀的已插入标识数，复杂度 O(len(p))。
func (t *hexTrie) countPrefix(p string) int {
	node := t.root
	rest := p
	for rest != "" {
		h, ok := hexVal(rest[0])
		if !ok {
			return 0
		}
		child := node.children[h]
		if child == nil {
			return 0
		}
		common := commonPrefixLen(child.edge, rest)
		switch {
		case common == len(rest):
			return child.size // 查询前缀结束于压缩边内部或末端
		case common < len(child.edge):
			return 0 // 在边内分叉，无任何键以该前缀开头
		default:
			node = child
			rest = rest[common:]
		}
	}
	return node.size
}

// findUnique 返回以 p 为前缀的唯一标识；若不唯一或不存在返回空串与 false。
// 复杂度 O(IDFullLen)。
func (t *hexTrie) findUnique(p string) (string, bool) {
	node := t.root
	rest := p
	var sb []byte
	for rest != "" {
		h, ok := hexVal(rest[0])
		if !ok {
			return "", false
		}
		child := node.children[h]
		if child == nil {
			return "", false
		}
		common := commonPrefixLen(child.edge, rest)
		if common < len(child.edge) && common < len(rest) {
			return "", false
		}
		sb = append(sb, child.edge[:common]...)
		if common == len(rest) {
			if child.size != 1 {
				return "", false
			}
			sb = append(sb, child.edge[common:]...)
			id := string(sb)
			return t.descendSingle(child, id), true
		}
		node = child
		rest = rest[common:]
	}
	if node.size != 1 {
		return "", false
	}
	return t.descendSingle(node, string(sb)), true
}

// descendSingle 从子树大小为 1 的节点下沿唯一分支取到终端标识。
func (t *hexTrie) descendSingle(node *trieNode, prefix string) string {
	for !node.end {
		var next *trieNode
		for _, c := range node.children {
			if c != nil {
				next = c
				break
			}
		}
		prefix += next.edge
		node = next
	}
	return prefix
}

// shortestUnique 返回 id 的不短于 minLen 的最短唯一前缀；
// id 不存在时返回空串。复杂度 O(IDFullLen)。
func (t *hexTrie) shortestUnique(id string, minLen int) string {
	if len(id) != IDFullLen || t.countPrefix(id) == 0 {
		return ""
	}
	if minLen < 1 {
		minLen = 1
	}
	if minLen > IDFullLen {
		minLen = IDFullLen
	}
	node := t.root
	depth := 0
	rest := id
	uniqueAt := -1
	for {
		if node.size == 1 {
			uniqueAt = depth
			break
		}
		if rest == "" {
			break
		}
		h, _ := hexVal(rest[0])
		child := node.children[h]
		if child == nil {
			break
		}
		// 进入压缩边：边内任一处的前缀计数均为 child.size。
		if child.size == 1 {
			uniqueAt = depth + 1
			break
		}
		node = child
		depth += len(child.edge)
		rest = rest[len(child.edge):]
	}
	if uniqueAt < 0 {
		uniqueAt = IDFullLen
	}
	if uniqueAt < minLen {
		uniqueAt = minLen
	}
	return id[:uniqueAt]
}
