package ontology

// 本文件包含一个独立维护完整图与各权限标签集合的朴素参考实现，
// 仅用于测试对照：它与正式实现不共享任何数据结构，
// 祖先核对使用线性扫描，可见性判定逐条链接重新联结标签表。

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// naiveGraph 独立维护完整图结构（links）与权限标签集合（labels）。
type naiveGraph struct {
	objects map[ObjectID]bool
	links   []Link           // 仅含拓扑：From/To/ID，Label 字段不使用
	labels  map[string]Label // 链接 ID -> 权限标签
}

func newNaiveGraph() *naiveGraph {
	return &naiveGraph{objects: make(map[ObjectID]bool), labels: make(map[string]Label)}
}

func (g *naiveGraph) addLink(l Link) {
	g.objects[l.From] = true
	g.objects[l.To] = true
	g.links = append(g.links, Link{ID: l.ID, From: l.From, To: l.To})
	g.labels[l.ID] = l.Label
}

func (g *naiveGraph) visible(l Link, caller map[Label]bool) bool {
	return caller[g.labels[l.ID]]
}

// naiveTraverse 是朴素遍历：逐条比对、线性扫描祖先序列。
// 语义必须与正式实现完全一致（错误次序、标记规则、去重规则）。
func (g *naiveGraph) traverse(req Request) (*Result, error) {
	if !g.objects[req.Start] {
		return nil, ErrStartNotFound
	}
	if len(req.CallerLabels) == 0 {
		return nil, ErrEmptyCallerLabels
	}
	if req.MaxDepth <= 0 {
		return nil, ErrInvalidMaxDepth
	}
	caller := make(map[Label]bool, len(req.CallerLabels))
	for _, l := range req.CallerLabels {
		caller[l] = true
	}
	// 起始对象可见性：逐条检查所有关联链接。
	startVisible := false
	for _, l := range g.links {
		if (l.From == req.Start || l.To == req.Start) && g.visible(l, caller) {
			startVisible = true
			break
		}
	}
	if !startVisible {
		return nil, ErrStartInvisible
	}
	root := g.expand(req.Start, 0, req.MaxDepth, caller, nil)
	return &Result{Root: root}, nil
}

func (g *naiveGraph) expand(obj ObjectID, depth, maxDepth int, caller map[Label]bool, path []ObjectID) *Node {
	node := &Node{Object: obj}
	if depth >= maxDepth {
		return node
	}
	path = append(path, obj)
	hiddenCycle, hiddenExt := false, false
	for _, l := range g.links {
		if l.From != obj {
			continue
		}
		cycle := slices.Contains(path, l.To) // 线性扫描祖先序列
		visible := g.visible(l, caller)
		switch {
		case cycle && visible:
			node.Edges = append(node.Edges, &Edge{
				Kind: TermCycle, Visible: true,
				LinkID: l.ID, LinkLabel: g.labels[l.ID], To: l.To,
			})
		case cycle:
			hiddenCycle = true
		case !visible:
			hiddenExt = true
		default:
			node.Edges = append(node.Edges, &Edge{
				Kind: TermExtended, Visible: true,
				LinkID: l.ID, LinkLabel: g.labels[l.ID], To: l.To,
				Child: g.expand(l.To, depth+1, maxDepth, caller, path),
			})
		}
	}
	if hiddenCycle {
		node.Edges = append(node.Edges, &Edge{Kind: TermCycle})
	}
	if hiddenExt {
		node.Edges = append(node.Edges, &Edge{Kind: TermHidden})
	}
	return node
}

// TestNaiveDifferential 在大量随机场景上把正式实现与朴素实现逐条比对。
func TestNaiveDifferential(t *testing.T) {
	const scenarios = 800
	rng := rand.New(rand.NewSource(20261007))
	for i := 0; i < scenarios; i++ {
		var (
			store = NewStore()
			naive = newNaiveGraph()
			objs  []ObjectID
		)
		nObj := 2 + rng.Intn(7)
		for j := 0; j < nObj; j++ {
			objs = append(objs, ObjectID(fmt.Sprintf("o%d", j)))
		}
		nLabels := 1 + rng.Intn(4)
		labels := make([]Label, nLabels)
		for j := range labels {
			labels[j] = Label(fmt.Sprintf("L%d", j))
		}
		nLinks := rng.Intn(16)
		for j := 0; j < nLinks; j++ {
			l := Link{
				ID:    fmt.Sprintf("e%d", j),
				From:  objs[rng.Intn(nObj)],
				To:    objs[rng.Intn(nObj)],
				Label: labels[rng.Intn(nLabels)],
			}
			if err := store.AddLink(l); err != nil {
				t.Fatal(err)
			}
			naive.addLink(l)
		}
		// 随机调用方标签集合（保证非空以覆盖主路径；
		// 另以一定概率置空覆盖错误路径）。
		var callerLabels []Label
		if rng.Intn(10) > 0 {
			for _, l := range labels {
				if rng.Intn(2) == 0 {
					callerLabels = append(callerLabels, l)
				}
			}
			if len(callerLabels) == 0 {
				callerLabels = append(callerLabels, labels[0])
			}
		}
		req := Request{
			CallerLabels: callerLabels,
			Start:        objs[rng.Intn(nObj)],
			MaxDepth:     1 + rng.Intn(6),
		}
		gotRes, gotErr := Traverse(store, req, nil)
		wantRes, wantErr := naive.traverse(req)
		if !errors.Is(gotErr, wantErr) {
			t.Fatalf("scenario %d: error mismatch: got %v, want %v", i, gotErr, wantErr)
		}
		if gotErr != nil {
			continue
		}
		if got, want := render(gotRes.Root), render(wantRes.Root); got != want {
			t.Fatalf("scenario %d: tree mismatch:\ngot:\n%s\nwant:\n%s", i, got, want)
		}
		// 正式实现的祖先核对次数必须恒等于扩展链接数（每条恰好一次）。
		if gotRes.Stats.AncestorChecks != gotRes.Stats.ExpandedLinks {
			t.Fatalf("scenario %d: ancestor checks %d != expanded links %d",
				i, gotRes.Stats.AncestorChecks, gotRes.Stats.ExpandedLinks)
		}
	}
}
