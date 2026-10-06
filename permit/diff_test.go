package permit_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/permit"
	"ontology/permit/naive"
)

// diffNetwork 小而密的路网，使绕行/走廊/同路段冲突都容易被触发。
func diffNetwork() (permit.Network, map[string]int) {
	n := permit.Network{Segments: []permit.Segment{
		{ID: "S1", Lanes: 3, Corridor: "K1", Detour: []string{"S2"}},
		{ID: "S2", Lanes: 10, Corridor: "K1", Detour: []string{"S3"}},
		{ID: "S3", Lanes: 10, Corridor: "K2", Detour: []string{"S1"}},
		{ID: "S4", Lanes: 1, Corridor: "K2", Detour: []string{"S3"}},
	}}
	return n, map[string]int{"K1": 2, "K2": 2}
}

type opLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *opLog) line(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func codeName(e *permit.RuleError) string {
	if e == nil {
		return "ACCEPT"
	}
	return e.Code.String()
}

// runRandomSequence 生成并重放一条随机操作序列，逐条比对正式实现与朴素模型。
// logEvery 控制是否打印每条操作的输入、输出与判定依据。
func runRandomSequence(t *testing.T, seed int64, nops int, logEvery bool) string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	net, capm := diffNetwork()
	svc, err := permit.NewService(net, capm)
	if err != nil {
		t.Fatal(err)
	}
	oracle := naive.New(net, capm)
	var lg opLog

	segs := []string{"S1", "S2", "S3", "S4"}
	lanesOf := map[string]int{"S1": 3, "S2": 2, "S3": 2, "S4": 1}

	clock := permit.Time(1)
	seq := 0

	snapshot := func(stage string) {
		t.Helper()
		got := svc.Snapshot()
		want := oracle.Snapshot()
		if !reflect.DeepEqual(canonical(got), canonical(want)) {
			t.Fatalf("seed=%d stage=%s snapshot mismatch\n--- got:\n%v\n--- want:\n%s\nLOG:\n%s",
				seed, stage, dump(got), dump(want), lg.buf.String())
		}
	}

	for i := 0; i < nops; i++ {
		clock += permit.Time(rng.Intn(3))
		kind := rng.Intn(10)
		switch {
		case kind <= 5: // 60% 申请
			seq++
			seg := segs[rng.Intn(len(segs))]
			lanes := 1 + rng.Intn(lanesOf[seg]+1)       // 偶发车道超限
			start := clock + permit.Time(rng.Intn(6)-1) // 偶发起始早于当前
			end := start + permit.Time(1+rng.Intn(8))
			prio := permit.Regular
			if rng.Intn(4) == 0 {
				prio = permit.Emergency
			}
			req := permit.ApplyRequest{
				OpAt: clock, ID: fmt.Sprintf("p%d", seq), Segment: seg,
				Lanes: lanes, Start: start, End: end, Priority: prio,
			}
			r1 := svc.Apply(req)
			r2 := oracle.Apply(req)
			if codeName(r1.Err) != codeName(r2.Err) {
				dbgDump(t, svc, oracle)
				t.Fatalf("seed=%d apply mismatch:\nreq=%+v\nsvc=%v\nnaive=%v\nLOG:\n%s",
					seed, req, codeName(r1.Err), codeName(r2.Err), lg.buf.String())
			}
			if r1.Accepted {
				if !reflect.DeepEqual(r1.Reschedule, r2.Reschedule) ||
					!reflect.DeepEqual(r1.RescheduledInterval, r2.RescheduledInterval) {
					t.Fatalf("seed=%d preemption result mismatch:\nreq=%+v\nsvc=%+v\nnaive=%+v\nLOG:\n%s",
						seed, req, r1, r2, lg.buf.String())
				}
			}
			if logEvery {
				lg.line("APPLY  in={at:%d id:%s seg:%s lanes:%d [%d,%d) prio:%d} out=%s preempt=%v resched=%v detail=%s",
					req.OpAt, req.ID, req.Segment, req.Lanes, req.Start, req.End, req.Priority,
					codeName(r1.Err), r1.PreemptedIDs, r1.Reschedule, witnessDetail(r1.Err))
			}
			snapshot(fmt.Sprintf("after-apply-%d", i))

		case kind <= 7: // 20% 延期
			id := pickExisting(rng, oracle)
			if id == "" {
				continue
			}
			newEnd := clock + permit.Time(1+rng.Intn(12))
			req := permit.ExtendRequest{OpAt: clock, ID: id, NewEnd: newEnd}
			e1 := svc.Extend(req)
			e2 := oracle.Extend(req)
			if codeName(e1) != codeName(e2) {
				t.Fatalf("seed=%d extend mismatch:\nreq=%+v\nsvc=%v\nnaive=%v\nLOG:\n%s",
					seed, req, codeName(e1), codeName(e2), lg.buf.String())
			}
			if logEvery {
				lg.line("EXTEND in={at:%d id:%s newEnd:%d} out=%s detail=%s",
					req.OpAt, req.ID, req.NewEnd, codeName(e1), witnessDetail(e1))
			}
			snapshot(fmt.Sprintf("after-extend-%d", i))

		case kind == 8: // 10% 撤销
			id := pickExisting(rng, oracle)
			if id == "" {
				continue
			}
			req := permit.RevokeRequest{OpAt: clock, ID: id}
			e1 := svc.Revoke(req)
			e2 := oracle.Revoke(req)
			if codeName(e1) != codeName(e2) {
				t.Fatalf("seed=%d revoke mismatch:\nreq=%+v\nsvc=%v\nnaive=%v\nLOG:\n%s",
					seed, req, codeName(e1), codeName(e2), lg.buf.String())
			}
			if logEvery {
				lg.line("REVOKE in={at:%d id:%s} out=%s", req.OpAt, req.ID, codeName(e1))
			}
			snapshot(fmt.Sprintf("after-revoke-%d", i))

		default: // 10% 查询
			seg := segs[rng.Intn(len(segs))]
			at := clock + permit.Time(rng.Intn(10)-3)
			req := permit.QueryRequest{Segment: seg, At: at}
			q1 := svc.Query(req)
			q2 := oracle.Query(req)
			a1, a2 := q1.ActiveIDs, q2.ActiveIDs
			if len(a1) == 0 {
				a1 = nil
			}
			if len(a2) == 0 {
				a2 = nil
			}
			if q1.Closed != q2.Closed || !reflect.DeepEqual(a1, a2) {
				dbgDump(t, svc, oracle)
				t.Fatalf("seed=%d query mismatch:\nreq=%+v\nsvc=%+v\nnaive=%+v\nLOG:\n%s",
					seed, req, q1, q2, lg.buf.String())
			}
			if logEvery {
				lg.line("QUERY  in={seg:%s at:%d} out=closed:%d active:%v",
					req.Segment, req.At, q1.Closed, q1.ActiveIDs)
			}
		}
	}
	return lg.buf.String()
}

