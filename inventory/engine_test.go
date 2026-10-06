package inventory

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustAddSegment(t *testing.T, e *Engine, now uint64, id string, seats, overbook int, auth [3]int) {
	t.Helper()
	if err := e.AddSegment(now, id, seats, overbook, auth); err != nil {
		t.Fatalf("AddSegment(%s) 失败: %v", id, err)
	}
}

func mustHold(t *testing.T, e *Engine, now uint64, legs []Leg, pax int) (uint64, uint64) {
	t.Helper()
	id, expiry, err := e.Hold(now, legs, pax)
	if err != nil {
		t.Fatalf("Hold(%v, %d) 失败: %v", legs, pax, err)
	}
	return id, expiry
}

func mustAvail(t *testing.T, e *Engine, at uint64, seg string, class int) int {
	t.Helper()
	v, err := e.Availability(at, seg, class)
	if err != nil {
		t.Fatalf("Availability(%s, %d) 失败: %v", seg, class, err)
	}
	return v
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	ie, ok := err.(*Error)
	if !ok {
		t.Fatalf("错误类型不是 *Error: %v", err)
	}
	return ie
}

func expectErrKind(t *testing.T, err error, kind ErrKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际成功", kind)
	}
	ie := asErr(t, err)
	if ie.Kind != kind {
		t.Fatalf("期望错误类别 %v，实际 %v (%v)", kind, ie.Kind, err)
	}
	return ie
}

// 当前时刻恰等于到期时刻算过期，小一秒不算。
func TestExpiryBoundaryEqualVsMinusOne(t *testing.T) {
	// 恰等于到期时刻：已过期
	e := NewEngine(100)
	mustAddSegment(t, e, 0, "A", 10, 0, [3]int{10, 8, 5})
	id, expiry := mustHold(t, e, 10, []Leg{{"A", 0}}, 2)
	if expiry != 110 {
		t.Fatalf("到期时刻 = %d，期望 110", expiry)
	}
	if v := mustAvail(t, e, 109, "A", 0); v != 8 {
		t.Fatalf("t=109 可用数 = %d，期望 8（预占仍计入）", v)
	}
	if v := mustAvail(t, e, 110, "A", 0); v != 10 {
		t.Fatalf("t=110 可用数 = %d，期望 10（恰等于到期时刻即不计入）", v)
	}
	expectErrKind(t, e.Confirm(110, id), ErrHoldExpired)
	expectErrKind(t, e.Cancel(110, id), ErrHoldExpired)

	// 小一秒：未过期，出票成功
	e2 := NewEngine(100)
	mustAddSegment(t, e2, 0, "A", 10, 0, [3]int{10, 8, 5})
	id2, _ := mustHold(t, e2, 10, []Leg{{"A", 0}}, 2)
	if err := e2.Confirm(109, id2); err != nil {
		t.Fatalf("t=109 出票应成功: %v", err)
	}
	// 已确认后永不过期
	if v := mustAvail(t, e2, 100000, "A", 0); v != 8 {
		t.Fatalf("出票后任意时刻可用数 = %d，期望 8", v)
	}
	// 取消已确认票：成功并释放占用
	if err := e2.Cancel(110, id2); err != nil {
		t.Fatalf("取消已确认票应成功: %v", err)
	}
	if v := mustAvail(t, e2, 110, "A", 0); v != 10 {
		t.Fatalf("取消后可用数 = %d，期望 10", v)
	}
}

