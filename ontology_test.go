package ontology

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
	"ontology/sched"
)

// recordingHooks 用 fail.Tracker 实现 sched.Hooks，供调度器单测使用。
type recordingHooks struct {
	tr     *fail.Tracker
	mu     sync.Mutex
	starts []string
	onFail func()
}

func (h *recordingHooks) OnStart(id string) {
	h.tr.MarkStarted(id)
	h.mu.Lock()
	h.starts = append(h.starts, id)
	h.mu.Unlock()
}
func (h *recordingHooks) OnResult(id string, err error, panicked bool) {
	if err != nil {
		h.tr.Complete(id, fail.StatusFailed, err)
		if h.onFail != nil {
			h.onFail()
		}
	} else {
		h.tr.Complete(id, fail.StatusSuccess, nil)
	}
}
func (h *recordingHooks) OnAbort(id string) {
	h.tr.CancelOne(id)
}
func (h *recordingHooks) MarkSkipped(id string) bool {
	return h.tr.MarkSkipped(id)
}

func runSched(t *testing.T, g *graph.Graph, funcs map[string]sched.TaskFunc,
	conc int, abortOnFail bool) (*sched.Scheduler, *fail.Tracker) {
	t.Helper()
	tr := fail.NewTracker(g)
	h := &recordingHooks{tr: tr}
	s := sched.New(g, funcs, h, conc)
	ctx, cancel := context.WithCancel(context.Background())
	if abortOnFail {
		h.onFail = func() {
			s.Abort()
			cancel()
		}
	}
	s.Run(ctx)
	cancel()
	return s, tr
}

func chainGraph(ids ...string) *graph.Graph {
	g := graph.New()
	for _, id := range ids {
		g.AddNode(id)
	}
	for i := 1; i < len(ids); i++ {
		_ = g.AddEdge(ids[i-1], ids[i])
	}
	return g
}

func TestFailStates(t *testing.T) {
	cases := []struct {
		name  string
		apply func(*fail.Tracker)
		check func(*testing.T, *fail.Tracker)
	}{
		{
			name: "skip reason points to earliest failed ancestor",
			apply: func(tr *fail.Tracker) {
				tr.Complete("A", fail.StatusFailed, errors.New("boom"))
				tr.MarkSkipped("B")
				tr.MarkSkipped("C")
			},
			check: func(t *testing.T, tr *fail.Tracker) {
				snap := tr.Snapshot()
				byID := map[string]fail.State{}
				for _, s := range snap {
					byID[s.ID] = s
				}
				for _, id := range []string{"B", "C"} {
					if byID[id].Status != fail.StatusSkipped || byID[id].ReasonID != "A" {
						t.Fatalf("%s = %+v, want skipped reason A", id, byID[id])
					}
				}
			},
		},
		{
			name: "started task cannot be skipped; late result discarded after cancel",
			apply: func(tr *fail.Tracker) {
				tr.MarkStarted("D")
				if tr.MarkSkipped("D") {
					t.Fatal("started task must not be marked skipped")
				}
				tr.CancelPending(false)
				if tr.Complete("D", fail.StatusSuccess, nil) {
					t.Fatal("late write after cancel must be discarded")
				}
			},
			check: func(t *testing.T, tr *fail.Tracker) {
				s := tr.StateOf("D")
				if s.Status != fail.StatusCanceled || !s.Started {
					t.Fatalf("D = %+v, want canceled+started", s)
				}
			},
		},
		{
			name: "success is terminal and never overwritten",
			apply: func(tr *fail.Tracker) {
				tr.Complete("A", fail.StatusSuccess, nil)
				tr.Complete("A", fail.StatusFailed, errors.New("late"))
			},
			check: func(t *testing.T, tr *fail.Tracker) {
				if s := tr.StateOf("A"); s.Status != fail.StatusSuccess {
					t.Fatalf("A = %+v, want success kept", s)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := fail.NewTracker(chainGraph("A", "B", "C"))
			if tc.name == "started task cannot be skipped; late result discarded after cancel" {
				g := graph.New()
				g.AddNode("D")
				tr = fail.NewTracker(g)
			}
			tc.apply(tr)
			tc.check(t, tr)
		})
	}
}

func TestFailPanicWrap(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"string panic", "kaboom", "kaboom"},
		{"int panic", 42, "42"},
		{"error panic", errors.New("x"), "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fail.WrapPanic(tc.v)
			if !errors.Is(err, fail.ErrTaskPanic) {
				t.Fatalf("err %v must match ErrTaskPanic", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %q must contain original %q", err, tc.want)
			}
		})
	}
}

