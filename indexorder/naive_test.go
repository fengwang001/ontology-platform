package indexorder

import (
	"math/rand"
	"sort"
	"sync"
	"testing"
)

var allDirs = []Direction{ASC, DESC}
var allNulls = []NullsOrder{NullsFirst, NullsLast}

// naiveSatisfies 是完全按题目规则逐列比对写的独立朴素实现。
func naiveSatisfies(idx Index, eq map[string]struct{}, need []ColumnItem, scan ScanDirection) bool {
	j := 0
	for _, c := range idx.Column {
		if _, skip := eq[c.Column]; skip {
			continue
		}
		if j >= len(need) {
			return true
		}
		n := need[j]
		if c.Column != n.Column {
			return false
		}
		wantDir, wantNulls := n.Dir, n.Nulls
		if scan == ScanBackward {
			wantDir = invertDirection(wantDir)
			wantNulls = invertNulls(wantNulls)
		}
		if c.Dir != wantDir || c.Nulls != wantNulls {
			return false
		}
		j++
	}
	return j == len(need)
}

func naiveNormalize(eq map[string]struct{}, order []ColumnItem) []ColumnItem {
	var need []ColumnItem
	seen := map[string]bool{}
	for _, o := range order {
		if _, drop := eq[o.Column]; drop {
			continue
		}
		if seen[o.Column] {
			continue
		}
		seen[o.Column] = true
		need = append(need, o)
	}
	return need
}

// naiveChoose 以排序后的索引列表复现选择次序，不依赖 map 迭代顺序。
func naiveChoose(indexes []Index, eq map[string]struct{}, order []ColumnItem) (Choice, bool) {
	need := naiveNormalize(eq, order)
	sorted := make([]Index, len(indexes))
	copy(sorted, indexes)
	sort.Slice(sorted, func(i, j int) bool {
		if len(sorted[i].Column) != len(sorted[j].Column) {
			return len(sorted[i].Column) < len(sorted[j].Column)
		}
		return sorted[i].Name < sorted[j].Name
	})
	if len(need) == 0 {
		if len(sorted) == 0 {
			return Choice{}, false
		}
		return Choice{IndexName: sorted[0].Name, Scan: ScanForward}, true
	}
	for _, idx := range sorted {
		if naiveSatisfies(idx, eq, need, ScanForward) {
			return Choice{IndexName: idx.Name, Scan: ScanForward}, true
		}
	}
	for _, idx := range sorted {
		if naiveSatisfies(idx, eq, need, ScanBackward) {
			return Choice{IndexName: idx.Name, Scan: ScanBackward}, true
		}
	}
	return Choice{}, false
}

func randomColumnItem(r *rand.Rand, cols []string) ColumnItem {
	return ColumnItem{
		Column: cols[r.Intn(len(cols))],
		Dir:    allDirs[r.Intn(2)],
		Nulls:  allNulls[r.Intn(2)],
	}
}

func randomDistinctColumns(r *rand.Rand, cols []string) []ColumnItem {
	perm := r.Perm(len(cols))
	n := 1 + r.Intn(len(cols))
	items := make([]ColumnItem, 0, n)
	for _, p := range perm[:n] {
		items = append(items, ColumnItem{
			Column: cols[p],
			Dir:    allDirs[r.Intn(2)],
			Nulls:  allNulls[r.Intn(2)],
		})
	}
	return items
}

