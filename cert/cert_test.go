package cert

import (
	"fmt"
	"reflect"
	"testing"
)

// 入口结算弹出数 ≤ 实际迁移数+1，且与设备/证书总数无关：
// 100 与 10000 台设备两档、同样 2 次迁移，popped 必须相同。
func TestSettlePoppedIndependentOfScale(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("devices=%d", n), func(t *testing.T) {
			st, err := NewStore(40, 100, 100)
			if err != nil {
				t.Fatal(err)
			}
			op, err := st.BeginOp(0)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				dev := fmt.Sprintf("dev-%05d", i)
				op.Insert(&Cert{Serial: fmt.Sprintf("a-%05d", i), Dev: dev, Nb: 0, Na: 1_000_000, State: Active})
				lapseAt := int64(1_000_000_000) // 未到期，只用于膨胀堆规模
				if i < 2 {
					lapseAt = 50 // 仅两台设备的 Pending 会到期
				}
				op.Insert(&Cert{Serial: fmt.Sprintf("p-%05d", i), Dev: dev, Nb: 0, Na: 2_000_000, State: Pending, LapseAt: lapseAt})
			}
			op.Commit()

			base := st.popped
			op, err = st.BeginOp(100)
			if err != nil {
				t.Fatal(err)
			}
			op.Commit()
			got := st.popped - base
			const migrations = 2
			t.Logf("devices=%d migrations=%d popped=%d", n, migrations, got)
			if got > migrations+1 {
				t.Fatalf("popped=%d 超过迁移数 %d+1", got, migrations)
			}
			if got != migrations {
				t.Fatalf("popped=%d, want %d（与设备规模无关）", got, migrations)
			}
			for _, i := range []int{0, 1} {
				c, _ := st.Lookup(fmt.Sprintf("p-%05d", i))
				if c.State != Lapsed {
					t.Fatalf("%s state=%s, want Lapsed", c.Serial, c.State)
				}
			}
		})
	}
}

// 结算踢线按 (发生时刻, 设备名字节序) 排序。
func TestSettleKickOrder(t *testing.T) {
	st, err := NewStore(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.BeginOp(0)
	if err != nil {
		t.Fatal(err)
	}
	// b 与 a 同时刻到期（字节序 a<b），c 更早到期。
	op.Insert(&Cert{Serial: "rb", Dev: "b", Nb: 0, Na: 100, State: Retiring, RetireAt: 10})
	op.SetSession("b", "rb")
	op.Insert(&Cert{Serial: "ra", Dev: "a", Nb: 0, Na: 100, State: Retiring, RetireAt: 10})
	op.SetSession("a", "ra")
	op.Insert(&Cert{Serial: "rc", Dev: "c", Nb: 0, Na: 100, State: Retiring, RetireAt: 5})
	op.SetSession("c", "rc")
	op.Commit()

	op, err = st.BeginOp(10)
	if err != nil {
		t.Fatal(err)
	}
	kicked := op.Commit()
	want := []string{"c", "a", "b"}
	if !reflect.DeepEqual(kicked, want) {
		t.Fatalf("kicked=%v, want %v", kicked, want)
	}
}

// 被拒操作回滚：结算不落地、时钟不推进、popped 复原。
func TestAbortRollsBackSettlement(t *testing.T) {
	st, err := NewStore(1, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := st.BeginOp(0)
	op.Insert(&Cert{Serial: "p", Dev: "d", Nb: 0, Na: 1000, State: Pending, LapseAt: 10})
	op.Commit()

	op, err = st.BeginOp(20) // 入口会把 p 结算为 Lapsed
	if err != nil {
		t.Fatal(err)
	}
	if c := op.Cert("p"); c.State != Lapsed {
		t.Fatalf("结算后 state=%s, want Lapsed", c.State)
	}
	op.Abort(ErrUnknown)

	c, _ := st.Lookup("p")
	if c.State != Pending {
		t.Fatalf("回滚后 state=%s, want Pending", c.State)
	}
	if got := st.Now(); got != 0 {
		t.Fatalf("回滚后 now=%d, want 0", got)
	}
	if st.popped != 0 {
		t.Fatalf("回滚后 popped=%d, want 0", st.popped)
	}
	if len(st.exp) != 1 {
		t.Fatalf("回滚后到期堆大小=%d, want 1", len(st.exp))
	}
}
