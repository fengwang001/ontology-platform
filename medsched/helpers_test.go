package medsched

import (
	"errors"
	"testing"
)

func codeOf(err error) ErrorCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func mustSys(t *testing.T, w int64) *System {
	t.Helper()
	s, err := New(w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func regDrug(t *testing.T, s *System, drug, cat string, min int64) {
	t.Helper()
	if err := s.RegisterDrug(0, drug, cat, min); err != nil {
		t.Fatalf("RegisterDrug %s: %v", drug, err)
	}
}

func intervalFreq(first, h int64) Frequency {
	return Frequency{Kind: FreqInterval, First: first, H: h}
}

func openInterval(t *testing.T, s *System, now int64, patient, drug string, first, h int64) string {
	t.Helper()
	id, err := s.OpenOrder(OpenOrderInput{Now: now, Patient: patient, Drug: drug, Frequency: intervalFreq(first, h)})
	if err != nil {
		t.Fatalf("OpenOrder: %v", err)
	}
	return id
}

func statusAt(pts []Point, planned int64) PointStatus {
	for _, p := range pts {
		if p.Planned == planned {
			return p.Status
		}
	}
	return 0
}
