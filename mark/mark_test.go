package mark_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/backfill"
	"ontology/mark"
	"ontology/part"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func checkWS(t *testing.T, s *mark.System, w, stable int) {
	t.Helper()
	if got := s.W(); got != w {
		t.Fatalf("W = %d, want %d", got, w)
	}
	if got := s.S(); got != stable {
		t.Fatalf("S = %d, want %d", got, stable)
	}
}

// 规格中的完整示例。
func TestSpecExample(t *testing.T) {
	s := mark.New(10)
	for p := 0; p <= 5; p++ {
		mustOK(t, s.Commit(p, p+1)) // t=1..6: Commit(0..5)
	}
	mustOK(t, s.Commit(7, 7)) // t=7: Commit(7)，6 空洞，W 不动
	checkWS(t, s, 5, 5)

	mustOK(t, s.Ack("c", 5, 8)) // t=8: Ack(c,5)

	mustOK(t, s.Begin("j1", 2, 3, 20, 10)) // t=10
	checkWS(t, s, 5, 1)                    // S 回退到 amin-1 = 1
	if got := s.Acked("c"); got != 5 {
		t.Fatalf("Acked(c) = %d, want 5 (不因 S 回退而改变)", got)
	}

	mustOK(t, s.Commit(6, 11)) // t=11: 探测 6、7、8，W=7
	checkWS(t, s, 7, 1)

	// t=12: Begin(j2,3,8) 与 j1 相交
	var oe *backfill.OverlapError
	if err := s.Begin("j2", 3, 8, 5, 12); !errors.As(err, &oe) || oe.Conflict != "j1" {
		t.Fatalf("err = %v, want ErrOverlap(j1)", err)
	}
	mustOK(t, s.Begin("j2", 8, 9, 5, 12)) // deadline = 17

	// t=13: Commit(8) 被 j2 占用
	var he *mark.HeldError
	if err := s.Commit(8, 13); !errors.As(err, &he) || he.Job != "j2" {
		t.Fatalf("err = %v, want ErrHeld(j2)", err)
	}
	mustOK(t, s.Stage("j1", 2, 13))
	var ie *backfill.IncompleteError
	if _, err := s.Finish("j1", 13); !errors.As(err, &ie) || ie.Part != 3 {
		t.Fatalf("err = %v, want ErrIncomplete(3)", err)
	}

	// t=14: 补齐并提交 j1
	mustOK(t, s.Stage("j1", 3, 14))
	rev, err := s.Finish("j1", 14)
	mustOK(t, err)
	want := mark.Revisions{{Consumer: "c", Parts: []int{2, 3}}}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("Revisions = %v, want %v", rev, want)
	}
	if s.Ver(2) != 2 || s.Ver(3) != 2 {
		t.Fatalf("Ver(2)=%d Ver(3)=%d, want 2 and 2", s.Ver(2), s.Ver(3))
	}
	checkWS(t, s, 7, 7) // S = min(7, 8-1) = 7

	// t=16: j2 未过期，仍 ErrHeld
	if err := s.Commit(8, 16); !errors.As(err, &he) || he.Job != "j2" {
		t.Fatalf("err = %v, want ErrHeld(j2)", err)
	}
	// t=17: j2 恰等到期，取消随本次接受落地
	mustOK(t, s.Commit(8, 17))
	checkWS(t, s, 8, 8)
	if s.Ver(8) != 1 {
		t.Fatalf("Ver(8) = %d, want 1", s.Ver(8))
	}
}

// deadline 恰等即过期、小 1 仍占用；Heartbeat 续期后恰等仍过期。
func TestDeadlineExactAndMinusOne(t *testing.T) {
	s := mark.New(10)
	mustOK(t, s.Begin("j", 0, 1, 5, 10)) // deadline = 15

	var he *mark.HeldError
	if err := s.Commit(0, 14); !errors.As(err, &he) || he.Job != "j" {
		t.Fatalf("t=14 err = %v, want ErrHeld(j)", err)
	}
	mustOK(t, s.Commit(0, 15)) // t=15 恰等到期：视同不存在
	if s.Ver(0) != 1 {
		t.Fatalf("Ver(0) = %d, want 1", s.Ver(0))
	}
	if err := s.Heartbeat("j", 16); !errors.Is(err, backfill.ErrNoJob) {
		t.Fatalf("Heartbeat err = %v, want ErrNoJob", err)
	}

	// Heartbeat 续期：deadline 变为 now+ttl。
	mustOK(t, s.Begin("h", 8, 9, 5, 16)) // deadline = 21
	mustOK(t, s.Heartbeat("h", 20))      // deadline = 25
	if err := s.Commit(8, 21); !errors.As(err, &he) || he.Job != "h" {
		t.Fatalf("t=21 err = %v, want ErrHeld(h)", err)
	}
	mustOK(t, s.Commit(8, 25)) // t=25 恰等到期
}