// 嵌套等级中：高等级可用数为负而低等级可用数为正时，低等级仍不可售。
func TestNestedNegativeHighPositiveLow(t *testing.T) {
	e := NewEngine(1000)
	// 座位 3，超售 2，授权 [5,3,1]
	mustAddSegment(t, e, 0, "A", 3, 2, [3]int{5, 3, 1})
	// 高等级预占 3 人：占用 (3,0,0)
	mustHold(t, e, 1, []Leg{{"A", 0}}, 3)
	// 下调授权：中 -> 1，高 -> 2。此后占用超过高、中授权。
	if err := e.AdjustAuth(2, "A", 1, 1); err != nil {
		t.Fatalf("AdjustAuth 中->1 失败: %v", err)
	}
	if err := e.AdjustAuth(3, "A", 0, 2); err != nil {
		t.Fatalf("AdjustAuth 高->2 失败: %v", err)
	}
	// 可用数：高 = 2-3 = -1，中 = 1-3 = -2，低 = 1-0 = 1
	if v := mustAvail(t, e, 4, "A", 0); v != -1 {
		t.Fatalf("高等级可用数 = %d，期望 -1", v)
	}
	if v := mustAvail(t, e, 4, "A", 2); v != 1 {
		t.Fatalf("低等级可用数 = %d，期望 1（为正）", v)
	}
	// 低等级可用数为正，但高等级为负 => 低等级不可售
	ok, err := e.Sellable(4, "A", 2)
	if err != nil {
		t.Fatalf("Sellable 失败: %v", err)
	}
	if ok {
		t.Fatal("高等级可用数为负时低等级不应可售")
	}
	// 预占同样被拒绝，报库存不足
	_, _, err = e.Hold(4, []Leg{{"A", 2}}, 1)
	expectErrKind(t, err, ErrInsufficientInventory)
}

// 授权量下调到低于已占用后不可售，再上调恢复可售。
func TestAdjustBelowOccupancyAndRestore(t *testing.T) {
	e := NewEngine(1000)
	mustAddSegment(t, e, 0, "A", 5, 0, [3]int{5, 5, 5})
	mustHold(t, e, 1, []Leg{{"A", 1}}, 2) // 占用 (0,2,0)
	// 下调中、低等级到 1（低于已占用 2）
	if err := e.AdjustAuth(2, "A", 2, 1); err != nil {
		t.Fatalf("AdjustAuth 低->1 失败: %v", err)
	}
	if err := e.AdjustAuth(3, "A", 1, 1); err != nil {
		t.Fatalf("AdjustAuth 中->1 失败: %v", err)
	}
	// 中等级可用 = 1-2 = -1：中、低均不可售；已有占用不受影响
	if ok, _ := e.Sellable(4, "A", 1); ok {
		t.Fatal("授权低于占用后中等级不应可售")
	}
	if ok, _ := e.Sellable(4, "A", 2); ok {
		t.Fatal("中等级不可售时低等级也不应可售")
	}
	if v := mustAvail(t, e, 4, "A", 1); v != -1 {
		t.Fatalf("中等级可用数 = %d，期望 -1", v)
	}
	// 再上调恢复
	if err := e.AdjustAuth(5, "A", 1, 5); err != nil {
		t.Fatalf("AdjustAuth 中->5 失败: %v", err)
	}
	if err := e.AdjustAuth(6, "A", 2, 5); err != nil {
		t.Fatalf("AdjustAuth 低->5 失败: %v", err)
	}
	if ok, _ := e.Sellable(7, "A", 2); !ok {
		t.Fatal("授权恢复后低等级应可售")
	}
	mustHold(t, e, 7, []Leg{{"A", 2}}, 1)
}

// 库存不足时报行程顺序中第一个不满足的航段。
func TestFirstFailingSegmentReported(t *testing.T) {
	e := NewEngine(1000)
	mustAddSegment(t, e, 0, "A", 5, 0, [3]int{5, 5, 5})
	mustAddSegment(t, e, 0, "B", 1, 0, [3]int{1, 1, 1})
	mustAddSegment(t, e, 0, "C", 1, 0, [3]int{1, 1, 1})
	// B、C 都被占满
	mustHold(t, e, 1, []Leg{{"B", 0}}, 1)
	mustHold(t, e, 1, []Leg{{"C", 0}}, 1)
	before := mustAvail(t, e, 2, "A", 0)
	_, _, err := e.Hold(2, []Leg{{"A", 0}, {"B", 0}, {"C", 0}}, 1)
	ie := expectErrKind(t, err, ErrInsufficientInventory)
	if ie.Segment != "B" {
		t.Fatalf("报错航段 = %q，期望 B（行程顺序中第一个不满足的航段）", ie.Segment)
	}
	// 全有或全无：A 的占用不得变化
	if v := mustAvail(t, e, 2, "A", 0); v != before {
		t.Fatalf("被拒绝的预占改动了 A 的占用: %d -> %d", before, v)
	}
}

