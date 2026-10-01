package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// naiveImage is an independent byte-level encoding of the 12-bit layout,
// written straight from the per-entry packing description.
func naiveImage(fat []uint16) []byte {
	b := make([]byte, (len(fat)*3+1)/2)
	for n, v := range fat {
		o := n + n/2
		if n%2 == 0 {
			b[o] = byte(v & 0xFF)
			b[o+1] = (b[o+1] & 0xF0) | byte((v>>8)&0x0F)
		} else {
			b[o] = (b[o] & 0x0F) | byte((v&0x0F)<<4)
			b[o+1] = byte((v >> 4) & 0xFF)
		}
	}
	return b
}

func imageEntries(t *testing.T, b []byte, count int) []uint16 {
	t.Helper()
	out := make([]uint16, count)
	for n := 0; n < count; n++ {
		o := n + n/2
		if n%2 == 0 {
			out[n] = uint16(b[o]) | uint16(b[o+1]&0x0F)<<8
		} else {
			out[n] = uint16(b[o]>>4) | uint16(b[o+1])<<4
		}
	}
	return out
}

func wantChain(t *testing.T, f *FAT12, h uint16, want []uint16) {
	t.Helper()
	got, err := f.Chain(h)
	if err != nil {
		t.Fatalf("Chain(%d): %v", h, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("Chain(%d) = %v, want %v", h, got, want)
	}
}

func TestNewBoundaries(t *testing.T) {
	for _, c := range []int{0, -1, 4079, 100000} {
		if _, err := New(c); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d) err = %v, want ErrInvalidArgument", c, err)
		}
	}

	for _, c := range []int{1, 4078} {
		f, err := New(c)
		if err != nil {
			t.Fatalf("New(%d): %v", c, err)
		}
		img := f.Image()
		if len(img) != ((c+2)*3+1)/2 {
			t.Fatalf("C=%d image len = %d", c, len(img))
		}
		e := imageEntries(t, img, c+2)
		if e[0] != 0xFF8 || e[1] != eocMarker {
			t.Fatalf("C=%d reserved entries = %#x %#x", c, e[0], e[1])
		}
		for i := 2; i < c+2; i++ {
			if e[i] != 0 {
				t.Fatalf("C=%d entry %d = %#x, want 0", c, i, e[i])
			}
		}
		if f.Rover() != 2 || f.Free() != c {
			t.Fatalf("C=%d initial rover/free wrong: %d %d", c, f.Rover(), f.Free())
		}
		if got := naiveImage(e); string(got) != string(img) {
			t.Fatalf("C=%d naive image mismatch", c)
		}
	}
}

func TestAdjacentParityEntriesDoNotInterfere(t *testing.T) {
	f, _ := New(4) // entries 0..5
	f.mu.Lock()
	f.fat[2] = 0xABC // even entry
	f.fat[3] = 0x123 // odd neighbour
	f.fat[4] = 0xFFF // even entry, EOC
	f.fat[5] = 0x000 // odd neighbour stays zero
	f.mu.Unlock()

	img := f.Image()
	e := imageEntries(t, img, 6)
	want := []uint16{0xFF8, 0xFFF, 0xABC, 0x123, 0xFFF, 0x000}
	for i, w := range want {
		if e[i] != w {
			t.Fatalf("entry %d = %#03x, want %#03x; img=% x", i, e[i], w, img)
		}
	}
	if got := naiveImage(want); string(got) != string(img) {
		t.Fatalf("packed image mismatch: got % x want % x", img, got)
	}
}

func TestBadClusterVersusEOCAndSkip(t *testing.T) {
	f, _ := New(4)
	if err := f.MarkBad(3); err != nil {
		t.Fatal(err)
	}
	if err := f.MarkBad(3); !errors.Is(err, ErrClusterNotFree) {
		t.Fatalf("remark bad: %v", err)
	}
	e := imageEntries(t, f.Image(), 6)
	if e[3] != badMarker {
		t.Fatalf("entry 3 = %#x, want 0xFF7", e[3])
	}
	if f.Free() != 3 {
		t.Fatalf("free = %d, want 3", f.Free())
	}

	h, err := f.Create(3) // 2 then skip bad 3: 4,5
	if err != nil || h != 2 {
		t.Fatalf("create h=%d err=%v", h, err)
	}
	wantChain(t, f, h, []uint16{2, 4, 5})
	e = imageEntries(t, f.Image(), 6)
	if e[5] != eocMarker {
		t.Fatalf("tail = %#x want 0xFFF", e[5])
	}
	if e[3] != badMarker {
		t.Fatal("bad marker disturbed")
	}
}