// Stage 不续期。
func TestStageDoesNotRenew(t *testing.T) {
	s := mark.New(10)
	mustOK(t, s.Begin("j", 0, 1, 5, 0)) // deadline = 5
	mustOK(t, s.Stage("j", 0, 4))
	mustOK(t, s.Stage("j", 1, 4))
	// t=5：若 Stage 续期则仍被占用；实际已过期，Commit 被接受。
	mustOK(t, s.Commit(0, 5))
}

// 过期作业的暂存不生效：同名重新 Begin 后需重新暂存。
func TestExpiredJobStagingDiscarded(t *testing.T) {
	s := mark.New(10)
	mustOK(t, s.Begin("j", 0, 1, 5, 0)) // deadline = 5
	mustOK(t, s.Stage("j", 0, 1))
	mustOK(t, s.Stage("j", 1, 2))
	mustOK(t, s.Commit(5, 6)) // 接受的操作落地 j 的取消
	mustOK(t, s.Begin("j", 0, 1, 5, 7))
	var ie *backfill.IncompleteError
	if _, err := s.Finish("j", 8); !errors.As(err, &ie) || ie.Part != 0 {
		t.Fatalf("Finish err = %v, want ErrIncomplete(0)（旧暂存已丢弃）", err)
	}
}

// 被拒操作不落地取消：S 只在接受的操作后才回升。
func TestRejectedOpDoesNotLandCancellation(t *testing.T) {
	s := mark.New(10)
	for p := 0; p <= 9; p++ {
		mustOK(t, s.Commit(p, p))
	}
	mustOK(t, s.Begin("j", 5, 6, 5, 10)) // deadline = 15，S 压到 4
	checkWS(t, s, 9, 4)

	// t=16：j 已过期，但 Commit(0) 因 ErrAlready 被拒，取消不落地。
	if err := s.Commit(0, 16); !errors.Is(err, part.ErrAlready) {
		t.Fatalf("err = %v, want ErrAlready", err)
	}
	checkWS(t, s, 9, 4) // S 仍被已过期未落地的 j 压低

	// t=17：被接受的操作落地取消，S 回升。
	mustOK(t, s.Commit(11, 17))
	checkWS(t, s, 9, 9)
}

// S 回退与回升：Begin 压低，Abort / Finish / 过期落地回升。
func TestSRegressAndRecover(t *testing.T) {
	s := mark.New(10)
	for p := 0; p <= 9; p++ {
		mustOK(t, s.Commit(p, p))
	}
	checkWS(t, s, 9, 9)

	mustOK(t, s.Begin("a", 3, 4, 100, 10))
	checkWS(t, s, 9, 2) // 回退到 amin-1 = 2

	mustOK(t, s.Begin("b", 7, 7, 100, 11))
	checkWS(t, s, 9, 2) // amin 仍为 3

	mustOK(t, s.Abort("a", 12))
	checkWS(t, s, 9, 6) // 回升到 7-1

	mustOK(t, s.Stage("b", 7, 13))
	_, err := s.Finish("b", 14)
	mustOK(t, err)
	checkWS(t, s, 9, 9) // 无活跃作业，S = W
}

// Ack 恰等 S 被接受；超过 S 报 ErrBeyondStable；回退报 ErrAckRegress。
func TestAckExactlyStable(t *testing.T) {
	s := mark.New(10)
	for p := 0; p <= 4; p++ {
		mustOK(t, s.Commit(p, p))
	}
	checkWS(t, s, 4, 4)
	mustOK(t, s.Ack("c", 4, 5)) // 恰等 S
	if err := s.Ack("c", 5, 6); !errors.Is(err, mark.ErrBeyondStable) {
		t.Fatalf("err = %v, want ErrBeyondStable", err)
	}
	mustOK(t, s.Ack("c", 4, 7)) // 与上次相等：接受
	if err := s.Ack("c", 3, 8); !errors.Is(err, mark.ErrAckRegress) {
		t.Fatalf("err = %v, want ErrAckRegress", err)
	}
	// 首次确认前视为 -1：S=-1（无任何提交）时任何 upto>=0 都越界。
	empty := mark.New(10)
	if err := empty.Ack("d", 0, 0); !errors.Is(err, mark.ErrBeyondStable) {
		t.Fatalf("err = %v, want ErrBeyondStable", err)
	}
}

