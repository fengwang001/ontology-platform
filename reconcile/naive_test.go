package reconcile

import "sort"

// naiveMatcher 严格按题面逐步书写的朴素模拟：
// 每轮开始重新对当前未匹配行按 id 排序切片，候选逐行扫描，
// 命中后立即从切片中删除；不做任何索引优化。
type naiveMatcher struct {
	bank     map[string]Line
	book     map[string]Line
	matched  map[string]bool // "b:id" / "k:id"
	forbid   map[[2]string]bool
	matches  map[int64]Match
	reversed map[int64]bool
	nextMid  int64
}

func newNaive() *naiveMatcher {
	return &naiveMatcher{
		bank:     map[string]Line{},
		book:     map[string]Line{},
		matched:  map[string]bool{},
		forbid:   map[[2]string]bool{},
		matches:  map[int64]Match{},
		reversed: map[int64]bool{},
		nextMid:  1,
	}
}

func sortedLines(m map[string]Line, matched map[string]bool, prefix string) []Line {
	var out []Line
	for _, ln := range m {
		if !matched[prefix+ln.ID] {
			out = append(out, ln)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func removeLine(s []Line, id string) []Line {
	for i, ln := range s {
		if ln.ID == id {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func adiff(a, b int64) int64 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}

func (n *naiveMatcher) reconcile(w int64) []Match {
	var produced []Match

	pick := func(b Line, books *[]Line, roundNum int, requireRef bool) bool {
		best := -1
		bestDiff := int64(0)
		for j, k := range *books {
			if n.forbid[[2]string{b.ID, k.ID}] {
				continue
			}
			if k.Amt != b.Amt {
				continue
			}
			if requireRef && (k.Ref == "" || k.Ref != b.Ref) {
				continue
			}
			d := adiff(k.Day, b.Day)
			if d > w {
				continue
			}
			if best == -1 || d < bestDiff || (d == bestDiff && k.ID < (*books)[best].ID) {
				best = j
				bestDiff = d
			}
		}
		if best >= 0 {
			k := (*books)[best]
			mm := Match{Mid: n.nextMid, Round: roundNum, BankID: []string{b.ID}, BookID: []string{k.ID}}
			n.nextMid++
			n.matched["b:"+b.ID] = true
			n.matched["k:"+k.ID] = true
			*books = removeLine(*books, k.ID)
			produced = append(produced, mm)
			return true
		}
		return false
	}

	round := func(roundNum int, requireRef bool) {
		banks := sortedLines(n.bank, n.matched, "b:")
		books := sortedLines(n.book, n.matched, "k:")
		for i := 0; i < len(banks); i++ {
			b := banks[i]
			if n.matched["b:"+b.ID] {
				continue
			}
			if requireRef && b.Ref == "" {
				continue
			}
			kk := books
			if pick(b, &kk, roundNum, requireRef) {
				books = kk
			}
		}
	}

	// 第一轮
	round(1, true)
	// 第二轮
	before := len(produced)
	round(2, false)
	_ = before

	// 第三轮：一对多。
	banks := sortedLines(n.bank, n.matched, "b:")
	books := sortedLines(n.book, n.matched, "k:")
	before = len(produced)
	for _, b := range banks {
		if n.matched["b:"+b.ID] || b.Ref == "" {
			continue
		}
		var s []Line
		var sum int64
		for _, k := range books {
			if n.matched["k:"+k.ID] {
				continue
			}
			if k.Ref != b.Ref {
				continue
			}
			if adiff(k.Day, b.Day) > w || n.forbid[[2]string{b.ID, k.ID}] {
				continue
			}
			s = append(s, k)
			sum += k.Amt
		}
		if len(s) >= 2 && sum == b.Amt {
			kids := make([]string, 0, len(s))
			n.matched["b:"+b.ID] = true
			for _, k := range s {
				kids = append(kids, k.ID)
				n.matched["k:"+k.ID] = true
			}
			sort.Strings(kids)
			produced = append(produced, Match{Mid: n.nextMid, Round: 3, BankID: []string{b.ID}, BookID: kids})
			n.nextMid++
		}
	}

	// 第四轮：多对一。
	banks = sortedLines(n.bank, n.matched, "b:")
	books = sortedLines(n.book, n.matched, "k:")
	for _, k := range books {
		if n.matched["k:"+k.ID] || k.Ref == "" {
			continue
		}
		var tg []Line
		var sum int64
		for _, b := range banks {
			if n.matched["b:"+b.ID] {
				continue
			}
			if b.Ref != k.Ref {
				continue
			}
			if adiff(b.Day, k.Day) > w || n.forbid[[2]string{b.ID, k.ID}] {
				continue
			}
			tg = append(tg, b)
			sum += b.Amt
		}
		if len(tg) >= 2 && sum == k.Amt {
			bids := make([]string, 0, len(tg))
			n.matched["k:"+k.ID] = true
			for _, b := range tg {
				bids = append(bids, b.ID)
				n.matched["b:"+b.ID] = true
			}
			sort.Strings(bids)
			produced = append(produced, Match{Mid: n.nextMid, Round: 4, BankID: bids, BookID: []string{k.ID}})
			n.nextMid++
		}
	}

	for _, mm := range produced {
		n.matches[mm.Mid] = mm
	}
	return produced
}

func (n *naiveMatcher) reverse(mid int64) (bool, bool) {
	mm, ok := n.matches[mid]
	if !ok {
		return false, n.reversed[mid] // 第二个返回值表示“已撤销”
	}
	delete(n.matches, mid)
	n.reversed[mid] = true
	for _, b := range mm.BankID {
		delete(n.matched, "b:"+b)
		for _, k := range mm.BookID {
			n.forbid[[2]string{b, k}] = true
		}
	}
	for _, k := range mm.BookID {
		delete(n.matched, "k:"+k)
	}
	return true, false
}

func (n *naiveMatcher) matchList() []Match {
	var out []Match
	for _, mm := range n.matches {
		out = append(out, mm)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BankID[0] != out[j].BankID[0] {
			return out[i].BankID[0] < out[j].BankID[0]
		}
		return out[i].Mid < out[j].Mid
	})
	return out
}

func (n *naiveMatcher) forbiddenList() [][2]string {
	var out [][2]string
	for f := range n.forbid {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func (n *naiveMatcher) unmatched(side Side) []Line {
	if side == Bank {
		return sortedLines(n.bank, n.matched, "b:")
	}
	return sortedLines(n.book, n.matched, "k:")
}
