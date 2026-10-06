package seats

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func newSystem(t *testing.T, holdDur int64) *System {
	t.Helper()
	s, err := NewSystem(holdDur)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return s
}

func addSeg(t *testing.T, s *System, id string, physical, overbook int, auth [NumClasses]int, now int64) {
	t.Helper()
	if err := s.AddSegment(id, physical, overbook, auth, now); err != nil {
		t.Fatalf("AddSegment(%s): %v", id, err)
	}
}

func mustHold(t *testing.T, s *System, legs []Leg, pax int, now int64) (string, int64) {
	t.Helper()
	id, expiry, err := s.Hold(legs, pax, now)
	if err != nil {
		t.Fatalf("Hold(%v, %d, %d): %v", legs, pax, now, err)
	}
	return id, expiry
}

func assertKind(t *testing.T, err error, want RejKind) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected rejection %v, got %v", want, err)
	}
	if e.Kind != want {
		t.Fatalf("expected rejection %v, got %v (%v)", want, e.Kind, e)
	}
	return e
}

func avail(t *testing.T, s *System, seg string, class int) int {
	t.Helper()
	a, err := s.Availability(seg, class)
	if err != nil {
		t.Fatalf("Availability(%s, %d): %v", seg, class, err)
	}
	return a
}

// 当前时刻恰等于到期时刻即视为过期；小一秒则未过期。
func TestExpiryBoundary(t *testing.T) {
	s := newSystem(t, 10)
	addSeg(t, s, "S1", 5, 0, [NumClasses]int{5, 3, 2}, 0)

	h1, expiry := mustHold(t, s, []Leg{{Segment: "S1", Class: 2}}, 1, 0)
	if expiry != 10 {
		t.Fatalf("expiry = %d, want 10", expiry)
	}
	if got := avail(t, s, "S1", 2); got != 1 {
		t.Fatalf("avail before expiry = %d, want 1", got)
	}
	// 到期前一秒（expiry-1）：未过期，取消成功。
	if err := s.Cancel(h1, 9); err != nil {
		t.Fatalf("cancel at expiry-1: %v", err)
	}
	if got := avail(t, s, "S1", 2); got != 2 {
		t.Fatalf("avail after cancel = %d, want 2", got)
	}

	h2, expiry2 := mustHold(t, s, []Leg{{Segment: "S1", Class: 2}}, 1, 9)
	if expiry2 != 19 {
		t.Fatalf("expiry = %d, want 19", expiry2)
	}
	// 恰等于到期时刻：出票与取消都报预占已过期。
	assertKind(t, s.Ticket(h2, 19), KindHoldExpired)
	assertKind(t, s.Cancel(h2, 20), KindHoldExpired)
	// 与条目不存在可区分。
	assertKind(t, s.Ticket("E000999", 20), KindNotFound)
	// 被拒绝的操作不推进时钟。
	if got := s.Now(); got != 9 {
		t.Fatalf("clock after rejections = %d, want 9", got)
	}
	// 接受一个 20 时刻的操作后，过期预占不再计入任何可用数。
	mustHold(t, s, []Leg{{Segment: "S1", Class: 1}}, 1, 20)
	if got := avail(t, s, "S1", 2); got != 2 {
		t.Fatalf("avail at class 2 = %d, want 2 (expired hold not counted)", got)
	}
	if got := avail(t, s, "S1", 1); got != 2 {
		t.Fatalf("avail at class 1 = %d, want 2", got)
	}
	// 到期前一秒出票成功；已出票条目再次出票报状态不符而非已过期。
	h4, _ := mustHold(t, s, []Leg{{Segment: "S1", Class: 0}}, 1, 20)
	if err := s.Ticket(h4, 29); err != nil {
		t.Fatalf("ticket at expiry-1: %v", err)
	}
	e := assertKind(t, s.Ticket(h4, 30), KindStateConflict)
	if e.Status != StatusTicketed {
		t.Fatalf("conflict status = %v, want ticketed", e.Status)
	}
}