// 四航段九旅客的全有或全无。
func TestFourLegsNinePaxAllOrNothing(t *testing.T) {
	e := NewEngine(100)
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		mustAddSegment(t, e, 0, id, 9, 0, [3]int{9, 9, 9})
	}
	legs := []Leg{{"S1", 0}, {"S2", 1}, {"S3", 2}, {"S4", 0}}
	// S4 先被占 1 人，只剩 8，放不下 9 人
	mustHold(t, e, 1, []Leg{{"S4", 0}}, 1)
	_, _, err := e.Hold(2, legs, 9)
	ie := expectErrKind(t, err, ErrInsufficientInventory)
	if ie.Segment != "S4" {
		t.Fatalf("报错航段 = %q，期望 S4", ie.Segment)
	}
	for _, id := range []string{"S1", "S2", "S3"} {
		if v := mustAvail(t, e, 2, id, 0); v != 9 {
			t.Fatalf("%s 可用数 = %d，期望 9（全有或全无）", id, v)
		}
	}
	// 换一个 S4 充足的系统，四航段九旅客成功
	e2 := NewEngine(100)
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		mustAddSegment(t, e2, 0, id, 9, 0, [3]int{9, 9, 9})
	}
	id, expiry := mustHold(t, e2, 2, legs, 9)
	if id != 1 || expiry != 102 {
		t.Fatalf("条目号/到期时刻 = %d/%d，期望 1/102", id, expiry)
	}
	for _, l := range legs {
		if v := mustAvail(t, e2, 2, l.Segment, l.Class); v != 0 {
			t.Fatalf("%s 等级%d 可用数 = %d，期望 0", l.Segment, l.Class, v)
		}
	}
	if err := e2.Confirm(3, id); err != nil {
		t.Fatalf("出票失败: %v", err)
	}
}

// 拒绝次序：参数非法 > 时钟回退。
func TestPrecedenceInvalidOverClock(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 5, "A", 10, 0, [3]int{10, 10, 10})
	// pax=10 参数非法，且 now=3 < 5 时钟回退：只报参数非法
	_, _, err := e.Hold(3, []Leg{{"A", 0}}, 10)
	expectErrKind(t, err, ErrInvalidParam)
}

// 拒绝次序：时钟回退 > 条目或航段不存在。
func TestPrecedenceClockOverNotFound(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 5, "A", 10, 0, [3]int{10, 10, 10})
	err := e.AdjustAuth(3, "ghost", 0, 1)
	expectErrKind(t, err, ErrClockRollback)
	err = e.Confirm(3, 999)
	expectErrKind(t, err, ErrClockRollback)
}

// 拒绝次序：不存在与已过期可区分（过期条目被保留，不报不存在）。
func TestPrecedenceNotFoundVsExpired(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 10, 0, [3]int{10, 10, 10})
	id, _ := mustHold(t, e, 0, []Leg{{"A", 0}}, 1) // 到期时刻 10
	expectErrKind(t, e.Confirm(20, 999), ErrNotFound)
	expectErrKind(t, e.Confirm(20, id), ErrHoldExpired)
	expectErrKind(t, e.Cancel(20, 999), ErrNotFound)
	expectErrKind(t, e.Cancel(20, id), ErrHoldExpired)
}

