package rollout

import (
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"testing"
)

// refController 是按题目规则直接重写的朴素参考实现，
// 与 Controller 之间不共享任何代码路径（除 FNV-1a 常量外）。
type refController struct {
	salt      string
	steps     []Step
	state     State
	level     int
	enteredAt int64
	paused    int64
	pauseAt   int64
	maxNow    int64
	haveNow   bool
}

func newRef(salt string, steps []Step) *refController {
	cp := make([]Step, len(steps))
	copy(cp, steps)
	return &refController{salt: salt, steps: cp, state: Idle, level: -1}
}

func (r *refController) rewind(now int64) bool {
	return r.haveNow && now < r.maxNow
}

func (r *refController) accept(now int64) {
	r.maxNow = now
	r.haveNow = true
}

func (r *refController) start(now int64) error {
	if r.rewind(now) {
		return ErrClockRewind
	}
	if r.state != Idle && r.state != RolledBack {
		return ErrInvalidState
	}
	r.accept(now)
	r.state = Running
	r.level = 0
	r.enteredAt = now
	r.paused = 0
	r.pauseAt = 0
	return nil
}

func (r *refController) pause(now int64) error {
	if r.rewind(now) {
		return ErrClockRewind
	}
	if r.state != Running {
		return ErrInvalidState
	}
	r.accept(now)
	r.state = Paused
	r.pauseAt = now
	return nil
}

func (r *refController) resume(now int64) error {
	if r.rewind(now) {
		return ErrClockRewind
	}
	if r.state != Paused {
		return ErrInvalidState
	}
	r.accept(now)
	r.paused += now - r.pauseAt
	r.state = Running
	return nil
}

func (r *refController) tick(now int64) ([]Transition, error) {
	if r.rewind(now) {
		return nil, ErrClockRewind
	}
	r.accept(now) // 无转移的 Tick 也登记最大时刻
	if r.state != Running {
		return nil, nil
	}
	var out []Transition
	for r.level < len(r.steps)-1 {
		due := r.enteredAt + r.steps[r.level].HoldMs + r.paused
		if now < due {
			break
		}
		nxt := r.level + 1
		out = append(out, Transition{From: r.level, To: nxt, AtMs: due})
		r.level = nxt
		r.enteredAt = due
		r.paused = 0
	}
	if r.level == len(r.steps)-1 {
		r.state = Completed
	}
	return out, nil
}

func (r *refController) rollback(now int64) error {
	if r.rewind(now) {
		return ErrClockRewind
	}
	if r.state != Running && r.state != Paused && r.state != Completed {
		return ErrInvalidState
	}
	r.accept(now)
	r.state = RolledBack
	r.level = -1
	r.enteredAt = 0
	r.paused = 0
	r.pauseAt = 0
	return nil
}

func (r *refController) status() StatusSnapshot {
	s := StatusSnapshot{State: r.state, Step: -1, Percent: 0}
	if r.level >= 0 {
		s.Step = r.level
		s.Percent = r.steps[r.level].Percent
	}
	return s
}

func refFnv1aBucket(salt, user string) uint32 {
	var h uint32 = 2166136261
	buf := make([]byte, 0, len(salt)+1+len(user))
	buf = append(buf, salt...)
	buf = append(buf, 0)
	buf = append(buf, user...)
	for _, b := range buf {
		h ^= uint32(b)
		h *= 16777619
	}
	return h % 10000
}

func (r *refController) inRollout(user string) (bool, error) {
	if user == "" {
		return false, ErrEmptyUser
	}
	percent := 0
	if r.level >= 0 {
		percent = r.steps[r.level].Percent
	}
	return int(refFnv1aBucket(r.salt, user)) < percent*100, nil
}

type opKind int

const (
	opStart opKind = iota
	opPause
	opResume
	opTick
	opRollback
	opInRollout
)

type refOp struct {
	kind opKind
	now  int64
	user string
}

func randomSteps(rng *rand.Rand) []Step {
	n := 2 + rng.Intn(5) // 2..6 级
	steps := make([]Step, n)
	prev := 0
	for i := range steps {
		if i == n-1 {
			steps[i].Percent = 100
		} else {
			top := 99
			remaining := n - 1 - i
			if top-prev <= remaining {
				steps[i].Percent = prev + 1
			} else {
				steps[i].Percent = prev + 1 + rng.Intn(top-prev-remaining)
			}
		}
		prev = steps[i].Percent
		steps[i].HoldMs = int64(rng.Intn(30)) // 0..29ms，含 0
	}
	return steps
}

func randomOps(rng *rand.Rand) []refOp {
	n := 1 + rng.Intn(40)
	ops := make([]refOp, n)
	now := int64(rng.Intn(5))
	for i := range ops {
		now += int64(rng.Intn(8))
		ops[i].now = now
		if rng.Intn(7) == 0 {
			ops[i].now -= int64(rng.Intn(5)) // 故意制造回拨
		}
		ops[i].kind = opKind(rng.Intn(6))
		if ops[i].kind == opInRollout {
			if rng.Intn(10) == 0 {
				ops[i].user = "" // 故意触发空用户
			} else {
				ops[i].user = "user-" + strconv.Itoa(rng.Intn(30))
			}
		}
	}
	return ops
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrClockRewind):
		return "rewind"
	case errors.Is(err, ErrInvalidState):
		return "state"
	case errors.Is(err, ErrEmptyUser):
		return "empty-user"
	default:
		return "other"
	}
}

