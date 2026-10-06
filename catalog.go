package ontology

type codeNode struct {
	parent     string
	accidental bool
	hasParent  bool
}

// catalog 管理疾病编码目录：编码至多一个上级，形成森林；
// 每个编码可被标记为意外类，意外标记沿层级向下继承。
// 目录只登记、不重挂父节点（森林天然无环），环检测作为防御保留。
type catalog struct {
	nodes map[string]*codeNode
}

func newCatalog() *catalog {
	return &catalog{nodes: make(map[string]*codeNode)}
}

// addCode 登记编码。编码重复报编码重复；上级非空且不存在报上级不存在；
// 使编码成为自身祖先的变更报参数非法（当前仅新增，环不可能，保留为防御）。
func (c *catalog) addCode(code, parent string, accidental bool) error {
	if code == "" {
		return ErrInvalidParam
	}
	if _, ok := c.nodes[code]; ok {
		return ErrCodeDuplicate
	}
	hasParent := parent != ""
	if hasParent {
		if _, ok := c.nodes[parent]; !ok {
			return ErrParentNotFound
		}
	}
	if hasParent && c.isAncestor(parent, code) {
		return ErrInvalidParam
	}
	c.nodes[code] = &codeNode{parent: parent, accidental: accidental, hasParent: hasParent}
	return nil
}

func (c *catalog) exists(code string) bool {
	_, ok := c.nodes[code]
	return ok
}

// isAncestor 判定 anc 是否为 node 的祖先（沿父链上溯，O(树高)）。
func (c *catalog) isAncestor(anc, node string) bool {
	cur := node
	for {
		n, ok := c.nodes[cur]
		if !ok || !n.hasParent {
			return false
		}
		if n.parent == anc {
			return true
		}
		cur = n.parent
	}
}

// ancestors 返回从父节点到根的祖先链（不含自身），O(树高)。
func (c *catalog) ancestors(code string) []string {
	var chain []string
	cur := code
	for {
		n, ok := c.nodes[cur]
		if !ok || !n.hasParent {
			return chain
		}
		chain = append(chain, n.parent)
		cur = n.parent
	}
}

// isAccidental 判定编码是否意外类：自身标记或任一祖先标记，O(树高)。
func (c *catalog) isAccidental(code string) bool {
	cur := code
	for {
		n, ok := c.nodes[cur]
		if !ok {
			return false
		}
		if n.accidental {
			return true
		}
		if !n.hasParent {
			return false
		}
		cur = n.parent
	}
}
