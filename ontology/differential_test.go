package ontology

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// requestLog 记录每次请求的输入/输出/续读标记/截断原因，供事后核验。
type requestLog struct {
	Case       int      `json:"case"`
	Start      string   `json:"start"`
	MaxDepth   int      `json:"max_depth"`
	MaxFanout  int      `json:"max_fanout"`
	Actor      string   `json:"actor"`
	Page       []string `json:"page"`
	NextToken  string   `json:"next_token,omitempty"`
	Done       bool     `json:"done"`
	Truncation string   `json:"truncation"`
	Metrics    Metrics  `json:"metrics"`
}

type rngGraph struct {
	s     *Store
	acl   *ACL
	actor Actor
	nodes []string
}

func buildRandomGraph(rng *rand.Rand, n, edgePct int, visibility float64) *rngGraph {
	s := NewStore()
	s.PutLinkType(LinkType{ID: lt, Cost: 1})
	acl := NewACL(s)
	actor := Actor{ID: "alice"}
	g := &rngGraph{s: s, acl: acl, actor: actor}

	for i := 0; i < n; i++ {
		id := nodeID(i)
		s.PutObject(Object{ID: id, Type: "T"})
		// 一部分对象对调用者不可见。
		if rng.Float64() < visibility {
			s.GrantObjectSee(id, actor.ID)
			g.nodes = append(g.nodes, id)
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j || rng.Intn(100) >= edgePct {
				continue
			}
			from, to := nodeID(i), nodeID(j)
			if err := s.AddLink(Link{Type: lt, Source: from, Target: to}); err != nil {
				continue
			}
			// 一部分链接不可遍历。
			if rng.Float64() < visibility {
				s.GrantLinkTraverse(Link{Type: lt, Source: from, Target: to}, actor.ID)
			}
		}
	}
	return g
}

func nodeID(i int) string {
	// 固定宽度，保证字典序与生成序一致，便于排错。
	return "n" + pad(i)
}

func pad(i int) string {
	const width = 4
	digits := []byte("0000")
	for k := width - 1; k >= 0; k-- {
		digits[k] = byte('0' + i%10)
		i /= 10
	}
	return string(digits)
}

func paginateAll(t *testing.T, it *Iterator, start string, d, f int, actor Actor, logs []requestLog, caseID int) ([]Object, Truncation, []requestLog, Metrics) {
	t.Helper()
	var got []Object
	tok := ""
	var trunc Truncation
	var total Metrics
	for {
		p, err := it.Traverse(start, d, f, tok, actor)
		if err != nil {
			t.Fatalf("case %d traverse: %v", caseID, err)
		}
		total.ObjectsLoaded += p.metrics.ObjectsLoaded
		total.LinksScanned += p.metrics.LinksScanned
		total.ACLChecks += p.metrics.ACLChecks
		logs = append(logs, requestLog{
			Case: caseID, Start: start, MaxDepth: d, MaxFanout: f, Actor: actor.ID,
			Page: ids(p.Objects), NextToken: p.NextToken, Done: p.Done,
			Truncation: p.Truncation.String(), Metrics: p.metrics,
		})
		got = append(got, p.Objects...)
		trunc = p.Truncation
		if p.Done {
			break
		}
		tok = p.NextToken
	}
	return got, trunc, logs, total
}

// TestRandomDifferential：与独立朴素穷举模型在随机配置上做结果对照。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261007))
	var logs []requestLog

	for caseID := 0; caseID < 200; caseID++ {
		n := 3 + rng.Intn(14)
		edgePct := 5 + rng.Intn(40)
		vis := 0.5 + rng.Float64()*0.5
		g := buildRandomGraph(rng, n, edgePct, vis)
		if len(g.nodes) == 0 {
			continue
		}
		start := g.nodes[rng.Intn(len(g.nodes))]
		d := rng.Intn(4)
		f := rng.Intn(5)

		// 朴素模型：对当前版本一次性穷举。
		snap := g.s.Current()
		ref, err := NaiveTraverse(snap, g.acl, g.actor, start, d, f)
		if err != nil {
			t.Fatalf("case %d naive: %v", caseID, err)
		}

		it := NewIterator(g.s, g.acl)
		it.SetPageSize(1 + rng.Intn(3)) // 强制分页
		got, trunc, l, _ := paginateAll(t, it, start, d, f, g.actor, nil, caseID)
		logs = append(logs, l...)

		if trunc != ref.Trunc {
			t.Fatalf("case %d trunc: got %v want %v (start=%s d=%d f=%d)",
				caseID, trunc, ref.Trunc, start, d, f)
		}
		if len(got) != len(ref.Order) {
			t.Fatalf("case %d len: got %v want %v", caseID, ids(got), ids(ref.Order))
		}
		for i := range got {
			if got[i].ID != ref.Order[i].ID {
				t.Fatalf("case %d order mismatch at %d:\n got %v\nwant %v",
					caseID, i, ids(got), ids(ref.Order))
			}
		}
	}

	writeLogs(t, logs)
}

