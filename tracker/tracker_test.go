package tracker_test

import (
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/promote"
	"ontology/tracker"
)

func mustNew(t *testing.T, primary string, replicas ...string) *tracker.Group {
	t.Helper()
	g, err := tracker.New(primary, replicas)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestNewValidation(t *testing.T) {
	longName := string(make([]byte, 65))
	bad := []struct {
		primary  string
		replicas []string
	}{
		{"", []string{"R"}},
		{"P", []string{longName}},
		{"P", []string{"P"}},
		{"P", []string{"a", "a"}},
	}
	for i, tc := range bad {
		if _, err := tracker.New(tc.primary, tc.replicas); !errors.Is(err, tracker.ErrInvalidArg) {
			t.Fatalf("case %d: got %v want ErrInvalidArg", i, err)
		}
	}
	reps := make([]string, 16)
	for i := range reps {
		reps[i] = strconv.Itoa(i)
	}
	if _, err := tracker.New("P", reps); !errors.Is(err, tracker.ErrInvalidArg) {
		t.Fatalf("17 members: got %v want ErrInvalidArg", err)
	}
}

// 题目主例：确认先于 gcp；FailReplica 后 gcp 跳升与补确认次序。
func TestExampleFailBranch(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		if _, err := g.Write("x"); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range [][2]int{
		{1, 1}, {1, 3}, {2, 1}, {2, 2}, {2, 3},
	} {
		name := []string{"P", "R1", "R2"}[a[0]]
		if err := g.Ack(name, a[1], 1); err != nil {
			t.Fatalf("Ack(%s,%d): %v", name, a[1], err)
		}
	}
	if l, _ := g.LCP("R1"); l != 1 {
		t.Fatalf("R1 lcp=%d want 1", l)
	}
	if l, _ := g.LCP("R2"); l != 3 {
		t.Fatalf("R2 lcp=%d want 3", l)
	}
	if l, _ := g.LCP("P"); l != 3 {
		t.Fatalf("P lcp=%d want 3", l)
	}
	if g.GCP() != 1 {
		t.Fatalf("gcp=%d want 1", g.GCP())
	}
	if got := g.Confirmed(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("Confirmed=%v want [1 3]", got)
	}

	if err := g.FailReplica("R1"); err != nil {
		t.Fatal(err)
	}
	if g.GCP() != 3 {
		t.Fatalf("after fail gcp=%d want 3", g.GCP())
	}
	if got := g.Confirmed(); !reflect.DeepEqual(got, []int{1, 3, 2}) {
		t.Fatalf("after fail Confirmed=%v want [1 3 2]", got)
	}
}

// 题目主例分支乙：Promote(R1) 补洞、R2 回滚重取、Lost、旧任期拒绝。
func TestExamplePromoteBranch(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		if _, err := g.Write("x"); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range [][2]int{{1, 1}, {1, 3}, {2, 1}, {2, 2}, {2, 3}} {
		name := []string{"P", "R1", "R2"}[a[0]]
		if err := g.Ack(name, a[1], 1); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := promote.Promote(g, "R1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.G != 1 || plan.M != 3 ||
		!reflect.DeepEqual(plan.Fill, []int{2}) ||
		!reflect.DeepEqual(plan.Lost, []int{2}) {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if g.Term() != 2 {
		t.Fatalf("term=%d want 2", g.Term())
	}
	if got := g.Lost(); !reflect.DeepEqual(got, []int{2}) {
		t.Fatalf("Lost=%v want [2]", got)
	}
	if g.GCP() != 3 {
		t.Fatalf("gcp=%d want 3", g.GCP())
	}
	if got := g.Confirmed(); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("Confirmed=%v want [1 3] (noop excluded)", got)
	}
	hist, err := g.History("R2")
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.Op{
		{Seq: 1, Term: 1, Body: "x"},
		{Seq: 2, Term: 2, Noop: true},
		{Seq: 3, Term: 1, Body: "x"},
	}
	if !reflect.DeepEqual(hist, want) {
		t.Fatalf("R2 history=%+v want %+v", hist, want)
	}
	if seq, err := g.Write("y"); err != nil || seq != 4 {
		t.Fatalf("Write after promote: seq=%d err=%v want 4", seq, err)
	}
	if err := g.Ack("R2", 3, 1); !errors.Is(err, tracker.ErrStaleTerm) {
		t.Fatalf("old-term ack: got %v want ErrStaleTerm", err)
	}
	if err := g.Ack("P", 3, 2); !errors.Is(err, tracker.ErrMemberNotFound) {
		t.Fatalf("ack removed primary: got %v want ErrMemberNotFound", err)
	}
}

// 追赶副本并入条件：lcp 够但缺已确认操作仍不算追上。
func TestCatchupJoinCondition(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		if _, err := g.Write("x"); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []int{1, 3} {
		if err := g.Ack("R1", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	for seq := 1; seq <= 3; seq++ {
		if err := g.Ack("R2", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddReplica("R3"); err != nil {
		t.Fatal(err)
	}
	if err := g.Ack("R3", 1, 1); err != nil {
		t.Fatal(err)
	}
	if l, _ := g.LCP("R3"); l != 1 || g.GCP() != 1 {
		t.Fatalf("precondition lcp=%d gcp=%d", l, g.GCP())
	}
	if err := g.MarkInSync("R3"); !errors.Is(err, tracker.ErrNotCaughtUp) {
		t.Fatalf("MarkInSync missing confirmed seq 3: got %v want ErrNotCaughtUp", err)
	}
	if err := g.Ack("R3", 3, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkInSync("R3"); err != nil {
		t.Fatalf("MarkInSync after ack 3: %v", err)
	}
	if g.GCP() != 1 {
		t.Fatalf("gcp=%d want 1", g.GCP())
	}
	if r, _ := g.RoleOf("R3"); r != tracker.RoleSync {
		t.Fatalf("R3 role=%d want RoleSync", r)
	}
}

func TestRejectOrdering(t *testing.T) {
	g := mustNew(t, "P", "R1")
	if _, err := g.Write("x"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"ack seq 0", func() error { return g.Ack("R1", 0, 1) }, tracker.ErrInvalidArg},
		{"ack seq beyond max", func() error { return g.Ack("R1", 9, 1) }, tracker.ErrInvalidArg},
		{"ack term above current", func() error { return g.Ack("R1", 1, 9) }, tracker.ErrInvalidArg},
		{"ack beyond max on unknown member", func() error { return g.Ack("X", 9, 1) }, tracker.ErrInvalidArg},
		{"ack unknown member", func() error { return g.Ack("X", 1, 1) }, tracker.ErrMemberNotFound},
		{"ack primary", func() error { return g.Ack("P", 1, 1) }, tracker.ErrWrongState},
		{"ack bad name", func() error { return g.Ack("", 1, 1) }, tracker.ErrInvalidArg},
		{"add bad name", func() error { return g.AddReplica("") }, tracker.ErrInvalidArg},
		{"add existing", func() error { return g.AddReplica("R1") }, tracker.ErrMemberExists},
		{"marksync unknown", func() error { return g.MarkInSync("X") }, tracker.ErrMemberNotFound},
		{"marksync non-catchup", func() error { return g.MarkInSync("R1") }, tracker.ErrWrongState},
		{"fail unknown", func() error { return g.FailReplica("X") }, tracker.ErrMemberNotFound},
		{"fail primary", func() error { return g.FailReplica("P") }, tracker.ErrWrongState},
		{"promote unknown", func() error {
			_, err := promote.Promote(g, "X")
			return err
		}, tracker.ErrMemberNotFound},
		{"promote primary", func() error {
			_, err := promote.Promote(g, "P")
			return err
		}, tracker.ErrWrongState},
	}
	for _, tc := range cases {
		if err := tc.call(); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, err, tc.want)
		}
	}
	if g.GCP() != 0 || g.Term() != 1 {
		t.Fatalf("state mutated by rejected calls: gcp=%d term=%d", g.GCP(), g.Term())
	}
}

func TestDuplicateAckIdempotent(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	if _, err := g.Write("x"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := g.Ack("R1", 1, 1); err != nil {
			t.Fatalf("repeat ack %d: %v", i, err)
		}
	}
	if got := g.Confirmed(); len(got) != 0 {
		t.Fatalf("Confirmed=%v want none (R2 pending)", got)
	}
	if err := g.Ack("R2", 1, 1); err != nil {
		t.Fatal(err)
	}
	if got := g.Confirmed(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("Confirmed=%v want [1]", got)
	}
}

func TestConcurrentAcks(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	const n = 200
	for i := 0; i < n; i++ {
		if _, err := g.Write("b"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, name := range []string{"R1", "R2"} {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			for seq := n; seq >= 1; seq-- {
				if err := g.Ack(who, seq, 1); err != nil {
					t.Errorf("Ack(%s,%d): %v", who, seq, err)
					return
				}
			}
		}(name)
	}
	wg.Wait()
	if g.GCP() != n {
		t.Fatalf("gcp=%d want %d", g.GCP(), n)
	}
	if len(g.Confirmed()) != n {
		t.Fatalf("confirmed=%d want %d", len(g.Confirmed()), n)
	}
}

// gcp 只增不减：移除落后成员后跳升；同步集合内历史在 gcp 以下逐条相同。
func TestGCPMonotonicAndHistoryAgreement(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2", "R3")
	for i := 0; i < 6; i++ {
		if _, err := g.Write("z"); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []int{1, 2, 3} {
		if err := g.Ack("R1", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []int{1, 2, 3, 4} {
		if err := g.Ack("R2", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []int{1, 2} {
		if err := g.Ack("R3", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	if g.GCP() != 2 {
		t.Fatalf("gcp=%d want 2", g.GCP())
	}
	if err := g.FailReplica("R3"); err != nil {
		t.Fatal(err)
	}
	if g.GCP() != 3 {
		t.Fatalf("gcp after fail R3=%d want 3", g.GCP())
	}
	if err := g.FailReplica("R1"); err != nil {
		t.Fatal(err)
	}
	if g.GCP() != 4 {
		t.Fatalf("gcp after fail R1=%d want 4", g.GCP())
	}
	hP, _ := g.History("P")
	hR2, _ := g.History("R2")
	if !reflect.DeepEqual(hP[:4], hR2[:4]) {
		t.Fatalf("histories disagree below gcp:\nP:  %+v\nR2: %+v", hP[:4], hR2[:4])
	}
}

// Lost 与 Confirmed 不相交：晋升丢弃的只能是未确认序号。
func TestLostConfirmedDisjoint(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 5; i++ {
		if _, err := g.Write("z"); err != nil {
			t.Fatal(err)
		}
	}
	// seq 1 全员确认；R1 只处理 1、4，故晋升后 M=4，洞 2,3 填 noop，seq 5 丢失。
	for _, seq := range []int{1, 4} {
		if err := g.Ack("R1", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	for seq := 1; seq <= 5; seq++ {
		if err := g.Ack("R2", seq, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	lost := map[int]bool{}
	for _, seq := range g.Lost() {
		lost[seq] = true
	}
	for _, seq := range g.Confirmed() {
		if lost[seq] {
			t.Fatalf("seq %d is both confirmed and lost", seq)
		}
	}
	if !lost[2] || !lost[3] || !lost[5] {
		t.Fatalf("Lost=%v want 2,3,5", g.Lost())
	}
	if lost[1] || lost[4] {
		t.Fatalf("seq 1,4 must survive, Lost=%v", g.Lost())
	}
}

// 任期过期：晋升后旧任期 Ack 被拒，且不改状态。
func TestStaleTermAfterPromote(t *testing.T) {
	g := mustNew(t, "P", "R1")
	for i := 0; i < 2; i++ {
		if _, err := g.Write("z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.Ack("R1", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Ack("R1", 2, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := promote.Promote(g, "R1"); err != nil {
		t.Fatal(err)
	}
	// 新主为 R1；加入追赶副本后旧任期 ack 仍应报过期。
	if err := g.AddReplica("C"); err != nil {
		t.Fatal(err)
	}
	err := g.Ack("C", 1, 1)
	if !errors.Is(err, tracker.ErrStaleTerm) {
		t.Fatalf("got %v want ErrStaleTerm", err)
	}
	if l, _ := g.LCP("C"); l != 0 {
		t.Fatalf("stale ack changed state: lcp=%d", l)
	}
	// 新任期可正常处理。
	if err := g.Ack("C", 1, 2); err != nil {
		t.Fatalf("current-term ack: %v", err)
	}
}
