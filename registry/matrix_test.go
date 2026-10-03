package registry

import (
	"errors"
	"testing"
)

func buildSituation(t *testing.T, sit string) (*Registry, int64) {
	t.Helper()
	r, _ := New(100, 1000)
	id, p := []byte("id"), []byte("owner")
	if sit == "Missing" {
		return r, 0
	}
	run, err := r.Start(id, p, 2, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sit == "Expired" {
		r.Finish(id, run, stCompleted, 10)
		return r, 0
	}
	if sit == "Running" {
		return r, run
	}
	if sit == "Terminated" {
		run2, err := r.Start(id, p, 2, 2, 20) // 终止 run1，新 run2
		if err != nil {
			t.Fatal(err)
		}
		r.Finish(id, run2, stFailed, 30) // 存活记录为失败类
		return r, run2
	}
	st := map[string]int{"Completed": stCompleted, "Failed": stFailed, "Cancelled": stCancelled}[sit]
	if err := r.Finish(id, run, st, 10); err != nil {
		t.Fatal(err)
	}
	return r, run
}

func wantOutcome(sit string, reuse, conflict int) (create, useExisting bool, err error) {
	if sit == "Running" {
		switch conflict {
		case 0:
			return false, false, ErrRunning
		case 1:
			return false, true, nil
		default:
			return true, false, nil
		}
	}
	if sit == "Completed" {
		if reuse == 0 {
			return true, false, nil
		}
		return false, false, ErrReuse
	}
	if sit == "Failed" || sit == "Cancelled" || sit == "Terminated" {
		if reuse == 2 {
			return false, false, ErrReuse
		}
		return true, false, nil
	}
	return true, false, nil // Expired/Missing
}

func TestStartMatrix(t *testing.T) {
	sits := []string{"Running", "Completed", "Failed", "Cancelled", "Terminated", "Expired", "Missing"}
	for _, sit := range sits {
		for reuse := 0; reuse <= 2; reuse++ {
			for conflict := 0; conflict <= 2; conflict++ {
				r, cur := buildSituation(t, sit)
				now := int64(60)
				if sit == "Expired" {
					now = 110
				}
				create, useExisting, wantErr := wantOutcome(sit, reuse, conflict)
				wantRun := cur
				if create {
					wantRun = r.nextRun + 1
				}
				got, err := r.Start([]byte("id"), []byte("owner"), reuse, conflict, now)
				t.Logf("现状=%s reuse=%d conflict=%d -> (%d,%v) 依据 Running看conflict/结束看reuse/无记录新建", sit, reuse, conflict, got, err)
				if !errors.Is(err, wantErr) || (err == nil && got != wantRun) || (useExisting && got != cur) {
					t.Fatalf("%s r=%d c=%d = (%d,%v) want run=%d err=%v", sit, reuse, conflict, got, err, wantRun, wantErr)
				}
			}
		}
	}
}
