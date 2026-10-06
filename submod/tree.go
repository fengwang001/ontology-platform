package submod

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Stats 解析开销计数，用于以可验证方式证明性能特性。
type Stats struct {
	RecordsScanned atomic.Int64 // 子模块表记录探测次数
	CommitsVisited atomic.Int64 // 提交对象访问次数
}

// mountNode 挂载树节点：一条记录及其解析结果。
type mountNode struct {
	Path     string // 相对超级仓库根的全路径
	Record   SubmoduleRecord
	Repo     *Repo   // nil 表示目标仓库未注册
	Pinned   *Commit // nil 表示悬空固定（提交缺失或仓库缺失）
	Children []*mountNode
}

// findRecord 在表中寻找路径为 remaining 或其段前缀的记录。
// 只探测 remaining 的前缀，开销与表外挂载点总数无关。
func (c *Coordinator) findRecord(t Table, remaining string) (SubmoduleRecord, string, bool) {
	segs := strings.Split(remaining, "/")
	for i := 1; i <= len(segs); i++ {
		prefix := strings.Join(segs[:i], "/")
		c.stats.RecordsScanned.Add(1)
		if rec, ok := t[prefix]; ok {
			rest := ""
			if i < len(segs) {
				rest = strings.Join(segs[i:], "/")
			}
			return rec, rest, true
		}
	}
	return SubmoduleRecord{}, "", false
}

// resolveMount 沿挂载链解析全路径 np（已规范化）对应的节点。
// 只访问当前链上的记录与提交：循环判定开销与无关分支规模无关。
func (c *Coordinator) resolveMount(np string) (*mountNode, error) {
	table := c.headTable()
	chain := map[RepoID]bool{c.super: true}
	full := ""
	remaining := np
	for {
		rec, rest, ok := c.findRecord(table, remaining)
		if !ok {
			return nil, fmt.Errorf("%w: no mount at %q", ErrInvalidParam, np)
		}
		full = joinPath(full, rec.Path)
		node := &mountNode{Path: full, Record: rec}
		if chain[rec.Repo] {
			return nil, fmt.Errorf("%w: repo %q already on ancestor chain of %q", ErrCycle, rec.Repo, full)
		}
		chain[rec.Repo] = true
		node.Repo = c.store.Get(rec.Repo)
		if node.Repo != nil {
			c.stats.CommitsVisited.Add(1)
			node.Pinned = node.Repo.Commits[rec.Pinned]
		}
		if rest == "" {
			return node, nil
		}
		if node.Pinned == nil {
			return nil, fmt.Errorf("%w: cannot resolve %q under dangling pin at %q", ErrDanglingPin, np, full)
		}
		table = node.Pinned.Table
		remaining = rest
	}
}

// buildTree 自顶向下构建整棵挂载树。
// 任一挂载链上出现循环立即报 ErrCycle；收集悬空固定节点供调用方判定。
func (c *Coordinator) buildTree() (root *mountNode, dangling []*mountNode, err error) {
	root = &mountNode{Path: ""}
	chain := map[RepoID]bool{c.super: true}
	dangling, err = c.expand(root, c.headTable(), chain)
	if err != nil {
		return nil, nil, err
	}
	return root, dangling, nil
}

func (c *Coordinator) expand(parent *mountNode, t Table, chain map[RepoID]bool) ([]*mountNode, error) {
	var dangling []*mountNode
	for _, rec := range t {
		c.stats.RecordsScanned.Add(1)
		node := &mountNode{Path: joinPath(parent.Path, rec.Path), Record: rec}
		parent.Children = append(parent.Children, node)
		if chain[rec.Repo] {
			return nil, fmt.Errorf("%w: repo %q already on ancestor chain of %q", ErrCycle, rec.Repo, node.Path)
		}
		node.Repo = c.store.Get(rec.Repo)
		if node.Repo != nil {
			c.stats.CommitsVisited.Add(1)
			node.Pinned = node.Repo.Commits[rec.Pinned]
		}
		if node.Pinned == nil {
			dangling = append(dangling, node)
			continue
		}
		chain[rec.Repo] = true
		sub, err := c.expand(node, node.Pinned.Table, chain)
		delete(chain, rec.Repo)
		if err != nil {
			return nil, err
		}
		dangling = append(dangling, sub...)
	}
	return dangling, nil
}

// walk 先序遍历挂载树（自顶向下）。
func walk(n *mountNode, fn func(*mountNode)) {
	for _, child := range n.Children {
		fn(child)
		walk(child, fn)
	}
}
