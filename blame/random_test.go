package blame

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naive 是"每次从第 0 期全量重扫"的朴素模拟，作为对照实现。
type naive struct {
	T       int64
	off     map[string]int64
	dur     map[string]int64
	parents map[string][]string
	order   []string
	land    map[string]map[int64]int64
	next    map[string]int64
	clock   int64
	started bool
	frozen  bool
	alerted map[nperiod]bool
	blamed  map[nperiod]Decision
}

type nperiod struct {
	d string
	k int64
}

func newNaive(T int64) *naive {
	return &naive{
		T:       T,
		off:     make(map[string]int64),
		dur:     make(map[string]int64),
		parents: make(map[string][]string),
		land:    make(map[string]map[int64]int64),
		next:    make(map[string]int64),
		alerted: make(map[nperiod]bool),
		blamed:  make(map[nperiod]Decision),
	}
}

func (n *naive) AddDataset(name string, off, dur int64, parents []string) error {
	if name == "" || off < 1 || off > n.T || dur < 0 || dur > n.T || len(parents) > 8 {
		return ErrInvalid
	}
	if n.frozen {
		return ErrFrozen
	}
	if _, ok := n.off[name]; ok {
		return ErrExists
	}
	seen := make(map[string]bool, len(parents))
	dedup := make([]string, 0, len(parents))
	for _, p := range parents {
		if seen[p] {
			continue
		}
		seen[p] = true
		if _, ok := n.off[p]; !ok {
			return ErrNoSuchParent
		}
		dedup = append(dedup, p)
	}
	if len(n.order) >= 10000 {
		return ErrTooMany
	}
	n.off[name] = off
	n.dur[name] = dur
	n.parents[name] = dedup
	n.order = append(n.order, name)
	return nil
}

func (n *naive) Land(d string, k, now int64) error {
	if k < 0 || now < 0 || now > MaxNow {
		return ErrInvalid
	}
	if n.started && now < n.clock {
		return ErrClock
	}
	if _, ok := n.off[d]; !ok {
		return ErrNoSuchDataset
	}
	nx := n.next[d]
	if k < nx {
		return ErrAlready
	}
	if k > nx {
		return ErrOutOfOrder
	}
	if now < k*n.T {
		return ErrTooEarly
	}
	missing := ""
	for _, p := range n.parents[d] {
		if n.next[p] <= k && (missing == "" || p < missing) {
			missing = p
		}
	}
	if missing != "" {
		return ErrUpstreamMissing
	}
	if n.land[d] == nil {
		n.land[d] = make(map[int64]int64)
	}
	n.land[d][k] = now
	n.next[d]++
	n.clock, n.started, n.frozen = now, true, true
	return nil
}

func (n *naive) violation(d string, k, now int64) bool {
	dl := k*n.T + n.off[d]
	if lt, ok := n.land[d][k]; ok {
		return lt > dl
	}
	return now > dl
}

func (n *naive) attribute(x string, k, now int64) (string, Kind) {
	ps := n.parents[x]
	if len(ps) == 0 {
		return x, Self
	}
	hasUn := false
	minUn := ""
	latest := int64(0)
	latestName := ""
	for _, p := range ps {
		lt, ok := n.land[p][k]
		if !ok {
			hasUn = true
			if minUn == "" || p < minUn {
				minUn = p
			}
			continue
		}
		if latestName == "" || lt > latest || (lt == latest && p < latestName) {
			latest, latestName = lt, p
		}
	}
	if !hasUn && latest+n.dur[x] <= k*n.T+n.off[x] {
		return x, Self
	}
	crit := latestName
	if hasUn {
		crit = minUn
	}
	if !n.violation(crit, k, now) {
		return x, Unreachable
	}
	return n.attribute(crit, k, now)
}