func noopTasks(g *graph.Graph) map[string]sched.TaskFunc {
	m := map[string]sched.TaskFunc{}
	for _, id := range g.Nodes() {
		id := id
		m[id] = func(context.Context) error { return nil }
	}
	return m
}

func statusMap(tr *fail.Tracker) map[string]fail.State {
	m := map[string]fail.State{}
	for _, st := range tr.Snapshot() {
		m[st.ID] = st
	}
	return m
}

func TestSchedConcurrencyAndDecisions(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T) (peak, decisions int)
	}{
		{
			name: "500 independent tasks capped at 8",
			run: func(t *testing.T) (int, int) {
				g := graph.New()
				for i := 0; i < 500; i++ {
					g.AddNode(fmt.Sprintf("n%03d", i))
				}
				release := make(chan struct{})
				var inMu sync.Mutex
				inFlight := 0
				maxSeen := 0
				tasks := map[string]sched.TaskFunc{}
				for _, id := range g.Nodes() {
					tasks[id] = func(context.Context) error {
						inMu.Lock()
						inFlight++
						if inFlight > maxSeen {
							maxSeen = inFlight
						}
						inMu.Unlock()
						<-release
						inMu.Lock()
						inFlight--
						inMu.Unlock()
						return nil
					}
				}
				tr := fail.NewTracker(g)
				h := &recordingHooks{tr: tr}
				s := sched.New(g, tasks, h, 8)
				go func() {
					time.Sleep(50 * time.Millisecond)
					close(release)
				}()
				s.Run(context.Background())
				if maxSeen > 8 {
					t.Fatalf("observed concurrency %d > 8", maxSeen)
				}
				return s.Peak(), s.Decisions()
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peak, decisions := tc.run(t)
			if peak > 8 {
				t.Fatalf("scheduler peak %d > 8", peak)
			}
			if decisions > 4*(500+0) {
				t.Fatalf("decisions %d > %d", decisions, 4*500)
			}
		})
	}
}

func TestSchedDecisionBoundGraphs(t *testing.T) {
	mk := func(nodes, extraEdges int) *graph.Graph {
		g := graph.New()
		for i := 0; i < nodes; i++ {
			g.AddNode(fmt.Sprintf("n%04d", i))
		}
		for i := 1; i < nodes; i++ {
			_ = g.AddEdge(fmt.Sprintf("n%04d", i-1), fmt.Sprintf("n%04d", i))
		}
		for k := 0; k < extraEdges && k+2 < nodes; k++ {
			_ = g.AddEdge(fmt.Sprintf("n%04d", k), fmt.Sprintf("n%04d", k+2))
		}
		return g
	}
	sizes := [][2]int{{100, 50}, {500, 200}, {1000, 400}}
	for _, sz := range sizes {
		g := mk(sz[0], sz[1])
		if err := g.Validate(); err != nil {
			t.Fatal(err)
		}
		s, tr := runSched(t, g, noopTasks(g), 8, false)
		if !tr.AllDone() {
			t.Fatalf("size %v not all done", sz)
		}
		v, e := sz[0], sz[0]-1+sz[1]
		if s.Decisions() > 4*(v+e) {
			t.Fatalf("size %v: decisions %d > 4*(V+E)=%d", sz, s.Decisions(), 4*(v+e))
		}
	}
}

func TestSchedOrderingAndSkip(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T) map[string]fail.State
		want map[string]string
	}{
		{
			name: "serial concurrency=1 finishes all",
			run: func(t *testing.T) map[string]fail.State {
				g := chainGraph("A", "B", "C")
				_, tr := runSched(t, g, noopTasks(g), 1, false)
				return statusMap(tr)
			},
			want: map[string]string{"A": "success", "B": "success", "C": "success"},
		},
		{
			name: "fail aborts descendants, reason reaches C through B",
			run: func(t *testing.T) map[string]fail.State {
				g := chainGraph("A", "B", "C")
				tasks := noopTasks(g)
				tasks["A"] = func(context.Context) error { return errors.New("boom") }
				_, tr := runSched(t, g, tasks, 4, true)
				return statusMap(tr)
			},
			want: map[string]string{"A": "failed", "B": "skipped", "C": "skipped"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.run(t)
			for id, want := range tc.want {
				if got[id].Status != want {
					t.Fatalf("%s status=%s want %s", id, got[id].Status, want)
				}
			}
			if tc.name[0] == 'f' {
				if got["C"].ReasonID != "A" {
					t.Fatalf("C reason=%q want A", got["C"].ReasonID)
				}
			}
		})
	}
}

