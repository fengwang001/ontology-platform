package fsck

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustCheck(t *testing.T, c *Checker) *Report {
	t.Helper()
	rep, err := c.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return rep
}

func mustRepair(t *testing.T, c *Checker) *RepairResult {
	t.Helper()
	res, err := c.Repair()
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	return res
}

func logRepair(t *testing.T, input []byte, res *RepairResult, rationale string) {
	t.Helper()
	t.Logf("input image: %d bytes", len(input))
	t.Logf("repair output:\n%s", res.Text())
	t.Logf("rationale: %s", rationale)
}

// Two inodes share one data block: the lowest-numbered inode keeps it, the
// other receives a copy in the lowest-numbered free block.
func TestSharedBlockCopiedToFreeBlock(t *testing.T) {
	spec := imageSpec{
		inodeCount: 4,
		blockCount: 8,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(7),
			1: file(3),
			2: file(3),
		},
		entries:   map[int][]dent{0: {{"a", 1}, {"b", 2}}},
		blockData: map[int]string{3: "shared-content"},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	rep := mustCheck(t, c)
	t.Logf("check findings:\n%s", rep.Text())
	if len(rep.Findings) != 1 || rep.Findings[0].Category != CatSharedBlock {
		t.Fatalf("expected exactly one shared-block finding, got:\n%s", rep.Text())
	}
	if rep.Findings[0].Inode != 2 || rep.Findings[0].Block != 3 {
		t.Fatalf("unexpected finding: %v", rep.Findings[0])
	}

	res := mustRepair(t, c)
	logRepair(t, data, res, "block 3 stays with inode 1 (lowest); inode 2 gets a copy in the lowest free block")
	if res.DataLoss {
		t.Fatal("no data loss expected when a free block exists")
	}

	img := parseImageFile(t, c)
	if got := img.Inodes[1].Blocks; len(got) != 1 || got[0] != 3 {
		t.Fatalf("inode 1 should keep block 3, got %v", got)
	}
	if got := img.Inodes[2].Blocks; len(got) != 1 || got[0] != 0 {
		t.Fatalf("inode 2 should get lowest free block 0, got %v", got)
	}
	if !bytes.Equal(img.Blocks[0], img.Blocks[3]) {
		t.Fatal("copied block content differs from the shared block")
	}
	if string(img.Blocks[0][:len("shared-content")]) != "shared-content" {
		t.Fatal("copied block lost the original content")
	}

	rep = mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// Same sharing, but no free block remains: the non-owner inode loses the
// block reference and the loss is reported.
func TestSharedBlockTruncatedWhenNoFreeBlocks(t *testing.T) {
	spec := imageSpec{
		inodeCount: 5,
		blockCount: 4,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(1),
			1: file(0),
			2: file(0),
			3: file(2, 3),
		},
		entries: map[int][]dent{0: {{"a", 1}, {"b", 2}, {"c", 3}}},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	res := mustRepair(t, c)
	logRepair(t, data, res, "all 4 blocks referenced to inodes; inode 2 cannot copy block 0 and must drop it")
	if !res.DataLoss {
		t.Fatal("data loss must be reported when truncation happens")
	}
	found := false
	for _, a := range res.Actions {
		if a.Kind == "shared-block-truncate" && a.Inode == 2 && a.Block == 0 && a.DataLoss {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing shared-block-truncate action for inode 2:\n%s", res.Text())
	}

	img := parseImageFile(t, c)
	if len(img.Inodes[2].Blocks) != 0 {
		t.Fatalf("inode 2 block list should be truncated to empty, got %v", img.Inodes[2].Blocks)
	}
	if got := img.Inodes[1].Blocks; len(got) != 1 || got[0] != 0 {
		t.Fatalf("inode 1 should keep block 0, got %v", got)
	}

	rep := mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// Two directories reference each other and are unreachable from the root:
// the smallest inode of the cycle is attached to the recovery directory.
func TestUnreachableDirectoryCycle(t *testing.T) {
	spec := imageSpec{
		inodeCount: 5,
		blockCount: 6,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(5),
			1: dir(1),
			2: dir(2),
		},
		entries: map[int][]dent{
			1: {{"b", 2}},
			2: {{"a", 1}},
		},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	rep := mustCheck(t, c)
	t.Logf("check findings:\n%s", rep.Text())
	if len(rep.Findings) != 2 || rep.Findings[0].Category != CatUnreachable || rep.Findings[1].Category != CatUnreachable {
		t.Fatalf("expected two unreachable-inode findings, got:\n%s", rep.Text())
	}

	res := mustRepair(t, c)
	logRepair(t, data, res, "cycle 1<->2 has no subtree top; smallest cycle inode 1 is attached under a new lost+found")

	img := parseImageFile(t, c)
	rootEntries := entryNames(img, 0)
	rec, ok := rootEntries[RecoveryName]
	if !ok {
		t.Fatalf("recovery directory %q not created under root", RecoveryName)
	}
	if rec != 3 {
		t.Fatalf("recovery directory should use first free inode 3, got %d", rec)
	}
	if img.Inodes[rec].Type != TypeDir {
		t.Fatal("recovery inode must be a directory")
	}
	recEntries := entryNames(img, rec)
	if recEntries["1"] != 1 {
		t.Fatalf("cycle top inode 1 should be attached by name, got %v", recEntries)
	}
	if _, ok := recEntries["2"]; ok {
		t.Fatal("only the cycle top (inode 1) may be attached, not inode 2")
	}

	rep = mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// A subdirectory collected by two parents stays only in the parent with
// the lowest inode number.
func TestDirectoryWithTwoParents(t *testing.T) {
	spec := imageSpec{
		inodeCount: 6,
		blockCount: 8,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(7),
			1: dir(3),
			2: dir(4),
			4: dir(5),
			5: file(6),
		},
		entries: map[int][]dent{
			0: {{"x", 1}, {"y", 2}},
			1: {{"sub", 4}},
			2: {{"sub", 4}},
			4: {{"f", 5}},
		},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	rep := mustCheck(t, c)
	t.Logf("check findings:\n%s", rep.Text())
	if len(rep.Findings) != 1 || rep.Findings[0].Category != CatMultiParent {
		t.Fatalf("expected exactly one multi-parent-dir finding, got:\n%s", rep.Text())
	}
	if rep.Findings[0].Inode != 4 || rep.Findings[0].Parent != 2 {
		t.Fatalf("unexpected finding: %v", rep.Findings[0])
	}

	res := mustRepair(t, c)
	logRepair(t, data, res, "directory 4 keeps only the entry in parent 1 (lowest parent inode)")

	img := parseImageFile(t, c)
	if got := entryNames(img, 1); got["sub"] != 4 {
		t.Fatalf("parent 1 must keep the entry, got %v", got)
	}
	if got := entryNames(img, 2); len(got) != 0 {
		t.Fatalf("parent 2 must lose the entry, got %v", got)
	}
	if img.Inodes[4].Nlink != 1 {
		t.Fatalf("inode 4 nlink should be 1 after repair, got %d", img.Inodes[4].Nlink)
	}

	rep = mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// Directory entries pointing at unallocated or out-of-range inodes are
// removed.
func TestDanglingDirEntry(t *testing.T) {
	spec := imageSpec{
		inodeCount: 6,
		blockCount: 4,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(1),
			1: file(0),
		},
		entries: map[int][]dent{
			0: {{"good", 1}, {"bad", 5}, {"ghost", 9}},
		},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	rep := mustCheck(t, c)
	t.Logf("check findings:\n%s", rep.Text())
	if len(rep.Findings) != 2 || rep.Findings[0].Category != CatDanglingEntry || rep.Findings[1].Category != CatDanglingEntry {
		t.Fatalf("expected two dangling-dir-entry findings, got:\n%s", rep.Text())
	}

	res := mustRepair(t, c)
	logRepair(t, data, res, "entries bad->5 (free inode) and ghost->9 (out of range) are dropped")

	img := parseImageFile(t, c)
	got := entryNames(img, 0)
	if len(got) != 1 || got["good"] != 1 {
		t.Fatalf("only the good entry may survive, got %v", got)
	}

	rep = mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// An image whose only problem is the bitmap: one missing bit and one
// surplus bit.
func TestBitmapOnlyMismatch(t *testing.T) {
	spec := imageSpec{
		inodeCount: 2,
		blockCount: 4,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(1),
			1: file(0),
		},
		entries:    map[int][]dent{0: {{"f", 1}}},
		bitmapFlip: []int{0, 2},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	rep := mustCheck(t, c)
	t.Logf("check findings:\n%s", rep.Text())
	if len(rep.Findings) != 2 {
		t.Fatalf("expected two bitmap findings, got:\n%s", rep.Text())
	}
	for _, f := range rep.Findings {
		if f.Category != CatBitmap {
			t.Fatalf("only bitmap findings expected, got %v", f)
		}
	}
	if rep.Findings[0].Block != 0 || rep.Findings[1].Block != 2 {
		t.Fatalf("unexpected bitmap findings: %v", rep.Findings)
	}

	res := mustRepair(t, c)
	logRepair(t, data, res, "bitmap rebuilt from actual references: block 0 used, block 2 free")

	img := parseImageFile(t, c)
	want := []bool{true, true, false, false}
	for b := 0; b < 4; b++ {
		if img.Bitmap[b] != want[b] {
			t.Fatalf("bitmap[%d] = %v, want %v", b, img.Bitmap[b], want[b])
		}
	}

	rep = mustCheck(t, c)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}
}

// A combined image exercising every repair stage; after one repair the
// recheck must report zero findings, and repeated runs over identical
// input must produce byte-identical results.
func TestRepairCombinedThenRecheckZeroAndDeterministic(t *testing.T) {
	spec := imageSpec{
		inodeCount: 8,
		blockCount: 10,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(9),
			1: file(3).links(5), // wrong link count on purpose
			2: file(3),          // shares block 3 with inode 1
			3: dir(4),
			4: dir(5),
			5: file(), // unreachable, no content: freed
			6: dir(6),
		},
		entries: map[int][]dent{
			0: {{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}, {"dead", 7}},
			3: {{"sub", 6}},
			4: {{"sub", 6}},
		},
		bitmapFlip: []int{8},
	}
	data := buildImage(t, spec)

	c1 := newChecker(t, data)
	c2 := newChecker(t, data)

	rep1 := mustCheck(t, c1)
	rep2 := mustCheck(t, c1)
	if rep1.Text() != rep2.Text() {
		t.Fatal("repeated checks on the same image must produce identical reports")
	}
	t.Logf("combined check findings:\n%s", rep1.Text())
	wantCats := []Category{CatSharedBlock, CatDanglingEntry, CatMultiParent, CatUnreachable, CatLinkCount, CatBitmap}
	if len(rep1.Findings) != len(wantCats) {
		t.Fatalf("expected %d findings, got:\n%s", len(wantCats), rep1.Text())
	}
	for i, cat := range wantCats {
		if rep1.Findings[i].Category != cat {
			t.Fatalf("finding %d: want category %v, got %v", i, cat, rep1.Findings[i])
		}
	}

	res1 := mustRepair(t, c1)
	res2 := mustRepair(t, c2)
	logRepair(t, data, res1, "all six stages applied in fixed order")
	if res1.Text() != res2.Text() {
		t.Fatal("identical inputs must produce identical repair logs")
	}
	out1 := readImageFile(t, c1)
	out2 := readImageFile(t, c2)
	if !bytes.Equal(out1, out2) {
		t.Fatal("identical inputs must produce byte-identical repaired images")
	}

	rep := mustCheck(t, c1)
	if len(rep.Findings) != 0 {
		t.Fatalf("recheck after repair must be clean, got:\n%s", rep.Text())
	}

	// Repairing an already-clean image is a no-op that changes nothing.
	res := mustRepair(t, c1)
	if len(res.Actions) != 0 || len(res.Findings) != 0 {
		t.Fatalf("repair of a clean image must be a no-op, got:\n%s", res.Text())
	}
	if !bytes.Equal(readImageFile(t, c1), out1) {
		t.Fatal("repairing a clean image must not change its bytes")
	}
}

// An interruption injected just before the atomic commit must leave the
// original image byte-identical.
func TestRepairInterruptedLeavesImageUntouched(t *testing.T) {
	spec := imageSpec{
		inodeCount: 4,
		blockCount: 8,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(7),
			1: file(3),
			2: file(3),
		},
		entries: map[int][]dent{0: {{"a", 1}, {"b", 2}}},
	}
	data := buildImage(t, spec)
	c := newChecker(t, data)

	boom := errors.New("injected crash")
	c.FailBeforeCommit = func() error { return boom }

	_, err := c.Repair()
	if !errors.Is(err, boom) {
		t.Fatalf("expected injected error, got %v", err)
	}
	if !bytes.Equal(readImageFile(t, c), data) {
		t.Fatal("original image changed despite the interrupted repair")
	}
	if _, err := os.Stat(c.Path() + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file must be removed after an interrupted repair")
	}
	t.Logf("input image: %d bytes; repair aborted with %q; image verified byte-identical", len(data), boom)

	// The checker stays usable after the injected failure.
	c.FailBeforeCommit = nil
	if _, err := c.Repair(); err != nil {
		t.Fatalf("repair after clearing the failpoint: %v", err)
	}
	if rep := mustCheck(t, c); len(rep.Findings) != 0 {
		t.Fatalf("recheck after successful repair must be clean, got:\n%s", rep.Text())
	}
}

// Each fatal condition refuses the whole operation with a distinguishable
// reason and leaves the image byte-identical.
func TestRefusals(t *testing.T) {
	base := imageSpec{
		inodeCount: 4,
		blockCount: 4,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(1),
			1: file(0),
		},
		entries: map[int][]dent{0: {{"f", 1}}},
	}

	corrupt := buildImage(t, base)
	corrupt[0] ^= 0xff

	truncated := buildImage(t, base)[:100]

	rootNotDir := base
	rootNotDir.inodes = map[int]inodeSpec{0: file(1), 1: file(0)}
	rootNotDir.entries = nil

	mounted := base
	mounted.flags = FlagMounted

	nameTaken := imageSpec{
		inodeCount: 4,
		blockCount: 4,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(2),
			1: file(0), // occupies the recovery name but is a file
			2: file(1), // unreachable with content: would need lost+found
		},
		entries: map[int][]dent{0: {{RecoveryName, 1}}},
	}

	noFreeInode := imageSpec{
		inodeCount: 2,
		blockCount: 3,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(1),
			1: file(0), // unreachable with content, no free inode for lost+found
		},
	}

	cases := []struct {
		name    string
		data    []byte
		want    error
		checkTo bool // also expect Check to refuse
	}{
		{"corrupt-header", corrupt, ErrCorruptHeader, true},
		{"truncated-image", truncated, ErrCorruptHeader, true},
		{"root-not-dir", buildImage(t, rootNotDir), ErrRootNotDir, true},
		{"write-mounted", buildImage(t, mounted), ErrMounted, true},
		{"recovery-name-taken", buildImage(t, nameTaken), ErrRecoveryNameTaken, false},
		{"no-free-inode", buildImage(t, noFreeInode), ErrNoFreeInode, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newChecker(t, tc.data)

			_, err := c.Repair()
			if !errors.Is(err, tc.want) {
				t.Fatalf("repair: want %v, got %v", tc.want, err)
			}
			if tc.checkTo {
				if _, err := c.Check(); !errors.Is(err, tc.want) {
					t.Fatalf("check: want %v, got %v", tc.want, err)
				}
			}
			if !bytes.Equal(readImageFile(t, c), tc.data) {
				t.Fatal("refused operation must leave the image byte-identical")
			}
			t.Logf("input image: %d bytes; refused with %q; image verified byte-identical", len(tc.data), tc.want)
		})
	}

	// The refusal reasons are mutually distinguishable.
	reasons := []error{ErrCorruptHeader, ErrRootNotDir, ErrRecoveryNameTaken, ErrNoFreeInode, ErrMounted}
	for i, a := range reasons {
		for _, b := range reasons[i+1:] {
			if errors.Is(a, b) {
				t.Fatalf("refusal reasons %v and %v are not distinguishable", a, b)
			}
		}
	}
}

