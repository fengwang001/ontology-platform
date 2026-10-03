package blocksched_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/blocksched"
)

var (
	errBadArg     = errors.New("bad arg")
	errPeerExists = errors.New("peer exists")
	errBanned     = errors.New("banned")
	errNoPeer     = errors.New("no peer")
	errNoRequest  = errors.New("no request")
	errClock      = errors.New("clock")
)

// errKind 把错误归为可比较的类别（两个实现使用各自的哨兵）。
func errKind(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, blocksched.ErrClock) || err == errClock:
		return "ErrClock"
	case errors.Is(err, blocksched.ErrPeerExists) || err == errPeerExists:
		return "ErrPeerExists"
	case errors.Is(err, blocksched.ErrBanned) || err == errBanned:
		return "ErrBanned"
	case errors.Is(err, blocksched.ErrBadArg) || err == errBadArg:
		return "ErrBadArg"
	case errors.Is(err, blocksched.ErrNoPeer) || err == errNoPeer:
		return "ErrNoPeer"
	case errors.Is(err, blocksched.ErrNoRequest) || err == errNoRequest:
		return "ErrNoRequest"
	default:
		return "UNKNOWN:" + err.Error()
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// runDifferential 对一条随机事件序列同时驱动正式实现与朴素实现，
// 逐步比较返回值与完成态；log 记录输入、输出与判定依据。
func runDifferential(t *testing.T, rng *rand.Rand, cfg testCfg, events int, echo bool) {
	t.Helper()
	real := blocksched.New(blocksched.Config{
		Blocks: cfg.B, BaseConcurrent: cfg.K, GlobalLimit: cfg.G,
		MaxBlockDup: cfg.M, Timeout: cfg.T, BanThreshold: cfg.F,
	})
	na := newNaive(cfg)
	log := []string{fmt.Sprintf("cfg=%+v", cfg)}
	failf := func(format string, args ...any) {
		t.Fatalf("%s\n%s", fmt.Sprintf(format, args...), joinLog(log))
	}
	note := func(line string) {
		log = append(log, line)
		if echo {
			t.Logf("%s", line)
		}
	}

	peerPool := []string{"p0", "p1", "p2", "p3", "p4"}
	known := func(id string) bool {
		// 朴素模型是唯一事实来源。
		_, ok := na.peers[id]
		return ok
	}

	for step := 0; step < events; step++ {
		// now 单调不减：大多数时候前进，偶尔不动。
		now := na.now
		switch rng.Intn(6) {
		case 0, 1:
			now += int64(rng.Intn(int(cfg.T) + 3))
		}

		kind := rng.Intn(8)
		switch kind {
		case 0: // AddPeer
			id := peerPool[rng.Intn(len(peerPool))]
			have := make([]bool, cfg.B)
			for b := range have {
				have[b] = rng.Intn(2) == 0
			}
			if rng.Intn(12) == 0 {
				have = have[:cfg.B-1] // 偶发错误长度（B>=2 时）
			}
			errReal := real.AddPeer(id, have)
			errNaive := na.addPeer(id, have)
			note(fmt.Sprintf("step %d AddPeer(%q,len=%d) -> real=%s naive=%s",
				step, id, len(have), errKind(errReal), errKind(errNaive)))
			if errKind(errReal) != errKind(errNaive) {
				failf("AddPeer mismatch at step %d", step)
			}
		case 1: // Have
			id := peerPool[rng.Intn(len(peerPool))]
			b := rng.Intn(cfg.B + 1) // 偶尔越界
			errReal := real.Have(id, b)
			errNaive := na.have(id, b)
			note(fmt.Sprintf("step %d Have(%q,%d) -> real=%s naive=%s [decision: availability only counts non-banned peers]",
				step, id, b, errKind(errReal), errKind(errNaive)))
			if errKind(errReal) != errKind(errNaive) {
				failf("Have mismatch at step %d", step)
			}
		case 2: // Drop
			id := peerPool[rng.Intn(len(peerPool))]
			errReal := real.Drop(id)
			errNaive := na.drop(id)
			note(fmt.Sprintf("step %d Drop(%q) -> real=%s naive=%s [decision: cancel all inflight, ban record persists]",
				step, id, errKind(errReal), errKind(errNaive)))
			if errKind(errReal) != errKind(errNaive) {
				failf("Drop mismatch at step %d", step)
			}
		case 3, 4: // Next
			id := peerPool[rng.Intn(len(peerPool))]
			bReal, okReal, errReal := real.Next(now, id)
			got := na.next(now, id)
			reason := "cap/global"
			if errReal == nil && okReal != got.ok {
				if got.ok {
					reason = "naive issued (rarity/endgame)"
				} else {
					reason = "no candidate / new-block-without-peer-block"
				}
			}
			note(fmt.Sprintf("step %d Next(now=%d,%q) -> real=(%d,%v,%s) naive=(%d,%v,%s) [decision: %s; known=%v]",
				step, now, id, bReal, okReal, errKind(errReal), got.block, got.ok, errKind(got.err), reason, known(id)))
			if errKind(errReal) != errKind(got.err) || okReal != got.ok || (okReal && bReal != got.block) {
				failf("Next mismatch at step %d", step)
			}
		case 5, 6: // Done
			id := peerPool[rng.Intn(len(peerPool))]
			b := rng.Intn(cfg.B + 1)
			success := rng.Intn(2) == 0
			cReal, banReal, errReal := real.Done(now, id, b, success)
			got := na.done(now, id, b, success)
			note(fmt.Sprintf("step %d Done(now=%d,%q,%d,ok=%v) -> real=(cancel=%v,ban=%v,%s) naive=(cancel=%v,ban=%v,%s) [decision: success cancels others & clears timeouts; failure marks block & bans at F]",
				step, now, id, b, success, cReal, banReal, errKind(errReal),
				got.canceled, got.banned, errKind(got.err)))
			if errKind(errReal) != errKind(got.err) || banReal != got.banned ||
				!sameStrings(cReal, got.canceled) {
				failf("Done mismatch at step %d", step)
			}
		case 7: // Tick
			expReal, errReal := real.Tick(now)
			got := na.tick(now)
			realTriples := make([]timeoutTriple, 0, len(expReal))
			for _, e := range expReal {
				realTriples = append(realTriples, timeoutTriple{e.Issued, e.Peer, e.Block})
			}
			note(fmt.Sprintf("step %d Tick(now=%d) -> real=%d:%v naive=%d:%v err=%s [decision: now-issued>=T, timeout++ lowers cap]",
				step, now, len(realTriples), realTriples, len(got.expired), got.expired, errKind(errReal)))
			if errKind(errReal) != errKind(got.err) {
				failf("Tick err mismatch at step %d", step)
			}
			if len(realTriples) != len(got.expired) {
				failf("Tick count mismatch at step %d", step)
			}
			for i := range realTriples {
				if realTriples[i] != got.expired[i] {
					failf("Tick entry %d mismatch at step %d: %+v vs %+v",
						i, step, realTriples[i], got.expired[i])
				}
			}
		}

		if real.Complete() != na.isComplete() {
			failf("Complete mismatch at step %d: real=%v naive=%v",
				step, real.Complete(), na.isComplete())
		}

		if err := real.DebugInvariants(); err != nil {
			failf("invariant violated at step %d: %v", step, err)
		}
	}
}

