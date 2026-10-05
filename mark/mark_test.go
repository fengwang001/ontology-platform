package mark_test

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/backfill"
	"ontology/mark"
)

// step 是表驱动场景的一步操作。wantErr 为哨兵错误（nil 表示成功），
// wantJob 校验错误携带的作业名细节；每步结束后校验 W、S、Ver 与确认值。
type step struct {
	op       string // commit/begin/stage/heartbeat/finish/abort/ack
	name     string // 作业名或消费者名
	a, b     int
	ttl      int
	p        int
	upto     int
	now      int
	wantErr  error
	wantJob  string
	wantPart int
	hasPart  bool
	wantVer  map[int]int
	wantAck  map[string]int
	wantRev  []mark.Revision
	wantW    int
	wantS    int
}

func runSteps(t *testing.T, s *mark.System, steps []step) {
	t.Helper()
	for i, st := range steps {
		var err error
		var revs []mark.Revision
		switch st.op {
		case "commit":
			err = s.Commit(st.p, st.now)
		case "begin":
			err = s.Begin(st.name, st.a, st.b, st.ttl, st.now)
		case "stage":
			err = s.Stage(st.name, st.p, st.now)
		case "heartbeat":
			err = s.Heartbeat(st.name, st.now)
		case "finish":
			revs, err = s.Finish(st.name, st.now)
		case "abort":
			err = s.Abort(st.name, st.now)
		case "ack":
			err = s.Ack(st.name, st.upto, st.now)
		default:
			t.Fatalf("step %d: unknown op %q", i, st.op)
		}
		if st.wantErr == nil && err != nil {
			t.Fatalf("step %d (%s): err = %v, want nil", i, st.op, err)
		}
		if st.wantErr != nil && !errors.Is(err, st.wantErr) {
			t.Fatalf("step %d (%s): err = %v, want %v", i, st.op, err, st.wantErr)
		}
		if st.wantJob != "" {
			var be *backfill.Error
			if !errors.As(err, &be) || be.Job != st.wantJob {
				t.Fatalf("step %d (%s): err = %v, want detail job %q", i, st.op, err, st.wantJob)
			}
		}
		if st.hasPart {
			var be *backfill.Error
			if !errors.As(err, &be) || be.Part != st.wantPart {
				t.Fatalf("step %d (%s): err = %v, want detail part %d", i, st.op, err, st.wantPart)
			}
		}
		if st.op == "finish" && err == nil && !reflect.DeepEqual(revs, st.wantRev) {
			t.Fatalf("step %d (finish): revs = %v, want %v", i, revs, st.wantRev)
		}
		if got := s.W(); got != st.wantW {
			t.Fatalf("step %d (%s): W = %d, want %d", i, st.op, got, st.wantW)
		}
		if got := s.S(); got != st.wantS {
			t.Fatalf("step %d (%s): S = %d, want %d", i, st.op, got, st.wantS)
		}
		for p, v := range st.wantVer {
			if got := s.Ver(p); got != v {
				t.Fatalf("step %d (%s): Ver(%d) = %d, want %d", i, st.op, p, got, v)
			}
		}
		for c, v := range st.wantAck {
			if got := s.Acked(c); got != v {
				t.Fatalf("step %d (%s): Acked(%s) = %d, want %d", i, st.op, c, got, v)
			}
		}
	}
}

// spec 示例逐步走查：t=1..17 的完整场景。
func TestSpecWalkthrough(t *testing.T) {
	s := mark.New(4)
	var steps []step
	for p := 0; p <= 5; p++ { // t=1..6: Commit(0)..Commit(5)
		steps = append(steps, step{op: "commit", p: p, now: p + 1, wantW: p, wantS: p})
	}
	steps = append(steps,
		step{op: "commit", p: 7, now: 7, wantW: 5, wantS: 5}, // t=7: 6 缺失，W 不动
		step{op: "ack", name: "c", upto: 5, now: 8, wantW: 5, wantS: 5, wantAck: map[string]int{"c": 5}},
		step{op: "begin", name: "j1", a: 2, b: 3, ttl: 20, now: 10, wantW: 5, wantS: 1}, // S 回退到 1
		step{op: "commit", p: 6, now: 11, wantW: 7, wantS: 1},                           // W 推进到 7
		step{op: "begin", name: "j2", a: 3, b: 8, ttl: 5, now: 12, wantErr: mark.ErrOverlap, wantJob: "j1", wantW: 7, wantS: 1},
		step{op: "begin", name: "j2", a: 8, b: 9, ttl: 5, now: 12, wantW: 7, wantS: 1},
		step{op: "commit", p: 8, now: 13, wantErr: mark.ErrHeld, wantJob: "j2", wantW: 7, wantS: 1},
		step{op: "stage", name: "j1", p: 2, now: 13, wantW: 7, wantS: 1},
		step{op: "finish", name: "j1", now: 13, wantErr: mark.ErrIncomplete, wantJob: "j1", hasPart: true, wantPart: 3, wantW: 7, wantS: 1},
		step{op: "stage", name: "j1", p: 3, now: 14, wantW: 7, wantS: 1},
		step{op: "finish", name: "j1", now: 14, wantW: 7, wantS: 7,
			wantVer: map[int]int{2: 2, 3: 2},
			wantRev: []mark.Revision{{Consumer: "c", Parts: []int{2, 3}}}},
		step{op: "commit", p: 8, now: 16, wantErr: mark.ErrHeld, wantJob: "j2", wantW: 7, wantS: 7},
		step{op: "commit", p: 8, now: 17, wantW: 8, wantS: 8, wantVer: map[int]int{8: 1}}, // j2 恰在 17 过期并落地
	)
	runSteps(t, s, steps)
}

