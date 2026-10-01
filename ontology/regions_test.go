package ontology

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func regionIDs(regions []Region) []string {
	ids := make([]string, 0, len(regions))
	for _, region := range regions {
		ids = append(ids, region.ID)
	}
	return ids
}

func TestBoundaryAndHoleOwnership(t *testing.T) {
	outer := []Point{{0, 0}, {8, 0}, {8, 8}, {0, 8}}
	hole := []Point{{3, 3}, {5, 3}, {5, 5}, {3, 5}}
	region := Region{ID: "r", Priority: 1, Outer: outer, Hole: hole}
	set := NewRegionSet()
	if err := set.Put(region); err != nil {
		t.Fatalf("Put(%+v): %v", region, err)
	}

	tests := []struct {
		name   string
		point  Point
		reason string
		want   bool
	}{
		{"outer vertex", Point{8, 8}, "outer closed boundary", true},
		{"outer edge", Point{0, 4}, "outer closed boundary", true},
		{"hole vertex", Point{3, 3}, "hole boundary remains in region", true},
		{"hole edge", Point{4, 5}, "hole boundary remains in region", true},
		{"hole interior", Point{4, 4}, "hole open interior is excluded", false},
		{"outside", Point{9, 4}, "outside outer closed region", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := set.Best(tt.point.X, tt.point.Y)
			if err != nil {
				t.Fatalf("Best(%d,%d): %v", tt.point.X, tt.point.Y, err)
			}
			t.Logf("input=(%d,%d) reason=%q output=(id=%q,hit=%v)", tt.point.X, tt.point.Y, tt.reason, got.ID, ok)
			if ok != tt.want {
				t.Fatalf("Best hit = %v, want %v (%s)", ok, tt.want, tt.reason)
			}
		})
	}
}

func TestClockwiseOuterRing(t *testing.T) {
	region := Region{
		ID:       "cw",
		Priority: 1,
		Outer:    []Point{{0, 0}, {0, 6}, {6, 6}, {6, 0}},
	}
	set := NewRegionSet()
	if err := set.Put(region); err != nil {
		t.Fatalf("Put clockwise region: %v", err)
	}

	best, ok, err := set.Best(3, 3)
	if err != nil || !ok || best.ID != "cw" {
		t.Fatalf("clockwise interior output=(%q,%v,%v)", best.ID, ok, err)
	}
	t.Logf("input=clockwise square (3,3) output=%q basis=all edge cross products have the polygon orientation sign", best.ID)
}

func TestLocateOrderAndSharedBoundary(t *testing.T) {
	regions := []Region{
		{ID: "c-id", Priority: 5, Outer: []Point{{0, 0}, {4, 0}, {4, 4}, {0, 4}}},
		{ID: "a-id", Priority: 5, Outer: []Point{{0, 0}, {0, 4}, {-4, 4}, {-4, 0}}},
		{ID: "low", Priority: 1, Outer: []Point{{0, 0}, {4, 0}, {4, 4}, {0, 4}}},
	}
	set := NewRegionSet()
	for _, region := range regions {
		if err := set.Put(region); err != nil {
			t.Fatalf("Put(%s): %v", region.ID, err)
		}
	}

	matches, err := set.Locate(0, 2)
	if err != nil {
		t.Fatalf("Locate shared boundary: %v", err)
	}
	gotIDs := regionIDs(matches)
	wantIDs := []string{"a-id", "c-id", "low"}
	t.Logf("input=(0,2) output=%v basis=shared boundary in both; priority desc then id asc", gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("order = %v, want %v", gotIDs, wantIDs)
	}
}

