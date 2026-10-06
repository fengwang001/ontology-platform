package slotting

import (
	"io"
	"testing"
)

func mustLoc(t *testing.T, s *System, l Location) {
	t.Helper()
	if err := s.AddLocation(l); err != nil {
		t.Fatalf("AddLocation %s: %v", l.ID, err)
	}
}

func mustPal(t *testing.T, s *System, p Pallet) {
	t.Helper()
	if err := s.RegisterPallet(p); err != nil {
		t.Fatalf("RegisterPallet %s: %v", p.ID, err)
	}
}

func cats(cs ...Category) []Category { return cs }

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	if e, ok := AsAllocationError(err); ok {
		return e.Reason()
	}
	return Reason("other:" + err.Error())
}

func locOf(id LocationID, w, h, cap int, mix bool, allowed ...Category) Location {
	return Location{ID: id, MaxWeight: w, ClearHeight: h, Allowed: allowed,
		Capacity: cap, AllowMix: mix, Status: StatusNormal}
}

func pallet(id, prod, batch string, c Category, w, h int) Pallet {
	return Pallet{ID: id, Product: prod, Batch: batch, Category: c, Weight: w, Height: h}
}

var _ io.Writer = (*sliceWriter)(nil)

type sliceWriter struct{ b []byte }

func (w *sliceWriter) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}

// assertSnapshotsEqual 比较 System 与朴素模型的全部在位关系。
func assertSnapshotsEqual(t *testing.T, s *System, m *naiveModel) {
	t.Helper()
	for pid, want := range m.where {
		got, err := s.PalletLocation(pid)
		if err != nil {
			t.Fatalf("托盘 %s 朴素模型在位于 %s，系统查询失败: %v", pid, want, err)
		}
		if got != want {
			t.Fatalf("托盘 %s 系统=%s 朴素=%s", pid, got, want)
		}
	}
	for id, ls := range m.locs {
		v, err := s.GetLocation(id)
		if err != nil {
			t.Fatalf("货位 %s 查询失败: %v", id, err)
		}
		if v.UsedWeight != ls.weight {
			t.Fatalf("货位 %s 重量 系统=%d 朴素=%d", id, v.UsedWeight, ls.weight)
		}
		if len(v.PalletIDs) != len(ls.pallets) {
			t.Fatalf("货位 %s 托盘数 系统=%v 朴素=%v", id, v.PalletIDs, ls.pallets)
		}
		gotSet := map[string]bool{}
		for _, p := range v.PalletIDs {
			gotSet[p] = true
		}
		for _, p := range ls.pallets {
			if !gotSet[p] {
				t.Fatalf("货位 %s 缺少托盘 %s", id, p)
			}
		}
	}
}
