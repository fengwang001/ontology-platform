package dbscan

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type naivePoint struct{ id, x, y, born int64 }

type naiveState struct {
	eps    int64
	minPts int
	w      int64
	c      int
	now    int64
	pts    map[int64]*naivePoint
}

type naiveLabels struct {
	core  map[int64]bool
	label map[int64]int64
	nbrs  map[int64][]int64
}

// naiveCompute 对当前存活点整体重新聚类，结果与插入顺序无关。
func naiveCompute(st *naiveState) *naiveLabels {
	e2 := st.eps * st.eps
	ids := make([]int64, 0, len(st.pts))
	for id := range st.pts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	nbrs := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		nbrs[id] = []int64{id}
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := st.pts[ids[i]], st.pts[ids[j]]
			dx, dy := a.x-b.x, a.y-b.y
			if dx*dx+dy*dy <= e2 {
				nbrs[a.id] = append(nbrs[a.id], b.id)
				nbrs[b.id] = append(nbrs[b.id], a.id)
			}
		}
	}
	core := make(map[int64]bool)
	for _, id := range ids {
		core[id] = len(nbrs[id]) >= st.minPts
	}
	parent := make(map[int64]int64)
	for _, id := range ids {
		if core[id] {
			parent[id] = id
		}
	}
	var find func(int64) int64
	find = func(x int64) int64 {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int64) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	for i := 0; i < len(ids); i++ {
		if !core[ids[i]] {
			continue
		}
		for j := i + 1; j < len(ids); j++ {
			if !core[ids[j]] {
				continue
			}
			a, b := st.pts[ids[i]], st.pts[ids[j]]
			dx, dy := a.x-b.x, a.y-b.y
			if dx*dx+dy*dy <= e2 {
				union(a.id, b.id)
			}
		}
	}
	rootMin := make(map[int64]int64)
	for _, id := range ids {
		if !core[id] {
			continue
		}
		r := find(id)
		if rootMin[r] == 0 || id < rootMin[r] {
			rootMin[r] = id
		}
	}
	label := make(map[int64]int64)
	for _, id := range ids {
		if core[id] {
			label[id] = rootMin[find(id)]
		}
	}
	for _, id := range ids {
		if core[id] {
			continue
		}
		minLab := int64(0)
		for _, nb := range nbrs[id] {
			if !core[nb] {
				continue
			}
			lab := rootMin[find(nb)]
			if minLab == 0 || lab < minLab {
				minLab = lab
			}
		}
		label[id] = minLab
	}
	return &naiveLabels{core: core, label: label, nbrs: nbrs}
}

