package guard_test

import (
	"errors"
	"testing"

	"ontology/breaker"
	"ontology/bulkhead"
	"ontology/guard"
)

// step is one table-driven operation. kind is one of:
//
//	"A" Acquire(now)              -> wantA, wantID, wantErr
//	"R" Release(id, ok, dur, now) -> wantErr
//	"S" Status(id, now)           -> wantO, wantErr
//	"K" check Snapshot()          -> wantSnap
type step struct {
	kind     string
	id       int
	ok       bool
	dur      int64
	now      int64
	wantA    guard.AcquireResult
	wantID   int
	wantO    bulkhead.Outcome
	wantErr  error
	wantSnap guard.Snapshot
}

func acq(now int64, res guard.AcquireResult, id int) step {
	return step{kind: "A", now: now, wantA: res, wantID: id}
}

func acqErr(now int64, err error) step {
	return step{kind: "A", now: now, wantErr: err}
}

func rel(id int, ok bool, dur, now int64) step {
	return step{kind: "R", id: id, ok: ok, dur: dur, now: now}
}

func relErr(id int, ok bool, dur, now int64, err error) step {
	return step{kind: "R", id: id, ok: ok, dur: dur, now: now, wantErr: err}
}

func stat(id int, now int64, outcome bulkhead.Outcome) step {
	return step{kind: "S", id: id, now: now, wantO: outcome}
}

func statErr(id int, now int64, err error) step {
	return step{kind: "S", id: id, now: now, wantErr: err}
}

func snap(state breaker.State, epoch uint64, ringCnt, fails, slows, inSvc, qLen int) step {
	return step{kind: "K", wantSnap: guard.Snapshot{
		State: state, Epoch: epoch, RingCount: ringCnt,
		Failures: fails, Slows: slows, InService: inSvc, QueueLen: qLen,
	}}
}

// checkInvariants verifies the cross-package invariants after every step.
func checkInvariants(t *testing.T, g *guard.Guard, cfg guard.Config) {
	t.Helper()
	snap := g.Snapshot()
	issued, counts := g.Ledger()
	sum := 0
	for _, c := range counts {
		sum += c
	}
	if issued != sum {
		t.Fatalf("invariant: issued=%d != outcome sum=%d", issued, sum)
	}
	if snap.InService > cfg.C {
		t.Fatalf("invariant: in-service %d > C=%d", snap.InService, cfg.C)
	}
	if snap.QueueLen > cfg.Q {
		t.Fatalf("invariant: queue %d > Q=%d", snap.QueueLen, cfg.Q)
	}
	if snap.State != breaker.Closed && snap.QueueLen != 0 {
		t.Fatalf("invariant: queue non-empty while %s", snap.State)
	}
	if counts[bulkhead.InService] != snap.InService {
		t.Fatalf("invariant: in-service tally %d != snapshot %d",
			counts[bulkhead.InService], snap.InService)
	}
	if counts[bulkhead.Queued] != snap.QueueLen {
		t.Fatalf("invariant: queued tally %d != snapshot %d",
			counts[bulkhead.Queued], snap.QueueLen)
	}
}