// 过期预占即使从未被任何操作触及，也不计入可用数（纯函数性质）。
func TestExpiredUntouchedHoldNotCounted(t *testing.T) {
	s := newSystem(t, 10)
	addSeg(t, s, "S1", 5, 0, [NumClasses]int{5, 5, 5}, 0)
	addSeg(t, s, "S2", 5, 0, [NumClasses]int{5, 5, 5}, 0)
	mustHold(t, s, []Leg{{Segment: "S1", Class: 0}}, 3, 0) // expiry 10
	// 只操作 S2 推进时钟，S1 上的预占从未被触及。
	mustHold(t, s, []Leg{{Segment: "S2", Class: 0}}, 1, 20)
	if got := avail(t, s, "S1", 0); got != 5 {
		t.Fatalf("avail = %d, want 5 (expired untouched hold not counted)", got)
	}
}

// 嵌套等级中高等级可用数为负而低等级可用数为正时，低等级不可售。
func TestNestedHighNegativeLowPositive(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S", 3, 0, [NumClasses]int{3, 2, 1}, 0)
	mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 3, 0) // 高等级占满
	// 下调最高等级授权量到低于已占用数：合法（不破坏嵌套）。
	if err := s.AdjustAuth("S", 0, 2, 1); err != nil {
		t.Fatalf("AdjustAuth: %v", err)
	}
	if got := avail(t, s, "S", 0); got != -1 {
		t.Fatalf("avail class 0 = %d, want -1", got)
	}
	if got := avail(t, s, "S", 2); got != 1 {
		t.Fatalf("avail class 2 = %d, want 1", got)
	}
	// 低等级可用数为正，但高等级为负 -> 低等级不可售。
	ok, seg, err := s.CanHold([]Leg{{Segment: "S", Class: 2}}, 1)
	if err != nil || ok {
		t.Fatalf("CanHold = %v, %v; want not sellable", ok, err)
	}
	_, _, herr := s.Hold([]Leg{{Segment: "S", Class: 2}}, 1, 2)
	e := assertKind(t, herr, KindInsufficient)
	if e.Segment != "S" {
		t.Fatalf("failing segment = %q, want S", e.Segment)
	}
	_ = seg
}

// 授权量下调到低于已占用后不可售，再上调恢复可售；已有占用不受影响。
func TestAdjustBelowOccupancyAndRestore(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S", 4, 0, [NumClasses]int{4, 3, 2}, 0)
	mustHold(t, s, []Leg{{Segment: "S", Class: 2}}, 2, 0) // 低等级占 2

	if err := s.AdjustAuth("S", 2, 1, 1); err != nil {
		t.Fatalf("AdjustAuth below occupancy: %v", err)
	}
	// 授权量(1) < 已占用(2) -> 可用数为负，该等级不可售。
	if got := avail(t, s, "S", 2); got != -1 {
		t.Fatalf("avail class 2 = %d, want -1", got)
	}
	if ok, _, err := s.CanHold([]Leg{{Segment: "S", Class: 2}}, 1); err != nil || ok {
		t.Fatalf("CanHold after lowering = %v, %v; want not sellable", ok, err)
	}
	// 高等级不受低等级授权量下调影响。
	if ok, _, err := s.CanHold([]Leg{{Segment: "S", Class: 1}}, 1); err != nil || !ok {
		t.Fatalf("CanHold class 1 = %v, %v; want sellable", ok, err)
	}
	// 再上调恢复可售；已有占用仍在。
	if err := s.AdjustAuth("S", 2, 3, 2); err != nil {
		t.Fatalf("AdjustAuth restore: %v", err)
	}
	if got := avail(t, s, "S", 2); got != 1 {
		t.Fatalf("avail class 2 = %d, want 1", got)
	}
	if ok, _, err := s.CanHold([]Leg{{Segment: "S", Class: 2}}, 1); err != nil || !ok {
		t.Fatalf("CanHold after restore = %v, %v; want sellable", ok, err)
	}
	// 破坏嵌套次序或超过上限的调整报参数非法。
	assertKind(t, s.AdjustAuth("S", 1, 0, 3), KindInvalidParam)  // 0 < auth[2]=3
	assertKind(t, s.AdjustAuth("S", 0, 5, 4), KindInvalidParam)  // 5 > physical+overbook=4
	assertKind(t, s.AdjustAuth("S", 2, -1, 5), KindInvalidParam) // 负授权量
	assertKind(t, s.AdjustAuth("S", 3, 1, 6), KindInvalidParam)  // 等级越界
	assertKind(t, s.AdjustAuth("SX", 0, 1, 7), KindNotFound)     // 航段不存在
	if got := s.Now(); got != 2 {
		t.Fatalf("clock = %d, want 2 (rejections do not advance clock)", got)
	}
}

