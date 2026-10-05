package ontology_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology"
	"ontology/role"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// newRoom 建立 o(Owner)、a/a2(Admin)、x/y/z(Member) 的房间。
func newRoom(t *testing.T, m int) *ontology.Manager {
	t.Helper()
	mg := ontology.New(m)
	must(t, mg.Join(0, "o"))
	for _, u := range []string{"a", "a2", "x", "y", "z"} {
		must(t, mg.Join(0, u))
	}
	must(t, mg.SetRole(0, "o", "a", role.Admin))
	must(t, mg.SetRole(0, "o", "a2", role.Admin))
	return mg
}

func checkView(t *testing.T, mg *ontology.Manager, mics []string, queue []string) {
	t.Helper()
	v := mg.Snapshot()
	if fmt.Sprint(v.Mics) != fmt.Sprint(mics) {
		t.Fatalf("mics = %v, want %v", v.Mics, mics)
	}
	if fmt.Sprint(v.Queue) != fmt.Sprint(queue) {
		t.Fatalf("queue = %v, want %v", v.Queue, queue)
	}
}

// 题目示例一：入口补麦使后来者不能插队；禁言到期取等。
func TestMicFillExample(t *testing.T) {
	setup := func(t *testing.T) *ontology.Manager {
		mg := newRoom(t, 1)
		must(t, mg.TakeMic(0, "x"))
		must(t, mg.TakeMic(0, "y"))
		must(t, mg.TakeMic(0, "z"))
		checkView(t, mg, []string{"x"}, []string{"y", "z"})
		must(t, mg.Mute(10, "a", "y", 100))
		must(t, mg.DropMic(20, "x")) // 补麦跳过 y，z 上麦
		checkView(t, mg, []string{"z"}, []string{"y"})
		must(t, mg.DropMic(30, "z")) // 麦空着，y 仍被禁言
		checkView(t, mg, []string{""}, []string{"y"})
		return mg
	}
	t.Run("t99_y_still_muted", func(t *testing.T) {
		mg := setup(t)
		must(t, mg.TakeMic(99, "x")) // 入口补麦时 y 仍被禁言，x 直接上麦
		checkView(t, mg, []string{"x"}, []string{"y"})
	})
	t.Run("t100_y_unmuted_first", func(t *testing.T) {
		mg := setup(t)
		must(t, mg.TakeMic(100, "x")) // 入口补麦时 y 恰好解除，y 上麦，x 排队
		checkView(t, mg, []string{"y"}, []string{"x"})
	})
}

// 禁言在 now < until 时生效，now == until 即解除。
func TestMuteExpiryEquality(t *testing.T) {
	mg := newRoom(t, 1)
	must(t, mg.Mute(10, "a", "y", 100))
	wantErr(t, mg.TakeMic(99, "y"), ontology.ErrMuted)
	must(t, mg.TakeMic(100, "y")) // 取等解除
	checkView(t, mg, []string{"y"}, nil)
}

// 被拒操作撤销入口补麦，不留痕迹，也不推进时钟。
func TestRejectedOpRollsBackPrefill(t *testing.T) {
	mg := newRoom(t, 1)
	must(t, mg.TakeMic(0, "x"))
	must(t, mg.TakeMic(0, "y"))
	must(t, mg.Mute(10, "a", "y", 100))
	must(t, mg.DropMic(20, "x")) // y 被禁言跳过，麦空，队列 [y]
	checkView(t, mg, []string{""}, []string{"y"})

	// t=100 时 y 禁言解除，入口补麦会把 y 补上麦；但操作者不在房间，整体撤销。
	wantErr(t, mg.TakeMic(100, "ghost"), ontology.ErrNotInRoom)
	checkView(t, mg, []string{""}, []string{"y"})

	// 时钟未被拒绝操作推进：t=50 的操作仍被接受（y 此刻仍禁言，x 直接上麦）。
	must(t, mg.TakeMic(50, "x"))
	checkView(t, mg, []string{"x"}, []string{"y"})

	// t=100 再次被拒（目标不在房间），y 仍在队列。
	wantErr(t, mg.Mute(100, "a", "ghost", 200), ontology.ErrTargetNotInRoom)
	checkView(t, mg, []string{"x"}, []string{"y"})

	// 合法操作：麦被 x 占着，z 排到 y 后面；x 下麦后出口补麦让 y 上麦。
	must(t, mg.TakeMic(100, "z"))
	checkView(t, mg, []string{"x"}, []string{"y", "z"})
	must(t, mg.DropMic(101, "x"))
	checkView(t, mg, []string{"y"}, []string{"z"})
}

