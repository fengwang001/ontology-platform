package inventory

import (
	"fmt"
	"sync"
	"testing"
)

func mustAddSegment(t *testing.T, s *System, id string, seats, overbook int, au [3]int) {
	t.Helper()
	if err := s.AddSegment(id, seats, overbook, au); err != nil {
		t.Fatalf("AddSegment(%q): %v", id, err)
	}
}

func mustHold(t *testing.T, s *System, legs []Leg, party int, now int64) (string, int64) {
	t.Helper()
	id, expiry, err := s.Hold(legs, party, now)
	if err != nil {
		t.Fatalf("Hold(%v, %d, %d): %v", legs, party, now, err)
	}
	return id, expiry
}

func mustAvail(t *testing.T, s *System, seg string, c Class, now int64) int {
	t.Helper()
	a, err := s.Availability(seg, c, now)
	if err != nil {
		t.Fatalf("Availability(%q, %v, %d): %v", seg, c, now, err)
	}
	return a
}

func wantErrKind(t *testing.T, err error, kind ErrKind) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("want error kind %v, got nil", kind)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("want kind %v, got %v: %v", kind, e.Kind, err)
	}
	return e
}

// 当前时刻恰等于到期时刻（算过期）与小一秒（未过期）的差异。
func TestExpiryBoundaryExactVsOneSecondBefore(t *testing.T) {
	s, err := NewSystem(100)
	if err != nil {
		t.Fatal(err)
	}
	mustAddSegment(t, s, "S1", 5, 0, [3]int{5, 5, 5})

	// 三条同样的预占，分别用于查询、出票、取消的边界判定。
	_, expiryQ, err := s.Hold([]Leg{{"S1", ClassHigh}}, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	idT, _, err := s.Hold([]Leg{{"S1", ClassHigh}}, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	idC, _, err := s.Hold([]Leg{{"S1", ClassHigh}}, 1, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if expiryQ != 1100 {
		t.Fatalf("expiry = %d, want 1100", expiryQ)
	}

	// 小一秒：三条预占仍计入占用。
	if got := mustAvail(t, s, "S1", ClassHigh, 1099); got != 2 {
		t.Fatalf("avail@1099 = %d, want 2", got)
	}
	// 恰等于到期时刻：已过期，占用释放（无论是否有操作触及）。
	if got := mustAvail(t, s, "S1", ClassHigh, 1100); got != 5 {
		t.Fatalf("avail@1100 = %d, want 5", got)
	}

	// 恰等于到期时刻出票 / 取消：报预占已过期，与不存在可区分。
	wantErrKind(t, s.Ticket(idT, 1100), ErrHoldExpired)
	wantErrKind(t, s.Cancel(idC, 1100), ErrHoldExpired)
	wantErrKind(t, s.Ticket("H999999", 1100), ErrNotFound)

	// 新系统验证“小一秒”可以出票，且出票后永不过期。
	s2, _ := NewSystem(100)
	mustAddSegment(t, s2, "S1", 5, 0, [3]int{5, 5, 5})
	id2, _ := mustHold(t, s2, []Leg{{"S1", ClassHigh}}, 2, 1000)
	if err := s2.Ticket(id2, 1099); err != nil {
		t.Fatalf("ticket at expiry-1: %v", err)
	}
	if got := mustAvail(t, s2, "S1", ClassHigh, 1<<40); got != 3 {
		t.Fatalf("confirmed occupancy must never expire, avail = %d, want 3", got)
	}
	// 已确认票可取消，取消后占用立即释放。
	if err := s2.Cancel(id2, 1<<40); err != nil {
		t.Fatalf("cancel ticketed entry: %v", err)
	}
	if got := mustAvail(t, s2, "S1", ClassHigh, 1<<40); got != 5 {
		t.Fatalf("avail after cancel = %d, want 5", got)
	}
}

// 嵌套等级中高等级可用数为负而低等级可用数为正时的可售判定；
// 以及授权量下调到低于已占用后再上调恢复。
func TestNestedNegativeHighAvailability(t *testing.T) {
	s, _ := NewSystem(1000)
	mustAddSegment(t, s, "S", 10, 0, [3]int{10, 10, 10})

	// 高舱占 9：occ_high=9, occ_mid=occ_low=0。
	mustHold(t, s, []Leg{{"S", ClassHigh}}, 9, 0)

	// 非法调整：破坏嵌套次序 / 超过物理座位+超售上限。
	wantErrKind(t, s.AdjustAuthorization("S", ClassMid, 11, 1), ErrInvalidParam)  // 中 > 高
	wantErrKind(t, s.AdjustAuthorization("S", ClassHigh, 11, 1), ErrInvalidParam) // 高 > 10
	wantErrKind(t, s.AdjustAuthorization("S", ClassLow, -1, 1), ErrInvalidParam)

	// 按低->中->高顺序下调到 5（每次调整都保持嵌套合法）。
	for i, c := range []Class{ClassLow, ClassMid, ClassHigh} {
		if err := s.AdjustAuthorization("S", c, 5, int64(1+i)); err != nil {
			t.Fatalf("adjust %v: %v", c, err)
		}
	}
	// 高：5-9=-4（负）；中：5-0=5；低：5-0=5（正）。
	if got := mustAvail(t, s, "S", ClassHigh, 3); got != -4 {
		t.Fatalf("avail high = %d, want -4", got)
	}
	if got := mustAvail(t, s, "S", ClassMid, 3); got != 5 {
		t.Fatalf("avail mid = %d, want 5", got)
	}
	if got := mustAvail(t, s, "S", ClassLow, 3); got != 5 {
		t.Fatalf("avail low = %d, want 5", got)
	}
	// 高等级可用数为负 ⇒ 中、低等级均不可售，即使它们自身可用数为正。
	for _, c := range []Class{ClassHigh, ClassMid, ClassLow} {
		ok, seg, err := s.ItinerarySellable([]Leg{{"S", c}}, 1, 3)
		if err != nil {
			t.Fatal(err)
		}
		if ok || seg != "S" {
			t.Fatalf("class %v should not be sellable, got ok=%v seg=%q", c, ok, seg)
		}
	}
	if _, _, err := s.Hold([]Leg{{"S", ClassLow}}, 1, 4); err == nil {
		t.Fatal("hold on low class should fail while high availability is negative")
	} else {
		wantErrKind(t, err, ErrInsufficientInventory)
	}

	// 再上调恢复：高->中->高顺序调回 10。
	for i, c := range []Class{ClassHigh, ClassMid, ClassLow} {
		if err := s.AdjustAuthorization("S", c, 10, int64(5+i)); err != nil {
			t.Fatalf("restore %v: %v", c, err)
		}
	}
	ok, _, err := s.ItinerarySellable([]Leg{{"S", ClassLow}}, 1, 7)
	if err != nil || !ok {
		t.Fatalf("low class should be sellable again: ok=%v err=%v", ok, err)
	}
	// 已有占用不受影响：高舱 9 名占用仍在。
	if got := mustAvail(t, s, "S", ClassHigh, 7); got != 1 {
		t.Fatalf("avail high after restore = %d, want 1", got)
	}
}

// 行程内第一个不满足航段的报错定位。
func TestFirstFailingSegmentReported(t *testing.T) {
	s, _ := NewSystem(100)
	mustAddSegment(t, s, "A", 5, 0, [3]int{5, 5, 5})
	mustAddSegment(t, s, "B", 2, 0, [3]int{2, 2, 2})
	mustAddSegment(t, s, "C", 1, 0, [3]int{1, 1, 1})

	// 占满 B。
	mustHold(t, s, []Leg{{"B", ClassHigh}}, 2, 0)

	// A 满足、B 不满足、C 也不满足：报第一个不满足的 B。
	legs := []Leg{{"A", ClassHigh}, {"B", ClassHigh}, {"C", ClassHigh}}
	_, _, err := s.Hold(legs, 2, 1)
	e := wantErrKind(t, err, ErrInsufficientInventory)
	if e.Segment != "B" {
		t.Fatalf("first failing segment = %q, want B", e.Segment)
	}
	ok, seg, err := s.ItinerarySellable(legs, 2, 1)
	if err != nil || ok || seg != "B" {
		t.Fatalf("ItinerarySellable = (%v, %q, %v), want (false, B, nil)", ok, seg, err)
	}

	// 被拒绝的预占不改动任何状态。
	if got := mustAvail(t, s, "A", ClassHigh, 1); got != 5 {
		t.Fatalf("A avail = %d, want 5 (rejected hold must not change state)", got)
	}
	if got := mustAvail(t, s, "C", ClassHigh, 1); got != 1 {
		t.Fatalf("C avail = %d, want 1", got)
	}
	if st := s.Stats(); st.Entries != 1 {
		t.Fatalf("entries = %d, want 1", st.Entries)
	}
}

// 四航段九旅客的全有或全无。
func TestAllOrNothingFourLegsNinePassengers(t *testing.T) {
	s, _ := NewSystem(100)
	for _, id := range []string{"L1", "L2", "L3", "L4"} {
		mustAddSegment(t, s, id, 9, 0, [3]int{9, 9, 9})
	}
	legs := []Leg{{"L1", ClassLow}, {"L2", ClassMid}, {"L3", ClassHigh}, {"L4", ClassLow}}

	// L3 先被占 1，剩 8 < 9。
	mustHold(t, s, []Leg{{"L3", ClassHigh}}, 1, 0)

	_, _, err := s.Hold(legs, 9, 5)
	e := wantErrKind(t, err, ErrInsufficientInventory)
	if e.Segment != "L3" {
		t.Fatalf("failing segment = %q, want L3", e.Segment)
	}
	// 全无：其它航段占用不变，条目数不变，时钟不推进。
	for _, id := range []string{"L1", "L2", "L4"} {
		if got := mustAvail(t, s, id, ClassHigh, 5); got != 9 {
			t.Fatalf("%s avail = %d, want 9", id, got)
		}
	}
	if st := s.Stats(); st.Entries != 1 {
		t.Fatalf("entries = %d, want 1", st.Entries)
	}
	if got := s.Clock(); got != 0 {
		t.Fatalf("clock = %d, want 0 (rejected op must not advance clock)", got)
	}

	// 释放 L3 后，四航段九旅客全有。
	if err := s.Cancel("H000001", 6); err != nil {
		t.Fatal(err)
	}
	id, expiry, err := s.Hold(legs, 9, 7)
	if err != nil {
		t.Fatal(err)
	}
	if id != "H000002" || expiry != 107 {
		t.Fatalf("id=%q expiry=%d, want H000002/107", id, expiry)
	}
	for _, l := range legs {
		if got := mustAvail(t, s, l.Segment, ClassHigh, 7); got != 0 {
			t.Fatalf("%s avail = %d, want 0", l.Segment, got)
		}
	}
}

// 拒绝次序的每一对相邻类别：
// 参数非法 > 时钟回退 > 不存在 > 预占已过期 > 状态不符 > 库存不足。
func TestRejectionPrecedenceAdjacentPairs(t *testing.T) {
	s, _ := NewSystem(100)
	mustAddSegment(t, s, "S", 5, 0, [3]int{5, 5, 5})

	// 时钟推进到 10；h1 在 110 到期。
	h1, _, err := s.Hold([]Leg{{"S", ClassHigh}}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}

	// 参数非法 > 时钟回退：人数 0 且时刻回退，报参数非法。
	_, _, err = s.Hold([]Leg{{"S", ClassHigh}}, 0, 5)
	wantErrKind(t, err, ErrInvalidParam)
	// 同一行程同一航段出现两次：参数非法。
	dupLegs := []Leg{{"S", ClassHigh}, {"S", ClassLow}}
	_, _, err = s.Hold(dupLegs, 1, 5)
	wantErrKind(t, err, ErrInvalidParam)
	// 负时刻：参数非法。
	_, _, err = s.Hold([]Leg{{"S", ClassHigh}}, 1, -1)
	wantErrKind(t, err, ErrInvalidParam)

	// 时钟回退 > 不存在：条目不存在且时刻回退，报时钟回退。
	wantErrKind(t, s.Ticket("H999999", 5), ErrClockRegression)
	wantErrKind(t, s.Cancel("H999999", 5), ErrClockRegression)

	// 推进时钟到 200，h1（110 到期）已过期。
	h2, _, err := s.Hold([]Leg{{"S", ClassMid}}, 1, 200)
	if err != nil {
		t.Fatal(err)
	}

	// 不存在 > 预占已过期：系统内存在已过期预占 h1，
	// 但对不存在条目的操作仍报不存在。
	wantErrKind(t, s.Ticket("H999999", 200), ErrNotFound)
	wantErrKind(t, s.Cancel("H999999", 200), ErrNotFound)

	// 预占已过期 > 状态不符：h1 状态仍是预占中，
	// 出票/取消报已过期而非按状态处理。
	wantErrKind(t, s.Ticket(h1, 200), ErrHoldExpired)
	wantErrKind(t, s.Cancel(h1, 200), ErrHoldExpired)

	// 状态不符：已出票与已取消可区分。
	if err := s.Ticket(h2, 201); err != nil {
		t.Fatal(err)
	}
	e := wantErrKind(t, s.Ticket(h2, 202), ErrInvalidState)
	if e.State != StateTicketed {
		t.Fatalf("state = %v, want ticketed", e.State)
	}
	if err := s.Cancel(h2, 203); err != nil {
		t.Fatal(err)
	}
	e = wantErrKind(t, s.Cancel(h2, 204), ErrInvalidState)
	if e.State != StateCancelled {
		t.Fatalf("state = %v, want cancelled", e.State)
	}
	e = wantErrKind(t, s.Ticket(h2, 205), ErrInvalidState)
	if e.State != StateCancelled {
		t.Fatalf("state = %v, want cancelled", e.State)
	}

	// 状态不符 > 库存不足：两类别不会同时出现在同一操作上，
	// 由 ErrKind 声明顺序保证次序（状态不符排在库存不足之前）。
	if ErrInvalidState >= ErrInsufficientInventory {
		t.Fatal("ErrKind declaration order must encode rejection precedence")
	}

	// 所有被拒绝的操作均未推进时钟、未改动占用。
	if got := s.Clock(); got != 203 {
		t.Fatalf("clock = %d, want 203 (only accepted ops advance it)", got)
	}
	if got := mustAvail(t, s, "S", ClassHigh, 205); got != 5 {
		t.Fatalf("avail high = %d, want 5 (h1 expired, h2 cancelled)", got)
	}
}

// 相同操作序列重放得到完全相同的结果、条目标识与到期时刻。
func TestDeterministicReplay(t *testing.T) {
	run := func() []string {
		s, _ := NewSystem(50)
		mustAddSegment(t, s, "A", 4, 1, [3]int{5, 4, 3})
		mustAddSegment(t, s, "B", 3, 0, [3]int{3, 3, 3})
		var out []string
		for i := int64(0); i < 20; i++ {
			legs := []Leg{{"A", Class(i % 3)}, {"B", Class((i + 1) % 3)}}
			id, expiry, err := s.Hold(legs, int(i%3)+1, i*10)
			if err != nil {
				out = append(out, "hold-err:"+err.(*Error).Kind.String())
				continue
			}
			out = append(out, fmt.Sprintf("hold:%s:%d", id, expiry))
			switch i % 4 {
			case 1:
				if err := s.Ticket(id, i*10+1); err != nil {
					out = append(out, "ticket-err")
				}
			case 2:
				if err := s.Cancel(id, i*10+1); err != nil {
					out = append(out, "cancel-err")
				}
			}
			a, _ := s.Availability("A", ClassHigh, i*10+2)
			out = append(out, fmt.Sprintf("avail:%d", a))
		}
		return out
	}
	first, second := run(), run()
	if len(first) != len(second) {
		t.Fatalf("replay length mismatch: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d: %q vs %q", i, first[i], second[i])
		}
	}
}

// 导出全部游走在新实例中恢复，恢复前后对同一组查询的结果逐项相等；
// 未过期预占保留原到期时刻，恢复后续操作的条目标识保持连续。
func TestExportRestore(t *testing.T) {
	s, _ := NewSystem(50)
	mustAddSegment(t, s, "A", 5, 1, [3]int{6, 4, 2})
	mustAddSegment(t, s, "B", 3, 0, [3]int{3, 3, 3})

	h1, _, _ := s.Hold([]Leg{{"A", ClassHigh}, {"B", ClassLow}}, 2, 0) // 出票
	h3, _, _ := s.Hold([]Leg{{"B", ClassHigh}}, 1, 0)                  // 取消
	h4, _, _ := s.Hold([]Leg{{"A", ClassLow}}, 1, 0)                   // 过期不触及（50 到期）
	if err := s.Ticket(h1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(h3, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.AdjustAuthorization("A", ClassMid, 5, 3); err != nil {
		t.Fatal(err)
	}
	h2, _, _ := s.Hold([]Leg{{"A", ClassMid}}, 1, 55) // 105 到期，恢复时未过期
	// 推进时钟到 60：h4 已过期且从未被触及。
	if err := s.AdjustAuthorization("B", ClassLow, 3, 60); err != nil {
		t.Fatal(err)
	}

	data, err := s.ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	r, err := RestoreJSON(data)
	if err != nil {
		t.Fatal(err)
	}

	// 恢复前后对同一组查询逐项相等。
	for _, now := range []int64{60, 61, 100, 1000} {
		for _, seg := range []string{"A", "B"} {
			for c := ClassHigh; c <= ClassLow; c++ {
				a1, err1 := s.Availability(seg, c, now)
				a2, err2 := r.Availability(seg, c, now)
				if (err1 == nil) != (err2 == nil) || a1 != a2 {
					t.Fatalf("avail mismatch %s/%v@%d: (%d,%v) vs (%d,%v)", seg, c, now, a1, err1, a2, err2)
				}
			}
			c1, h1x, _ := s.Load(seg, now)
			c2, h2x, _ := r.Load(seg, now)
			if c1 != c2 || h1x != h2x {
				t.Fatalf("load mismatch %s@%d: (%d,%d) vs (%d,%d)", seg, now, c1, h1x, c2, h2x)
			}
		}
	}
	// 过期判定一致：h4 在两个实例中都报已过期；h2 未过期可出票。
	wantErrKind(t, r.Ticket(h4, 60), ErrHoldExpired)
	if err := r.Ticket(h2, 60); err != nil {
		t.Fatalf("unexpired hold must keep original expiry after restore: %v", err)
	}
	// 条目标识连续：恢复后新预占的 ID 不与已有冲突。
	id, _, err := r.Hold([]Leg{{"B", ClassLow}}, 1, 61)
	if err != nil {
		t.Fatal(err)
	}
	if id != "H000005" {
		t.Fatalf("next id after restore = %q, want H000005", id)
	}
}

// 并发调用：结果等价于某个串行顺序；不变量
// 已确认+未到期预占 <= 最高等级授权量 始终成立。
func TestConcurrentEquivalence(t *testing.T) {
	s, _ := NewSystem(100000)
	mustAddSegment(t, s, "S", 120, 0, [3]int{120, 120, 120})

	const workers = 8
	const perWorker = 200
	var mu sync.Mutex
	accepted := make(map[string]int) // id -> party
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				party := (i % 9) + 1
				class := Class((w + i) % 3)
				id, _, err := s.Hold([]Leg{{"S", class}}, party, 0)
				if err != nil {
					if e, ok := err.(*Error); !ok || e.Kind != ErrInsufficientInventory {
						t.Errorf("unexpected error: %v", err)
					}
					continue
				}
				mu.Lock()
				accepted[id] = party
				mu.Unlock()
				switch i % 3 {
				case 0:
					_ = s.Ticket(id, 0)
				case 1:
					_ = s.Cancel(id, 0)
				}
			}
		}(w)
	}
	wg.Wait()

	// 串行一致性：每个被接受的预占都有唯一条目，占用合计与接受数一致。
	confirmed, held, err := s.Load("S", 0)
	if err != nil {
		t.Fatal(err)
	}
	totalParty := 0
	for _, p := range accepted {
		totalParty += p
	}
	if confirmed+held > totalParty {
		t.Fatalf("occupancy %d exceeds accepted parties %d", confirmed+held, totalParty)
	}
	if confirmed+held > 120 {
		t.Fatalf("occupancy %d exceeds top authorization 120", confirmed+held)
	}
	if st := s.Stats(); st.Entries != len(accepted) {
		t.Fatalf("entries = %d, want %d", st.Entries, len(accepted))
	}
}
