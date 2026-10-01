package rollout

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// naive 是按规格逐条直译的朴素模拟器, 用于与 Controller 对拍。
type naive struct {
	salt       string
	steps      []Step
	state      State
	level      int
	enteredAt  int64
	paused     int64
	pauseStart int64
	maxNow     int64
	hasNow     bool
}

func newNaive(salt string, steps []Step) *naive {
	return &naive{salt: salt, steps: steps, state: Idle, level: -1}
}

func (n *naive) clock(now int64) error {
	if n.hasNow && now < n.maxNow {
		return ErrClockBackwards
	}
	if !n.hasNow || now > n.maxNow {
		n.maxNow = now
	}
	n.hasNow = true
	return nil
}

func (n *naive) start(now int64) error {
	if err := n.clock(now); err != nil {
		return err
	}
	if n.state != Idle && n.state != RolledBack {
		return ErrInvalidState
	}
	n.state, n.level, n.enteredAt, n.paused = Running, 0, now, 0
	return nil
}

func (n *naive) pause(now int64) error {
	if err := n.clock(now); err != nil {
		return err
	}
	if n.state != Running {
		return ErrInvalidState
	}
	n.state, n.pauseStart = Paused, now
	return nil
}

func (n *naive) resume(now int64) error {
	if err := n.clock(now); err != nil {
		return err
	}
	if n.state != Paused {
		return ErrInvalidState
	}
	n.paused += now - n.pauseStart
	n.state = Running
	return nil
}

func (n *naive) tick(now int64) ([]Transition, error) {
	if err := n.clock(now); err != nil {
		return nil, err
	}
	var out []Transition
	if n.state != Running {
		return out, nil
	}
	for n.level < len(n.steps)-1 {
		due := n.enteredAt + n.steps[n.level].Hold + n.paused
		if now < due {
			break
		}
		out = append(out, Transition{n.level, n.level + 1, due})
		n.level, n.enteredAt, n.paused = n.level+1, due, 0
	}
	if n.level == len(n.steps)-1 {
		n.state = Completed
	}
	return out, nil
}

func (n *naive) rollback(now int64) error {
	if err := n.clock(now); err != nil {
		return err
	}
	if n.state != Running && n.state != Paused && n.state != Completed {
		return ErrInvalidState
	}
	n.state, n.level, n.paused = RolledBack, -1, 0
	return nil
}

func (n *naive) status() (State, int, int) {
	if n.state == Idle || n.state == RolledBack {
		return n.state, -1, 0
	}
	return n.state, n.level, n.steps[n.level].Percent
}

func (n *naive) inRollout(user string) (bool, error) {
	if user == "" {
		return false, ErrEmptyUser
	}
	h := uint32(2166136261)
	for _, b := range []byte(n.salt + "\x00" + user) {
		h ^= uint32(b)
		h *= 16777619
	}
	_, _, pct := n.status()
	return h%10000 < uint32(pct)*100, nil
}

// op 描述一次随机操作。
type op struct {
	kind string
	now  int64
	user string
}

func (o op) String() string {
	if o.kind == "InRollout" {
		return fmt.Sprintf("%s(%q)", o.kind, o.user)
	}
	if o.kind == "Status" {
		return o.kind + "()"
	}
	return fmt.Sprintf("%s(%d)", o.kind, o.now)
}

func applyOp(c *Controller, n *naive, o op) (gotOut, wantOut string, err error) {
	cmp := func(gerr, werr error) error {
		if (gerr == nil) != (werr == nil) {
			return fmt.Errorf("错误不一致: controller=%v naive=%v", gerr, werr)
		}
		if gerr != nil && !errors.Is(gerr, werr) {
			return fmt.Errorf("错误原因不一致: controller=%v naive=%v", gerr, werr)
		}
		return nil
	}
	switch o.kind {
	case "Start":
		return "", "", cmp(c.Start(o.now), n.start(o.now))
	case "Pause":
		return "", "", cmp(c.Pause(o.now), n.pause(o.now))
	case "Resume":
		return "", "", cmp(c.Resume(o.now), n.resume(o.now))
	case "Rollback":
		return "", "", cmp(c.Rollback(o.now), n.rollback(o.now))
	case "Tick":
		gtrs, gerr := c.Tick(o.now)
		wtrs, werr := n.tick(o.now)
		if err := cmp(gerr, werr); err != nil {
			return "", "", err
		}
		if len(gtrs) != len(wtrs) {
			return "", "", fmt.Errorf("转移数量不一致: controller=%v naive=%v", gtrs, wtrs)
		}
		for i := range gtrs {
			if gtrs[i] != wtrs[i] {
				return "", "", fmt.Errorf("转移不一致: controller=%v naive=%v", gtrs, wtrs)
			}
		}
		return fmt.Sprintf("%v", gtrs), fmt.Sprintf("%v", wtrs), nil
	case "Status":
		gs, gl, gp := c.Status()
		ws, wl, wp := n.status()
		if gs != ws || gl != wl || gp != wp {
			return "", "", fmt.Errorf("Status 不一致: controller=(%v,%d,%d) naive=(%v,%d,%d)", gs, gl, gp, ws, wl, wp)
		}
		return fmt.Sprintf("(%v,%d,%d)", gs, gl, gp), fmt.Sprintf("(%v,%d,%d)", ws, wl, wp), nil
	case "InRollout":
		g, gerr := c.InRollout(o.user)
		w, werr := n.inRollout(o.user)
		if err := cmp(gerr, werr); err != nil {
			return "", "", err
		}
		if g != w {
			return "", "", fmt.Errorf("InRollout 不一致: controller=%v naive=%v", g, w)
		}
		return fmt.Sprintf("%v", g), fmt.Sprintf("%v", w), nil
	}
	return "", "", fmt.Errorf("未知操作 %q", o.kind)
}

