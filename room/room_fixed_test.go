package room

import (
	"errors"
	"testing"

	"ontology/seat"
)

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func eqErr(got, want error) bool { return errors.Is(got, want) }

// TestSpecExample1 复现题目第一个完整例子。
func TestSpecExample1(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 2, 3, 2, 100))
	mustOK(t, m.FormParty("P1", []string{"a", "b"}))
	mustOK(t, m.FormParty("P2", []string{"c"}))
	mustOK(t, m.FormParty("P3", []string{"d", "e"}))
	mustOK(t, m.Reserve(0, "r", "P1"))
	mustOK(t, m.Reserve(10, "r", "P2"))
	mustOK(t, m.Reserve(20, "r", "P3"))
	v, err := m.Seats(20, "r")
	mustOK(t, err)
	want := map[[2]int]string{{0, 0}: "a", {0, 1}: "b", {1, 0}: "c", {1, 1}: "d", {1, 2}: "e"}
	for _, c := range v.Cells {
		name, occ := want[[2]int{c.Team, c.Index}]
		if occ != (c.Status == seat.Reserved) || (occ && c.Player != name) {
			t.Fatalf("cell %d,%d = %q/%v", c.Team, c.Index, c.Player, c.Status)
		}
	}
	if seq, err := m.Confirm(30, "r", "b"); err != nil || seq != 1 || m.rooms["r"].host != "b" {
		t.Fatalf("confirm b: seq=%d err=%v host=%q", seq, err, m.rooms["r"].host)
	}
	if seq, err := m.Confirm(50, "r", "a"); err != nil || seq != 2 {
		t.Fatalf("confirm a: seq=%d err=%v", seq, err)
	}
	mustOK(t, m.Leave(60, "r", "b"))
	if v, _ := m.Seats(60, "r"); v.Host != "a" {
		t.Fatalf("host after leave = %q", v.Host)
	}
	if seq, err := m.Confirm(109, "r", "c"); err != nil || seq != 3 {
		t.Fatalf("confirm c at 109: seq=%d err=%v", seq, err)
	}
	if _, err := m.Confirm(110, "r", "c"); !eqErr(err, ErrNoReservation) {
		t.Fatalf("confirm c at 110: %v", err)
	}
	v, _ = m.Seats(120, "r")
	if v.Host != "a" {
		t.Fatalf("host at 120 = %q", v.Host)
	}
	free := 0
	for _, c := range v.Cells {
		if c.Status == seat.Empty {
			free++
		}
		if c.Player == "d" || c.Player == "e" {
			t.Fatalf("d/e must be released at 120")
		}
	}
	if free != 4 { // 队0：b离开与未用位=2；队1：d、e到期=2；a、c在座
		t.Fatalf("empty seats at 120 = %d", free)
	}
}

// TestSpecExample2 失衡、空席不足先于失衡、被拒不改状态。
func TestSpecExample2(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 2, 3, 1, 100))
	mustOK(t, m.FormParty("X", []string{"x"}))
	mustOK(t, m.FormParty("Y", []string{"y"}))
	mustOK(t, m.FormParty("AB", []string{"a", "b"}))
	mustOK(t, m.Reserve(0, "r", "X"))
	mustOK(t, m.Reserve(1, "r", "Y"))
	if err := m.Reserve(2, "r", "AB"); !eqErr(err, ErrImbalance) {
		t.Fatalf("imbalance: %v", err)
	}
	mustOK(t, m.FormParty("Z", []string{"z"}))
	mustOK(t, m.Reserve(3, "r", "Z"))
	v, _ := m.Seats(3, "r")
	if v.Cells[0].Player != "x" || v.Cells[1].Player != "z" {
		t.Fatalf("rejected reserve changed state: %+v", v.Cells[:4])
	}
	// 构造占用 1 比 3：6 个位置用单人填成 3 比 3，再让队0 两人离开。
	m2 := NewManager()
	mustOK(t, m2.NewRoom("r", 2, 3, 1, 100))
	var err error
	for i, name := range []string{"p", "q", "r", "s", "t", "u"} {
		mustOK(t, m2.FormParty(name, []string{name}))
		mustOK(t, m2.Reserve(int64(i), "r", name))
	}
	_, err = m2.Confirm(6, "r", "p")
	mustOK(t, err)
	_, err = m2.Confirm(7, "r", "q")
	mustOK(t, err)
	mustOK(t, m2.Leave(8, "r", "p"))
	mustOK(t, m2.Leave(9, "r", "q"))
	mustOK(t, m2.FormParty("VV", []string{"v", "w"}))
	if err := m2.Reserve(10, "r", "VV"); !eqErr(err, ErrNoSeats) {
		t.Fatalf("want no seats, got %v", err)
	}
}

