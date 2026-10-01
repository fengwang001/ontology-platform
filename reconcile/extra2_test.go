package reconcile

import (
	"fmt"
	"sync"
	"testing"
)

// 同一批行以任意顺序并发登记后执行一次 Reconcile，结果逐项完全相同。
func TestConcurrentAddDeterministic(t *testing.T) {
	bankSpecs := []Line{
		{ID: "b3", Amt: 300, Day: 20, Ref: "Y"},
		{ID: "b1", Amt: 100, Day: 10, Ref: "X"},
		{ID: "b2", Amt: 100, Day: 12, Ref: "X"},
	}
	bookSpecs := []Line{
		{ID: "k3", Amt: 100, Day: 19, Ref: "Y"},
		{ID: "k1", Amt: 100, Day: 11, Ref: "X"},
		{ID: "k4", Amt: 200, Day: 21, Ref: "Y"},
		{ID: "k2", Amt: 100, Day: 13, Ref: "X"},
	}

	run := func(seed int) []Match {
		m := NewMatcher()
		var wg sync.WaitGroup
		for i, b := range bankSpecs {
			wg.Add(1)
			go func(j int, ln Line) {
				defer wg.Done()
				if (seed+j)%2 == 0 {
					_ = m.AddLine(Bank, ln.ID, ln.Amt, ln.Day, ln.Ref)
				} else {
					_ = m.AddLine(Bank, ln.ID, ln.Amt, ln.Day, ln.Ref)
				}
			}(i, b)
		}
		for i, k := range bookSpecs {
			wg.Add(1)
			go func(j int, ln Line) {
				defer wg.Done()
				_ = m.AddLine(Book, ln.ID, ln.Amt, ln.Day, ln.Ref)
				_ = j
			}(i, k)
		}
		wg.Wait()
		got, err := m.Reconcile(2)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	base := run(0)
	for s := 1; s < 16; s++ {
		got := run(s)
		logf(t, "并发乱序 seed=%d 输出 %+v; 判定: 与基准逐项一致", s, got)
		assertMatches(t, got, base)
	}
}

// 并发混合调用：AddLine / Reconcile / Reverse / 查询同时进行，
// 不变量始终成立（行至多属于一个有效匹配；匹配金额平衡；匹配内无禁配）。
func TestConcurrentMixed(t *testing.T) {
	m := NewMatcher()
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				id := fmt.Sprintf("b-%d-%d", g, i)
				_ = m.AddLine(Bank, id, int64((i%5)+1)*10, int64(i%7), pickRef(i))
				id2 := fmt.Sprintf("k-%d-%d", g, i)
				_ = m.AddLine(Book, id2, int64((i%5)+1)*10, int64(i%7), pickRef(i))
				i++
				if i > 200 {
					return
				}
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := m.Reconcile(int64(j % 4)); err != nil {
					t.Error(err)
					return
				}
				for _, mm := range m.Matches() {
					if j%3 == 0 {
						_, _, _ = m.Reverse(mm.Mid)
					}
				}
				_, _ = m.Unmatched(Bank)
				_ = m.Forbidden()
			}
		}()
	}
	wg.Wait()
	close(stop)

	assertInvariants(t, m)
	logf(t, "并发混合结束: 有效匹配=%d 禁配对=%d; 判定: 不变量全部成立",
		len(m.Matches()), len(m.Forbidden()))
}

func pickRef(i int) string {
	if i%2 == 0 {
		return fmt.Sprintf("R%d", i%4)
	}
	return ""
}

func assertInvariants(t *testing.T, m *Matcher) {
	t.Helper()
	seenBank := map[string]int64{}
	seenBook := map[string]int64{}
	fb := map[[2]string]bool{}
	for _, f := range m.Forbidden() {
		fb[f] = true
	}
	var bankSum, bookSum int64
	for _, mm := range m.Matches() {
		var bs, ks int64
		for _, b := range mm.BankID {
			if other, dup := seenBank[b]; dup {
				t.Fatalf("bank %s in mids %d and %d", b, other, mm.Mid)
			}
			seenBank[b] = mm.Mid
			bs += findLine(t, m, Bank, b).Amt
		}
		for _, k := range mm.BookID {
			if other, dup := seenBook[k]; dup {
				t.Fatalf("book %s in mids %d and %d", k, other, mm.Mid)
			}
			seenBook[k] = mm.Mid
			ks += findLine(t, m, Book, k).Amt
		}
		if bs != ks {
			t.Fatalf("mid %d sums %d != %d", mm.Mid, bs, ks)
		}
		bankSum += bs
		bookSum += ks
		for _, b := range mm.BankID {
			for _, k := range mm.BookID {
				if fb[[2]string{b, k}] {
					t.Fatalf("mid %d contains forbidden (%s,%s)", mm.Mid, b, k)
				}
			}
		}
	}
	if bankSum != bookSum {
		t.Fatalf("totals %d != %d", bankSum, bookSum)
	}
}

func findLine(t *testing.T, m *Matcher, side Side, id string) Line {
	t.Helper()
	for _, ln := range unmatchedErr(t, m, side) {
		if ln.ID == id {
			return ln
		}
	}
	// 已匹配行不在 Unmatched 中：通过 Matches 校验金额时由调用处另行取值，
	// 这里直接在内部映射上只读访问。
	if side == Bank {
		if r, ok := m.bank[id]; ok {
			return r.line
		}
	} else {
		if r, ok := m.book[id]; ok {
			return r.line
		}
	}
	t.Fatalf("line %s not found", id)
	return Line{}
}