func (n *naive) Evaluate(now int64) ([]Alert, error) {
	if now < 0 || now > MaxNow {
		return nil, ErrInvalid
	}
	if n.started && now < n.clock {
		return nil, ErrClock
	}
	n.clock, n.started, n.frozen = now, true, true
	type key struct {
		root string
		kind Kind
		k    int64
	}
	groups := make(map[key][]string)
	for _, d := range n.order {
		for k := int64(0); ; k++ { // 朴素：每期从 0 全量重扫
			dl := k*n.T + n.off[d]
			lt, landed := n.land[d][k]
			var viol bool
			if landed {
				viol = lt > dl
			} else {
				if now <= dl {
					break
				}
				viol = true
			}
			if !viol || n.alerted[nperiod{d, k}] {
				continue
			}
			root, kind := n.attribute(d, k, now)
			gk := key{root, kind, k}
			groups[gk] = append(groups[gk], d)
			n.alerted[nperiod{d, k}] = true
			n.blamed[nperiod{d, k}] = Decision{Root: root, Kind: kind}
		}
	}
	alerts := make([]Alert, 0, len(groups))
	for gk, affected := range groups {
		sorted := append([]string(nil), affected...)
		sortStrings(sorted)
		alerts = append(alerts, Alert{Root: gk.root, Kind: gk.kind, K: gk.k, Affected: sorted})
	}
	sortAlerts(alerts)
	return alerts, nil
}

func (n *naive) Blame(d string, k int64) (Decision, error) {
	dec, ok := n.blamed[nperiod{d, k}]
	if !ok {
		return Decision{}, ErrNotFound
	}
	return dec, nil
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func sortAlerts(a []Alert) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0; j-- {
			x, y := a[j-1], a[j]
			less := x.K > y.K ||
				(x.K == y.K && x.Root > y.Root) ||
				(x.K == y.K && x.Root == y.Root && x.Kind > y.Kind)
			if !less {
				break
			}
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func checkSameErr(t *testing.T, trace *[]string, what string, real, want error) {
	*trace = append(*trace, fmt.Sprintf("%s -> real=%v naive=%v", what, real, want))
	if (real == nil) != (want == nil) {
		t.Fatalf("%s: real err=%v, naive err=%v\n%s", what, real, want, joinTrace(*trace))
	}
	if real != nil && !errors.Is(real, want) {
		t.Fatalf("%s: real err=%v, naive err=%v\n%s", what, real, want, joinTrace(*trace))
	}
}

func joinTrace(trace []string) string {
	out := ""
	for _, l := range trace {
		out += "  " + l + "\n"
	}
	return out
}

// TestRandomVsNaive 用 1500 组随机图与操作序列，把增量实现与
// "每次从第 0 期全量重扫"的朴素模拟逐操作对照（错误、告警、Blame）。
func TestRandomVsNaive(t *testing.T) {
	const cases = 1500
	for seed := int64(0); seed < cases; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomCase(t, rand.New(rand.NewSource(seed)), seed)
		})
	}
}