// 库存不足时报出的航段为行程顺序中第一个不满足的航段。
func TestFirstFailingSegment(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "A", 2, 0, [NumClasses]int{2, 2, 2}, 0)
	addSeg(t, s, "B", 1, 0, [NumClasses]int{1, 1, 1}, 0)
	addSeg(t, s, "C", 1, 0, [NumClasses]int{1, 1, 1}, 0)
	mustHold(t, s, []Leg{{Segment: "B", Class: 0}}, 1, 0) // B 占满
	mustHold(t, s, []Leg{{Segment: "C", Class: 0}}, 1, 0) // C 也占满

	_, _, err := s.Hold([]Leg{
		{Segment: "A", Class: 0},
		{Segment: "B", Class: 0},
		{Segment: "C", Class: 0},
	}, 1, 1)
	e := assertKind(t, err, KindInsufficient)
	if e.Segment != "B" {
		t.Fatalf("failing segment = %q, want B (first in itinerary order)", e.Segment)
	}
	// 被拒绝的预占不改动任何状态。
	if got := avail(t, s, "A", 0); got != 2 {
		t.Fatalf("avail A = %d, want 2 (rejected hold changed nothing)", got)
	}
	// 调换顺序后第一个不满足的航段变为 C。
	_, _, err = s.Hold([]Leg{
		{Segment: "C", Class: 0},
		{Segment: "A", Class: 0},
		{Segment: "B", Class: 0},
	}, 1, 2)
	e = assertKind(t, err, KindInsufficient)
	if e.Segment != "C" {
		t.Fatalf("failing segment = %q, want C", e.Segment)
	}
}

// 四航段九旅客的全有或全无。
func TestFourLegsNinePaxAllOrNothing(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S1", 9, 0, [NumClasses]int{9, 9, 9}, 0)
	addSeg(t, s, "S2", 9, 0, [NumClasses]int{9, 9, 9}, 0)
	addSeg(t, s, "S3", 9, 0, [NumClasses]int{8, 8, 8}, 0) // 只差 1 个座位
	addSeg(t, s, "S4", 9, 0, [NumClasses]int{9, 9, 9}, 0)
	itin := []Leg{
		{Segment: "S1", Class: 0},
		{Segment: "S2", Class: 0},
		{Segment: "S3", Class: 0},
		{Segment: "S4", Class: 0},
	}
	_, _, err := s.Hold(itin, 9, 0)
	e := assertKind(t, err, KindInsufficient)
	if e.Segment != "S3" {
		t.Fatalf("failing segment = %q, want S3", e.Segment)
	}
	// 全无：其它航段没有任何占用。
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		want := 9
		if id == "S3" {
			want = 8
		}
		if got := avail(t, s, id, 0); got != want {
			t.Fatalf("avail %s = %d, want %d (all-or-nothing)", id, got, want)
		}
	}
	// 补足 S3 授权量后，同一行程全有。
	if err := s.AdjustAuth("S3", 0, 9, 1); err != nil {
		t.Fatalf("AdjustAuth: %v", err)
	}
	if err := s.AdjustAuth("S3", 1, 9, 1); err != nil {
		t.Fatalf("AdjustAuth: %v", err)
	}
	if err := s.AdjustAuth("S3", 2, 9, 1); err != nil {
		t.Fatalf("AdjustAuth: %v", err)
	}
	hid, _ := mustHold(t, s, itin, 9, 2)
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		if got := avail(t, s, id, 0); got != 0 {
			t.Fatalf("avail %s = %d, want 0", id, got)
		}
	}
	// 取消后整条行程的占用立即释放。
	if err := s.Cancel(hid, 3); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	for _, id := range []string{"S1", "S2", "S3", "S4"} {
		if got := avail(t, s, id, 0); got != 9 {
			t.Fatalf("avail %s = %d, want 9 after cancel", id, got)
		}
	}
}