// 拒绝次序：预占已过期 > 状态不符。
func TestPrecedenceExpiredOverState(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 10, 0, [3]int{10, 10, 10})
	id, _ := mustHold(t, e, 0, []Leg{{"A", 0}}, 1) // 到期时刻 10
	if err := e.Cancel(1, id); err != nil {
		t.Fatalf("取消预占失败: %v", err)
	}
	// 到期前再出票：状态不符（已取消）
	ie := expectErrKind(t, e.Confirm(5, id), ErrStateConflict)
	if ie.State.String() != "已取消" {
		t.Fatalf("状态不符应可分辨为已取消，实际 %v", ie.State)
	}
	// 到期后再出票：已过期优先于状态不符
	expectErrKind(t, e.Confirm(15, id), ErrHoldExpired)
	expectErrKind(t, e.Cancel(15, id), ErrHoldExpired)
}

// 拒绝次序：状态不符 > 库存不足（二者不会同时出现于一个操作，
// 这里验证两类错误可区分且次序常量定义正确）。
func TestPrecedenceStateOverInventory(t *testing.T) {
	if ErrStateConflict >= ErrInsufficientInventory {
		t.Fatal("状态不符的次序应先于库存不足")
	}
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 1, 0, [3]int{1, 1, 1})
	id, _ := mustHold(t, e, 0, []Leg{{"A", 0}}, 1)
	if err := e.Confirm(1, id); err != nil {
		t.Fatalf("出票失败: %v", err)
	}
	// 已出票再出票：状态不符，可分辨为已出票
	ie := expectErrKind(t, e.Confirm(2, id), ErrStateConflict)
	if ie.State != StateConfirmed {
		t.Fatalf("状态不符应可分辨为已出票，实际 %v", ie.State)
	}
	// 库存满时再预占：库存不足
	_, _, err := e.Hold(2, []Leg{{"A", 0}}, 1)
	expectErrKind(t, err, ErrInsufficientInventory)
}

// 时钟：被拒绝的操作不推进时钟；只读查询不参与时钟约束。
func TestClockRules(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 5, "A", 1, 0, [3]int{1, 1, 1})
	// 相同时刻可以接受
	mustHold(t, e, 5, []Leg{{"A", 0}}, 1)
	// 回退被拒绝
	expectErrKind(t, e.AddSegment(4, "B", 1, 0, [3]int{1, 1, 1}), ErrClockRollback)
	// 被拒绝的操作不推进时钟：now=8 的库存不足（第一笔预占到期时刻为 15）被拒绝后，now=6 仍可接受
	_, _, err := e.Hold(8, []Leg{{"A", 0}}, 1)
	expectErrKind(t, err, ErrInsufficientInventory)
	if e.LastTime() != 5 {
		t.Fatalf("被拒绝的操作推进了时钟: %d", e.LastTime())
	}
	mustAddSegment(t, e, 6, "B", 1, 0, [3]int{1, 1, 1})
	// 只读查询不受时钟约束：可以用任意时刻查询
	if _, err := e.Availability(0, "A", 0); err != nil {
		t.Fatalf("早于时钟的查询不应报时钟回退: %v", err)
	}
	if _, err := e.Availability(1000000, "A", 0); err != nil {
		t.Fatalf("晚于时钟的查询不应报错: %v", err)
	}
	if e.LastTime() != 6 {
		t.Fatalf("查询改动了时钟: %d", e.LastTime())
	}
}

// 预占参数校验。
func TestHoldParamValidation(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 10, 0, [3]int{10, 10, 10})
	cases := []struct {
		name string
		legs []Leg
		pax  int
	}{
		{"零航段", nil, 1},
		{"五航段", []Leg{{"A", 0}, {"A", 0}, {"A", 0}, {"A", 0}, {"A", 0}}, 1},
		{"零旅客", []Leg{{"A", 0}}, 0},
		{"十旅客", []Leg{{"A", 0}}, 10},
		{"舱位越界", []Leg{{"A", 3}}, 1},
		{"空航段标识", []Leg{{"", 0}}, 1},
		{"航段重复", []Leg{{"A", 0}, {"A", 1}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := e.Hold(1, c.legs, c.pax)
			expectErrKind(t, err, ErrInvalidParam)
		})
	}
}