// spec 示例变体：t=16 先 Heartbeat(j2)，deadline 变为 21，t=17 仍 ErrHeld。
func TestSpecWalkthroughHeartbeatVariant(t *testing.T) {
	s := mark.New(4)
	var steps []step
	for p := 0; p <= 5; p++ {
		steps = append(steps, step{op: "commit", p: p, now: p + 1, wantW: p, wantS: p})
	}
	steps = append(steps,
		step{op: "commit", p: 7, now: 7, wantW: 5, wantS: 5},
		step{op: "ack", name: "c", upto: 5, now: 8, wantW: 5, wantS: 5},
		step{op: "begin", name: "j1", a: 2, b: 3, ttl: 20, now: 10, wantW: 5, wantS: 1},
		step{op: "commit", p: 6, now: 11, wantW: 7, wantS: 1},
		step{op: "begin", name: "j2", a: 8, b: 9, ttl: 5, now: 12, wantW: 7, wantS: 1},
		step{op: "stage", name: "j1", p: 2, now: 13, wantW: 7, wantS: 1},
		step{op: "stage", name: "j1", p: 3, now: 14, wantW: 7, wantS: 1},
		step{op: "finish", name: "j1", now: 14, wantW: 7, wantS: 7,
			wantRev: []mark.Revision{{Consumer: "c", Parts: []int{2, 3}}}},
		step{op: "heartbeat", name: "j2", now: 16, wantW: 7, wantS: 7}, // deadline 变为 21
		step{op: "commit", p: 8, now: 17, wantErr: mark.ErrHeld, wantJob: "j2", wantW: 7, wantS: 7},
		step{op: "commit", p: 8, now: 20, wantErr: mark.ErrHeld, wantJob: "j2", wantW: 7, wantS: 7},
		step{op: "commit", p: 8, now: 21, wantW: 8, wantS: 8, wantVer: map[int]int{8: 1}}, // 恰等 deadline 过期
	)
	runSteps(t, s, steps)
}

// deadline 恰等即过期、小 1 仍存活。
func TestDeadlineEdge(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "begin", name: "j", a: 0, b: 1, ttl: 5, now: 0, wantW: -1, wantS: -1}, // deadline=5
		{op: "commit", p: 2, now: 0, wantW: -1, wantS: -1},
		{op: "commit", p: 0, now: 4, wantErr: mark.ErrHeld, wantJob: "j", wantW: -1, wantS: -1}, // now=4 < deadline
		{op: "stage", name: "j", p: 0, now: 4, wantW: -1, wantS: -1},
		{op: "heartbeat", name: "j", now: 5, wantErr: mark.ErrNoJob, wantJob: "j", wantW: -1, wantS: -1}, // 恰等即过期
		{op: "stage", name: "j", p: 1, now: 5, wantErr: mark.ErrNoJob, wantJob: "j", wantW: -1, wantS: -1},
		{op: "commit", p: 0, now: 5, wantW: 0, wantS: 0, wantVer: map[int]int{0: 1}}, // 过期落地，暂存丢弃
		{op: "commit", p: 1, now: 6, wantW: 2, wantS: 2},
	})
}