// 拒绝次序：相邻类别逐对验证。能在单个操作中共现的用真实操作验证；因语
// 义互斥无法共现的（不存在 vs 已过期、已过期 vs 状态不符、状态不符 vs 库
// 存不足）验证类别序号即规定次序，并验证可区分性。
func TestRejectionPrecedence(t *testing.T) {
	// 类别声明顺序即规定拒绝次序。
	kinds := []RejKind{KindInvalidParam, KindClockRollback, KindNotFound,
		KindHoldExpired, KindStateConflict, KindInsufficient}
	for i := 1; i < len(kinds); i++ {
		if kinds[i] <= kinds[i-1] {
			t.Fatalf("rejection kinds not in precedence order")
		}
	}

	s := newSystem(t, 10)
	addSeg(t, s, "S", 2, 0, [NumClasses]int{2, 2, 2}, 5) // 时钟推进到 5

	// 参数非法 > 时钟回退：旅客人数非法且时刻回退，报参数非法。
	assertKind(t, func() error {
		_, _, err := s.Hold([]Leg{{Segment: "S", Class: 0}}, 10, 3)
		return err
	}(), KindInvalidParam)

	// 时钟回退 > 不存在：条目不存在且时刻回退，报时钟回退。
	assertKind(t, s.Ticket("E000777", 3), KindClockRollback)

	// 不存在 vs 预占已过期：二者可区分（互斥，无法共现于同一操作）。
	h, _ := mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 1, 5) // expiry 15
	assertKind(t, s.Cancel(h, 15), KindHoldExpired)
	assertKind(t, s.Cancel("E000888", 15), KindNotFound)

	// 时钟回退 > 预占已过期（非相邻，顺带验证）。
	assertKind(t, s.Ticket(h, 4), KindClockRollback)

	// 预占已过期 vs 状态不符互斥：已过期要求状态为预占，状态不符要求非预占。
	h2, _ := mustHold(t, s, []Leg{{Segment: "S", Class: 1}}, 1, 15) // expiry 25
	if err := s.Ticket(h2, 16); err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	// 状态不符 > 库存不足：状态不符只作用于出票/取消，库存不足只作用于预
	// 占，无法共现；分别验证可区分性与序号次序（见上）。
	e := assertKind(t, s.Ticket(h2, 17), KindStateConflict)
	if e.Status != StatusTicketed {
		t.Fatalf("conflict status = %v, want ticketed", e.Status)
	}
	if err := s.Cancel(h2, 18); err != nil {
		t.Fatalf("Cancel ticketed: %v", err)
	}
	// 库存不足单独可报：把航段占满（物理 2，等级 0 占 2）。
	mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 2, 18)
	assertKind(t, func() error {
		_, _, err := s.Hold([]Leg{{Segment: "S", Class: 0}}, 1, 19)
		return err
	}(), KindInsufficient)
	// 参数非法 > 不存在（非相邻，顺带验证）：人数非法且航段不存在。
	assertKind(t, func() error {
		_, _, err := s.Hold([]Leg{{Segment: "NOPE", Class: 0}}, 0, 19)
		return err
	}(), KindInvalidParam)
	// 不存在 > 库存不足（非相邻，顺带验证）：航段缺失与人数合法。
	assertKind(t, func() error {
		_, _, err := s.Hold([]Leg{{Segment: "NOPE", Class: 0}}, 1, 19)
		return err
	}(), KindNotFound)
}