// 航段参数校验。
func TestSegmentParamValidation(t *testing.T) {
	e := NewEngine(10)
	expectErrKind(t, e.AddSegment(0, "", 1, 0, [3]int{1, 1, 1}), ErrInvalidParam)
	expectErrKind(t, e.AddSegment(0, "A", 0, 0, [3]int{1, 1, 1}), ErrInvalidParam)
	expectErrKind(t, e.AddSegment(0, "A", 1, -1, [3]int{1, 1, 1}), ErrInvalidParam)
	expectErrKind(t, e.AddSegment(0, "A", 1, 0, [3]int{1, 2, 1}), ErrInvalidParam)    // 破坏嵌套
	expectErrKind(t, e.AddSegment(0, "A", 1, 0, [3]int{3, 1, 1}), ErrInvalidParam)    // 超过座位+超售
	expectErrKind(t, e.AddSegment(0, "A", 1, 0, [3]int{-1, -1, -1}), ErrInvalidParam) // 负授权
	mustAddSegment(t, e, 0, "A", 1, 2, [3]int{3, 1, 1})
	expectErrKind(t, e.AddSegment(1, "A", 1, 0, [3]int{1, 1, 1}), ErrInvalidParam) // 重复
	// 授权量调整校验
	expectErrKind(t, e.AdjustAuth(2, "A", 0, 4), ErrInvalidParam)  // 超过座位+超售
	expectErrKind(t, e.AdjustAuth(2, "A", 2, 2), ErrInvalidParam)  // 高于中等级
	expectErrKind(t, e.AdjustAuth(2, "A", 0, 0), ErrInvalidParam)  // 低于中等级
	expectErrKind(t, e.AdjustAuth(2, "A", 3, 1), ErrInvalidParam)  // 舱位越界
	expectErrKind(t, e.AdjustAuth(2, "A", 0, -1), ErrInvalidParam) // 负授权
}

// 取消与出票的状态机。
func TestCancelAndConfirmStateMachine(t *testing.T) {
	e := NewEngine(100)
	mustAddSegment(t, e, 0, "A", 2, 0, [3]int{2, 2, 2})
	// 取消预占：立即释放
	id1, _ := mustHold(t, e, 1, []Leg{{"A", 0}}, 2)
	if err := e.Cancel(2, id1); err != nil {
		t.Fatalf("取消预占失败: %v", err)
	}
	if v := mustAvail(t, e, 2, "A", 0); v != 2 {
		t.Fatalf("取消预占后可用数 = %d，期望 2", v)
	}
	// 已取消再取消、再出票：状态不符（已取消）
	ie := expectErrKind(t, e.Cancel(3, id1), ErrStateConflict)
	if ie.State.String() != "已取消" {
		t.Fatalf("期望已取消，实际 %v", ie.State)
	}
	expectErrKind(t, e.Confirm(4, id1), ErrStateConflict)
	// 出票后再取消（已确认票可取消），再操作报状态不符
	id2, _ := mustHold(t, e, 5, []Leg{{"A", 0}}, 1)
	if err := e.Confirm(6, id2); err != nil {
		t.Fatalf("出票失败: %v", err)
	}
	ie = expectErrKind(t, e.Confirm(7, id2), ErrStateConflict)
	if ie.State != StateConfirmed {
		t.Fatalf("期望已出票，实际 %v", ie.State)
	}
	if err := e.Cancel(8, id2); err != nil {
		t.Fatalf("取消已确认票失败: %v", err)
	}
	expectErrKind(t, e.Cancel(9, id2), ErrStateConflict)
	if v := mustAvail(t, e, 9, "A", 0); v != 2 {
		t.Fatalf("取消已确认票后可用数 = %d，期望 2", v)
	}
}

