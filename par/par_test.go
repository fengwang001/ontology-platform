package par_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/norm"
	"ontology/par"
)

// cutsEnum 枚举 [0,n] 中 k 个严格升序切点（含 0 与 n，覆盖空段）。
func cutsEnum(n, k int, fn func([]int)) {
	cur := make([]int, k)
	var rec func(i, lo int)
	rec = func(i, lo int) {
		if i == k {
			fn(append([]int(nil), cur...))
			return
		}
		for v := lo; v <= n; v++ {
			cur[i] = v
			rec(i+1, v+1)
		}
	}
	rec(0, 0)
}

func TestParAllCuts(t *testing.T) {
	inputs := []string{"a \t\r\nb  \r\rc \n", "\r \n\r\n  "}
	for _, in := range inputs {
		for _, pol := range []norm.Policy{norm.Preserve, norm.EnsureOne, norm.StripTrailing} {
			opt := norm.Options{Policy: pol}
			want, wm, err := norm.Normalize([]byte(in), opt)
			if err != nil {
				t.Fatal(err)
			}
			for k := 1; k <= 8; k++ {
				cutsEnum(len(in), k-1, func(cuts []int) {
					got, gm, err := par.Normalize([]byte(in), cuts, opt)
					if err != nil || string(got) != string(want) || !slices.Equal(gm.Runs(), wm.Runs()) {
						t.Fatalf("in=%q pol=%d cuts=%v: got %q runs=%v want %q runs=%v",
							in, pol, cuts, got, gm.Runs(), want, wm.Runs())
					}
				})
			}
		}
	}
}

func TestParRepeat(t *testing.T) {
	in := []byte("x \t\r\ny  \rz\n\n" + strings.Repeat("q \r\n", 100))
	cuts := []int{1, 5, 6, 9, 40, 40} // 含重复切点（空段）
	want, wm, err := par.Normalize(in, cuts, norm.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for r := 0; r < 50; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, gm, err := par.Normalize(in, cuts, norm.Options{})
			if err != nil || string(got) != string(want) || !slices.Equal(gm.Runs(), wm.Runs()) {
				t.Error("par result not deterministic")
			}
			if _, _, err := norm.Normalize(in, norm.Options{}); err != nil { // 多实例并发
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestParError(t *testing.T) {
	_, _, err := par.Normalize([]byte("ab\x00cd"), []int{2}, norm.Options{Strict: true})
	var at *norm.ErrAt
	if !errors.Is(err, norm.ErrNUL) || !errors.As(err, &at) || at.Off != 2 {
		t.Fatalf("err=%v want ErrNUL@2", err)
	}
}

func TestProbe(t *testing.T) {
	line := "abc \t\r\n" // 行尾混杂 + 行尾空白
	for _, in := range []string{strings.Repeat(line, 100000), strings.Repeat(line, 10000000/len(line))} {
		_, m, err := norm.Normalize([]byte(in), norm.Options{})
		if err != nil {
			t.Fatal(err)
		}
		bound := 2*int(math.Log2(float64(m.Len()))) + 4
		for o := 0; o <= m.OutLen(); o += 997 {
			if m.ToOrig(o); m.LastProbe() > bound {
				t.Fatalf("ToOrig probe=%d bound=%d runs=%d", m.LastProbe(), bound, m.Len())
			}
		}
		for i := 0; i <= m.OrigLen(); i += 991 {
			if m.ToOut(i); m.LastProbe() > bound {
				t.Fatalf("ToOut probe=%d bound=%d runs=%d", m.LastProbe(), bound, m.Len())
			}
		}
		t.Logf("len=%d runs=%d bound=%d", len(in), m.Len(), bound)
	}
	_, m, _ := norm.Normalize([]byte(strings.Repeat("x\n", 5000000)), norm.Options{})
	if m.Len() > 4 {
		t.Fatalf("pure-LF 10MB runs=%d want <=4", m.Len())
	}
}