// 状态不符可区分已取消与已出票；重复出票/取消均报状态不符。
func TestStateConflictDistinguishable(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S", 2, 0, [NumClasses]int{2, 2, 2}, 0)
	h, _ := mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 1, 0)
	if err := s.Ticket(h, 1); err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	e := assertKind(t, s.Ticket(h, 2), KindStateConflict)
	if e.Status != StatusTicketed {
		t.Fatalf("re-ticket conflict status = %v, want ticketed", e.Status)
	}
	if err := s.Cancel(h, 3); err != nil {
		t.Fatalf("Cancel ticketed: %v", err)
	}
	e = assertKind(t, s.Cancel(h, 4), KindStateConflict)
	if e.Status != StatusCancelled {
		t.Fatalf("re-cancel conflict status = %v, want cancelled", e.Status)
	}
	e = assertKind(t, s.Ticket(h, 5), KindStateConflict)
	if e.Status != StatusCancelled {
		t.Fatalf("ticket-after-cancel conflict status = %v, want cancelled", e.Status)
	}
	// 预占直接取消后同样可区分。
	h2, _ := mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 1, 5)
	if err := s.Cancel(h2, 6); err != nil {
		t.Fatalf("Cancel hold: %v", err)
	}
	e = assertKind(t, s.Ticket(h2, 7), KindStateConflict)
	if e.Status != StatusCancelled {
		t.Fatalf("conflict status = %v, want cancelled", e.Status)
	}
}

// 被拒绝的操作不推进时钟。
func TestRejectedOpsDoNotAdvanceClock(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S", 1, 0, [NumClasses]int{1, 1, 1}, 5)
	h, _ := mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 1, 5)
	// 库存不足的拒绝发生在时刻 8，不应推进时钟。
	assertKind(t, func() error {
		_, _, err := s.Hold([]Leg{{Segment: "S", Class: 0}}, 1, 8)
		return err
	}(), KindInsufficient)
	if got := s.Now(); got != 5 {
		t.Fatalf("clock = %d, want 5", got)
	}
	// 时刻 6（介于 5 与 8 之间）的操作仍被接受，证明时钟未被拒绝操作推进。
	if err := s.Cancel(h, 6); err != nil {
		t.Fatalf("Cancel at 6: %v", err)
	}
	if got := s.Now(); got != 6 {
		t.Fatalf("clock = %d, want 6", got)
	}
}

// 参数非法的预占：空行程、超四段、人数越界、航段重复、等级越界。
func TestInvalidHoldParams(t *testing.T) {
	s := newSystem(t, 100)
	addSeg(t, s, "S", 9, 0, [NumClasses]int{9, 9, 9}, 0)
	cases := []struct {
		name string
		legs []Leg
		pax  int
	}{
		{"empty itinerary", nil, 1},
		{"five legs", []Leg{{"S", 0}, {"S", 0}, {"S", 0}, {"S", 0}, {"S", 0}}, 1},
		{"zero pax", []Leg{{"S", 0}}, 0},
		{"ten pax", []Leg{{"S", 0}}, 10},
		{"duplicate segment", []Leg{{"S", 0}, {"S", 1}}, 1},
		{"class out of range", []Leg{{"S", 3}}, 1},
		{"empty segment id", []Leg{{"", 0}}, 1},
	}
	for i, tc := range cases {
		_, _, err := s.Hold(tc.legs, tc.pax, int64(i+1))
		assertKind(t, err, KindInvalidParam)
	}
}

