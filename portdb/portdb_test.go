package portdb

import (
	"errors"
	"testing"

	"ontology/numplan"
)

const n = "13805001234"

func setupExample(t *testing.T, lmin, q int64) (*DB, *numplan.Plan) {
	t.Helper()
	p := numplan.New()
	if err := p.AssignBlock("1380", 11, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.AssignBlock("13805", 11, 2, 10); err != nil {
		t.Fatal(err)
	}
	return New(p, lmin, q), p
}

func TestWorkedExample(t *testing.T) {
	d, _ := setupExample(t, 50, 90)
	if _, err := d.RequestPort(n, 2, 3, 69, 20); !errors.Is(err, numplan.ErrLeadTime) {
		t.Fatalf("at=69: %v", err)
	}
	id, err := d.RequestPort(n, 2, 3, 70, 20)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("first order id = %d", id)
	}
	// 恰等生效：t=70 服务方已是 C。
	st, err := d.StateAtLocked(n, 70)
	if err != nil || st.Serving != 3 || !st.Ported {
		t.Fatalf("at=70 state=%+v err=%v", st, err)
	}
	st69, _ := d.StateAtLocked(n, 69)
	if st69.Serving != 2 || st69.Ported {
		t.Fatalf("t=69 state=%+v", st69)
	}
	// 已生效不可撤。
	if err := d.Cancel(id, 70); !errors.Is(err, numplan.ErrEffective) {
		t.Fatalf("cancel at 70: %v", err)
	}
}