func TestValidationRejectionsAndCheckOrder(t *testing.T) {
	validOuter := []Point{{0, 0}, {8, 0}, {8, 8}, {0, 8}}
	star := []Point{{4, 9}, {9, 0}, {0, 6}, {8, 6}, {-1, 0}}
	tests := []struct {
		name    string
		region  Region
		wantErr error
		reason  string
	}{
		{
			name:    "coordinate out of range",
			region:  Region{ID: "bad", Outer: []Point{{0, 0}, {1_000_000_001, 0}, {0, 4}}},
			wantErr: ErrCoordinateOutOfRange,
			reason:  "absolute coordinate exceeds 1e9 before geometric checks",
		},
		{
			name:    "too few outer vertices",
			region:  Region{ID: "bad", Outer: []Point{{0, 0}, {4, 0}}},
			wantErr: ErrTooFewVertices,
			reason:  "outer has fewer than 3 vertices",
		},
		{
			name:    "too few hole vertices",
			region:  Region{ID: "bad", Outer: validOuter, Hole: []Point{{1, 1}, {2, 1}}},
			wantErr: ErrTooFewVertices,
			reason:  "hole has fewer than 3 vertices",
		},
		{
			name:    "outer not convex",
			region:  Region{ID: "bad", Outer: []Point{{0, 0}, {4, 0}, {4, 4}, {2, 1}, {0, 4}}},
			wantErr: ErrOuterNotConvex,
			reason:  "consecutive turns do not have one strict sign",
		},
		{
			name:    "hole not convex",
			region:  Region{ID: "bad", Outer: validOuter, Hole: []Point{{1, 1}, {7, 1}, {7, 7}, {2, 7}, {2, 2}}},
			wantErr: ErrHoleNotConvex,
			reason:  "hole turns do not have one strict sign",
		},
		{
			name:    "star winds twice",
			region:  Region{ID: "bad", Outer: star},
			wantErr: ErrOuterNotConvex,
			reason:  "pentagram traversal self-intersects and winds twice",
		},
		{
			name: "hole vertex on outer edge",
			region: Region{
				ID:    "bad",
				Outer: validOuter,
				Hole:  []Point{{2, 0}, {6, 2}, {2, 4}},
			},
			wantErr: ErrHoleOutsideOuter,
			reason:  "hole vertex is on, not strictly inside, outer boundary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := NewRegionSet()
			err := set.Put(tt.region)
			t.Logf("input=%s output=%v reason=%q", tt.name, err, tt.reason)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Put error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	t.Run("geometric checks precede duplicate id", func(t *testing.T) {
		set := NewRegionSet()
		original := Region{ID: "same", Priority: 1, Outer: validOuter}
		if err := set.Put(original); err != nil {
			t.Fatalf("Put original: %v", err)
		}
		invalidDuplicate := Region{ID: "same", Outer: []Point{{0, 0}, {1_000_000_001, 0}, {0, 4}}}
		err := set.Put(invalidDuplicate)
		t.Logf("input=duplicate id with invalid geometry output=%v reason=coordinate check precedes id check", err)
		if !errors.Is(err, ErrCoordinateOutOfRange) {
			t.Fatalf("error = %v, want %v", err, ErrCoordinateOutOfRange)
		}
	})
}

func TestPutReplaceRemoveErrors(t *testing.T) {
	region := Region{ID: "r", Outer: []Point{{0, 0}, {4, 0}, {0, 4}}}
	set := NewRegionSet()

	if err := set.Put(region); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := set.Put(region); !errors.Is(err, ErrRegionExists) {
		t.Fatalf("duplicate Put error = %v, want %v", err, ErrRegionExists)
	}

	missing := Region{ID: "missing", Outer: []Point{{0, 0}, {4, 0}, {0, 4}}}
	if err := set.Replace(missing); !errors.Is(err, ErrRegionNotFound) {
		t.Fatalf("Replace missing error = %v, want %v", err, ErrRegionNotFound)
	}
	if err := set.Remove("missing"); !errors.Is(err, ErrRegionNotFound) {
		t.Fatalf("Remove missing error = %v, want %v", err, ErrRegionNotFound)
	}

	replacement := Region{ID: "r", Priority: 9, Outer: []Point{{10, 10}, {14, 10}, {10, 14}}}
	if err := set.Replace(replacement); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, ok, err := set.Best(1, 1); err != nil || ok {
		t.Fatalf("old point after replacement hit=%v err=%v", ok, err)
	}
	best, ok, err := set.Best(11, 11)
	if err != nil || !ok || best.ID != "r" || best.Priority != 9 {
		t.Fatalf("new region output=(id=%q priority=%d hit=%v err=%v)", best.ID, best.Priority, ok, err)
	}
	t.Logf("input=Replace output=old points miss and new points hit basis=single map assignment under write lock")
}

func TestFailedReplaceLeavesOriginal(t *testing.T) {
	original := Region{ID: "keep", Priority: 3, Outer: []Point{{0, 0}, {6, 0}, {6, 6}, {0, 6}}}
	set := NewRegionSet()
	if err := set.Put(original); err != nil {
		t.Fatalf("Put original: %v", err)
	}

	invalid := Region{
		ID:    "keep",
		Outer: []Point{{0, 0}, {6, 0}, {6, 6}, {0, 6}},
		Hole:  []Point{{2, 2}, {8, 2}, {2, 8}},
	}
	err := set.Replace(invalid)
	t.Logf("input=invalid Replace output=%v reason=rejection before mutation", err)
	if !errors.Is(err, ErrHoleOutsideOuter) {
		if err == nil {
			t.Fatal("invalid Replace unexpectedly succeeded")
		}
	}

	matches, err := set.Locate(2, 2)
	if err != nil {
		t.Fatalf("Locate original: %v", err)
	}
	if len(matches) != 1 || matches[0].ID != "keep" || matches[0].Priority != 3 {
		t.Fatalf("original matches = %+v, want unchanged keep/priority=3", matches)
	}
	t.Logf("input=(2,2) output=%v basis=original outer still present", regionIDs(matches))
}

func TestLocateRejectsOutOfRangeQuery(t *testing.T) {
	set := NewRegionSet()
	_, err := set.Locate(1_000_000_001, 0)
	if !errors.Is(err, ErrCoordinateOutOfRange) {
		t.Fatalf("Locate error = %v, want %v", err, ErrCoordinateOutOfRange)
	}
	t.Logf("input=(1000000001,0) output=%v reason=query uses same coordinate bound", err)
}

func TestMatchesNaiveCrossProductOnSmallGrid(t *testing.T) {
	regions := []Region{
		{
			ID:       "a",
			Priority: 2,
			Outer:    []Point{{-6, -6}, {2, -6}, {2, 2}, {-6, 2}},
			Hole:     []Point{{-4, -4}, {-1, -4}, {-1, -1}, {-4, -1}},
		},
		{
			ID:       "b",
			Priority: 5,
			Outer:    []Point{{0, 0}, {6, 0}, {6, 6}, {0, 6}},
		},
		{
			ID:       "c",
			Priority: 5,
			Outer:    []Point{{-2, -2}, {4, -2}, {4, 4}, {-2, 4}},
		},
	}
	set := NewRegionSet()
	for _, region := range regions {
		if err := set.Put(region); err != nil {
			t.Fatalf("Put(%s): %v", region.ID, err)
		}
	}

	for x := -7; x <= 7; x++ {
		for y := -7; y <= 7; y++ {
			query := Point{int64(x), int64(y)}
			got, err := set.Locate(query.X, query.Y)
			if err != nil {
				t.Fatalf("Locate(%d,%d): %v", x, y, err)
			}
			want := naiveLocate(regions, query)
			gotIDs := regionIDs(got)
			wantIDs := regionIDs(want)
			if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
				t.Fatalf("Locate(%d,%d) = %v, naive = %v", x, y, gotIDs, wantIDs)
			}
			t.Logf("input=(%d,%d) output=%v basis=outer cross signs allow zero; hole open interior requires strict same signs", x, y, gotIDs)
		}
	}
}

func TestConcurrentReadsSeeAtomicReplacement(t *testing.T) {
	oldRegion := Region{
		ID:       "moving",
		Priority: 1,
		Outer:    []Point{{0, 0}, {4, 0}, {4, 4}, {0, 4}},
	}
	newRegion := Region{
		ID:       "moving",
		Priority: 2,
		Outer:    []Point{{1, 1}, {3, 1}, {3, 3}, {1, 3}},
	}
	set := NewRegionSet()
	if err := set.Put(oldRegion); err != nil {
		t.Fatalf("Put old: %v", err)
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	var replacementErr error
	for reader := 0; reader < 8; reader++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}

				matches, err := set.Locate(2, 2)
				if err != nil {
					t.Errorf("Locate common point: %v", err)
					return
				}
				if len(matches) != 1 {
					t.Errorf("atomic snapshot=%v", regionIDs(matches))
					return
				}
				current := matches
				if current[0].ID != "moving" || (current[0].Priority != 1 && current[0].Priority != 2) {
					t.Errorf("unexpected snapshot %+v", current[0])
					return
				}
				if (current[0].Priority == 1 && !naiveClosedConvexContains(Point{2, 2}, oldRegion.Outer)) ||
					(current[0].Priority == 2 && !naiveClosedConvexContains(Point{2, 2}, newRegion.Outer)) {
					t.Errorf("snapshot point outside returned version %+v", current[0])
					return
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		if i%2 == 0 {
			if replacementErr = set.Replace(newRegion); replacementErr != nil {
				break
			}
		} else {
			if replacementErr = set.Replace(oldRegion); replacementErr != nil {
				break
			}
		}
	}
	close(stop)
	readers.Wait()
	if replacementErr != nil {
		t.Fatalf("Replace: %v", replacementErr)
	}
	t.Logf("input=concurrent Replace/Locate output=one snapshot state each read basis=RWMutex plus single map assignment")
}

func naiveLocate(regions []Region, p Point) []Region {
	matches := make([]Region, 0)
	for _, region := range regions {
		if naiveClosedConvexContains(p, region.Outer) &&
			(len(region.Hole) == 0 || !naiveStrictConvexContains(p, region.Hole)) {
			matches = append(matches, region)
		}
	}

	for i := 0; i < len(matches); i++ {
		for j := i + 1; j < len(matches); j++ {
			if matches[j].Priority > matches[i].Priority ||
				(matches[j].Priority == matches[i].Priority && matches[j].ID < matches[i].ID) {
				matches[i], matches[j] = matches[j], matches[i]
			}
		}
	}
	return matches
}

func naiveClosedConvexContains(p Point, polygon []Point) bool {
	return crossSignsMatch(p, polygon, true)
}

func naiveStrictConvexContains(p Point, polygon []Point) bool {
	return crossSignsMatch(p, polygon, false)
}

func crossSignsMatch(p Point, polygon []Point, allowZero bool) bool {
	var sign int
	for i, start := range polygon {
		end := polygon[(i+1)%len(polygon)]
		value := cross(start, end, p)
		if value == 0 {
			if !allowZero {
				return false
			}
			continue
		}

		current := 1
		if value < 0 {
			current = -1
		}
		if sign == 0 {
			sign = current
		} else if sign != current {
			return false
		}
	}
	return true
}
