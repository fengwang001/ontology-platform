package ontology

import (
	"fmt"
	"math/bits"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"
)

type modelRecord struct {
	key  string
	size int
}

type modelPage struct {
	id       int
	records  []modelRecord
	occupied int
}

type naiveTree struct {
	capacity  int
	pageLimit int
	pages     []*modelPage
	nextPage  int
}

func newModelTree(capacity int, pageLimit int) (*ByteBPlusTree, error) {
	return NewByteBPlusTree(capacity, pageLimit)
}

func newNaiveTree(capacity int, pageLimit int) *naiveTree {
	return &naiveTree{
		capacity:  capacity,
		pageLimit: pageLimit,
		pages:     []*modelPage{{id: 1}},
		nextPage:  2,
	}
}

func (m *naiveTree) locate(key string) int {
	left := 0
	right := len(m.pages)
	for right-left > 1 {
		middle := left + (right-left)/2
		if m.pages[middle].records[0].key <= key {
			left = middle
		} else {
			right = middle
		}
	}
	return left
}

func (m *naiveTree) insert(key string, size int) error {
	if key == "" || size < 1 || size > m.capacity/4 {
		return ErrInvalidParameter
	}
	index := m.locate(key)
	page := m.pages[index]
	recordIndex, found := modelFind(page, key)
	if found {
		return ErrDuplicateKey
	}
	if page.occupied+size > m.capacity && len(m.pages) == m.pageLimit {
		return ErrPageLimit
	}

	page.records = slices.Insert(page.records, recordIndex, modelRecord{key: key, size: size})
	page.occupied += size
	if page.occupied <= m.capacity {
		return nil
	}

	total := page.occupied
	bestIndex := 1
	bestDifference := total + 1
	for splitIndex := 1; splitIndex < len(page.records); splitIndex++ {
		leftOccupied := modelSum(page.records[:splitIndex])
		difference := leftOccupied - (total - leftOccupied)
		if difference < 0 {
			difference = -difference
		}
		if difference < bestDifference {
			bestDifference = difference
			bestIndex = splitIndex
		}
	}

	right := &modelPage{
		id:       m.nextPage,
		records:  slices.Clone(page.records[bestIndex:]),
		occupied: modelSum(page.records[bestIndex:]),
	}
	m.nextPage++
	page.records = slices.Clone(page.records[:bestIndex])
	page.occupied = modelSum(page.records)
	m.pages = slices.Insert(m.pages, index+1, right)
	return nil
}

func (m *naiveTree) delete(key string) error {
	if key == "" {
		return ErrInvalidParameter
	}
	index := m.locate(key)
	page := m.pages[index]
	recordIndex, found := modelFind(page, key)
	if !found {
		return ErrKeyNotFound
	}

	page.occupied -= page.records[recordIndex].size
	page.records = slices.Delete(page.records, recordIndex, recordIndex+1)
	if len(m.pages) == 1 || page.occupied >= (m.capacity+1)/2 {
		return nil
	}
	m.rebalance(index)
	return nil
}

func (m *naiveTree) rebalance(index int) {
	if m.borrowLeft(index) || m.borrowRight(index) || m.mergeLeft(index) {
		return
	}
	_ = m.mergeRight(index)
}

func (m *naiveTree) borrowLeft(index int) bool {
	if index == 0 {
		return false
	}
	minimum := (m.capacity + 1) / 2
	left := m.pages[index-1]
	underflow := m.pages[index]
	for count := 1; count <= len(left.records); count++ {
		movedSize := modelSum(left.records[len(left.records)-count:])
		if underflow.occupied+movedSize >= minimum && left.occupied-movedSize >= minimum {
			underflow.records = append(slices.Clone(left.records[len(left.records)-count:]), underflow.records...)
			underflow.occupied += movedSize
			left.records = slices.Delete(left.records, len(left.records)-count, len(left.records))
			left.occupied -= movedSize
			return true
		}
	}
	return false
}

func (m *naiveTree) borrowRight(index int) bool {
	if index == len(m.pages)-1 {
		return false
	}
	minimum := (m.capacity + 1) / 2
	underflow := m.pages[index]
	right := m.pages[index+1]
	for count := 1; count <= len(right.records); count++ {
		movedSize := modelSum(right.records[:count])
		if underflow.occupied+movedSize >= minimum && right.occupied-movedSize >= minimum {
			underflow.records = append(underflow.records, slices.Clone(right.records[:count])...)
			underflow.occupied += movedSize
			right.records = slices.Delete(right.records, 0, count)
			right.occupied -= movedSize
			return true
		}
	}
	return false
}

