package stm

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 朴素模拟：按题目规则逐步写成的独立参照实现，与 Manager 不共享任何代码。

type simTxn struct {
	kp    int
	ab    int
	att   []int
	holds []Mode
	st    status
}

type naiveSim struct {
	cfg  Config
	txns []*simTxn
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{cfg: cfg}
}

func (s *naiveSim) begin() int {
	s.txns = append(s.txns, &simTxn{
		att:   make([]int, s.cfg.M),
		holds: make([]Mode, s.cfg.M),
		st:    stActive,
	})
	return len(s.txns)
}

func (s *naiveSim) find(t int) (*simTxn, error) {
	if t < 1 || t > len(s.txns) {
		return nil, ErrNoSuchTxn
	}
	return s.txns[t-1], nil
}

func (s *naiveSim) open(t, o int, write bool) (OpenResult, error) {
	tx, err := s.find(t)
	if err != nil {
		return OpenResult{}, err
	}
	if tx.st != stActive {
		return OpenResult{}, ErrBadState
	}
	if o < 0 || o >= s.cfg.M {
		return OpenResult{}, ErrBadObject
	}
	want := ModeRead
	if write {
		want = ModeWrite
	}
	if tx.holds[o] >= want {
		return OpenResult{Acquired: true}, nil
	}
	var adv []int
	for i, e := range s.txns {
		if i == t-1 {
			continue
		}
		h := e.holds[o]
		if h == ModeNone {
			continue
		}
		if !write && h == ModeRead {
			continue
		}
		adv = append(adv, i+1)
	}
	if len(adv) == 0 {
		if tx.holds[o] == ModeNone && tx.kp < s.cfg.P {
			tx.kp++
		}
		tx.holds[o] = want
		tx.att[o] = 0
		return OpenResult{Acquired: true}, nil
	}
	k := tx.att[o]
	winAll := true
	for _, eid := range adv {
		e := s.txns[eid-1]
		tPriv := tx.ab >= s.cfg.L
		ePriv := e.ab >= s.cfg.L
		var win bool
		switch {
		case tPriv && ePriv:
			win = t < eid
		case tPriv:
			win = true
		case ePriv:
			win = false
		default:
			win = k >= s.cfg.Q || tx.kp+k > e.kp
		}
		if !win {
			winAll = false
			break
		}
	}
	if !winAll {
		tx.att[o] = k + 1
		exp := k
		if exp > s.cfg.E {
			exp = s.cfg.E
		}
		return OpenResult{Delay: s.cfg.D * (1 << exp)}, nil
	}
	for _, eid := range adv {
		e := s.txns[eid-1]
		e.st = stAborted
		e.ab++
		e.kp = (e.kp + 1) / 2
		for j := range e.holds {
			e.holds[j] = ModeNone
			e.att[j] = 0
		}
	}
	if tx.holds[o] == ModeNone && tx.kp < s.cfg.P {
		tx.kp++
	}
	tx.holds[o] = want
	tx.att[o] = 0
	return OpenResult{Acquired: true, Aborted: adv}, nil
}

func (s *naiveSim) restart(t int) (int, error) {
	tx, err := s.find(t)
	if err != nil {
		return 0, err
	}
	if tx.st != stAborted {
		return 0, ErrBadState
	}
	tx.st = stActive
	for j := range tx.holds {
		tx.holds[j] = ModeNone
		tx.att[j] = 0
	}
	exp := tx.ab - 1
	if exp < 0 {
		exp = 0
	}
	if exp > s.cfg.E {
		exp = s.cfg.E
	}
	return s.cfg.D * (1 << exp), nil
}

func (s *naiveSim) finish(t int, st status) error {
	tx, err := s.find(t)
	if err != nil {
		return err
	}
	if tx.st != stActive {
		return ErrBadState
	}
	tx.st = st
	for j := range tx.holds {
		tx.holds[j] = ModeNone
		tx.att[j] = 0
	}
	return nil
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "无"
	case errors.Is(err, ErrNoSuchTxn):
		return "事务号不存在"
	case errors.Is(err, ErrBadState):
		return "状态不符"
	case errors.Is(err, ErrBadObject):
		return "对象越界"
	default:
		return "未知错误"
	}
}

// stateMismatch 比较 Manager 与朴素模拟的全量内部状态。
func stateMismatch(m *Manager, s *naiveSim) string {
	if len(m.txns) != len(s.txns) {
		return fmt.Sprintf("事务数: manager=%d sim=%d", len(m.txns), len(s.txns))
	}
	for i := range m.txns {
		a, b := m.txns[i], s.txns[i]
		if a.kp != b.kp || a.ab != b.ab || a.st != b.st ||
			!reflect.DeepEqual(a.att, b.att) || !reflect.DeepEqual(a.holds, b.holds) {
			return fmt.Sprintf("事务 %d: manager={kp:%d ab:%d st:%d att:%v holds:%v} sim={kp:%d ab:%d st:%d att:%v holds:%v}",
				i+1, a.kp, a.ab, a.st, a.att, a.holds, b.kp, b.ab, b.st, b.att, b.holds)
		}
	}
	return ""
}

