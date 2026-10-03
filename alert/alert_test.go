package alert

import (
	"errors"
	"log"
	"os"
	"testing"

	"ontology/cal"
	"ontology/sla"
)

func setup(t *testing.T) (*Manager, *sla.Manager, *cal.Calendar) {
	t.Helper()
	c, err := cal.New(540, 1080, 0b0011111)
	if err != nil {
		t.Fatal(err)
	}
	sm := sla.NewManager(c)
	am := NewManager(sm, c)
	am.SetLogger(log.New(os.Stderr, "ALERT ", log.LstdFlags|log.Lmicroseconds))
	return am, sm, c
}

func alertsEqual(got []Alert, want []Alert) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if string(got[i].ID) != string(want[i].ID) ||
			got[i].Level != want[i].Level || got[i].At != want[i].At {
			return false
		}
	}
	return true
}

func TestTickExampleAndRedelivery(t *testing.T) {
	am, sm, _ := setup(t)
	id := []byte("s")
	if err := sm.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	got, err := am.Tick(2100)
	if err != nil {
		t.Fatal(err)
	}
	want := []Alert{{id, 50, 900}, {id, 80, 1080}, {id, 100, 2100}}
	if !alertsEqual(got, want) {
		t.Fatalf("Tick=%+v want %+v", got, want)
	}
	got2, _ := am.Tick(2100)
	if !alertsEqual(got2, want) {
		t.Fatalf("重复 Tick 应再次返回三条: %+v", got2)
	}
	if err := am.Ack(id, 50, 2100); err != nil {
		t.Fatal(err)
	}
	got3, _ := am.Tick(2200)
	if !alertsEqual(got3, want[1:]) {
		t.Fatalf("Ack 后应剩两条: %+v", got3)
	}
	if err := am.Ack(id, 50, 2200); err != nil {
		t.Fatalf("重复 Ack 应成功无操作: %v", err)
	}
}

func TestAckNotDueAndArgs(t *testing.T) {
	am, sm, _ := setup(t)
	id := []byte("s")
	if err := sm.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := am.Ack(id, 100, 2000); !errors.Is(err, ErrNotDue) {
		t.Fatalf("未到期 Ack err=%v want ErrNotDue", err)
	}
	if err := am.Ack(id, 70, 3000); !errors.Is(err, cal.ErrArgument) {
		t.Fatalf("非法 q err=%v", err)
	}
	if err := am.Ack(nil, 50, 3000); !errors.Is(err, cal.ErrArgument) {
		t.Fatalf("空 id err=%v", err)
	}
	if err := am.Ack([]byte("nope"), 50, 3000); !errors.Is(err, sla.ErrNotFound) {
		t.Fatalf("未知 id err=%v", err)
	}
}

func TestDueWhilePausedStillTicks(t *testing.T) {
	am, sm, _ := setup(t)
	id := []byte("s")
	if err := sm.Start(id, 600, 600); err != nil {
		t.Fatal(err)
	}
	if err := sm.Pause(id, 1000); err != nil {
		t.Fatal(err)
	}
	got, _ := am.Tick(2000)
	want := []Alert{{id, 50, 900}}
	if !alertsEqual(got, want) {
		t.Fatalf("暂停时已到期的 50%% 档仍应返回: %+v", got)
	}
	if err := am.Ack(id, 80, 2000); !errors.Is(err, ErrNotDue) {
		t.Fatalf("暂停未达档 Ack err=%v", err)
	}
}

func TestOrderingByAtIDLevel(t *testing.T) {
	am, sm, _ := setup(t)
	b1, b2 := []byte("b"), []byte("a")
	if err := sm.Start(b1, 600, 540); err != nil {
		t.Fatal(err)
	}
	if err := sm.Start(b2, 600, 540); err != nil {
		t.Fatal(err)
	}
	got, _ := am.Tick(1080)
	if len(got) != 4 {
		t.Fatalf("got %d alerts want 4", len(got))
	}
	if got[0].At != 840 || string(got[0].ID) != "a" || got[0].Level != 50 {
		t.Fatalf("排序首条错误: %+v", got[0])
	}
	if string(got[1].ID) != "b" {
		t.Fatalf("同刻应按 id 字节序: %+v", got[1])
	}
	for _, a := range got {
		if a.Level == 100 {
			t.Fatalf("100%% 档此刻未到期，不应出现: %+v", a)
		}
	}
}
