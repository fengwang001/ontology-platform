package impossibletravel

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, v, k, h int64) *Detector {
	t.Helper()
	d, err := NewDetector(v, k, h)
	if err != nil {
		t.Fatalf("NewDetector(%d, %d, %d): %v", v, k, h, err)
	}
	return d
}

func check(t *testing.T, d *Detector, card string, tm, x, y int64, want Decision) {
	t.Helper()
	if got := d.Check([]byte(card), tm, x, y); got != want {
		t.Errorf("Check(%q, %d, %d, %d) = %v, want %v", card, tm, x, y, got, want)
	}
}

func TestConstructorValidation(t *testing.T) {
	for _, tc := range []struct{ v, k, h int64 }{
		{1, 1, 1},
		{1_000_000, 100, 1_000_000_000_000},
		{900, 2, 100},
	} {
		if _, err := NewDetector(tc.v, tc.k, tc.h); err != nil {
			t.Errorf("NewDetector(%d, %d, %d) = %v, want nil", tc.v, tc.k, tc.h, err)
		}
	}
	for _, tc := range []struct{ v, k, h int64 }{
		{0, 1, 1},
		{1_000_001, 1, 1},
		{-1, 1, 1},
		{1, 0, 1},
		{1, 101, 1},
		{1, 1, 0},
		{1, 1, 1_000_000_000_001},
	} {
		if _, err := NewDetector(tc.v, tc.k, tc.h); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("NewDetector(%d, %d, %d) = %v, want ErrInvalidParam", tc.v, tc.k, tc.h, err)
		}
	}
}

// 题目主示例：V=900, K=2, H=100。
func TestMainExample(t *testing.T) {
	d := mustNew(t, 900, 2, 100)
	const c = "card"
	check(t, d, c, 0, 0, 0, Accepted)
	// d=900, 900*3600 == 900*3600，恰等接受。
	check(t, d, c, 3600, 600, 300, Accepted)
	check(t, d, c, 3600, 600, 300, RejectedDuplicate)
	// 900*3600 > 900*1，不可能行程，历史 [3601]，cnt=1。
	check(t, d, c, 3601, 0, 0, RejectedImpossibleTravel)
	// 3601 不大于 3750-100=3650，cnt=1，历史 [3601,3750]。
	check(t, d, c, 3750, 0, 0, RejectedImpossibleTravel)
	// 3750 > 3700 计入，3601 不计，cnt=2，冻结。
	check(t, d, c, 3800, 0, 0, RejectedImpossibleTravel)
	st, ok := d.State([]byte(c))
	if !ok || !st.Frozen {
		t.Fatalf("state = %+v, %v; want frozen", st, ok)
	}
	if want := []int64{3601, 3750, 3800}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v", st.Rejections, want)
	}
	if st.AnchorT != 3600 || st.AnchorX != 600 || st.AnchorY != 300 {
		t.Errorf("anchor = (%d,%d,%d), want (3600,600,300)", st.AnchorT, st.AnchorX, st.AnchorY)
	}
	check(t, d, c, 3800, 0, 0, RejectedFrozen)
}

// 速度恰等接受；d*3600 比 V*dt 大 1 拒绝。
func TestSpeedBoundary(t *testing.T) {
	d := mustNew(t, 3600, 100, 1000)
	check(t, d, "eq", 0, 0, 0, Accepted)
	// d=1, dt=1: 1*3600 == 3600*1，恰等接受。
	check(t, d, "eq", 1, 1, 0, Accepted)

	d2 := mustNew(t, 3599, 100, 1000)
	check(t, d2, "over", 0, 0, 0, Accepted)
	// d=1, dt=1: 1*3600 = 3600 = 3599*1 + 1，恰大 1，拒绝。
	check(t, d2, "over", 1, 1, 0, RejectedImpossibleTravel)
}