func TestSpecWorkedExample(t *testing.T) {
	f, _ := New(10)
	h1, err := f.Create(3)
	if err != nil || h1 != 2 || f.Rover() != 5 {
		t.Fatalf("create1 h=%d rover=%d err=%v", h1, f.Rover(), err)
	}
	h2, err := f.Create(2)
	if err != nil || h2 != 5 || f.Rover() != 7 {
		t.Fatalf("create2 h=%d rover=%d", h2, f.Rover())
	}
	if err := f.Delete(h1); err != nil || f.Rover() != 2 {
		t.Fatalf("delete rover=%d err=%v", f.Rover(), err)
	}
	h3, err := f.Create(4)
	if err != nil || h3 != 7 || f.Rover() != 11 {
		t.Fatalf("create4 h=%d rover=%d err=%v", h3, f.Rover(), err)
	}
	wantChain(t, f, h3, []uint16{7, 8, 9, 10})
	if f.Free() != 4 {
		t.Fatalf("free = %d", f.Free())
	}

	hd, err := f.Defrag(h3)
	if err != nil || hd != 2 || f.Rover() != 8 {
		t.Fatalf("defrag h=%d rover=%d err=%v", hd, f.Rover(), err)
	}
	wantChain(t, f, hd, []uint16{2, 3, 4, 7})
	if _, err := f.Chain(h3); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("old handle still valid: %v", err)
	}
	if f.Free() != 4 {
		t.Fatalf("free after defrag = %d", f.Free())
	}
}

func TestAllocateRunAtRoverBeatsEarlierRun(t *testing.T) {
	// rover=6: free run 3,4,5 ends just before rover while run 6,7,8
	// starts exactly at rover; n=3 must take the run at rover.
	f, _ := New(10)
	f.mu.Lock()
	for _, c := range []uint16{2, 9, 10, 11} {
		f.fat[c] = eocMarker
	}
	f.rover = 6
	f.mu.Unlock()

	got := f.allocate(3)
	if fmt.Sprint(got) != "[6 7 8]" {
		t.Fatalf("allocate = %v, want [6 7 8]", got)
	}

	// Run strictly before rover: contiguous search must not reach back.
	f, _ = New(10)
	f.mu.Lock()
	for _, c := range []uint16{2, 6, 7, 9, 10, 11} {
		f.fat[c] = eocMarker
	}
	f.rover = 6
	f.mu.Unlock()
	got = f.allocate(3) // free: 3,4,5,8 -> cyclic from 6: 8, wrap->3,4
	if fmt.Sprint(got) != "[8 3 4]" {
		t.Fatalf("allocate = %v, want [8 3 4] (cyclic, no backward reach)", got)
	}
}

func TestAllocateContiguousDoesNotWrapButCyclicDoes(t *testing.T) {
	// rover=10: 10 occupied, 11 free, wraps to free 2.
	f, _ := New(10)
	f.mu.Lock()
	for c := 2; c <= 11; c++ {
		f.fat[c] = eocMarker
	}
	f.fat[11] = 0
	f.fat[2] = 0
	f.fat[5] = badMarker
	f.rover = 10
	f.mu.Unlock()

	// n=2: contiguous range [10,11] cannot hold 2 (10 occupied); cyclic
	// scans 10 no, 11 yes, wraps past C+1 to 2 yes.
	got := f.allocate(2)
	if fmt.Sprint(got) != "[11 2]" {
		t.Fatalf("allocate = %v, want [11 2] (scan wraps to 2)", got)
	}
}

func TestOccupiedAndBadBreakContiguousRun(t *testing.T) {
	f, _ := New(10)
	f.mu.Lock()
	f.fat[4] = eocMarker // occupied breaks 2,3 | 5,6
	f.fat[7] = badMarker // bad breaks 5,6 | 8,9,10,11
	f.rover = 2
	f.mu.Unlock()

	got := f.allocate(4) // 8,9,10,11 is the first length-4 run
	if fmt.Sprint(got) != "[8 9 10 11]" {
		t.Fatalf("allocate = %v, want [8 9 10 11]", got)
	}

	f, _ = New(10)
	f.mu.Lock()
	f.fat[4] = eocMarker
	f.fat[7] = badMarker
	f.fat[10] = eocMarker // splits tail: 8,9 and 11
	f.rover = 2
	f.mu.Unlock()
	got = f.allocate(3) // runs max out at length 2; cyclic from 2: 2,3,5
	if fmt.Sprint(got) != "[2 3 5]" {
		t.Fatalf("allocate = %v, want [2 3 5] (cyclic fallback)", got)
	}
}

