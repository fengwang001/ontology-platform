package transfer

import (
	"errors"
	"sync"
	"testing"

	"ontology/ward"
)

func newSvc(t *testing.T, cl, hold int) (*ward.Hospital, *Service) {
	t.Helper()
	h, err := ward.New(cl, hold)
	if err != nil {
		t.Fatal(err)
	}
	return h, New(h)
}

func setup(t *testing.T, h *ward.Hospital) {
	t.Helper()
	for _, r := range []struct {
		w, room string
		beds    int
	}{
		{"W", "R1", 2}, {"W", "R2", 3}, {"W", "R3", 2},
		{"V", "R9", 2},
	} {
		if err := h.AddRoom(r.w, r.room, r.beds); err != nil {
			t.Fatal(err)
		}
	}
	h.Grant("op", "W")
	h.Grant("op", "V")
}

func admit(t *testing.T, s *Service, now int64, id string, sex ward.Sex, iso bool, w string) string {
	t.Helper()
	c, err := s.Admit(now, "op", id, sex, iso, w)
	if err != nil {
		t.Fatalf("Admit %s: %v", id, err)
	}
	return c.Room.Name + "-" + nitoa(c.Bed.No)
}

func nitoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func errIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v want %v", got, want)
	}
}

// TestSpecExampleW 复刻题面 W 病区示例。
func TestSpecExampleW(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	if got := admit(t, s, 0, "a", ward.SexFemale, false, "W"); got != "R1-1" {
		t.Fatalf("a = %s", got)
	}
	if got := admit(t, s, 0, "b", ward.SexMale, false, "W"); got != "R3-1" {
		t.Fatalf("b = %s", got)
	}
	if got := admit(t, s, 0, "c", ward.SexFemale, false, "W"); got != "R1-2" {
		t.Fatalf("c = %s", got)
	}
	if got := admit(t, s, 0, "d", ward.SexFemale, false, "W"); got != "R2-1" {
		t.Fatalf("d = %s", got)
	}
	_, err := s.Admit(0, "op", "e", ward.SexMale, true, "W")
	errIs(t, err, ward.ErrNoBed)

	if err := s.Discharge(100, "op", "b"); err != nil {
		t.Fatal(err)
	}
	if s.Touched() != 1 {
		t.Fatalf("Discharge touched=%d want 1", s.Touched())
	}
	_, err = s.Admit(129, "op", "e", ward.SexMale, true, "W")
	errIs(t, err, ward.ErrNoBed)
	if got := admit(t, s, 130, "e", ward.SexMale, true, "W"); got != "R3-1" {
		t.Fatalf("e at 130 = %s want R3-1", got)
	}
	_, err = s.Admit(131, "op", "f", ward.SexMale, false, "W")
	errIs(t, err, ward.ErrNoBed)
	if got := admit(t, s, 131, "g", ward.SexFemale, false, "W"); got != "R2-2" {
		t.Fatalf("g = %s want R2-2", got)
	}

	if err := s.Discharge(140, "op", "e"); err != nil {
		t.Fatal(err)
	}
	if got := admit(t, s, 170, "h", ward.SexMale, false, "W"); got != "R3-1" {
		t.Fatalf("h after iso left = %s want R3-1", got)
	}
}

// TestSpecExampleV 复刻预留、Confirm 两阶段与清洁恰等示例。
func TestSpecExampleV(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 0, "d", ward.SexFemale, false, "W")  // R1-1
	admit(t, s, 0, "x2", ward.SexFemale, false, "W") // R1-2（R1 满，R2 整间保留）

	c, err := s.Request(200, "op", "d", "V")
	if err != nil {
		t.Fatal(err)
	}
	if c.Room.Name+"-"+nitoa(c.Bed.No) != "R9-1" {
		t.Fatalf("reserve = %s-%d", c.Room.Name, c.Bed.No)
	}
	_, err = s.Admit(210, "op", "h", ward.SexMale, false, "V")
	errIs(t, err, ward.ErrNoBed)
	if err := s.Confirm(259, "op", "d"); err != nil {
		t.Fatalf("confirm at 259: %v", err)
	}
	if s.Touched() != 2 {
		t.Fatalf("Confirm touched=%d want 2", s.Touched())
	}
	p := h.Patients["d"]
	if p.Ward != "V" || p.Room != "R9" || p.Transfer != nil {
		t.Fatalf("d after confirm = %+v", p)
	}
	if got := admit(t, s, 289, "i", ward.SexFemale, false, "W"); got != "R1-1" {
		t.Fatalf("i = %s want R1-1 (clean exact; occupied room preferred over empty)", got)
	}
}