// TestDifferentialAgainstNaiveSimulator 与朴素模拟器对拍 2000 组随机操作序列。
// 每组日志打印输入操作、两侧输出（错误码/转移/状态）与判定依据；
// 正常重放可用 `go test -v` 查看，失败时该组日志始终输出。
func TestDifferentialAgainstNaiveSimulator(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261001))
	for seq := 0; seq < sequences; seq++ {
		salt := "salt-" + strconv.Itoa(rng.Intn(5))
		steps := randomSteps(rng)
		ops := randomOps(rng)
		log := fmt.Sprintf("seq %d salt=%q steps=%v\n", seq, salt, steps)

		actual, err := New(salt, steps)
		ref := newRef(salt, steps)
		if err != nil {
			t.Fatalf("[%s] unexpected construction error: %v", log, err)
		}

		for oi, op := range ops {
			var aErr, rErr error
			var aTr, rTr []Transition
			var aIn, rIn bool
			var aStatus, rStatus StatusSnapshot
			switch op.kind {
			case opStart:
				aErr = actual.Start(op.now)
				rErr = ref.start(op.now)
			case opPause:
				aErr = actual.Pause(op.now)
				rErr = ref.pause(op.now)
			case opResume:
				aErr = actual.Resume(op.now)
				rErr = ref.resume(op.now)
			case opTick:
				aTr, aErr = actual.Tick(op.now)
				rTr, rErr = ref.tick(op.now)
			case opRollback:
				aErr = actual.Rollback(op.now)
				rErr = ref.rollback(op.now)
			case opInRollout:
				aIn, aErr = actual.InRollout(op.user)
				rIn, rErr = ref.inRollout(op.user)
			}
			aStatus = actual.Status()
			rStatus = ref.status()

			entry := fmt.Sprintf("  op[%d] kind=%s now=%d user=%q -> errs(%s|%s) tr=%v|%v in=%v|%v status=%+v|%+v",
				oi, kindName(op.kind), op.now, op.user,
				errCode(aErr), errCode(rErr), aTr, rTr, aIn, rIn, aStatus, rStatus)
			log += entry + "\n"
			t.Log(entry)

			basis := fmt.Sprintf("seq=%d op[%d]", seq, oi)
			if errCode(aErr) != errCode(rErr) {
				t.Fatalf("%s error mismatch: actual=%v ref=%v\n%s", basis, aErr, rErr, log)
			}
			if op.kind == opTick {
				if len(aTr) != len(rTr) {
					t.Fatalf("%s transition count mismatch: %v vs %v\n%s", basis, aTr, rTr, log)
				}
				for i := range aTr {
					if aTr[i] != rTr[i] {
						t.Fatalf("%s transition[%d] mismatch: %+v vs %+v\n%s", basis, i, aTr[i], rTr[i], log)
					}
				}
			}
			if op.kind == opInRollout && aIn != rIn {
				t.Fatalf("%s inRollout mismatch: %v vs %v\n%s", basis, aIn, rIn, log)
			}
			if aStatus != rStatus {
				t.Fatalf("%s status mismatch: actual=%+v ref=%+v\n%s", basis, aStatus, rStatus, log)
			}
		}
		t.Logf("seq %d: %d ops matched on errors, transitions, in-rollout flags and status; determinism holds because both models advance only at due=enteredAt+Hold+paused and bucket via FNV-1a(salt,0,user)%%10000", seq, len(ops))
	}
}

func kindName(k opKind) string {
	switch k {
	case opStart:
		return "Start"
	case opPause:
		return "Pause"
	case opResume:
		return "Resume"
	case opTick:
		return "Tick"
	case opRollback:
		return "Rollback"
	case opInRollout:
		return "InRollout"
	default:
		return "?"
	}
}

// TestConcurrentOperations 验证并发调用结果等价于某个串行顺序：
// 只有一个 Start 能在 Idle 上成功；随后的读写在 -race 下不出现数据竞争。
func TestConcurrentOperations(t *testing.T) {
	c := mustNew(t, "salt", exampleSteps())
	var wg sync.WaitGroup
	startErrs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			startErrs <- c.Start(0)
		}()
	}
	wg.Wait()
	close(startErrs)
	ok, rejected := 0, 0
	for err := range startErrs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInvalidState):
			rejected++
		default:
			t.Fatalf("unexpected Start error: %v", err)
		}
	}
	if ok != 1 || rejected != 15 {
		t.Fatalf("concurrent Start: ok=%d rejected=%d, want exactly 1 ok", ok, rejected)
	}

	stop := make(chan struct{})
	var wg2 sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			now := int64(1)
			for {
				select {
				case <-stop:
					return
				default:
				}
				now += 2
				_, _ = c.Tick(now)
				_ = c.Status()
				_, _ = c.InRollout("u")
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = c.Rollback(3000)
				_ = c.Start(4000)
			}
		}()
	}
	// 短暂并发后停止；-race 会捕获任何数据竞争。
	done := make(chan struct{})
	go func() { wg2.Wait(); close(done) }()
	for {
		c.Status()
		if s := c.Status(); s.State == Completed {
			break
		}
	}
	close(stop)
	<-done
}
