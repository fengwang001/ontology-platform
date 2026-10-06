package exports

import "sync/atomic"

// Resolver 是并发安全的导出映射解析器。
// 内部持有当前表的指针，解析与整表替换可并发进行：
// 每次解析要么完整看到旧表，要么完整看到新表，绝不混用。
type Resolver struct {
	tbl atomic.Pointer[Table]
}

// NewResolver 用一张已构造的表创建解析器。
func NewResolver(t *Table) *Resolver {
	r := &Resolver{}
	r.tbl.Store(t)
	return r
}

// Replace 原子替换整张表。新表先完整构造并校验，
// 校验失败时返回 KindInvalidTable 错误且旧表继续生效。
func (r *Resolver) Replace(entries []Entry) error {
	t, err := NewTable(entries)
	if err != nil {
		return err
	}
	r.tbl.Store(t)
	return nil
}

// Table 返回当前表的快照（不可变，可安全并发使用）。
func (r *Resolver) Table() *Table {
	return r.tbl.Load()
}

// Resolve 解析一次导入请求。被拒绝的请求不会改变解析器状态。
func (r *Resolver) Resolve(subpath string, conditions []string) (Result, error) {
	return r.tbl.Load().Resolve(subpath, conditions)
}