// 导出全部状态并在新实例中恢复：恢复前后对同一组查询的结果逐项相等，
// 未过期预占保留原到期时刻，条目标识计数器延续。
func TestExportRestore(t *testing.T) {
	s := newSystem(t, 10)
	addSeg(t, s, "S1", 5, 1, [NumClasses]int{6, 4, 2}, 0)
	addSeg(t, s, "S2", 5, 0, [NumClasses]int{5, 4, 3}, 0)
	itin := []Leg{{Segment: "S1", Class: 0}, {Segment: "S2", Class: 2}}
	hExpired, _ := mustHold(t, s, itin, 1, 0) // expiry 10，将过期且不被触及
	hActive, actExpiry := mustHold(t, s, itin, 2, 5)
	hTicket, _ := mustHold(t, s, []Leg{{Segment: "S1", Class: 1}}, 1, 6)
	if err := s.Ticket(hTicket, 7); err != nil {
		t.Fatalf("Ticket: %v", err)
	}
	hCancel, _ := mustHold(t, s, []Leg{{Segment: "S2", Class: 0}}, 1, 8)
	if err := s.Cancel(hCancel, 9); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if err := s.AdjustAuth("S1", 2, 1, 12); err != nil { // 推进时钟到 12，hExpired 过期
		t.Fatalf("AdjustAuth: %v", err)
	}

	snap := s.Export()
	// 快照可 JSON 序列化且确定性：两次导出逐字节一致。
	buf1, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	buf2, _ := json.Marshal(s.Export())
	if string(buf1) != string(buf2) {
		t.Fatalf("export not deterministic")
	}

	r, err := Restore(snap)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	// 恢复前后对同一组查询逐项相等。
	for _, segID := range []string{"S1", "S2"} {
		for class := 0; class < NumClasses; class++ {
			a1, err1 := s.Availability(segID, class)
			a2, err2 := r.Availability(segID, class)
			if err1 != nil || err2 != nil || a1 != a2 {
				t.Fatalf("Availability(%s,%d): (%d,%v) vs (%d,%v)", segID, class, a1, err1, a2, err2)
			}
		}
	}
	for _, pax := range []int{1, 2, 3} {
		ok1, seg1, _ := s.CanHold(itin, pax)
		ok2, seg2, _ := r.CanHold(itin, pax)
		if ok1 != ok2 || seg1 != seg2 {
			t.Fatalf("CanHold(%d): (%v,%s) vs (%v,%s)", pax, ok1, seg1, ok2, seg2)
		}
	}
	// 未过期预占保留原到期时刻。
	e, err := r.Lookup(hActive)
	if err != nil || e.Expiry != actExpiry || e.Status != StatusHold {
		t.Fatalf("restored entry = %+v, %v; want expiry %d", e, err, actExpiry)
	}
	if _, err := r.Lookup(hExpired); err != nil {
		t.Fatalf("Lookup expired hold: %v", err)
	}
	// 对已过期预占的出票在恢复后仍报预占已过期。
	assertKind(t, r.Ticket(hExpired, 12), KindHoldExpired)
	// 条目标识计数器延续：恢复后的新预占不与既有标识冲突，且与原实例一致。
	idOrig, expOrig, err := s.Hold(itin, 1, 13)
	if err != nil {
		t.Fatalf("Hold on original: %v", err)
	}
	idRest, expRest, err := r.Hold(itin, 1, 13)
	if err != nil {
		t.Fatalf("Hold on restored: %v", err)
	}
	if idOrig != idRest || expOrig != expRest {
		t.Fatalf("restored hold = (%s,%d), original = (%s,%d)", idRest, expRest, idOrig, expOrig)
	}
}

// 相同操作序列重放得到完全相同的结果、条目标识与到期时刻。
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		s := newSystem(t, 7)
		var out []string
		record := func(format string, args ...interface{}) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		for _, err := range []error{
			s.AddSegment("A", 4, 1, [NumClasses]int{5, 3, 2}, 0),
			s.AddSegment("B", 3, 0, [NumClasses]int{3, 2, 1}, 1),
		} {
			record("add: %v", err)
		}
		itin := []Leg{{Segment: "A", Class: 0}, {Segment: "B", Class: 2}}
		for i := int64(2); i < 12; i++ {
			id, expiry, err := s.Hold(itin, int(i%3)+1, i)
			record("hold: id=%s expiry=%d err=%v", id, expiry, err)
			if err == nil {
				if i%2 == 0 {
					record("ticket: %v", s.Ticket(id, i))
				} else {
					record("cancel: %v", s.Cancel(id, i))
				}
			}
			record("adjust: %v", s.AdjustAuth("A", 2, int(i%3), i))
			a, _ := s.Availability("A", 0)
			record("avail=%d now=%d", a, s.Now())
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\n%v\nvs\n%v", first, second)
	}
}