// dt 为 0：d 为 0 报重复，d 为 1 报不可能行程。
func TestZeroDt(t *testing.T) {
	d := mustNew(t, 1_000_000, 100, 1000)
	check(t, d, "c", 100, 5, 5, Accepted)
	check(t, d, "c", 100, 5, 5, RejectedDuplicate)
	check(t, d, "c", 100, 6, 5, RejectedImpossibleTravel)
}

// 被拒绝的一笔不推进锚点。
func TestRejectionKeepsAnchor(t *testing.T) {
	d := mustNew(t, 1, 100, 1000)
	check(t, d, "c", 0, 0, 0, Accepted)
	check(t, d, "c", 10, 1000, 0, RejectedImpossibleTravel)
	st, _ := d.State([]byte("c"))
	if st.AnchorT != 0 || st.AnchorX != 0 || st.AnchorY != 0 {
		t.Errorf("anchor = (%d,%d,%d), want (0,0,0)", st.AnchorT, st.AnchorX, st.AnchorY)
	}
	// 锚点未推进，慢速小步仍按原锚点判定：d=1, dt=1, 1*3600 > 1*1。
	check(t, d, "c", 1, 1, 0, RejectedImpossibleTravel)
}

// t 恰等于锚点 t 且坐标不同不算乱序；小 1 为乱序。
func TestOutOfOrderBoundary(t *testing.T) {
	d := mustNew(t, 1_000_000, 100, 1000)
	check(t, d, "c", 100, 0, 0, Accepted)
	// t == t0，坐标不同：不乱序，进入速度判定（dt=0, d=1）。
	check(t, d, "c", 100, 1, 0, RejectedImpossibleTravel)
	check(t, d, "c", 99, 0, 0, RejectedOutOfOrder)
}

// 差旅窗口：from 恰等免检，to 恰等不免检（左闭右开）。
func TestTravelWindowEdges(t *testing.T) {
	d := mustNew(t, 1, 100, 1000)
	if err := d.Declare([]byte("c"), 1000, 2000); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	check(t, d, "c", 0, 0, 0, Accepted)
	// t == from：免检接受，尽管速度不可行。
	check(t, d, "c", 1000, 1_000_000_000, 0, Accepted)

	d2 := mustNew(t, 1, 100, 1000)
	if err := d2.Declare([]byte("c"), 1000, 2000); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	check(t, d2, "c", 0, 0, 0, Accepted)
	// t == to：不免检，速度不可行，拒绝。
	check(t, d2, "c", 2000, 1_000_000_000, 0, RejectedImpossibleTravel)
}

// 免检接受后拒绝历史仍保留；Declare 只保留最后一次登记。
func TestTravelExemptKeepsHistory(t *testing.T) {
	d := mustNew(t, 1, 100, 1000)
	check(t, d, "c", 0, 0, 0, Accepted)
	check(t, d, "c", 10, 1000, 0, RejectedImpossibleTravel)
	if err := d.Declare([]byte("c"), 20, 30); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	check(t, d, "c", 25, 9999, 9999, Accepted)
	st, _ := d.State([]byte("c"))
	if want := []int64{10}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v", st.Rejections, want)
	}
	if st.AnchorT != 25 || st.AnchorX != 9999 || st.AnchorY != 9999 {
		t.Errorf("anchor = (%d,%d,%d), want (25,9999,9999)", st.AnchorT, st.AnchorX, st.AnchorY)
	}
	// 重新登记后旧窗口失效：t=25 已不在新窗口 [100,200)。
	if err := d.Declare([]byte("c"), 100, 200); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	check(t, d, "c", 26, 0, 0, RejectedImpossibleTravel)
	check(t, d, "c", 150, 0, 0, Accepted)
}

// Declare 参数校验。
func TestDeclareValidation(t *testing.T) {
	d := mustNew(t, 1, 1, 1)
	for _, tc := range []struct {
		card     string
		from, to int64
	}{
		{"", 0, 1},
		{"c", -1, 1},
		{"c", 5, 5},
		{"c", 6, 5},
		{"c", 0, 10_000_000_000_001},
	} {
		if err := d.Declare([]byte(tc.card), tc.from, tc.to); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("Declare(%q, %d, %d) = %v, want ErrInvalidParam", tc.card, tc.from, tc.to, err)
		}
	}
	if err := d.Declare([]byte("c"), 0, 10_000_000_000_000); err != nil {
		t.Errorf("Declare upper bound = %v, want nil", err)
	}
}