// TestRandomAgainstNaiveSim 随机生成 2000 组调用序列，逐步与朴素模拟对照。
// 日志打印输入（配置与每步调用）、输出（返回结果）与判定依据（逐项对比说明）。
func TestRandomAgainstNaiveSim(t *testing.T) {
	const sequences = 2000
	failures := 0
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq) + 1))
		cfg := Config{
			M: 1 + rng.Intn(6),
			L: 1 + rng.Intn(3),
			D: 1 + rng.Intn(5),
			E: rng.Intn(4),
			P: 1 + rng.Intn(6),
			Q: 1 + rng.Intn(4),
		}
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("序列 %d: 合法配置被拒: %v", seq, err)
		}
		sim := newNaiveSim(cfg)
		ops := 20 + rng.Intn(21)
		log := []string{fmt.Sprintf("序列 %d 输入: 配置 %+v, 步数 %d", seq, cfg, ops)}
		bad := ""
		for i := 0; i < ops && bad == ""; i++ {
			n := len(sim.txns)
			switch op := rng.Intn(100); {
			case op < 15:
				got, want := m.Begin(), sim.begin()
				log = append(log, fmt.Sprintf("  步 %d Begin() => %d (期望 %d)", i, got, want))
				if got != want {
					bad = fmt.Sprintf("Begin 返回 %d, 模拟 %d", got, want)
				}
			case op < 60:
				id, o := 1+rng.Intn(n+2), -1+rng.Intn(cfg.M+2)
				w := rng.Intn(2) == 0
				gr, ge := m.Open(id, o, w)
				wr, we := sim.open(id, o, w)
				log = append(log, fmt.Sprintf("  步 %d Open(t=%d,o=%d,写=%v) => %+v 拒绝=%s (模拟 %+v 拒绝=%s)",
					i, id, o, w, gr, errKind(ge), wr, errKind(we)))
				if !reflect.DeepEqual(gr, wr) || errKind(ge) != errKind(we) {
					bad = fmt.Sprintf("Open 结果不一致: manager=%+v/%s sim=%+v/%s", gr, errKind(ge), wr, errKind(we))
				}
			case op < 75:
				id := 1 + rng.Intn(n+2)
				gd, ge := m.Restart(id)
				wd, we := sim.restart(id)
				log = append(log, fmt.Sprintf("  步 %d Restart(%d) => 延迟 %d 拒绝=%s (模拟 延迟 %d 拒绝=%s)",
					i, id, gd, errKind(ge), wd, errKind(we)))
				if gd != wd || errKind(ge) != errKind(we) {
					bad = fmt.Sprintf("Restart 不一致: manager=%d/%s sim=%d/%s", gd, errKind(ge), wd, errKind(we))
				}
			default:
				id := 1 + rng.Intn(n+2)
				commit := rng.Intn(2) == 0
				var ge, we error
				name := "Abort"
				if commit {
					name = "Commit"
					ge, we = m.Commit(id), sim.finish(id, stCommitted)
				} else {
					ge, we = m.Abort(id), sim.finish(id, stAborted)
				}
				log = append(log, fmt.Sprintf("  步 %d %s(%d) => 拒绝=%s (模拟 拒绝=%s)", i, name, id, errKind(ge), errKind(we)))
				if errKind(ge) != errKind(we) {
					bad = fmt.Sprintf("%s 不一致: manager=%s sim=%s", name, errKind(ge), errKind(we))
				}
			}
			if bad == "" {
				if msg := stateMismatch(m, sim); msg != "" {
					bad = "状态不一致: " + msg
				}
			}
		}
		if bad != "" {
			log = append(log, "判定依据: 逐步对比返回值/延迟/被中止列表/拒绝类别及全量状态(kp,ab,att,持有,状态), 发现: "+bad)
			for _, line := range log {
				t.Log(line)
			}
			t.Errorf("序列 %d 对照失败", seq)
			if failures++; failures >= 5 {
				t.Fatalf("失败过多, 停止")
			}
			continue
		}
		log = append(log, fmt.Sprintf("判定依据: %d 步调用的返回值、延迟、被中止列表、拒绝类别与全量状态(kp,ab,att,持有,状态)逐步一致", ops))
		for _, line := range log {
			t.Log(line)
		}
	}
}
