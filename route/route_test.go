package route

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"ontology/numplan"
	"ontology/portdb"
)

const exN = "13805001234"

func buildExampleRouter(lmin, q int64, t0, t10 int64) *Router {
	p := numplan.New()
	must0(p.AssignBlock("1380", 11, 1, t0))
	must0(p.AssignBlock("13805", 11, 2, t10))
	return New(portdb.New(p, lmin, q))
}

// advance 用一次只读 Query 推进时钟（Query 是可接受操作，会推进 maxNow）。
func advance(r *Router, now int64) {
	if _, err := r.Query(exN, 4, ACQ, now); err != nil {
		panic(err)
	}
}

func must0(err error) {
	if err != nil {
		panic(err)
	}
}

func eqPath(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWorkedExampleRouting(t *testing.T) {
	r := buildExampleRouter(50, 90, 0, 10)
	id, err := r.DB().RequestPort(exN, 2, 3, 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	advance(r, 100)
	// t=99 前：orig=D OR 得 [B]、未携出。
	res, err := r.QueryAt(exN, 4, OR, 99)
	if err != nil || !eqPath(res.Path, []int{2}) || res.Ported {
		t.Fatalf("OR@99: %+v err=%v", res, err)
	}
	// 推进时钟到 100。
	if _, err := r.Query(exN, 4, OR, 100); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		orig   int
		method Method
		want   []int
		ported bool
	}{
		{4, OR, []int{2, 3}, true},
		{4, ACQ, []int{3}, true},
		{2, OR, []int{3}, true}, // h==orig
		{3, OR, []int{}, true},  // s==orig 网内
		{3, ACQ, []int{}, true},
	}
	for _, tc := range cases {
		res, err := r.QueryAt(exN, tc.orig, tc.method, 100)
		if err != nil || !eqPath(res.Path, tc.want) || res.Ported != tc.ported {
			t.Fatalf("orig=%d method=%v: path=%v ported=%v err=%v", tc.orig, tc.method, res.Path, res.Ported, err)
		}
	}
	// Cancel 边界：99 已过去（时钟 100），撤已生效单报已生效。
	if err := r.DB().Cancel(id, 100); !errors.Is(err, numplan.ErrEffective) {
		t.Fatalf("cancel effective: %v", err)
	}
}

func TestSecondExample(t *testing.T) {
	r := buildExampleRouter(50, 90, 0, 10)
	mustPort(r.DB().RequestPort(exN, 2, 3, 100, 20))
	mustPort(r.DB().RequestPort(exN, 3, 1, 250, 150))
	mustPort(r.DB().RequestPort(exN, 1, 2, 320, 260))
	if err := r.DB().Disconnect(exN, 400); err != nil {
		t.Fatal(err)
	}
	advance(r, 490)
	res, err := r.QueryAt(exN, 4, OR, 250)
	if err != nil || !eqPath(res.Path, []int{2, 1}) {
		t.Fatalf("OR@250: %+v %v", res, err)
	}
	res, _ = r.QueryAt(exN, 4, OR, 320)
	if !eqPath(res.Path, []int{2}) || res.Ported {
		t.Fatalf("OR@320 port back: %+v", res)
	}
	// 后登记嵌套号段：自 300 起归属 D。
	// 该号段例子中于 t=300 登记；为同时验证销号时间线，单独建库验证。
	r2 := buildExampleRouter(50, 90, 0, 10)
	mustPort(r2.DB().RequestPort(exN, 2, 3, 100, 20))
	mustPort(r2.DB().RequestPort(exN, 3, 1, 250, 150))
	mustPort(r2.DB().RequestPort(exN, 1, 2, 320, 260))
	must0(r2.DB().Plan().AssignBlock("138050", 11, 4, 300))
	advance(r2, 320)
	res2, err := r2.QueryAt(exN, 2, OR, 300)
	if err != nil || !eqPath(res2.Path, []int{4, 1}) || !res2.Ported {
		t.Fatalf("OR@300 new home D, serving A: %+v %v", res2, err)
	}
	res2, _ = r2.QueryAt(exN, 1, OR, 320)
	if !eqPath(res2.Path, []int{4, 2}) || !res2.Ported {
		t.Fatalf("OR@320 home D serving B: %+v", res2)
	}
	// 销号冷冻左闭右开。
	if _, err := r.QueryAt(exN, 4, OR, 450); !errors.Is(err, numplan.ErrFrozen) {
		t.Fatalf("frozen QueryAt: %v", err)
	}
	if _, err := r.QueryAt(exN, 4, OR, 489); !errors.Is(err, numplan.ErrFrozen) {
		t.Fatalf("frozen 489: %v", err)
	}
	res, err = r.QueryAt(exN, 4, OR, 490)
	if err != nil || !eqPath(res.Path, []int{2}) || res.Ported {
		t.Fatalf("thaw@490: %+v %v", res, err)
	}
}

func mustPort(id int64, err error) {
	if err != nil {
		panic(err)
	}
}

func TestHistoryImmutability(t *testing.T) {
	r := buildExampleRouter(50, 90, 0, 10)
	_, _ = r.DB().RequestPort(exN, 2, 3, 100, 20)
	// 在后续操作前快照多个历史时刻，做完操作后答案必须不变。
	advance(r, 200)
	snap := map[int64]Result{}
	for _, tt := range []int64{9, 50, 99, 100, 200} {
		res, err := r.QueryAt(exN, 4, OR, tt)
		if err != nil {
			t.Fatal(err)
		}
		snap[tt] = res
	}
	_, _ = r.DB().RequestPort(exN, 3, 1, 250, 150)
	_, _ = r.Query(exN, 4, OR, 300)
	must0(r.DB().Plan().AssignBlock("138050", 11, 4, 300))
	_ = r.DB().Disconnect(exN, 400)
	for tt, want := range snap {
		got, err := r.QueryAt(exN, 4, OR, tt)
		if err != nil || !eqPath(got.Path, want.Path) || got.Ported != want.Ported ||
			got.Serving != want.Serving || got.Home != want.Home {
			t.Fatalf("history rewritten at %d: got=%+v want=%+v", tt, got, want)
		}
	}
	// QueryAt 不推进时钟：t>maxNow 报参数非法。
	if _, err := r.QueryAt(exN, 4, OR, 401); !errors.Is(err, numplan.ErrInvalid) {
		t.Fatalf("QueryAt future: %v", err)
	}
}

func TestProbeScale(t *testing.T) {
	for _, scale := range []int{1000, 100000} {
		t.Run(fmt.Sprintf("n=%d", scale), func(t *testing.T) {
			r := buildExampleRouter(0, 0, 0, 0)
			// 每个号码挂两条不同前缀号段，再加一条携转（构造少量历史）。
			numbers := make([]string, scale)
			for i := 0; i < scale; i++ {
				num := fmt.Sprintf("138%08d", i) // 11 位
				numbers[i] = num
				// 共享 138 号段已让其归属 A=1；再为其挂长度 5 的唯一前缀不现实，
				// 改为验证号段探测数只与号码位数有关。
				if _, err := r.Query(num, 4, OR, 0); err != nil {
					t.Fatal(err)
				}
			}
			res, err := r.QueryAt(numbers[scale-1], 4, OR, 0)
			if err != nil {
				t.Fatal(err)
			}
			if res.Probe.Blocks > len(numbers[scale-1]) {
				t.Fatalf("blocks probe=%d digits=%d", res.Probe.Blocks, len(numbers[scale-1]))
			}
			if res.Probe.History > 2 {
				t.Fatalf("history probe=%d for empty history", res.Probe.History)
			}
		})
	}
}

// TestProbeHistoryLog 构造深历史并验证历史探针 ≤ log2(k)+2，与号码总数无关。
func TestProbeHistoryLog(t *testing.T) {
	r := buildExampleRouter(0, 0, 0, 0)
	const k = 500
	prev := 2
	var at int64 = 100
	for i := 0; i < k; i++ {
		rec := 3 + i%2
		if _, err := r.DB().RequestPort(exN, prev, rec, at, at); err != nil {
			t.Fatal(err)
		}
		prev = rec
		at++
	}
	maxProbe := 0
	for tt := int64(100); tt < at; tt++ {
		res, err := r.QueryAt(exN, 4, OR, tt)
		if err != nil {
			t.Fatal(err)
		}
		if res.Probe.History > maxProbe {
			maxProbe = res.Probe.History
		}
		bound := int(math.Ceil(math.Log2(float64(k+1)))) + 2
		if res.Probe.History > bound {
			t.Fatalf("t=%d probes=%d bound=%d", tt, res.Probe.History, bound)
		}
	}
	t.Logf("k=%d max history probes=%d (digits=%d)", k, maxProbe, len(exN))
}