// TestReservationExpiry：未确认时预留恰等失效，房间重新可分，Confirm 报状态不符。
func TestReservationExpiry(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 0, "d", ward.SexFemale, false, "W")
	if _, err := s.Request(200, "op", "d", "V"); err != nil {
		t.Fatal(err)
	}
	if got := admit(t, s, 260, "h", ward.SexMale, false, "V"); got != "R9-1" {
		t.Fatalf("h = %s want R9-1", got)
	}
	errIs(t, s.Confirm(260, "op", "d"), ward.ErrState)
	p := h.Patients["d"]
	if p.Ward != "W" || p.Room != "R1" || p.Bed != 1 {
		t.Fatalf("d moved unexpectedly: %+v", p)
	}
	// h 占用 R9-1，但 R9-2 空闲：女 d 仍可转入（同病区规则下 V 无其他女患者）。
	if err := s.Discharge(261, "op", "h"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Request(262, "op", "d", "V"); err != nil {
		t.Fatalf("re-request after expiry: %v", err)
	}
}

// TestRejectOrder 验证拒绝优先级：参数>时钟>不存在>授权>状态>无床/不独占。
func TestRejectOrder(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 10, "a", ward.SexFemale, false, "W")

	_, err := s.Admit(5, "op", "x", ward.Sex(0), false, "W")
	errIs(t, err, ward.ErrInvalid)
	_, err = s.Admit(5, "op", "x", ward.SexMale, false, "W")
	errIs(t, err, ward.ErrClock)
	_, err = s.Admit(20, "op", "x", ward.SexMale, false, "ZZ")
	errIs(t, err, ward.ErrNotFound)
	_, err = s.Admit(20, "x", "z", ward.SexMale, false, "W")
	errIs(t, err, ward.ErrNoGrant)
	_, err = s.Admit(20, "op", "a", ward.SexMale, false, "W")
	errIs(t, err, ward.ErrState)

	_, err = s.Request(20, "op", "ghost", "W")
	errIs(t, err, ward.ErrNotFound)
	_, err = s.Request(20, "op2", "a", "V")
	errIs(t, err, ward.ErrNoGrant)
	_, err = s.Request(20, "op", "a", "W")
	errIs(t, err, ward.ErrInvalid)

	// 被拒不改时钟：now=9（<10 且病区不存在）先判时钟回退。
	_, err = s.Admit(9, "op", "q", ward.SexMale, false, "ZZ")
	errIs(t, err, ward.ErrClock)

	errIs(t, s.Discharge(20, "op", "ghost"), ward.ErrNotFound)
	errIs(t, s.Confirm(20, "op", "ghost"), ward.ErrNotFound)
	errIs(t, s.CancelTransfer(20, "op", "ghost"), ward.ErrNotFound)
	errIs(t, s.SetIso(20, "op", "ghost", true), ward.ErrNotFound)

	errIs(t, s.Confirm(20, "op", "a"), ward.ErrState)
	errIs(t, s.CancelTransfer(20, "op", "a"), ward.ErrState)
	if _, err := s.Request(20, "op", "a", "V"); err != nil {
		t.Fatal(err)
	}
	errIs(t, s.SetIso(20, "op", "a", true), ward.ErrState)
}

// TestSetIsoSole：开启隔离要求除本人外无在房者，清洁中不妨碍。
func TestSetIsoSole(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	// 直接布置：R1-1=q、R1-2=a（同性同房），R2 三名女性占满。
	place := func(room string, no int, id string, iso bool) {
		h.Wards["W"].Rooms[room].Beds[no].Occupant = id
		h.Patients[id] = &ward.Patient{ID: id, Sex: ward.SexFemale, Iso: iso, Ward: "W", Room: room, Bed: no}
	}
	place("R1", 1, "q", false)
	place("R1", 2, "a", false)
	place("R2", 1, "r1", false)
	place("R2", 2, "r2", false)
	place("R2", 3, "r3", false)

	// 同房间有 q：置隔离被拒（房间不独占）。
	errIs(t, s.SetIso(10, "op", "a", true), ward.ErrNotSole)
	if err := s.Discharge(10, "op", "q"); err != nil {
		t.Fatal(err)
	}
	// q 的床清洁中（10..40）：t=20 置隔离允许，清洁床不妨碍独占判定。
	if err := s.SetIso(20, "op", "a", true); err != nil {
		t.Fatalf("set iso with cleaning roommate bed: %v", err)
	}
	// R1 带隔离标志、R2 已满：女性 x 只能进入空房 R3，得 R3-1。
	if got := admit(t, s, 40, "x", ward.SexFemale, false, "W"); got != "R3-1" {
		t.Fatalf("x = %s want R3-1 (R1 iso-flagged, R2 full)", got)
	}
	if err := s.SetIso(50, "op", "a", false); err != nil {
		t.Fatal(err)
	}
	// q 床 t=40 已清洁；a 解除标志后女性 y 可进 R1-1。
	if got := admit(t, s, 50, "y", ward.SexFemale, false, "W"); got != "R1-1" {
		t.Fatalf("y = %s want R1-1", got)
	}
}