func (m *naiveTree) mergeLeft(index int) bool {
	if index == 0 {
		return false
	}
	left := m.pages[index-1]
	underflow := m.pages[index]
	if left.occupied+underflow.occupied > m.capacity {
		return false
	}
	left.records = append(left.records, underflow.records...)
	left.occupied += underflow.occupied
	m.pages = slices.Delete(m.pages, index, index+1)
	return true
}

func (m *naiveTree) mergeRight(index int) bool {
	if index == len(m.pages)-1 {
		return false
	}
	underflow := m.pages[index]
	right := m.pages[index+1]
	if underflow.occupied+right.occupied > m.capacity {
		return false
	}
	underflow.records = append(underflow.records, right.records...)
	underflow.occupied += right.occupied
	m.pages = slices.Delete(m.pages, index+1, index+2)
	return true
}

func (m *naiveTree) pagesSnapshot() []Page {
	result := make([]Page, len(m.pages))
	for i, page := range m.pages {
		keys := make([]string, len(page.records))
		for j, item := range page.records {
			keys[j] = item.key
		}
		result[i] = Page{ID: page.id, Keys: keys, Occupied: page.occupied}
	}
	return result
}

func modelFind(page *modelPage, key string) (int, bool) {
	for i, item := range page.records {
		if item.key == key {
			return i, true
		}
	}
	insertion := 0
	for insertion < len(page.records) && page.records[insertion].key < key {
		insertion++
	}
	return insertion, false
}

func modelSum(records []modelRecord) int {
	total := 0
	for _, item := range records {
		total += item.size
	}
	return total
}

func TestSkeleton(t *testing.T) {
	if _, err := newModelTree(100, 10); err != nil {
		t.Fatal(err)
	}
}

func TestSplitTieUsesSmallerPosition(t *testing.T) {
	tree, err := newModelTree(24, 10)
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if err := tree.Insert(key, 6); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}

	pages := tree.Pages()
	got := flattenPageKeys(pages)
	want := [][]string{{"a", "b"}, {"c", "d", "e"}}
	t.Logf("input=insert a:6,b:6,c:6,d:6,e:6 with C=24; output=%v; reason=positions 2 and 3 both have byte difference 6, so smaller j=2", got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
}

func TestLeftBorrowTakesMinimumRecordsAndAllowsLenderAtM(t *testing.T) {
	tree, err := newModelTree(24, 10)
	if err != nil {
		t.Fatal(err)
	}
	tree.pages[0] = &leafPage{
		id: 1,
		records: []record{
			{key: "a", size: 6},
			{key: "b", size: 6},
			{key: "c", size: 2},
			{key: "d", size: 1},
		},
		occupied: 15,
	}
	tree.pages = append(tree.pages, &leafPage{
		id: 2,
		records: []record{
			{key: "e", size: 10},
			{key: "f", size: 2},
		},
		occupied: 12,
	})
	tree.nextPage = 3

	if err := tree.Delete("f"); err != nil {
		t.Fatal(err)
	}

	pages := tree.Pages()
	got := flattenPageKeys(pages)
	want := [][]string{{"a", "b"}, {"c", "d", "e"}}
	t.Logf("input=delete f from pages [a:6,b:6,c:2,d:1],[e:10,f:2]; output=%v; reason=borrowing d alone gives 11<M, borrowing c,d is minimum prefix giving 13 and left remains 12", got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}

	tree2, err := newModelTree(41, 10)
	if err != nil {
		t.Fatal(err)
	}
	tree2.pages[0] = &leafPage{
		id: 1,
		records: []record{
			{key: "a", size: 10},
			{key: "b", size: 10},
			{key: "c", size: 2},
		},
		occupied: 22,
	}
	tree2.pages = append(tree2.pages, &leafPage{
		id: 2,
		records: []record{
			{key: "d", size: 20},
			{key: "e", size: 5},
		},
		occupied: 25,
	})
	tree2.nextPage = 3
	if err := tree2.Delete("e"); err != nil {
		t.Fatal(err)
	}
	got2 := tree2.Pages()
	want2 := []Page{{ID: 1, Keys: []string{"a", "b", "c"}, Occupied: 22}, {ID: 2, Keys: []string{"d"}, Occupied: 20}}
	t.Logf("input=delete e from pages [a:10,b:10,c:2],[d:20,e:5] with C=41; output=%v; reason=borrowing c leaves left at 20<M=21 and merging is 42>C, so underflow remains", pageSummary(got2))
	assertPages(t, got2, want2)
}

