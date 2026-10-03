package scan_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/rule"
	"ontology/scan"
	"ontology/store"
)

// ---- 独立朴素模型：逐步按规格文字重写，一次性无穷预算整库扫描 ----

type nv struct {
	ver    int64
	c      int64
	marker bool
	r      int64
}

type nr struct {
	id     string
	prefix string
	kind   int // 0 Expire 1 Noncurrent 2 Orphan
	days   int
	keep   int
}

type ni struct {
	key    string
	ver    int64
	action int // 0 AddMarker 1 Removed 2 Blocked
	reason string
}

type nOutcome struct {
	items      []ni
	failedKey  string // 空表示全部完成
	removeSeen int
}

func nDue(t int64, days int) int64 {
	x := t + int64(days)*86400
	return ((x + 86399) / 86400) * 86400
}

func nMatch(rules []nr, key string, kind int) (nr, bool) {
	var best nr
	found := false
	for _, r := range rules {
		if r.kind != kind || !strings.HasPrefix(key, r.prefix) {
			continue
		}
		if !found || len(r.prefix) > len(best.prefix) ||
			(len(r.prefix) == len(best.prefix) && r.id < best.id) {
			best, found = r, true
		}
	}
	return best, found
}

// naiveRun 对 state 做一次无穷 budget 的串行扫描；failN>0 时第 failN 次
// Remove 失败，该键整键撤销，游标停在该键。
func naiveRun(state map[string][]nv, rules []nr, now int64, failN int) nOutcome {
	out := nOutcome{}
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	maxVer := int64(0)
	for _, vs := range state {
		for _, v := range vs {
			if v.ver > maxVer {
				maxVer = v.ver
			}
		}
	}

	removeFail := func() bool {
		out.removeSeen++
		return failN > 0 && out.removeSeen == failN
	}

	for _, key := range keys {
		orig := append([]nv(nil), state[key]...)
		work := append([]nv(nil), orig...)
		var keyItems []ni
		var addedMarker bool
		var markerVer int64

		// 阶段一 Expire。
		if r, ok := nMatch(rules, key, 0); ok && len(work) > 0 {
			cur := work[len(work)-1]
			if !cur.marker {
				due := nDue(cur.c, r.days)
				if now >= due {
					maxVer++
					markerVer = maxVer
					work = append(work, nv{ver: markerVer, c: due, marker: true})
					addedMarker = true
					keyItems = append(keyItems, ni{key, markerVer, 0,
						fmt.Sprintf("expire current v%d c=%d due=%d <= now=%d", cur.ver, cur.c, due, now)})
				}
			}
		}

		// 阶段二 NoncurrentExpire。
		var removals []int64
		if r, ok := nMatch(rules, key, 1); ok && len(work) > 0 {
			type cand struct {
				v   nv
				t   int64
				due int64
			}
			var ncs []cand
			for i := 0; i+1 < len(work); i++ {
				if !work[i].marker {
					t0 := work[i+1].c
					ncs = append(ncs, cand{work[i], t0, nDue(t0, r.days)})
				}
			}
			prot := r.keep
			if prot > len(ncs) {
				prot = len(ncs)
			}
			firstExp := len(ncs) - prot
			var s2 []ni
			for idx, c := range ncs {
				if idx >= firstExp || now < c.due {
					continue
				}
				if c.v.r > now {
					s2 = append(s2, ni{key, c.v.ver, 2,
						fmt.Sprintf("v%d due=%d but locked r=%d > now=%d", c.v.ver, c.due, c.v.r, now)})
					continue
				}
				s2 = append(s2, ni{key, c.v.ver, 1,
					fmt.Sprintf("v%d noncurrent since %d due=%d <= now=%d", c.v.ver, c.t, c.due, now)})
				removals = append(removals, c.v.ver)
			}
			sort.Slice(s2, func(i, j int) bool { return s2[i].ver < s2[j].ver })
			keyItems = append(keyItems, s2...)
		}

		// 阶段三 OrphanMarker。
		surv := map[int64]bool{}
		for _, v := range work {
			surv[v.ver] = true
		}
		for _, x := range removals {
			delete(surv, x)
		}
		if _, ok := nMatch(rules, key, 2); ok && len(surv) == 1 {
			var only nv
			for _, v := range work {
				if surv[v.ver] {
					only = v
				}
			}
			if only.marker {
				keyItems = append(keyItems, ni{key, only.ver, 1, "sole survivor is delete marker -> orphan remove"})
				removals = append(removals, only.ver)
			}
		}

		// 应用删除（全局 Remove 计数；任一失败整键撤销）。
		sort.Slice(removals, func(i, j int) bool { return removals[i] < removals[j] })
		failed := false
		for _, rv := range removals {
			if removeFail() {
				failed = true
				break
			}
			for i, v := range work {
				if v.ver == rv {
					work = append(work[:i], work[i+1:]...)
					break
				}
			}
		}
		if failed {
			state[key] = orig
			out.failedKey = key
			return out
		}
		if addedMarker {
			_ = markerVer
		}
		if len(work) == 0 {
			delete(state, key)
		} else {
			state[key] = work
		}
		out.items = append(out.items, keyItems...)
	}
	return out
}