func runCase(t *testing.T, cfg guard.Config, steps []step, want guard.Snapshot) {
	t.Helper()
	g, err := guard.New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	for i, s := range steps {
		switch s.kind {
		case "A":
			res, id, err := g.Acquire(s.now)
			t.Logf("step %02d Acquire(now=%d) => %s id=%d err=%v | 依据 %v",
				i, s.now, res, id, err, g.Snapshot())
			if res != s.wantA || id != s.wantID || !errors.Is(err, s.wantErr) {
				t.Errorf("step %d Acquire: got (%s, id=%d, %v), want (%s, id=%d, %v)",
					i, res, id, err, s.wantA, s.wantID, s.wantErr)
			}
		case "R":
			err := g.Release(s.id, s.ok, s.dur, s.now)
			t.Logf("step %02d Release(id=%d, ok=%v, dur=%d, now=%d) => err=%v | 依据 %v",
				i, s.id, s.ok, s.dur, s.now, err, g.Snapshot())
			if !errors.Is(err, s.wantErr) {
				t.Errorf("step %d Release: got err=%v, want %v", i, err, s.wantErr)
			}
		case "S":
			outcome, err := g.Status(s.id, s.now)
			t.Logf("step %02d Status(id=%d, now=%d) => %s err=%v | 依据 %v",
				i, s.id, s.now, outcome, err, g.Snapshot())
			if outcome != s.wantO || !errors.Is(err, s.wantErr) {
				t.Errorf("step %d Status: got (%s, %v), want (%s, %v)",
					i, outcome, err, s.wantO, s.wantErr)
			}
		case "K":
			got := g.Snapshot()
			t.Logf("step %02d Snapshot => %+v", i, got)
			if got != s.wantSnap {
				t.Errorf("step %d Snapshot: got %+v, want %+v", i, got, s.wantSnap)
			}
		default:
			t.Fatalf("step %d: unknown kind %q", i, s.kind)
		}
		checkInvariants(t, g, cfg)
	}
	if got := g.Snapshot(); got != want {
		t.Errorf("final snapshot: got %+v, want %+v", got, want)
	}
	t.Logf("final snapshot %+v", g.Snapshot())
}

// cfgBase is the configuration from the specification example.
var cfgBase = guard.Config{N: 2, M: 2, F: 50, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 1, Q: 1}

