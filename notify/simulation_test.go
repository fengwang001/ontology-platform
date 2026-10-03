package notify

// 随机操作序列在 Notifier 与朴素模拟上同步重放，
// 逐步比对错误类别、Tick 清单与通知内容，并校验不变量。

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/alertstore"
	"ontology/suppress"
)

func matchAll(mt suppress.Matchers, l alertstore.Labels) bool {
	for k, v := range mt {
		if l[k] != v {
			return false
		}
	}
	return true
}

func (m *naive) visible(fp string, now int64) bool {
	a := m.alerts[fp]
	if !a.firing {
		return false
	}
	for _, s := range m.sils {
		if s.start <= now && now < s.end && matchAll(s.m, a.labels) {
			return false
		}
	}
	for _, r := range m.rules {
		if !matchAll(r.Target, a.labels) {
			continue
		}
		for ofp, o := range m.alerts {
			if ofp == fp || !o.firing || !matchAll(r.Source, o.labels) {
				continue
			}
			eq := true
			for _, name := range r.Equal {
				if o.labels[name] != a.labels[name] {
					eq = false
					break
				}
			}
			if eq {
				return false
			}
		}
	}
	return true
}

func randLabels(rng *rand.Rand) alertstore.Labels {
	keys := []string{"svc", "sev", "name", "inst"}
	vals := map[string][]string{
		"svc": {"db", "api", "cache"}, "sev": {"critical", "warning", "info"},
		"name": {"n0", "n1", "n2", "n3", "n4", "n5"}, "inst": {"i0", "i1"},
	}
	rng.Shuffle(len(keys), func(i, j int) { keys[i], keys[j] = keys[j], keys[i] })
	l := alertstore.Labels{}
	for _, k := range keys[:1+rng.Intn(3)] {
		vs := vals[k]
		l[k] = vs[rng.Intn(len(vs))]
	}
	return l
}

func TestNaiveSimulation(t *testing.T) {
	for seed := int64(0); seed < 6; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) { runSim(t, seed) })
	}
}

func runSim(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	g := [][]string{nil, {"svc"}, {"svc", "sev"}, {"inst"}}[rng.Intn(4)]
	w := []int64{0, 5, 10, 50}[rng.Intn(4)]
	r := []int64{10, 50, 100, 1000}[rng.Intn(4)]
	amax := 3 + rng.Intn(6)
	rules := []suppress.Rule{
		{Source: suppress.Matchers{"sev": "critical"}, Target: suppress.Matchers{"sev": "warning"}, Equal: []string{"svc"}},
		{Source: suppress.Matchers{"svc": "db"}, Target: suppress.Matchers{"sev": "info"}},
	}
	n := mustNotifier(t, g, w, r, amax, rules)
	m := newNaive(g, w, r, amax, rules)
	t.Logf("config: G=%v W=%d R=%d Amax=%d rules=%d", g, w, r, amax, len(rules))
	silIDs := []string{"s0", "s1", "s2", "s3"}
	call := 0
	failPred := func(i int) bool { return i%4 == 2 } // 确定性失败注入
	var now int64
	for step := 0; step < 300; step++ {
		now += int64(rng.Intn(40))
		var errN, errM error
		switch op := rng.Intn(100); {
		case op < 40:
			l := randLabels(rng)
			t.Logf("step %d: Fire(%d, %v)", step, now, l)
			errN, errM = n.Fire(now, l), m.fire(now, l)
		case op < 58:
			l := randLabels(rng)
			t.Logf("step %d: Resolve(%d, %v)", step, now, l)
			errN, errM = n.Resolve(now, l), m.resolve(now, l)
		case op < 66:
			id := silIDs[rng.Intn(len(silIDs))]
			mt := suppress.Matchers{"svc": []string{"db", "api"}[rng.Intn(2)]}
			start := now + int64(rng.Intn(20))
			end := start + 1 + int64(rng.Intn(100))
			t.Logf("step %d: AddSilence(%d, %q, %v, [%d,%d))", step, now, id, mt, start, end)
			errN = n.AddSilence(now, id, mt, start, end)
			errM = m.addSilence(id, mt, start, end)
		case op < 70:
			id := silIDs[rng.Intn(len(silIDs))]
			t.Logf("step %d: ExpireSilence(%d, %q)", step, now, id)
			errN, errM = n.ExpireSilence(now, id), m.expireSilence(now, id)
		default:
			simTick(t, n, m, now, &call, failPred)
		}
		if errKind(errN) != errKind(errM) {
			t.Fatalf("step %d: err %v (%s) != naive %v (%s)", step, errN, errKind(errN), errM, errKind(errM))
		}
		if n.store.Len() > amax {
			t.Fatalf("step %d: active %d > Amax %d", step, n.store.Len(), amax)
		}
	}
}

func simTick(t *testing.T, n *Notifier, m *naive, now int64, call *int, failPred func(int) bool) {
	base := *call
	local := 0
	var got []Notification
	res, err := n.Tick(now, func(nt Notification) error {
		got = append(got, nt)
		if failPred(base + local) {
			local++
			return errSend
		}
		local++
		return nil
	})
	if err != nil {
		t.Fatalf("Tick(%d): %v", now, err)
	}
	local = 0
	want, sent, failed, reasons := m.tick(now, func(Notification) bool {
		f := failPred(base + local)
		local++
		return f
	})
	t.Logf("Tick(%d) -> Sent=%v Failed=%v notes=%v", now, res.Sent, res.Failed, want)
	for _, why := range reasons {
		t.Logf("  判定依据: %s", why)
	}
	if local != len(got) {
		t.Fatalf("send calls: notifier %d != naive %d", len(got), local)
	}
	*call = base + local
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notes = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(res.Sent, sent) || !reflect.DeepEqual(res.Failed, failed) {
		t.Fatalf("result = %+v, want Sent=%v Failed=%v", res, sent, failed)
	}
	for _, nt := range got { // 不变量：互不相交、升序、Firing 当时可见
		if !sort.StringsAreSorted(nt.Firing) || !sort.StringsAreSorted(nt.Resolved) {
			t.Fatalf("unsorted notification: %+v", nt)
		}
		set := map[string]bool{}
		for _, fp := range nt.Firing {
			set[fp] = true
			if !m.visible(fp, now) {
				t.Fatalf("fp %q in Firing but not visible at %d", fp, now)
			}
		}
		for _, fp := range nt.Resolved {
			if set[fp] {
				t.Fatalf("Firing and Resolved intersect at %q", fp)
			}
		}
	}
}
