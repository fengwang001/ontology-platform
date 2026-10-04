package dock

import (
	"bytes"
	"errors"
	"testing"

	"ontology/appt"
)

func cfg() appt.Config { return appt.Config{S: 20, E: 30, L: 15, Wmax: 60, K: 2} }

func TestAddDockTable(t *testing.T) {
	cases := []struct {
		name string
		run  func(d *Docks) error
		want error
	}{
		{"ok", func(d *Docks) error { return d.AddDock([]byte("D1"), Dry, 0) }, nil},
		{"空编号", func(d *Docks) error { return d.AddDock(nil, Dry, 0) }, ErrInvalid},
		{"非法类型", func(d *Docks) error { return d.AddDock([]byte("X"), appt.Kind(7), 0) }, ErrInvalid},
		{"重复编号", func(d *Docks) error {
			if err := d.AddDock([]byte("D1"), Dry, 0); err != nil {
				t.Fatal(err)
			}
			return d.AddDock([]byte("D1"), Reefer, 5)
		}, ErrDuplicate},
		{"时钟回退", func(d *Docks) error {
			if err := d.AddDock([]byte("D1"), Dry, 10); err != nil {
				t.Fatal(err)
			}
			return d.AddDock([]byte("D2"), Dry, 9)
		}, ErrClock},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(New(cfg())); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestTakeAndRelease(t *testing.T) {
	d := New(cfg())
	for _, x := range [][2]any{{"R2", Reefer}, {"R1", Reefer}, {"D2", Dry}, {"D1", Dry}} {
		if err := d.AddDock([]byte(x[0].(string)), x[1].(Kind), 0); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := d.Take(Reefer, false)
	if !ok || !bytes.Equal(got, []byte("R1")) {
		t.Fatalf("reefer min = R1, got %s", got)
	}
	got, _ = d.Take(Dry, false)
	if !bytes.Equal(got, []byte("D1")) {
		t.Fatalf("dry prefers dry min = D1, got %s", got)
	}
	got, _ = d.Take(Dry, false)
	if !bytes.Equal(got, []byte("D2")) {
		t.Fatalf("next dry min = D2, got %s", got)
	}
	// 无常温位且允许借用时取冷藏最小。
	got, _ = d.Take(Dry, true)
	if !bytes.Equal(got, []byte("R2")) {
		t.Fatalf("borrow reefer min = R2, got %s", got)
	}
	// 无任何可借。
	if _, ok := d.Take(Dry, true); ok {
		t.Fatal("no docks left")
	}
	if _, ok := d.Take(Reefer, false); ok {
		t.Fatal("no reefer left")
	}
	d.Release([]byte("R1"))
	got, ok = d.Take(Reefer, false)
	if !ok || !bytes.Equal(got, []byte("R1")) {
		t.Fatalf("after release R1, got %s", got)
	}
	d.Release([]byte("D1"))
	// 常温位回归后常温车不再借冷藏。
	got, _ = d.Take(Dry, true)
	if !bytes.Equal(got, []byte("D1")) {
		t.Fatalf("dry prefers returned D1, got %s", got)
	}
}