func TestSpecCases(t *testing.T) {
	tests := []struct {
		name  string
		cfg   guard.Config
		steps []step
		want  guard.Snapshot
	}{
		{
			name: "example: grant queue full, open, half-open, close",
			cfg:  cfgBase,
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(1, guard.Queued, 2),
				acq(2, guard.RejectedFull, 0), // 拒绝不耗编号
				rel(1, true, 10, 3),           // 环[成功], 许可授予 2
				stat(2, 3, bulkhead.InService),
				rel(2, false, 10, 4), // 环[成功,失败], 1*100>=50*2 恰等, 开路
				snap(breaker.Open, 1, 0, 0, 0, 0, 0),
				acq(13, guard.RejectedOpen, 0), // 13 < 4+10
				acq(14, guard.Granted, 3),      // 恰在 openedAt+O 转半开
				acq(14, guard.RejectedHalfOpenFull, 0),
				rel(3, true, 10, 15), // 探测成功数达 H, 恢复 Closed
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 3},
		},
		{
			name: "example: queued waiter times out before grant",
			cfg:  cfgBase,
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(1, guard.Queued, 2),
				stat(2, 5, bulkhead.Queued), // 1+5=6 > 5, 未超时
				rel(1, true, 10, 6),         // 结算先使 2 超时(1+5<=6), 无人可授
				stat(2, 6, bulkhead.TimedOut),
			},
			want: guard.Snapshot{State: breaker.Closed, RingCount: 1},
		},
		{
			name: "example: half-open probe failure reopens",
			cfg:  cfgBase,
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0), // 环[失败], 条数 1 < M
				acq(1, guard.Granted, 2),
				rel(2, false, 10, 1), // 环[失败,失败], 开路 openedAt=1
				acq(10, guard.RejectedOpen, 0),
				acq(11, guard.Granted, 3),
				rel(3, false, 10, 12), // 探测失败, 重新开路 openedAt=12
				snap(breaker.Open, 3, 0, 0, 0, 0, 0),
				acq(21, guard.RejectedOpen, 0), // 21 < 12+10
				acq(22, guard.Granted, 4),
				rel(4, true, 10, 22),
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 5},
		},
		{
			name: "failure rate exactly at threshold opens",
			cfg:  guard.Config{N: 2, M: 2, F: 50, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 2, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Granted, 2),
				rel(1, false, 10, 0),
				rel(2, true, 10, 0), // 1*100 >= 50*2 恰等, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "failure rate one below threshold stays closed",
			cfg:  guard.Config{N: 101, M: 101, F: 1, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 101, Q: 0},
			// 100 成功 + 1 失败: 1*100=100 < 1*101=101, 差 1 不开路;
			// 再来 1 失败挤掉最旧成功: 2*100=200 >= 101, 开路(兼证环满淘汰)。
			steps: oneBelowSteps(),
			want:  guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "slow duration exactly S counts as slow",
			cfg:  guard.Config{N: 1, M: 1, F: 100, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 1, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, true, 100, 0), // dur==S 算慢, 1*100>=100*1, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "slow duration just below S does not",
			cfg:  guard.Config{N: 1, M: 1, F: 100, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 1, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, true, 99, 0),
				snap(breaker.Closed, 0, 1, 0, 0, 0, 0),
			},
			want: guard.Snapshot{State: breaker.Closed, RingCount: 1},
		},
		{
			name: "slow rate alone trips the breaker",
			cfg:  guard.Config{N: 2, M: 2, F: 100, SR: 50, S: 50, O: 10, Wt: 5, H: 1, C: 2, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Granted, 2),
				rel(1, true, 50, 0),
				rel(2, true, 50, 0), // 失败 0 次, 慢 2 次: 2*100>=50*2, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "failure rate alone trips the breaker",
			cfg:  guard.Config{N: 2, M: 2, F: 50, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 2, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Granted, 2),
				rel(1, false, 10, 0),
				rel(2, false, 10, 0), // 慢 0 次, 失败 2 次: 2*100>=50*2, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "window below min calls is not evaluated",
			cfg:  guard.Config{N: 3, M: 2, F: 1, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 1, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0), // 失败率 100% 但条数 1 < M=2, 不判定
				snap(breaker.Closed, 0, 1, 1, 0, 0, 0),
				acq(0, guard.Granted, 2),
				rel(2, true, 10, 0), // 条数 2 >= M, 1*100>=1*2, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "full window evicts the oldest entry",
			cfg:  guard.Config{N: 3, M: 3, F: 50, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 1, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0), // [F]
				acq(0, guard.Granted, 2),
				rel(2, true, 10, 0), // [F ok]
				acq(0, guard.Granted, 3),
				rel(3, true, 10, 0), // [F ok ok], 1*100<50*3, 不开
				snap(breaker.Closed, 0, 3, 1, 0, 0, 0),
				acq(0, guard.Granted, 4),
				rel(4, false, 10, 0), // 淘汰最旧 F -> [ok ok F], 仍 1 失败, 不开
				snap(breaker.Closed, 0, 3, 1, 0, 0, 0),
				acq(0, guard.Granted, 5),
				rel(5, false, 10, 0), // 淘汰 ok -> [ok F F], 2*100>=50*3, 开路
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "example: half-open slow probe (dur==S, ok) reopens",
			cfg:  cfgBase,
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0),
				acq(1, guard.Granted, 2),
				rel(2, false, 10, 1),
				acq(11, guard.Granted, 3),
				rel(3, true, 100, 12), // ok 但 dur>=S 算慢, 重新开路
				snap(breaker.Open, 3, 0, 0, 0, 0, 0),
				acq(22, guard.Granted, 4),
				rel(4, true, 10, 22),
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 5},
		},
		{
			name: "half-open probes exhausted then reopen",
			cfg:  guard.Config{N: 1, M: 1, F: 50, SR: 100, S: 1000, O: 10, Wt: 5, H: 2, C: 2, Q: 1},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0), // 开路 openedAt=0
				acq(9, guard.RejectedOpen, 0),
				acq(10, guard.Granted, 2), // 半开, 探测 1
				acq(10, guard.Granted, 3), // 探测 2, 已用尽
				acq(10, guard.RejectedHalfOpenFull, 0),
				rel(2, true, 10, 10), // 成功探测 1 < H, 仍半开
				snap(breaker.HalfOpen, 2, 0, 0, 0, 1, 0),
				rel(3, false, 10, 10), // 任一探测失败, 重新开路
				snap(breaker.Open, 3, 0, 0, 0, 0, 0),
				acq(19, guard.RejectedOpen, 0),
				acq(20, guard.Granted, 4),
				rel(4, true, 10, 20),
				acq(20, guard.Granted, 5),
				rel(5, true, 10, 20), // 两个探测均成功, 恢复 Closed
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 5},
		},
		{
			name: "open revokes every queued waiter",
			cfg:  guard.Config{N: 1, M: 1, F: 50, SR: 100, S: 1000, O: 10, Wt: 100, H: 1, C: 1, Q: 2},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Queued, 2),
				acq(0, guard.Queued, 3),
				acq(0, guard.RejectedFull, 0),
				rel(1, false, 10, 0), // 开路, 2 与 3 按入队序撤销
				stat(2, 0, bulkhead.Revoked),
				stat(3, 0, bulkhead.Revoked),
				snap(breaker.Open, 1, 0, 0, 0, 0, 0),
			},
			want: guard.Snapshot{State: breaker.Open, Epoch: 1},
		},
		{
			name: "queue is only used while closed",
			cfg:  guard.Config{N: 1, M: 1, F: 50, SR: 100, S: 1000, O: 10, Wt: 100, H: 2, C: 1, Q: 1},
			steps: []step{
				acq(0, guard.Granted, 1),
				rel(1, false, 10, 0),
				acq(0, guard.RejectedOpen, 0),
				acq(10, guard.Granted, 2), // 半开, 探测 1
				// 半开、探测未用尽、无空闲许可: 拒绝 Full 而非入队
				acq(10, guard.RejectedFull, 0),
				rel(2, true, 10, 10), // 成功探测 1 < H
				acq(11, guard.Granted, 3),
				rel(3, true, 10, 11), // 成功探测 2 = H, 恢复 Closed
				acq(12, guard.Granted, 4),
				acq(12, guard.Queued, 5), // Closed 下恢复排队
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 3, InService: 1, QueueLen: 1},
		},
		{
			name: "rejections consume no id",
			cfg:  cfgBase,
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Queued, 2),
				acq(0, guard.RejectedFull, 0),
				acq(0, guard.RejectedFull, 0),
				rel(1, true, 10, 1), // 许可授予 2
				rel(2, true, 10, 2),
				acq(3, guard.Granted, 3), // 编号未被拒绝消耗
			},
			want: guard.Snapshot{State: breaker.Closed, RingCount: 2, InService: 1},
		},
		{
			name: "errors do not mutate any state",
			cfg:  cfgBase,
			steps: []step{
				relErr(0, true, 10, 0, guard.ErrInvalidParam),       // id<=0
				relErr(1, true, -1, 0, guard.ErrInvalidParam),       // dur<0
				acqErr(-1, guard.ErrInvalidTime),                    // now<0
				acqErr(1_000_000_000_000_001, guard.ErrInvalidTime), // now>1e15
				statErr(0, 0, guard.ErrInvalidParam),                // id<=0
				statErr(1, 0, guard.ErrUnknownID),                   // 从未发出
				relErr(1, true, 10, 0, guard.ErrNotInService),       // 从未发出
				snap(breaker.Closed, 0, 0, 0, 0, 0, 0),              // 状态不变
				acq(0, guard.Granted, 1),                            // maxNow 与编号未被推进
				acq(1, guard.Queued, 2),
				acqErr(0, guard.ErrClockRegression),           // 0 < maxNow=1
				relErr(2, true, 10, 1, guard.ErrNotInService), // 排队中的 id
				rel(1, true, 10, 2),                           // 记环[成功], 许可授予 2
				relErr(1, true, 10, 2, guard.ErrNotInService), // 已归还
				statErr(99, 2, guard.ErrUnknownID),
				stat(1, 2, bulkhead.Released),
				stat(2, 2, bulkhead.InService),
			},
			want: guard.Snapshot{State: breaker.Closed, RingCount: 1, InService: 1},
		},
		{
			name: "zero queue capacity rejects immediately",
			cfg:  guard.Config{N: 2, M: 2, F: 50, SR: 100, S: 100, O: 10, Wt: 5, H: 1, C: 1, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.RejectedFull, 0),
			},
			want: guard.Snapshot{State: breaker.Closed, InService: 1},
		},
		{
			name: "stale-epoch release does not pollute the ring",
			cfg:  guard.Config{N: 1, M: 1, F: 50, SR: 100, S: 1000, O: 10, Wt: 5, H: 1, C: 2, Q: 0},
			steps: []step{
				acq(0, guard.Granted, 1),
				acq(0, guard.Granted, 2),
				rel(1, false, 10, 0),      // 开路, 纪元 1; id 2 仍为旧纪元在役
				acq(10, guard.Granted, 3), // 半开, 纪元 2
				rel(3, true, 10, 10),      // 恢复 Closed, 纪元 3, 环清空
				rel(2, false, 10, 10),     // 旧纪元归还, 不计入(否则 1 失败即开路)
				snap(breaker.Closed, 3, 0, 0, 0, 0, 0),
				stat(2, 10, bulkhead.Released),
			},
			want: guard.Snapshot{State: breaker.Closed, Epoch: 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCase(t, tt.cfg, tt.steps, tt.want)
		})
	}
}