func naiveApplyAndDiff(st *naiveState, mutate func(), deleted map[int64]bool) ([]Change, []Event) {
	old := naiveCompute(st)
	mutate()
	nw := naiveCompute(st)
	var changes []Change
	seen := make(map[int64]bool)
	for id := range deleted {
		changes = append(changes, Change{ID: id, OldLabel: old.label[id], NewLabel: -1})
		seen[id] = true
	}
	for id, nl := range nw.label {
		if seen[id] {
			continue
		}
		ol, existed := old.label[id]
		if !existed {
			ol = -1
		}
		if ol != nl {
			changes = append(changes, Change{ID: id, OldLabel: ol, NewLabel: nl})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	return changes, buildEvents(snapshotClusters(old.core, old.label), snapshotClusters(nw.core, nw.label))
}

type opResult struct {
	ok      bool
	reason  Reason
	changes []Change
	events  []Event
	state   *naiveState
}

func copyNaiveState(st *naiveState) *naiveState {
	cp := &naiveState{
		eps: st.eps, minPts: st.minPts, w: st.w, c: st.c, now: st.now,
		pts: make(map[int64]*naivePoint, len(st.pts)),
	}
	for id, p := range st.pts {
		q := *p
		cp.pts[id] = &q
	}
	return cp
}

// runNaive 重放字符串编码的操作序列，返回最终朴素状态与每次操作的期望结果。
func runNaive(params [4]int64, ops []string) (*naiveState, []opResult) {
	st := &naiveState{
		eps:    params[0],
		minPts: int(params[1]),
		w:      params[2],
		c:      int(params[3]),
		pts:    make(map[int64]*naivePoint),
	}
	out := make([]opResult, len(ops))
	for idx, op := range ops {
		var kind string
		fmt.Sscanf(op, "%s", &kind)
		rest := strings.TrimSpace(op[len(kind):])
		switch kind {
		case "I":
			var id, x, y int64
			fmt.Sscanf(rest, "%d %d %d", &id, &x, &y)
			if id <= 0 || x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
				out[idx] = opResult{ok: false, reason: ReasonInvalidParam}
				continue
			}
			if st.pts[id] != nil {
				out[idx] = opResult{ok: false, reason: ReasonIDExists}
				continue
			}
			if len(st.pts) >= st.c {
				out[idx] = opResult{ok: false, reason: ReasonCapacityFull}
				continue
			}
			p := &naivePoint{id: id, x: x, y: y, born: st.now}
			ch, ev := naiveApplyAndDiff(st, func() { st.pts[id] = p }, nil)
			out[idx] = opResult{ok: true, changes: ch, events: ev, state: copyNaiveState(st)}
		case "R":
			var id int64
			fmt.Sscanf(rest, "%d", &id)
			if id <= 0 {
				out[idx] = opResult{ok: false, reason: ReasonInvalidParam}
				continue
			}
			if st.pts[id] == nil {
				out[idx] = opResult{ok: false, reason: ReasonIDNotFound}
				continue
			}
			deleted := map[int64]bool{id: true}
			ch, ev := naiveApplyAndDiff(st, func() { delete(st.pts, id) }, deleted)
			out[idx] = opResult{ok: true, changes: ch, events: ev, state: copyNaiveState(st)}
		case "T":
			var tt int64
			fmt.Sscanf(rest, "%d", &tt)
			if tt < 0 || tt > maxTick {
				out[idx] = opResult{ok: false, reason: ReasonInvalidParam}
				continue
			}
			if tt < st.now {
				out[idx] = opResult{ok: false, reason: ReasonClockRewind}
				continue
			}
			deleted := make(map[int64]bool)
			ch, ev := naiveApplyAndDiff(st, func() {
				for id, p := range st.pts {
					if p.born+st.w <= tt {
						deleted[id] = true
					}
				}
				for id := range deleted {
					delete(st.pts, id)
				}
				st.now = tt
			}, deleted)
			out[idx] = opResult{ok: true, changes: ch, events: ev, state: copyNaiveState(st)}
		}
	}
	return st, out
}

func genOps(rnd *rand.Rand, n int) []string {
	ops := make([]string, 0, n)
	t := int64(0)
	for i := 0; i < n; i++ {
		r := rnd.Intn(10)
		switch {
		case r < 6:
			id := int64(1 + rnd.Intn(12))
			x := int64(rnd.Intn(21) - 10)
			y := int64(rnd.Intn(21) - 10)
			ops = append(ops, fmt.Sprintf("I %d %d %d", id, x, y))
		case r < 8:
			id := int64(1 + rnd.Intn(14))
			ops = append(ops, fmt.Sprintf("R %d", id))
		default:
			t += int64(rnd.Intn(5))
			ops = append(ops, fmt.Sprintf("T %d", t))
		}
	}
	return ops
}

func parseOp(op string) (kind string, id, x, y, t int64) {
	fmt.Sscanf(op, "%s", &kind)
	rest := strings.TrimSpace(op[len(kind):])
	switch kind {
	case "I":
		fmt.Sscanf(rest, "%d %d %d", &id, &x, &y)
	case "R":
		fmt.Sscanf(rest, "%d", &id)
	case "T":
		fmt.Sscanf(rest, "%d", &t)
	}
	return
}

func invoke(svc *Service, op string) (*Result, error) {
	kind, id, x, y, t := parseOp(op)
	switch kind {
	case "I":
		return svc.Insert(id, x, y)
	case "R":
		return svc.Remove(id)
	case "T":
		return svc.Tick(t)
	}
	return nil, nil
}

func emptyToNil(s []int64) []int64 {
	if len(s) == 0 {
		return nil
	}
	return s
}

func sameResult(res *Result, want opResult) bool {
	if len(res.Changes) != len(want.changes) || len(res.Events) != len(want.events) {
		return false
	}
	for i := range res.Changes {
		if res.Changes[i] != want.changes[i] {
			return false
		}
	}
	for i := range res.Events {
		if res.Events[i].Type != want.events[i].Type ||
			!reflect.DeepEqual(emptyToNil(res.Events[i].OldLabels), emptyToNil(want.events[i].OldLabels)) ||
			!reflect.DeepEqual(emptyToNil(res.Events[i].NewLabels), emptyToNil(want.events[i].NewLabels)) {
			return false
		}
	}
	return true
}

// verifyState 用公开查询接口与朴素全量模型逐点核对标签、邻域、簇与存活数。
func verifyState(svc *Service, st *naiveState, log *strings.Builder) bool {
	ok := true
	nw := naiveCompute(st)
	if svc.Alive() != len(st.pts) {
		fmt.Fprintf(log, "  判定依据: Alive=%d want %d\n", svc.Alive(), len(st.pts))
		ok = false
	}
	for id := range st.pts {
		lab, exists := svc.Label(id)
		if !exists || lab != nw.label[id] {
			fmt.Fprintf(log, "  判定依据: Label(%d)=%d(exists=%v) want %d\n", id, lab, exists, nw.label[id])
			ok = false
		}
		nb, exists := svc.Neighbors(id)
		wantNb := append([]int64(nil), nw.nbrs[id]...)
		sort.Slice(wantNb, func(i, j int) bool { return wantNb[i] < wantNb[j] })
		if !exists || !reflect.DeepEqual(nb, wantNb) {
			fmt.Fprintf(log, "  判定依据: Neighbors(%d)=%v want %v\n", id, nb, wantNb)
			ok = false
		}
	}
	clusters := svc.Clusters()
	wantClusters := make(map[int64][]int64)
	for id, lab := range nw.label {
		if lab == 0 {
			continue
		}
		wantClusters[lab] = append(wantClusters[lab], id)
	}
	if len(clusters) != len(wantClusters) {
		fmt.Fprintf(log, "  判定依据: cluster count=%d want %d\n", len(clusters), len(wantClusters))
		ok = false
	}
	for _, c := range clusters {
		sort.Slice(c.Members, func(i, j int) bool { return c.Members[i] < c.Members[j] })
		sort.Slice(wantClusters[c.Label], func(i, j int) bool { return wantClusters[c.Label][i] < wantClusters[c.Label][j] })
		if !reflect.DeepEqual(c.Members, wantClusters[c.Label]) {
			fmt.Fprintf(log, "  判定依据: cluster %d=%v want %v\n", c.Label, c.Members, wantClusters[c.Label])
			ok = false
		}
	}
	return ok
}

func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seed := int64(1); seed <= sequences; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		params := [4]int64{
			int64(1 + rnd.Intn(6)),
			int64(1 + rnd.Intn(4)),
			int64(3 + rnd.Intn(8)),
			int64(6 + rnd.Intn(25)),
		}
		ops := genOps(rnd, 40)

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d params eps=%d minPts=%d W=%d C=%d\n",
			seed, params[0], params[1], params[2], params[3])

		svc, err := New(params[0], int(params[1]), params[2], int(params[3]))
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		st, wants := runNaive(params, ops)

		failed := false
		for idx, op := range ops {
			fmt.Fprintf(&log, "op%d 输入: %s\n  输出: ", idx, op)
			res, gerr := invoke(svc, op)
			want := wants[idx]
			if !want.ok {
				if gerr == nil {
					fmt.Fprintf(&log, "被接受，但朴素模型拒绝(%s)\n", want.reason)
					failed = true
					continue
				}
				oe := gerr.(*OpError)
				if oe.Reason != want.reason {
					fmt.Fprintf(&log, "拒绝原因=%s，朴素模型=%s\n", oe.Reason, want.reason)
					failed = true
					continue
				}
				fmt.Fprintf(&log, "拒绝(%s)，判定依据: 与朴素模型拒绝原因一致\n", want.reason)
				continue
			}
			if gerr != nil {
				fmt.Fprintf(&log, "意外错误 %v\n", gerr)
				failed = true
				continue
			}
			fmt.Fprintf(&log, "changes=%v events=%v\n", res.Changes, res.Events)
			if !sameResult(res, want) {
				fmt.Fprintf(&log, "  判定依据: 朴素模型 changes=%v events=%v\n", want.changes, want.events)
				failed = true
			}
			// 判定依据：每次接受操作后，公开查询结果与朴素整体重算逐点一致。
			if !verifyState(svc, want.state, &log) {
				failed = true
			}
		}
		if failed {
			t.Fatalf("seed %d 与朴素整体重算不一致:\n%s最终状态 Alive=%d\n", seed, log.String(), svc.Alive())
		}
		// 再与整段重放后的最终朴素状态核对一次。
		if !verifyState(svc, st, &log) {
			t.Fatalf("seed %d 最终状态不一致:\n%s", seed, log.String())
		}
		t.Logf("seed=%d: 40 ops 全部与朴素整体重算一致（输入/输出/判定依据见 -v 日志）；最终 Alive=%d",
			seed, svc.Alive())
	}
}