// 题目示例二：同级可覆盖、高级压制、缩短禁言、到期后可重新禁言。
func TestSuppressionAndOverride(t *testing.T) {
	mg := newRoom(t, 1)
	must(t, mg.Mute(10, "a", "y", 100)) // L0=2
	must(t, mg.Unmute(15, "a2", "y"))   // 同级不算压制
	must(t, mg.Mute(16, "a", "y", 100))
	must(t, mg.Mute(20, "o", "y", 50)) // Owner 缩短为 50，L0=3
	if got := mg.Snapshot().Mutes["y"]; got.Until != 50 || got.L0 != role.Owner {
		t.Fatalf("mute = %+v, want until=50 L0=Owner", got)
	}
	wantErr(t, mg.Mute(30, "a", "y", 200), ontology.ErrSuppressed)
	must(t, mg.Mute(50, "a", "y", 200)) // 到期解除后可重新禁言，L0=2
	if got := mg.Snapshot().Mutes["y"]; got.Until != 200 || got.L0 != role.Admin {
		t.Fatalf("mute = %+v, want until=200 L0=Admin", got)
	}
	// 同级不可禁言；任何人都无法禁言 Owner。
	wantErr(t, mg.Mute(60, "a", "a2", 300), ontology.ErrLevel)
	wantErr(t, mg.Mute(60, "a", "o", 300), ontology.ErrLevel)
	wantErr(t, mg.Mute(60, "x", "o", 300), ontology.ErrLevel)
	wantErr(t, mg.Unmute(60, "a2", "x"), ontology.ErrNotMuted)
}

// L0 是施加时的快照：施加者升降级或离开都不改它。
func TestL0Snapshot(t *testing.T) {
	t.Run("promotion_does_not_change_L0", func(t *testing.T) {
		mg := newRoom(t, 1)
		must(t, mg.Mute(10, "a", "y", 100)) // L0=2
		must(t, mg.Transfer(20, "o", "a"))  // a 升为 Owner
		// 若 L0 跟随升至 3，a2(Admin) 会被压制；快照不变则同级可覆盖。
		must(t, mg.Mute(30, "a2", "y", 150))
		if got := mg.Snapshot().Mutes["y"]; got.L0 != role.Admin {
			t.Fatalf("L0 = %v, want Admin", got.L0)
		}
	})
	t.Run("demotion_keeps_record_and_level_checks_use_current", func(t *testing.T) {
		mg := newRoom(t, 1)
		must(t, mg.Mute(10, "a", "y", 100))
		must(t, mg.SetRole(20, "o", "a", role.Member)) // a 降为 Member
		if got := mg.Snapshot().Mutes["y"]; got.L0 != role.Admin {
			t.Fatalf("L0 = %v, want Admin (snapshot)", got.L0)
		}
		wantErr(t, mg.Unmute(30, "a", "y"), ontology.ErrLevel) // 等级检查用当前等级
		must(t, mg.Unmute(30, "a2", "y"))                      // 同级 L0=2 可解除
	})
	t.Run("leaver_record_keeps_L0", func(t *testing.T) {
		mg := newRoom(t, 1)
		must(t, mg.Mute(10, "a", "y", 100))
		must(t, mg.Leave(20, "a")) // 施加者离开，记录与 L0 不变
		must(t, mg.Mute(30, "a2", "y", 150))
		if got := mg.Snapshot().Mutes["y"]; got.Until != 150 || got.L0 != role.Admin {
			t.Fatalf("mute = %+v", got)
		}
	})
}

// 被禁言期间离开再加入，禁言仍然生效。
func TestLeaveRejoinKeepsMute(t *testing.T) {
	mg := newRoom(t, 1)
	must(t, mg.Mute(10, "a", "y", 100))
	must(t, mg.Leave(20, "y"))
	must(t, mg.Join(30, "y")) // 重新加入为 Member
	if got := mg.Snapshot().Roles["y"]; got != role.Member {
		t.Fatalf("role = %v, want Member", got)
	}
	wantErr(t, mg.TakeMic(50, "y"), ontology.ErrMuted)
	must(t, mg.TakeMic(100, "y")) // 到期后可上麦
}