func writeLogs(t *testing.T, logs []requestLog) {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "ontology-traverse-logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("mkdir logs: %v", err)
		return
	}
	path := filepath.Join(dir, "requests.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Logf("create log: %v", err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for i := range logs {
		if err := enc.Encode(&logs[i]); err != nil {
			t.Logf("encode: %v", err)
			return
		}
	}
	t.Logf("wrote %d request records to %s", len(logs), path)
}

// TestConcurrentPaginationAndMutation：并发分页 + 并发变更下的快照锚定稳定性。
func TestConcurrentPaginationAndMutation(t *testing.T) {
	s := NewStore()
	s.PutLinkType(LinkType{ID: lt, Cost: 1})
	acl := NewACL(s)
	actor := Actor{ID: "alice"}
	for _, id := range []string{"A", "B", "C", "D", "E"} {
		addObject(s, acl, actor.ID, id)
	}
	addLink(s, acl, actor.ID, "A", "B")
	addLink(s, acl, actor.ID, "A", "C")
	addLink(s, acl, actor.ID, "B", "D")
	addLink(s, acl, actor.ID, "C", "E")

	it := NewIterator(s, acl)
	it.SetPageSize(1)
	p1, err := it.Traverse("A", 3, 10, "", actor)
	if err != nil {
		t.Fatal(err)
	}
	anchorTok := p1.NextToken

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 多个并发分页线程 + 一个变更线程同时运行。
	var readerWG sync.WaitGroup
	for w := 0; w < 8; w++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for k := 0; k < 200; k++ {
				p, err := it.Traverse("A", 3, 10, anchorTok, actor)
				if err != nil {
					t.Errorf("concurrent traverse: %v", err)
					return
				}
				if len(p.Objects) != 1 || p.Objects[0].ID != "B" {
					t.Errorf("token drifted under concurrency: %v", ids(p.Objects))
					return
				}
			}
		}()
	}

	// 变更线程在读者运行期间持续制造版本。
	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			id := "z" + pad(i%10)
			s.PutObject(Object{ID: id})
			s.GrantObjectSee(id, actor.ID)
			_ = s.AddLink(Link{Type: lt, Source: "A", Target: id})
			s.GrantLinkTraverse(Link{Type: lt, Source: "A", Target: id}, actor.ID)
			if i%2 == 0 {
				s.DeleteObject(id)
			}
			i++
		}
	}()

	readerWG.Wait()
	close(stop)
	wg.Wait()
}

// TestMetricsIndependentOfGraphSize：访问度量只与 d*f 邻域相关，与图规模无关。
func TestMetricsIndependentOfGraphSize(t *testing.T) {
	measure := func(totalNodes int) Metrics {
		rng := rand.New(rand.NewSource(42))
		s := NewStore()
		s.PutLinkType(LinkType{ID: lt, Cost: 1})
		acl := NewACL(s)
		actor := Actor{ID: "alice"}
		// 起点是一个与其余 totalNodes 节点完全不相连的独立小邻域。
		for _, id := range []string{"root", "a", "b"} {
			addObject(s, acl, actor.ID, id)
		}
		addLink(s, acl, actor.ID, "root", "a")
		addLink(s, acl, actor.ID, "root", "b")

		for i := 0; i < totalNodes; i++ {
			id := "far" + pad(i)
			s.PutObject(Object{ID: id})
			if rng.Intn(2) == 0 {
				s.GrantObjectSee(id, actor.ID)
			}
			// 与 root 无任何连接。
			if i > 0 {
				_ = s.AddLink(Link{Type: lt, Source: "far" + pad(i-1), Target: id})
			}
		}
		it := NewIterator(s, acl)
		p, err := it.Traverse("root", 1, 10, "", actor)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Done || len(p.Objects) != 3 {
			t.Fatalf("unexpected page: %v done=%v", ids(p.Objects), p.Done)
		}
		return p.Metrics()
	}

	m1 := measure(100)
	m2 := measure(4000)
	if m1.ObjectsLoaded != m2.ObjectsLoaded || m1.LinksScanned != m2.LinksScanned {
		t.Fatalf("metrics grew with graph size: small=%+v large=%+v", m1, m2)
	}
	t.Logf("metrics stable across graph size 100 -> 4000: %+v", m1)
}