func TestNoSpaceLeavesStateUntouched(t *testing.T) {
	f, _ := New(4)
	h, _ := f.Create(2)
	before := f.Image()
	rover := f.Rover()

	if _, err := f.Create(3); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("create: %v", err)
	}
	if err := f.Extend(h, 3); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("extend: %v", err)
	}
	if string(f.Image()) != string(before) || f.Rover() != rover {
		t.Fatal("state changed after no-space rejection")
	}

	// n<1 is invalid before the space check even when the disk is full.
	f2, _ := New(1)
	h2, _ := f2.Create(1)
	if _, err := f2.Create(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("create(0) full: %v", err)
	}
	if err := f2.Extend(h2, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("extend(0) full: %v", err)
	}
}

func TestTruncateDeleteRoverRewind(t *testing.T) {
	f, _ := New(10)
	h, _ := f.Create(4) // 2,3,4,5 rover 6
	if err := f.Truncate(h, 4); err != nil || f.Rover() != 6 {
		t.Fatalf("no-release truncate moved rover: %d", f.Rover())
	}
	if err := f.Truncate(h, 2); err != nil || f.Rover() != 4 {
		t.Fatalf("truncate rover = %d, want 4", f.Rover())
	}
	wantChain(t, f, h, []uint16{2, 3})
	if err := f.Truncate(h, 3); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("truncate past end: %v", err)
	}

	h2, _ := f.Create(3) // contiguous 4,5,6 (rover 7)
	if err := f.Delete(h2); err != nil || f.Rover() != 4 {
		t.Fatalf("delete rover = %d, want 4", f.Rover())
	}
	if err := f.Delete(h2); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("double delete: %v", err)
	}
	if err := f.Delete(h); err != nil || f.Rover() != 2 {
		t.Fatalf("delete rover = %d, want 2", f.Rover())
	}
}

func TestExtendNonAdjacentLinking(t *testing.T) {
	f, _ := New(10)
	h, _ := f.Create(2) // 2,3 rover 4
	if err := f.MarkBad(4); err != nil {
		t.Fatal(err)
	}
	// Extend by 2: contiguous from rover=4 blocked by bad 4 and 5,6?
	// free 5,6,7,... contiguous 5,6 succeeds starting at 5 (4 is bad but
	// run need not start at rover), so barrier further:
	if err := f.MarkBad(5); err != nil {
		t.Fatal(err)
	}
	if err := f.Extend(h, 2); err != nil {
		t.Fatalf("extend: %v", err)
	}
	wantChain(t, f, h, []uint16{2, 3, 6, 7})
	if f.Rover() != 8 {
		t.Fatalf("rover = %d, want 8", f.Rover())
	}

	// Cyclic fallback extension landing non-adjacent to the old tail.
	g, _ := New(6)
	x, _ := g.Create(2) // 2,3 rover 4
	y, _ := g.Create(2) // 4,5 rover 6
	_ = y
	z, _ := g.Create(2) // 6,7 rover 2
	_ = z
	if err := g.Delete(y); err != nil || g.Rover() != 2 {
		t.Fatal("setup")
	}
	// free 4,5; x occupies 2,3 and rover=4. Extend z (tail 7) by 2:
	// contiguous 4,5 from rover 4; link 7 -> 4 -> 5.
	if err := g.Extend(z, 2); err != nil {
		t.Fatalf("extend z: %v", err)
	}
	wantChain(t, g, z, []uint16{6, 7, 4, 5})
	wantChain(t, g, x, []uint16{2, 3})
}

func TestDefragAlreadyCompactNoChange(t *testing.T) {
	f, _ := New(8)
	h, _ := f.Create(3) // 2,3,4 rover 5
	imgBefore := f.Image()
	hd, err := f.Defrag(h)
	if err != nil || hd != h {
		t.Fatalf("defrag no-op h=%d err=%v", hd, err)
	}
	if f.Rover() != 5 {
		t.Fatalf("rover changed to %d", f.Rover())
	}
	if string(f.Image()) != string(imgBefore) {
		t.Fatal("image changed on no-op defrag")
	}
	wantChain(t, f, h, []uint16{2, 3, 4})
}

func TestDefragSameSetReorderedRoverUnchanged(t *testing.T) {
	// File occupies 2 and 4; free clusters 3,5,... S must reorder the same
	// set only if no free cluster is smaller. Construct a chain whose set
	// already equals the lowest-L members but whose order is reversed by
	// raw FAT surgery; free count stays identical and no cluster is freed.
	f, _ := New(6)
	f.mu.Lock()
	// file handle 2: 2 -> 4 -> 3(EOC); free 5,6,7
	f.fat[2] = 4
	f.fat[4] = 3
	f.fat[3] = eocMarker
	f.files[2] = struct{}{}
	f.rover = 6
	f.mu.Unlock()

	imgBefore := f.Image()
	hd, err := f.Defrag(2)
	if err != nil || hd != 2 {
		t.Fatalf("defrag h=%d err=%v", hd, err)
	}
	if f.Rover() != 6 {
		t.Fatalf("rover changed to %d on set-preserving defrag", f.Rover())
	}
	wantChain(t, f, hd, []uint16{2, 3, 4})
	// Only the link entries of clusters in the same set changed.
	e := imageEntries(t, f.Image(), 8)
	for c, v := range map[uint16]uint16{2: 3, 3: 4, 4: eocMarker, 5: 0, 6: 0, 7: 0} {
		if e[c] != v {
			t.Fatalf("entry %d = %#x want %#x (before % x)", c, e[c], v, imgBefore)
		}
	}
	if f.Free() != 3 {
		t.Fatalf("free = %d want 3", f.Free())
	}
}

