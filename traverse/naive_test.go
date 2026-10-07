package traverse

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/graph"
)

// 本文件包含一个独立实现的朴素遍历，用于与正式实现逐条对照。
// 朴素实现不做任何提前停止：先完整枚举全部终端路径，再以纯声明式
// 方式推导返回集、截断分支集与四种标记，借此交叉验证正式实现中
// 「达到条数上限即停止扩展」的优化不改变可观察结果。

// naiveStep 与正式实现的 Step 结构一致，但排序、展开逻辑全部独立重写。
type naiveStep struct {
	dir      graph.Direction
	linkID   string
	linkType string
	toID     string
}

type naivePath struct {
	start string
	steps []naiveStep
}

func (p naivePath) depth() int { return len(p.steps) }

func (p naivePath) end() string {
	if len(p.steps) == 0 {
		return p.start
	}
	return p.steps[len(p.steps)-1].toID
}

// naiveTraverse 返回 (返回路径标记序列, 截断分支标记序列)，路径以对象序列表示。
func naiveTraverse(snap *graph.Snapshot, req Request) ([]Marker, []Marker, []string, []string) {
	dirs := append([]graph.Direction(nil), req.Directions...)
	sort.Slice(dirs, func(i, j int) bool { return dirs[i] < dirs[j] })

	stepsOf := func(id string) []naiveStep {
		var out []naiveStep
		for _, d := range dirs {
			for _, l := range snap.Links(id, d) {
				st := naiveStep{dir: d, linkID: l.ID, linkType: l.Type}
				if d == graph.Outgoing {
					st.toID = l.TargetID
				} else {
					st.toID = l.SourceID
				}
				out = append(out, st)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			a, b := out[i], out[j]
			if a.dir != b.dir {
				return a.dir < b.dir
			}
			if a.linkType != b.linkType {
				return a.linkType < b.linkType
			}
			if a.toID != b.toID {
				return a.toID < b.toID
			}
			return a.linkID < b.linkID
		})
		return out
	}

	// 完整枚举全部终端路径（深度上限处截断），不做任何提前停止。
	var terminal []naivePath
	var enum func(p naivePath)
	enum = func(p naivePath) {
		if p.depth() == req.DepthLimit {
			terminal = append(terminal, p)
			return
		}
		sts := stepsOf(p.end())
		if len(sts) == 0 {
			terminal = append(terminal, p)
			return
		}
		for _, st := range sts {
			np := naivePath{start: p.start, steps: append(append([]naiveStep(nil), p.steps...), st)}
			enum(np)
		}
	}
	enum(naivePath{start: req.StartID})

	// 返回条件：跳数 >= 1。
	var candidates []naivePath
	for _, p := range terminal {
		if p.depth() >= 1 {
			candidates = append(candidates, p)
		}
	}

	pathStr := func(p naivePath) string {
		s := p.start
		for _, st := range p.steps {
			s += "-[" + string(st.dir) + ":" + st.linkType + ":" + st.linkID + "]->" + st.toID
		}
		return s
	}

	var retMarkers []Marker
	var retPaths []string
	n := req.ResultLimit
	if len(candidates) < n {
		n = len(candidates)
	}
	for i := 0; i < n; i++ {
		p := candidates[i]
		retPaths = append(retPaths, pathStr(p))
		switch {
		case p.depth() == req.DepthLimit && i+1 == req.ResultLimit:
			retMarkers = append(retMarkers, MarkerDepthThenLimit)
		case p.depth() == req.DepthLimit:
			retMarkers = append(retMarkers, MarkerDepthOnly)
		default:
			retMarkers = append(retMarkers, MarkerNone)
		}
	}

	// 截断分支 = 候选序列中第 N 条之后每条路径相对第 N 条的「分叉前缀」集合。
	var truncMarkers []Marker
	var truncPaths []string
	if len(candidates) > req.ResultLimit {
		tN := candidates[req.ResultLimit-1]
		isPrefix := func(prefix, full naivePath) bool {
			if len(prefix.steps) > len(full.steps) {
				return false
			}
			for i := range prefix.steps {
				if prefix.steps[i] != full.steps[i] {
					return false
				}
			}
			return true
		}
		seen := map[string]naivePath{}
		for _, t := range candidates[req.ResultLimit:] {
			lcp := 0
			for lcp < len(tN.steps) && lcp < len(t.steps) && tN.steps[lcp] == t.steps[lcp] {
				lcp++
			}
			q := naivePath{start: t.start, steps: t.steps[:lcp+1]}
			seen[pathStr(q)] = q
		}
		var qs []naivePath
		for _, q := range seen {
			qs = append(qs, q)
		}
		lessNaive := func(a, b naivePath) bool {
			n := len(a.steps)
			if len(b.steps) < n {
				n = len(b.steps)
			}
			for i := 0; i < n; i++ {
				x, y := a.steps[i], b.steps[i]
				if x.dir != y.dir {
					return x.dir < y.dir
				}
				if x.linkType != y.linkType {
					return x.linkType < y.linkType
				}
				if x.toID != y.toID {
					return x.toID < y.toID
				}
				if x.linkID != y.linkID {
					return x.linkID < y.linkID
				}
			}
			return len(a.steps) < len(b.steps)
		}
		sort.Slice(qs, func(i, j int) bool { return lessNaive(qs[i], qs[j]) })
		for _, q := range qs {
			truncPaths = append(truncPaths, pathStr(q))
			fire := q.depth() == req.DepthLimit
			if !fire {
				for _, t := range terminal {
					if t.depth() == req.DepthLimit && isPrefix(q, t) {
						fire = true
						break
					}
				}
			}
			if fire {
				truncMarkers = append(truncMarkers, MarkerLimitThenDepth)
			} else {
				truncMarkers = append(truncMarkers, MarkerLimitOnly)
			}
		}
	}
	return retMarkers, truncMarkers, retPaths, truncPaths
}

