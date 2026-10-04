package matrix_test

import (
	"errors"
	"testing"

	"ontology/matrix"
)

type fakeGate struct{ empty bool }

func (g fakeGate) Empty() bool { return g.empty }

func TestSetChangeover(t *testing.T) {
	t.Run("asymmetric and default zero", func(t *testing.T) {
		m := matrix.New(fakeGate{empty: true})
		if err := m.SetChangeover("A", "B", 20); err != nil {
			t.Fatalf("A->B: %v", err)
		}
		if err := m.SetChangeover("B", "A", 15); err != nil {
			t.Fatalf("B->A: %v", err)
		}
		if got := m.Get("A", "B"); got != 20 {
			t.Errorf("Get(A,B)=%d, want 20", got)
		}
		if got := m.Get("B", "A"); got != 15 {
			t.Errorf("Get(B,A)=%d, want 15", got)
		}
		if got := m.Get("A", "C"); got != 0 {
			t.Errorf("unset pair=%d, want 0", got)
		}
		if got := m.Get("A", "A"); got != 0 {
			t.Errorf("same family=%d, want 0", got)
		}
	})

	cases := []struct {
		name    string
		a, b    string
		minutes int64
		want    error
	}{
		{"same family zero ok", "A", "A", 0, nil},
		{"same family nonzero", "A", "A", 1, matrix.ErrInvalidArgument},
		{"negative minutes", "A", "B", -1, matrix.ErrInvalidArgument},
		{"minutes over max", "A", "B", 100_001, matrix.ErrInvalidArgument},
		{"minutes at max ok", "A", "B", 100_000, nil},
		{"empty family", "", "B", 1, matrix.ErrInvalidArgument},
		{"33 byte family", "012345678901234567890123456789012", "B", 1, matrix.ErrInvalidArgument},
		{"32 byte family ok", "01234567890123456789012345678901", "B", 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := matrix.New(fakeGate{empty: true})
			err := m.SetChangeover(tc.a, tc.b, tc.minutes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}

	t.Run("state mismatch when non-empty", func(t *testing.T) {
		m := matrix.New(fakeGate{empty: false})
		err := m.SetChangeover("A", "B", 5)
		if !errors.Is(err, matrix.ErrInvalidState) {
			t.Fatalf("err=%v, want ErrInvalidState", err)
		}
	})

	t.Run("zero resets entry", func(t *testing.T) {
		m := matrix.New(fakeGate{empty: true})
		_ = m.SetChangeover("A", "B", 20)
		_ = m.SetChangeover("A", "B", 0)
		if got := m.Get("A", "B"); got != 0 {
			t.Fatalf("after reset=%d, want 0", got)
		}
	})
}
