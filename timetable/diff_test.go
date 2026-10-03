package timetable

import (
	"log"
	"math/rand"
	"testing"
)

// costAt 返回某版本快照下边 e 在绝对时刻 t 的耗时（-1 封闭，-2 无记录）。
func costAt(ce *cedge, t int64) int64 {
	i := ce.locate(t)
	if i < 0 {
		return -2
	}
	return ce.segs[i].cost
}

// naiveReference 用逐时刻时间展开做朴素最短路：
// 状态为 (节点, 时刻)；在每个到达时刻可等待到任意更晚时刻并出发。
// 做法：d[x] 为已知最早到达；枚举每个整时刻 t=t0..H 的动作。
// 返回 (最早到达, 是否在 H 内可达, 最优路线)。
//
// 路线 tie-break：先边数最少，再边号序列字典序最小。
type pathState struct {
	arr  int64
	seq  []int
	deps []int64
}

func naiveReference(edges []*cedge, N int, s int, t0 int64, g int, H int64) (int64, bool, []Leg) {
	// 按时刻扫描：best[x] 保存到 x 的最优结果（最早 arr；同 arr 取 tie-break）。
	best := make([]*pathState, N)
	best[s] = &pathState{arr: t0}
	out := make([][]*cedge, N)
	for _, ce := range edges {
		out[ce.u] = append(out[ce.u], ce)
	}
	better := func(a, b *pathState) bool {
		if a == nil {
			return false
		}
		if b == nil || a.arr < b.arr {
			return true
		}
		if a.arr > b.arr {
			return false
		}
		if len(a.seq) != len(b.seq) {
			return len(a.seq) < len(b.seq)
		}
		for i := range a.seq {
			if a.seq[i] != b.seq[i] {
				return a.seq[i] < b.seq[i]
			}
		}
		// 边序列相同：逐段出发时刻取字典序最小（即最小整数出发时刻）。
		for i := range a.deps {
			if a.deps[i] != b.deps[i] {
				return a.deps[i] < b.deps[i]
			}
		}
		return false
	}
	// 对每个 (x, 出发时刻 t) 枚举；为避免组合爆炸，按 arr 升序做
	// Bellman 风格松弛：反复扫描，直到一轮无改进（FIFO 下有限）。
	for iter := 0; iter < N+2; iter++ {
		improved := false
		for x := 0; x < N; x++ {
			b := best[x]
			if b == nil {
				continue
			}
			for _, ce := range out[x] {
				// 枚举从 b.arr 起到 H 的每个候选出发点：分段起点 + b.arr。
				cands := map[int64]bool{b.arr: true}
				for _, sg := range ce.segs {
					if sg.start >= b.arr && sg.start <= H {
						cands[sg.start] = true
					}
				}
				for dep := range cands {
					if dep < b.arr || dep > H {
						continue
					}
					c := costAt(ce, dep)
					if c < 0 {
						continue
					}
					arr := dep + c
					if arr > H {
						continue
					}
					seq := make([]int, 0, len(b.seq)+1)
					seq = append(seq, b.seq...)
					seq = append(seq, ce.id)
					deps := make([]int64, 0, len(b.deps)+1)
					deps = append(deps, b.deps...)
					deps = append(deps, dep)
					cand := &pathState{arr: arr, seq: seq, deps: deps}
					if better(cand, best[ce.v]) {
						best[ce.v] = cand
						improved = true
					}
				}
			}
		}
		if !improved {
			break
		}
	}
	bg := best[g]
	if bg == nil {
		return 0, false, nil
	}
	route := make([]Leg, len(bg.seq))
	for i := range bg.seq {
		arr := bg.deps[i]
		// 重新求该边在 dep 时的耗时得到 arr。
		var ce *cedge
		for _, e := range edges {
			if e.id == bg.seq[i] {
				ce = e
			}
		}
		arr += ce.segs[ce.locate(bg.deps[i])].cost
		route[i] = Leg{Edge: bg.seq[i], Dep: bg.deps[i], Arr: arr}
	}
	return bg.arr, true, route
}