// 判定依据: 根据操作与前置状态说明期望结果的来源。
func basis(n *naive, o op) string {
	if o.kind != "Status" && o.kind != "InRollout" && n.hasNow && o.now < n.maxNow {
		return fmt.Sprintf("时钟回拨: now=%d < maxNow=%d, 应拒", o.now, n.maxNow)
	}
	s, lvl, pct := n.status()
	switch o.kind {
	case "Start":
		return fmt.Sprintf("前置状态 %v, 仅 Idle/RolledBack 可 Start", s)
	case "Pause":
		return fmt.Sprintf("前置状态 %v, 仅 Running 可 Pause", s)
	case "Resume":
		return fmt.Sprintf("前置状态 %v, 仅 Paused 可 Resume", s)
	case "Rollback":
		return fmt.Sprintf("前置状态 %v, Idle/RolledBack 拒绝 Rollback", s)
	case "Tick":
		if s != Running {
			return fmt.Sprintf("状态 %v 非 Running, Tick 无转移", s)
		}
		due := n.enteredAt + n.steps[lvl].Hold + n.paused
		return fmt.Sprintf("第 %d 级 due=%d+%d+%d=%d, now=%d", lvl, n.enteredAt, n.steps[lvl].Hold, n.paused, due, o.now)
	case "Status":
		return fmt.Sprintf("当前 (%v, level=%d, pct=%d)", s, lvl, pct)
	case "InRollout":
		return fmt.Sprintf("当前百分比 %d, 阈值 %d/10000", pct, pct*100)
	}
	return ""
}

func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	users := []string{"alice", "bob", "carol", "dave", "erin", "用户甲", "用户乙", "x", ""}
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		// 随机阶梯: 2~5 级, 严格递增百分比, Hold 0~300。
		nSteps := 2 + rng.Intn(4)
		steps := make([]Step, nSteps)
		pct := 0
		for i := range steps {
			pct += 1 + rng.Intn((100-pct)/(nSteps-i))
			steps[i] = Step{Percent: pct, Hold: rng.Int63n(301)}
		}
		salt := fmt.Sprintf("salt-%d", rng.Intn(10))
		c, err := New(salt, steps)
		if err != nil {
			t.Fatalf("序列 %d: 合法阶梯构造失败: %v", seq, err)
		}
		n := newNaive(salt, steps)

		nOps := 5 + rng.Intn(30)
		now := int64(0)
		for k := 0; k < nOps; k++ {
			// 时钟通常前进, 偶尔回拨或静止。
			switch r := rng.Intn(10); {
			case r < 7:
				now += int64(rng.Intn(400))
			case r < 9:
				// 静止
			default:
				now -= int64(rng.Intn(200))
				if now < 0 {
					now = 0
				}
			}
			var o op
			switch rng.Intn(7) {
			case 0:
				o = op{"Start", now, ""}
			case 1:
				o = op{"Pause", now, ""}
			case 2:
				o = op{"Resume", now, ""}
			case 3:
				o = op{"Tick", now, ""}
			case 4:
				o = op{"Rollback", now, ""}
			case 5:
				o = op{"Status", now, ""}
			default:
				o = op{"InRollout", now, users[rng.Intn(len(users))]}
			}
			why := basis(n, o)
			got, want, err := applyOp(c, n, o)
			t.Logf("seq=%d op=%d 输入=%s 阶梯=%v 盐=%q | 依据: %s | 输出=%s", seq, k, o, steps, salt, why, got)
			if err != nil {
				t.Fatalf("序列 %d 操作 %d (%s): %v\ncontroller 输出 %q, naive 输出 %q", seq, k, o, err, got, want)
			}
		}
	}
}
