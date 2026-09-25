// Package jidx 在 rel.Store 之上维护以 b 为键的物化 join 索引。
package jidx

import (
	"fmt"
	"sort"

	"ontology/rel"
)

// Index 维护 byA[a]=a 可连接的 c 集，与反向索引 byB[b]={a:R[a]=b}（删除按它扇出，不扫 R 表）。
// 调用方（api）负责加锁并保证 rel.Store 与索引的更新顺序。
type Index struct {
	st   *rel.Store
	byA  map[int]map[int]struct{}
	byB  map[int]map[int]struct{}
	size int
	// delChecks 记录最近一次 DelC/UnbindR 中检查过的 R 条目数；非导出，仅供同包测试读取。
	delChecks int
}

// New 基于给定 rel.Store 创建空索引。
func New(st *rel.Store) *Index {
	return &Index{
		st:  st,
		byA: map[int]map[int]struct{}{},
		byB: map[int]map[int]struct{}{},
	}
}

func (ix *Index) addPair(a, c int) {
	set := ix.byA[a]
	if set == nil {
		set = map[int]struct{}{}
		ix.byA[a] = set
	}
	set[c] = struct{}{}
	ix.size++
}

func (ix *Index) removePair(a, c int) {
	if set := ix.byA[a]; set != nil {
		delete(set, c)
		if len(set) == 0 {
			delete(ix.byA, a)
		}
	}
	ix.size--
}

// BindR 处理新增 a→b：把 a 登记进 byB[b]，并按 S[b] 建立 a 的全部 join 对。
func (ix *Index) BindR(a, b int) {
	set := ix.byB[b]
	if set == nil {
		set = map[int]struct{}{}
		ix.byB[b] = set
	}
	set[a] = struct{}{}
	for _, c := range ix.st.Members(b) {
		ix.addPair(a, c)
	}
}

// UnbindR 处理 DelR：经 byA[a] 直接定位 a 的全部对，无需检查任何其他 R 条目。
func (ix *Index) UnbindR(a, b int) {
	ix.delChecks = 0 // 反向索引 byA 直接给出待删集合，检查的 R 条目数为 0
	for c := range ix.byA[a] {
		ix.removePair(a, c)
	}
	delete(ix.byA, a)
	if set := ix.byB[b]; set != nil {
		delete(set, a)
		if len(set) == 0 {
			delete(ix.byB, b)
		}
	}
}

// RebindR 处理 SetR 重赋：先移除 a 经旧 b 的全部对，再按新 b 重建。
func (ix *Index) RebindR(a, oldB, newB int) {
	if oldB == newB {
		return
	}
	ix.UnbindR(a, oldB)
	ix.BindR(a, newB)
}

// AddC 处理 AddS(b,c)：为 byB[b] 中每个 a 增加 (a,c)。
func (ix *Index) AddC(b, c int) {
	for a := range ix.byB[b] {
		ix.addPair(a, c)
	}
}

// DelC 处理 DelS(b,c)：经反向索引 byB[b] 扇出到全部匹配 a，
// delChecks 恰好等于该 b 下的 a 数 m，与 R 表总条数 N 无关。
func (ix *Index) DelC(b, c int) {
	ix.delChecks = 0
	for a := range ix.byB[b] {
		ix.delChecks++
		ix.removePair(a, c)
	}
}

// Join 返回与 a 可连接的 c（升序去重）；无对时返回 nil。
func (ix *Index) Join(a int) []int {
	set := ix.byA[a]
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Ints(out)
	return out
}

// Size 返回 join 对总数。
func (ix *Index) Size() int { return ix.size }

// SelfTest 在内部自建场景验证「DelS 检查数不随 R 表总条数增长」。
// 只回传成败判定，绝不向外暴露 delChecks 的数值。
func SelfTest() error {
	for _, N := range []int{100, 1000, 10000} {
		st := rel.New()
		ix := New(st)
		const m = 3
		for i := 0; i < N; i++ {
			b := 11 + i%97 // 其他 a 落在 11..107，永不等于 10，保证 b=10 恰有 m 条
			if i < m {
				b = 10 // 仅前 m 条 R[a]=10
			}
			st.SetR(i, b)
			ix.BindR(i, b)
		}
		if err := st.AddS(10, 100); err != nil {
			return err
		}
		ix.AddC(10, 100)
		ix.DelC(10, 100)
		if ix.delChecks != m {
			return fmt.Errorf("jidx: N=%d delS examined %d R entries, want %d", N, ix.delChecks, m)
		}
	}
	return nil
}
