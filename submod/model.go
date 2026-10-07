// Package submod 实现超级仓库的子模块固定与更新协调器。
//
// 模型：每个仓库（Repo）有提交图（Commit 带唯一标识与若干父提交）与若干
// 分支；超级仓库的每个提交附带一张子模块表（Table），每条记录（Record）
// 含挂载路径、目标仓库标识、固定提交标识与可选的跟踪分支。子模块的固定
// 提交自身可能也带子模块表，从而形成一棵挂载树。
package submod

import (
	"fmt"
	"sort"
	"strings"
)

// Record 是子模块表中的一条记录。
type Record struct {
	Repo   string // 目标仓库标识
	Commit string // 固定提交标识
	Track  string // 可选的跟踪分支，空串表示不跟踪
}

// Table 以规范化后的挂载路径为键的子模块表。
type Table map[string]Record

// Commit 是提交图中的一个节点。
type Commit struct {
	ID      string
	Parents []string
	Table   Table // 该提交附带的子模块表，可为 nil
}

// Repo 是一个仓库：提交图 + 命名分支。
type Repo struct {
	ID       string
	Commits  map[string]*Commit
	Branches map[string]string // 分支名 -> 顶端提交标识
	autoN    int               // 自动生成提交标识的序号
}

// NewRepo 创建一个空仓库。
func NewRepo(id string) *Repo {
	return &Repo{ID: id, Commits: map[string]*Commit{}, Branches: map[string]string{}}
}

// AddCommit 以显式标识加入一个提交（建模/测试用）。父提交必须已存在。
func (r *Repo) AddCommit(id string, parents []string, table Table) error {
	if id == "" {
		return fmt.Errorf("submod: 仓库 %q 的提交标识为空", r.ID)
	}
	if _, ok := r.Commits[id]; ok {
		return fmt.Errorf("submod: 仓库 %q 中提交 %q 重复", r.ID, id)
	}
	for _, p := range parents {
		if _, ok := r.Commits[p]; !ok {
			return fmt.Errorf("submod: 仓库 %q 中提交 %q 的父提交 %q 不存在", r.ID, id, p)
		}
	}
	r.Commits[id] = &Commit{ID: id, Parents: append([]string(nil), parents...), Table: table}
	return nil
}

// createCommit 生成一个自动标识的提交（协调器内部用于落地子模块表变更）。
func (r *Repo) createCommit(parents []string, table Table) string {
	for {
		r.autoN++
		id := fmt.Sprintf("auto-%d", r.autoN)
		if _, ok := r.Commits[id]; !ok {
			r.Commits[id] = &Commit{ID: id, Parents: append([]string(nil), parents...), Table: table}
			return id
		}
	}
}

// SetBranch 把分支指向一个已存在的提交（可前移/回移，用于建模远端变化）。
func (r *Repo) SetBranch(name, commit string) error {
	if _, ok := r.Commits[commit]; !ok {
		return fmt.Errorf("submod: 仓库 %q 中提交 %q 不存在，无法设置分支 %q", r.ID, commit, name)
	}
	r.Branches[name] = commit
	return nil
}

// hasCommit 报告提交是否存在于本仓库。
func (r *Repo) hasCommit(id string) bool {
	_, ok := r.Commits[id]
	return ok
}

// isAncestorOrEqual 报告 old 是否为 newer 的祖先或二者相等（沿父链可达）。
// 调用方保证两个提交都存在于 repo 中。
func isAncestorOrEqual(repo *Repo, old, newer string) bool {
	seen := map[string]bool{}
	stack := []string{newer}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == old {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if c := repo.Commits[cur]; c != nil {
			stack = append(stack, c.Parents...)
		}
	}
	return false
}

// NormalizePath 规范化挂载路径：统一斜杠、折叠重复分隔符与 "."、解析
// 不越界的 ".."。空路径或越界 ".." 视为参数非法。
func NormalizePath(p string) (string, error) {
	if p == "" {
		return "", &Error{Code: ErrCodeInvalidParam, Msg: "挂载路径为空"}
	}
	p = strings.ReplaceAll(p, "\\", "/")
	var out []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) == 0 {
				return "", &Error{Code: ErrCodeInvalidParam, Path: p, Msg: "挂载路径越界: " + p}
			}
			out = out[:len(out)-1]
		default:
			out = append(out, seg)
		}
	}
	if len(out) == 0 {
		return "", &Error{Code: ErrCodeInvalidParam, Path: p, Msg: "挂载路径为空: " + p}
	}
	return strings.Join(out, "/"), nil
}

// isAncestorPath 报告 a 是否为 b 的祖先目录（严格前缀，按段对齐）。
func isAncestorPath(a, b string) bool {
	return a != b && strings.HasPrefix(b, a+"/")
}

// validateTable 规范化一张子模块表并校验：路径合法、仓库标识非空、
// 规范化后不重复、任意两条记录互不为祖先后代。
func validateTable(t Table) (Table, error) {
	if t == nil {
		return nil, nil
	}
	out := make(Table, len(t))
	paths := make([]string, 0, len(t))
	for k, rec := range t {
		nk, err := NormalizePath(k)
		if err != nil {
			return nil, &Error{Code: ErrCodeInvalidTable, Path: k, Msg: "子模块表含非法路径: " + k}
		}
		if rec.Repo == "" {
			return nil, &Error{Code: ErrCodeInvalidTable, Path: nk, Msg: "子模块表含空仓库标识: " + nk}
		}
		if _, dup := out[nk]; dup {
			return nil, &Error{Code: ErrCodeInvalidTable, Path: nk, Msg: "路径规范化后重复: " + nk}
		}
		out[nk] = rec
		paths = append(paths, nk)
	}
	// 祖先后代检查：对每个路径查它的所有段对齐前缀是否也在表中，
	// 复杂度 O(路径数 x 深度)，不随表规模平方增长。
	set := make(map[string]bool, len(paths))
	for _, p := range paths {
		set[p] = true
	}
	for _, p := range paths {
		segs := strings.Split(p, "/")
		for i := 1; i < len(segs); i++ {
			if prefix := strings.Join(segs[:i], "/"); set[prefix] {
				return nil, &Error{Code: ErrCodeInvalidTable, Path: p,
					Msg: fmt.Sprintf("路径 %q 是 %q 的后代，子模块表非法", p, prefix)}
			}
		}
	}
	return out, nil
}

// sortedKeys 返回排序后的表键，保证遍历确定。
func sortedKeys(t Table) []string {
	keys := make([]string, 0, len(t))
	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// joinPath 拼接挂载树路径。
func joinPath(prefix, p string) string {
	if prefix == "" {
		return p
	}
	return prefix + "/" + p
}