func TestMergeBoundariesAndPriorities(t *testing.T) {
	tests := []struct {
		name      string
		capacity  int
		pages     []*leafPage
		deleteKey string
		want      []Page
		reason    string
	}{
		{
			name:     "merge left exactly capacity",
			capacity: 48,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 12}, {key: "b", size: 8}, {key: "c", size: 5}}, occupied: 25},
				{id: 2, records: []record{{key: "d", size: 12}, {key: "e", size: 11}, {key: "z", size: 1}}, occupied: 24},
			},
			deleteKey: "z",
			want:      []Page{{ID: 1, Keys: []string{"a", "b", "c", "d", "e"}, Occupied: 48}},
			reason:    "lending cannot leave left at M=24 and 25+23=48=C, so merge-left succeeds and page 2 is released",
		},
		{
			name:     "merge left one byte over capacity stays underflow",
			capacity: 48,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 12}, {key: "b", size: 10}, {key: "c", size: 4}}, occupied: 26},
				{id: 2, records: []record{{key: "d", size: 12}, {key: "e", size: 11}, {key: "z", size: 1}}, occupied: 24},
			},
			deleteKey: "z",
			want: []Page{
				{ID: 1, Keys: []string{"a", "b", "c"}, Occupied: 26},
				{ID: 2, Keys: []string{"d", "e"}, Occupied: 23},
			},
			reason: "lending cannot leave left at M=24 and 26+23=49>C, so no step succeeds",
		},
		{
			name:     "left borrow wins over right borrow",
			capacity: 40,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 20}, {key: "b", size: 10}}, occupied: 30},
				{id: 2, records: []record{{key: "c", size: 10}, {key: "cb", size: 1}}, occupied: 11},
				{id: 3, records: []record{{key: "d", size: 10}, {key: "e", size: 20}}, occupied: 30},
			},
			deleteKey: "cb",
			want: []Page{
				{ID: 1, Keys: []string{"a"}, Occupied: 20},
				{ID: 2, Keys: []string{"b", "c"}, Occupied: 20},
				{ID: 3, Keys: []string{"d", "e"}, Occupied: 30},
			},
			reason: "left and right can each lend a 10-byte record; fixed order chooses b from the left page",
		},
		{
			name:     "right borrow wins over merge left",
			capacity: 40,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 20}}, occupied: 20},
				{id: 2, records: []record{{key: "b", size: 10}, {key: "bb", size: 1}}, occupied: 11},
				{id: 3, records: []record{{key: "c", size: 10}, {key: "d", size: 20}}, occupied: 30},
			},
			deleteKey: "bb",
			want: []Page{
				{ID: 1, Keys: []string{"a"}, Occupied: 20},
				{ID: 2, Keys: []string{"b", "c"}, Occupied: 20},
				{ID: 3, Keys: []string{"d"}, Occupied: 20},
			},
			reason: "lending a would leave the left page below M, but right borrow succeeds before merge-left",
		},
		{
			name:     "merge left wins over merge right",
			capacity: 40,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 10}}, occupied: 10},
				{id: 2, records: []record{{key: "b", size: 10}, {key: "bb", size: 1}}, occupied: 11},
				{id: 3, records: []record{{key: "c", size: 10}}, occupied: 10},
			},
			deleteKey: "bb",
			want: []Page{
				{ID: 1, Keys: []string{"a", "b"}, Occupied: 20},
				{ID: 3, Keys: []string{"c"}, Occupied: 10},
			},
			reason: "neighbor lending cannot keep both sides at M, and merging with either side fits; left merge is first",
		},
		{
			name:     "leftmost only borrows from right and separator refreshes",
			capacity: 40,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 10}, {key: "aa", size: 1}}, occupied: 11},
				{id: 2, records: []record{{key: "b", size: 10}, {key: "c", size: 20}}, occupied: 30},
			},
			deleteKey: "aa",
			want: []Page{
				{ID: 1, Keys: []string{"a", "b"}, Occupied: 20},
				{ID: 2, Keys: []string{"c"}, Occupied: 20},
			},
			reason: "the underflow page is leftmost, so it never tries left-side operations; its first key remains a and page 2 first key becomes c",
		},
		{
			name:     "leftmost merges right and surviving page has no separator",
			capacity: 40,
			pages: []*leafPage{
				{id: 1, records: []record{{key: "a", size: 10}, {key: "aa", size: 1}}, occupied: 11},
				{id: 2, records: []record{{key: "b", size: 10}}, occupied: 10},
			},
			deleteKey: "aa",
			want:      []Page{{ID: 1, Keys: []string{"a", "b"}, Occupied: 20}},
			reason:    "the left page is the survivor and there is no non-leftmost page left, so no separator remains",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tree, err := newModelTree(tt.capacity, 10)
			if err != nil {
				t.Fatal(err)
			}
			tree.pages = cloneLeafPages(tt.pages)
			tree.nextPage = 4
			if err := tree.Delete(tt.deleteKey); err != nil {
				t.Fatal(err)
			}
			got := tree.Pages()
			t.Logf("input=delete %s from %s; output=%v; reason=%s", tt.deleteKey, leafPageSummary(tt.pages), pageSummary(got), tt.reason)
			assertPages(t, got, tt.want)
		})
	}
}

