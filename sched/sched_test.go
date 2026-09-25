package sched

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"ontology/admit"
)

func drain(s *Scheduler, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		tk, err := s.Next()
		if err != nil {
			break
		}
		out = append(out, tk.Tenant)
	}
	return out
}

// TestFairShare 表驱动：不同权重比下长期执行次数比例必须收敛到权重比。
func TestFairShare(t *testing.T) {
	cases := []struct {
		name    string
		weights map[string]float64
		tasks   int
		tol     float64
	}{
		{"1:3", map[string]float64{"A": 1, "B": 3}, 6000, 0.05},
		{"1:3:6", map[string]float64{"A": 1, "B": 3, "C": 6}, 100000, 0.05},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(nil)
			var totalW float64
			for id, w := range tc.weights {
				if err := s.AddTenant(id, w, 0); err != nil {
					t.Fatal(err)
				}
				totalW += w
			}
			ids := make([]string, 0, len(tc.weights))
			for id := range tc.weights {
				ids = append(ids, id)
			}
			for i := 0; i < tc.tasks; i++ {
				if _, err := s.Submit(ids[i%len(ids)], 1); err != nil {
					t.Fatal(err)
				}
			}
			drain(s, tc.tasks)
			snap := s.Snapshot()
			dev, total := snap.ShareDeviations()
			for _, id := range ids {
				if got := dev[id]; got >= tc.tol {
					want := tc.weights[id] / totalW
					t.Fatalf("%s: tenant %s deviation %.4f >= %.2f (want share %.4f, cost %.1f/%.1f)",
						tc.name, id, got, tc.tol, want, row(snap, id).Cost, total)
				}
			}
			for _, r := range snap.Rows {
				t.Logf("%s share tenant=%s weight=%.0f runs=%d cost=%.0f wantShare=%.4f dev=%.5f",
					tc.name, r.ID, r.Weight, r.Runs, r.Cost, r.Weight/totalW, dev[r.ID])
			}
		})
	}
}

func row(sn interface {
	Rows() []struct{}
}, id string) struct{} { panic("unused") }