// Missing 分区回填不产生修订但可推进 W；再次回填才产生修订。
func TestMissingBackfillNoRevisionButAdvancesW(t *testing.T) {
	s := mark.New(10)
	mustOK(t, s.Begin("j1", 0, 2, 100, 0))
	for p := 0; p <= 2; p++ {
		mustOK(t, s.Stage("j1", p, 1))
	}
	rev, err := s.Finish("j1", 2)
	mustOK(t, err)
	if len(rev) != 0 {
		t.Fatalf("Revisions = %v, want empty（提交前均 Missing）", rev)
	}
	checkWS(t, s, 2, 2) // W 被回填推进
	for p := 0; p <= 2; p++ {
		if s.Ver(p) != 1 {
			t.Fatalf("Ver(%d) = %d, want 1", p, s.Ver(p))
		}
	}

	mustOK(t, s.Ack("c", 2, 3))
	mustOK(t, s.Begin("j2", 0, 2, 100, 4))
	for p := 0; p <= 2; p++ {
		mustOK(t, s.Stage("j2", p, 5))
	}
	rev, err = s.Finish("j2", 6)
	mustOK(t, err)
	want := mark.Revisions{{Consumer: "c", Parts: []int{0, 1, 2}}}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("Revisions = %v, want %v", rev, want)
	}
}

// 修订清单：多消费者按名字节序；只列 p <= 已确认值且提交前 ver>=1；
// 清单为空的消费者不列出。
func TestRevisionsMultipleConsumers(t *testing.T) {
	s := mark.New(10)
	for p := 0; p <= 5; p++ {
		mustOK(t, s.Commit(p, p))
	}
	mustOK(t, s.Ack("b", 5, 6))
	mustOK(t, s.Ack("a", 3, 7))
	// "c" 从未确认（视为 -1），不应列出。
	mustOK(t, s.Begin("j", 2, 4, 100, 8))
	for p := 2; p <= 4; p++ {
		mustOK(t, s.Stage("j", p, 9))
	}
	rev, err := s.Finish("j", 10)
	mustOK(t, err)
	want := mark.Revisions{
		{Consumer: "a", Parts: []int{2, 3}},
		{Consumer: "b", Parts: []int{2, 3, 4}},
	}
	if !reflect.DeepEqual(rev, want) {
		t.Fatalf("Revisions = %v, want %v", rev, want)
	}
}

// 拒绝次序（只报第一个）：参数非法 > 时钟回退 > 作业不存在/已存在 >
// 状态类错误（Begin: TooManyJobs > Overlap；Commit: Held > Already；
// Ack: AckRegress > BeyondStable）。
func TestRejectionOrder(t *testing.T) {
	setup := func() *mark.System {
		s := mark.New(1)
		for p := 0; p <= 3; p++ {
			mustOK(t, s.Commit(p, p)) // maxNow = 3
		}
		mustOK(t, s.Ack("c", 3, 4))
		mustOK(t, s.Begin("j", 0, 1, 100, 5)) // 占用 [0,1]，S 压到 -1，maxNow = 5
		return s
	}
	cases := []struct {
		name string
		op   func(s *mark.System) error
		want error
	}{
		{"参数非法优先于时钟回退", func(s *mark.System) error {
			return s.Begin("x", -1, 5, 10, 0) // 非法区间且 now < maxNow
		}, mark.ErrInvalid},
		{"时钟回退优先于作业不存在", func(s *mark.System) error {
			return Finish2Err(s, "nope", 0) // now < maxNow 且作业不存在
		}, mark.ErrClock},
		{"作业不存在优先于越界", func(s *mark.System) error {
			return s.Stage("nope", 999, 6)
		}, backfill.ErrNoJob},
		{"已存在优先于满员", func(s *mark.System) error {
			return s.Begin("j", 5, 6, 10, 6) // maxJobs=1 已满，但 j 已存在
		}, backfill.ErrJobExists},
		{"满员优先于相交", func(s *mark.System) error {
			return s.Begin("k", 0, 1, 10, 6) // 与 j 相交，但先报满员
		}, backfill.ErrTooManyJobs},
		{"占用优先于已提交", func(s *mark.System) error {
			return s.Commit(0, 6) // 0 已 Committed 且被 j 占用
		}, mark.ErrHeld},
		{"回退优先于越稳定水位", func(s *mark.System) error {
			return s.Ack("c", 2, 6) // 2 < 3 回退，且 2 > S=-1 越界
		}, mark.ErrAckRegress},
		{"越稳定水位", func(s *mark.System) error {
			return s.Ack("c", 3, 6) // 3 不回退，但 3 > S=-1
		}, mark.ErrBeyondStable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.op(setup()); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// Finish2Err 把 Finish 的返回值压成 error，便于表驱动。
func Finish2Err(s *mark.System, job string, now int) error {
	_, err := s.Finish(job, now)
	return err
}