// Many read-only checks run concurrently and all observe the same report.
func TestConcurrentChecks(t *testing.T) {
	spec := imageSpec{
		inodeCount: 4,
		blockCount: 8,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(7),
			1: file(3),
			2: file(3),
		},
		entries: map[int][]dent{0: {{"a", 1}, {"b", 2}}},
	}
	c := newChecker(t, buildImage(t, spec))

	want := mustCheck(t, c).Text()
	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				rep, err := c.Check()
				if err != nil {
					errs <- fmt.Errorf("worker %d: %w", id, err)
					return
				}
				if rep.Text() != want {
					errs <- fmt.Errorf("worker %d: report mismatch:\n%s", id, rep.Text())
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("%d concurrent readers x 25 checks each observed identical reports", workers)
}

// Repair and write-mount are mutually exclusive: a repair started while
// the image is mounted blocks until the unmount, and a second mount
// blocks while the first is held.
func TestRepairExcludesWriteMount(t *testing.T) {
	spec := imageSpec{
		inodeCount: 4,
		blockCount: 8,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(7),
			1: file(3),
			2: file(3),
		},
		entries: map[int][]dent{0: {{"a", 1}, {"b", 2}}},
	}
	c := newChecker(t, buildImage(t, spec))

	unmount, err := c.MountWrite()
	if err != nil {
		t.Fatalf("mount: %v", err)
	}

	// A second mount blocks while the first is held.
	second := make(chan func(), 1)
	go func() {
		u, err := c.MountWrite()
		if err != nil {
			t.Errorf("second mount: %v", err)
			second <- nil
			return
		}
		second <- u
	}()
	select {
	case <-second:
		t.Fatal("second mount succeeded while the first mount was held")
	case <-time.After(100 * time.Millisecond):
	}

	// A repair blocks while the image is mounted.
	repaired := make(chan error, 1)
	go func() {
		_, err := c.Repair()
		repaired <- err
	}()
	select {
	case err := <-repaired:
		t.Fatalf("repair completed while the image was mounted: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	unmount()
	// Whichever of the second mount or the repair acquires the lock first,
	// both must eventually proceed; release the second mount immediately.
	select {
	case u := <-second:
		if u != nil {
			u()
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second mount did not proceed after the first unmount")
	}
	select {
	case err := <-repaired:
		if err != nil {
			t.Fatalf("repair after unmount: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repair did not complete after unmount")
	}

	if rep := mustCheck(t, c); len(rep.Findings) != 0 {
		t.Fatalf("image should be repaired and clean, got:\n%s", rep.Text())
	}
	t.Log("repair and write-mount verified mutually exclusive")
}

// The findings list is sorted by category and inode number.
func TestFindingsSorted(t *testing.T) {
	spec := imageSpec{
		inodeCount: 8,
		blockCount: 10,
		root:       0,
		inodes: map[int]inodeSpec{
			0: dir(9),
			1: file(3),
			2: file(3),
			5: file(),
			6: file(),
		},
		entries:    map[int][]dent{0: {{"a", 1}, {"b", 2}, {"dead", 7}}},
		bitmapFlip: []int{8},
	}
	c := newChecker(t, buildImage(t, spec))
	rep := mustCheck(t, c)
	t.Logf("findings:\n%s", rep.Text())
	for i := 1; i < len(rep.Findings); i++ {
		a, b := rep.Findings[i-1], rep.Findings[i]
		if a.Category > b.Category || (a.Category == b.Category && a.Inode > b.Inode) {
			t.Fatalf("findings not sorted: %v before %v", a, b)
		}
	}
	if !strings.Contains(rep.Text(), "unreachable-inode inode=5") || !strings.Contains(rep.Text(), "unreachable-inode inode=6") {
		t.Fatalf("expected unreachable findings for inodes 5 and 6:\n%s", rep.Text())
	}
}