// graphABCD: A->B->C, D 独立旁支。
func graphABCD() *graph.Graph {
	g := graph.New()
	for _, n := range []string{"A", "B", "C", "D"} {
		g.AddNode(n)
	}
	_ = g.AddEdge("A", "B")
	_ = g.AddEdge("B", "C")
	return g
}

func runEngine(t *testing.T, g *graph.Graph, tasks exec.TaskMap,
	mode exec.Mode, conc int) []fail.State {
	t.Helper()
	eng := exec.New(g, tasks, mode, conc)
	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return res.States
}

func TestExecStateDistribution(t *testing.T) {
	slowD := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
			return nil
		}
	}
	cases := []struct {
		name   string
		mode   exec.Mode
		want   map[string]string
		reason string
	}{
		{
			name:   "failfast: C skipped-reason-A, independent D canceled",
			mode:   exec.FailFast,
			want:   map[string]string{"A": "failed", "B": "skipped", "C": "skipped", "D": "canceled"},
			reason: "A",
		},
		{
			name: "besteffort: failure branch skipped, D runs to success",
			mode: exec.BestEffort,
			want: map[string]string{"A": "failed", "B": "skipped", "C": "skipped",
				"D": "success"},
			reason: "A",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tasks := exec.TaskMap{
				"A": func(context.Context) error { return errors.New("A boom") },
				"B": func(context.Context) error { return nil },
				"C": func(context.Context) error { return nil },
				"D": slowD,
			}
			states := runEngine(t, graphABCD(), tasks, tc.mode, 8)
			m := map[string]fail.State{}
			for _, st := range states {
				m[st.ID] = st
			}
			for id, want := range tc.want {
				if m[id].Status != want {
					t.Fatalf("%s status=%s want %s (all=%v)",
						id, m[id].Status, want, m)
				}
			}
			if m["C"].ReasonID != tc.reason || m["B"].ReasonID != tc.reason {
				t.Fatalf("reason B=%q C=%q want A", m["B"].ReasonID, m["C"].ReasonID)
			}
			if d := m["D"]; tc.mode == exec.FailFast && (!d.Started || d.Status != "canceled") {
				t.Fatalf("D must be started+canceled, got %+v", d)
			}
		})
	}
}

func TestExecFaultInjection(t *testing.T) {
	cases := []struct {
		name      string
		run       func(*testing.T) map[string]fail.State
		want      map[string]string
		panicInfo string
	}{
		{
			name: "panic captured as failed",
			run: func(t *testing.T) map[string]fail.State {
				g := graph.New()
				g.AddNode("P")
				tasks := exec.TaskMap{"P": func(context.Context) error {
					panic("explode-42")
				}}
				return stateSliceMap(runEngine(t, g, tasks, exec.BestEffort, 2))
			},
			want:      map[string]string{"P": "failed"},
			panicInfo: "explode-42",
		},
		{
			name: "multiple simultaneous failures all recorded",
			run: func(t *testing.T) map[string]fail.State {
				g := graph.New()
				for _, n := range []string{"X", "Y", "Z"} {
					g.AddNode(n)
				}
				_ = g.AddEdge("X", "Z")
				_ = g.AddEdge("Y", "Z")
				barrier := make(chan struct{})
				tasks := exec.TaskMap{
					"X": func(context.Context) error {
						<-barrier
						return errors.New("x-fail")
					},
					"Y": func(context.Context) error {
						<-barrier
						return errors.New("y-fail")
					},
					"Z": func(context.Context) error { return nil },
				}
				go func() {
					time.Sleep(30 * time.Millisecond)
					close(barrier)
				}()
				return stateSliceMap(runEngine(t, g, tasks, exec.BestEffort, 8))
			},
			want: map[string]string{"X": "failed", "Y": "failed", "Z": "skipped"},
		},
		{
			name: "canceled slow task writes late, result discarded",
			run: func(t *testing.T) map[string]fail.State {
				g := graph.New()
				g.AddNode("F")
				g.AddNode("S")
				tasks := exec.TaskMap{
					"F": func(context.Context) error { return errors.New("fast fail") },
					"S": func(ctx context.Context) error {
						<-ctx.Done()
						time.Sleep(20 * time.Millisecond) // 取消后仍继续并“写回”
						return nil
					},
				}
				return stateSliceMap(runEngine(t, g, tasks, exec.FailFast, 8))
			},
			want: map[string]string{"F": "failed", "S": "canceled"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.run(t)
			for id, want := range tc.want {
				if got[id].Status != want {
					t.Fatalf("%s=%s want %s", id, got[id].Status, want)
				}
			}
			if tc.panicInfo != "" {
				err := got["P"].Err
				if !errors.Is(err, fail.ErrTaskPanic) ||
					!strings.Contains(err.Error(), tc.panicInfo) {
					t.Fatalf("panic err=%v, want wrap+%q", err, tc.panicInfo)
				}
			}
			if tc.want["Z"] == "skipped" {
				if r := got["Z"].ReasonID; r != "X" {
					t.Fatalf("Z reason=%q want earliest upstream X", r)
				}
			}
		})
	}
}