// TestExpiryEqualityAndPartialConfirm 到期取等、部分确认只放未确认者、房主再产生。
func TestExpiryEqualityAndPartialConfirm(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 2, 4, 4, 10))
	mustOK(t, m.FormParty("P", []string{"a", "b"}))
	mustOK(t, m.Reserve(0, "r", "P"))
	_, e := m.Confirm(5, "r", "a")
	mustOK(t, e)
	v, err := m.Seats(10, "r")
	mustOK(t, err)
	for _, c := range v.Cells {
		if c.Player == "b" {
			t.Fatalf("b should expire at equality")
		}
	}
	if v.Host != "a" {
		t.Fatalf("host = %q, want a", v.Host)
	}
	mustOK(t, m.FormParty("Q", []string{"c"}))
	mustOK(t, m.Reserve(11, "r", "Q"))
	if seq, err := m.Confirm(12, "r", "c"); err != nil || seq != 2 {
		t.Fatalf("confirm c seq=%d err=%v", seq, err)
	}
	if v, _ := m.Seats(12, "r"); v.Host != "a" {
		t.Fatalf("host = %q want a", v.Host)
	}
	mustOK(t, m.Leave(13, "r", "a"))
	if v, _ := m.Seats(13, "r"); v.Host != "c" {
		t.Fatalf("host after a leaves = %q want c", v.Host)
	}
	mustOK(t, m.Leave(14, "r", "c"))
	if v, _ := m.Seats(14, "r"); v.Host != "" {
		t.Fatalf("host should be empty, got %q", v.Host)
	}
	mustOK(t, m.FormParty("G", []string{"g"}))
	mustOK(t, m.Reserve(15, "r", "G"))
	if _, err := m.Confirm(16, "r", "g"); err != nil {
		t.Fatalf("regenerate host: %v", err)
	}
	if v, _ := m.Seats(16, "r"); v.Host != "g" {
		t.Fatalf("host regenerate = %q", v.Host)
	}
}

func TestTeamTiePickSmallest(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 3, 4, 4, 100))
	mustOK(t, m.FormParty("A", []string{"a"}))
	mustOK(t, m.FormParty("B", []string{"b"}))
	mustOK(t, m.Reserve(0, "r", "A"))
	mustOK(t, m.Reserve(1, "r", "B"))
	v, _ := m.Seats(1, "r")
	if v.Cells[4].Player != "b" {
		t.Fatalf("tie should pick team 1, got %q", v.Cells[4].Player)
	}
}

func TestHostTransferByConfirmSeq(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 2, 4, 4, 100))
	mustOK(t, m.FormParty("P", []string{"a", "b", "c"}))
	mustOK(t, m.Reserve(0, "r", "P"))
	_, e := m.Confirm(1, "r", "c")
	mustOK(t, e)
	_, e = m.Confirm(2, "r", "b")
	mustOK(t, e)
	_, e = m.Confirm(3, "r", "a")
	mustOK(t, e)
	mustOK(t, m.Leave(4, "r", "c"))
	if v, _ := m.Seats(4, "r"); v.Host != "b" {
		t.Fatalf("host = %q want b", v.Host)
	}
}

func TestBanAndKick(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r", 2, 4, 4, 100))
	mustOK(t, m.FormParty("P", []string{"a", "b"}))
	mustOK(t, m.Reserve(0, "r", "P"))
	_, e := m.Confirm(1, "r", "a")
	mustOK(t, e)
	if err := m.Kick(2, "r", "b", "a"); !eqErr(err, ErrNotHost) {
		t.Fatalf("non host kick: %v", err)
	}
	if err := m.Kick(2, "r", "a", "a"); !eqErr(err, ErrInvalidParam) {
		t.Fatalf("self kick: %v", err)
	}
	mustOK(t, m.Kick(3, "r", "a", "b"))
	if v, _ := m.Seats(3, "r"); v.Host != "a" {
		t.Fatalf("host after kick = %q", v.Host)
	}
	mustOK(t, m.Leave(4, "r", "a"))
	if err := m.Reserve(5, "r", "P"); !eqErr(err, ErrBanned) {
		t.Fatalf("re-reserve banned member: %v", err)
	}
	if err := m.Kick(6, "r", "a", "b"); !eqErr(err, ErrNotHost) {
		t.Fatalf("kick after no host: %v", err)
	}
}

