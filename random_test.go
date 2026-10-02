package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveState struct {
	localMiss   int64
	banCount    int64
	bannedUntil int64
}

type naiveExecutor struct {
	patterns  map[string]*program
	memos     map[string]bool
	state     map[string]*naiveState
	maxNow    int64
	epoch     int64
	remaining int64
	local     int64
	epochLen  int64
	global    int64
	threshold int64
	ban       int64
}

func newNaive(local, epochLen, global, threshold, ban int64) *naiveExecutor {
	return &naiveExecutor{
		patterns:  make(map[string]*program),
		memos:     make(map[string]bool),
		state:     make(map[string]*naiveState),
		remaining: global,
		local:     local,
		epochLen:  epochLen,
		global:    global,
		threshold: threshold,
		ban:       ban,
	}
}

func (n *naiveExecutor) register(id string, pattern []byte, memo bool) error {
	root, err := parsePattern(pattern)
	if err != nil {
		return err
	}
	prog, err := compilePattern(root, 100000)
	if err != nil {
		return err
	}
	n.patterns[id] = prog
	n.memos[id] = memo
	n.state[id] = &naiveState{}
	return nil
}

type naiveResult struct {
	kind  string
	start int
	end   int
	steps int64
	err   error
}

func (n *naiveExecutor) match(id string, input []byte, now int64) naiveResult {
	if len(id) == 0 || len(id) > 64 || len(input) > 65536 || now < 0 || now > 1_000_000_000_000_000 {
		return naiveResult{err: ErrInvalidArgument}
	}
	prog, ok := n.patterns[id]
	if !ok {
		return naiveResult{err: ErrNotFound}
	}
	state := n.state[id]
	if now < n.maxNow {
		return naiveResult{err: ErrClockRewind}
	}
	if now < state.bannedUntil {
		return naiveResult{err: ErrBanned}
	}
	epoch := now / n.epochLen
	rem := n.remaining
	if epoch != n.epoch {
		rem = n.global
	}
	if rem == 0 {
		return naiveResult{err: ErrGlobalBudget}
	}
	limit := rem
	kind := OutcomeGlobalLimited
	if rem >= n.local {
		limit = n.local
		kind = OutcomeLocalLimited
	}

	out := naiveRun(prog, input, limit, n.memos[id], kind)
	n.maxNow = now
	if epoch != n.epoch {
		n.epoch = epoch
		n.remaining = n.global
	}
	n.remaining -= out.steps
	if out.kind == OutcomeLocalLimited {
		state.localMiss++
		if state.localMiss >= n.threshold {
			state.banCount++
			mult := int64(1)
			if state.banCount < 4 {
				mult = int64(1) << (state.banCount - 1)
			} else {
				mult = 8
			}
			state.bannedUntil = now + n.ban*mult
			state.localMiss = 0
		}
	} else if out.kind == OutcomeMatch || out.kind == OutcomeNoMatch {
		state.localMiss = 0
	}
	return naiveResult{kind: out.kind, start: out.start, end: out.end, steps: out.steps}
}

type naiveFrame struct {
	pc  int
	pos int
}

func naiveRun(prog *program, input []byte, limit int64, memo bool, limitKind string) executionOutcome {
	seen := map[executionState]struct{}{}
	frames := make([]naiveFrame, 0, len(prog.code))
	steps := int64(0)
	start := 0
	for start <= len(input) {
		frame := naiveFrame{pc: 0, pos: start}
		frames = frames[:0]
	currentStart:
		for {
			if memo {
				key := executionState{pc: frame.pc, pos: frame.pos}
				if _, duplicated := seen[key]; duplicated {
					if len(frames) == 0 {
						break currentStart
					}
					frame = frames[len(frames)-1]
					frames = frames[:len(frames)-1]
					continue
				}
				seen[key] = struct{}{}
			}
			if steps == limit {
				return executionOutcome{kind: limitKind, steps: steps}
			}
			steps++
			inst := prog.code[frame.pc]
			switch inst.op {
			case opChar:
				if frame.pos >= len(input) || input[frame.pos] != inst.byte {
					goto pop
				}
				frame.pc++
				frame.pos++
			case opAny:
				if frame.pos >= len(input) || input[frame.pos] == '\n' {
					goto pop
				}
				frame.pc++
				frame.pos++
			case opClass:
				if frame.pos >= len(input) || !inst.mask[input[frame.pos]] {
					goto pop
				}
				frame.pc++
				frame.pos++
			case opStartAssert:
				if frame.pos != 0 {
					goto pop
				}
				frame.pc++
			case opEndAssert:
				if frame.pos != len(input) {
					goto pop
				}
				frame.pc++
			case opSplit:
				frames = append(frames, naiveFrame{pc: inst.second, pos: frame.pos})
				frame.pc = inst.first
			case opJmp:
				frame.pc = inst.first
			case opMatch:
				return executionOutcome{kind: OutcomeMatch, start: start, end: frame.pos, steps: steps}
			}
			continue
		pop:
			if len(frames) == 0 {
				break currentStart
			}
			frame = frames[len(frames)-1]
			frames = frames[:len(frames)-1]
		}
		start++
	}
	return executionOutcome{kind: OutcomeNoMatch, steps: steps}
}