func stateSliceMap(states []fail.State) map[string]fail.State {
	m := map[string]fail.State{}
	for _, st := range states {
		m[st.ID] = st
	}
	return m
}

func TestExecNoGoroutineLeak(t *testing.T) {
	cases := []struct {
		name  string
		tasks func() (exec.TaskMap, *graph.Graph)
		mode  exec.Mode
	}{
		{
			name: "all success",
			mode: exec.BestEffort,
			tasks: func() (exec.TaskMap, *graph.Graph) {
				g := graphABCD()
				tasks := exec.TaskMap{}
				for _, id := range g.Nodes() {
					tasks[id] = func(context.Context) error { return nil }
				}
				return tasks, g
			},
		},
		{
			name: "failfast",
			mode: exec.FailFast,
			tasks: func() (exec.TaskMap, *graph.Graph) {
				g := graphABCD()
				return exec.TaskMap{
					"A": func(context.Context) error { return errors.New("x") },
					"B": func(context.Context) error { return nil },
					"C": func(context.Context) error { return nil },
					"D": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
				}, g
			},
		},
		{
			name: "besteffort",
			mode: exec.BestEffort,
			tasks: func() (exec.TaskMap, *graph.Graph) {
				g := graphABCD()
				return exec.TaskMap{
					"A": func(context.Context) error { return errors.New("x") },
					"B": func(context.Context) error { return nil },
					"C": func(context.Context) error { return nil },
					"D": func(context.Context) error { return nil },
				}, g
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := goroutineStableCount()
			for i := 0; i < 3; i++ {
				tasks, g := tc.tasks()
				runEngine(t, g, tasks, tc.mode, 8)
			}
			got := goroutineStableCount()
			if got > base+1 {
				t.Fatalf("goroutines leaked: base=%d now=%d", base, got)
			}
		})
	}
}

func goroutineStableCount() int {
	var prev, cur int
	cur = runtime.NumGoroutine()
	for range 5 {
		time.Sleep(20 * time.Millisecond)
		prev = cur
		cur = runtime.NumGoroutine()
		if cur == prev {
			return cur
		}
	}
	return cur
}

func TestExecDeterministicReport(t *testing.T) {
	edges := [][2]string{{"A", "B"}, {"A", "C"}, {"B", "D"}, {"C", "D"},
		{"B", "E"}, {"D", "F"}, {"E", "F"}, {"A", "G"}}
	type lockedRand struct {
		mu  sync.Mutex
		rng *rand.Rand
	}
	lr := &lockedRand{rng: rand.New(rand.NewSource(1))}
	mkTasks := func(rng *lockedRand) exec.TaskMap {
		tasks := exec.TaskMap{}
		for _, n := range []string{"A", "B", "C", "D", "E", "F", "G"} {
			n := n
			tasks[n] = func(context.Context) error {
				rng.mu.Lock()
				d := time.Duration(rng.rng.Intn(5)) * time.Millisecond
				rng.mu.Unlock()
				time.Sleep(d)
				if n == "G" {
					return errors.New("g fail")
				}
				return nil
			}
		}
		return tasks
	}
	var baseline []fail.State
	for iter := 0; iter < 20; iter++ {
		shuffleRng := rand.New(rand.NewSource(int64(iter + 1)))
		lr.rng = rand.New(rand.NewSource(int64(iter + 100)))
		g := graph.New()
		for _, n := range []string{"A", "B", "C", "D", "E", "F", "G"} {
			g.AddNode(n)
		}
		perm := edges
		if iter > 0 {
			perm = append([][2]string{}, edges...)
			shuffleRng.Shuffle(len(perm), func(i, j int) {
				perm[i], perm[j] = perm[j], perm[i]
			})
		}
		for _, e := range perm {
			if err := g.AddEdge(e[0], e[1]); err != nil {
				t.Fatal(err)
			}
		}
		states := runEngine(t, g, mkTasks(lr), exec.BestEffort, 8)
		if iter == 0 {
			baseline = states
			continue
		}
		if statesDigest(states) != statesDigest(baseline) {
			t.Fatalf("iter %d report differs from baseline:\n%v\n%v",
				iter, states, baseline)
		}
	}
}