func TestCrossRoomOccupancy(t *testing.T) {
	m := NewManager()
	mustOK(t, m.NewRoom("r1", 2, 3, 3, 100))
	mustOK(t, m.NewRoom("r2", 2, 3, 3, 100))
	mustOK(t, m.FormParty("P", []string{"a", "b"}))
	mustOK(t, m.Reserve(0, "r1", "P"))
	if err := m.Reserve(1, "r2", "P"); !eqErr(err, ErrOccupied) {
		t.Fatalf("cross room reserve: %v", err)
	}
	_, e := m.Confirm(2, "r1", "a")
	mustOK(t, e)
	if _, err := m.Confirm(3, "r2", "a"); !eqErr(err, ErrNoReservation) {
		t.Fatalf("cross room confirm: %v", err)
	}
	_, err := m.Seats(101, "r1")
	mustOK(t, err)
	// a 已确认，到期只释放 b；a 离开后整组才真正腾出。
	mustOK(t, m.Leave(101, "r1", "a"))
	mustOK(t, m.Reserve(102, "r2", "P"))
}

func TestRejectOrdersAndRollback(t *testing.T) {
	m := NewManager()
	var err error
	if err = m.NewRoom("r", 1, 3, 3, 100); !eqErr(err, ErrInvalidParam) {
		t.Fatalf("bad params: %v", err)
	}
	if err := m.NewRoom("", 2, 3, 3, 100); !eqErr(err, ErrInvalidParam) {
		t.Fatalf("empty rid: %v", err)
	}
	mustOK(t, m.NewRoom("r", 2, 3, 2, 100))
	if err := m.NewRoom("r", 2, 3, 2, 100); !eqErr(err, ErrRoomExists) {
		t.Fatalf("dup room: %v", err)
	}
	if _, err := m.Seats(4, "r"); err != nil {
		t.Fatalf("prime maxNow: %v", err)
	}
	if err := m.Reserve(3, "r", "P"); !eqErr(err, ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
	if err := m.Reserve(5, "r", "P"); !eqErr(err, ErrPartyNotFound) {
		t.Fatalf("party not found: %v", err)
	}
	mustOK(t, m.FormParty("P", []string{"a", "b"}))
	mustOK(t, m.Reserve(5, "r", "P"))
	if err := m.Reserve(6, "r", "P"); !eqErr(err, ErrOccupied) {
		t.Fatalf("self occupied: %v", err)
	}
	if _, err := m.Confirm(7, "r", "zzz"); !eqErr(err, ErrNoReservation) {
		t.Fatalf("confirm ghost: %v", err)
	}
	if err := m.Leave(7, "r", "zzz"); !eqErr(err, ErrNotInRoom) {
		t.Fatalf("leave ghost: %v", err)
	}
	// 时钟回退不改时钟：之后等于 maxNow 的操作仍可接受。
	_, err = m.Seats(7, "r")
	mustOK(t, err)
	// 参数非法优先于时钟回退。
	if _, err := m.Confirm(-9, "", ""); !eqErr(err, ErrInvalidParam) {
		t.Fatalf("param over rollback: %v", err)
	}
}

// TestTouchedBound 到期检查数 ≤ 释放组数 + 1，且与房间数、未到期预留数无关。
func TestTouchedBound(t *testing.T) {
	for _, rooms := range []int{10, 10000} {
		m := NewManager()
		for i := 0; i < rooms; i++ {
			rid := "r" + itoa(i)
			mustOK(t, m.NewRoom(rid, 2, 16, 16, 100))
		}
		// 目标房：3 组到期 + 若干未到期预留。
		target := "r0"
		for i := 0; i < 3; i++ {
			pid := "exp" + itoa(i)
			mustOK(t, m.FormParty(pid, []string{pid + "a"}))
			mustOK(t, m.Reserve(0, target, pid))
		}
		for i := 0; i < 5; i++ {
			pid := "keep" + itoa(i)
			mustOK(t, m.FormParty(pid, []string{pid + "a"}))
			mustOK(t, m.Reserve(50, target, pid)) // expire=150，在 now=100 仍有效
		}
		// 其他房间各塞未到期预留。
		for i := 1; i < rooms; i++ {
			rid := "r" + itoa(i)
			pid := "noise" + itoa(i)
			mustOK(t, m.FormParty(pid, []string{pid + "a"}))
			mustOK(t, m.Reserve(50, rid, pid))
		}
		v, err := m.Seats(100, target)
		mustOK(t, err)
		if m.touched > 3+1 {
			t.Fatalf("rooms=%d touched=%d > %d", rooms, m.touched, 3+1)
		}
		if m.touched != 4 {
			t.Fatalf("rooms=%d touched=%d want 4 (3 pops + 1 peek)", rooms, m.touched)
		}
		alive := 0
		for _, c := range v.Cells {
			if c.Status != seat.Empty {
				alive++
			}
		}
		if alive != 5 {
			t.Fatalf("alive reservations = %d, want 5", alive)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