// 过期作业的暂存不生效：同名重新 Begin 后暂存集为空。
func TestExpiredStagingDiscarded(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "begin", name: "j", a: 0, b: 1, ttl: 5, now: 0, wantW: -1, wantS: -1},
		{op: "stage", name: "j", p: 0, now: 1, wantW: -1, wantS: -1},
		{op: "stage", name: "j", p: 1, now: 1, wantW: -1, wantS: -1},
		{op: "commit", p: 5, now: 5, wantW: -1, wantS: -1}, // 接受此操作时 j 的取消落地
		{op: "begin", name: "j", a: 0, b: 1, ttl: 5, now: 6, wantW: -1, wantS: -1},
		{op: "finish", name: "j", now: 6, wantErr: mark.ErrIncomplete, hasPart: true, wantPart: 0, wantW: -1, wantS: -1},
	})
}

// 被拒操作不落地取消、不推进时钟。
func TestRejectedOpDoesNotLandCancellation(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "commit", p: 0, now: 0, wantW: 0, wantS: 0},
		{op: "commit", p: 1, now: 1, wantW: 1, wantS: 1},
		{op: "begin", name: "j", a: 0, b: 1, ttl: 5, now: 2, wantW: 1, wantS: -1},   // deadline=7
		{op: "commit", p: 0, now: 7, wantErr: mark.ErrAlready, wantW: 1, wantS: -1}, // 被拒：j 的取消不落地，S 仍为 -1
		{op: "ack", name: "c", upto: 2, now: 7, wantErr: mark.ErrBeyondStable, wantW: 1, wantS: -1},
		{op: "commit", p: 2, now: 7, wantW: 2, wantS: 2},                                // 接受：j 的取消落地，S 回升
		{op: "commit", p: 1, now: 6, wantErr: mark.ErrClockRegress, wantW: 2, wantS: 2}, // 时钟已推进到 7
	})
}

// Ack：恰等 S 接受、回退拒绝、超过 S 拒绝、S 回退后已确认值不变。
func TestAckSemantics(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "commit", p: 0, now: 0, wantW: 0, wantS: 0},
		{op: "commit", p: 1, now: 1, wantW: 1, wantS: 1},
		{op: "commit", p: 2, now: 2, wantW: 2, wantS: 2},
		{op: "commit", p: 3, now: 3, wantW: 3, wantS: 3},
		{op: "ack", name: "c", upto: -1, now: 4, wantW: 3, wantS: 3, wantAck: map[string]int{"c": -1}}, // 首次前视为 -1
		{op: "ack", name: "c", upto: 3, now: 5, wantW: 3, wantS: 3, wantAck: map[string]int{"c": 3}},   // 恰等 S
		{op: "ack", name: "c", upto: 2, now: 6, wantErr: mark.ErrAckRegress, wantW: 3, wantS: 3},
		{op: "ack", name: "c", upto: 4, now: 6, wantErr: mark.ErrBeyondStable, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 1, b: 2, ttl: 100, now: 7, wantW: 3, wantS: 0},                 // S 回退到 0
		{op: "ack", name: "c", upto: 3, now: 8, wantErr: mark.ErrBeyondStable, wantW: 3, wantS: 0}, // 3 > 当前 S=0
		{op: "ack", name: "c", upto: 0, now: 8, wantErr: mark.ErrAckRegress, wantW: 3, wantS: 0},   // 回退优先于超界
		{op: "ack", name: "c", upto: 2, now: 9, wantErr: mark.ErrAckRegress, wantW: 3, wantS: 0},
		{op: "ack", name: "d", upto: 1, now: 9, wantErr: mark.ErrBeyondStable, wantW: 3, wantS: 0},
		{op: "ack", name: "d", upto: 0, now: 9, wantW: 3, wantS: 0, wantAck: map[string]int{"d": 0}}, // 恰等回退后的 S
		{op: "abort", name: "j", now: 10, wantW: 3, wantS: 3},                                        // S 回升
	})
}

// Missing 分区回填不产生修订，但可推进 W。
func TestBackfillMissingNoRevisions(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "ack", name: "c", upto: -1, now: 0, wantW: -1, wantS: -1},
		{op: "begin", name: "j", a: 0, b: 2, ttl: 10, now: 0, wantW: -1, wantS: -1},
		{op: "stage", name: "j", p: 0, now: 1, wantW: -1, wantS: -1},
		{op: "stage", name: "j", p: 1, now: 1, wantW: -1, wantS: -1},
		{op: "stage", name: "j", p: 2, now: 1, wantW: -1, wantS: -1},
		{op: "finish", name: "j", now: 2, wantW: 2, wantS: 2, // 提交前全为 Missing：无修订
			wantVer: map[int]int{0: 1, 1: 1, 2: 1}},
	})
}