func statesDigest(states []fail.State) string {
	var b strings.Builder
	for _, st := range states {
		fmt.Fprintf(&b, "%s|%s|%s|%v;", st.ID, st.Status, st.ReasonID, st.Started)
	}
	return b.String()
}

func TestExecSerialEqualsParallel(t *testing.T) {
	g := graphABCD()
	mk := func() exec.TaskMap {
		return exec.TaskMap{
			"A": func(context.Context) error { return nil },
			"B": func(context.Context) error { return nil },
			"C": func(context.Context) error { return nil },
			"D": func(context.Context) error { return nil },
		}
	}
	s1 := runEngine(t, g, mk(), exec.BestEffort, 1)
	s8 := runEngine(t, g, mk(), exec.BestEffort, 8)
	if statesDigest(s1) != statesDigest(s8) {
		t.Fatalf("serial %v != parallel %v", s1, s8)
	}
}

func TestReportRender(t *testing.T) {
	errBoom := errors.New("boom line\nbreak")
	cases := []struct {
		name   string
		states []fail.State
		want   string
	}{
		{
			name: "sorted with reasons and started flag",
			states: []fail.State{
				{ID: "C", Status: fail.StatusSkipped, ReasonID: "A"},
				{ID: "A", Status: fail.StatusFailed, Err: errBoom},
				{ID: "B", Status: fail.StatusSkipped, ReasonID: "A"},
				{ID: "D", Status: fail.StatusCanceled, Started: true},
				{ID: "E", Status: fail.StatusSuccess},
			},
			want: "A\tfailed\terr=boom line break\n" +
				"B\tskipped\treason=A\n" +
				"C\tskipped\treason=A\n" +
				"D\tcanceled\tstarted=true\n" +
				"E\tsuccess\n",
		},
		{
			name:   "empty states render empty report",
			states: nil,
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := report.Render(tc.states)
			if got != tc.want {
				t.Fatalf("report mismatch:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

func TestReportByteEqualAcrossRuns(t *testing.T) {
	// 复用 A→B→C + 慢 D 的 failfast 场景，跑 20 次，报告必须逐字节相同。
	for iter := 0; iter < 20; iter++ {
		tasks := exec.TaskMap{
			"A": func(context.Context) error { return errors.New("boom") },
			"B": func(context.Context) error { return nil },
			"C": func(context.Context) error { return nil },
			"D": func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		}
		states := runEngine(t, graphABCD(), tasks, exec.FailFast, 8)
		got := report.Render(states)
		want := "A\tfailed\terr=boom\n" +
			"B\tskipped\treason=A\n" +
			"C\tskipped\treason=A\n" +
			"D\tcanceled\tstarted=true\n"
		if got != want {
			t.Fatalf("iter %d:\n%q\nwant:\n%q", iter, got, want)
		}
	}
}

func TestGraphBuild(t *testing.T) {
	cases := []struct {
		name  string
		build func() (*graph.Graph, error)
		check func(*testing.T, *graph.Graph, error)
	}{
		{
			name:  "empty graph",
			build: func() (*graph.Graph, error) { return graph.New(), nil },
			check: func(t *testing.T, g *graph.Graph, _ error) {
				if len(g.Nodes()) != 0 {
					t.Fatalf("empty graph should have no nodes, got %v", g.Nodes())
				}
			},
		},
		{
			name: "dup edge idempotent",
			build: func() (*graph.Graph, error) {
				g := graph.New()
				g.AddNode("a")
				g.AddNode("b")
				if err := g.AddEdge("a", "b"); err != nil {
					return nil, err
				}
				return g, g.AddEdge("a", "b")
			},
			check: func(t *testing.T, g *graph.Graph, _ error) {
				if succ := g.Succs("a"); len(succ) != 1 || succ[0] != "b" {
					t.Fatalf("dup edge should be idempotent, got %v", succ)
				}
			},
		},
		{
			name: "missing dependency node",
			build: func() (*graph.Graph, error) {
				g := graph.New()
				g.AddNode("a")
				return g, g.AddEdge("a", "ghost")
			},
			check: func(t *testing.T, _ *graph.Graph, err error) {
				if !errors.Is(err, graph.ErrNodeNotFound) {
					t.Fatalf("want ErrNodeNotFound, got %v", err)
				}
			},
		},
		{
			name: "nodes sorted",
			build: func() (*graph.Graph, error) {
				g := graph.New()
				for _, id := range []string{"c", "a", "b"} {
					g.AddNode(id)
				}
				return g, nil
			},
			check: func(t *testing.T, g *graph.Graph, _ error) {
				got := g.Nodes()
				want := []string{"a", "b", "c"}
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Fatalf("nodes = %v, want %v", got, want)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := tc.build()
			tc.check(t, g, err)
		})
	}
}

func TestGraphCycles(t *testing.T) {
	type edge struct{ from, to string }
	cases := []struct {
		name  string
		nodes []string
		edges []edge
		cycle bool
	}{
		{"self loop", []string{"a"}, []edge{{"a", "a"}}, true},
		{"two cycle", []string{"a", "b"}, []edge{{"a", "b"}, {"b", "a"}}, true},
		{"three cycle with tail", []string{"s", "a", "b", "c"},
			[]edge{{"s", "a"}, {"a", "b"}, {"b", "c"}, {"c", "a"}}, true},
		{"dag is clean", []string{"a", "b", "c"},
			[]edge{{"a", "b"}, {"b", "c"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := graph.New()
			for _, n := range tc.nodes {
				g.AddNode(n)
			}
			for _, e := range tc.edges {
				if err := g.AddEdge(e.from, e.to); err != nil {
					t.Fatal(err)
				}
			}
			err := g.Validate()
			if tc.cycle != (err != nil) {
				t.Fatalf("cycle=%v, err=%v", tc.cycle, err)
			}
			if !tc.cycle {
				return
			}
			var ce *graph.CycleError
			if !errors.As(err, &ce) {
				t.Fatalf("want *CycleError, got %T %v", err, err)
			}
			p := ce.Path
			if len(p) < 2 || p[0] != p[len(p)-1] {
				t.Fatalf("cycle path must be closed, got %v", p)
			}
			for i := 0; i+1 < len(p); i++ {
				if !g.HasEdge(p[i], p[i+1]) {
					t.Fatalf("cycle hop %q->%q is not a real edge; path=%v",
						p[i], p[i+1], p)
				}
			}
		})
	}
}

func TestGraphLayers(t *testing.T) {
	cases := []struct {
		name       string
		build      func() *graph.Graph
		wantLayers [][]string
	}{
		{
			name: "diamond",
			build: func() *graph.Graph {
				g := graph.New()
				for _, n := range []string{"a", "b", "c", "d"} {
					g.AddNode(n)
				}
				_ = g.AddEdge("a", "b")
				_ = g.AddEdge("a", "c")
				_ = g.AddEdge("b", "d")
				_ = g.AddEdge("c", "d")
				return g
			},
			wantLayers: [][]string{{"a"}, {"b", "c"}, {"d"}},
		},
		{
			name: "chain 1000 no stack overflow",
			build: func() *graph.Graph {
				g := graph.New()
				for i := 0; i < 1000; i++ {
					g.AddNode(fmt.Sprintf("t%04d", i))
				}
				for i := 1; i < 1000; i++ {
					_ = g.AddEdge(fmt.Sprintf("t%04d", i-1), fmt.Sprintf("t%04d", i))
				}
				return g
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.build()
			if err := g.Validate(); err != nil {
				t.Fatal(err)
			}
			layers := g.Layers()
			if tc.wantLayers != nil {
				if fmt.Sprint(layers) != fmt.Sprint(tc.wantLayers) {
					t.Fatalf("layers=%v want %v", layers, tc.wantLayers)
				}
				return
			}
			if len(layers) != 1000 || len(layers[0]) != 1 ||
				layers[0][0] != "t0000" || layers[999][0] != "t0999" {
				t.Fatalf("1000-chain layering wrong: %d layers", len(layers))
			}
		})
	}
}
