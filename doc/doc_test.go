package doc

import (
	"errors"
	"sync"
	"testing"
)

// TestEightSteps 钉住第三节八行表（不变量 1/2/3）。
func TestEightSteps(t *testing.T) {
	got, err := eightSteps(New())
	if err != nil || len(got) != len(eightWant) {
		t.Fatalf("err=%v got=%v", err, got)
	}
	for i := range got {
		if got[i] != eightWant[i] {
			t.Errorf("step %d got %q want %q", i+1, got[i], eightWant[i])
		}
	}
}

// TestNaiveReference 钉不变量 1：Text == 朴素批量重算。
func TestNaiveReference(t *testing.T) {
	d0 := New()
	_, _ = eightSteps(d0)
	d1 := New()
	_ = d1.Insert(Empty, id(1, "A"), 'a')
	_ = d1.Delete(id(1, "A"))
	for i, d := range []*Doc{d0, d1, New()} {
		if g, w := d.Text(), naiveText(d.log); g != w {
			t.Errorf("case %d %q != %q", i, g, w)
		}
	}
}

// TestConvergencePermutations 钉不变量 2：并发子元素六种到达顺序结果一致。
func TestConvergencePermutations(t *testing.T) {
	a, b, c := id(3, "R3"), id(1, "R1"), id(2, "R2")
	for _, ord := range [][]ID{{a, b, c}, {a, c, b}, {b, a, c}, {b, c, a}, {c, a, b}, {c, b, a}} {
		d := New()
		must(t, d.Insert(Empty, id(1, "A"), 'a'))
		for _, x := range ord {
			must(t, d.Insert(id(1, "A"), x, rune('0'+x.Lamport)))
		}
		if d.Text() != "a321" {
			t.Fatalf("%v -> %q", ord, d.Text())
		}
	}
}

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// TestTombstone 钉不变量 3：墓碑不摘除、可定位、子孙保留。
func TestTombstone(t *testing.T) {
	d := New()
	add := func(p, x ID, c rune) { must(t, d.Insert(p, x, c)) }
	add(Empty, id(1, "A"), 'a')
	add(id(1, "A"), id(2, "A"), 'b')
	add(id(2, "A"), id(3, "A"), 'c')
	if e := d.Delete(id(1, "A")); e != nil || d.Text() != "bc" {
		t.Fatalf("delete %v %q", e, d.Text())
	}
	if e := d.Insert(id(1, "A"), id(5, "B"), 'z'); e != nil || d.Text() != "zbc" {
		t.Fatalf("insert after tomb %v %q", e, d.Text())
	}
	if e := d.Delete(id(9, "X")); !errors.Is(e, ErrIDNotFound) {
		t.Errorf("delete missing -> %v", e)
	}
	if e := d.Delete(id(2, "A")); e != nil {
		t.Fatal(e)
	}
	if e := d.Delete(id(2, "A")); !errors.Is(e, ErrAlreadyDeleted) {
		t.Errorf("double delete -> %v", e)
	}
}

// TestErrorsDistinctAndAtomic 钉不变量 4：三类错误互不相同、失败不留痕。
func TestErrorsDistinctAndAtomic(t *testing.T) {
	if ErrDuplicateID == ErrPrevNotFound || ErrPrevNotFound == ErrInvalidID ||
		ErrDuplicateID == ErrInvalidID {
		t.Fatal("sentinels not distinct")
	}
	cases := []struct {
		do   func(*Doc) error
		want error
	}{
		{func(d *Doc) error { return d.Insert(Empty, id(1, "A"), 'q') }, ErrDuplicateID},
		{func(d *Doc) error { return d.Insert(id(9, "X"), id(2, "A"), 'q') }, ErrPrevNotFound},
		{func(d *Doc) error { return d.Insert(Empty, ID{Lamport: 1}, 'q') }, ErrInvalidID},
	}
	for _, c := range cases {
		d := New()
		must(t, d.Insert(Empty, id(1, "A"), 'a'))
		if e := c.do(d); !errors.Is(e, c.want) || d.Text() != "a" {
			t.Fatalf("got %v text %q", e, d.Text())
		}
		if e := d.Insert(id(1, "A"), id(2, "A"), 'b'); e != nil {
			t.Fatalf("unusable after rejection: %v", e)
		}
	}
}

// TestPrevProbeConstant 钉复杂度：定位 prev 的探针数不随 m 线性增长。
func TestPrevProbeConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		d := New()
		for i := 1; i <= m; i++ {
			must(t, d.Insert(Empty, id(uint64(i), "R"), 'x'))
		}
		must(t, d.Insert(id(uint64(m/2), "R"), id(uint64(m+1), "Q"), 'z'))
		if d.lastProbe > 1 {
			t.Fatalf("m=%d probe %d grows", m, d.lastProbe)
		}
	}
}

// TestConcurrentReaders 钉并发：多 goroutine 并发 Text/SelfCheck，结果逐字符
// 相同；启动栅栏同步，无 sleep。
func TestConcurrentReaders(t *testing.T) {
	d := New()
	if _, e := eightSteps(d); e != nil {
		t.Fatal(e)
	}
	const N = 64
	var wg sync.WaitGroup
	start, res := make(chan struct{}), make([]string, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			res[g] = d.Text()
			if g%2 == 0 && d.SelfCheck() != nil {
				t.Errorf("selfcheck")
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < N; g++ {
		if res[g] != res[0] {
			t.Fatalf("reader %d %q != %q", g, res[g], res[0])
		}
	}
}
