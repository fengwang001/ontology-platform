package approval

import (
	"errors"
	"math/rand"
	"strconv"
	"testing"
)

// virtualCopy 推演 r 到 now 的只读副本，不改变参考模型已提交状态。
func advanceCopy(r *naiveReq, t, now int64) (pos int, ta int64, oc Outcome, finalAt int64) {
	pos, ta, oc, finalAt = r.pos, r.ta, r.outcome, r.finalAt
	for oc == Pending && ta+t <= now {
		pos++
		ta += t
		if pos >= len(r.cand) {
			return pos - 1, ta - t, Expired, ta
		}
	}
	return pos, ta, oc, finalAt
}

// 朴素参考模型：逐时刻逐级推演到期，终局后冻结，实时复核额度。
type naiveReq struct {
	cand    []string
	amt     int64
	pos     int
	ta      int64
	outcome Outcome
	finalAt int64
}

type naiveModel struct {
	manager map[string]string
	limit   map[string]int64
	t       int64
	reqs    map[string]*naiveReq
}

func (m *naiveModel) advance(r *naiveReq, now int64) {
	for r.outcome == Pending && r.ta+m.t <= now {
		r.pos++
		r.ta += m.t
		if r.pos >= len(r.cand) {
			r.outcome, r.finalAt, r.pos, r.ta = Expired, r.ta, len(r.cand)-1, r.ta-m.t
		}
	}
}

func (m *naiveModel) submit(req, a string, amt, now int64) error {
	if _, ok := m.reqs[req]; ok {
		return ErrExist
	}
	var cand []string
	seen := map[string]bool{a: true}
	cur, ok := m.manager[a]
	for ok && !seen[cur] {
		seen[cur] = true
		if m.limit[cur] >= amt {
			cand = append(cand, cur)
		}
		cur, ok = m.manager[cur]
	}
	if len(cand) == 0 {
		return ErrNoApprover
	}
	m.reqs[req] = &naiveReq{cand: cand, amt: amt, ta: now}
	return nil
}

// TestRandomNaive：随机 Submit/SetLimit/Decide/Status 序列，引擎结果须与
// 朴素逐级推演逐操作一致（状态、当前审批人、ta、终局、时刻）。
func TestRandomNaive(t *testing.T) {
	const trials, people, T = 120, 6, int64(8)
	names := []string{"A", "B", "C", "D", "E", "F"}
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < trials; trial++ {
		e, o := newTestEngine(t, T)
		m := &naiveModel{
			manager: map[string]string{},
			limit:   map[string]int64{},
			t:       T,
			reqs:    map[string]*naiveReq{},
		}
		// 固定一条无环链 A→B→…，提交后再随机改额度（不改链，测冻结+撤权）。
		buildChain(t, o, names)
		for i := 1; i < people; i++ {
			m.manager[names[i-1]] = names[i]
			v := int64(rng.Intn(3)) * 100
			mustLimit(t, o, names[i], v)
			m.limit[names[i]] = v
		}
		now := int64(0)
		for step := 0; step < 120; step++ {
			now += int64(rng.Intn(7))
			switch rng.Intn(4) {
			case 0: // 随机改某个审批人额度（不带时钟，不触发升级）
				n := names[1+rng.Intn(people-1)]
				v := int64(rng.Intn(4)) * 100
				errE := o.SetLimit(n, v)
				m.limit[n] = v
				if errE != nil {
					t.Fatalf("setlimit unexpected err %v", errE)
				}
			case 1: // 提交
				req := "r" + strconv.Itoa(rng.Intn(12))
				amt := int64(1+rng.Intn(4)) * 100
				errE := e.Submit(req, "A", amt, now)
				errM := m.submit(req, "A", amt, now)
				if code(errE) != code(errM) {
					t.Fatalf("submit mismatch %v vs %v", errE, errM)
				}
			case 2: // 决策：随机挑候选位置的人，制造 ErrNotAssignee
				req := "r" + strconv.Itoa(rng.Intn(12))
				r, ok := m.reqs[req]
				who := names[1+rng.Intn(people-1)]
				approve := rng.Intn(2) == 0
				errE := e.Decide(req, who, approve, now)
				var errM error
				if !ok {
					errM = ErrNotFound
				} else {
					pos, ta, oc, _ := advanceCopy(r, T, now)
					switch {
					case oc != Pending:
						errM = ErrClosed
					case who != r.cand[pos]:
						errM = ErrNotAssignee
					case m.limit[who] < r.amt:
						errM = ErrRevoked
					default:
						// 与引擎相同：成功时逐级升级在此刻落实。
						m.advance(r, now)
						if approve {
							r.outcome, r.finalAt = Approved, now
						} else {
							r.outcome, r.finalAt = Rejected, now
						}
						_ = ta
					}
				}
				if code(errE) != code(errM) {
					t.Fatalf("decide mismatch req=%s now=%d %v vs %v", req, now, errE, errM)
				}
			default: // 状态对照（只读，now 不小于时钟）
				req := "r" + strconv.Itoa(rng.Intn(12))
				st, errE := e.Status(req, now)
				r, ok := m.reqs[req]
				if !ok {
					if !errors.Is(errE, ErrNotFound) {
						t.Fatalf("status err=%v want ErrNotFound", errE)
					}
					continue
				}
				pos, ta, oc, finalAt := advanceCopy(r, T, now)
				if errE != nil || st.Outcome != oc || st.FinalAt != finalAt ||
					(oc == Pending && (st.Assignee != r.cand[pos] || st.AssignedAt != ta)) {
					t.Fatalf("status mismatch req=%s now=%d eng=%+v naive=%+v", req, now, st, r)
				}
			}
		}
		t.Logf("trial %d: 120 个随机操作与朴素逐级推演完全一致", trial)
	}
}

func code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrExist):
		return "exist"
	case errors.Is(err, ErrNoApprover):
		return "noapprover"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrClosed):
		return "closed"
	case errors.Is(err, ErrNotAssignee):
		return "assignee"
	case errors.Is(err, ErrRevoked):
		return "revoked"
	case errors.Is(err, ErrClock):
		return "clock"
	default:
		return "other"
	}
}