// 历史项恰等于 t-H 已过期；大 1 计入。
func TestWindowCountBoundary(t *testing.T) {
	// K=2, H=100：历史 [10]，t=110 时 10 恰等于 110-100，过期，cnt=1，不冻结。
	d := mustNew(t, 1, 2, 100)
	check(t, d, "c", 0, 0, 0, Accepted)
	check(t, d, "c", 10, 1000, 0, RejectedImpossibleTravel)
	check(t, d, "c", 110, 1000, 0, RejectedImpossibleTravel)
	if st, _ := d.State([]byte("c")); st.Frozen {
		t.Error("t-H 恰等的历史项应过期，不应冻结")
	}
	// 历史 [10,110]，t=209 时 110 > 209-100=109 计入，cnt=2，冻结。
	check(t, d, "c", 209, 1000, 0, RejectedImpossibleTravel)
	if st, _ := d.State([]byte("c")); !st.Frozen {
		t.Error("t-H 大 1 的历史项应计入，应冻结")
	}
}

// 历史项大于本笔 t（历史非单调）也计入。
func TestNonMonotonicHistory(t *testing.T) {
	d := mustNew(t, 1, 2, 100)
	check(t, d, "c", 0, 0, 0, Accepted)
	// 锚点停在 0：t=200 的拒绝先于 t=150 的拒绝入历史。
	check(t, d, "c", 200, 1000, 0, RejectedImpossibleTravel)
	// 200 > 150-100=50，计入，cnt=2，冻结。
	check(t, d, "c", 150, 1000, 0, RejectedImpossibleTravel)
	st, _ := d.State([]byte("c"))
	if !st.Frozen {
		t.Error("历史项 200 大于本笔 t=150 应计入 cnt，应冻结")
	}
	if want := []int64{200, 150}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v", st.Rejections, want)
	}
}

// 第 K 笔仍报不可能行程；冻结期间重复、乱序、不可能行程都报已冻结。
func TestFrozenBehavior(t *testing.T) {
	d := mustNew(t, 1, 2, 1000)
	check(t, d, "c", 0, 0, 0, Accepted)
	check(t, d, "c", 10, 1000, 0, RejectedImpossibleTravel)
	// 第 K 笔：报不可能行程而非已冻结，此后卡冻结。
	check(t, d, "c", 20, 1000, 0, RejectedImpossibleTravel)
	if st, _ := d.State([]byte("c")); !st.Frozen {
		t.Fatal("cnt 达到 K 后应冻结")
	}
	check(t, d, "c", 0, 0, 0, RejectedFrozen)     // 与锚点相同，本应是重复
	check(t, d, "c", 5, 0, 0, RejectedFrozen)     // t 小于锚点 t=0？否；用乱序笔见下
	check(t, d, "c", 30, 1000, 0, RejectedFrozen) // 本应是不可能行程
	check(t, d, "c", 10, 1000, 0, RejectedFrozen) // 冻结期间不改历史
	st, _ := d.State([]byte("c"))
	if want := []int64{10, 20}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v", st.Rejections, want)
	}
}

// 冻结期间乱序笔也报已冻结（t 小于锚点 t）。
func TestFrozenOutOfOrder(t *testing.T) {
	d := mustNew(t, 1, 1, 1000)
	check(t, d, "c", 100, 0, 0, Accepted)
	check(t, d, "c", 200, 1000, 0, RejectedImpossibleTravel) // K=1，冻结
	check(t, d, "c", 50, 0, 0, RejectedFrozen)               // 本应是乱序
	check(t, d, "c", 100, 0, 0, RejectedFrozen)              // 本应是重复
}