// oneBelowSteps builds the "failure rate one below threshold" scenario:
// N=M=101, F=1 -> 1 failure gives 1*100=100 < 1*101=101, exactly one
// below the threshold, so the breaker stays closed; one more failure
// (evicting the oldest success) trips it.
func oneBelowSteps() []step {
	var steps []step
	for i := 1; i <= 101; i++ {
		steps = append(steps, acq(0, guard.Granted, i))
	}
	for i := 1; i <= 100; i++ {
		steps = append(steps, rel(i, true, 10, 0))
	}
	steps = append(steps, rel(101, false, 10, 0))
	steps = append(steps, snap(breaker.Closed, 0, 101, 1, 0, 0, 0))
	steps = append(steps, acq(0, guard.Granted, 102))
	steps = append(steps, rel(102, false, 10, 0))
	return steps
}

func TestInvalidConfig(t *testing.T) {
	mutations := map[string]func(*guard.Config){
		"M=0":    func(c *guard.Config) { c.M = 0 },
		"M>N":    func(c *guard.Config) { c.M = c.N + 1 },
		"N>1000": func(c *guard.Config) { c.N, c.M = 1001, 1001 },
		"F=0":    func(c *guard.Config) { c.F = 0 },
		"F=101":  func(c *guard.Config) { c.F = 101 },
		"SR=0":   func(c *guard.Config) { c.SR = 0 },
		"SR=101": func(c *guard.Config) { c.SR = 101 },
		"S=0":    func(c *guard.Config) { c.S = 0 },
		"S>1e9":  func(c *guard.Config) { c.S = 1_000_000_001 },
		"O=0":    func(c *guard.Config) { c.O = 0 },
		"O>1e9":  func(c *guard.Config) { c.O = 1_000_000_001 },
		"Wt=0":   func(c *guard.Config) { c.Wt = 0 },
		"Wt>1e9": func(c *guard.Config) { c.Wt = 1_000_000_001 },
		"H=0":    func(c *guard.Config) { c.H = 0 },
		"H=101":  func(c *guard.Config) { c.H = 101 },
		"C=0":    func(c *guard.Config) { c.C = 0 },
		"C=1001": func(c *guard.Config) { c.C = 1001 },
		"Q=-1":   func(c *guard.Config) { c.Q = -1 },
		"Q=1001": func(c *guard.Config) { c.Q = 1001 },
	}
	for name, mutate := range mutations {
		cfg := cfgBase
		mutate(&cfg)
		if _, err := guard.New(cfg); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if _, err := guard.New(cfgBase); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}
}