func TestDifferentialRandom2000(t *testing.T) {
	r := rand.New(rand.NewSource(20261001))
	cols := []string{"a", "b", "c", "d"}

	for iter := 0; iter < 2000; iter++ {
		nIdx := r.Intn(5)
		indexes := make([]Index, 0, nIdx)
		usedNames := map[string]bool{}
		for len(indexes) < nIdx {
			name := string(rune('i'+len(indexes))) + "_" + randName(r)
			if usedNames[name] {
				continue
			}
			usedNames[name] = true
			indexes = append(indexes, Index{Name: name, Column: randomDistinctColumns(r, cols)})
		}

		// 随机 eq 集合。
		eq := map[string]struct{}{}
		for _, c := range cols {
			if r.Intn(3) == 0 {
				eq[c] = struct{}{}
			}
		}
		// 随机 order（允许重复列，可能包含 eq 列）。
		order := make([]ColumnItem, 1+r.Intn(4))
		for k := range order {
			order[k] = randomColumnItem(r, cols)
		}

		cat := NewCatalog()
		// 打乱登记顺序以验证结果与登记顺序无关。
		shuffled := append([]Index(nil), indexes...)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, idx := range shuffled {
			if err := cat.Register(idx); err != nil {
				t.Fatalf("iter %d register: %v", iter, err)
			}
		}

		got, gotErr := cat.Choose(eq, order)
		want, wantOK := naiveChoose(indexes, eq, order)
		if wantOK {
			if gotErr != nil || got != want {
				t.Fatalf("iter %d mismatch: indexes=%v eq=%v order=%v got=(%+v,%v) want=%+v",
					iter, indexes, eq, order, got, gotErr, want)
			}
		} else if gotErr != ErrNoMatchingIndex {
			t.Fatalf("iter %d: want no match, got %+v, %v", iter, got, gotErr)
		}

		if iter < 20 || !wantOK {
			need := naiveNormalize(eq, order)
			basis := "no index matches"
			if wantOK {
				basis = "need=" + fmtItems(need) + " => " + string(want.IndexName) + " " + string(want.Scan)
			}
			testLogger.Printf("DIFF[%04d] input indexes=%v eq=%v order=%v | output=%+v err=%v | 依据: %s",
				iter, indexes, eq, order, got, gotErr, basis)
		}
	}
}

func randName(r *rand.Rand) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, 1+r.Intn(3))
	for i := range b {
		b[i] = letters[r.Intn(len(letters))]
	}
	return string(b)
}

func fmtItems(items []ColumnItem) string {
	s := "["
	for i, it := range items {
		if i > 0 {
			s += ", "
		}
		s += it.Column + " " + string(it.Dir) + " " + string(it.Nulls)
	}
	return s + "]"
}

func TestConcurrentAccess(t *testing.T) {
	cat := NewCatalog()
	for i := 0; i < 10; i++ {
		_ = cat.Register(Index{
			Name: "idx" + string(rune('a'+i)),
			Column: []ColumnItem{
				{Column: "a", Dir: ASC, Nulls: NullsLast},
				{Column: "b", Dir: DESC, Nulls: NullsFirst},
			},
		})
	}

	var wg sync.WaitGroup
	order := []ColumnItem{
		{Column: "a", Dir: ASC, Nulls: NullsLast},
		{Column: "b", Dir: DESC, Nulls: NullsFirst},
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				switch k % 4 {
				case 0:
					_, _ = cat.Choose(nil, order)
				case 1:
					_ = cat.Drop("idxa")
				case 2:
					_ = cat.Register(Index{
						Name:   "idxa",
						Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}},
					})
				case 3:
					_ = cat.Drop("missing")
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestReplayDeterministic(t *testing.T) {
	run := func() []string {
		cat := NewCatalog()
		var log []string
		steps := []func() string{
			func() string {
				err := cat.Register(Index{Name: "b", Column: []ColumnItem{{Column: "a", Dir: DESC, Nulls: NullsFirst}}})
				return errStr(err)
			},
			func() string {
				err := cat.Register(Index{Name: "a", Column: []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}}})
				return errStr(err)
			},
			func() string {
				ch, e := cat.Choose(nil, []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}})
				if e != nil {
					return e.Error()
				}
				return ch.IndexName + " " + string(ch.Scan)
			},
			func() string { return errStr(cat.Drop("a")) },
			func() string {
				_, e := cat.Choose(nil, []ColumnItem{{Column: "a", Dir: ASC, Nulls: NullsLast}})
				return errStr(e)
			},
		}
		for _, s := range steps {
			log = append(log, s())
		}
		return log
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay diverged at %d: %q vs %q", i, first[i], second[i])
		}
	}
	testLogger.Printf("REPLAY result=%v (两次重放一致)", first)
}

func errStr(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
