// Package phrase 实现短语查询（多词位置对齐）与布尔 AND 查询。
package phrase

import "ontology/posting"

// Hit 是一次短语命中：文档号 + 起始位置（0 起）。
type Hit struct {
	Doc   uint32
	Start uint32
}

// Stats 携带非导出比较计数器，用于复杂度断言。
type Stats struct {
	comparisons int
}

// Comparisons 返回查询过程中的位置/文档号比较次数。
func (s *Stats) Comparisons() int { return s.comparisons }

// Phrase 返回短语在倒排链集合中的全部命中，允许重叠计数。
// lists[i] 是短语第 i 个词的倒排链。比较次数与位置表长度之和成正比。
func Phrase(lists []posting.List, st *Stats) []Hit {
	k := len(lists)
	if k == 0 {
		return nil
	}
	if st == nil {
		st = &Stats{}
	}
	for _, l := range lists {
		if len(l) == 0 {
			return nil
		}
	}
	if k == 1 { // 短语长 1 退化为单词查询
		var out []Hit
		for _, p := range lists[0] {
			for _, pos := range p.Pos {
				out = append(out, Hit{p.Doc, pos})
			}
		}
		return out
	}
	var out []Hit
	ptr := make([]int, k)
	for i0 := 0; i0 < len(lists[0]); i0++ {
		doc := lists[0][i0].Doc
		posLists := make([][]uint32, k)
		posLists[0] = lists[0][i0].Pos
		matched := true
		for j := 1; j < k; j++ { // 文档号对齐：游标只前进
			for ptr[j] < len(lists[j]) {
				st.comparisons++
				if lists[j][ptr[j]].Doc >= doc {
					break
				}
				ptr[j]++
			}
			if ptr[j] >= len(lists[j]) {
				return out
			}
			st.comparisons++
			if lists[j][ptr[j]].Doc != doc {
				matched = false
				break
			}
			posLists[j] = lists[j][ptr[j]].Pos
		}
		if matched {
			out = alignPositions(posLists, doc, st, out)
		}
	}
	return out
}

// alignPositions 在同一文档内对齐 k 条位置表，锚表每次只前进一格以允许重叠。
func alignPositions(pl [][]uint32, doc uint32, st *Stats, out []Hit) []Hit {
	k := len(pl)
	idx := make([]int, k)
	for idx[0] < len(pl[0]) {
		a := pl[0][idx[0]]
		ok := true
		for i := 1; i < k; i++ {
			target := a + uint32(i)
			for idx[i] < len(pl[i]) {
				st.comparisons++
				if pl[i][idx[i]] >= target {
					break
				}
				idx[i]++
			}
			if idx[i] >= len(pl[i]) {
				return out
			}
			st.comparisons++
			if pl[i][idx[i]] != target {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, Hit{Doc: doc, Start: a})
		}
		idx[0]++ // 命中后仅锚表前进一格：重叠命中不被跳过
	}
	return out
}

// And 返回同时包含全部词的文档号（升序）。以最短链驱动，
// 其余链用指数跳跃 + 二分定位，比较次数 <= 4*最短链长*链数。
func And(lists []posting.List, st *Stats) []uint32 {
	k := len(lists)
	if k == 0 {
		return nil
	}
	if st == nil {
		st = &Stats{}
	}
	docs := make([][]uint32, k)
	shortest := 0
	for i, l := range lists {
		if len(l) == 0 {
			return nil
		}
		docs[i] = make([]uint32, len(l))
		for j, p := range l {
			docs[i][j] = p.Doc
		}
		if len(l) < len(lists[shortest]) {
			shortest = i
		}
	}
	ptr := make([]int, k)
	var out []uint32
	for _, cand := range docs[shortest] {
		match := true
		for j := 0; j < k; j++ {
			if j == shortest {
				continue
			}
			ptr[j] = gallop(docs[j], ptr[j], cand, st)
			if ptr[j] >= len(docs[j]) {
				return out
			}
			st.comparisons++
			if docs[j][ptr[j]] != cand {
				match = false
			}
		}
		if match {
			out = append(out, cand)
		}
	}
	return out
}

// gallop 返回 d 中从 from 起第一个 >= target 的下标；不存在时返回 len(d)。
func gallop(d []uint32, from int, target uint32, st *Stats) int {
	n := len(d)
	if from >= n {
		return n
	}
	st.comparisons++
	if d[from] >= target {
		return from
	}
	lo, step := from, 1 // 不变量：d[lo] < target
	for lo+step < n {
		st.comparisons++
		if d[lo+step] >= target {
			break
		}
		lo += step
		step *= 2
	}
	hi := lo + step
	if hi > n {
		hi = n
	}
	l, r := lo+1, hi // 在 (lo, hi] 中二分第一个 >= target
	for l < r {
		m := int(uint(l+r) >> 1)
		st.comparisons++
		if d[m] >= target {
			r = m
		} else {
			l = m + 1
		}
	}
	return l
}
