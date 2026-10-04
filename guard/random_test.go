package guard

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func errClass(err error) error {
	for _, s := range []error{
		nil, ErrCurfew, ErrQuotaExhausted, ErrDeviceOnline, ErrDeviceLimit,
		ErrDeviceOffline, ErrNoAccount, ErrClockBack,
	} {
		if errors.Is(err, s) {
			return s
		}
	}
	return err
}

func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1000
	rng := rand.New(rand.NewSource(20261004))
	for g := 0; g < groups; g++ {
		p := simParams{
			tz:   int64(rng.Intn(7)-3) * 14400, // -43200..+43200 离散档
			cs:   int64(rng.Intn(8)) * 3 * 3600,
			ce:   int64(rng.Intn(8)) * 3 * 3600,
			lw:   int64(rng.Intn(6)) * 300,
			lh:   int64(rng.Intn(6)) * 300,
			hb:   int64(1+rng.Intn(12)) * 300,
			dmax: 1 + rng.Intn(3),
		}
		if p.cs > 86399 {
			p.cs = 21 * 3600
		}
		if p.ce > 86399 {
			p.ce = 21 * 3600
		}
		runRandomGroup(t, rng, p, g)
	}
}

func runRandomGroup(t *testing.T, rng *rand.Rand, p simParams, gid int) {
	t.Helper()
	impl, err := New(p.tz, p.cs, p.ce, p.lw, p.lh, p.hb, p.dmax)
	if err != nil {
		t.Fatalf("group %d New(%+v): %v", gid, p, err)
	}
	nv := newNaive(p)
	accts := []string{"a0", "a1"}
	devs := []string{"d0", "d1", "d2"}
	for _, a := range accts {
		impl.Register(a)
		nv.reg[a] = true
	}
	// 随机把个别日设为节假日（只允许未来日）。
	var log string
	add := func(s string) {
		log += s
	}
	add(fmt.Sprintf("group %d params=%+v\n", gid, p))
	for i := 0; i < 4; i++ {
		day := int64(-1 + rng.Intn(5))
		on := rng.Intn(2) == 0
		now := int64(rng.Intn(3 * 86400))
		gotH := errClass(impl.SetHoliday(now, day, on))
		// naive 侧复制 SetHoliday 的判定（含单调时钟）。
		var wantH error
		if now < nv.maxNow {
			wantH = ErrClockBack
		} else {
			nv.maxNow = now
			nv.now = now
			dayStart := day*86400 - p.tz
			if dayStart <= now {
				wantH = ErrTooLate
			} else {
				// 成功标记
			}
		}
		if gotH != wantH {
			t.Fatalf("group %d SetHoliday(now=%d,day=%d,on=%v) impl=%v naive=%v\n%s",
				gid, now, day, on, gotH, wantH, log)
		}
		// 参数与时钟合法即推进时钟（即使为时已晚）；clockback 不推进。
		if wantH != ErrClockBack {
			nv.maxNow = now
			nv.now = now
		}
		if wantH == nil {
			if on {
				nv.holiday[day] = true
			} else {
				delete(nv.holiday, day)
			}
		}
		add(fmt.Sprintf("  SetHoliday(now=%d day=%d on=%v) -> %v\n", now, day, on, gotH))
	}

	cursor := int64(0)
	const horizon = 3 * 86400
	for i := 0; i < 80; i++ {
		step := int64(1 + rng.Intn(1500))
		if rng.Intn(7) == 0 {
			step = int64(rng.Intn(horizon))
		}
		now := cursor + step
		if now > horizon {
			now = horizon
		}
		if rng.Intn(10) == 0 && cursor > 5 {
			now = cursor - int64(1+rng.Intn(5))
		}
		o := simOp{
			kind: simOpKind(rng.Intn(3)),
			now:  now,
			acct: accts[rng.Intn(len(accts))],
			dev:  devs[rng.Intn(len(devs))],
		}
		var got error
		switch o.kind {
		case simLogin:
			got = impl.Login(o.now, o.acct, o.dev)
		case simHeartbeat:
			got = impl.Heartbeat(o.now, o.acct, o.dev)
		case simLogout:
			got = impl.Logout(o.now, o.acct, o.dev)
		}
		want := nv.run(o)
		if errClass(got) != errClass(want) {
			names := []string{"Login", "Heartbeat", "Logout"}
			t.Fatalf("group %d op%d %s(%d,%s,%s) impl=%v naive=%v\n%s",
				gid, i, names[o.kind], o.now, o.acct, o.dev, got, want, log)
		}
		add(fmt.Sprintf("  %-9s now=%-6d %s/%s -> %v\n",
			[]string{"Login", "Heartbeat", "Logout"}[o.kind], o.now, o.acct, o.dev, errClass(got)))
		if now >= cursor {
			cursor = now
		}
	}
	// 观测点对照 Used/Remaining/在线设备集。
	base := nv.maxNow
	if base > horizon {
		base = horizon
	}
	for probe := base; probe <= horizon; probe += 1373 {
		nv.tickTo(probe)
		for _, a := range accts {
			d0 := nv.day(probe)
			for _, d := range []int64{d0 - 1, d0, d0 + 1} {
				gu, _ := impl.Used(probe, a, d)
				if gu != nv.usedOf(a, d) {
					t.Fatalf("group %d Used(%d,%s,day=%d) impl=%d naive=%d\n%s",
						gid, probe, a, d, gu, nv.usedOf(a, d), log)
				}
			}
			gr, _ := impl.Remaining(probe, a)
			nr := nv.limit(d0) - nv.usedOf(a, d0)
			if nr < 0 {
				nr = 0
			}
			if gr != nr {
				t.Fatalf("group %d Remaining(%d,%s) impl=%d naive=%d\n%s",
					gid, probe, a, gr, nr, log)
			}
			gd := impl.onlineDevices(probe, a)
			nd := nv.onlineDevs(a)
			if fmt.Sprint(gd) != fmt.Sprint(nd) {
				t.Fatalf("group %d devices(%d,%s) impl=%v naive=%v\n%s",
					gid, probe, a, gd, nd, log)
			}
		}
	}
}