// snapshotOf 直接构造快照（测试辅助）。
func snapshotOf(t *testing.T, s *graph.Store) *graph.Snapshot {
	t.Helper()
	return s.Snapshot()
}

// 大量随机图结构与上限组合上，正式实现与朴素实现逐条比对标记结果。
func TestCrossCheckAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20260918))
	for tc := 0; tc < 400; tc++ {
		numObj := 2 + rng.Intn(8)
		objects := make([]string, numObj)
		for i := range objects {
			objects[i] = string(rune('a' + i))
		}
		s := graph.NewStore()
		for _, id := range objects {
			if err := s.AddObject(graph.Object{ID: id}); err != nil {
				t.Fatal(err)
			}
		}
		numLinks := rng.Intn(2*numObj + 1)
		for i := 0; i < numLinks; i++ {
			l := graph.Link{
				ID:       string(rune('A'+tc%26)) + "-" + string(rune('a'+i)),
				Type:     []string{"t1", "t2"}[rng.Intn(2)],
				SourceID: objects[rng.Intn(numObj)],
				TargetID: objects[rng.Intn(numObj)],
			}
			if err := s.AddLink(l); err != nil {
				t.Fatal(err)
			}
		}
		dirs := []graph.Direction{graph.Outgoing}
		if rng.Intn(2) == 0 {
			dirs = append(dirs, graph.Incoming)
		}
		req := Request{
			StartID:     objects[rng.Intn(numObj)],
			Directions:  dirs,
			DepthLimit:  1 + rng.Intn(4),
			ResultLimit: 1 + rng.Intn(20),
		}

		got, err := NewService(s, nil).Traverse(req)
		if err != nil {
			t.Fatalf("case %d: %v", tc, err)
		}
		wantRet, wantTrunc, wantRetPaths, wantTruncPaths := naiveTraverse(snapshotOf(t, s), req)

		eq := func(a, b any) bool {
			// 空切片与 nil 视为相等
			if reflect.ValueOf(a).Len() == 0 && reflect.ValueOf(b).Len() == 0 {
				return true
			}
			return reflect.DeepEqual(a, b)
		}
		if gotRet := markers(got.Returned); !eq(gotRet, wantRet) {
			t.Fatalf("case %d req=%+v:\nreturned markers got %v want %v\ngot paths %v\nwant paths %v",
				tc, req, gotRet, wantRet, pathStrings(got.Returned), wantRetPaths)
		}
		if gotRetPaths := pathStrings(got.Returned); !eq(gotRetPaths, wantRetPaths) {
			t.Fatalf("case %d req=%+v:\nreturned paths got %v want %v", tc, req, gotRetPaths, wantRetPaths)
		}
		if gotTrunc := markers(got.Truncated); !eq(gotTrunc, wantTrunc) {
			t.Fatalf("case %d req=%+v:\ntruncated markers got %v want %v\ngot paths %v\nwant paths %v",
				tc, req, gotTrunc, wantTrunc, pathStrings(got.Truncated), wantTruncPaths)
		}
		if gotTruncPaths := pathStrings(got.Truncated); !eq(gotTruncPaths, wantTruncPaths) {
			t.Fatalf("case %d req=%+v:\ntruncated paths got %v want %v", tc, req, gotTruncPaths, wantTruncPaths)
		}
	}
}
