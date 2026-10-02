package uniquetable

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// sim is a deliberately naive, literal step-by-step model of the rules.
// It exists only to cross-check Table against an independent
// implementation. Every method also returns a short reason string used as
// the judgment basis in test logs.
type sim struct {
	deferrable        bool
	initiallyDeferred bool

	committed map[string]*string

	inTx     bool
	deferred bool // current mode: true = DEFERRED
	data     map[string]*string
	touched  map[string]bool
}

func newSim(deferrable, initiallyDeferred bool) *sim {
	return &sim{
		deferrable:        deferrable,
		initiallyDeferred: initiallyDeferred,
		committed:         make(map[string]*string),
	}
}

func simCopy(src map[string]*string) map[string]*string {
	out := make(map[string]*string, len(src))
	for r, k := range src {
		if k == nil {
			out[r] = nil
		} else {
			c := *k
			out[r] = &c
		}
	}
	return out
}

// simRowViolation reports whether row currently violates uniqueness in
// data, i.e. its key is non-NULL and some other row holds the same key.
func simRowViolation(data map[string]*string, row string) (string, bool) {
	k, ok := data[row]
	if !ok || k == nil {
		return "", false
	}
	for r, other := range data {
		if r != row && other != nil && *other == *k {
			return *k, true
		}
	}
	return "", false
}

// simCheckRows returns the smallest violating key among rows, skipping
// rows that no longer exist.
func simCheckRows(data map[string]*string, rows []string) (string, bool) {
	found := false
	min := ""
	for _, r := range rows {
		if key, bad := simRowViolation(data, r); bad && (!found || key < min) {
			found = true
			min = key
		}
	}
	return min, found
}

func (s *sim) touchedExisting() []string {
	rows := make([]string, 0, len(s.touched))
	for r := range s.touched {
		if _, ok := s.data[r]; ok {
			rows = append(rows, r)
		}
	}
	sort.Strings(rows)
	return rows
}

func (s *sim) begin() (error, string) {
	if s.inTx {
		return ErrTxActive, "已有事务在进行"
	}
	s.inTx = true
	s.deferred = s.deferrable && s.initiallyDeferred
	s.data = simCopy(s.committed)
	s.touched = make(map[string]bool)
	return nil, "事务开始，模式置为初始模式"
}

func (s *sim) apply(ops []Op) (error, string) {
	if !s.inTx {
		return ErrNoTx, "没有事务"
	}
	work := simCopy(s.data)
	var stmtTouched []string
	for i, op := range ops {
		if op.Row == "" {
			return ErrEmptyRow, fmt.Sprintf("第%d个op行号为空，语句撤销", i)
		}
		switch op.Kind {
		case OpInsert:
			if _, ok := work[op.Row]; ok {
				return ErrRowExists, fmt.Sprintf("第%d个op Insert 行号%q已存在，语句撤销", i, op.Row)
			}
			work[op.Row] = op.Key
			stmtTouched = append(stmtTouched, op.Row)
		case OpUpdate:
			if _, ok := work[op.Row]; !ok {
				return ErrRowNotFound, fmt.Sprintf("第%d个op Update 行号%q不存在，语句撤销", i, op.Row)
			}
			work[op.Row] = op.Key
			stmtTouched = append(stmtTouched, op.Row)
		case OpDelete:
			if _, ok := work[op.Row]; !ok {
				return ErrRowNotFound, fmt.Sprintf("第%d个op Delete 行号%q不存在，语句撤销", i, op.Row)
			}
			delete(work, op.Row)
		}
		if !s.deferrable && op.Kind != OpDelete {
			if key, bad := simRowViolation(work, op.Row); bad {
				return &ViolationError{Key: key},
					fmt.Sprintf("非可延迟约束在第%d个op后即时检查行%q违例键%q，语句撤销", i, op.Row, key)
			}
		}
	}
	if s.deferrable && !s.deferred {
		if key, bad := simCheckRows(work, stmtTouched); bad {
			return &ViolationError{Key: key},
				fmt.Sprintf("即时模式语句末检查本语句触及行违例键%q，语句撤销", key)
		}
	}
	s.data = work
	for _, r := range stmtTouched {
		s.touched[r] = true
	}
	if s.deferrable && s.deferred {
		return nil, "延迟模式语句内不检查，语句成功"
	}
	return nil, "语句成功"
}

