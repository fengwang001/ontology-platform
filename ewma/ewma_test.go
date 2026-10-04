package ewma

import "testing"

func TestValAndUpdate(t *testing.T) {
	const tau, p0 = int64(100), int64(50)
	tests := []struct {
		name string
		ops  func(e *Estimator)
		now  int64
		want int64
		has  bool
	}{
		{
			name: "无样本取先验P0",
			ops:  func(e *Estimator) {},
			now:  0,
			want: p0,
			has:  false,
		},
		{
			name: "样本后立即读取不衰减",
			ops:  func(e *Estimator) { e.Update(200, 10, tau) },
			now:  10,
			want: 200,
			has:  true,
		},
		{
			name: "中途线性衰减",
			ops:  func(e *Estimator) { e.Update(200, 10, tau) },
			now:  60,
			want: 100, // 200*(100-50)/100
			has:  true,
		},
		{
			name: "d等于tau恰归零",
			ops:  func(e *Estimator) { e.Update(200, 10, tau) },
			now:  110,
			want: 0,
			has:  true,
		},
		{
			name: "d大于tau被夹取仍为零",
			ops:  func(e *Estimator) { e.Update(200, 10, tau) },
			now:  999,
			want: 0,
			has:  true,
		},
		{
			name: "低于衰减值的样本不降低估计",
			ops: func(e *Estimator) {
				e.Update(200, 10, tau)
				e.Update(30, 60, tau) // 衰减值100，30不降低
			},
			now:  60,
			want: 100,
			has:  true,
		},
		{
			name: "高于衰减值的样本立即抬升",
			ops: func(e *Estimator) {
				e.Update(200, 10, tau)
				e.Update(300, 60, tau) // 衰减值100，抬升到300
			},
			now:  60,
			want: 300,
			has:  true,
		},
		{
			name: "抬升后从新时刻重新衰减",
			ops: func(e *Estimator) {
				e.Update(200, 10, tau)
				e.Update(300, 60, tau)
			},
			now:  110, // d=50
			want: 150,
			has:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var e Estimator
			tc.ops(&e)
			if got := e.Val(tc.now, tau, p0); got != tc.want {
				t.Errorf("Val(%d) = %d, want %d", tc.now, got, tc.want)
			}
			if got := e.HasSample(); got != tc.has {
				t.Errorf("HasSample() = %v, want %v", got, tc.has)
			}
		})
	}
}

func TestValNeverExceedsMaxSample(t *testing.T) {
	const tau, p0 = int64(1000), int64(50)
	var e Estimator
	maxSeen := p0
	for _, s := range []struct {
		rtt, now int64
	}{{500, 0}, {10, 100}, {900, 200}, {1, 300}} {
		e.Update(s.rtt, s.now, tau)
		if s.rtt > maxSeen {
			maxSeen = s.rtt
		}
		for now := s.now; now <= s.now+2*tau; now += 7 {
			if v := e.Val(now, tau, p0); v > maxSeen {
				t.Fatalf("Val(%d)=%d 超过 max(P0, 最大样本)=%d", now, v, maxSeen)
			}
		}
	}
}
