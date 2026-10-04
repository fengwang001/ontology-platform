package quota

import (
	"errors"
	"testing"

	"ontology/calendar"
)

func TestManager(t *testing.T) {
	cases := []struct {
		name   string
		lw, lh int64
		ok     bool
	}{
		{"normal", 3600, 7200, true},
		{"zero quotas", 0, 0, true},
		{"max quotas", 86400, 86400, true},
		{"lw negative", -1, 10, false},
		{"lh over max", 10, 86401, false},
	}
	c, _ := calendar.New(0, 0, 0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := New(c, tc.lw, tc.lh)
			if tc.ok {
				if err != nil || m == nil {
					t.Fatalf("New err=%v", err)
				}
			} else if !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("err=%v want ErrInvalidParam", err)
			}
		})
	}

	if err := c.SetHoliday(0, 1, true); err != nil {
		t.Fatal(err)
	}
	m, err := New(c, 3600, 7200)
	if err != nil {
		t.Fatal(err)
	}
	if m.Limit(0) != 3600 || m.Limit(1) != 7200 {
		t.Fatalf("limits wrong: %d %d", m.Limit(0), m.Limit(1))
	}
	if err := m.Add(0, 3600); err != nil {
		t.Fatalf("fill day 0: %v", err)
	}
	if err := m.Add(0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("overflow: err=%v", err)
	}
	if err := m.Add(1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("negative add: err=%v", err)
	}
	if m.Used(0) != 3600 || m.Remaining(0) != 0 {
		t.Fatalf("day0 used/remaining wrong: %d %d", m.Used(0), m.Remaining(0))
	}
	if m.Remaining(86400) != 7200 {
		t.Fatalf("holiday remaining wrong: %d", m.Remaining(86400))
	}
	if m.Used(99) != 0 {
		t.Fatal("untouched day used must be 0")
	}
}

func TestZeroQuotaDay(t *testing.T) {
	c, _ := calendar.New(0, 0, 0)
	m, _ := New(c, 0, 10)
	if m.Limit(0) != 0 || m.Remaining(0) != 0 {
		t.Fatal("zero-quota day remaining should be 0")
	}
	if err := m.Add(0, 0); err != nil {
		t.Fatalf("add zero on zero-quota day: %v", err)
	}
}