func (s *sim) setMode(m Mode) (error, string) {
	if !s.inTx {
		return ErrNoTx, "没有事务"
	}
	if m != Immediate && m != Deferred {
		return ErrInvalidMode, "模式不是 IMMEDIATE/DEFERRED"
	}
	if !s.deferrable {
		return ErrNotDeferrable, "约束不可延迟"
	}
	if m == Immediate {
		if key, bad := simCheckRows(s.data, s.touchedExisting()); bad {
			return &ViolationError{Key: key},
				fmt.Sprintf("切到IMMEDIATE检查事务内触及行违例键%q，拒绝且模式与状态不变", key)
		}
	}
	s.deferred = m == Deferred
	return nil, fmt.Sprintf("模式切换为%v", m)
}

func (s *sim) commit() (error, string) {
	if !s.inTx {
		return ErrNoTx, "没有事务"
	}
	if s.deferred {
		if key, bad := simCheckRows(s.data, s.touchedExisting()); bad {
			s.inTx = false
			s.data = nil
			s.touched = nil
			return &ViolationError{Key: key},
				fmt.Sprintf("延迟模式提交检查违例键%q，整个事务回滚", key)
		}
		s.committed = s.data
		s.inTx = false
		s.data = nil
		s.touched = nil
		return nil, "延迟模式提交检查通过，提交"
	}
	s.committed = s.data
	s.inTx = false
	s.data = nil
	s.touched = nil
	return nil, "即时模式直接提交"
}

func (s *sim) rollback() (error, string) {
	if !s.inTx {
		return ErrNoTx, "没有事务"
	}
	s.inTx = false
	s.data = nil
	s.touched = nil
	return nil, "放弃事务"
}

func (s *sim) view() map[string]*string {
	if s.inTx {
		return s.data
	}
	return s.committed
}

func (s *sim) get(row string) (*string, bool) {
	k, ok := s.view()[row]
	if !ok {
		return nil, false
	}
	if k == nil {
		return nil, true
	}
	c := *k
	return &c, true
}

func (s *sim) keys() []Entry {
	view := s.view()
	rows := make([]string, 0, len(view))
	for r := range view {
		rows = append(rows, r)
	}
	sort.Strings(rows)
	out := make([]Entry, 0, len(rows))
	for _, r := range rows {
		k := view[r]
		if k == nil {
			out = append(out, Entry{Row: r})
		} else {
			c := *k
			out = append(out, Entry{Row: r, Key: &c})
		}
	}
	return out
}

func errTag(err error) string {
	if err == nil {
		return "ok"
	}
	var verr *ViolationError
	if errors.As(err, &verr) {
		return "violation:" + verr.Key
	}
	return err.Error()
}

func fmtKey(k *string) string {
	if k == nil {
		return "NULL"
	}
	return fmt.Sprintf("%q", *k)
}

func fmtOp(op Op) string {
	switch op.Kind {
	case OpInsert:
		return fmt.Sprintf("Insert(%q,%s)", op.Row, fmtKey(op.Key))
	case OpUpdate:
		return fmt.Sprintf("Update(%q,%s)", op.Row, fmtKey(op.Key))
	default:
		return fmt.Sprintf("Delete(%q)", op.Row)
	}
}

func fmtEntries(entries []Entry) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, fmt.Sprintf("%q=%s", e.Row, fmtKey(e.Key)))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func fmtGet(key *string, found bool) string {
	if !found {
		return "未找到"
	}
	return fmtKey(key)
}