// Unfreeze：历史清空、锚点与差旅窗口保留；错误顺序为卡不存在、卡未冻结。
func TestUnfreeze(t *testing.T) {
	d := mustNew(t, 1, 1, 1000)
	if err := d.Unfreeze([]byte("ghost")); !errors.Is(err, ErrCardNotFound) {
		t.Errorf("Unfreeze(ghost) = %v, want ErrCardNotFound", err)
	}
	// 已登记但未冻结的卡：报未冻结而非不存在。
	if err := d.Declare([]byte("c"), 500, 600); err != nil {
		t.Fatalf("Declare: %v", err)
	}
	if err := d.Unfreeze([]byte("c")); !errors.Is(err, ErrCardNotFrozen) {
		t.Errorf("Unfreeze(not frozen) = %v, want ErrCardNotFrozen", err)
	}
	check(t, d, "c", 0, 0, 0, Accepted)
	check(t, d, "c", 10, 1000, 0, RejectedImpossibleTravel) // K=1，冻结
	if err := d.Unfreeze([]byte("c")); err != nil {
		t.Fatalf("Unfreeze: %v", err)
	}
	st, _ := d.State([]byte("c"))
	if st.Frozen || len(st.Rejections) != 0 {
		t.Errorf("state = %+v, want unfrozen with empty history", st)
	}
	if st.AnchorT != 0 || !st.HasAnchor {
		t.Errorf("anchor lost after Unfreeze: %+v", st)
	}
	if !st.HasTravel || st.TravelFrom != 500 || st.TravelTo != 600 {
		t.Errorf("travel window lost after Unfreeze: %+v", st)
	}
	// 锚点保留：与锚点 (0,0,0) 三项全同，报重复。
	check(t, d, "c", 0, 0, 0, RejectedDuplicate)
	// 差旅窗口保留：t=550 免检接受。
	check(t, d, "c", 550, 9999, 0, Accepted)
	// 历史已清空：t=700 在窗口外，拒绝后历史只含新的一笔（K=1 再冻结）。
	check(t, d, "c", 700, 0, 0, RejectedImpossibleTravel)
	st, _ = d.State([]byte("c"))
	if want := []int64{700}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v (历史应已被 Unfreeze 清空)", st.Rejections, want)
	}
}

// 参数非法的 Check 不改状态；空卡号拒绝。
func TestInvalidParamCheck(t *testing.T) {
	d := mustNew(t, 1, 1, 1)
	check(t, d, "", 0, 0, 0, RejectedInvalidParam)
	check(t, d, "c", -1, 0, 0, RejectedInvalidParam)
	check(t, d, "c", 1_000_000_000_001, 0, 0, RejectedInvalidParam)
	check(t, d, "c", 0, 1_000_000_001, 0, RejectedInvalidParam)
	check(t, d, "c", 0, 0, -1_000_000_001, RejectedInvalidParam)
	if _, ok := d.State([]byte("c")); ok {
		t.Error("非法 Check 不应创建卡")
	}
	if _, ok := d.State([]byte("")); ok {
		t.Error("空卡号不应创建卡")
	}
	// 边界值合法。
	check(t, d, "c", 1_000_000_000_000, 1_000_000_000, -1_000_000_000, Accepted)
}

// 题目批处理示例：批内乱序按 t 稳定排序处理，批内中途冻结。
func TestBatchExample(t *testing.T) {
	d := mustNew(t, 900, 2, 100)
	const c = "card"
	check(t, d, c, 0, 0, 0, Accepted)
	check(t, d, c, 3600, 600, 300, Accepted)
	// i0=(3800,0,0) i1=(3601,0,0) i2=(3750,0,0)，排序后处理 i1,i2,i0。
	got, err := d.CheckBatch([]byte(c), []Txn{
		{T: 3800, X: 0, Y: 0},
		{T: 3601, X: 0, Y: 0},
		{T: 3750, X: 0, Y: 0},
	})
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	want := []Decision{RejectedImpossibleTravel, RejectedImpossibleTravel, RejectedImpossibleTravel}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("batch results = %v, want %v", got, want)
	}
	st, _ := d.State([]byte(c))
	if !st.Frozen {
		t.Error("i0 处理时 cnt=2 应触发冻结")
	}
	if want := []int64{3601, 3750, 3800}; !reflect.DeepEqual(st.Rejections, want) {
		t.Errorf("rejections = %v, want %v", st.Rejections, want)
	}
}