// TestRandomDifferential 用 2000 组随机小路网与朴素时间展开结果对照。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		N := 2 + rng.Intn(7)
		E := 1 + rng.Intn(12)
		nw, _ := New(N, E+4)
		edgeIDs := []int{}
		for i := 0; i < E; i++ {
			u, v := rng.Intn(N), rng.Intn(N)
			if u == v {
				v = (v + 1) % N
			}
			p := randomProfile(rng)
			id, err := nw.AddEdge(u, v, p)
			if err != nil {
				t.Fatalf("case %d add: %v", tc, err)
			}
			edgeIDs = append(edgeIDs, id)
		}
		// 随机追加/替换少量通告（now 保持 0，允许任意 eff）。
		extra := rng.Intn(4)
		for i := 0; i < extra; i++ {
			id := edgeIDs[rng.Intn(len(edgeIDs))]
			eff := int64(rng.Intn(12))
			if err := nw.Announce(id, eff, randomProfile(rng)); err != nil {
				// 乱序等拒绝可接受：随机输入不强制成功。
				continue
			}
		}
		curVer := nw.Version()
		ver := curVer
		if rng.Intn(2) == 0 && curVer > 0 {
			ver = rng.Intn(curVer + 1)
		}
		s, g := rng.Intn(N), rng.Intn(N)
		t0 := int64(rng.Intn(12))
		H := int64(60)

		edges := nw.snapshot(ver)
		nArr, nOK, nRoute := naiveReference(edges, N, s, t0, g, H)
		r, qErr := nw.EarliestArrival(s, t0, g, ver)

		log.Printf("[diff %d] N=%d E=%d s=%d g=%d t0=%d ver=%d/%d -> naive(ok=%v arr=%d route=%v) service(err=%v result=%v)",
			tc, N, E, s, g, t0, ver, curVer, nOK, nArr, nRoute, qErr, r)

		if qErr != nil {
			if code(qErr) == ErrUnreachable {
				// 服务报不可达只可能是 H 外才可达：朴素已限定 H，
				// 若朴素 H 内可达则为分歧。
				if nOK {
					t.Fatalf("case %d: service unreachable but naive arr=%d route=%v", tc, nArr, nRoute)
				}
				continue
			}
			t.Fatalf("case %d unexpected error: %v", tc, qErr)
		}
		if !nOK {
			t.Fatalf("case %d: service arr=%d but naive unreachable within H=%d (path may exceed horizon)", tc, r.Arrival, H)
		}
		if r.Arrival != nArr || r.Arrival > H {
			t.Fatalf("case %d: arr mismatch service=%d naive=%d", tc, r.Arrival, nArr)
		}
		if !sameLegs(r.Route, nRoute) {
			t.Fatalf("case %d: route mismatch service=%v naive=%v", tc, r.Route, nRoute)
		}
		// popped 不得超过 d(x)<=d(g) 的节点数。
		full := nw.fullDistances(s, t0, ver)
		bound := 0
		for _, d := range full {
			if d >= 0 && d <= r.Arrival {
				bound++
			}
		}
		if r.Popped > bound {
			t.Fatalf("case %d: popped=%d > bound=%d", tc, r.Popped, bound)
		}
	}
}

func code(err error) ErrorCode {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return 0
}

func sameLegs(a, b []Leg) bool {
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

func randomProfile(rng *rand.Rand) []Segment {
	k := 1 + rng.Intn(4)
	segs := make([]Segment, k)
	off := int64(0)
	for i := 0; i < k; i++ {
		if i > 0 {
			off += int64(1 + rng.Intn(8))
		}
		c := int64(1 + rng.Intn(6))
		if rng.Intn(4) == 0 {
			c = -1
		}
		segs[i] = Segment{Offset: off, Cost: c}
	}
	return segs
}