// TestDifferentialRandomSequences replays 2000 random operation sequences
// against both Table and the naive sim, requiring identical results and
// errors. Inputs, outputs and the judgment basis are logged per step.
func TestDifferentialRandomSequences(t *testing.T) {
	const sequences = 2000
	rowPool := []string{"a", "b", "c", "d"}
	keyPool := []string{"", "x", "y", "z"}

	randRow := func(r *rand.Rand) string {
		if r.Intn(12) == 0 {
			return ""
		}
		return rowPool[r.Intn(len(rowPool))]
	}
	randKey := func(r *rand.Rand) *string {
		if r.Intn(4) == 0 {
			return nil
		}
		return Str(keyPool[r.Intn(len(keyPool))])
	}

	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)))
		deferrable := r.Intn(2) == 0
		initiallyDeferred := deferrable && r.Intn(2) == 0

		tbl, err := New(deferrable, initiallyDeferred)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		sm := newSim(deferrable, initiallyDeferred)

		var log strings.Builder
		fmt.Fprintf(&log, "序列%d 输入: deferrable=%v initiallyDeferred=%v\n",
			seq, deferrable, initiallyDeferred)

		steps := 5 + r.Intn(36)
		for i := 0; i < steps; i++ {
			var tblErr, simErr error
			var input, basis string
			switch r.Intn(10) {
			case 0:
				input = "Begin()"
				tblErr = tbl.Begin()
				simErr, basis = sm.begin()
			case 1, 2, 3, 4:
				n := 1 + r.Intn(4)
				ops := make([]Op, 0, n)
				for j := 0; j < n; j++ {
					switch r.Intn(3) {
					case 0:
						ops = append(ops, Insert(randRow(r), randKey(r)))
					case 1:
						ops = append(ops, Update(randRow(r), randKey(r)))
					default:
						ops = append(ops, Delete(randRow(r)))
					}
				}
				parts := make([]string, 0, len(ops))
				for _, op := range ops {
					parts = append(parts, fmtOp(op))
				}
				input = "Apply([" + strings.Join(parts, " ") + "])"
				tblErr = tbl.Apply(ops)
				simErr, basis = sm.apply(ops)
			case 5:
				var m Mode
				switch r.Intn(4) {
				case 0:
					m = Immediate
				case 1:
					m = Deferred
				default:
					m = Mode(2 + r.Intn(5))
				}
				input = fmt.Sprintf("SetMode(%v)", m)
				tblErr = tbl.SetMode(m)
				simErr, basis = sm.setMode(m)
			case 6:
				input = "Commit()"
				tblErr = tbl.Commit()
				simErr, basis = sm.commit()
			case 7:
				input = "Rollback()"
				tblErr = tbl.Rollback()
				simErr, basis = sm.rollback()
			case 8:
				row := randRow(r)
				input = fmt.Sprintf("Get(%q)", row)
				tk, tf := tbl.Get(row)
				sk, sf := sm.get(row)
				if tf != sf || fmtGet(tk, tf) != fmtGet(sk, sf) {
					t.Fatalf("seq %d step %d %s: Table=%s,%v sim=%s,%v\n%s",
						seq, i, input, fmtGet(tk, tf), tf, fmtGet(sk, sf), sf, log.String())
				}
				basis = "读事务内视图或已提交状态"
				fmt.Fprintf(&log, "  步骤%d 输入:%s 输出:%s 判定依据:%s\n",
					i, input, fmtGet(tk, tf), basis)
				continue
			default:
				input = "Keys()"
				tk := tbl.Keys()
				sk := sm.keys()
				if fmtEntries(tk) != fmtEntries(sk) {
					t.Fatalf("seq %d step %d %s: Table=%s sim=%s\n%s",
						seq, i, input, fmtEntries(tk), fmtEntries(sk), log.String())
				}
				basis = "按行号字节序返回(行号,键)序列"
				fmt.Fprintf(&log, "  步骤%d 输入:%s 输出:%s 判定依据:%s\n",
					i, input, fmtEntries(tk), basis)
				continue
			}

			fmt.Fprintf(&log, "  步骤%d 输入:%s 输出:%s 判定依据:%s\n",
				i, input, errTag(tblErr), basis)
			if errTag(tblErr) != errTag(simErr) {
				t.Fatalf("seq %d step %d %s: Table=%s sim=%s (依据:%s)\n%s",
					seq, i, input, errTag(tblErr), errTag(simErr), basis, log.String())
			}

			// The committed state must never hold duplicate non-NULL keys.
			if input == "Commit()" && tblErr == nil {
				seen := make(map[string]string)
				for _, e := range tbl.Keys() {
					if e.Key == nil {
						continue
					}
					if prev, dup := seen[*e.Key]; dup {
						t.Fatalf("seq %d step %d: committed state has duplicate key %q on rows %q and %q\n%s",
							seq, i, *e.Key, prev, e.Row, log.String())
					}
					seen[*e.Key] = e.Row
				}
			}
		}
		t.Log(log.String())
	}
}