func TestRandomNaive1500(t *testing.T) {
	const groups = 1500
	rng := rand.New(rand.NewSource(20261003))

	prefixes := []string{"", "a", "ab", "img/", "log/"}
	for g := 0; g < groups; g++ {
		// 1) 随机规则（保证 id 互不相同）。
		var nrules []nr
		usedID := map[string]bool{}
		genRule := func(kind int, tag string) {
			id := fmt.Sprintf("%s%d", tag, len(nrules))
			for usedID[id] {
				id += "x"
			}
			usedID[id] = true
			nrules = append(nrules, nr{
				id:     id,
				prefix: prefixes[rng.Intn(len(prefixes))],
				kind:   kind,
				days:   1 + rng.Intn(400),
				keep:   rng.Intn(4),
			})
		}
		genRule(rng.Intn(3), "x")
		genRule(rng.Intn(3), "y")
		if rng.Intn(2) == 0 {
			genRule(rng.Intn(3), "z")
		}

		// 2) 随机键与版本。
		state := map[string][]nv{}
		nKeys := 1 + rng.Intn(5)
		for ki := 0; ki < nKeys; ki++ {
			key := []string{"a", "ab", "abc", "img/1", "log/x", "z"}[ki]
			ver := int64(ki * 10)
			c := int64(rng.Intn(200000))
			nVer := 1 + rng.Intn(5)
			var vs []nv
			for vi := 0; vi < nVer; vi++ {
				ver++
				c += int64(rng.Intn(200000))
				marker := rng.Intn(5) == 0
				var lock int64
				if !marker && rng.Intn(3) == 0 {
					lock = int64(rng.Intn(1_000_000))
				}
				vs = append(vs, nv{ver: ver, c: c, marker: marker, r: lock})
			}
			state[key] = vs
		}
		now := int64(rng.Intn(1_500_000))

		// 3) 朴素：无穷 budget，一次跑完。
		naiveState := cloneState(state)
		naive := naiveRun(naiveState, nrules, now, 0)

		// 4) 真实：随机 budget 序列（大量 budget=1）反复到空游标，同时做一次失败路径对照。
		realState := cloneState(state)
		st := buildStore(t, realState)
		eng := buildEngine(t, nrules)
		se := scan.New(st, eng)

		var realItems []flatItem
		var cursor []byte
		var examinedOK = true
		for steps := 0; ; steps++ {
			budget := int64(1 + rng.Intn(3))
			if rng.Intn(3) == 0 {
				budget = 1
			}
			out := se.Scan(now, budget, cursor)
			if out.Err != nil {
				t.Fatalf("group %d unexpected scan error: %v", g, out.Err)
			}
			if out.Examined() < 1 || out.Examined() > budget+5 {
				examinedOK = false
			}
			for _, it := range out.Items {
				realItems = append(realItems, flatItem{string(it.Key), it.Ver, int(it.Action)})
			}
			cursor = out.Cursor
			if len(cursor) == 0 {
				break
			}
			if steps > 10000 {
				t.Fatalf("group %d scan did not terminate", g)
			}
		}

		// 5) 对照清单与最终状态。
		if !itemsEqual(naive.items, realItems) {
			t.Fatalf("group %d ITEM MISMATCH\ninput:\n%s\nnaive=%s\nreal =%s\n",
				g, dumpInput(nrules, state, now), dumpItems(naive.items), dumpFlat(realItems))
		}
		if !examinedOK {
			t.Fatalf("group %d examined budget invariant violated", g)
		}
		if !statesEqual(naiveState, st) {
			t.Fatalf("group %d FINAL STATE MISMATCH\ninput:\n%s\nnaive=%s\n",
				g, dumpInput(nrules, state, now), dumpState(naiveState))
		}

		// 6) 失败路径：在第 failN 次 Remove 注入失败，朴素与真实各自重放后必须一致。
		if naive.removeSeen > 0 {
			failN := 1 + rng.Intn(naive.removeSeen)
			fState := cloneState(state)
			fOut := naiveRun(fState, nrules, now, failN)
			if fOut.failedKey == "" {
				t.Fatalf("group %d fail injection %d did not fire (seen=%d)", g, failN, naive.removeSeen)
			}

			rState := cloneState(state)
			rst := buildStore(t, rState)
			rst.FailNext(failN)
			rse := scan.New(rst, buildEngine(t, nrules))
			var rItems []flatItem
			var rCur []byte
			var failedAt string
			for steps := 0; ; steps++ {
				out := rse.Scan(now, int64(1+rng.Intn(3)), rCur)
				for _, it := range out.Items {
					rItems = append(rItems, flatItem{string(it.Key), it.Ver, int(it.Action)})
				}
				if out.Err != nil {
					if !errorsIs(out.Err, store.ErrRemoveFailed) {
						t.Fatalf("group %d unexpected err %v", g, out.Err)
					}
					failedAt = string(out.Cursor)
					rCur = out.Cursor
					out2 := rse.Scan(now, 1_000_000_000, rCur) // 注入已消耗，整库续跑
					if out2.Err != nil {
						t.Fatalf("group %d retry error: %v", g, out2.Err)
					}
					for _, it := range out2.Items {
						rItems = append(rItems, flatItem{string(it.Key), it.Ver, int(it.Action)})
					}
					if len(out2.Cursor) != 0 {
						t.Fatalf("group %d retry did not finish, cursor=%q", g, out2.Cursor)
					}
					break
				}
				rCur = out.Cursor
				if len(rCur) == 0 {
					break
				}
				if steps > 10000 {
					t.Fatalf("group %d failure scan did not terminate", g)
				}
			}

			// 朴素失败后继续无注入跑完（用于最终状态对照）。
			rest := naiveRun(fState, nrules, now, 0)
			wantItems := append(append([]ni{}, fOut.items...), rest.items...)
			if failedAt != fOut.failedKey {
				t.Fatalf("group %d failed key: real=%q naive=%q", g, failedAt, fOut.failedKey)
			}
			if !itemsEqual(wantItems, rItems) {
				t.Fatalf("group %d FAILURE-PATH ITEM MISMATCH\ninput:\n%s\nnaive=%s\nreal =%s\n",
					g, dumpInput(nrules, state, now), dumpItems(wantItems), dumpFlat(rItems))
			}
			if !statesEqual(fState, rst) {
				t.Fatalf("group %d FAILURE-PATH STATE MISMATCH\ninput:\n%s\nnaive=%s\n",
					g, dumpInput(nrules, state, now), dumpState(fState))
			}
		}

		// -v 时打印输入、输出与判定依据。
		if testing.Verbose() {
			t.Logf("group %d\n%s\noutput: %s", g, dumpInput(nrules, state, now), dumpItemsWithReasons(naive.items))
		}
	}
}

