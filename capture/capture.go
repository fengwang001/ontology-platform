// Package capture 分析一个作用域引用了哪些外层声明。依赖 resolve。
package capture

import (
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

// Entry 是一条被捕获的外层声明及其所在深度。
type Entry struct {
	Decl  name.Decl
	Depth int
}

type declID struct {
	depth int
	n     name.Name
}

// Of 列出 refs 中属于作用域 s、且成功解析到严格外层深度的声明。
// 同一外层声明被引用多次只算一条；解析失败的引用不产生捕获。
func Of(s *scope.Scope, refs []resolve.Ref, res *resolve.Resolver) ([]Entry, error) {
	seen := make(map[declID]bool)
	var out []Entry
	for _, ref := range refs {
		if ref.Scope != s {
			continue
		}
		r, err := res.Resolve(ref)
		if err != nil {
			return nil, err
		}
		if r.Depth >= s.Depth() {
			continue
		}
		id := declID{depth: r.Depth, n: r.Decl.Name}
		if !seen[id] {
			seen[id] = true
			out = append(out, Entry{Decl: r.Decl, Depth: r.Depth})
		}
	}
	return out, nil
}