// 并发调用等价于某个串行顺序：不变式（航段总占用不超过最高等级授权量）
// 在并发结束后仍成立，且占用计数不为负。配合 go test -race 验证。
func TestConcurrency(t *testing.T) {
	s := newSystem(t, 50)
	for _, id := range []string{"A", "B", "C"} {
		addSeg(t, s, id, 20, 0, [NumClasses]int{20, 20, 20}, 0)
	}
	segIDs := []string{"A", "B", "C"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			var mine []string
			for i := 0; i < 200; i++ {
				seg := segIDs[(g+i)%len(segIDs)]
				now := int64(i / 8) // 各协程时刻交错，部分因回退被拒绝
				switch i % 4 {
				case 0, 1:
					id, _, err := s.Hold([]Leg{{Segment: seg, Class: (g + i) % NumClasses}}, 1, now)
					if err == nil {
						mine = append(mine, id)
					}
				case 2:
					if len(mine) > 0 {
						s.Ticket(mine[0], now)
						mine = mine[1:]
					}
				case 3:
					if len(mine) > 0 {
						s.Cancel(mine[0], now)
						mine = mine[1:]
					}
					s.Availability(seg, 0)
					s.CanHold([]Leg{{Segment: seg, Class: 0}}, 1)
				}
			}
		}(g)
	}
	wg.Wait()
	snap := s.Export()
	for _, ss := range snap.Segments {
		a, err := s.Availability(ss.ID, 0)
		if err != nil {
			t.Fatalf("Availability: %v", err)
		}
		if a < 0 {
			t.Fatalf("segment %s total occupancy exceeds top authorization", ss.ID)
		}
		if a > ss.Auth[0] {
			t.Fatalf("segment %s availability %d exceeds authorization %d", ss.ID, a, ss.Auth[0])
		}
	}
}

// 性能证明：查询与预占的开销不随累计预占/出票数量增长，也不随已过期但
// 从未被触及的预占数量增长。每条过期队列记录至多被清扫一次（摊还 O(1)），
// 用清扫计数器精确验证，避免计时不稳定。
func TestSweepAmortizedConstant(t *testing.T) {
	const rounds = 50
	const perRound = 100
	s := newSystem(t, 1)
	addSeg(t, s, "S", perRound, 0, [NumClasses]int{perRound, perRound, perRound}, 0)
	// 每轮创建 perRound 个预占，下一轮到期；累计 rounds*perRound 个过期预占。
	for r := int64(1); r <= rounds; r++ {
		for i := 0; i < perRound; i++ {
			if _, _, err := s.Hold([]Leg{{Segment: "S", Class: 0}}, 1, r); err != nil {
				t.Fatalf("Hold round %d: %v", r, err)
			}
		}
	}
	// 所有预占（到期时刻 r+1 <= rounds+1）在最后一次预占的清扫后全部过期。
	// 关键断言：每条过期记录恰好被清扫一次，总清扫量 == 过期记录数，
	// 与查询次数无关。
	if got := s.swept; got > rounds*perRound {
		t.Fatalf("swept = %d, want <= %d (each expired entry swept at most once)", got, rounds*perRound)
	}
	before := s.swept
	for i := 0; i < 1000; i++ {
		if _, err := s.Availability("S", 0); err != nil {
			t.Fatalf("Availability: %v", err)
		}
		ok, _, err := s.CanHold([]Leg{{Segment: "S", Class: 0}}, 1)
		if err != nil || ok {
			t.Fatalf("CanHold: %v %v", ok, err)
		}
	}
	// 1000 次查询不再触发任何清扫：查询开销与过期记录数无关。
	if got := s.swept; got != before {
		t.Fatalf("queries swept %d entries, want 0", got-before)
	}
	// 最后一轮预占（到期 rounds+1）尚未过期，可用数为 0。
	if got := avail(t, s, "S", 0); got != 0 {
		t.Fatalf("avail = %d, want 0 (last round still active)", got)
	}
	// 时钟推进到全部过期后，无需逐条触及，可用数自动恢复。
	mustHold(t, s, []Leg{{Segment: "S", Class: 0}}, 1, rounds+1)
	if got := avail(t, s, "S", 0); got != perRound-1 {
		t.Fatalf("avail after expiry = %d, want %d", got, perRound-1)
	}
	// 全程总清扫量恰好等于过期记录数：每条记录只被处理一次。
	if got := s.swept; got != rounds*perRound {
		t.Fatalf("total swept = %d, want %d", got, rounds*perRound)
	}
}
