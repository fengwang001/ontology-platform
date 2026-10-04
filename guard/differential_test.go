package guard

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

type op struct {
	kind string
	now  int64
	dev  string
	day  int64
	on   bool
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && (a == nil) == (b == nil)
}

// daysTouched 收集两侧需要比对的日号。
func runDifferential(t *testing.T, seed int64, verbose bool) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	tz := []int64{0, -28800, 28800, 50400, -43200}[rng.Intn(5)]
	var cs, ce int64
	switch rng.Intn(3) {
	case 0: // 无宵禁
		cs, ce = 0, 0
	case 1: // 日内
		cs = int64(20000 + rng.Intn(20000))
		ce = cs + int64(5000+rng.Intn(20000))
	default: // 跨午夜
		cs = int64(60000 + rng.Intn(20000))
		ce = int64(10000 + rng.Intn(20000))
	}
	lw := int64(rng.Intn(4000))     // 0..3999
	lh := int64(rng.Intn(5000))     // 0..4999
	hb := int64(1 + rng.Intn(1200)) // 1..1200
	dmax := 1 + rng.Intn(3)

	g := New(tz, cs, ce, lw, lh, hb, dmax)
	n := newNaive(tz, cs, ce, lw, lh, hb, dmax)

	// 起始时刻靠近日界，制造跨日机会；本地日界 = -tz。
	start := -tz + int64(rng.Intn(3000)) - 1500
	now := start
	g.Register("a")
	n.registered = true

	nOps := 30 + rng.Intn(25)
	touched := map[int64]bool{}
	d0, _ := n.dayX(now)
	for d := d0 - 2; d <= d0+4; d++ {
		touched[d] = true
	}
	nextDev := 0
	pickDev := func() string {
		// 2/3 概率引用历史设备（可能在线/已超时），1/3 引用不存在的设备。
		if nextDev > 0 && rng.Intn(3) > 0 {
			return fmt.Sprintf("dev%d", rng.Intn(nextDev))
		}
		return fmt.Sprintf("void%d", rng.Intn(5))
	}

	apply := func(o op) error {
		switch o.kind {
		case "login":
			return g.Login(o.now, "a", o.dev)
		case "hb":
			return g.Heartbeat(o.now, "a", o.dev)
		case "logout":
			return g.Logout(o.now, "a", o.dev)
		case "remain":
			_, err := g.Remaining(o.now, "a")
			return err
		case "used":
			_, err := g.Used(o.now, "a", o.day)
			return err
		case "holiday":
			return g.SetHoliday(o.now, o.day, o.on)
		case "ghost":
			return g.Login(o.now, "ghost", o.dev)
		}
		return nil
	}
	applyN := func(o op) error {
		switch o.kind {
		case "login":
			return n.login(o.now, o.dev)
		case "hb":
			return n.heartbeat(o.now, o.dev)
		case "logout":
			return n.logout(o.now, o.dev)
		case "remain":
			_, err := n.remaining(o.now)
			return err
		case "used":
			_, err := n.usedAt(o.now, o.day)
			return err
		case "holiday":
			return n.setHoliday(o.now, o.day, o.on)
		case "ghost":
			return n.loginAcct(o.now, "ghost", o.dev)
		}
		return nil
	}

	for i := 0; i < nOps; i++ {
		// 多数操作时间向前；少量回退以触发时钟检查。
		gap := int64(1 + rng.Intn(900))
		if rng.Intn(8) == 0 {
			gap = -int64(1 + rng.Intn(50))
		}
		now += gap

		var o op
		switch rng.Intn(10) {
		case 0, 1:
			dev := fmt.Sprintf("dev%d", nextDev)
			nextDev++
			o = op{kind: "login", now: now, dev: dev}
		case 2, 3, 4:
			o = op{kind: "hb", now: now, dev: pickDev()}
		case 5:
			o = op{kind: "logout", now: now, dev: pickDev()}
		case 6:
			o = op{kind: "remain", now: now}
		case 7:
			day := d0 + int64(rng.Intn(7)-2)
			o = op{kind: "used", now: now, day: day}
		case 8:
			day := d0 + int64(1+rng.Intn(4))
			o = op{kind: "holiday", now: now, day: day, on: rng.Intn(2) == 0}
		default:
			o = op{kind: "ghost", now: now, dev: fmt.Sprintf("dev%d", nextDev)}
			nextDev++
		}

		e1 := apply(o)
		e2 := applyN(o)
		if verbose || seed <= 3 {
			t.Logf("[seed=%d] #%d %+v -> guard=%v naive=%v", seed, i, o, e1, e2)
		}
		if (e1 == nil) != (e2 == nil) || (e1 != nil && e1.Error() != e2.Error()) {
			t.Fatalf("seed=%d op#%d %+v error mismatch: guard=%v naive=%v\ncfg: tz=%d cs=%d ce=%d lw=%d lh=%d hb=%d dmax=%d",
				seed, i, o, e1, e2, tz, cs, ce, lw, lh, hb, dmax)
		}
		if e1 == nil {
			for d := range touched {
				gu, _ := g.Used(now, "a", d)
				nu, _ := n.usedAt(now, d)
				if gu != nu {
					t.Fatalf("seed=%d after op#%d %+v day %d used diverged: guard=%d naive=%d",
						seed, i, o, d, gu, nu)
				}
			}
		}
	}

	// 全部操作后逐日比对 Used（再做一次只读推进对齐时钟）。
	for day := range touched {
		gu, _ := g.Used(now, "a", day)
		nu, _ := n.usedAt(now, day)
		if gu != nu {
			t.Fatalf("seed=%d day %d used mismatch: guard=%d naive=%d\ncfg: tz=%d cs=%d ce=%d lw=%d lh=%d hb=%d dmax=%d",
				seed, day, gu, nu, tz, cs, ce, lw, lh, hb, dmax)
		}
		// 不变量：任一日不超过该日额度；与朴素模拟一致即满足并集长度。
		var lim int64
		if n.holidays[day] {
			lim = lh
		} else {
			lim = lw
		}
		if gu > lim {
			t.Fatalf("seed=%d day %d used %d exceeds limit %d", seed, day, gu, lim)
		}
	}
}

func TestDifferentialRandom1000(t *testing.T) {
	const trials = 1000
	for s := int64(1); s <= trials; s++ {
		runDifferential(t, s, s <= 3)
	}
	t.Logf("随机朴素模拟对照完成：%d 组小参数随机序列（逐秒模拟），返回值与每日 Used 全部一致；前 3 组已打印输入/输出", trials)
}
