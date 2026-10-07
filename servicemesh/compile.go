package servicemesh

import "sort"

// 编译产物：发布时一次构建，请求路径上只读，整体不可变。

type matcher struct {
	id       int
	ruleIdx  int
	matchIdx int
	path     PathMatch
	headers  []HeaderMatch // 除驱动条件外的其余头条件
	driver   *HeaderMatch  // 倒排驱动条件；nil 表示该匹配项无头条件
}

type compiledRule struct {
	targets  []Target
	policies Policy
}

type compiledConfig struct {
	rules     []compiledRule
	fallbacks []Target
	def       Policy
	index     *routeIndex
	matches   [][]MatchItem // 原始匹配项深拷贝，供 GetConfig 重建
}

// valueTrie 为值前缀倒排树：查询值各前缀节点上的条件全部枚举。
type valueTrieNode struct {
	children map[byte]*valueTrieNode
	ids      []int
}

type valueTrie struct{ root *valueTrieNode }

func newValueTrie() *valueTrie {
	return &valueTrie{root: &valueTrieNode{children: map[byte]*valueTrieNode{}}}
}

func (t *valueTrie) add(value string, id int) {
	n := t.root
	for i := 0; i < len(value); i++ {
		next := n.children[value[i]]
		if next == nil {
			next = &valueTrieNode{children: map[byte]*valueTrieNode{}}
			n.children[value[i]] = next
		}
		n = next
	}
	n.ids = append(n.ids, id)
}

func (t *valueTrie) enumerate(value string, out *[]int) {
	n := t.root
	*out = append(*out, n.ids...)
	for i := 0; i < len(value); i++ {
		next := n.children[value[i]]
		if next == nil {
			return
		}
		n = next
		*out = append(*out, n.ids...)
	}
}

type headerIndex struct {
	exact   map[string]map[string][]int
	prefix  map[string]*valueTrie
	present map[string][]int
}

func newHeaderIndex() *headerIndex {
	return &headerIndex{
		exact:   map[string]map[string][]int{},
		prefix:  map[string]*valueTrie{},
		present: map[string][]int{},
	}
}

type trieNode struct {
	children      map[string]*trieNode
	prefix        []int        // 无前驱头条件的前缀路径匹配项
	exact         []int        // 无头条件的精确路径匹配项
	prefixHeaders *headerIndex // 前缀路径匹配项的头倒排（每段途经节点评估）
	exactHeaders  *headerIndex // 精确路径匹配项的头倒排（仅终止节点评估）
}

func newTrieNode() *trieNode {
	return &trieNode{
		children:      map[string]*trieNode{},
		prefixHeaders: newHeaderIndex(),
		exactHeaders:  newHeaderIndex(),
	}
}

type routeIndex struct {
	root     *trieNode
	matchers []matcher
}

func newIndex() *routeIndex {
	return &routeIndex{root: newTrieNode()}
}

func splitSegments(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '/' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}

func pathSegments(path string) []string {
	if path == "/" {
		return nil
	}
	return splitSegments(path[1:])
}

// driverRank 粗略刻画枚举扇出：精确最窄，前缀次之，存在最宽。
func driverRank(h HeaderMatch) int {
	switch h.Op {
	case HeaderExact:
		return 0
	case HeaderPrefix:
		return 1
	default:
		return 2
	}
}

func chooseDriver(mt *matcher) {
	if len(mt.headers) == 0 {
		return
	}
	best := 0
	for i := 1; i < len(mt.headers); i++ {
		if driverRank(mt.headers[i]) < driverRank(mt.headers[best]) {
			best = i
		}
	}
	d := mt.headers[best]
	mt.driver = &d
	mt.headers = append(mt.headers[:best], mt.headers[best+1:]...)
}

func (idx *routeIndex) add(m matcher) {
	node := idx.root
	for _, seg := range pathSegments(m.path.Path) {
		next := node.children[seg]
		if next == nil {
			next = newTrieNode()
			node.children[seg] = next
		}
		node = next
	}
	m.id = len(idx.matchers)
	idx.matchers = append(idx.matchers, m)

	if m.driver == nil {
		if m.path.Kind == PathExact {
			node.exact = append(node.exact, m.id)
		} else {
			node.prefix = append(node.prefix, m.id)
		}
		return
	}
	name := m.driver.Name
	var hi *headerIndex
	if m.path.Kind == PathExact {
		hi = node.exactHeaders
	} else {
		hi = node.prefixHeaders
	}
	switch m.driver.Op {
	case HeaderExact:
		vals := hi.exact[name]
		if vals == nil {
			vals = map[string][]int{}
			hi.exact[name] = vals
		}
		vals[m.driver.Value] = append(vals[m.driver.Value], m.id)
	case HeaderPrefix:
		vt := hi.prefix[name]
		if vt == nil {
			vt = newValueTrie()
			hi.prefix[name] = vt
		}
		vt.add(m.driver.Value, m.id)
	case HeaderPresent:
		hi.present[name] = append(hi.present[name], m.id)
	}
}

// lookup 返回按规则序号、匹配项序号排序的、全部条件被满足的匹配项。
// 开销只与路径段数及与该请求相关的匹配项数量有关，与规则总数无关。
func (idx *routeIndex) lookup(path string, headers map[string][]string) []matcher {
	clean := stripQuery(path)
	var segs []string
	if clean != "/" {
		segs = splitSegments(clean[1:])
	}
	node := idx.root
	seen := map[int]struct{}{}
	candidates := make([]int, 0, 16)
	matched := []matcher{}
	collectNode(node, len(segs) == 0, headers, idx.matchers, seen, &candidates, &matched)
	for i, seg := range segs {
		next := node.children[seg]
		if next == nil {
			break
		}
		node = next
		collectNode(node, i == len(segs)-1, headers, idx.matchers, seen, &candidates, &matched)
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].ruleIdx != matched[j].ruleIdx {
			return matched[i].ruleIdx < matched[j].ruleIdx
		}
		return matched[i].matchIdx < matched[j].matchIdx
	})
	return matched
}

// collectNode 收集该节点候选；精确匹配项只在请求路径的终止节点（isFinal）收集，
// 前缀匹配项在每个途经节点收集。
func collectNode(n *trieNode, isFinal bool, headers map[string][]string, all []matcher,
	seen map[int]struct{}, candidates *[]int, out *[]matcher) {
	*candidates = (*candidates)[:0]
	*candidates = append(*candidates, n.prefix...)
	appendHeaderCandidates(n.prefixHeaders, headers, candidates)
	if isFinal {
		*candidates = append(*candidates, n.exact...)
		appendHeaderCandidates(n.exactHeaders, headers, candidates)
	}
	for _, id := range *candidates {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		mt := all[id]
		if matcherSatisfied(mt, headers) {
			*out = append(*out, mt)
		}
	}
}

func appendHeaderCandidates(hi *headerIndex, headers map[string][]string, out *[]int) {
	for name, values := range headers {
		if vals := hi.exact[name]; vals != nil {
			for _, v := range values {
				*out = append(*out, vals[v]...)
			}
		}
		if vt := hi.prefix[name]; vt != nil {
			for _, v := range values {
				vt.enumerate(v, out)
			}
		}
		if ids := hi.present[name]; ids != nil {
			*out = append(*out, ids...)
		}
	}
}

func matcherSatisfied(mt matcher, headers map[string][]string) bool {
	for _, h := range mt.headers {
		if !matchHeader(h, headers[h.Name]) {
			return false
		}
	}
	return true
}
