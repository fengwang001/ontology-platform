package router

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// 随机操作序列对照测试：2000 组序列，逐条把 Router 的输出与朴素
// 模拟对比，并在日志中打印每步的输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		ttd := int64(1 + rng.Intn(30))
		d := int64(1 + rng.Intn(30))
		r, err := NewRouter(ttd, d)
		if err != nil {
			t.Fatalf("seq=%d NewRouter(%d,%d) 失败: %v", seq, ttd, d, err)
		}
		m := newModel(ttd, d)
		log := newOpLogger(t, seq, ttd, d)

		hostIDs := []string{"h0", "h1", "h2", "h3"}
		keys := []string{"k0", "k1", "k2", "k3", "k4", "k5"}
		pickHost := func() string {
			switch rng.Intn(12) {
			case 0:
				return ""
			case 1:
				return "ghost"
			}
			return hostIDs[rng.Intn(len(hostIDs))]
		}
		pickKey := func() string {
			if rng.Intn(12) == 0 {
				return ""
			}
			return keys[rng.Intn(len(keys))]
		}
		// now 大多为不减的随机游标，偶尔产生非法时间或时钟回退。
		var cursor int64
		pickNow := func() int64 {
			switch rng.Intn(20) {
			case 0:
				return -1 - int64(rng.Intn(100))
			case 1:
				return 1_000_000_000_000_000 + 1 + int64(rng.Intn(100))
			case 2:
				if cursor > 0 {
					return cursor - 1
				}
				return cursor
			}
			cursor += int64(rng.Intn(8))
			return cursor
		}

		steps := 20 + rng.Intn(30)
		for step := 0; step < steps; step++ {
			switch rng.Intn(10) {
			case 0, 1: // AddHost
				id := pickHost()
				gotErr := r.AddHost(id)
				wantErr, why := m.addHost(id)
				log.step("AddHost", fmt.Sprintf("id=%q", id), fmt.Sprintf("err=%v", gotErr), why)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d step=%d AddHost(%q): got %v, want %v", seq, step, id, gotErr, wantErr)
				}
			case 2, 3, 4, 5: // Route
				key, now := pickKey(), pickNow()
				gotHost, gotErr := r.Route(key, now)
				wantHost, wantErr, why := m.route(key, now)
				log.step("Route", fmt.Sprintf("key=%q now=%d", key, now), fmt.Sprintf("host=%q err=%v", gotHost, gotErr), why)
				if !errors.Is(gotErr, wantErr) || (gotErr == nil && gotHost != wantHost) {
					t.Fatalf("seq=%d step=%d Route(%q,%d): got (%q,%v), want (%q,%v)",
						seq, step, key, now, gotHost, gotErr, wantHost, wantErr)
				}
			case 6, 7: // Drain
				id, now := pickHost(), pickNow()
				gotErr := r.Drain(id, now)
				wantErr, why := m.drain(id, now)
				log.step("Drain", fmt.Sprintf("id=%q now=%d", id, now), fmt.Sprintf("err=%v", gotErr), why)
				if !errors.Is(gotErr, wantErr) {
					t.Fatalf("seq=%d step=%d Drain(%q,%d): got %v, want %v", seq, step, id, now, gotErr, wantErr)
				}
			case 8: // Status
				id, now := pickHost(), pickNow()
				gotS, gotErr := r.Status(id, now)
				wantS, wantErr, why := m.statusOf(id, now)
				log.step("Status", fmt.Sprintf("id=%q now=%d", id, now), fmt.Sprintf("status=%v err=%v", gotS, gotErr), why)
				if !errors.Is(gotErr, wantErr) || (gotErr == nil && gotS != wantS) {
					t.Fatalf("seq=%d step=%d Status(%q,%d): got (%v,%v), want (%v,%v)",
						seq, step, id, now, gotS, gotErr, wantS, wantErr)
				}
			case 9: // Live
				id, now := pickHost(), pickNow()
				gotN, gotErr := r.Live(id, now)
				wantN, wantErr, why := m.liveOf(id, now)
				log.step("Live", fmt.Sprintf("id=%q now=%d", id, now), fmt.Sprintf("live=%d err=%v", gotN, gotErr), why)
				if !errors.Is(gotErr, wantErr) || (gotErr == nil && gotN != wantN) {
					t.Fatalf("seq=%d step=%d Live(%q,%d): got (%d,%v), want (%d,%v)",
						seq, step, id, now, gotN, gotErr, wantN, wantErr)
				}
			}
		}

		// 序列末尾在同一时刻对全部主机比对状态与有效绑定数。
		finalNow := cursor
		for _, id := range hostIDs {
			gotS, gotErr := r.Status(id, finalNow)
			wantS, wantErr, why := m.statusOf(id, finalNow)
			log.step("Status", fmt.Sprintf("id=%q now=%d（终态核对）", id, finalNow), fmt.Sprintf("status=%v err=%v", gotS, gotErr), why)
			if !errors.Is(gotErr, wantErr) || (gotErr == nil && gotS != wantS) {
				t.Fatalf("seq=%d 终态 Status(%q): got (%v,%v), want (%v,%v)", seq, id, gotS, gotErr, wantS, wantErr)
			}
			gotN, gotErr := r.Live(id, finalNow)
			wantN, wantErr, why := m.liveOf(id, finalNow)
			log.step("Live", fmt.Sprintf("id=%q now=%d（终态核对）", id, finalNow), fmt.Sprintf("live=%d err=%v", gotN, gotErr), why)
			if !errors.Is(gotErr, wantErr) || (gotErr == nil && gotN != wantN) {
				t.Fatalf("seq=%d 终态 Live(%q): got (%d,%v), want (%d,%v)", seq, id, gotN, gotErr, wantN, wantErr)
			}
		}
	}
}

// opLogger 把每组序列的每步输入、输出与判定依据写入测试日志。
type opLogger struct {
	t   *testing.T
	seq int
	n   int
}

func newOpLogger(t *testing.T, seq int, ttd, d int64) *opLogger {
	t.Helper()
	t.Logf("seq=%d 开始：T=%d D=%d", seq, ttd, d)
	return &opLogger{t: t, seq: seq}
}

func (l *opLogger) step(op, input, output, why string) {
	l.t.Logf("seq=%d step=%d %s 输入[%s] 输出[%s] 判定依据[%s]", l.seq, l.n, op, input, output, why)
	l.n++
}