// 批内冻结后，其后各笔（按排序次序）报已冻结。
func TestBatchFreezeMidway(t *testing.T) {
	d := mustNew(t, 1, 1, 1000)
	check(t, d, "c", 0, 0, 0, Accepted)
	// 排序后：t=10 不可能行程（K=1 冻结），t=20、t=30 报已冻结。
	got, err := d.CheckBatch([]byte("c"), []Txn{
		{T: 30, X: 1000, Y: 0},
		{T: 10, X: 1000, Y: 0},
		{T: 20, X: 1000, Y: 0},
	})
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	want := []Decision{RejectedFrozen, RejectedImpossibleTravel, RejectedFrozen}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("batch results = %v, want %v", got, want)
	}
}

// t 相同的批内各笔保持原下标次序（稳定排序）。
func TestBatchStableOrder(t *testing.T) {
	d := mustNew(t, 1_000_000, 100, 1000)
	check(t, d, "c", 0, 0, 0, Accepted)
	// 三笔 t 全为 5：第一笔接受并移动锚点，第二笔与锚点相同为重复，
	// 第三笔坐标不同且 dt=0、d=1 为不可能行程。若次序不稳结果会不同。
	got, err := d.CheckBatch([]byte("c"), []Txn{
		{T: 5, X: 1, Y: 0},
		{T: 5, X: 1, Y: 0},
		{T: 5, X: 2, Y: 0},
	})
	if err != nil {
		t.Fatalf("CheckBatch: %v", err)
	}
	want := []Decision{Accepted, RejectedDuplicate, RejectedImpossibleTravel}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("batch results = %v, want %v", got, want)
	}
}

// 批内任一笔非法或批大小越界则整批拒绝，不改状态。
func TestBatchValidation(t *testing.T) {
	d := mustNew(t, 1, 1, 1)
	check(t, d, "c", 0, 0, 0, Accepted)

	if _, err := d.CheckBatch([]byte("c"), nil); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("empty batch = %v, want ErrInvalidParam", err)
	}
	if _, err := d.CheckBatch([]byte(""), []Txn{{T: 1}}); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("empty card = %v, want ErrInvalidParam", err)
	}
	big := make([]Txn, 1001)
	if _, err := d.CheckBatch([]byte("c"), big); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("oversized batch = %v, want ErrInvalidParam", err)
	}
	// 首笔合法、次笔非法：整批拒绝，首笔不得生效。
	_, err := d.CheckBatch([]byte("c"), []Txn{
		{T: 1, X: 0, Y: 0},
		{T: 2, X: 2_000_000_000, Y: 0},
	})
	if !errors.Is(err, ErrInvalidParam) {
		t.Errorf("batch with invalid txn = %v, want ErrInvalidParam", err)
	}
	st, _ := d.State([]byte("c"))
	if st.AnchorT != 0 || st.AnchorX != 0 || st.AnchorY != 0 {
		t.Errorf("anchor = (%d,%d,%d), want (0,0,0)：非法批不得改状态", st.AnchorT, st.AnchorX, st.AnchorY)
	}
	// 合法批大小边界：1 与 1000。
	if _, err := d.CheckBatch([]byte("c"), []Txn{{T: 1, X: 0, Y: 0}}); err != nil {
		t.Errorf("size-1 batch = %v, want nil", err)
	}
	d2 := mustNew(t, 1_000_000, 100, 1000)
	full := make([]Txn, 1000)
	for i := range full {
		full[i] = Txn{T: int64(i), X: 0, Y: 0}
	}
	if _, err := d2.CheckBatch([]byte("c"), full); err != nil {
		t.Errorf("size-1000 batch = %v, want nil", err)
	}
}
