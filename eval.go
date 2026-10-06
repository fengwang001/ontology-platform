package ontology

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// verdict 是单条路径由规则集得出的最终裁决。
type verdict int

const (
	verdictNone verdict = iota // 无规则命中
	verdictIncluded
	verdictExcluded
)

// pathVerdict 求任意路径（文件或目录）在编译规则集下的最终命中裁决。
// 命中范围：前缀规则命中其所有祖先链上的目录自身及全部后代；
// 精确规则只命中自身；本层通配只命中作用目录的直接子项。
// 只访问该路径祖先链上的规则索引，与其他目录文件数无关。
func pathVerdict(c *compiled, segs []string) verdict {
	best := -1
	bestAction := Exclude
	// 前缀规则：该路径的每个祖先目录（含根）上的前缀规则都可能命中。
	for i := 0; i <= len(segs); i++ {
		if r, ok := c.prefix[dirKey(segs[:i])]; ok && r.index > best {
			best, bestAction = r.index, r.action
		}
	}
	if len(segs) > 0 {
		// 精确规则与本层通配：只看「父目录 -> 自身」这一层。
		dir := dirKey(segs[:len(segs)-1])
		name := segs[len(segs)-1]
		if m, ok := c.exact[dir]; ok {
			if r, ok := m[name]; ok && r.index > best {
				best, bestAction = r.index, r.action
			}
		}
		// 本层通配规则作用于 dir 的直接子项 name；
		// name 自身为文件时命中，name 为目录时目录路径也命中，
		// 但不穿透到更深层（更深层在其自身祖先链上查不到该规则）。
		if m, ok := c.child[dir]; ok {
			if r, ok := m["*"]; ok && r.index > best {
				best, bestAction = r.index, r.action
			}
		}
	}
	if best < 0 {
		return verdictNone
	}
	if bestAction == Include {
		return verdictIncluded
	}
	return verdictExcluded
}

// fileVerdict 求一个文件（按分段）在编译规则集下的最终命中裁决。
func fileVerdict(c *compiled, segs []string) verdict { return pathVerdict(c, segs) }

// ruleTrie 是按目录组织的规则视图，每个节点携带「本目录及所有后代的规则」指纹。
type ruleTrie struct {
	children   map[string]*ruleTrie
	hash       string
	subInclude bool        // 本节点自身规则或任意后代规则中存在 Include
	self       indexedRule // 作用于该节点路径自身的精确规则（hasSelf 判定）
	hasSelf    bool
	childStar  indexedRule // 作用于本目录直接子项的通配规则
	hasStar    bool
}

// buildRuleTrie 依据编译规则集建立目录规则树（根节点对应 ""）。
func buildRuleTrie(c *compiled) *ruleTrie {
	if c.trie != nil {
		return c.trie
	}
	root := &ruleTrie{children: map[string]*ruleTrie{}}
	ensure := func(dir string) {
		node := root
		var segs []string
		if dir != "" {
			segs = splitPath(dir)
		}
		for _, s := range segs {
			nxt := node.children[s]
			if nxt == nil {
				nxt = &ruleTrie{children: map[string]*ruleTrie{}}
				node.children[s] = nxt
			}
			node = nxt
		}
	}
	for dir := range c.prefix {
		ensure(dir)
	}
	for dir := range c.exact {
		ensure(dir)
	}
	// 精确规则目标文件/目录的完整路径也要建节点，
	// 但其包含语义只归属目标自身，不自动外溢到中间目录。
	for dir, m := range c.exact {
		for name := range m {
			n := ensurePath(root, childPath(dir, name))
			n.self = m[name]
			n.hasSelf = true
		}
	}
	for dir := range c.child {
		ensure(dir)
		if r, ok := c.child[dir]["*"]; ok {
			setChildStar(root, dir, r)
		}
	}
	// 确保所有规则作用目录的完整父链存在（child 规则的 dir 亦如此）。
	for dir := range c.child {
		ensurePath(root, dir)
	}
	computeRuleHashes(root, c, "", false)
	c.trie = root
	return root
}

func setChildStar(root *ruleTrie, dir string, r indexedRule) {
	node := root
	if dir != "" {
		for _, s := range splitPath(dir) {
			node = node.children[s]
		}
	}
	node.childStar = r
	node.hasStar = true
}

// ensurePath 沿完整路径建出全部中间节点。
func ensurePath(root *ruleTrie, path string) *ruleTrie {
	node := root
	if path != "" {
		for _, s := range splitPath(path) {
			nxt := node.children[s]
			if nxt == nil {
				nxt = &ruleTrie{children: map[string]*ruleTrie{}}
				node.children[s] = nxt
			}
			node = nxt
		}
	}
	return node
}

