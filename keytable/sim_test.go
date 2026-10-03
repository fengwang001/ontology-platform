package keytable

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/report"
)

// naiveState 是与生产代码完全独立的逐条朴素模型：
// 每租户用 map + []string 顺序表模拟 LRU（表头最旧），便于人工核对语义。
type naiveState struct {
	n, m, w  int64
	kt, tmax int
	maxNow   int64
	tenants  map[string]map[string]*naiveEntry
	order    map[string][]string // tenant -> 键，表头最旧、表尾最新
}

type naiveEntry struct{ win, cnt, dropped int64 }

type naiveRec struct {
	now       int64
	tenant    string
	key       string
	sev       int
	kept      bool
	summaries []report.Summary
	err       error
}

func newNaive(n, m, w int64, kt, tmax int) *naiveState {
	return &naiveState{
		n: n, m: m, w: w, kt: kt, tmax: tmax,
		tenants: make(map[string]map[string]*naiveEntry),
		order:   make(map[string][]string),
	}
}

func (st *naiveState) touch(tenant, key string) {
	ord := st.order[tenant]
	out := ord[:0]
	for _, k := range ord {
		if k != key {
			out = append(out, k)
		}
	}
	out = append(out, key)
	st.order[tenant] = out
}

func (st *naiveState) record(now int64, tenant, key string, sev int) naiveRec {
	r := naiveRec{now: now, tenant: tenant, key: key, sev: sev}
	if tenant == "" || len(tenant) > 64 || key == "" || len(key) > 64 ||
		sev < 0 || sev > 5 || now < 0 || now > 1_000_000_000_000 {
		r.err = report.ErrInvalidArgument
		return r
	}
	if now < st.maxNow {
		r.err = report.ErrClockSkew
		return r
	}
	t, ok := st.tenants[tenant]
	if !ok {
		if len(st.tenants) >= st.tmax {
			r.err = report.ErrTenantLimit
			return r
		}
		t = make(map[string]*naiveEntry)
		st.tenants[tenant] = t
	}
	st.maxNow = now
	cur := now / st.w
	e, hit := t[key]
	if hit {
		if e.win != cur {
			if e.dropped > 0 {
				r.summaries = append(r.summaries, report.Summary{
					Tenant: tenant, Key: key, Window: e.win,
					Dropped: e.dropped, Reason: report.Rolled,
				})
			}
			e.win, e.cnt, e.dropped = cur, 0, 0
		}
	} else {
		if len(t) >= st.kt {
			oldest := st.order[tenant][0]
			v := t[oldest]
			if v.dropped > 0 {
				r.summaries = append(r.summaries, report.Summary{
					Tenant: tenant, Key: oldest, Window: v.win,
					Dropped: v.dropped, Reason: report.Evicted,
				})
			}
			delete(t, oldest)
			ord := st.order[tenant][1:]
			st.order[tenant] = ord
		}
		e = &naiveEntry{win: cur}
		t[key] = e
	}
	st.touch(tenant, key)
	if sev >= 4 {
		r.kept = true
	} else {
		e.cnt++
		r.kept = e.cnt <= st.n || (e.cnt > st.n && (e.cnt-st.n)%st.m == 0)
		if !r.kept {
			e.dropped++
		}
	}
	return r
}

// TestAgainstNaiveRandom 随机序列逐条比对真实采样器与朴素模型的 kept/摘要/错误。
func TestAgainstNaiveRandom(t *testing.T) {
	cases := []struct {
		n, m, w       int64
		kt, tmax      int
		tenants, keys int
	}{
		{2, 3, 1000, 2, 2, 2, 4},  // 淘汰频繁
		{0, 5, 777, 3, 1, 1, 6},   // N=0
		{4, 1, 500, 50, 3, 3, 40}, // M=1
		{1, 1, 10, 4, 2, 2, 8},    // 高频窗口滚动
		{10, 7, 100000, 8, 4, 4, 12},
	}
	for ci, c := range cases {
		rng := rand.New(rand.NewSource(int64(ci + 1)))
		s, _ := New(c.n, c.m, c.w, int64(c.kt), int64(c.tmax))
		ns := newNaive(c.n, c.m, c.w, c.kt, c.tmax)
		var now int64
		for step := 0; step < 3000; step++ {
			// 小概率构造被拒输入：回退或坏参数；租户/键略超池以触达上限。
			tn := "t" + itoa(int64(rng.Intn(c.tenants+1)))
			k := "k" + itoa(int64(rng.Intn(c.keys)))
			sev := rng.Intn(6)
			if rng.Intn(10) == 0 {
				now += int64(rng.Intn(int(c.w * 2))) // 跨窗
			}
			if rng.Intn(40) == 0 {
				now -= int64(rng.Intn(5) + 1) // 偶发回退（随后会被双方拒绝）
			}
			if now < 0 {
				now = 0
			}
			got := s.Record(now, tn, k, sev)
			want := ns.record(now, tn, k, sev)
			if !reflect.DeepEqual(got.Kept, want.kept) ||
				!reflect.DeepEqual(got.Summaries, want.summaries) ||
				!sameErr(got.Err, want.err) {
				t.Fatalf("case#%d step=%d 输入 now=%d %s/%s sev=%d\n真实=%+v\n朴素=%+v",
					ci, step, now, tn, k, sev, got, want)
			}
			t.Logf("case#%d step=%d 输入 now=%d %s/%s sev=%d | 输出 kept=%v summaries=%d err=%v",
				ci, step, now, tn, k, sev, got.Kept, len(got.Summaries), got.Err)
			// 时钟回退双方都拒绝，后续恢复单调。
			if got.Err == report.ErrClockSkew {
				now = ns.maxNow
			}
		}
	}
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil)
}
