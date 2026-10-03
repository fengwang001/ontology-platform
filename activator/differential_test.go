package activator

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/config"
)

type randOp struct {
	kind string
	ver  int64
	in   config.PushInput
	name string
	now  int64
}

func genOps(seed int64) (int64, []randOp) {
	rng := rand.New(rand.NewSource(seed))
	w := int64(1 + rng.Intn(10))
	const nc, nr = 4, 4
	var ops []randOp
	n := newNaive(w)
	now := int64(0)
	for st := 0; st < 40; st++ {
		now += int64(rng.Intn(int(w) + 2))
		if rng.Intn(8) == 0 {
			now += w * 2 // 强制越过超时点
		}
		switch rng.Intn(4) {
		case 0:
			ver := n.lastVer + 1
			if rng.Intn(6) == 0 {
				ver += int64(1 + rng.Intn(3))
			}
			in := config.PushInput{}
			for i := 0; i < nc; i++ {
				if rng.Intn(2) == 0 {
					in.Clusters = append(in.Clusters, config.Cluster{Name: fmt.Sprintf("c%d", i)})
				}
			}
			for i := 0; i < nr; i++ {
				if rng.Intn(3) == 0 {
					k := 1 + rng.Intn(3)
					refs := map[string]bool{}
					for len(refs) < k {
						refs[fmt.Sprintf("c%d", rng.Intn(nc+1))] = true // 含悬空池外名 c4
					}
					rf := make([]string, 0, k)
					for x := range refs {
						rf = append(rf, x)
					}
					sort.Strings(rf)
					in.Routes = append(in.Routes, config.Route{Name: fmt.Sprintf("r%d", i), Clusters: rf})
				}
			}
			for i := 0; i < nc; i++ {
				if rng.Intn(5) == 0 {
					in.DeleteClusters = append(in.DeleteClusters, fmt.Sprintf("c%d", i))
				}
			}
			for i := 0; i < nr; i++ {
				if rng.Intn(6) == 0 {
					in.DeleteRoutes = append(in.DeleteRoutes, fmt.Sprintf("r%d", i))
				}
			}
			ops = append(ops, randOp{kind: "push", ver: ver, in: in, now: now})
			// 用独立的朴素模型跟踪接受结果以生成下一步合法序号。
			n.push(ver, in, now)
		case 1:
			ops = append(ops, randOp{kind: "ready", name: fmt.Sprintf("c%d", rng.Intn(nc)), now: now})
			n.ready(ops[len(ops)-1].name, now)
		case 2:
			ops = append(ops, randOp{kind: "serving", name: fmt.Sprintf("r%d", rng.Intn(nr)), now: now})
			n.query("serving", ops[len(ops)-1].name, now)
		default:
			ops = append(ops, randOp{kind: "state", name: fmt.Sprintf("c%d", rng.Intn(nc)), now: now})
			n.query("state", ops[len(ops)-1].name, now)
		}
	}
	return w, ops
}

// TestRandomDifferential 2000 组随机序列：产品实现与朴素模拟器逐步对照，
// 日志打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const seeds = 2000
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			w, ops := genOps(seed)
			a := New(w)
			n := newNaive(w)
			var log strings.Builder
			defer func() {
				if t.Failed() {
					t.Logf("\n%s", log.String())
				}
			}()
			fmt.Fprintf(&log, "W=%d\n", w)
			for i, o := range ops {
				fmt.Fprintf(&log, "#%d %s ver=%d now=%d name=%q in=%+v\n",
					i, o.kind, o.ver, o.now, o.name, o.in)
				var gotCode string
				var gotVal int64
				var gotOK bool
				switch o.kind {
				case "push":
					gotCode = codeOf(a.Push(o.ver, o.in, o.now))
				case "ready":
					gotCode = codeOf(a.Ready(o.name, o.now))
				case "serving":
					gotVal, gotOK, _ = a.Serving(o.name, o.now)
				case "state":
					st, ok, _ := a.State(o.name, o.now)
					gotVal, gotOK = st.ServingVer, ok
				}

				var nr nResult
				switch o.kind {
				case "push":
					nr = n.push(o.ver, o.in, o.now)
				case "ready":
					nr = n.ready(o.name, o.now)
				default:
					nr = n.query(o.kind, o.name, o.now)
				}
				fmt.Fprintf(&log, "   impl: code=%s val=%d ok=%v | naive: code=%s val=%d ok=%v\n",
					gotCode, gotVal, gotOK, nr.errCode, nr.val, nr.ok)
				if gotCode != nr.errCode || (gotCode == "" && (gotVal != nr.val || gotOK != nr.ok)) {
					t.Fatalf("seed=%d step#%d mismatch:\n%s", seed, i, log.String())
				}
			}
		})
	}
}

// TestReadyCascadeBound 验证 Ready 检查数 ≤ 引用该集群的待激活路由数 + 1，
// 路由总数覆盖 100 与 10000 两档。
func TestReadyCascadeBound(t *testing.T) {
	for _, total := range []int{100, 10000} {
		t.Run(fmt.Sprintf("routes-%d", total), func(t *testing.T) {
			a := New(10)
			in := config.PushInput{Clusters: []config.Cluster{{Name: "dep"}}}
			for i := 0; i < total; i++ {
				in.Routes = append(in.Routes,
					config.Route{Name: fmt.Sprintf("r%d", i), Clusters: []string{"dep"}})
			}
			if err := a.Push(1, in, 0); err != nil {
				t.Fatal(err)
			}
			if err := a.Ready("dep", 1); err != nil {
				t.Fatal(err)
			}
			checks := a.lastReadyCascadeBound()
			if checks > total+1 {
				t.Fatalf("checks=%d exceed bound %d", checks, total+1)
			}
			if checks != total+1 {
				t.Fatalf("checks=%d want %d (all pending reference dep, +1 self)", checks, total+1)
			}
			// 第二次 Ready 已无待激活路由引用：只付 +1 自身检查。
			in2 := config.PushInput{Clusters: []config.Cluster{{Name: "dep"}}}
			if err := a.Push(2, in2, 2); err != nil {
				t.Fatal(err)
			}
			if err := a.Ready("dep", 3); err != nil {
				t.Fatal(err)
			}
			if checks := a.lastReadyCascadeBound(); checks != 1 {
				t.Fatalf("idle Ready checks=%d want 1", checks)
			}
		})
	}
}

// TestConcurrentSerialEquivalence 并发压力：在 -race 下验证无数据竞争，
// 并在结束后检查跨包不变量（在役路由引用的集群必存在且就绪）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	a := New(5)
	if err := a.Push(1, withRoute(pushC("c1", "c2"), "r1", "c1", "c2"), 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var clock int64
	var clockMu sync.Mutex
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				clockMu.Lock()
				clock++
				now := clock
				clockMu.Unlock()
				_, _, _ = a.Serving("r1", now)
				_, _, _ = a.State("c1", now)
			}
		}()
	}
	wg.Wait()
	a.mu.Lock()
	defer a.mu.Unlock()
	for name, r := range a.routes {
		if r.servingVer == 0 {
			continue
		}
		for _, ref := range r.servingRef {
			if !a.clusters.IsReady(ref) {
				t.Fatalf("invariant broken: serving route %s v%d refs %s not ready",
					name, r.servingVer, ref)
			}
		}
	}
}