// 修订清单：按消费者名字节序，仅含提交前 ver>=1 且 p <= 已确认值的分区，空清单不列出。
func TestRevisionsMultipleConsumers(t *testing.T) {
	s := mark.New(4)
	var steps []step
	for p := 0; p <= 5; p++ {
		steps = append(steps, step{op: "commit", p: p, now: p, wantW: p, wantS: p})
	}
	steps = append(steps,
		step{op: "ack", name: "b", upto: 5, now: 6, wantW: 5, wantS: 5},
		step{op: "ack", name: "a", upto: 2, now: 7, wantW: 5, wantS: 5},
		step{op: "ack", name: "c", upto: 0, now: 8, wantW: 5, wantS: 5},
		step{op: "begin", name: "j", a: 1, b: 4, ttl: 50, now: 9, wantW: 5, wantS: 0},
		step{op: "stage", name: "j", p: 1, now: 10, wantW: 5, wantS: 0},
		step{op: "stage", name: "j", p: 2, now: 10, wantW: 5, wantS: 0},
		step{op: "stage", name: "j", p: 3, now: 10, wantW: 5, wantS: 0},
		step{op: "stage", name: "j", p: 4, now: 10, wantW: 5, wantS: 0},
		step{op: "finish", name: "j", now: 11, wantW: 5, wantS: 5,
			wantVer: map[int]int{1: 2, 2: 2, 3: 2, 4: 2},
			wantRev: []mark.Revision{ // c 已确认 0，区间内 p 均 > 0，清单为空不列出
				{Consumer: "a", Parts: []int{1, 2}},
				{Consumer: "b", Parts: []int{1, 2, 3, 4}},
			}},
	)
	runSteps(t, s, steps)
}

// 拒绝次序：参数非法 > 时钟回退 > 作业不存在/已存在 > 状态类错误。
func TestRejectionOrder(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "commit", p: -1, now: 0, wantErr: mark.ErrInvalid, wantW: -1, wantS: -1},
		{op: "commit", p: mark.MaxPart + 1, now: 0, wantErr: mark.ErrInvalid, wantW: -1, wantS: -1},
		{op: "commit", p: 0, now: -1, wantErr: mark.ErrInvalid, wantW: -1, wantS: -1},
		{op: "commit", p: 0, now: 0, wantW: 0, wantS: 0},
		{op: "commit", p: 1, now: 1, wantW: 1, wantS: 1},
		{op: "commit", p: 2, now: 2, wantW: 2, wantS: 2},
		{op: "commit", p: 3, now: 3, wantW: 3, wantS: 3},
		// Begin 参数非法（先于时钟与状态检查）。
		{op: "begin", name: "j", a: 5, b: 3, ttl: 1, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 0, b: mark.MaxSpan, ttl: 1, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 0, b: 1, ttl: 0, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 0, b: 1, ttl: mark.MaxTTL + 1, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 0, b: mark.MaxPart + 1, ttl: 1, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "begin", name: "j", a: 0, b: 1, ttl: 0, now: 0, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3}, // 参数非法 > 时钟回退
		{op: "begin", name: "j", a: 4, b: 5, ttl: 10, now: 0, wantErr: mark.ErrClockRegress, wantW: 3, wantS: 3},
		{op: "commit", p: 9, now: 2, wantErr: mark.ErrClockRegress, wantW: 3, wantS: 3},
		// Ack 参数非法与回退。
		{op: "ack", name: "c", upto: -2, now: 4, wantErr: mark.ErrInvalid, wantW: 3, wantS: 3},
		{op: "ack", name: "c", upto: 3, now: 4, wantW: 3, wantS: 3, wantAck: map[string]int{"c": 3}},
		// 作业已存在 > ErrOverlap；ErrHeld > ErrAlready。
		{op: "begin", name: "j", a: 2, b: 3, ttl: 100, now: 5, wantW: 3, wantS: 1},
		{op: "begin", name: "j", a: 2, b: 3, ttl: 100, now: 5, wantErr: mark.ErrJobExists, wantJob: "j", wantW: 3, wantS: 1},
		{op: "begin", name: "k", a: 3, b: 4, ttl: 100, now: 5, wantErr: mark.ErrOverlap, wantJob: "j", wantW: 3, wantS: 1},
		{op: "commit", p: 2, now: 5, wantErr: mark.ErrHeld, wantJob: "j", wantW: 3, wantS: 1}, // 2 已提交但仍报 ErrHeld
		// ErrNoJob > 状态类错误。
		{op: "stage", name: "ghost", p: 2, now: 5, wantErr: mark.ErrNoJob, wantJob: "ghost", wantW: 3, wantS: 1},
		{op: "finish", name: "ghost", now: 5, wantErr: mark.ErrNoJob, wantJob: "ghost", wantW: 3, wantS: 1},
		{op: "heartbeat", name: "ghost", now: 5, wantErr: mark.ErrNoJob, wantJob: "ghost", wantW: 3, wantS: 1},
		{op: "abort", name: "ghost", now: 5, wantErr: mark.ErrNoJob, wantJob: "ghost", wantW: 3, wantS: 1},
		// Stage 越界；Finish 缺暂存带最小分区号。
		{op: "stage", name: "j", p: 9, now: 5, wantErr: mark.ErrOutOfRange, wantJob: "j", hasPart: true, wantPart: 9, wantW: 3, wantS: 1},
		{op: "finish", name: "j", now: 5, wantErr: mark.ErrIncomplete, wantJob: "j", hasPart: true, wantPart: 2, wantW: 3, wantS: 1},
		// Ack：回退 > 超界。
		{op: "ack", name: "c", upto: 2, now: 5, wantErr: mark.ErrAckRegress, wantW: 3, wantS: 1},
		{op: "ack", name: "c", upto: 3, now: 5, wantErr: mark.ErrBeyondStable, wantW: 3, wantS: 1}, // 3 > S=1
		// 暂存齐全后提交：c 已确认 3，区间内提交前 ver>=1 → 修订 [2,3]。
		{op: "stage", name: "j", p: 2, now: 6, wantW: 3, wantS: 1},
		{op: "stage", name: "j", p: 3, now: 6, wantW: 3, wantS: 1},
		{op: "finish", name: "j", now: 7, wantW: 3, wantS: 3,
			wantVer: map[int]int{2: 2, 3: 2},
			wantRev: []mark.Revision{{Consumer: "c", Parts: []int{2, 3}}}},
	})
}