func TestRandomizedNaiveSimulation2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	patterns := []string{
		`a`, `b`, `ab`, `a*b`, `a+b?`, `a{0,2}b`, `a{2,3}?b`,
		`ab|cd`, `(ab|c)*d`, `[ab]+c`, `[^ab]+`, `.b`, `^a$`, `a$`,
		`x?y?z`, `(a|b)*abb`, `a{1,}`, `\*`, `[a-cx-]+`,
	}
	inputs := [][]byte{
		nil,
		[]byte(""),
		[]byte("a"),
		[]byte("b"),
		[]byte("ab"),
		[]byte("ac"),
		[]byte("aab"),
		[]byte("abb"),
		[]byte("cd"),
		[]byte("xyz"),
		[]byte("aaa"),
		[]byte("*"),
		[]byte("d"),
	}

	for iteration := 0; iteration < 2000; iteration++ {
		local := int64(1 + rng.Intn(40))
		epochLen := int64(1 + rng.Intn(20))
		global := int64(1 + rng.Intn(80))
		threshold := int64(1 + rng.Intn(3))
		banDuration := int64(1 + rng.Intn(20))
		actual := newTestExecutor(t, local, epochLen, global, threshold, banDuration, 100000)
		reference := newNaive(local, epochLen, global, threshold, banDuration)
		ids := []string{"p1", "p2", "p3"}
		now := int64(0)

		t.Logf("iter=%d config={L:%d E:%d G:%d K:%d D:%d}", iteration, local, epochLen, global, threshold, banDuration)
		for i, pattern := range patterns {
			if i >= len(ids) {
				break
			}
			memo := rng.Intn(2) == 0
			errA := actual.Register(ids[i], []byte(pattern), memo)
			errR := reference.register(ids[i], []byte(pattern), memo)
			if fmt.Sprint(errA) != fmt.Sprint(errR) {
				t.Fatalf("register mismatch pattern=%s: %v vs %v", pattern, errA, errR)
			}
		}

		for op := 0; op < 10; op++ {
			id := ids[rng.Intn(len(ids))]
			input := inputs[rng.Intn(len(inputs))]
			switch rng.Intn(3) {
			case 0:
				if rng.Intn(5) == 0 {
					now--
				} else {
					now += int64(rng.Intn(25))
				}
			case 1:
				now += int64(rng.Intn(30))
			}
			if now < 0 {
				now = 0
			}
			got, errA := actual.Match(id, input, now)
			want := reference.match(id, input, now)
			t.Logf("input=%q now=%d output={kind:%s start:%d end:%d steps:%d err:%v} basis={refKind:%s refErr:%v actual={rem:%d c:%d b:%d u:%d} reference={rem:%d c:%d b:%d u:%d}}",
				input, now, got.Kind, got.Start, got.End, got.Steps, errA,
				want.kind, want.err, actual.remaining,
				actual.patterns[id].localMiss, actual.patterns[id].banCount, actual.patterns[id].bannedUntil,
				reference.remaining, reference.state[id].localMiss, reference.state[id].banCount, reference.state[id].bannedUntil)
			if fmt.Sprint(errA) != fmt.Sprint(want.err) || got.Kind != want.kind ||
				got.Start != want.start || got.End != want.end || got.Steps != want.steps ||
				actual.remaining != reference.remaining || actual.maxNow != reference.maxNow ||
				actual.currentEpoch != reference.epoch {
				t.Fatalf("match mismatch iteration=%d id=%s input=%q now=%d", iteration, id, input, now)
			}
			patternState := actual.patterns[id]
			refState := reference.state[id]
			if patternState.localMiss != refState.localMiss ||
				patternState.banCount != refState.banCount ||
				patternState.bannedUntil != refState.bannedUntil {
				t.Fatalf("pattern state mismatch iteration=%d", iteration)
			}
		}
	}
}