func TestDefragMovesHandleAndSkipsBad(t *testing.T) {
	f, _ := New(10)
	if err := f.MarkBad(2); err != nil {
		t.Fatal(err)
	}
	h, _ := f.Create(3) // 3,4,5 rover 6
	// Occupy 3 again later? Instead fragment: occupy 6,7 then delete 3,4,5.
	h2, _ := f.Create(2) // 6,7 rover 8
	_ = h2
	if err := f.Delete(h); err != nil || f.Rover() != 3 {
		t.Fatal("setup")
	}
	// Now bad=2, free 3,4,5,8,9,10,11, file 6,7. Defrag skips bad 2 and
	// moves the chain to 3,4 (L=2).
	hd, err := f.Defrag(h2)
	if err != nil || hd != 3 {
		t.Fatalf("defrag h=%d err=%v", hd, err)
	}
	wantChain(t, f, hd, []uint16{3, 4})
	if f.Rover() != 3 {
		t.Fatalf("rover = %d want 3", f.Rover())
	}
	if _, err := f.Chain(h2); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("old handle valid: %v", err)
	}
	e := imageEntries(t, f.Image(), 12)
	if e[2] != badMarker {
		t.Fatalf("bad cluster moved: %#x", e[2])
	}
	if f.Free() != 7 {
		t.Fatalf("free = %d want 7", f.Free())
	}
}

func TestRejectionOrder(t *testing.T) {
	f, _ := New(4)
	// Invalid argument precedes missing-file for every checked op.
	if _, err := f.Create(0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("create: %v", err)
	}
	if err := f.Extend(99, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("extend: %v", err)
	}
	if err := f.Truncate(99, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("truncate: %v", err)
	}
	if err := f.MarkBad(1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("markbad: %v", err)
	}
	if _, err := f.Defrag(99); !errors.Is(err, ErrFileNotFound) {
		// Defrag has no invalid-argument path; unknown handle -> not found.
		t.Fatalf("defrag: %v", err)
	}
	if err := f.Extend(99, 1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("extend missing: %v", err)
	}
	if err := f.Truncate(99, 1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("truncate missing: %v", err)
	}
	if err := f.Delete(99); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := f.MarkBad(99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("markbad out of range must precede state checks: %v", err)
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	f, _ := New(200)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			var handles []uint16
			for i := 0; i < 60; i++ {
				k := (seed*7 + i) % 6
				switch k {
				case 0:
					if h, err := f.Create(1 + (seed+i)%5); err == nil {
						handles = append(handles, h)
					}
				case 1:
					if len(handles) > 0 {
						_ = f.Extend(handles[0], 1+(i%3))
					}
				case 2:
					_ = f.MarkBad(uint16(2 + (seed*13+i*7)%200))
				case 3:
					if len(handles) > 0 {
						_ = f.Truncate(handles[len(handles)-1], 1)
					}
				case 4:
					if len(handles) > 0 {
						idx := (i % len(handles))
						if _, err := f.Defrag(handles[idx]); err == nil {
						}
					}
				case 5:
					_, _ = f.Image(), f.Free()
					_ = f.Rover()
				}
			}
		}(g + 1)
	}
	wg.Wait()

	// Post-condition snapshot taken under a single lock: image matches a
	// uint16 model, free counts zero entries and every live chain is sound.
	f.mu.RLock()
	img := encodeImage(f.fat)
	e := imageEntries(t, img, 202)
	if got := naiveImage(e); string(got) != string(img) {
		t.Fatal("image encoding drifted under concurrency")
	}
	free := 0
	for c := 2; c <= 201; c++ {
		if e[c] == 0 {
			free++
		}
	}
	if free != f.freeCount() {
		t.Fatalf("free = %d but zero entries = %d", f.freeCount(), free)
	}
	live := make([]uint16, 0, len(f.files))
	for h := range f.files {
		ch, ok := f.chainOf(h)
		if !ok {
			t.Fatalf("live handle %d broken", h)
		}
		live = append(live, ch...)
	}
	seen := make(map[uint16]int)
	for _, c := range live {
		seen[c]++
		if seen[c] > 1 {
			t.Fatalf("cluster %d shared by chains", c)
		}
	}
	f.mu.RUnlock()
}
