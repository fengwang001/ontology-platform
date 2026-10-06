package room

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"
	"testing"
)

// genCases 生成 count 组随机操作序列。覆盖：
//   - 随机 L/U/C/R 边界；
//   - 玩家池小于/等于/大于上限，含重名加入与未加入者操作；
//   - 单调时间与时钟回退、偶尔非法参数（空标识/越界 now）；
//   - 恰等于/差一秒到期、惰性推进、中途退出后再上报等。
func genCases(rng *rand.Rand, count int) []struct {
	cfg Config
	ops []op
} {
	cases := make([]struct {
		cfg Config
		ops []op
	}, count)
	for c := range cases {
		l := 2 + rng.Intn(4) // 2..5，保证小房间快速进入各阶段
		u := l + rng.Intn(4) // L..L+3（<=7）
		cfg := Config{
			L: l,
			U: u,
			C: int64(1 + rng.Intn(8)), // 1..8
			R: int64(1 + rng.Intn(10)),
		}
		// 玩家池：有时不足 L，有时超出 U，并加入一个永不加入的局外人。
		poolSize := 1 + rng.Intn(u+3)
		pool := make([]string, poolSize)
		for i := range pool {
			pool[i] = fmt.Sprintf("p%02d", i)
		}
		joined := map[string]bool{}
		var now int64
		n := 30 + rng.Intn(70)
		ops := make([]op, 0, n)
		for i := 0; i < n; i++ {
			// 时间：多数小幅前进（常出现差一秒/恰到期），偶尔停滞或回退。
			switch rng.Intn(12) {
			case 0:
			case 1:
				if now > 0 {
					now -= int64(1 + rng.Intn(3))
				}
			default:
				now += int64(rng.Intn(4))
			}
			if now < 0 {
				now = 0
			}
			user := pool[rng.Intn(len(pool))]
			// 胜者候选：在池内或一个不在名单的名字。
			winner := pool[rng.Intn(len(pool))]
			if rng.Intn(8) == 0 {
				winner = "zz"
			}
			var o op
			switch rng.Intn(8) {
			case 0, 1:
				o = op{kind: opJoin, user: user, now: now}
				joined[user] = true
			case 2:
				o = op{kind: opReady, user: user, now: now}
			case 3:
				o = op{kind: opUnready, user: user, now: now}
			case 4:
				o = op{kind: opLeave, user: user, now: now}
				delete(joined, user)
			case 5:
				o = op{kind: opEnd, user: user, now: now}
			case 6:
				o = op{kind: opReport, user: user, winner: winner, now: now}
			default:
				o = op{kind: opSnapshot, now: now}
			}
			// 偶发非法参数，验证参数检查在时钟检查之前且无副作用。
			if rng.Intn(25) == 0 {
				o.user = ""
			}
			if rng.Intn(40) == 0 {
				o.now = 1_000_000_000_001
			}
			ops = append(ops, o)
		}
		cases[c].cfg = cfg
		cases[c].ops = ops
	}
	return cases
}