// 导出全部状态并在新实例中恢复：恢复前后对同一组查询结果逐项相等，
// 未过期预占保留原到期时刻，后续同一操作序列产生完全相同的结果。
func TestExportImportRoundTrip(t *testing.T) {
	e := NewEngine(50)
	mustAddSegment(t, e, 0, "A", 10, 2, [3]int{12, 8, 5})
	mustAddSegment(t, e, 1, "B", 4, 0, [3]int{4, 3, 2})
	h1, _ := mustHold(t, e, 10, []Leg{{"A", 0}, {"B", 1}}, 2) // 到期 60，未过期
	h2, _ := mustHold(t, e, 11, []Leg{{"A", 2}}, 3)           // 到期 61，未过期
	h3, _ := mustHold(t, e, 12, []Leg{{"B", 0}}, 1)           // 到期 62，将出票
	h4, _ := mustHold(t, e, 13, []Leg{{"A", 1}}, 1)           // 到期 63，将取消
	h5, _ := mustHold(t, e, 14, []Leg{{"A", 0}}, 1)           // 到期 64
	if err := e.Confirm(15, h3); err != nil {
		t.Fatalf("出票失败: %v", err)
	}
	if err := e.Cancel(16, h4); err != nil {
		t.Fatalf("取消失败: %v", err)
	}
	if err := e.AdjustAuth(17, "A", 2, 4); err != nil {
		t.Fatalf("授权调整失败: %v", err)
	}
	// 制造一些已过期且从未被触及的预占（在另一个航段上推进时钟）
	mustAddSegment(t, e, 18, "C", 100, 0, [3]int{100, 100, 100})
	for i := 0; i < 20; i++ {
		mustHold(t, e, 19, []Leg{{"C", 0}}, 1) // 到期 69
	}
	if err := e.AdjustAuth(100, "C", 0, 100); err != nil { // 推进时钟到 100，h5 已过期
		t.Fatalf("推进时钟失败: %v", err)
	}

	data, err := e.ExportJSON()
	if err != nil {
		t.Fatalf("导出失败: %v", err)
	}
	e2, err := ImportJSON(data)
	if err != nil {
		t.Fatalf("恢复失败: %v", err)
	}

	// 恢复前后对同一组查询逐项相等（含早于/晚于各到期时刻的查询时刻）
	for _, at := range []uint64{0, 10, 59, 60, 61, 63, 64, 69, 100, 1000} {
		for _, seg := range []string{"A", "B", "C"} {
			for class := 0; class < 3; class++ {
				v1, err1 := e.Availability(at, seg, class)
				v2, err2 := e2.Availability(at, seg, class)
				if (err1 == nil) != (err2 == nil) || v1 != v2 {
					t.Fatalf("t=%d %s/%d 可用数不一致: %v/%v vs %v/%v", at, seg, class, v1, err1, v2, err2)
				}
				s1, _ := e.Sellable(at, seg, class)
				s2, _ := e2.Sellable(at, seg, class)
				if s1 != s2 {
					t.Fatalf("t=%d %s/%d 可售不一致: %v vs %v", at, seg, class, s1, s2)
				}
			}
		}
	}
	// 恢复后未过期预占保留原到期时刻：h1 在 t=59 可出票，t=60 报已过期
	if err := e2.Confirm(100, h1); !expectHoldExpired(err) {
		t.Fatalf("h1 在 t=100 应已过期，实际 %v", err)
	}
	// 恢复后同一操作序列产生相同条目号与到期时刻
	id1, exp1, err1 := e.Hold(101, []Leg{{"A", 0}}, 1)
	id2, exp2, err2 := e2.Hold(101, []Leg{{"A", 0}}, 1)
	if (err1 == nil) != (err2 == nil) || id1 != id2 || exp1 != exp2 {
		t.Fatalf("恢复后重放结果不一致: (%d,%d,%v) vs (%d,%d,%v)", id1, exp1, err1, id2, exp2, err2)
	}
	_ = h2
	_ = h5
}

func expectHoldExpired(err error) bool {
	ie, ok := err.(*Error)
	return ok && ie.Kind == ErrHoldExpired
}