func TestIDsRejectionsAndSinglePage(t *testing.T) {
	tree, err := NewByteBPlusTree(8, 2)
	if err != nil {
		t.Fatal(err)
	}
	entries := []struct {
		key  string
		size int
	}{
		{"a", 2}, {"b", 2}, {"c", 2}, {"d", 2}, {"e", 2}, {"f", 2},
	}
	for _, item := range entries {
		if err := tree.Insert(item.key, item.size); err != nil {
			t.Fatalf("insert %s: %v", item.key, err)
		}
	}

	before := tree.Pages()
	err = tree.Insert("g", 2)
	after := tree.Pages()
	t.Logf("input=insert g:2 after %v with P=2; output=%v; reason=split requires page 3 but page count is already at the limit", pageSummary(before), err)
	if err != ErrPageLimit {
		t.Fatalf("error = %v, want ErrPageLimit", err)
	}
	if tree.nextPage != 3 {
		t.Fatalf("nextPage = %d, want 3", tree.nextPage)
	}
	assertPages(t, after, before)

	for _, key := range []string{"e", "f", "d"} {
		if err := tree.Delete(key); err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
	}
	for _, item := range []struct {
		key  string
		size int
	}{
		{"d", 2}, {"e", 2}, {"f", 2},
	} {
		if err := tree.Insert(item.key, item.size); err != nil {
			t.Fatalf("insert %s after release: %v", item.key, err)
		}
	}
	gotPages := tree.Pages()
	t.Logf("input=delete e,f,d then insert d,e,f after ID 2 was released; output=%v; reason=the next split allocates ID 3 and never reuses 2", pageSummary(gotPages))
	if len(gotPages) != 2 || gotPages[0].ID != 1 || gotPages[1].ID != 3 {
		t.Fatalf("pages=%v: released ID 2 must not be reused", pageSummary(gotPages))
	}

	single, err := NewByteBPlusTree(8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := single.Insert("a", 2); err != nil {
		t.Fatal(err)
	}
	if err := single.Delete("a"); err != nil {
		t.Fatal(err)
	}
	got := single.Pages()
	want := []Page{{ID: 1, Keys: []string{}, Occupied: 0}}
	t.Logf("input=insert a:2 then delete a with one page and C=8; output=%v; reason=single-page underflow is not rebalanced", pageSummary(got))
	assertPages(t, got, want)
}

func TestValidationAndRejectionOrder(t *testing.T) {
	tree, err := NewByteBPlusTree(8, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tree.Insert("a", 2); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"b", "c", "d"} {
		if err := tree.Insert(key, 2); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	if err := tree.Insert("", 2); err != ErrInvalidParameter {
		t.Fatalf("empty insert error = %v", err)
	}
	if err := tree.Insert("a", 0); err != ErrInvalidParameter {
		t.Fatalf("invalid size error = %v", err)
	}
	if err := tree.Insert("a", 2); err != ErrDuplicateKey {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := tree.Insert("e", 2); err != ErrPageLimit {
		t.Fatalf("page-limit error = %v", err)
	}
	if err := tree.Delete(""); err != ErrInvalidParameter {
		t.Fatalf("empty delete error = %v", err)
	}
	if err := tree.Delete("missing"); err != ErrKeyNotFound {
		t.Fatalf("missing delete error = %v", err)
	}
	if _, err := NewByteBPlusTree(7, 1); err != ErrInvalidParameter {
		t.Fatalf("capacity constructor error = %v", err)
	}
	if _, err := NewByteBPlusTree(8, 0); err != ErrInvalidParameter {
		t.Fatalf("limit constructor error = %v", err)
	}
}

type testOperation struct {
	kind string
	key  string
	size int
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		random := rand.New(rand.NewPCG(1200+uint64(sequence), 77))
		capacity := 8 + random.IntN(93)
		pageLimit := 1 + random.IntN(12)
		tree, err := NewByteBPlusTree(capacity, pageLimit)
		if err != nil {
			t.Fatalf("sequence %d constructor: %v", sequence, err)
		}
		model := newNaiveTree(capacity, pageLimit)
		operations := make([]testOperation, 40)
		inputs := make([]string, 0, 40)
		results := make([]string, 0, 40)

		for i := range operations {
			key := fmt.Sprintf("k%02d", random.IntN(18))
			if random.IntN(18) == 0 {
				key = ""
			}
			size := 1 + random.IntN(capacity/2)
			if random.IntN(5) == 0 {
				operations[i] = testOperation{kind: "delete", key: key}
				inputs = append(inputs, fmt.Sprintf("Delete(%q)", key))
			} else {
				operations[i] = testOperation{kind: "insert", key: key, size: size}
				inputs = append(inputs, fmt.Sprintf("Insert(%q,%d)", key, size))
			}

			var actual error
			if operations[i].kind == "delete" {
				actual = tree.Delete(operations[i].key)
			} else {
				actual = tree.Insert(operations[i].key, operations[i].size)
			}
			var expected error
			if operations[i].kind == "delete" {
				expected = model.delete(operations[i].key)
			} else {
				expected = model.insert(operations[i].key, operations[i].size)
			}
			results = append(results, fmt.Sprintf("%v", actual))

			actualPages := tree.Pages()
			expectedPages := model.pagesSnapshot()
			if actual != expected || !reflect.DeepEqual(actualPages, expectedPages) {
				t.Fatalf("sequence %d C=%d P=%d\ninput=%s\noutput=%v\nactual error=%v pages=%v\nexpected error=%v pages=%v\nreason=naive step-by-step split and fixed-order rebalance must match implementation",
					sequence, capacity, pageLimit, inputs, results, actual, pageSummary(actualPages), expected, pageSummary(expectedPages))
			}
		}
		t.Logf("sequence=%d C=%d P=%d input=%s output=%v final=%v; reason=all 40 operations matched step-by-step naive simulation", sequence, capacity, pageLimit, inputs, results, pageSummary(model.pagesSnapshot()))
	}
}

func TestPageLocationComparisonBound(t *testing.T) {
	tree, err := NewByteBPlusTree(100, 100)
	if err != nil {
		t.Fatal(err)
	}

	for page := 2; page <= 65; page++ {
		firstKey := fmt.Sprintf("k%03d", page)
		tree.pages = append(tree.pages, &leafPage{
			id:       page,
			records:  []record{{key: firstKey, size: 1}},
			occupied: 1,
		})
		before := tree.comparisonCount
		tree.locatePageIndex(firstKey)
		used := tree.comparisonCount - before
		pageCount := len(tree.pages)
		allowed := ceilLog2(pageCount)
		t.Logf("input=locate %s among %d pages; output=%d comparisons; reason=non-exported counter bound ceil(log2(%d))=%d", firstKey, pageCount, used, pageCount, allowed)
		if used > allowed {
			t.Fatalf("used %d comparisons, want <= %d", used, allowed)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	tree, err := NewByteBPlusTree(100, 100)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for item := 0; item < 40; item++ {
				key := fmt.Sprintf("w%02d-k%02d", worker, item)
				if err := tree.Insert(key, 2); err != nil {
					t.Errorf("insert %s: %v", key, err)
					return
				}
				_ = tree.Pages()
				if item%2 == 0 {
					if err := tree.Delete(key); err != nil {
						t.Errorf("delete %s: %v", key, err)
						return
					}
				}
			}
		}(worker)
	}
	wait.Wait()

	pages := tree.Pages()
	totalRecords := 0
	totalOccupied := 0
	previous := ""
	for _, page := range pages {
		for _, key := range page.Keys {
			if previous != "" && key <= previous {
				t.Fatalf("keys not globally ordered: %s before %s", previous, key)
			}
			previous = key
			totalRecords++
		}
		totalOccupied += page.Occupied
	}
	t.Logf("input=32 workers with 40 insert/delete operations; output=%d records and %d occupied bytes; reason=mutex serializes all operations and Pages reads a deep copy", totalRecords, totalOccupied)
	if totalRecords != 640 || totalOccupied != 1280 {
		t.Fatalf("records=%d occupied=%d, want 640 and 1280", totalRecords, totalOccupied)
	}
}

func TestDeletingFirstRecordRefreshesSeparator(t *testing.T) {
	tree, err := NewByteBPlusTree(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		if err := tree.Insert(key, 20); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	if err := tree.Delete("a"); err != nil {
		t.Fatal(err)
	}

	got := tree.Pages()
	want := []Page{
		{ID: 1, Keys: []string{"b", "c", "d"}, Occupied: 60},
		{ID: 2, Keys: []string{"e", "f", "g", "h"}, Occupied: 80},
	}
	t.Logf("input=insert a:h at 20 bytes then delete a; output=%v; reason=right borrow moves d to page 1 and page 2's implicit separator refreshes from d to e", pageSummary(got))
	assertPages(t, got, want)
}

func TestSpecExamples(t *testing.T) {
	keys := []string{"a", "b", "c", "d", "e", "f"}

	tree1, err := NewByteBPlusTree(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if err := tree1.Insert(key, 20); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	if err := tree1.Delete("d"); err != nil {
		t.Fatal(err)
	}
	want1 := []Page{{ID: 1, Keys: []string{"a", "b", "c", "e", "f"}, Occupied: 100}}
	got1 := tree1.Pages()
	t.Logf("input=insert a:f at 20 bytes then delete d; output=%v; reason=left borrow leaves page 1 below 50, so merge-left fills exactly 100 and releases page 2", pageSummary(got1))
	assertPages(t, got1, want1)

	tree2, err := NewByteBPlusTree(100, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range append(keys, "g", "h") {
		if err := tree2.Insert(key, 20); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}
	if err := tree2.Delete("a"); err != nil {
		t.Fatal(err)
	}
	want2 := []Page{
		{ID: 1, Keys: []string{"b", "c", "d"}, Occupied: 60},
		{ID: 2, Keys: []string{"e", "f", "g", "h"}, Occupied: 80},
	}
	got2 := tree2.Pages()
	t.Logf("input=insert a:h at 20 bytes then delete a; output=%v; reason=leftmost page borrows d from the right head and separator refreshes to e", pageSummary(got2))
	assertPages(t, got2, want2)
}

func ceilLog2(value int) uint64 {
	if value <= 1 {
		return 0
	}
	return uint64(bits.Len(uint(value - 1)))
}

func flattenPageKeys(pages []Page) [][]string {
	keys := make([][]string, len(pages))
	for i, page := range pages {
		keys[i] = append([]string(nil), page.Keys...)
	}
	return keys
}

func pageSummary(pages []Page) []Page {
	result := make([]Page, len(pages))
	for i, page := range pages {
		result[i] = Page{ID: page.ID, Keys: append([]string(nil), page.Keys...), Occupied: page.Occupied}
	}
	return result
}

func assertPages(t *testing.T, got, want []Page) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages = %+v, want %+v", got, want)
	}
}

func cloneLeafPages(pages []*leafPage) []*leafPage {
	result := make([]*leafPage, len(pages))
	for i, page := range pages {
		result[i] = &leafPage{
			id:       page.id,
			records:  append([]record(nil), page.records...),
			occupied: page.occupied,
		}
	}
	return result
}

func leafPageSummary(pages []*leafPage) string {
	result := make([]Page, len(pages))
	for i, page := range pages {
		keys := make([]string, len(page.records))
		for j, item := range page.records {
			keys[j] = fmt.Sprintf("%s:%d", item.key, item.size)
		}
		result[i] = Page{ID: page.id, Keys: keys, Occupied: page.occupied}
	}
	return fmt.Sprint(result)
}
