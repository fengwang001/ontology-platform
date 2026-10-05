package transfer_test

import (
	"errors"
	"testing"

	"ontology/transfer"
)

var scenarios = []scenario{
	{
		// 题目给定示例：CD=1000, R=500, U=200, Wt=100。
		name: "worked example",
		cd:   1000, r: 500, u: 200, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 3},
			{op: "create", now: 0, sid: 1, char: "A", name: "neo"},
			{op: "create", now: 0, sid: 2, char: "B", name: "neo"},
			{op: "create", now: 0, sid: 2, char: "C", name: "trin"},
			{op: "request", now: 0, char: "B", dst: 1},
			{op: "request", now: 5, char: "C", dst: 1, want: transfer.ErrShardFull}, // 负载 1+1=2
			{op: "complete", now: 50, char: "B"},                                    // 服2 neo 为 B 保留至 550
			{op: "inspect", char: "B", view: transfer.CharView{Shard: 1, Name: "", PendingRename: true}},
			{op: "create", now: 60, sid: 2, char: "D", name: "neo", want: transfer.ErrNameReserved},
			{op: "request", now: 100, char: "B", dst: 2}, // 100 < 50+200，回迁免冷却
			{op: "complete", now: 120, char: "B"},        // 取回 neo；coolStart 仍为 50，prev 清空
			{op: "inspect", char: "B", view: transfer.CharView{Shard: 2, Name: "neo"}},
			{op: "request", now: 130, char: "B", dst: 1, want: transfer.ErrCooling},
			{op: "request", now: 1050, char: "B", dst: 1}, // 取等可迁
			{op: "load", sid: 1, num: 2},                  // A + B 的在途预留
		},
	},
	{
		// 冷却取等：now == coolStart+CD 可迁，差 1 毫秒报冷却中。
		name: "cooldown equality",
		cd:   100, r: 10, u: 5, wt: 50,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 0, char: "A"}, // coolStart=0
			{op: "request", now: 99, char: "A", dst: 1, want: transfer.ErrCooling},
			{op: "request", now: 100, char: "A", dst: 1}, // 取等可迁
		},
	},
	{
		// 回迁窗口取等：now == prevDone+U 不算回迁，按普通迁移判冷却。
		name: "return window equality",
		cd:   10000, r: 10, u: 200, wt: 1000,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 50, char: "A"},         // prevDone=50, coolStart=50
			{op: "request", now: 249, char: "A", dst: 1}, // 249 < 250，回迁
			{op: "cancel", now: 249, char: "A"},
			{op: "request", now: 250, char: "A", dst: 1, want: transfer.ErrCooling}, // 取等非回迁
		},
	},
	{
		// 回迁不重置冷却，且回迁后 prev 清空、不可连环回迁。
		name: "return keeps cooldown, no chained return",
		cd:   1000, r: 10, u: 200, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 3},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 50, char: "A"},                                    // coolStart=50, prev=1
			{op: "request", now: 100, char: "A", dst: 1},                            // 回迁
			{op: "complete", now: 120, char: "A"},                                   // prev 清空，coolStart 仍为 50
			{op: "request", now: 130, char: "A", dst: 2, want: transfer.ErrCooling}, // 130 < 50+1000
			{op: "request", now: 1050, char: "A", dst: 2},
			{op: "complete", now: 1050, char: "A"},                                   // prev=1, prevDone=1050, coolStart=1050
			{op: "request", now: 1100, char: "A", dst: 1},                            // 回迁
			{op: "complete", now: 1100, char: "A"},                                   // prev 清空
			{op: "request", now: 1150, char: "A", dst: 2, want: transfer.ErrCooling}, // 不可连环回迁
		},
	},
	{
		// 迁移单取等过期：Complete 报无在途迁移，预留已释放，他人可再订。
		name: "ticket expiry equality and reservation release",
		cd:   1, r: 10, u: 10, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 1},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "create", now: 0, sid: 1, char: "B", name: "y"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "request", now: 1, char: "B", dst: 2, want: transfer.ErrShardFull}, // 负载 0+1=1
			{op: "complete", now: 100, char: "A", want: transfer.ErrNoTicket},       // 取等过期
			{op: "load", sid: 2, num: 1},                                            // 被拒绝不推进时钟，逻辑时间仍为 0
			{op: "request", now: 100, char: "B", dst: 2},                            // 判定视角下旧预留已释放
			{op: "load", sid: 2, num: 1},
		},
	},
	{
		// 保留期取等：now == 保留到期时刻即释放，他人可用。
		name: "reservation expiry equality",
		cd:   1, r: 500, u: 1, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "neo"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 50, char: "A"}, // neo 为 A 保留至 550
			{op: "create", now: 549, sid: 1, char: "B", name: "neo", want: transfer.ErrNameReserved},
			{op: "create", now: 550, sid: 1, char: "B", name: "neo"}, // 取等释放
		},
	},
	{
		// 到达撞名转待改名；待改名的角色离开某服时不产生保留。
		name: "arrival clash pending; pending leaves no reservation",
		cd:   1, r: 10, u: 1, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "newshard", sid: 3, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "create", now: 0, sid: 2, char: "D", name: "x"},
			{op: "request", now: 1, char: "A", dst: 2},
			{op: "complete", now: 2, char: "A"}, // 撞名：待改名，不持有名字
			{op: "inspect", char: "A", view: transfer.CharView{Shard: 2, Name: "", PendingRename: true}},
			{op: "request", now: 3, char: "A", dst: 3},
			{op: "complete", now: 4, char: "A"}, // 离开服2 未持有名字，不留保留
			{op: "inspect", char: "A", view: transfer.CharView{Shard: 3, Name: "x"}},
			{op: "rename", now: 5, char: "D", name: "y"},         // D 改名将 x 立即释放
			{op: "create", now: 6, sid: 2, char: "E", name: "x"}, // 证明服2 无遗留保留
		},
	},
	{
		// 改名：释放旧名不产生保留；冻结期报已冻结；占用先于保留。
		name: "rename rules",
		cd:   1000, r: 500, u: 1, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "a"},
			{op: "create", now: 0, sid: 1, char: "B", name: "b"},
			{op: "rename", now: 0, char: "A", name: "a2"},
			{op: "create", now: 1, sid: 1, char: "C", name: "a"}, // 旧名立即可用
			{op: "rename", now: 1, char: "A", name: "b", want: transfer.ErrNameOccupied},
			{op: "request", now: 2, char: "B", dst: 2},
			{op: "rename", now: 3, char: "B", name: "x", want: transfer.ErrFrozen}, // 冻结期
			{op: "complete", now: 4, char: "B"},                                    // b 为 B 保留至 504
			{op: "rename", now: 5, char: "A", name: "b", want: transfer.ErrNameReserved},
			{op: "rename", now: 505, char: "A", name: "b"}, // 505 >= 504，保留已释放
		},
	},
	{
		// 保留者本人取回：本人经 Rename 取回自留名并清除保留。
		name: "self retake via rename",
		cd:   1, r: 500, u: 1, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "a"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 0, char: "A"}, // a 在服1为 A 保留至 500
			{op: "rename", now: 1, char: "A", name: "b"},
			{op: "request", now: 2, char: "A", dst: 1}, // 非回迁（2 >= 0+1），冷却取等可迁
			{op: "complete", now: 3, char: "A"},        // 期望名 b 在服1空闲，A 持有 b
			{op: "create", now: 4, sid: 1, char: "C", name: "a", want: transfer.ErrNameReserved},
			{op: "rename", now: 4, char: "A", name: "a"}, // 本人取回 a，清除保留；b 立即释放
			{op: "create", now: 5, sid: 1, char: "D", name: "b"},
			{op: "create", now: 6, sid: 1, char: "E", name: "a", want: transfer.ErrNameOccupied},
		},
	},
	{
		// 阻断项先于冷却；已有有效迁移单先于阻断项。
		name: "blockers and ticket order",
		cd:   1000, r: 10, u: 5, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "create", now: 0, sid: 1, char: "B", name: "y"},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "complete", now: 0, char: "A"}, // coolStart=0
			{op: "blockers", char: "A", num: 1},
			{op: "request", now: 10, char: "A", dst: 1, want: transfer.ErrBlocked}, // 阻断先于冷却
			{op: "blockers", char: "A", num: 0},
			{op: "request", now: 10, char: "A", dst: 1, want: transfer.ErrCooling},
			{op: "request", now: 0, char: "B", dst: 2},
			{op: "blockers", char: "B", num: 2},
			{op: "request", now: 1, char: "B", dst: 2, want: transfer.ErrTicketExists}, // 迁移单先于阻断
		},
	},
	{
		// Create 拒绝次序：参数非法 > 时钟回退 > 服不存在 > 角色已存在 >
		// 负载已达 cap > 名字被占用。
		name: "create rejection order",
		cd:   1, r: 1, u: 1, wt: 1,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 1},
			{op: "create", now: 10, sid: 1, char: "A", name: "x"},
			{op: "create", now: 9, sid: 1, char: "B", name: "", want: transfer.ErrInvalidParam},
			{op: "create", now: 9, sid: 9, char: "B", name: "y", want: transfer.ErrClockRollback},
			{op: "create", now: 10, sid: 9, char: "A", name: "y", want: transfer.ErrShardNotFound},
			{op: "create", now: 10, sid: 2, char: "B", name: "y"}, // 填满服2
			{op: "create", now: 10, sid: 2, char: "A", name: "z", want: transfer.ErrCharExists},
			{op: "create", now: 10, sid: 2, char: "C", name: "y", want: transfer.ErrShardFull},
			{op: "create", now: 10, sid: 1, char: "D", name: "x", want: transfer.ErrNameOccupied},
		},
	},
	{
		// Request 拒绝次序：参数非法 > 时钟回退 > 角色不存在 > 服不存在 >
		// dst 即当前服 > 已有有效迁移单 > 冷却中 > dst 负载已达 cap。
		name: "request rejection order",
		cd:   1000, r: 10, u: 5, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 2},
			{op: "newshard", sid: 2, cap: 2},
			{op: "newshard", sid: 3, cap: 1},
			{op: "create", now: 10, sid: 1, char: "A", name: "x"},
			{op: "create", now: 10, sid: 3, char: "C", name: "z"},
			{op: "request", now: 9, char: "", dst: 1, want: transfer.ErrInvalidParam},
			{op: "request", now: 9, char: "ghost", dst: 1, want: transfer.ErrClockRollback},
			{op: "request", now: 10, char: "ghost", dst: 9, want: transfer.ErrCharNotFound},
			{op: "request", now: 10, char: "A", dst: 9, want: transfer.ErrShardNotFound},
			{op: "request", now: 10, char: "A", dst: 2},
			{op: "request", now: 10, char: "A", dst: 1, want: transfer.ErrSameShard},
			{op: "request", now: 10, char: "A", dst: 2, want: transfer.ErrTicketExists},
			{op: "complete", now: 10, char: "A"},                                   // coolStart=10
			{op: "request", now: 20, char: "A", dst: 3, want: transfer.ErrCooling}, // 冷却先于 dst 已满
		},
	},
	{
		// 无在途迁移报错；Cancel 释放预留后他人可订。
		name: "no ticket; cancel releases reservation",
		cd:   1, r: 10, u: 10, wt: 100,
		steps: []step{
			{op: "newshard", sid: 1, cap: 3},
			{op: "newshard", sid: 2, cap: 1},
			{op: "create", now: 0, sid: 1, char: "A", name: "x"},
			{op: "create", now: 0, sid: 1, char: "B", name: "y"},
			{op: "complete", now: 0, char: "A", want: transfer.ErrNoTicket},
			{op: "cancel", now: 0, char: "A", want: transfer.ErrNoTicket},
			{op: "request", now: 0, char: "A", dst: 2},
			{op: "request", now: 1, char: "B", dst: 2, want: transfer.ErrShardFull},
			{op: "cancel", now: 1, char: "A"},
			{op: "load", sid: 2, num: 0},
			{op: "request", now: 1, char: "B", dst: 2},
		},
	},
	{
		// 时钟：取等允许；回退报错；被拒绝的操作不推进时钟；now 上限 1e12。
		name: "clock rules",
		cd:   1, r: 1, u: 1, wt: 1,
		steps: []step{
			{op: "newshard", sid: 1, cap: 5},
			{op: "create", now: 100, sid: 1, char: "A", name: "a"},
			{op: "create", now: 100, sid: 1, char: "B", name: "b"}, // 取等允许
			{op: "create", now: 99, sid: 1, char: "C", name: "c", want: transfer.ErrClockRollback},
			{op: "create", now: 100, sid: 1, char: "C", name: "c"}, // 被拒绝不推进时钟
			{op: "create", now: 1_000_000_000_001, sid: 1, char: "D", name: "d", want: transfer.ErrInvalidParam},
		},
	},
	{
		// NewShard 参数校验与重复注册。
		name: "newshard validation",
		cd:   1, r: 1, u: 1, wt: 1,
		steps: []step{
			{op: "newshard", sid: 1, cap: 0, want: transfer.ErrInvalidParam},
			{op: "newshard", sid: 1, cap: 1_000_001, want: transfer.ErrInvalidParam},
			{op: "newshard", sid: 1, cap: 1},
			{op: "newshard", sid: 1, cap: 1, want: transfer.ErrShardExists},
		},
	},
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		cd, r, u, wt int64
		want         error
	}{
		{0, 1, 1, 1, transfer.ErrInvalidParam},
		{1, 0, 1, 1, transfer.ErrInvalidParam},
		{1, 1, 0, 1, transfer.ErrInvalidParam},
		{1, 1, 1, 0, transfer.ErrInvalidParam},
		{1, 1, 1, 10_000_000_001, transfer.ErrInvalidParam},
		{-5, 1, 1, 1, transfer.ErrInvalidParam},
		{1, 1, 1, 1, nil},
		{10_000_000_000, 10_000_000_000, 10_000_000_000, 10_000_000_000, nil},
	}
	for i, tc := range cases {
		if _, err := transfer.New(tc.cd, tc.r, tc.u, tc.wt); !errors.Is(err, tc.want) {
			t.Errorf("case %d: New(%d,%d,%d,%d) = %v, want %v", i, tc.cd, tc.r, tc.u, tc.wt, err, tc.want)
		}
	}
}