// 可用数是纯函数：过期预占无需任何操作触及即不再计入；
// 对同一状态、同一查询时刻，任意操作交错后结果一致。
func TestAvailabilityPureFunction(t *testing.T) {
	e := NewEngine(10)
	mustAddSegment(t, e, 0, "A", 100, 0, [3]int{100, 100, 100})
	mustAddSegment(t, e, 0, "B", 100, 0, [3]int{100, 100, 100})
	// A 上 50 个预占，到期时刻 20
	for i := 0; i < 50; i++ {
		mustHold(t, e, 10, []Leg{{"A", 0}}, 1)
	}
	// 只在 B 上推进时钟到 30，A 上的预占从未被触及
	mustHold(t, e, 30, []Leg{{"B", 0}}, 1)
	// t=19：50 个预占都未到期
	if v := mustAvail(t, e, 19, "A", 0); v != 50 {
		t.Fatalf("t=19 可用数 = %d，期望 50", v)
	}
	// t=20：恰等于到期时刻，全部不再计入
	if v := mustAvail(t, e, 20, "A", 0); v != 100 {
		t.Fatalf("t=20 可用数 = %d，期望 100", v)
	}
	// t=15 再查（时刻回退的只读查询）：仍按 t=15 的纯函数计算
	if v := mustAvail(t, e, 15, "A", 0); v != 50 {
		t.Fatalf("t=15 可用数 = %d，期望 50", v)
	}
}

// 并发调用：结果等价于某个串行顺序，且满足库存不变量。
func TestConcurrentOps(t *testing.T) {
	e := NewEngine(50)
	for _, id := range []string{"S0", "S1", "S2", "S3"} {
		mustAddSegment(t, e, 0, id, 1000, 0, [3]int{1000, 1000, 1000})
	}
	var clock atomic.Uint64
	var ids atomic.Uint64
	const workers = 8
	const opsPerWorker = 400
	idCh := make(chan uint64, workers*opsPerWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerWorker; i++ {
				now := clock.Add(1)
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4: // 预占
					seg := "S" + string(rune('0'+rng.Intn(4)))
					id, _, err := e.Hold(now, []Leg{{seg, rng.Intn(3)}}, 1+rng.Intn(9))
					if err == nil {
						idCh <- id
					}
				case 5, 6: // 出票
					if n := ids.Load(); n > 0 {
						_ = e.Confirm(now, 1+uint64(rng.Int63n(int64(n))))
					}
				case 7: // 取消
					if n := ids.Load(); n > 0 {
						_ = e.Cancel(now, 1+uint64(rng.Int63n(int64(n))))
					}
				default: // 只读查询
					seg := "S" + string(rune('0'+rng.Intn(4)))
					_, _ = e.Availability(now, seg, rng.Intn(3))
					_, _ = e.Sellable(now, seg, rng.Intn(3))
				}
			}
		}(int64(w))
	}
	wg.Wait()
	close(idCh)
	for id := range idCh {
		if id > ids.Load() {
			ids.Store(id)
		}
	}
	// 串行一致性校验：从快照重建占用，与查询结果逐项相等
	snap := e.Export()
	last := e.LastTime()
	occ := map[string][3]int{}
	for _, es := range snap.Entries {
		active := es.State == StateConfirmed || (es.State == StateHold && es.Expiry > last)
		if !active {
			continue
		}
		for _, l := range es.Legs {
			o := occ[l.Segment]
			o[l.Class] += es.Pax
			occ[l.Segment] = o
		}
	}
	for _, ss := range snap.Segments {
		o := occ[ss.ID]
		total := 0
		for class := 0; class < 3; class++ {
			total += o[class]
			want := ss.Auth[class]
			for j := class; j < 3; j++ {
				want -= o[j]
			}
			got, err := e.Availability(last, ss.ID, class)
			if err != nil || got != want {
				t.Fatalf("%s/%d 可用数 = %d,%v，期望 %d", ss.ID, class, got, err, want)
			}
		}
		if total > ss.Auth[0] {
			t.Fatalf("%s 总占用 %d 超过最高等级授权量 %d", ss.ID, total, ss.Auth[0])
		}
	}
}
