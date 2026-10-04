package bedalloc

import (
	"testing"

	"ontology/ward"
)

func mustSetup(t *testing.T, cl, hold int) (*ward.Hospital, *Allocator) {
	t.Helper()
	h, err := ward.New(cl, hold)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return h, New(h)
}

func pick(a *Allocator, now int64, h *ward.Hospital, wn string, sex ward.Sex, iso bool) string {
	c := a.Select(now, h.Wards[wn], sex, iso)
	if c == nil {
		return ""
	}
	return c.Room.Name + "-" + itoa(c.Bed.No)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestSelectRules 覆盖同性房优先、并列房号、最小床号、清洁恰等与隔离全空。
func TestSelectRules(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, h *ward.Hospital, a *Allocator)
	}{
		{
			name: "空房并列取总床数最少、房号字节序、最小床号",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "W", "R1", 2)
				mustRoom(t, h, "W", "R2", 3)
				mustRoom(t, h, "W", "R3", 2)
				if got := pick(a, 0, h, "W", ward.SexFemale, false); got != "R1-1" {
					t.Fatalf("female a = %q want R1-1", got)
				}
			},
		},
		{
			name: "已有同性在房者房间优先，按空闲床数最少",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "W", "R1", 2)
				mustRoom(t, h, "W", "R2", 3)
				mustRoom(t, h, "W", "R3", 2)
				occupy(h, "W", "R1", 1, "a", ward.SexFemale, false)
				occupy(h, "W", "R2", 1, "d", ward.SexFemale, false)
				occupy(h, "W", "R3", 1, "b", ward.SexMale, false)
				if got := pick(a, 0, h, "W", ward.SexFemale, false); got != "R1-2" {
					t.Fatalf("female c = %q want R1-2 (R1 fewer free beds)", got)
				}
			},
		},
		{
			name: "异性房不可用、隔离房不可用",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "W", "R1", 2)
				mustRoom(t, h, "W", "R2", 3)
				occupy(h, "W", "R1", 1, "a", ward.SexFemale, false)
				occupy(h, "W", "R2", 1, "e", ward.SexMale, true)
				if got := pick(a, 0, h, "W", ward.SexMale, false); got != "" {
					t.Fatalf("male f = %q want no bed (R1 female, R2 iso)", got)
				}
				if got := pick(a, 0, h, "W", ward.SexFemale, false); got != "R1-2" {
					t.Fatalf("female g = %q want R1-2", got)
				}
			},
		},
		{
			name: "隔离只收全空房，清洁中不算；恰等清洁完成可用",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "W", "R1", 2)
				b := h.Wards["W"].Rooms["R1"].Beds[1]
				b.CleanUntil = 30
				if got := pick(a, 29, h, "W", ward.SexMale, true); got != "" {
					t.Fatalf("iso at 29 = %q want none (R1 cleaning)", got)
				}
				if got := pick(a, 30, h, "W", ward.SexMale, true); got != "R1-1" {
					t.Fatalf("iso at 30 = %q want R1-1 (clean finished exactly)", got)
				}
			},
		},
		{
			name: "有效预留计入性别与占用；恰等时刻预留失效",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "V", "R9", 2)
				reserve(h, "V", "R9", 1, "d", ward.SexFemale, 200)
				if got := pick(a, 210, h, "V", ward.SexMale, false); got != "" {
					t.Fatalf("male at 210 = %q want none (female reserve)", got)
				}
				if got := pick(a, 259, h, "V", ward.SexMale, true); got != "" {
					t.Fatalf("iso at 259 = %q want none (reserve active)", got)
				}
				if got := pick(a, 260, h, "V", ward.SexMale, false); got != "R9-1" {
					t.Fatalf("male at 260 = %q want R9-1 (reserve expired exactly)", got)
				}
			},
		},
		{
			name: "清洁中床不妨碍非隔离填空房（全空但有清洁床）",
			run: func(t *testing.T, h *ward.Hospital, a *Allocator) {
				mustRoom(t, h, "W", "R2", 2)
				h.Wards["W"].Rooms["R2"].Beds[1].CleanUntil = 100
				if got := pick(a, 0, h, "W", ward.SexMale, false); got != "R2-2" {
					t.Fatalf("got %q want R2-2 (free bed in room with cleaning bed)", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, a := mustSetup(t, 30, 60)
			tc.run(t, h, a)
		})
	}
}

// TestExaminedBound：examined 不超过目标病区床位数，且与其他病区数量无关。
func TestExaminedBound(t *testing.T) {
	cases := []struct {
		name       string
		otherWards int
	}{
		{"1 个病区", 0},
		{"1000 个病区", 999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, a := mustSetup(t, 30, 60)
			mustRoom(t, h, "W", "R1", 8)
			for i := 0; i < tc.otherWards; i++ {
				wn := "X" + itoa(i)
				if err := h.AddRoom(wn, "R", 8); err != nil {
					t.Fatal(err)
				}
			}
			a.Select(0, h.Wards["W"], ward.SexMale, false)
			if a.Examined() > 8 {
				t.Fatalf("non-iso examined=%d > target beds 8", a.Examined())
			}
			a.Select(0, h.Wards["W"], ward.SexMale, true)
			if a.Examined() > 8 {
				t.Fatalf("iso examined=%d > target beds 8", a.Examined())
			}
		})
	}
}

func mustRoom(t *testing.T, h *ward.Hospital, wn, rn string, beds int) {
	t.Helper()
	if err := h.AddRoom(wn, rn, beds); err != nil {
		t.Fatalf("AddRoom %s/%s: %v", wn, rn, err)
	}
}

func occupy(h *ward.Hospital, wn, rn string, no int, id string, sex ward.Sex, iso bool) {
	h.Wards[wn].Rooms[rn].Beds[no].Occupant = id
	h.Patients[id] = &ward.Patient{ID: id, Sex: sex, Iso: iso, Ward: wn, Room: rn, Bed: no}
}

func reserve(h *ward.Hospital, wn, rn string, no int, id string, sex ward.Sex, at int64) {
	b := h.Wards[wn].Rooms[rn].Beds[no]
	b.ReservedBy = id
	b.ReserveAt = at
	b.ReserveSex = sex
	b.ReserveIso = false
}
