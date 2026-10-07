package ontology

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// TestRandomDifferential 在随机图上把生产实现与朴素穷举模型逐配置对照，
// 并通过分页（含重放）验证对象集合、顺序、截断原因、度量完全一致。
func TestRandomDifferential(t *testing.T) {
	traversalLog.reset()

	const cases = 400
	rng := rand.New(rand.NewSource(20261007))
	caller := &Principal{Name: "random-caller"}

	for iter := 0; iter < cases; iter++ {
		nObjects := 1 + rng.Intn(14)
		objectIDs := make([]ObjectID, nObjects)
		hidden := map[*Principal][]ObjectID{}
		for i := range objectIDs {
			objectIDs[i] = ObjectID(idName(i))
			// 约 15% 对象对调用者隐藏。
			if rng.Intn(100) < 15 && i > 0 {
				hidden[caller] = append(hidden[caller], objectIDs[i])
			}
		}

		var blocked map[*Principal][]Link
		nLinks := rng.Intn(nObjects * 2)
		type spec struct {
			link Link
			dir  LinkDirection
		}
		specs := make([]spec, 0, nLinks)
		for i := 0; i < nLinks; i++ {
			from := objectIDs[rng.Intn(nObjects)]
			to := objectIDs[rng.Intn(nObjects)]
			l := Link{Type: LinkTypeID(dirName(rng.Intn(3))), From: from, To: to}
			dir := dirFromName(string(l.Type))
			specs = append(specs, spec{link: l, dir: dir})
			if rng.Intn(100) < 15 {
				if blocked == nil {
					blocked = map[*Principal][]Link{}
				}
				blocked[caller] = append(blocked[caller], l)
			}
		}

		policy := NewACLPolicy(hidden, blocked)
		st := NewStore(policy)
		for _, id := range objectIDs {
			st.AddObject(Object{ID: id})
		}
		for _, s := range specs {
			st.AddLinkType(LinkType{ID: s.link.Type, Direction: s.dir, Cost: 1})
			st.AddLink(s.link)
		}

		start := objectIDs[rng.Intn(nObjects)]
		maxDepth := rng.Intn(5)
		maxFanout := rng.Intn(5)
		pageSize := 1 + rng.Intn(4)

		// 起点不可见时只核对拒绝语义。
		if !policy.CanSeeObject(st.Current(), caller, start) {
			if _, err := NewTraverser(st).Traverse(caller, TraverseParams{
				Start: start, MaxDepth: maxDepth, MaxFanout: maxFanout,
			}); err != ErrForbidden {
				t.Fatalf("iter %d: hidden start err=%v want ErrForbidden", iter, err)
			}
			continue
		}

		g := oracleFromSnapshot(st.Current())
		want := oracleNeighborhood(g, policy, st.Current(), caller, start, maxDepth, maxFanout)

		tr := NewTraverser(st)
		p := TraverseParams{Start: start, MaxDepth: maxDepth, MaxFanout: maxFanout, PageSize: pageSize}
		var got []ObjectID
		var reason TruncationReason
		var firstToken string
		for {
			page, err := tr.Traverse(caller, p)
			if err != nil {
				t.Fatalf("iter %d: traverse: %v (start=%s d=%d f=%d)", iter, err, start, maxDepth, maxFanout)
			}
			if firstToken == "" && page.NextToken != "" {
				firstToken = page.NextToken
			}
			got = append(got, page.Objects...)
			reason = page.Truncation
			traversalLog.record(p, page.Objects, reason, page.NextToken)
			if page.NextToken == "" {
				break
			}
			p.Token = page.NextToken
			// 约 10% 概率把 MaxDepth/MaxFanout 留 0（沿用标记内参数）。
			if rng.Intn(10) == 0 {
				p.MaxDepth = 0
				p.MaxFanout = 0
			}
		}

		if reason != want.Reason || !equalIDs(got, want.Order) {
			t.Fatalf("iter %d mismatch: start=%s d=%d f=%d ps=%d\n got=%v (%v)\nwant=%v (%v)",
				iter, start, maxDepth, maxFanout, pageSize, got, reason, want.Order, want.Reason)
		}

		// 重放校验：用首页标记再取一次，结果必须与当时的下一页逐字节相同。
		if firstToken != "" {
			replayed, err := tr.Traverse(caller, TraverseParams{Token: firstToken})
			if err != nil {
				t.Fatalf("iter %d replay: %v", iter, err)
			}
			second, err := tr.Traverse(caller, TraverseParams{Token: firstToken})
			if err != nil || !equalIDs(replayed.Objects, second.Objects) {
				t.Fatalf("iter %d replay drift: %v vs %v err=%v", iter, replayed.Objects, second.Objects, err)
			}
		}
	}

	writeTraversalLog(t)
}

func idName(i int) string {
	// 固定宽度，便于阅读与排序。
	const alphabet = "abcdefghijklmnopqrstuvwxyz"
	name := ""
	for {
		name = string(alphabet[i%26]) + name
		i /= 26
		if i == 0 {
			break
		}
		i--
	}
	return "n" + name
}

func dirName(i int) string {
	return [...]string{"out", "in", "both"}[i%3]
}

func dirFromName(name string) LinkDirection {
	switch name {
	case "in":
		return DirectionIn
	case "both":
		return DirectionBoth
	default:
		return DirectionOut
	}
}

// writeTraversalLog 把每次请求的输入/输出/标记/截断原因落盘，便于审计。
// 路径由 ONTOLOGY_TRAVERSAL_LOG 指定，缺省写入测试临时目录。
func writeTraversalLog(t *testing.T) {
	t.Helper()
	traversalLog.mu.Lock()
	data, err := json.MarshalIndent(traversalLog.entry, "", "  ")
	traversalLog.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("ONTOLOGY_TRAVERSAL_LOG")
	if path == "" {
		path = filepath.Join(t.TempDir(), "traversal-log.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d traversal requests to %s", len(traversalLog.entry), path)
}