type flatItem struct {
	key    string
	ver    int64
	action int
}

func itemsEqual(a []ni, b []flatItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].key != b[i].key || a[i].ver != b[i].ver || a[i].action != b[i].action {
			return false
		}
	}
	return true
}

func cloneState(s map[string][]nv) map[string][]nv {
	out := make(map[string][]nv, len(s))
	for k, vs := range s {
		out[k] = append([]nv(nil), vs...)
	}
	return out
}

func buildStore(t *testing.T, s map[string][]nv) *store.Store {
	t.Helper()
	st := store.New()
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		var vs []store.Version
		for _, v := range s[k] {
			vs = append(vs, store.Version{Ver: v.ver, C: v.c, Marker: v.marker, R: v.r})
		}
		if err := st.Load([]byte(k), vs); err != nil {
			t.Fatalf("Load %s: %v", k, err)
		}
	}
	return st
}

func buildEngine(t *testing.T, rs []nr) *rule.Engine {
	t.Helper()
	var rr []rule.Rule
	for _, r := range rs {
		rr = append(rr, rule.Rule{
			ID: r.id, Prefix: []byte(r.prefix),
			Kind: rule.Kind(r.kind), Days: r.days, Keep: r.keep,
		})
	}
	eng, err := rule.New(rr)
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func errorsIs(err, target error) bool { return err == target }

func statesEqual(want map[string][]nv, st *store.Store) bool {
	gotKeys := st.KeysFrom(nil)
	if len(gotKeys) != len(want) {
		return false
	}
	for k, wvs := range want {
		gvs, ok := st.Snapshot([]byte(k))
		if !ok || len(gvs) != len(wvs) {
			return false
		}
		for i := range wvs {
			if gvs[i].Ver != wvs[i].ver || gvs[i].C != wvs[i].c ||
				gvs[i].Marker != wvs[i].marker || gvs[i].R != wvs[i].r {
				return false
			}
		}
	}
	return true
}

func actionName(a int) string {
	return [...]string{"AddMarker", "Removed", "Blocked"}[a]
}

func kindName(k int) string {
	return [...]string{"Expire", "Noncurrent", "Orphan"}[k]
}

func dumpInput(rules []nr, state map[string][]nv, now int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  now=%d\n  rules:", now)
	for _, r := range rules {
		fmt.Fprintf(&b, "\n    %s kind=%s prefix=%q days=%d keep=%d", r.id, kindName(r.kind), r.prefix, r.days, r.keep)
	}
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  key %q versions:", k)
		for _, v := range state[k] {
			tag := "data"
			if v.marker {
				tag = "marker"
			}
			fmt.Fprintf(&b, "\n    v%d c=%d %s r=%d", v.ver, v.c, tag, v.r)
		}
	}
	return b.String()
}

func dumpItems(items []ni) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "\n    key=%q v%d %s", it.key, it.ver, actionName(it.action))
	}
	return b.String()
}

func dumpFlat(items []flatItem) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "\n    key=%q v%d %s", it.key, it.ver, actionName(it.action))
	}
	return b.String()
}

func dumpItemsWithReasons(items []ni) string {
	var b strings.Builder
	for _, it := range items {
		fmt.Fprintf(&b, "\n    key=%q v%d %s | %s", it.key, it.ver, actionName(it.action), it.reason)
	}
	return b.String()
}

func dumpState(s map[string][]nv) string {
	var b strings.Builder
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n  key %q:", k)
		for _, v := range s[k] {
			tag := "data"
			if v.marker {
				tag = "marker"
			}
			fmt.Fprintf(&b, " v%d(c=%d,%s,r=%d)", v.ver, v.c, tag, v.r)
		}
	}
	return b.String()
}