func ruleNodeDigest(c *compiled, dir string) []byte {
	h := sha256.New()
	if r, ok := c.prefix[dir]; ok {
		writeDigest(h, "P", r)
	}
	if r, ok := c.child[dir]["*"]; ok {
		writeDigest(h, "C", r)
	}
	if m, ok := c.exact[dir]; ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte("E"))
			h.Write([]byte(k))
			writeDigest(h, "", m[k])
		}
	}
	return h.Sum(nil)
}

func writeDigest(h interface{ Write([]byte) (int, error) }, tag string, r indexedRule) {
	h.Write([]byte(tag))
	var buf [8]byte
	x := r.index
	for i := 0; i < 8; i++ {
		buf[i] = byte(x >> (8 * i))
	}
	h.Write(buf[:])
	if r.action == Include {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
}

func computeRuleHashes(n *ruleTrie, c *compiled, dir string, ancPrefixInclude bool) string {
	h := sha256.New()
	h.Write(ruleNodeDigest(c, dir))
	selfPrefixInclude := false
	if r, ok := c.prefix[dir]; ok && r.action == Include {
		n.subInclude = true
		selfPrefixInclude = true
	}
	// 作用于该节点路径自身的精确规则。
	if n.hasSelf && n.self.action == Include {
		n.subInclude = true
	}
	if ancPrefixInclude {
		n.subInclude = true
	}
	// 作用于本目录直接子项的通配规则：只影响「子项」层，
	// 因此不标记本节点 subInclude，仅写入哈希参与剪枝。
	keys := make([]string, 0, len(n.children))
	for k := range n.children {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		childAnc := ancPrefixInclude || selfPrefixInclude
		sub := computeRuleHashes(n.children[k], c, childPath(dir, k), childAnc)
		if n.children[k].subInclude {
			n.subInclude = true
		}
		h.Write([]byte{byte(len(k) >> 8), byte(len(k))})
		h.Write([]byte(k))
		h.Write([]byte(sub))
		h.Write([]byte{0})
	}
	n.hash = hex.EncodeToString(h.Sum(nil))
	return n.hash
}

// affectedEntry 是受影响文件的旧/新裁决对照。
type affectedEntry struct {
	path    string
	oldV    verdict
	newV    verdict
	oldSeen bool
	newSeen bool
}

// affectedFiles 联合遍历「旧树+旧规则」与「新树+新规则」：
// 某目录子树结构指纹相同（to.hash==tn.hash）且规则子树指纹相同
// （ro.hash==rn.hash）时整棵剪枝——其下所有文件的存在性与裁决都不变，
// 开销与其中文件数无关。
func affectedFiles(oldTree, newTree *trieNode, oldRules, newRules *compiled) []affectedEntry {
	rtOld := buildRuleTrie(oldRules)
	rtNew := buildRuleTrie(newRules)
	var out []affectedEntry
	rootPrefixOld := indexedRule{index: -1}
	rootPrefixNew := indexedRule{index: -1}
	if r, ok := oldRules.prefix[""]; ok {
		rootPrefixOld = r
	}
	if r, ok := newRules.prefix[""]; ok {
		rootPrefixNew = r
	}
	walkAffected(oldTree, newTree, rtOld, rtNew, oldRules, newRules,
		nil, rootPrefixOld, rootPrefixNew, &out)
	sortAffected(out)
	return out
}

// prefixBest 携带「到达本目录为止、沿祖先前缀链的最强前缀命中」。
func walkAffected(to, tn *trieNode, ro, rn *ruleTrie, co, cn *compiled,
	segs []string, prefOld, prefNew indexedRule, out *[]affectedEntry) {
	dir := joinSegs(segs)
	// 合并本目录自身的前缀规则，得到作用于本目录及全部后代的前缀继承。
	if r, ok := co.prefix[dir]; ok && r.index > prefOld.index {
		prefOld = r
	}
	if r, ok := cn.prefix[dir]; ok && r.index > prefNew.index {
		prefNew = r
	}
	// 剪枝：树子树结构一致、继承前缀裁决一致，且规则树在该子树完全相同
	// （含本目录局部规则与全部后代规则）—— 全部文件裁决不变。
	if to != nil && tn != nil && !to.isFile && to.hash == tn.hash &&
		prefEqual(prefOld, prefNew) && ruleSubtreeEq(ro, rn) {
		return
	}
	if to != nil && to.isFile || tn != nil && tn.isFile {
		ent := affectedEntry{path: joinSegs(segs)}
		if to != nil && to.isFile {
			ent.oldSeen = true
			ent.oldV = fileVerdict(co, segs)
		}
		if tn != nil && tn.isFile {
			ent.newSeen = true
			ent.newV = fileVerdict(cn, segs)
		}
		// 两侧都是文件（或一侧文件一侧不存在）：该路径本身即叶，记录后结束。
		oldLeaf := to == nil || to.isFile
		newLeaf := tn == nil || tn.isFile
		if oldLeaf && newLeaf {
			*out = append(*out, ent)
			return
		}
		// 一侧是文件、另一侧是同名目录：文件侧按出现/消失记录，
		// 但目录侧仍需继续向下枚举其后代，故不 return。
		*out = append(*out, ent)
	}
	treeSame := to != nil && tn != nil && to.hash == tn.hash
	names := map[string]bool{}
	onlyRulePaths := false
	// 树结构相同且继承前缀裁决一致时，普通子项的最终裁决不变，
	// 只沿规则真正作用的路径（规则树节点 / 直接精确目标）下降，
	// 避免枚举与本次变更无关的大量文件子项。
	if treeSame && prefEqual(prefOld, prefNew) {
		onlyRulePaths = true
		if ro != nil {
			for k := range ro.children {
				names[k] = true
			}
		}
		if rn != nil {
			for k := range rn.children {
				names[k] = true
			}
		}
		// 本目录直接精确规则的目标名（不体现在规则树结构边中）。
		for k := range co.exact[dir] {
			names[k] = true
		}
		for k := range cn.exact[dir] {
			names[k] = true
		}
		// 本层通配命中全部直接子项：通配变化时必须枚举所有子项。
		if childStarChanged(co, cn, dir) {
			onlyRulePaths = false
			for k := range to.children {
				names[k] = true
			}
			for k := range tn.children {
				names[k] = true
			}
		}
	}
	if !onlyRulePaths {
		if to != nil {
			for k := range to.children {
				names[k] = true
			}
		}
		if tn != nil {
			for k := range tn.children {
				names[k] = true
			}
		}
	}
	for name := range names {
		var oChild, nChild *trieNode
		var roChild, rnChild *ruleTrie
		if to != nil {
			oChild = to.children[name]
		}
		if tn != nil {
			nChild = tn.children[name]
		}
		if ro != nil {
			roChild = ro.children[name]
		}
		if rn != nil {
			rnChild = rn.children[name]
		}
		childSegs := append(append([]string{}, segs...), name)
		// 前缀继承对所有子项相同（前缀规则命中全部后代）；
		// 精确/通配只在叶级 fileVerdict 中按具体路径计算。
		walkAffected(oChild, nChild, roChild, rnChild, co, cn,
			childSegs, prefOld, prefNew, out)
	}
}

func prefEqual(a, b indexedRule) bool {
	return a.index == b.index && a.action == b.action
}

// ruleSubtreeEq 比较两个规则树子树是否完全一致（含局部与后代规则）。
func ruleSubtreeEq(ro, rn *ruleTrie) bool {
	if ro == nil && rn == nil {
		return true
	}
	if ro == nil || rn == nil {
		return false
	}
	return ro.hash == rn.hash
}

func childStarChanged(co, cn *compiled, dir string) bool {
	a, oa := co.child[dir]["*"]
	b, ob := cn.child[dir]["*"]
	if oa != ob {
		return true
	}
	return a != b
}

func sortAffected(out []affectedEntry) {
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
}

// subtreeMayInclude 判断：目录 dir（其路径 segs）自身或任意后代，
// 是否存在一条路径在完整有序规则下「最终裁决为包含」。
// 自顶向下沿规则树 DFS，携带当前最强的祖先前缀命中；
// 进入某目录时该目录前缀规则（作用于自身与全部后代）与祖先前缀取较新者。
// 只遍历该目录子树内的规则节点，与树中其他目录无关。
func subtreeMayInclude(c *compiled, rt *ruleTrie, fileRoot *trieNode, segs []string) bool {
	node := rt
	fileNode := fileRoot
	for _, s := range segs {
		if node != nil {
			node = node.children[s]
		}
		if fileNode != nil {
			fileNode = fileNode.children[s]
		}
	}
	// 起点：祖先链前缀命中（含该目录自身的前缀规则）。
	best := indexedRule{index: -1}
	for i := 0; i <= len(segs); i++ {
		if r, ok := c.prefix[joinSegs(segs[:i])]; ok && r.index > best.index {
			best = r
		}
	}
	dir := joinSegs(segs)
	// 作用于该目录路径自身的精确规则 / 父目录本层通配。
	if len(segs) > 0 {
		parent := joinSegs(segs[:len(segs)-1])
		name := segs[len(segs)-1]
		if m, ok := c.exact[parent]; ok {
			if r, ok := m[name]; ok && r.index > best.index {
				best = r
			}
		}
		if m, ok := c.child[parent]; ok {
			if r, ok := m["*"]; ok && r.index > best.index {
				best = r
			}
		}
	}
	if (best.index >= 0 && best.action == Include) || selfExactInclude(c, dir, best) {
		return true
	}
	if node == nil {
		return childLevelInclude(c, dir, best, fileNode)
	}
	return dfsInclude(c, node, fileNode, dir, best, best)
}

func selfExactInclude(c *compiled, dir string, prefixBest indexedRule) bool {
	if len(splitPath(dir)) == 0 {
		return false
	}
	segs := splitPath(dir)
	parent := joinSegs(segs[:len(segs)-1])
	name := segs[len(segs)-1]
	best := prefixBest
	if m, ok := c.exact[parent]; ok {
		if r, ok := m[name]; ok && r.index > best.index {
			best = r
		}
	}
	if m, ok := c.child[parent]; ok {
		if r, ok := m["*"]; ok && r.index > best.index {
			best = r
		}
	}
	return best.index >= 0 && best.action == Include
}

func dfsInclude(c *compiled, n *ruleTrie, fn *trieNode, dir string, prefixInherit, selfBest indexedRule) bool {
	// prefixInherit: 祖先链 + 本目录自身前缀规则的累积（前缀命中全部后代）。
	cur := prefixInherit
	if r, ok := c.prefix[dir]; ok && r.index > cur.index {
		cur = r
	}
	// selfBest: 父层算出的「本目录路径自身」最终命中（精确/通配/前缀）。
	if selfBest.index >= 0 && selfBest.action == Include {
		return true
	}
	hasStar := false
	var star indexedRule
	if m, ok := c.child[dir]; ok {
		if r, ok := m["*"]; ok {
			star, hasStar = r, true
		}
	}
	names := map[string]bool{}
	if n != nil {
		for name := range n.children {
			names[name] = true
		}
	}
	if fn != nil {
		for name := range fn.children {
			names[name] = true
		}
	}
	for name := range names {
		var ch *ruleTrie
		if n != nil {
			ch = n.children[name]
		}
		// 子项路径自身的最终命中 = 前缀累积(cur) + 父层通配 + 父层精确
		// + 子项自身路径前缀。
		childBest := cur
		if hasStar && star.index > childBest.index {
			childBest = star
		}
		if m, ok := c.exact[dir]; ok {
			if r, ok := m[name]; ok && r.index > childBest.index {
				childBest = r
			}
		}
		childPath := childPath(dir, name)
		if r, ok := c.prefix[childPath]; ok && r.index > childBest.index {
			childBest = r
		}
		var nextRule *ruleTrie
		if ch != nil {
			nextRule = ch
		}
		var nextFile *trieNode
		if fn != nil {
			nextFile = fn.children[name]
		}
		if nextRule == nil && nextFile == nil {
			if childBest.index >= 0 && childBest.action == Include {
				return true
			}
			continue
		}
		// 向孙层只传播前缀累积 cur（精确/通配不穿透）。
		if dfsInclude(c, nextRule, nextFile, childPath, cur, childBest) {
			return true
		}
	}
	return false
}

// childLevelInclude 检查 dir 的直接子项层是否存在最终包含命中，
// 用于规则树在 dir 处无节点的情况。
func childLevelInclude(c *compiled, dir string, cur indexedRule, fn *trieNode) bool {
	var star indexedRule
	hasStar := false
	if m, ok := c.child[dir]; ok {
		if r, ok := m["*"]; ok {
			star, hasStar = r, true
		}
	}
	names := map[string]bool{}
	if m, ok := c.exact[dir]; ok {
		for name := range m {
			names[name] = true
		}
	}
	if fn != nil {
		for name := range fn.children {
			names[name] = true
		}
	}
	for name := range names {
		best := cur
		if hasStar && star.index > best.index {
			best = star
		}
		if m, ok := c.exact[dir]; ok {
			if r, ok := m[name]; ok && r.index > best.index {
				best = r
			}
		}
		if best.index >= 0 && best.action == Include {
			return true
		}
	}
	return false
}
