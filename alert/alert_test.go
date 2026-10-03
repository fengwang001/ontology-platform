package alert

import (
	"errors"
	"fmt"
	"testing"

	"ontology/cal"
	"ontology/sla"
)

func eqAlarms(got []Alarm, want []Alarm) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Q != want[i].Q || got[i].T != want[i].T ||
			string(got[i].ID) != string(want[i].ID) {
			return false
		}
	}
	return true
}

func logAlarms(t *testing.T, label string, a []Alarm) {
	t.Helper()
	for _, x := range a {
		t.Logf("%s: (%s,%d,%d)", label, x.ID, x.Q, x.T)
	}
}

func TestExampleAlarms(t *testing.T) {
	c := cal.New(540, 1080, 0b0011111)
	m, s := NewManager(c)
	id := []byte("s")
	if err := s.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	got, err := m.Tick(2100)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alarm{{id, 50, 900}, {id, 80, 1080}, {id, 100, 2100}}
	logAlarms(t, "Tick(2100)", got)
	if !eqAlarms(got, want) {
		t.Fatalf("Tick=%v want %v", got, want)
	}
	// Tick 不消费：重复返回全部
	got2, _ := m.Tick(2200)
	if !eqAlarms(got2, want) {
		t.Fatalf("second Tick must redeliver all: %v", got2)
	}
	if err := m.Ack(id, 50, 2200); err != nil {
		t.Fatalf("ack 50: %v", err)
	}
	got3, _ := m.Tick(2300)
	if !eqAlarms(got3, want[1:]) {
		t.Fatalf("after ack50: %v want %v", got3, want[1:])
	}
	// 重复 Ack 成功无操作
	if err := m.Ack(id, 50, 2400); err != nil {
		t.Fatalf("double ack: %v", err)
	}
	// Tick 提前到未到期时刻
	c2 := cal.New(540, 1080, 0b0011111)
	m2, s2 := NewManager(c2)
	s2.Start(id, 600, 600)
	if a, _ := m2.Tick(899); len(a) != 0 {
		t.Fatalf("Tick(899) must be empty got %v", a)
	}
	if err := m2.Ack(id, 50, 899); !errors.Is(err, ErrNotDue) {
		t.Fatalf("ack early want ErrNotDue got %v", err)
	}
	if err := m2.Ack(id, 70, 900); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad q want ErrInvalid got %v", err)
	}
	t.Logf("example alarms, redelivery, ack, not-due verified")
}

func TestPausedDueAlarms(t *testing.T) {
	c := cal.New(540, 1080, 0b0011111)
	m, s := NewManager(c)
	id := []byte("s")
	s.Start(id, 600, 600)
	// 900 已到期后，1000 暂停；暂停期间 50% 仍应投递，80/100 未到期
	s.Pause(id, 1000)
	got, _ := m.Tick(2000)
	logAlarms(t, "paused Tick(2000)", got)
	if len(got) != 1 || got[0].Q != 50 || got[0].T != 900 {
		t.Fatalf("paused due alarm =%v want just (s,50,900)", got)
	}
	if err := m.Ack(id, 80, 2000); !errors.Is(err, ErrNotDue) {
		t.Fatalf("ack 80 while paused-not-due: %v", err)
	}
	s.Resume(id, 2000)
	got, _ = m.Tick(2200)
	if !eqAlarms(got, []Alarm{{id, 50, 900}, {id, 80, 2080}, {id, 100, 2200}}) {
		t.Fatalf("after resume: %v", got)
	}
	t.Logf("paused-then-due alarms match T80=2080 T100=2200")
}

func TestHolidayInterleaveAlarms(t *testing.T) {
	c := cal.New(540, 1080, 0b0011111)
	m, s := NewManager(c)
	id := []byte("s")
	s.Start(id, 600, 600)
	s.Pause(id, 1000)
	if err := c.AddHoliday(1, 1000); err != nil {
		t.Fatal(err)
	}
	s.Resume(id, 2000)
	got, _ := m.Tick(4000)
	logAlarms(t, "holiday Tick", got)
	if !eqAlarms(got, []Alarm{{id, 50, 900}, {id, 80, 3500}, {id, 100, 3620}}) {
		t.Fatalf("holiday-interleave alarms=%v", got)
	}
}

func TestOrdering(t *testing.T) {
	c := cal.New(0, 1440, 127)
	m, s := NewManager(c)
	s.Start([]byte("b"), 100, 0)
	s.Start([]byte("a"), 100, 0)
	got, _ := m.Tick(1440 * 3)
	logAlarms(t, "ordering", got)
	// 排序以 t 为主键，同刻再 id 字节序、再 q
	wantQs := []int{50, 50, 80, 80, 100, 100}
	wantIDs := []string{"a", "b", "a", "b", "a", "b"}
	for i := range got {
		if got[i].Q != wantQs[i] || string(got[i].ID) != wantIDs[i] || got[i].T > 1440*3 {
			t.Fatalf("order mismatch at %d: %v", i, got)
		}
	}
	t.Logf("ordering by t,id,q verified")
}

func TestClockRejects(t *testing.T) {
	c := cal.New(0, 1440, 127)
	m, s := NewManager(c)
	s.Start([]byte("x"), 10, 100)
	if _, err := m.Tick(99); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("Tick backwards: %v", err)
	}
	if err := m.Ack([]byte("x"), 50, 99); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("Ack backwards: %v", err)
	}
	if err := m.Ack([]byte("missing"), 50, 200); !errors.Is(err, sla.ErrNotFound) {
		t.Fatalf("missing timer at future now want NotFound got %v", err)
	}
	if err := m.Ack([]byte("missing"), 50, 99); !errors.Is(err, cal.ErrClock) {
		t.Fatalf("clock precedence over notfound got %v", err)
	}
	fmt.Println("clock rejection precedence verified")
}