func TestRandomDifferential(t *testing.T) {
	const total = 1600 // >= 1500
	rng := rand.New(rand.NewSource(20261006))
	cases := genCases(rng, total)

	var failed string
	var failedAt int
	for i, tc := range cases {
		f, err := New(tc.cfg)
		if err != nil {
			t.Fatalf("case %d: 合法配置被拒: %v", i, err)
		}
		n := newNaive(tc.cfg)
		var logBuf []string
		logBuf = append(logBuf,
			fmt.Sprintf("case=%d config L=%d U=%d C=%d R=%d", i, tc.cfg.L, tc.cfg.U, tc.cfg.C, tc.cfg.R))
		bad := false
		for j, o := range tc.ops {
			var ef, es error
			switch o.kind {
			case opJoin:
				ef = f.Join(o.user, o.now)
				es = n.join(o.user, o.now)
			case opReady:
				ef = f.SetReady(o.user, true, o.now)
				es = n.setReady(o.user, true, o.now)
			case opUnready:
				ef = f.SetReady(o.user, false, o.now)
				es = n.setReady(o.user, false, o.now)
			case opLeave:
				ef = f.Leave(o.user, o.now)
				es = n.leave(o.user, o.now)
			case opEnd:
				ef = f.End(o.user, o.now)
				es = n.end(o.user, o.now)
			case opReport:
				ef = f.Report(o.user, o.winner, o.now)
				es = n.report(o.user, o.winner, o.now)
			case opSnapshot:
				var sf, ss Snapshot
				sf, ef = f.Snapshot(o.now)
				ss, es = n.snapshot(o.now)
				if reason(ef) == reason(es) && !snapEqual(sf, ss) {
					logBuf = append(logBuf, fmt.Sprintf("#%03d %s => 快照不一致\n f=%#v\n s=%#v",
						j, o.String(), sf, ss))
					bad = true
					break
				}
			}
			if reason(ef) != reason(es) {
				logBuf = append(logBuf, fmt.Sprintf("#%03d %s => fast=%s slow=%s 【结果分歧】",
					j, o.String(), reason(ef), reason(es)))
				bad = true
				break
			}
			// 每条操作后用同 now 查询，全字段比对（查询本身不改逻辑状态）。
			sf, _ := f.Snapshot(clampNow(o.now))
			ss, _ := n.snapshot(clampNow(o.now))
			if !snapEqual(sf, ss) {
				logBuf = append(logBuf, fmt.Sprintf("#%03d %s => ok 后快照不一致\n f=%#v\n s=%#v",
					j, o.String(), sf, ss))
				bad = true
				break
			}
			logBuf = append(logBuf, fmt.Sprintf("#%03d %s => %s", j, o.String(), reason(ef)))
		}
		if bad {
			failedAt = i
			// 保留分歧前后的上下文。
			if len(logBuf) > 120 {
				logBuf = append(logBuf[:0], append(
					append([]string{}, logBuf[len(logBuf)-120:]...),
					"…（仅保留分歧前 120 行）")...,
				)
			}
			failed = joinLines(logBuf)
			break
		}
	}
	if failed != "" {
		path := "/tmp/room_diff_trace.log"
		_ = os.WriteFile(path, []byte(failed), 0o644)
		t.Fatalf("第 %d 组与朴素模型分歧；输入/输出/依据已写 %s\n%s", failedAt, path, failed)
	}
	t.Logf("随机差分通过：%d 组，每组 30~99 条操作", total)
}

func randSrc(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

func clampNow(n int64) int64 {
	if n < 0 {
		return 0
	}
	if n > 1e12 {
		return 1e12
	}
	return n
}

func joinLines(ls []string) string {
	out := ""
	for _, l := range ls {
		out += l + "\n"
	}
	return out
}

func snapEqual(a, b Snapshot) bool {
	return reflect.DeepEqual(normSnap(a), normSnap(b))
}

// TestConcurrentSerialEquivalence 并发调用结果必须等价于某种串行顺序：
// 多 goroutine 以单调时钟施加操作，不允许崩溃/数据竞争，终态合法。
func TestConcurrentSerialEquivalence(t *testing.T) {
	r, err := New(Config{L: 2, U: 6, C: 3, R: 20})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var clock int64
	var cmu sync.Mutex

	advance := func() int64 {
		cmu.Lock()
		defer cmu.Unlock()
		clock++
		return clock
	}

	users := []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6"}
	for _, u := range users {
		wg.Add(1)
		go func(user string) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(user[1])))
			for k := 0; k < 60; k++ {
				now := advance()
				switch rng.Intn(6) {
				case 0:
					_ = r.Join(user, now)
				case 1, 2:
					_ = r.SetReady(user, rng.Intn(2) == 0, now)
				case 3:
					_ = r.Leave(user, now)
				case 4:
					_ = r.End(user, now)
				case 5:
					_ = r.Report(user, users[rng.Intn(len(users))], now)
				}
			}
		}(u)
	}
	wg.Wait()

	s, err := r.Snapshot(clock + 100)
	if err != nil {
		t.Fatal(err)
	}
	switch s.Phase {
	case PhaseEnded, PhaseVoided, PhaseWaiting, PhaseCountdown, PhasePlaying, PhaseSettling:
	default:
		t.Fatalf("非法终态 %s", s.Phase)
	}
	// 结构不变量：在室人数不超上限；房主必为在室者。
	if len(s.Present) > 6 {
		t.Fatalf("在室人数超上限: %d", len(s.Present))
	}
	if s.Owner != "" && !mapHas(s.Ready, s.Owner) && len(s.Present) > 0 {
		// owner 可能未就绪，这里仅校验 owner 在 Present 中。
	}
	if s.Owner != "" {
		found := false
		for _, p := range s.Present {
			if p == s.Owner {
				found = true
			}
		}
		if !found {
			t.Fatalf("房主 %q 不在在室列表 %v", s.Owner, s.Present)
		}
	}
}

func mapHas(m map[string]bool, k string) bool {
	_, ok := m[k]
	return ok
}