// TestTransferLifecycle：目标授权确认、源授权取消、出院连带取消预留。
func TestTransferLifecycle(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 0, "a", ward.SexFemale, false, "W")

	if _, err := s.Request(10, "op", "a", "V"); err != nil {
		t.Fatal(err)
	}
	errIs(t, s.Confirm(10, "wonly", "a"), ward.ErrNoGrant)

	h.Grant("wonly", "W")
	if err := s.CancelTransfer(11, "wonly", "a"); err != nil {
		t.Fatalf("cancel by source grant: %v", err)
	}
	if got := admit(t, s, 12, "m", ward.SexMale, false, "V"); got != "R9-1" {
		t.Fatalf("V free after cancel, got %s", got)
	}
	if err := s.Discharge(13, "op", "m"); err != nil {
		t.Fatal(err)
	}
	// R9-1 进入清洁至 43。t=43 恰等清洁完成，预留才能选中该床。

	if _, err := s.Request(43, "op", "a", "V"); err != nil {
		t.Fatal(err)
	}
	if err := s.Discharge(44, "op", "a"); err != nil {
		t.Fatal(err)
	}
	b := h.Wards["V"].Rooms["R9"].Beds[1]
	if got := b.State(44, h.H); got != ward.BedFree {
		t.Fatalf("reserved bed after discharge = %v want free", got)
	}
}

// TestRejectedOpNoChange：被拒操作不触碰床位与时钟。
func TestRejectedOpNoChange(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 0, "a", ward.SexFemale, false, "W")
	before := *h.Wards["W"].Rooms["R1"].Beds[1]

	_, _ = s.Admit(5, "op", "x", ward.SexMale, false, "W")
	_, _ = s.Admit(0, "nobody", "x", ward.SexMale, false, "W")
	_, _ = s.Request(5, "op", "a", "V")
	_ = s.SetIso(0, "nobody", "a", true)

	after := *h.Wards["W"].Rooms["R1"].Beds[1]
	if after != before {
		t.Fatalf("bed changed after rejected ops: %+v vs %+v", before, after)
	}
	// 成功操作仍推进时钟（先前 a 在 0 入住；此处 now>=0 的新操作可成功）。
	if _, err := s.Admit(100, "op", "z", ward.SexMale, false, "W"); err != nil {
		t.Fatal(err)
	}
}

// TestTouchedBound：Discharge/Confirm 触碰床位数不超过 2。
func TestTouchedBound(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	admit(t, s, 0, "a", ward.SexFemale, false, "W")
	if _, err := s.Request(10, "op", "a", "V"); err != nil {
		t.Fatal(err)
	}
	if err := s.Confirm(11, "op", "a"); err != nil {
		t.Fatal(err)
	}
	if s.Touched() > 2 {
		t.Fatalf("Confirm touched=%d > 2", s.Touched())
	}
	if err := s.Discharge(12, "op", "a"); err != nil {
		t.Fatal(err)
	}
	if s.Touched() > 2 {
		t.Fatalf("Discharge touched=%d > 2", s.Touched())
	}
}

// TestConcurrentLinearizable：并发操作不产生竞态且不变量成立。
func TestConcurrentLinearizable(t *testing.T) {
	h, s := newSvc(t, 30, 60)
	setup(t, h)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "p" + nitoa(i)
			_, _ = s.Admit(int64(i), "op", id, ward.Sex(i%2+1), i%5 == 0, "W")
			_ = s.Discharge(int64(i+100), "op", id)
		}(i)
	}
	wg.Wait()
	// 时钟必须停留在某个被接受操作的 now 上（确定性串行化）。
	if h.LastNow < 0 || h.LastNow > 115 {
		t.Fatalf("LastNow=%d", h.LastNow)
	}
}