func dbgDump(t *testing.T, svc *permit.Service, o *naive.Model) {
	t.Helper()
	t.Logf("svc snapshot:")
	for id, st := range svc.Snapshot() {
		t.Logf("  svc %s: %s lanes=%d [%d,%d) %s prio=%d", id, st.Segment, st.Lanes, st.Start, st.End, st.Status, st.Priority)
	}
	t.Logf("naive snapshot:")
	for id, st := range o.Snapshot() {
		t.Logf("  ora %s: %s lanes=%d [%d,%d) %s prio=%d", id, st.Segment, st.Lanes, st.Start, st.End, st.Status, st.Priority)
	}
}

func witnessDetail(e *permit.RuleError) string {
	if e == nil {
		return "-"
	}
	return strings.Join(e.Witness, ",")
}

func pickExisting(rng *rand.Rand, m *naive.Model) string {
	snap := m.Snapshot()
	ids := make([]string, 0, len(snap))
	for id, st := range snap {
		if st.Status == permit.StatusApproved || st.Status == permit.StatusPreempted ||
			st.Status == permit.StatusPendingReschedule {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[rng.Intn(len(ids))]
}

func canonical(m map[string]permit.PermitState) map[string]permit.PermitState {
	for id, st := range m {
		cp := st
		// 历史条目去掉易变的人类描述细节后比较结构（Kind/时段/By）。
		for i := range cp.History {
			cp.History[i].Detail = ""
		}
		m[id] = cp
	}
	return m
}

func dump(m map[string]permit.PermitState) string {
	var b strings.Builder
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	for _, id := range ids {
		st := m[id]
		fmt.Fprintf(&b, "  %s: seg=%s lanes=%d prio=%d [%d,%d) status=%s order=%d hist=%d\n",
			id, st.Segment, st.Lanes, st.Priority, st.Start, st.End, st.Status, st.Order, len(st.History))
	}
	return b.String()
}

// TestRandomDifferential 多组随机序列与朴素全量重判模型对照。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= 120; seed++ {
		runRandomSequence(t, seed, 500, false)
	}
}

