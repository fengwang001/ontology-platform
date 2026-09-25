package export

import (
	"fmt"
	"testing"

	"ontology/snap"
)

func buildSnap(n int) *snap.Snapshot {
	m := make(map[string]int64, n)
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("k%06d", i)] = int64(i)
	}
	return snap.Capture(m)
}

// TestProbeCountSublinear 钉住复杂度约束：从靠中间的位点 Next，
// 为定位检查的键个数不随 n 线性增长（白盒读非导出字段 probeCount）。
func TestProbeCountSublinear(t *testing.T) {
	const chunk = 16
	for _, n := range []int{100, 500, 1000, 5000, 10000} {
		e := New(buildSnap(n), chunk)
		mid := fmt.Sprintf("k%06d", n/2)
		entries, _, _, err := e.Next(mid)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if len(entries) != chunk {
			t.Fatalf("n=%d: got %d entries", n, len(entries))
		}
		if e.probeCount > maxLocateProbes+chunk {
			t.Fatalf("n=%d: probeCount=%d 随规模线性增长（线性扫描应≈%d）",
				n, e.probeCount, n/2)
		}
	}
}

// TestNextSemantics 表驱动：严格 >、满块 done、完成后 ErrFinished。
func TestNextSemantics(t *testing.T) {
	s := buildSnap(6) // k000000..k000005
	k := func(i int) string { return fmt.Sprintf("k%06d", i) }
	cases := []struct {
		name    string
		cursor  string
		wantLen int
		wantCur string
		done    bool
	}{
		{"从头", "", 2, k(1), false},
		{"续传严格大于", k(1), 2, k(3), false},
		{"末块满块也done", k(3), 2, k(5), true},
	}
	e := New(s, 2)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			en, cur, done, err := e.Next(c.cursor)
			if err != nil || len(en) != c.wantLen || cur != c.wantCur || done != c.done {
				t.Fatalf("got len=%d cur=%q done=%v err=%v", len(en), cur, done, err)
			}
			if len(en) > 0 && en[0].Key <= c.cursor {
				t.Fatalf("首键 %q 未严格大于位点 %q", en[0].Key, c.cursor)
			}
		})
	}
	if _, _, _, err := e.Next(k(5)); err != ErrFinished {
		t.Fatalf("done 后应 ErrFinished，got %v", err)
	}
	if _, _, _, err := e.Resume(k(5)); err != ErrFinished {
		t.Fatalf("Resume 同样应 ErrFinished，got %v", err)
	}
}

// TestResumeNoRepeat 续传不重复导出位点键，拼接等于全量。
func TestResumeNoRepeat(t *testing.T) {
	for _, n := range []int{1, 2, 3, 100} {
		for _, chunk := range []int{1, 2, 7} {
			e := New(buildSnap(n), chunk)
			var got []snap.Entry
			seen := map[string]bool{}
			cur := ""
			for {
				en, nc, done, err := e.Resume(cur)
				if err != nil {
					t.Fatalf("n=%d chunk=%d: %v", n, chunk, err)
				}
				for _, x := range en {
					if seen[x.Key] {
						t.Fatalf("n=%d chunk=%d: 键 %q 重复导出", n, chunk, x.Key)
					}
					seen[x.Key] = true
				}
				got = append(got, en...)
				cur = nc
				if done {
					break
				}
			}
			if len(got) != n {
				t.Fatalf("n=%d chunk=%d: 导出 %d 条", n, chunk, len(got))
			}
		}
	}
}