func joinLog(log []string) string {
	out := ""
	for _, line := range log {
		out += line + "\n"
	}
	return out
}

// TestDifferentialRandom 与朴素实现对照 2000 组随机事件序列。
// 设置环境变量 SEED 可用固定种子复现；默认逐组打印种子。
func TestDifferentialRandom(t *testing.T) {
	const runs = 2000
	for i := 0; i < runs; i++ {
		seed := int64(1000 + i) // 确定性：每次运行结果可精确复现
		rng := rand.New(rand.NewSource(seed))
		cfg := testCfg{
			B: 1 + rng.Intn(5),
			K: 1 + rng.Intn(4),
			G: 1 + rng.Intn(6),
			M: 2 + rng.Intn(3),
			T: int64(1 + rng.Intn(8)),
			F: 1 + rng.Intn(3),
		}
		events := 40 + rng.Intn(120)
		t.Run(fmt.Sprintf("seed=%d_cfg=%+v", seed, cfg), func(t *testing.T) {
			runDifferential(t, rng, cfg, events, false)
		})
	}
}

// TestDifferentialVerbose 小规模演示：-v 下逐条打印输入、输出与判定依据。
func TestDifferentialVerbose(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cfg := testCfg{B: 3, K: 2, G: 3, M: 2, T: 5, F: 2}
	t.Logf("verbose differential demo: seed=42 cfg=%+v events=60", cfg)
	runDifferential(t, rng, cfg, 60, true)
}
