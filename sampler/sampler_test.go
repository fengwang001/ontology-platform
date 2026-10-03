package sampler

import "testing"

func mustNew(t *testing.T, k, e, p int64) *Sampler {
	t.Helper()
	s, err := New(k, e, p)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) 出错: %v", k, e, p, err)
	}
	return s
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		k, e, p int64
		wantErr bool
	}{
		{"最小合法", 1, 1, 1, false},
		{"最大合法", 1e6, 1e6, 1e9, false},
		{"k 为零", 0, 1, 1, true},
		{"k 超界", 1e6 + 1, 1, 1, true},
		{"e 为零", 1, 0, 1, true},
		{"e 超界", 1, 1e6 + 1, 1, true},
		{"p 为零", 1, 1, 0, true},
		{"p 超界", 1, 1, 1e9 + 1, true},
		{"负数", -1, -1, -1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.k, tc.e, tc.p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("New(%d,%d,%d) err=%v, wantErr=%v", tc.k, tc.e, tc.p, err, tc.wantErr)
			}
		})
	}
}

func TestAdmitSampling(t *testing.T) {
	cases := []struct {
		name string
		k    int64
		n    int
		want []bool // 每次 Admit 的 sampled
	}{
		{"k=1 全采中", 1, 4, []bool{true, true, true, true}},
		{"k=2 偶数采中", 2, 5, []bool{false, true, false, true, false}},
		{"k=3 第 3 的倍数采中", 3, 7, []bool{false, false, true, false, false, true, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t, tc.k, 1, 1)
			for i := 0; i < tc.n; i++ {
				sampled, paused := s.Admit(int64(i))
				t.Logf("Admit(now=%d) -> sampled=%v paused=%v (c=%d, k=%d, 依据 c mod k == 0)", i, sampled, paused, s.Qualified(), tc.k)
				if paused {
					t.Fatalf("未触发暂停却报 paused")
				}
				if sampled != tc.want[i] {
					t.Fatalf("第 %d 次 Admit sampled=%v, 期望 %v", i+1, sampled, tc.want[i])
				}
			}
			if s.Qualified() != int64(tc.n) {
				t.Fatalf("c=%d, 期望 %d", s.Qualified(), tc.n)
			}
		})
	}
}

func TestPauseLifecycle(t *testing.T) {
	// E=2, P=100：错误于 now=10、20 到达，第二次令 pausedUntil=120。
	s := mustNew(t, 2, 2, 100)
	s.OnError(10)
	if s.ConsecFails() != 1 || s.PausedUntil() != 0 {
		t.Fatalf("首次错误后 s=%d pausedUntil=%d, 期望 1/0", s.ConsecFails(), s.PausedUntil())
	}
	s.OnError(20)
	if s.ConsecFails() != 0 || s.PausedUntil() != 120 {
		t.Fatalf("第二次错误后 s=%d pausedUntil=%d, 期望 0/120", s.ConsecFails(), s.PausedUntil())
	}

	// 暂停期间 Admit 报暂停且不推进 c。
	if sampled, paused := s.Admit(119); sampled || paused != true {
		t.Fatalf("now=119 应暂停, got sampled=%v paused=%v", sampled, paused)
	}
	if s.Qualified() != 0 {
		t.Fatalf("暂停期间 c 不应推进, got %d", s.Qualified())
	}
	// 恰在 pausedUntil 恢复。
	if sampled, paused := s.Admit(120); sampled || paused {
		t.Fatalf("now=120 应恢复且 c=1 不采中, got sampled=%v paused=%v", sampled, paused)
	}
	if s.Qualified() != 1 {
		t.Fatalf("恢复后 c=1, got %d", s.Qualified())
	}
}

func TestPauseWindowErrorAndSuccess(t *testing.T) {
	cases := []struct {
		name string
		e, p int64
		ops  []struct {
			err bool // true=OnError, false=OnSuccess
			now int64
		}
		wantS        int64
		wantPausedAt int64
	}{
		{
			name: "暂停期间错误不累计",
			e:    2, p: 100,
			ops: []struct {
				err bool
				now int64
			}{
				{true, 10}, {true, 20}, // 触发暂停至 120
				{true, 50}, {true, 119}, // 暂停期间，s 不变
			},
			wantS: 0, wantPausedAt: 120,
		},
		{
			name: "暂停期间成功不清零",
			e:    3, p: 100,
			ops: []struct {
				err bool
				now int64
			}{
				{true, 10}, {true, 20}, {true, 30}, // 触发暂停至 130
				{false, 40}, // 暂停期间成功，s 保持 0（已清零）
			},
			wantS: 0, wantPausedAt: 130,
		},
		{
			name: "非暂停期成功清零连续失败",
			e:    3, p: 100,
			ops: []struct {
				err bool
				now int64
			}{
				{true, 10}, {true, 20}, // s=2
				{false, 30}, // 清零
				{true, 40},  // s=1
			},
			wantS: 1, wantPausedAt: 0,
		},
		{
			name: "恢复后错误重新累计",
			e:    2, p: 100,
			ops: []struct {
				err bool
				now int64
			}{
				{true, 10}, {true, 20}, // 暂停至 120
				{true, 120}, // 恢复后 s=1
				{true, 130}, // s=2 触发新暂停至 230
			},
			wantS: 0, wantPausedAt: 230,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustNew(t, 1, tc.e, tc.p)
			for i, op := range tc.ops {
				if op.err {
					s.OnError(op.now)
				} else {
					s.OnSuccess(op.now)
				}
				t.Logf("op%d: err=%v now=%d -> s=%d pausedUntil=%d", i, op.err, op.now, s.ConsecFails(), s.PausedUntil())
			}
			if s.ConsecFails() != tc.wantS || s.PausedUntil() != tc.wantPausedAt {
				t.Fatalf("s=%d pausedUntil=%d, 期望 %d/%d",
					s.ConsecFails(), s.PausedUntil(), tc.wantS, tc.wantPausedAt)
			}
		})
	}
}