// Owner 移交与离开。
func TestOwnerTransferAndLeave(t *testing.T) {
	mg := ontology.New(2)
	must(t, mg.Join(0, "o"))
	must(t, mg.Join(0, "x"))
	wantErr(t, mg.Leave(1, "o"), ontology.ErrMustTransfer) // 还有他人须先移交
	wantErr(t, mg.Transfer(1, "o", "o"), ontology.ErrParam)
	wantErr(t, mg.Transfer(1, "x", "o"), ontology.ErrLevel) // 只有 Owner 能移交
	must(t, mg.Transfer(2, "o", "x"))
	v := mg.Snapshot()
	if v.Roles["x"] != role.Owner || v.Roles["o"] != role.Admin {
		t.Fatalf("roles = %v", v.Roles)
	}
	must(t, mg.Leave(3, "o")) // 降为 Admin 后可离开
	must(t, mg.Join(3, "y"))
	wantErr(t, mg.Leave(4, "x"), ontology.ErrMustTransfer) // 还有他人须先移交
	must(t, mg.Transfer(5, "x", "y"))
	must(t, mg.Leave(6, "x")) // 已移交，可离开
	must(t, mg.Leave(7, "y")) // 房间只剩自己，可离开
	must(t, mg.Join(8, "z"))  // 空房间加入者成为 Owner
	if got := mg.Snapshot().Roles["z"]; got != role.Owner {
		t.Fatalf("role = %v, want Owner", got)
	}
}

// 拒绝次序：参数非法 > 时钟回退 > 操作者不在房间 > 目标不在房间 >
// 等级不足 > 被压制 > 其余状态类。
func TestRejectionOrdering(t *testing.T) {
	mg := newRoom(t, 1)
	must(t, mg.TakeMic(0, "x"))
	must(t, mg.Mute(10, "o", "y", 200)) // y 被 Owner 禁言，L0=3

	cases := []struct {
		name string
		op   func() error
		want error
	}{
		{"param_before_clock", func() error { return mg.Mute(5, "a", "z", 3) }, ontology.ErrParam},
		{"clock_before_absent", func() error { return mg.TakeMic(5, "ghost") }, ontology.ErrClock},
		{"by_absent_before_target_absent", func() error { return mg.Mute(20, "g1", "g2", 300) }, ontology.ErrNotInRoom},
		{"target_absent_before_level", func() error { return mg.Mute(20, "x", "g2", 300) }, ontology.ErrTargetNotInRoom},
		{"level_before_suppressed", func() error { return mg.Mute(20, "x", "y", 300) }, ontology.ErrLevel},
		{"suppressed", func() error { return mg.Mute(20, "a", "y", 300) }, ontology.ErrSuppressed},
		{"suppressed_unmute", func() error { return mg.Unmute(20, "a", "y") }, ontology.ErrSuppressed},
		{"state_not_muted", func() error { return mg.Unmute(20, "a", "z") }, ontology.ErrNotMuted},
		{"state_muted", func() error { return mg.TakeMic(20, "y") }, ontology.ErrMuted},
		{"state_duplicate_join", func() error { return mg.Join(20, "x") }, ontology.ErrDuplicate},
		{"state_duplicate_mic", func() error { return mg.TakeMic(20, "x") }, ontology.ErrDuplicate},
		{"state_not_in_mic_order", func() error { return mg.DropMic(20, "z") }, ontology.ErrNotInMicOrder},
		{"state_must_transfer", func() error { return mg.Leave(20, "o") }, ontology.ErrMustTransfer},
		{"param_bad_role", func() error { return mg.SetRole(20, "o", "x", role.Owner) }, ontology.ErrParam},
		{"param_now_out_of_range", func() error { return mg.Join(1_000_000_000_001, "n") }, ontology.ErrParam},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantErr(t, c.op(), c.want)
		})
	}
	// 全部被拒，状态与 maxNow 不变：t=10 的合法操作仍被接受。
	must(t, mg.TakeMic(10, "z"))
	checkView(t, mg, []string{"x"}, []string{"z"})
}

// 并发调用等价于某个串行顺序：无数据竞争，且结束后不变量成立。
func TestConcurrent(t *testing.T) {
	mg := ontology.New(4)
	must(t, mg.Join(0, "o"))
	users := []string{"u0", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	for _, u := range users {
		must(t, mg.Join(0, u))
	}
	must(t, mg.SetRole(0, "o", "u0", role.Admin))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var now int64
			for i := 0; i < 300; i++ {
				now += rng.Int63n(20)
				u := users[rng.Intn(len(users))]
				switch rng.Intn(5) {
				case 0:
					_ = mg.TakeMic(now, u)
				case 1:
					_ = mg.DropMic(now, u)
				case 2:
					_ = mg.Mute(now, "o", u, now+rng.Int63n(50)+1)
				case 3:
					_ = mg.Unmute(now, "o", u)
				case 4:
					_ = mg.TakeMic(now, u)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	checkInvariants(t, mg.Snapshot(), 1_000_000)
}
