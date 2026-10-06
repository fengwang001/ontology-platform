// Package resolve 在登记状态的一致快照上解析读写目标。
package resolve

import (
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/indexreg"
)

// ErrNoWriteIndex 表示别名存在但按推定规则没有写索引。
var ErrNoWriteIndex = errors.New("resolve: no write index")

// touched 是非导出计数器，记录 ResolveWrite 触碰的成员记录数，
// 用于证明写解析与别名成员数无关（每次至多 1 条）。
var touched atomic.Int64

// ReadTarget 是一个读目标：索引名与其成员 filter（索引自身为 nil）。
type ReadTarget struct {
	Index  string
	Filter *string
}

// ResolveRead 对索引返回它自己（已关闭报 ErrIndexClosed）；对别名返回未关闭
// 成员按索引名字节序的列表，关闭成员被静默跳过，全部关闭时返回空列表。
func ResolveRead(r *indexreg.Registry, name string) (targets []ReadTarget, err error) {
	r.View(func(st *indexreg.State) {
		if idx, ok := st.Indices[name]; ok {
			if idx.Closed {
				err = fmt.Errorf("resolve: index %q: %w", name, indexreg.ErrIndexClosed)
				return
			}
			targets = []ReadTarget{{Index: name}}
			return
		}
		al, ok := st.Aliases[name]
		if !ok {
			err = fmt.Errorf("resolve: name %q: %w", name, indexreg.ErrNameNotFound)
			return
		}
		names := make([]string, 0, len(al.Members))
		for idx := range al.Members {
			names = append(names, idx)
		}
		sort.Strings(names)
		for _, idx := range names {
			ix := st.Indices[idx]
			if ix == nil || ix.Closed {
				continue
			}
			m := al.Members[idx]
			targets = append(targets, ReadTarget{Index: idx, Filter: m.Filter})
		}
	})
	return targets, err
}

// ResolveWrite 对索引返回它自己（已关闭报 ErrIndexClosed）；对别名返回推定的
// 写索引，无写索引报 ErrNoWriteIndex，写索引已关闭报 ErrIndexClosed 且不回落。
func ResolveWrite(r *indexreg.Registry, name string) (target string, err error) {
	r.View(func(st *indexreg.State) {
		if idx, ok := st.Indices[name]; ok {
			if idx.Closed {
				err = fmt.Errorf("resolve: index %q: %w", name, indexreg.ErrIndexClosed)
				return
			}
			target = name
			return
		}
		al, ok := st.Aliases[name]
		if !ok {
			err = fmt.Errorf("resolve: name %q: %w", name, indexreg.ErrNameNotFound)
			return
		}
		if !al.HasWrite {
			err = fmt.Errorf("resolve: alias %q: %w", name, ErrNoWriteIndex)
			return
		}
		// 只触碰写索引对应的那一条成员记录，与别名成员总数无关。
		if _, ok := al.Members[al.WriteIndex]; !ok {
			err = fmt.Errorf("resolve: alias %q: %w", name, ErrNoWriteIndex)
			return
		}
		touched.Add(1)
		if ix := st.Indices[al.WriteIndex]; ix == nil || ix.Closed {
			err = fmt.Errorf("resolve: index %q: %w", al.WriteIndex, indexreg.ErrIndexClosed)
			return
		}
		target = al.WriteIndex
	})
	return target, err
}
