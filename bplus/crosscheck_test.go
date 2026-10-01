package bplus

import (
	"fmt"
	"testing"
)

func TestNaiveBulkLoadCrossCheck(t *testing.T) {
	cases := []struct {
		leafCapacity     int
		internalCapacity int
		fillPercent      int
	}{
		{4, 4, 100}, {4, 4, 1}, {5, 4, 50}, {6, 5, 75},
		{7, 3, 100}, {8, 4, 50}, {9, 5, 37}, {10, 6, 1},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("C%d_B%d_p%d", tc.leafCapacity, tc.internalCapacity, tc.fillPercent)
		t.Run(name, func(t *testing.T) {
			leafMin := tc.leafCapacity / 2
			internalMin := (tc.internalCapacity + 1) / 2
			leafTarget := max((tc.leafCapacity*tc.fillPercent+99)/100, leafMin)
			internalTarget := max((tc.internalCapacity*tc.fillPercent+99)/100, internalMin)

			for n := 0; n <= 200; n++ {
				keys := testKeys(n)
				loader, err := NewBulkLoader(tc.leafCapacity, tc.internalCapacity, tc.fillPercent)
				if err != nil {
					t.Fatal(err)
				}
				chunk := 1 + (n*7)%11
				for start := 0; start < len(keys); start += chunk {
					end := min(start+chunk, len(keys))
					if err := loader.Add(keys[start:end]...); err != nil {
						t.Fatalf("n=%d Add: %v", n, err)
					}
				}
				if err := loader.Finish(); err != nil {
					t.Fatalf("n=%d Finish: %v", n, err)
				}

				actual := loader.Pages()
				expected := naiveBulkLoad(keys, leafTarget, tc.leafCapacity, internalTarget, tc.internalCapacity)
				if n%25 == 0 {
					t.Logf("input n=%d keys=%v output=%v basis=tL=%d/mL=%d,tI=%d/mI=%d naive=%v",
						n, keys, occupancies(actual), leafTarget, leafMin, internalTarget, internalMin, naiveOccupancies(expected))
				}
				assertTreeEqual(t, n, actual, expected, tc.leafCapacity, tc.internalCapacity, leafMin, internalMin)
				if height := loader.Height(); height != len(actual) {
					t.Fatalf("n=%d height=%d levels=%d", n, height, len(actual))
				}

				for index, key := range keys {
					result, err := loader.Get(key)
					if err != nil {
						t.Fatalf("Get(%q): %v", key, err)
					}
					if !result.Found || result.Accesses != len(actual) {
						t.Fatalf("n=%d key index=%d Get=%+v want found, accesses=%d", n, index, result, len(actual))
					}
				}
				for _, key := range []string{"000-missing", "999-missing", fmt.Sprintf("%03d-extra", n)} {
					result, err := loader.Get(key)
					if err != nil {
						t.Fatalf("Get(%q): %v", key, err)
					}
					if result.Found || result.Accesses != len(actual) {
						t.Fatalf("missing key %q Get=%+v want absent, accesses=%d", key, result, len(actual))
					}
				}
			}
		})
	}
}

func assertTreeEqual(t *testing.T, n int, actual [][]Page, expected naiveTree, leafCapacity, internalCapacity, leafMin, internalMin int) {
	t.Helper()
	assertOccupancy(t, actual, leafCapacity, internalCapacity, leafMin, internalMin)
	if len(actual) != len(expected.levels) {
		t.Fatalf("n=%d levels=%d want %d actual=%v expected=%v", n, len(actual), len(expected.levels), occupancies(actual), naiveOccupancies(expected))
	}
	for level := range actual {
		if len(actual[level]) != len(expected.levels[level]) {
			t.Fatalf("n=%d level %d pages=%d want %d", n, level, len(actual[level]), len(expected.levels[level]))
		}
		for pageIndex := range actual[level] {
			if fmt.Sprint(actual[level][pageIndex].Keys) != fmt.Sprint(expected.levels[level][pageIndex].keys) {
				t.Fatalf("n=%d level %d page %d keys=%v want %v actual=%#v expected=%#v", n, level, pageIndex,
					actual[level][pageIndex].Keys, expected.levels[level][pageIndex].keys, actual, expected.levels)
			}
			if fmt.Sprint(actual[level][pageIndex].Children) != fmt.Sprint(expected.levels[level][pageIndex].children) {
				t.Fatalf("n=%d level %d page %d children=%v want %v", n, level, pageIndex,
					actual[level][pageIndex].Children, expected.levels[level][pageIndex].children)
			}
		}
	}
}

func assertOccupancy(t *testing.T, levels [][]Page, leafCapacity, internalCapacity, leafMin, internalMin int) {
	t.Helper()
	if len(levels) == 0 {
		t.Fatal("tree has no levels")
	}
	for levelIndex, level := range levels {
		if len(level) == 0 {
			t.Fatalf("level %d has no pages", levelIndex)
		}
		capacity := leafCapacity
		minimum := leafMin
		if levelIndex > 0 {
			capacity = internalCapacity
			minimum = internalMin
		}
		for pageIndex, page := range level {
			size := len(page.Keys)
			if levelIndex > 0 {
				size = len(page.Children)
			}
			isSingleRoot := levelIndex == len(levels)-1 && len(level) == 1
			if size < 0 || size > capacity || (!isSingleRoot && pageIndex < len(level)-1 && size < minimum) {
				t.Fatalf("level %d page %d size=%d outside [%d,%d]", levelIndex, pageIndex, size, minimum, capacity)
			}
		}
	}
}

func testKeys(n int) []string {
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = fmt.Sprintf("%03d", i)
	}
	return keys
}

func occupancies(levels [][]Page) [][]int {
	result := make([][]int, len(levels))
	for levelIndex, level := range levels {
		for _, page := range level {
			size := len(page.Keys)
			if levelIndex > 0 {
				size = len(page.Children)
			}
			result[levelIndex] = append(result[levelIndex], size)
		}
	}
	return result
}

func naiveOccupancies(tree naiveTree) [][]int {
	result := make([][]int, len(tree.levels))
	for levelIndex, level := range tree.levels {
		for _, page := range level {
			size := len(page.keys)
			if levelIndex > 0 {
				size = len(page.children)
			}
			result[levelIndex] = append(result[levelIndex], size)
		}
	}
	return result
}
