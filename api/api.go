// Package api 对外门面：建表、加分区、合取范围谓词裁剪查询、自检。依赖 prune。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/part"
	"ontology/prune"
)

// 三类可判定错误，互不相同。
var (
	ErrInvalidPartition = part.ErrInvalidPartition
	ErrInvalidPredicate = prune.ErrInvalidPredicate
	ErrUnknownPartition = prune.ErrUnknownPartition
)

// Pruner 是并发安全的分区裁剪器。
type Pruner struct {
	mu sync.RWMutex
	t  *prune.Table
}

// New 创建空裁剪器。
func New() *Pruner { return &Pruner{t: prune.NewTable()} }

// AddPartition 注册一个分区；非法（lo>=hi、minv>maxv、id 重复或范围重叠）整体失败。
func (p *Pruner) AddPartition(id string, lo, hi, minv, maxv int64) error {
	pp, err := part.New(id, lo, hi, minv, maxv)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.t.Add(pp)
}

// Query 返回扫描集与裁剪集（按 lo 升序）；谓词非法整体失败。
func (p *Pruner) Query(Plo, Phi, Vlo, Vhi int64) (scan, pruned []string, err error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.t.Query(prune.Predicate{Plo: Plo, Phi: Phi, Vlo: Vlo, Vhi: Vhi})
}

// QueryRefs 仅裁剪给定 id 引用的分区；引用未注册 id 整体失败。
func (p *Pruner) QueryRefs(ids []string, Plo, Phi, Vlo, Vhi int64) (scan, pruned []string, err error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.t.QueryRefs(ids, prune.Predicate{Plo: Plo, Phi: Phi, Vlo: Vlo, Vhi: Vhi})
}

// SelfCheck 用内置的第三节五分区与谓词核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	pr := New()
	rows := [][5]int64{{0, 10, 10, 20, 0}, {10, 20, 30, 40, 0}, {20, 30, 50, 60, 0}, {30, 40, 15, 25, 0}, {40, 50, 60, 80, 0}}
	ids := []string{"P0", "P1", "P2", "P3", "P4"}
	for i, r := range rows {
		if err := pr.AddPartition(ids[i], r[0], r[1], r[2], r[3]); err != nil {
			return fmt.Errorf("selfcheck add: %w", err)
		}
	}
	scan, pruned, err := pr.Query(20, 50, 30, 60)
	if err != nil {
		return fmt.Errorf("selfcheck query: %w", err)
	}
	// 不变量2 剪枝最大化：扫描集恰为两维都相交者。
	if !reflect.DeepEqual(scan, []string{"P2"}) || !reflect.DeepEqual(pruned, []string{"P0", "P1", "P3", "P4"}) {
		return errors.New("selfcheck: exactness violated")
	}
	// 不变量1 不漏：被裁者边界盒至少一维与谓词无交集（由构造复核）。
	boxes := map[string][4]int64{"P0": {0, 10, 10, 20}, "P1": {10, 20, 30, 40}, "P3": {30, 40, 15, 25}, "P4": {40, 50, 60, 80}}
	for _, id := range pruned {
		b := boxes[id]
		disjointP := b[1] <= 20 || b[0] >= 50
		disjointV := b[3] < 30 || b[2] >= 60
		if !disjointP && !disjointV {
			return fmt.Errorf("selfcheck: soundness violated by %s", id)
		}
	}
	// 不变量3 顺序无关：先动态后静态重算，扫描集必须相同。
	scan2 := []string{}
	for i, r := range rows {
		pp, _ := part.New(ids[i], r[0], r[1], r[2], r[3])
		if !pp.DynamicPrune(30, 60) && !pp.StaticPrune(20, 50) {
			scan2 = append(scan2, ids[i])
		}
	}
	if !reflect.DeepEqual(scan, scan2) {
		return errors.New("selfcheck: order dependence detected")
	}
	// 不变量4 失败不留痕：三类拒绝后状态不变、仍可正常查询。
	before := scan
	if err := pr.AddPartition("PX", 5, 5, 0, 1); !errors.Is(err, ErrInvalidPartition) {
		return errors.New("selfcheck: bad partition not rejected")
	}
	if _, _, err := pr.Query(50, 50, 30, 60); !errors.Is(err, ErrInvalidPredicate) {
		return errors.New("selfcheck: bad predicate not rejected")
	}
	if _, _, err := pr.QueryRefs([]string{"NOPE"}, 20, 50, 30, 60); !errors.Is(err, ErrUnknownPartition) {
		return errors.New("selfcheck: unknown ref not rejected")
	}
	after, _, err := pr.Query(20, 50, 30, 60)
	if err != nil || !reflect.DeepEqual(before, after) {
		return errors.New("selfcheck: state changed after rejected ops")
	}
	return nil
}
