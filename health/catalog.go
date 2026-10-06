package health

import (
	"sort"
	"sync"
)

type codeNode struct {
	parent   string // 空串表示顶级
	accident bool
}

// Catalog 疾病编码层级目录（至多一个上级），自带读写锁。
// 所有 Engine 入口均在 Catalog 锁之外保留 Engine 锁，加锁次序固定为
// Engine -> Catalog，不会反向获取，因此无死锁。
type Catalog struct {
	mu    sync.RWMutex
	nodes map[string]*codeNode
}

// NewCatalog 创建空目录。
func NewCatalog() *Catalog { return &Catalog{nodes: map[string]*codeNode{}} }

// AddCode 登记编码：编码重复报 ErrCodeDuplicate；上级不存在报 ErrParentNotFound；
// 参数（空编码）非法报 ErrInvalidArgument。
func (c *Catalog) AddCode(in CodeInput) error {
	if in.Code == "" {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.nodes[in.Code]; ok {
		return ErrCodeDuplicate
	}
	if in.Parent != "" {
		if _, ok := c.nodes[in.Parent]; !ok {
			return ErrParentNotFound
		}
	}
	c.nodes[in.Code] = &codeNode{parent: in.Parent, accident: in.Accident}
	return nil
}

// ChangeParent 变更上级：空 newParent 表示改为顶级。编码或新上级不存在时
// 分别报 ErrCodeNotFound / ErrParentNotFound；使 code 成为自身祖先时
// 报 ErrInvalidArgument。
func (c *Catalog) ChangeParent(code, newParent string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	node, ok := c.nodes[code]
	if !ok {
		return ErrCodeNotFound
	}
	if newParent != "" {
		if _, ok := c.nodes[newParent]; !ok {
			return ErrParentNotFound
		}
	}
	if newParent == code {
		return ErrInvalidArgument
	}
	for ancestor := newParent; ancestor != ""; {
		if ancestor == code {
			return ErrInvalidArgument
		}
		ancestor = c.nodes[ancestor].parent
	}
	node.parent = newParent
	return nil
}

// has 判断编码是否在目录中（调用方须自行持锁）。
func (c *Catalog) has(code string) bool {
	_, ok := c.nodes[code]
	return ok
}

// parentOf 返回上级（空串表示顶级）与是否存在（调用方持锁）。
func (c *Catalog) parentOf(code string) (string, bool) {
	node, ok := c.nodes[code]
	if !ok {
		return "", false
	}
	return node.parent, true
}

// accident 判断编码或任一祖先是否标记为意外（调用方持锁）。
func (c *Catalog) accident(code string) bool {
	for code != "" {
		node, ok := c.nodes[code]
		if !ok {
			return false
		}
		if node.accident {
			return true
		}
		code = node.parent
	}
	return false
}

// codesSorted 返回目录全部编码的排序快照（调用方持锁）。
func (c *Catalog) codesSorted() []string {
	out := make([]string, 0, len(c.nodes))
	for code := range c.nodes {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}