// Abort 放弃作业并丢弃暂存，区间释放，S 回升。
func TestAbort(t *testing.T) {
	s := mark.New(4)
	runSteps(t, s, []step{
		{op: "commit", p: 0, now: 0, wantW: 0, wantS: 0},
		{op: "commit", p: 1, now: 1, wantW: 1, wantS: 1},
		{op: "begin", name: "j", a: 0, b: 1, ttl: 100, now: 2, wantW: 1, wantS: -1},
		{op: "stage", name: "j", p: 0, now: 3, wantW: 1, wantS: -1},
		{op: "abort", name: "j", now: 4, wantW: 1, wantS: 1},
		{op: "commit", p: 0, now: 5, wantErr: mark.ErrAlready, wantW: 1, wantS: 1}, // 区间已释放
		{op: "begin", name: "j2", a: 0, b: 1, ttl: 10, now: 6, wantW: 1, wantS: -1},
		{op: "finish", name: "j2", now: 6, wantErr: mark.ErrIncomplete, hasPart: true, wantPart: 0, wantW: 1, wantS: -1}, // 暂存已丢弃
	})
}

// 活跃作业数上限 J。
func TestTooManyJobs(t *testing.T) {
	s := mark.New(1)
	runSteps(t, s, []step{
		{op: "begin", name: "j1", a: 0, b: 1, ttl: 10, now: 0, wantW: -1, wantS: -1},
		{op: "begin", name: "j2", a: 2, b: 3, ttl: 10, now: 0, wantErr: mark.ErrTooManyJobs, wantW: -1, wantS: -1},
		{op: "abort", name: "j1", now: 1, wantW: -1, wantS: -1},
		{op: "begin", name: "j2", a: 2, b: 3, ttl: 10, now: 1, wantW: -1, wantS: -1},
	})
}

// 并发冒烟：多协程提交不相交分区 + 并发查询，-race 下等价某串行序。
func TestConcurrentCommits(t *testing.T) {
	s := mark.New(4)
	const workers, per = 8, 250
	var clock int64
	var wg sync.WaitGroup
	stop := make(chan struct{})
	go func() { // 并发只读查询
		for {
			select {
			case <-stop:
				return
			default:
				_, _, _ = s.W(), s.S(), s.Ver(3)
			}
		}
	}()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				p := base*per + i
				for {
					now := int(atomic.AddInt64(&clock, 1))
					err := s.Commit(p, now)
					if errors.Is(err, mark.ErrClockRegress) {
						continue // 串行序中排在更晚的 now 之后，重试
					}
					if err != nil {
						t.Errorf("Commit(%d) = %v", p, err)
					}
					break
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	if got, want := s.W(), workers*per-1; got != want {
		t.Fatalf("W = %d, want %d", got, want)
	}
	if got := s.S(); got != workers*per-1 {
		t.Fatalf("S = %d, want %d", got, workers*per-1)
	}
}
