package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// normalize 将路径结果规范化为可比较、与枚举顺序无关的形式。
func normalize(paths []PathResult) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		key := fmt.Sprintf("v=%d h=%d end=%s hops=[", p.Verdict, p.Hidden, p.End)
		for _, h := range p.Prefix {
			key += h.LinkID + ","
		}
		key += "]"
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// TestNaiveDifferentialRandom 在大量随机图与随机权限集合上，
// 将高效实现与独立朴素实现逐条比对。
func TestNaiveDifferentialRandom(t *testing.T) {
	const iterations = 400
	rng := rand.New(rand.NewSource(20261007))
	for iter := 0; iter < iterations; iter++ {
		nobj := 2 + rng.Intn(7)
		objs := make([]string, nobj)
		for i := range objs {
			objs[i] = fmt.Sprintf("o%d", i)
		}
		allLabels := []string{"A", "B", "C"}
		g := NewGraphStore()
		for _, o := range objs {
			g.AddObject(o)
		}
		nlinks := rng.Intn(nobj*2 + 1)
		usedIDs := map[string]bool{}
		for k := 0; k < nlinks; k++ {
			id := fmt.Sprintf("e%d-%d", iter, k)
			if usedIDs[id] {
				continue
			}
			usedIDs[id] = true
			from := objs[rng.Intn(nobj)]
			to := objs[rng.Intn(nobj)]
			label := allLabels[rng.Intn(len(allLabels))]
			if err := g.AddLink(link(id, from, to, label)); err != nil {
				t.Fatalf("iter %d add: %v", iter, err)
			}
		}

		// 随机调用方权限集合：非空（空集合是错误路径，另行测试）。
		labels := map[string]struct{}{}
		for _, l := range allLabels {
			if rng.Intn(2) == 1 {
				labels[l] = struct{}{}
			}
		}
		if len(labels) == 0 {
			labels[allLabels[0]] = struct{}{}
		}
		maxDepth := 1 + rng.Intn(5)
		start := objs[rng.Intn(nobj)]

		snap := g.Snapshot()
		svc := NewService(g, nil)
		got, gerr := svc.traverseSnapshot(snap, TraverseRequest{
			Start: start, Labels: labels, MaxDepth: maxDepth,
		})
		want := NaiveTraverse(snap, TraverseRequest{
			Start: start, Labels: labels, MaxDepth: maxDepth,
		})

		// 起始对象不可见时两边都应静默（朴素返回空），服务端返回错误。
		if gerr != nil {
			if errorsIs(gerr, ErrStartNotVisible) && len(want.Paths) == 0 {
				continue
			}
			t.Fatalf("iter %d unexpected err %v", iter, gerr)
		}
		if !reflect.DeepEqual(normalize(got.Paths), normalize(want.Paths)) {
			var dbg []string
			for _, lks := range snap.out {
				for _, lk := range lks {
					dbg = append(dbg, fmt.Sprintf("%s:%s->%s(%s)", lk.ID, lk.From, lk.To, lk.Label))
				}
			}
			t.Fatalf("iter %d mismatch\n start=%s labels=%v depth=%d\n got=%v\nwant=%v\n edges=%v",
				iter, start, labels, maxDepth,
				normalize(got.Paths), normalize(want.Paths), dbg)
		}
	}
}
