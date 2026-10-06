package planningpoker

import (
	"fmt"
	"math/rand"
	"testing"
)

type opKind int

const (
	opJoin opKind = iota
	opLeave
	opSetRole
	opStart
	opVote
	opUnvote
	opReveal
	opRevote
	opPeek
)

type genOp struct {
	kind opKind
	user string
	role Role
	card Card
	now  int64
}

// generateSequence 生成一条带随机配置与成员池的操作序列。
// 时间戳以较高概率单调递增，偶发回退以覆盖时钟检查；T 较小以频繁触发到期。
func generateSequence(rng *rand.Rand) (cfg Config, ops []genOp) {
	deckSize := 2 + rng.Intn(6)
	cards := make([]int, deckSize)
	v := 1 + rng.Intn(3)
	for i := range cards {
		v += 1 + rng.Intn(4)
		cards[i] = v
	}
	roundLimit := 1 + rng.Intn(5)
	t := 3 + rng.Intn(12)
	cfg = Config{Cards: cards, RoundLimit: roundLimit, RoundSeconds: t, AutoReveal: rng.Intn(2) == 0}

	pool := []string{"host", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	now := int64(0)
	n := 40 + rng.Intn(120)
	ops = make([]genOp, 0, n)
	for i := 0; i < n; i++ {
		if rng.Intn(10) != 0 {
			now += int64(rng.Intn(5)) // 多数操作时间前进 0~4
		} else {
			now -= int64(rng.Intn(3)) // 少量回退
			if now < 0 {
				now = 0
			}
		}
		u := pool[rng.Intn(len(pool))]
		k := opKind(rng.Intn(int(opPeek) + 1))
		op := genOp{kind: k, user: u, now: now}
		switch k {
		case opJoin, opSetRole:
			op.role = Role(rng.Intn(2))
		case opVote:
			if rng.Intn(8) == 0 {
				if rng.Intn(2) == 0 {
					op.card = CardUncertain
				} else {
					op.card = CardBreak
				}
			} else if rng.Intn(15) == 0 {
				op.card = numCard(999 + rng.Intn(5)) // 非法牌
			} else {
				op.card = numCard(cards[rng.Intn(len(cards))])
			}
		}
		ops = append(ops, op)
	}
	return cfg, ops
}

func cardKeyC(c Card) string {
	if c.Special {
		return "S" + c.Name
	}
	return fmt.Sprintf("N%d", c.Value)
}

func errCode(err error) oErr {
	switch {
	case err == nil:
		return oOK
	case errorIs(err, ErrInvalidArgument):
		return oInvalid
	case errorIs(err, ErrClockRewind):
		return oRewind
	case errorIs(err, ErrAlreadyExists):
		return oExists
	case errorIs(err, ErrNotInSession):
		return oNotMember
	case errorIs(err, ErrForbidden):
		return oForbidden
	case errorIs(err, ErrNoVotes):
		return oNoVotes
	case errorIs(err, ErrState):
		return oState
	default:
		return oErr("UNKNOWN")
	}
}

func errorIs(err error, target error) bool {
	type iser interface{ Is(error) bool }
	if err == target {
		return true
	}
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return false
}

func resultsEqual(got *RevealResult, want *oResult) string {
	if got == nil || want == nil {
		return "nil-ness differs"
	}
	if got.Round != want.round {
		return fmt.Sprintf("round got %d want %d", got.Round, want.round)
	}
	if got.RevealedAt != want.at {
		return fmt.Sprintf("at got %d want %d", got.RevealedAt, want.at)
	}
	if got.Category != want.category {
		return fmt.Sprintf("category got %d want %d (%s)", got.Category, want.category, want.reason)
	}
	if got.NumericVotes != want.numericVotes {
		return fmt.Sprintf("numericVotes got %d want %d", got.NumericVotes, want.numericVotes)
	}
	if got.Value != want.value {
		return fmt.Sprintf("value got %d want %d", got.Value, want.value)
	}
	if got.Final != want.final {
		return fmt.Sprintf("final got %v want %v", got.Final, want.final)
	}
	if len(got.Distribution) != len(want.dist) {
		return "distribution length differs"
	}
	for i := range want.dist {
		if got.Distribution[i].Count != want.dist[i].count {
			return fmt.Sprintf("dist[%s] got %d want %d",
				cardKeyC(want.dist[i].card), got.Distribution[i].Count, want.dist[i].count)
		}
	}
	return ""
}

func peekEqual(got *PeekView, want *oPeek) string {
	if int(got.Phase) != int(want.phase) {
		return fmt.Sprintf("phase got %d want %d", got.Phase, want.phase)
	}
	if got.Round != want.round {
		return "round differs"
	}
	if got.VotedCount != want.votedCount {
		return fmt.Sprintf("votedCount got %d want %d", got.VotedCount, want.votedCount)
	}
	if got.VoterTotal != want.voterTotal {
		return fmt.Sprintf("voterTotal got %d want %d", got.VoterTotal, want.voterTotal)
	}
	ownNil := got.OwnVote == nil
	wantOwnNil := want.ownVote == nil
	if ownNil != wantOwnNil {
		return "ownvote presence differs"
	}
	if !ownNil && (got.OwnVote.Value != want.ownVote.value || got.OwnVote.Special != want.ownVote.special) {
		return "ownvote value differs"
	}
	if (got.Outcome == nil) != (want.outcome == nil) {
		return "outcome presence differs"
	}
	if got.Outcome != nil {
		if d := resultsEqual(got.Outcome, want.outcome); d != "" {
			return "peek outcome: " + d
		}
	}
	return ""
}

// TestDifferentialRandom 与独立朴素模型对照至少 1500 组随机操作序列，
// 打印每条输入、输出与判定依据；不一致立即失败并给出完整轨迹。
func TestDifferentialRandom(t *testing.T) {
	const sequences = 1500
	rng := rand.New(rand.NewSource(20261006))
	for seq := 0; seq < sequences; seq++ {
		cfg, ops := generateSequence(rng)
		s, err := NewSession("host", cfg, 0)
		if err != nil {
			t.Fatalf("seq %d new session: %v", seq, err)
		}
		o := newOracle("host", cfg.Cards, cfg.RoundLimit, cfg.RoundSeconds, cfg.AutoReveal, 0)

		tracer := func(format string, args ...any) {
			t.Logf("%s", fmt.Sprintf(format, args...))
		}
		tracer("=== seq %d cfg cards=%v R=%d T=%d auto=%v ops=%d ===",
			seq, cfg.Cards, cfg.RoundLimit, cfg.RoundSeconds, cfg.AutoReveal, len(ops))

		for step, op := range ops {
			var gotRes *RevealResult
			var gotErr error
			var gotPeek *PeekView

			switch op.kind {
			case opJoin:
				gotRes, gotErr = s.Join(op.user, op.role, op.now)
			case opLeave:
				gotRes, gotErr = s.Leave(op.user, op.now)
			case opSetRole:
				gotRes, gotErr = s.SetRole(op.user, op.role, op.now)
			case opStart:
				gotErr = s.Start(op.user, op.now)
			case opVote:
				gotRes, gotErr = s.Vote(op.user, op.card, op.now)
			case opUnvote:
				gotRes, gotErr = s.Unvote(op.user, op.now)
			case opReveal:
				gotRes, gotErr = s.Reveal(op.user, op.now)
			case opRevote:
				gotErr = s.Revote(op.user, op.now)
			case opPeek:
				gotPeek, gotErr = s.Peek(op.user, op.now)
			}

			var wantRes *oResult
			var wantErr oErr
			var wantPeek *oPeek
			switch op.kind {
			case opJoin:
				wantRes, wantErr = o.join(op.user, op.role, op.now)
			case opLeave:
				wantRes, wantErr = o.leave(op.user, op.now)
			case opSetRole:
				wantRes, wantErr = o.setRole(op.user, op.role, op.now)
			case opStart:
				wantErr = o.start(op.user, op.now)
			case opVote:
				wantRes, wantErr = o.vote(op.user, op.card.Value, op.card.Special, op.now)
			case opUnvote:
				wantRes, wantErr = o.unvote(op.user, op.now)
			case opReveal:
				wantRes, wantErr = o.revealOp(op.user, op.now)
			case opRevote:
				wantErr = o.revote(op.user, op.now)
			case opPeek:
				wantPeek, wantErr = o.peek(op.user, op.now)
			}

			kindName := opKindName(op.kind)
			tracer("  step %d t=%d %s user=%s -> implErr=%v oracleErr=%s",
				step, op.now, kindName, op.user, gotErr, wantErr)
			if gotRes != nil || wantRes != nil {
				tracer("    impl reveal cat=%d val=%d final=%v numeric=%d | oracle cat=%d val=%d final=%v numeric=%d reason=%q",
					catOf(gotRes), valOf(gotRes), finalOf(gotRes), numOf(gotRes),
					catO(wantRes), valO(wantRes), finO(wantRes), numO(wantRes), reasonO(wantRes))
			}

			if errCode(gotErr) != wantErr {
				t.Fatalf("seq %d step %d %s: error got %v(%v) want %s",
					seq, step, kindName, gotErr, errCode(gotErr), wantErr)
			}
			if op.kind == opPeek {
				if gotErr == nil {
					if d := peekEqual(gotPeek, wantPeek); d != "" {
						t.Fatalf("seq %d step %d peek mismatch: %s", seq, step, d)
					}
				}
			} else if op.kind != opStart && op.kind != opRevote {
				if (gotRes != nil) != (wantRes != nil) {
					t.Fatalf("seq %d step %d reveal presence: gotNil=%v wantNil=%v",
						seq, step, gotRes == nil, wantRes == nil)
				}
				if gotRes != nil {
					if d := resultsEqual(gotRes, wantRes); d != "" {
						t.Fatalf("seq %d step %d reveal mismatch: %s", seq, step, d)
					}
				}
			}
		}
	}
	t.Logf("differential sequences completed: %d", sequences)
}

func opKindName(k opKind) string {
	return [...]string{"Join", "Leave", "SetRole", "Start", "Vote", "Unvote", "Reveal", "Revote", "Peek"}[k]
}

func catOf(r *RevealResult) int {
	if r == nil {
		return -1
	}
	return int(r.Category)
}
func valOf(r *RevealResult) int {
	if r == nil {
		return 0
	}
	return r.Value
}
func finalOf(r *RevealResult) bool {
	if r == nil {
		return false
	}
	return r.Final
}
func numOf(r *RevealResult) int {
	if r == nil {
		return 0
	}
	return r.NumericVotes
}

func catO(r *oResult) int {
	if r == nil {
		return -1
	}
	return int(r.category)
}
func valO(r *oResult) int {
	if r == nil {
		return 0
	}
	return r.value
}
func finO(r *oResult) bool {
	if r == nil {
		return false
	}
	return r.final
}
func numO(r *oResult) int {
	if r == nil {
		return 0
	}
	return r.numericVotes
}
func reasonO(r *oResult) string {
	if r == nil {
		return ""
	}
	return r.reason
}
