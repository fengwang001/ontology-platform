package namereg_test

import (
	"fmt"
	"testing"

	"ontology/namereg"
)

// 一次名字判定（占用与保留）触达的记录数不超过 3，
// 与该服角色数无关：100 与 100000 两档对照。
func TestProbesConstantPerCheck(t *testing.T) {
	for _, n := range []int{100, 100000} {
		t.Run(fmt.Sprintf("chars=%d", n), func(t *testing.T) {
			reg := namereg.New()
			reg.AddShard(1)
			for i := 0; i < n; i++ {
				reg.Hold(1, fmt.Sprintf("name-%d", i), fmt.Sprintf("char-%d", i))
			}
			reg.Reserve(1, "reserved-name", "other", 10)
			reg.Reserve(1, "expired-name", "other", 10)
			cases := []struct {
				name string
				now  int64
			}{
				{"free-name", 0},
				{"name-0", 0},        // 被占用
				{"reserved-name", 9}, // 有效保留
				{"expired-name", 10}, // 取等即释放
			}
			for _, tc := range cases {
				reg.ResetProbes()
				reg.Status(1, tc.name, "self", tc.now)
				if p := reg.Probes(); p > 3 {
					t.Fatalf("chars=%d name=%s: probes=%d > 3", n, tc.name, p)
				}
			}
		})
	}
}

// 保留到期靠惰性判定释放，且到期后他人可持有该名。
func TestReservationExpiryLazy(t *testing.T) {
	reg := namereg.New()
	reg.AddShard(1)
	reg.Reserve(1, "neo", "A", 500)
	if _, reserved := reg.Status(1, "neo", "B", 499); !reserved {
		t.Fatal("499: should be reserved for A")
	}
	if _, reserved := reg.Status(1, "neo", "A", 499); reserved {
		t.Fatal("499: owner self should not count as reserved")
	}
	if _, reserved := reg.Status(1, "neo", "B", 500); reserved {
		t.Fatal("500: equality must release the reservation")
	}
	reg.Hold(1, "neo", "B")
	if occupied, _ := reg.Status(1, "neo", "C", 500); !occupied {
		t.Fatal("B should hold neo after expiry")
	}
}
