package rescale

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/kgrp"
)

// keysIn finds n keys hashing into allowed groups (nil = all groups).
func keysIn(n int, allow map[int]bool, maxP int, pref string) []string {
	out := make([]string, 0, n)
	for i := 0; len(out) < n; i++ {
		k := fmt.Sprintf("%s%d", pref, i)
		if g, e := kgrp.KeyGroup(k, maxP); e == nil && (allow == nil || allow[g]) {
			out = append(out, k)
		}
	}
	return out
}

// naiveRanges merges per-group inst(kg,p) into per-instance runs.
func naiveRanges(maxP, p int) [][2]int {
	out, start := [][2]int{}, 0
	for kg := 1; kg <= maxP; kg++ {
		if kg == maxP || kgrp.Owner(kg, p, maxP) != kgrp.Owner(start, p, maxP) {
			out, start = append(out, [2]int{start, kg}), kg
		}
	}
	return out
}

// TestRangesMatchNaive pins I1 (ranges == naive merge) and I2 (partition).
func TestRangesMatchNaive(t *testing.T) {
	for mp := 1; mp <= 64; mp++ {
		for p := 1; p <= mp; p++ {
			s, e := New(mp, p)
			if e != nil {
				t.Fatal(e)
			}
			r := s.Ranges()
			if !reflect.DeepEqual(r, naiveRanges(mp, p)) || r[0][0] != 0 || r[p-1][1] != mp {
				t.Fatalf("mp=%d p=%d bad ranges %v", mp, p, r)
			}
			minL, maxL := mp, 0
			for i, iv := range r {
				l := iv[1] - iv[0]
				if i > 0 && iv[0] != r[i-1][1] {
					t.Fatalf("mp=%d p=%d gap %v", mp, p, r)
				}
				if l < minL {
					minL = l
				} else if l > maxL {
					maxL = l
				}
			}
			if maxL-minL > 1 {
				t.Fatalf("mp=%d p=%d lengths %d..%d", mp, p, minL, maxL)
			}
		}
	}
}

// TestOwnerAndPlacement pins I1 for keys: formula Owner and physical bucket.
func TestOwnerAndPlacement(t *testing.T) {
	for _, mp := range []int{3, 7, 10, 13, 64} {
		s, _ := New(mp, 3)
		for i, k := range keysIn(40, nil, mp, "q") {
			if e := s.Put(k, int64(i)); e != nil {
				t.Fatal(e)
			}
			g, _ := kgrp.KeyGroup(k, mp)
			want := kgrp.Owner(g, 3, mp)
			got, e := s.Owner(k)
			if _, placed := s.inst[want][g][k]; e != nil || got != want || !placed {
				t.Fatalf("%s owner=%d want %d or misplaced", k, got, want)
			}
		}
	}
}

// TestRescaleConservationAndMinimal pins I3: conservation, exact moved count, untouched buckets, plan sources/order.
func TestRescaleConservationAndMinimal(t *testing.T) {
	for _, c := range [][3]int{{10, 3, 4}, {10, 4, 3}, {13, 1, 13}, {13, 13, 1}, {32, 5, 7}, {64, 8, 3}} {
		mp, p1, p2 := c[0], c[1], c[2]
		s, _ := New(mp, p1)
		keys := keysIn(200, nil, mp, "w")
		for i, k := range keys {
			if e := s.Put(k, int64(i+1)); e != nil {
				t.Fatal(e)
			}
		}
		moving, wantMoved, stayPtr := map[int]bool{}, 0, map[int]uintptr{}
		for kg := 0; kg < mp; kg++ {
			o1, o2 := kgrp.Owner(kg, p1, mp), kgrp.Owner(kg, p2, mp)
			b := s.inst[o1][kg]
			if o1 != o2 {
				moving[kg], wantMoved = true, wantMoved+len(b)
			} else if b != nil {
				stayPtr[kg] = reflect.ValueOf(b).Pointer()
			}
		}
		pl, e := s.Rescale(p2)
		if e != nil || pl.MovedKeys != wantMoved || s.P() != p2 || s.Check() != nil {
			t.Fatalf("case %v moved=%d want %d err=%v", c, pl.MovedKeys, wantMoved, e)
		}
		for i, k := range keys {
			if v, ok := s.Get(k); !ok || v != int64(i+1) {
				t.Fatalf("case %v lost %s", c, k)
			}
		}
		for kg, ptr := range stayPtr {
			o := kgrp.Owner(kg, p2, mp)
			if reflect.ValueOf(s.inst[o][kg]).Pointer() != ptr {
				t.Fatalf("case %v non-moving group %d rebuilt", c, kg)
			}
		}
		inc, prev := 0, -1
		for _, ip := range pl.Instances {
			for _, m := range ip.Incoming {
				if !moving[m.KeyGroup] || m.From != kgrp.Owner(m.KeyGroup, p1, mp) || m.KeyGroup <= prev {
					t.Fatalf("case %v bad incoming %+v", c, m)
				}
				inc, prev = inc+1, m.KeyGroup
			}
		}
		if inc != len(moving) {
			t.Fatalf("case %v incoming %d want %d", c, inc, len(moving))
		}
	}
}

// TestVisitedCounterSublinear proves bucket relocation: counter == moved count regardless of m.
func TestVisitedCounterSublinear(t *testing.T) {
	stay := map[int]bool{0: true, 1: true, 2: true, 4: true, 7: true}
	for _, m := range []int{100, 1000, 10000} {
		s, _ := New(10, 3)
		for i, k := range keysIn(m, stay, 10, "z") {
			_ = s.Put(k, int64(i))
		}
		mk := keysIn(2, map[int]bool{3: true, 6: true}, 10, "m")
		_ = s.Put(mk[0], 1)
		_ = s.Put(mk[1], 2)
		if pl, e := s.Rescale(4); e != nil || pl.MovedKeys != 2 || s.lastVisited != 2 {
			t.Fatalf("m=%d moved=%d visited=%d want 2,2", m, pl.MovedKeys, s.lastVisited)
		}
	}
}