func TestCancelBoundary(t *testing.T) {
	d, _ := setupExample(t, 50, 90)
	id, err := d.RequestPort(n, 2, 3, 100, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Cancel(id, 99); err != nil {
		t.Fatalf("cancel at 99: %v", err)
	}
	if err := d.Cancel(id, 99); !errors.Is(err, numplan.ErrNoOrder) {
		t.Fatalf("double cancel: %v", err)
	}
	if err := d.Cancel(id+100, 99); !errors.Is(err, numplan.ErrNoOrder) {
		t.Fatalf("missing order: %v", err)
	}
	st, _ := d.StateAtLocked(n, 100)
	if st.Serving != 2 || st.Ported {
		t.Fatalf("canceled order took effect: %+v", st)
	}
	// 撤销单不占号后续单号仍连续（被拒不占号）。
	id2, err := d.RequestPort(n, 2, 3, 200, 100)
	if err != nil || id2 != 2 {
		t.Fatalf("next id = %d, err=%v", id2, err)
	}
}

func TestRequestPortRejectOrder(t *testing.T) {
	type step struct {
		fn func(d *DB) error
	}
	cases := []struct {
		name string
		call func(d *DB) error
		want error
	}{
		{"invalid number", func(d *DB) error {
			_, err := d.RequestPort("123", 2, 3, 100, 20)
			return err
		}, numplan.ErrInvalid},
		{"invalid op", func(d *DB) error {
			_, err := d.RequestPort(n, 0, 3, 100, 20)
			return err
		}, numplan.ErrInvalid},
		{"invalid time", func(d *DB) error {
			_, err := d.RequestPort(n, 2, 3, -1, 20)
			return err
		}, numplan.ErrInvalid},
		{"clock back", func(d *DB) error {
			_, err := d.RequestPort(n, 2, 3, 100, 5)
			return err
		}, numplan.ErrClockBack},
		{"not assigned", func(d *DB) error {
			_, err := d.RequestPort("13999000000", 2, 3, 200, 30)
			return err
		}, numplan.ErrNotAssigned},
		{"pending exists", func(d *DB) error {
			_, err := d.RequestPort(n, 2, 3, 200, 30)
			return err
		}, nil},
		{"pending second", func(d *DB) error {
			if _, err := d.RequestPort(n, 2, 3, 200, 30); err != nil {
				return err
			}
			_, err := d.RequestPort(n, 2, 4, 210, 30)
			return err
		}, numplan.ErrPending},
		{"wrong donor", func(d *DB) error {
			// 先取消挂单再以错误 donor 申请。
			_ = d.Cancel(1, 30)
			_, err := d.RequestPort(n, 1, 3, 200, 30)
			return err
		}, numplan.ErrDonor},
		{"same op", func(d *DB) error {
			_, err := d.RequestPort(n, 2, 2, 200, 30)
			return err
		}, numplan.ErrSameOp},
		{"lead time", func(d *DB) error {
			_, err := d.RequestPort(n, 2, 3, 79, 30)
			return err
		}, numplan.ErrLeadTime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := setupExample(t, 50, 90)
			err := tc.call(d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestMultiPortAndORIndependence(t *testing.T) {
	d, _ := setupExample(t, 0, 90)
	must := func(id int64, err error) int64 {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	must(d.RequestPort(n, 2, 3, 100, 20))
	must(d.RequestPort(n, 3, 1, 250, 150))
	must(d.RequestPort(n, 1, 2, 320, 260))
	st, _ := d.StateAtLocked(n, 320)
	if st.Serving != 2 || st.Home != 2 || st.Ported {
		t.Fatalf("t=320: %+v", st)
	}
	st, _ = d.StateAtLocked(n, 250)
	if st.Serving != 1 || !st.Ported {
		t.Fatalf("t=250: %+v", st)
	}
}

func TestDisconnectFreezeAndAutoCancel(t *testing.T) {
	d, _ := setupExample(t, 0, 90)
	id, err := d.RequestPort(n, 2, 3, 450, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Disconnect(n, 400); err != nil {
		t.Fatal(err)
	}
	// 自动撤销未生效单。
	if err := d.Cancel(id, 400); !errors.Is(err, numplan.ErrNoOrder) {
		t.Fatalf("auto-cancel: %v", err)
	}
	// 冷冻左闭右开：400..489 冷冻。
	for _, tc := range []struct {
		t      int64
		frozen bool
	}{
		{399, false}, {400, true}, {489, true}, {490, false},
	} {
		st, err := d.StateAtLocked(n, tc.t)
		if tc.frozen {
			if err != nil || !st.Frozen {
				t.Fatalf("t=%d frozen=%v err=%v", tc.t, st.Frozen, err)
			}
		} else if err != nil || st.Frozen || st.Serving != st.Home {
			t.Fatalf("t=%d state=%+v err=%v", tc.t, st, err)
		}
	}
	// 冷冻中再销号报错；冷冻中携转报错。
	if err := d.Disconnect(n, 450); !errors.Is(err, numplan.ErrFrozen) {
		t.Fatalf("disconnect in freeze: %v", err)
	}
	if _, err := d.RequestPort(n, 2, 3, 600, 450); !errors.Is(err, numplan.ErrFrozen) {
		t.Fatalf("request in freeze: %v", err)
	}
	// 解冻后可重新携转。
	if _, err := d.RequestPort(n, 2, 3, 600, 490); err != nil {
		t.Fatalf("request after thaw: %v", err)
	}
	// 未分配号码销号。
	if err := d.Disconnect("13999000000", 500); !errors.Is(err, numplan.ErrNotAssigned) {
		t.Fatalf("disconnect unassigned: %v", err)
	}
}

func TestProbeLogBound(t *testing.T) {
	d, p := setupExample(t, 0, 0)
	// 构造同一号码 k 条历史（交替携转，lmin=0）。
	const k = 200
	var prevOp int = 2
	var at int64 = 100
	for i := 0; i < k; i++ {
		recipient := 3 + i%2 // 3,4 交替
		if _, err := d.RequestPort(n, prevOp, recipient, at, at); err != nil {
			t.Fatalf("port %d: %v", i, err)
		}
		prevOp = recipient
		at++
	}
	// 各时刻 probes ≤ log2(历史条数)+2。
	for tt := int64(0); tt < at; tt++ {
		st, err := d.StateAtLocked(n, tt)
		if err != nil {
			t.Fatal(err)
		}
		bound := 0
		for m := 1; m <= len(d.recs[n].events); m <<= 1 {
			bound++
		}
		bound += 2
		if st.Probe.History > bound {
			t.Fatalf("t=%d probes=%d bound=%d k=%d", tt, st.Probe.History, bound, len(d.recs[n].events))
		}
	}
	_ = p
}
