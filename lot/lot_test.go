package lot

import (
	"errors"
	"testing"
)

func newPos(qy int64, sp0 int64, batches ...Batch) *Position {
	t := make([]Batch, len(batches))
	copy(t, batches)
	return &Position{Qy: qy, Sp0: sp0, Today: t}
}

func TestCloseModes(t *testing.T) {
	cases := []struct {
		name    string
		pos     *Position
		run     func(p *Position) error
		wantErr error
		wantQy  int64
		wantTdy []Batch
	}{
		{
			name:   "平昨部分",
			pos:    newPos(5, 100),
			run:    func(p *Position) error { return p.CloseYesterday(3) },
			wantQy: 2,
		},
		{
			name:    "平昨不足",
			pos:     newPos(2, 100),
			run:     func(p *Position) error { return p.CloseYesterday(3) },
			wantErr: ErrYesterdayShort,
			wantQy:  2,
		},
		{
			name: "平今跨批次且队首部分",
			pos:  newPos(0, 100, Batch{10, 2}, Batch{11, 3}, Batch{12, 4}),
			run: func(p *Position) error {
				got, err := p.CloseToday(4)
				if err != nil {
					return err
				}
				want := []Batch{{10, 2}, {11, 2}}
				if len(got) != len(want) {
					t.Fatalf("平今批次数=%d 判据: 应消费 2 个批次", len(got))
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("批次 %d=%v 判据: 先开先平应为 %v", i, got[i], want[i])
					}
				}
				return nil
			},
			wantTdy: []Batch{{11, 1}, {12, 4}},
		},
		{
			name:    "平今不足",
			pos:     newPos(9, 100, Batch{10, 8}),
			run:     func(p *Position) error { _, e := p.CloseToday(9); return e },
			wantErr: ErrTodayShort,
			wantQy:  9,
			wantTdy: []Batch{{10, 8}},
		},
		{
			name: "自动先昨后今",
			pos:  newPos(3, 100, Batch{10, 2}, Batch{11, 2}),
			run: func(p *Position) error {
				y, td, err := p.CloseAuto(5)
				if err != nil {
					return err
				}
				if y != 3 || len(td) != 1 || td[0] != (Batch{10, 2}) {
					t.Fatalf("y=%d td=%v 判据: 先平昨 3 手再平今 2 手", y, td)
				}
				return nil
			},
			wantQy:  0,
			wantTdy: []Batch{{11, 2}},
		},
		{
			name:    "自动总量不足",
			pos:     newPos(1, 100, Batch{10, 1}),
			run:     func(p *Position) error { _, _, e := p.CloseAuto(3); return e },
			wantErr: ErrPositionShort,
			wantQy:  1,
			wantTdy: []Batch{{10, 1}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run(c.pos)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v 判据: errors.Is(%v)", err, c.wantErr)
			}
			if c.pos.Qy != c.wantQy {
				t.Fatalf("Qy=%d 判据: 拒绝全有或全无/成功精确扣减, 期望 %d", c.pos.Qy, c.wantQy)
			}
			if len(c.pos.Today) != len(c.wantTdy) {
				t.Fatalf("Today=%v 判据: 期望 %v", c.pos.Today, c.wantTdy)
			}
			for i := range c.wantTdy {
				if c.pos.Today[i] != c.wantTdy[i] {
					t.Fatalf("Today=%v 判据: 期望 %v", c.pos.Today, c.wantTdy)
				}
			}
		})
	}
}

func TestTouchedCounter(t *testing.T) {
	for _, n := range []int{10, 10000} {
		p := &Position{}
		for i := 0; i < n; i++ {
			p.Open(int64(100+i), 1)
		}
		// 平 3.5 批: 完全平掉 3 批 + 触碰第 4 批部分
		if _, err := p.CloseToday(3); err != nil {
			t.Fatal(err)
		}
		if p.touched != 3 {
			t.Fatalf("n=%d touched=%d 判据: 整批平仓 touched==完全平掉批次数 3", n, p.touched)
		}
		p2 := &Position{}
		for i := 0; i < n; i++ {
			p2.Open(int64(100+i), 2)
		}
		if _, err := p2.CloseToday(7); err != nil { // 3 整批 + 第 4 批 1 手
			t.Fatal(err)
		}
		if p2.touched != 4 {
			t.Fatalf("n=%d touched=%d 判据: touched<=完全平掉批次数+1=4, 与总批次 %d 无关", n, p2.touched, n)
		}
	}
}

func TestAbsorb(t *testing.T) {
	p := newPos(2, 100, Batch{110, 3}, Batch{120, 1})
	p.Absorb(115)
	if p.Qy != 6 || p.Sp0 != 115 || len(p.Today) != 0 {
		t.Fatalf("Qy=%d Sp0=%d Today=%v 判据: 今仓全部并入昨仓且基准价置为结算价", p.Qy, p.Sp0, p.Today)
	}
}