func runRandomCase(t *testing.T, rng *rand.Rand, seed int64) {
	T := int64(5 + rng.Intn(26)) // [5,30]
	mon, err := New(T)
	if err != nil {
		t.Fatalf("New(%d): %v", T, err)
	}
	nv := newNaive(T)
	var trace []string
	names := []string{}
	nDS := 1 + rng.Intn(6)
	for i := 0; i < nDS; i++ {
		name := fmt.Sprintf("d%d", i)
		off := 1 + int64(rng.Intn(int(T)))
		dur := int64(rng.Intn(int(T) + 1))
		var ps []string
		if len(names) > 0 {
			np := rng.Intn(min(3, len(names)) + 1)
			perm := rng.Perm(len(names))
			for j := 0; j < np; j++ {
				ps = append(ps, names[perm[j]])
			}
		}
		e1 := mon.AddDataset(name, off, dur, ps)
		e2 := nv.AddDataset(name, off, dur, ps)
		checkSameErr(t, &trace, fmt.Sprintf("AddDataset(%s,%d,%d,%v)", name, off, dur, ps), e1, e2)
		if e1 == nil {
			names = append(names, name)
		}
	}
	trace = append(trace, fmt.Sprintf("seed=%d T=%d datasets=%v", seed, T, names))
	alertedOnce := make(map[nperiod]bool)
	ops := 20 + rng.Intn(40)
	for op := 0; op < ops; op++ {
		switch rng.Intn(10) {
		case 0: // 随机 AddDataset（冻结后应报 ErrFrozen）
			name := fmt.Sprintf("x%d", rng.Intn(3))
			off := int64(rng.Intn(int(T) + 2))
			dur := int64(rng.Intn(int(T)+2)) - 1
			var ps []string
			if len(names) > 0 && rng.Intn(2) == 0 {
				ps = []string{names[rng.Intn(len(names))]}
			}
			if rng.Intn(10) == 0 {
				ps = []string{"ghost"}
			}
			e1 := mon.AddDataset(name, off, dur, ps)
			e2 := nv.AddDataset(name, off, dur, ps)
			checkSameErr(t, &trace, fmt.Sprintf("op%d AddDataset(%s,%d,%d,%v)", op, name, off, dur, ps), e1, e2)
			if e1 == nil {
				names = append(names, name)
			}
		case 1, 2, 3, 4, 5: // Land
			d := names[rng.Intn(len(names))]
			if rng.Intn(20) == 0 {
				d = "ghost"
			}
			k := nv.next[d] + int64(rng.Intn(3)) - 1
			if k < 0 {
				k = 0
			}
			if rng.Intn(20) == 0 {
				k = -1
			}
			var now int64
			switch rng.Intn(4) {
			case 0:
				now = k*T + int64(rng.Intn(int(T)+2)) - 1 // 周期起点附近（可能过早）
			case 1:
				now = nv.clock + int64(rng.Intn(3)) - 1 // 时钟附近（可能回退）
			case 2:
				now = max(nv.clock, k*T) + int64(rng.Intn(2)) // 大概率为合法落地
			default:
				now = int64(rng.Intn(int(3*T) + 2))
			}
			if rng.Intn(30) == 0 {
				now = -1
			}
			e1 := mon.Land(d, k, now)
			e2 := nv.Land(d, k, now)
			checkSameErr(t, &trace, fmt.Sprintf("op%d Land(%s,%d,%d)", op, d, k, now), e1, e2)
		default: // Evaluate
			var now int64
			switch rng.Intn(3) {
			case 0:
				now = nv.clock + int64(rng.Intn(int(2*T)+2))
			case 1:
				now = nv.clock + int64(rng.Intn(3)) - 1
			default:
				now = int64(rng.Intn(int(3*T) + 2))
			}
			if rng.Intn(30) == 0 {
				now = -1
			}
			before := mon.examined
			a1, e1 := mon.Evaluate(now)
			a2, e2 := nv.Evaluate(now)
			checkSameErr(t, &trace, fmt.Sprintf("op%d Evaluate(%d)", op, now), e1, e2)
			if e1 != nil {
				continue
			}
			trace = append(trace, fmt.Sprintf("op%d Evaluate(%d) alerts=%+v", op, now, a1))
			if !reflect.DeepEqual(a1, a2) {
				t.Fatalf("Evaluate(%d) alerts mismatch:\n real=%+v\nnaive=%+v\n%s",
					now, a1, a2, joinTrace(trace))
			}
			newV := 0
			for _, a := range a1 {
				for _, d := range a.Affected {
					key := nperiod{d, a.K}
					if alertedOnce[key] {
						t.Fatalf("duplicate alert for %v\n%s", key, joinTrace(trace))
					}
					alertedOnce[key] = true
					newV++
				}
			}
			if got := mon.examined - before; got > int64(newV+len(nv.order)) {
				t.Fatalf("examined=%d > %d new violations + %d datasets\n%s",
					got, newV, len(nv.order), joinTrace(trace))
			}
			for p, dec := range nv.blamed {
				got, err := mon.Blame(p.d, p.k)
				if err != nil || got != dec {
					t.Fatalf("Blame(%s,%d): real=%+v,%v naive=%+v\n%s",
						p.d, p.k, got, err, dec, joinTrace(trace))
				}
			}
		}
	}
	// 收尾：所有期的 Blame 结果与朴素模拟一致。
	for _, d := range names {
		for k := int64(0); k < nv.next[d]+2; k++ {
			got, e1 := mon.Blame(d, k)
			want, e2 := nv.Blame(d, k)
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("Blame(%s,%d): real err=%v naive err=%v\n%s", d, k, e1, e2, joinTrace(trace))
			}
			if e1 == nil && got != want {
				t.Fatalf("Blame(%s,%d): real=%+v naive=%+v\n%s", d, k, got, want, joinTrace(trace))
			}
		}
	}
	t.Logf("seed=%d T=%d datasets=%d ops=%d alerts=%d examined=%d (输入/输出/判定见失败时 trace)",
		seed, T, len(names), ops, len(alertedOnce), mon.examined)
}
