// Package linkrepairtest 为链接记录修复裁决提供独立的“朴素参照模型”，
// 并在大量随机构造的损坏链接集合上与正式实现做差分对照。
//
// 参照模型刻意不 import ontology/linkrepair 的任何内部符号，
// 只复制其公开数据结构后，用最直白、逐步独立判定的方式重新实现一遍，
// 从而避免“实现自己验证自己”。两边对同一输入必须产出：
//   - 完全相同的保留链接集合；
//   - 对每条输入 Offset 完全相同的舍弃原因（四类互斥）。
package linkrepairtest

// 以下为朴素模型专用的本地类型，与正式实现类型隔离。

type OID string
type TID string

const unbounded = -1

type Card struct{ MaxFrom, MaxTo int }

type Type struct {
	ID   TID
	Card Card
}

type Raw struct {
	Offset int
	Type   TID
	From   OID
	To     OID
}

type Input struct {
	Types     map[TID]Type
	Available map[OID]bool
	Records   []Raw
}

type L struct {
	Type TID
	From OID
	To   OID
}

// 朴素判定结果：保留集合 + 每个 offset 的舍弃原因。
type NaiveResult struct {
	Kept   map[L]bool
	Reason map[int]string // offset -> 四类原因之一
}

type cand struct {
	l      L
	offset int
}

type member struct {
	l      L
	peer   OID
	offset int
}

const (
	rMalformed = "malformed_structure"
	rRef       = "reference_unavailable"
	rCard      = "cardinality_conflict"
	rDuplicate = "duplicate_record"
)

// NaiveRepair 是逐步独立判定的参照实现。
//
// 步骤与正式实现的固定优先级一一对应，但写法完全朴素：
//  1. 逐条判定结构，坏记录立即淘汰；
//  2. 好记录逐条核对两端引用，悬空立即淘汰；
//  3. 幸存者按内容去重，只留最小 Offset；
//  4. 对每个 (类型, From) 与 (类型, To) 分组分别手工排序裁决，
//     最后保留同时通过两端裁决的链接。
func NaiveRepair(in Input) NaiveResult {
	out := NaiveResult{Kept: map[L]bool{}, Reason: map[int]string{}}

	survivors := []cand{}

	// 第 1、2 级：结构与引用，逐条独立判定。
	for _, r := range in.Records {
		t, known := in.Types[r.Type]
		cardOK := !known || (validCard(t.Card))
		if r.Type == "" || !known || !cardOK || r.From == "" || r.To == "" {
			out.Reason[r.Offset] = rMalformed
			continue
		}
		if !in.Available[r.From] || !in.Available[r.To] {
			out.Reason[r.Offset] = rRef
			continue
		}
		survivors = append(survivors, cand{L{r.Type, r.From, r.To}, r.Offset})
	}

	// 第 4 级（先于基数，保证每组内容唯一）：手工去重，留最小 Offset。
	minOffset := map[L]int{}
	for _, c := range survivors {
		if old, ok := minOffset[c.l]; !ok || c.offset < old {
			minOffset[c.l] = c.offset
		}
	}
	uniq := []cand{}
	for _, c := range survivors {
		if minOffset[c.l] != c.offset {
			out.Reason[c.offset] = rDuplicate
			continue
		}
		uniq = append(uniq, c)
	}

	// 第 3 级：两个方向分别用插入排序式朴素选择计算允许集合。
	okFrom := map[L]bool{}
	okTo := map[L]bool{}
	selectSide(in.Types, uniq, true, okFrom, out.Reason)
	selectSide(in.Types, uniq, false, okTo, out.Reason)

	// 交集；基数原因只在交集阶段定案，保证一条链接最多一个原因。
	for _, c := range uniq {
		if okFrom[c.l] && okTo[c.l] {
			out.Kept[c.l] = true
			continue
		}
		if _, exists := out.Reason[c.offset]; !exists {
			out.Reason[c.offset] = rCard
		}
	}

	// 安全断言：若某链接两端都失败，上面的 map 赋值只写一次。
	return out
}

func validCard(c Card) bool {
	f := c.MaxFrom == unbounded || c.MaxFrom >= 1
	to := c.MaxTo == unbounded || c.MaxTo >= 1
	return f && to
}

// selectSide 用最简单的“收集-排序-取前 cap”方式裁决一个方向。
func selectSide(
	types map[TID]Type, uniq []cand,
	fromSide bool, allowed map[L]bool, _ map[int]string,
) {
	groups := map[string][]member{}
	for _, c := range uniq {
		var owner OID
		var peer OID
		if fromSide {
			owner, peer = c.l.From, c.l.To
		} else {
			owner, peer = c.l.To, c.l.From
		}
		key := string(c.l.Type) + "|" + string(owner)
		groups[key] = append(groups[key], member{c.l, peer, c.offset})
	}

	for key, ms := range groups {
		typ := TID(takeType(key))
		cap := types[typ].Card.MaxTo
		if fromSide {
			cap = types[typ].Card.MaxFrom
		}
		if cap == unbounded {
			for _, m := range ms {
				allowed[m.l] = true
			}
			continue
		}
		// 朴素插入排序：对方 ID 升序，平局 Offset 升序。
		sorted := make([]member, len(ms))
		copy(sorted, ms)
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0; j-- {
				a, b := sorted[j-1], sorted[j]
				if a.peer > b.peer || (a.peer == b.peer && a.offset > b.offset) {
					sorted[j-1], sorted[j] = b, a
				} else {
					break
				}
			}
		}
		for i, m := range sorted {
			if i < cap {
				allowed[m.l] = true
			}
		}
	}
}

func takeType(key string) string {
	for i, r := range key {
		if r == '|' {
			return key[:i]
		}
	}
	return key
}