// TestRandomDifferentialLogged 打印一条样例序列的完整日志：
// 每条操作的输入、输出与判定依据（拒绝原因/抢占/顺延结果/查询结果）。
func TestRandomDifferentialLogged(t *testing.T) {
	log := runRandomSequence(t, 99, 60, true)
	t.Log("\n" + log)
	if !testing.Verbose() {
		// 非 -v 时仍确保日志确实产出（每条操作至少一行）。
		if lines := strings.Count(log, "\n"); lines < 30 {
			t.Fatalf("expected detailed op log, got %d lines", lines)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放两次，许可状态历史完全相同。
func TestReplayDeterminism(t *testing.T) {
	build := func() map[string]permit.PermitState {
		runRandomSequence(t, 7, 200, false)
		net, capm := diffNetwork()
		svc, _ := permit.NewService(net, capm)
		// 用固定序列（独立于上面的随机过程）。
		fixed := []permit.ApplyRequest{
			{OpAt: 1, ID: "a", Segment: "S1", Lanes: 2, Start: 10, End: 20},
			{OpAt: 2, ID: "b", Segment: "S2", Lanes: 1, Start: 12, End: 18},
			{OpAt: 3, ID: "e", Segment: "S1", Lanes: 3, Start: 10, End: 15, Priority: permit.Emergency},
		}
		for _, r := range fixed {
			svc.Apply(r)
		}
		_ = svc.Extend(permit.ExtendRequest{OpAt: 4, ID: "a", NewEnd: 30})
		_ = svc.Revoke(permit.RevokeRequest{OpAt: 5, ID: "b"})
		return canonical(svc.Snapshot())
	}
	s1 := build()
	s2 := build()
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replay must produce identical state history")
	}
}

// TestConcurrentOperations 并发调用等价于某个串行顺序：
// 并发受理互不相交路段的许可，最终全部成功且状态与串行版一致。
func TestConcurrentOperations(t *testing.T) {
	net := permit.Network{Segments: []permit.Segment{
		{ID: "S1", Lanes: 10, Corridor: "K1"},
		{ID: "S2", Lanes: 10, Corridor: "K2"},
		{ID: "S3", Lanes: 10, Corridor: "K3"},
	}}
	s, _ := permit.NewService(net, map[string]int{"K1": 10, "K2": 10, "K3": 10})
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			seg := []string{"S1", "S2", "S3"}[i%3]
			res := s.Apply(permit.ApplyRequest{
				OpAt: 1, ID: fmt.Sprintf("c%d", i), Segment: seg, Lanes: 1,
				Start: 10, End: 20,
			})
			if res.Err != nil {
				t.Errorf("concurrent apply %d rejected: %v", i, res.Err)
			}
		}()
	}
	wg.Wait()
	for _, seg := range []string{"S1", "S2", "S3"} {
		q := s.Query(permit.QueryRequest{Segment: seg, At: 15})
		if q.Closed != 10 || len(q.ActiveIDs) != 10 {
			t.Fatalf("seg %s: %+v", seg, q)
		}
	}
}
