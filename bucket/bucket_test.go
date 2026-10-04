package bucket

import (
	"reflect"
	"testing"
)

func owners(tbl *Table) []int {
	out := make([]int, tbl.N())
	for i, b := range tbl.Snapshot() {
		out[i] = b.Owner
	}
	return out
}

func TestAssignAllAlternating(t *testing.T) {
	tbl := New(8)
	tbl.AssignAll(map[int]int{1: 4, 2: 4}, 0)
	want := []int{1, 2, 1, 2, 1, 2, 1, 2}
	if got := owners(tbl); !reflect.DeepEqual(got, want) {
		t.Fatalf("AssignAll=%v want %v | 依据: 亏额并列取编号小, 逐个即时更新", got, want)
	}
	for _, b := range tbl.Snapshot() {
		if b.LastUsed != 0 {
			t.Fatalf("建组 lastUsed 应取 now")
		}
	}
}

func TestReconcileIdleThreshold(t *testing.T) {
	tests := []struct {
		name     string
		now      int64
		ti       int64
		tu       int64
		since    int64
		want     []int
		balanced bool
	}{
		{"热点桶空闲差 1 不搬, 空闲桶照搬", 109, 10, 0, 100, []int{1, 2, 3, 3, 3, 3, 1, 2}, true},
		{"恰等 Ti 可搬", 110, 10, 0, 100, []int{1, 2, 3, 3, 3, 3, 1, 2}, true},
		{"恰满 Tu 强制", 150, 1 << 40, 50, 100, []int{3, 3, 3, 3, 1, 2, 1, 2}, true},
		{"差 1 到 Tu 不强制", 149, 1 << 40, 50, 100, []int{1, 2, 1, 2, 1, 2, 1, 2}, false},
	}
	targets := map[int]int{1: 2, 2: 2, 3: 4}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := New(8)
			tbl.AssignAll(map[int]int{1: 4, 2: 4}, 0)
			hot := int64(0) // Ti 场景：桶 0、1 在 95 用过；Tu 场景：全部桶在 100 用过
			if tt.tu > 0 {
				hot = 100
				for h := uint64(0); h < 8; h++ {
					tbl.Lookup(h, hot)
				}
			} else {
				hot = tt.now - tt.ti + 1
				tbl.Lookup(0, hot)
				tbl.Lookup(1, hot)
			}
			gotBal := tbl.Reconcile(targets, tt.now, tt.ti, tt.since, tt.tu)
			if got := owners(tbl); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("%s: 桶表=%v want %v", tt.name, got, tt.want)
			}
			if gotBal != tt.balanced {
				t.Fatalf("%s: balanced=%v want %v", tt.name, gotBal, tt.balanced)
			}
		})
	}
}

func TestAssignKeepsLastUsed(t *testing.T) {
	tbl := New(8)
	tbl.AssignAll(map[int]int{1: 4, 2: 4}, 0)
	tbl.Lookup(0, 95)
	tbl.Lookup(9, 95) // hash%8==1
	tbl.Assign([]int{0, 1}, map[int]int{3: 2})
	for i, b := range tbl.Snapshot() {
		if i < 2 && b.LastUsed != 95 {
			t.Fatalf("立即指派不应改 lastUsed, 桶%d=%d", i, b.LastUsed)
		}
		if i >= 2 && b.LastUsed != 0 {
			t.Fatalf("未动桶 lastUsed 不应变, 桶%d=%d", i, b.LastUsed)
		}
	}
}

func TestAssignToEmptyWhenNoAlive(t *testing.T) {
	tbl := New(4)
	tbl.AssignAll(map[int]int{1: 2, 2: 2}, 7)
	tbl.Assign([]int{0, 1, 2, 3}, map[int]int{})
	if got := owners(tbl); !reflect.DeepEqual(got, []int{0, 0, 0, 0}) {
		t.Fatalf("无存活成员应置空, got %v", got)
	}
	if !tbl.Balanced(map[int]int{}) {
		t.Fatalf("全空应对空目标平衡")
	}
	for _, b := range tbl.Snapshot() {
		if b.LastUsed != 7 {
			t.Fatalf("置空不应改 lastUsed")
		}
	}
}

func TestTouchedCount(t *testing.T) {
	tbl := New(8)
	tbl.AssignAll(map[int]int{1: 4, 2: 4}, 0)
	tbl.ResetTouched()
	tbl.Lookup(0, 5)
	if tbl.Touched() != 1 {
		t.Fatalf("Lookup 应触碰 1 桶, got %d", tbl.Touched())
	}
	tbl.ResetTouched()
	tbl.Reconcile(map[int]int{1: 2, 2: 2, 3: 4}, 110, 10, 100, 0)
	if tbl.Touched() != 8 {
		t.Fatalf("一趟整理逐桶扫描应触碰 8, got %d", tbl.Touched())
	}
}
