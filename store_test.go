package doublewrite

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
)

const (
	testNumPages       = 6
	testSectorsPerPage = 4
	testBatchCap       = 6
	testSectorSize     = 128
	testPageSize       = testSectorsPerPage * testSectorSize
)

type testEnv struct {
	disk   *Disk
	store  *Store
	logBuf *bytes.Buffer
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	total := (testNumPages+testBatchCap)*testSectorsPerPage + 1
	disk := NewDisk(total, testSectorSize)
	buf := &bytes.Buffer{}
	logger := log.New(buf, "[dw] ", log.LstdFlags)
	return &testEnv{disk: disk, store: NewStore(disk, Config{
		NumPages:       testNumPages,
		SectorsPerPage: testSectorsPerPage,
		BatchCapacity:  testBatchCap,
	}, logger), logBuf: buf}
}

func mustPage(t *testing.T, pageNo, version, seed int) []byte {
	t.Helper()
	payload := make([]byte, testPageSize-headerSize-checksumSize)
	for i := range payload {
		payload[i] = byte(seed + i)
	}
	img, err := EncodePage(uint32(pageNo), uint64(version), payload, testPageSize)
	if err != nil {
		t.Fatalf("encode page %d: %v", pageNo, err)
	}
	return img
}

func pageVersion(t *testing.T, s *Store, pageNo int) (uint64, bool) {
	t.Helper()
	img := s.readInPlace(pageNo)
	info, _, err := ParsePage(img, testPageSize)
	if err != nil {
		return 0, false
	}
	return info.Version, int(info.PageNo) == pageNo
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func hasUnrecoverable(r RecoveryReport) bool {
	for _, d := range r.Decisions {
		if d.Unrecoverable {
			return true
		}
	}
	return false
}

func snapEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestPowerCutBoundary walks every sector write of one flush and asserts the
// recovered batch is atomic: every batch page ends at either its old or its
// new version, with no torn/new-old mix. Crash points cover every sector of
// every staged doublewrite page, the point before/while/after the completion
// marker, and every sector of every in-place write-back.
func TestPowerCutBoundary(t *testing.T) {
	batchPages := []int{1, 3, 4}

	baseline := newTestEnv(t)
	for p := 0; p < testNumPages; p++ {
		if err := baseline.store.Flush([][]byte{mustPage(t, p, 1, 10+p)}); err != nil {
			t.Fatalf("seed flush page %d: %v", p, err)
		}
	}
	oldSnap := baseline.disk.Snapshot()

	newImgs := map[int][]byte{}
	batch := make([][]byte, 0, len(batchPages))
	for _, p := range batchPages {
		img := mustPage(t, p, 2, 100+p)
		newImgs[p] = img
		batch = append(batch, img)
	}

	totalWrites := len(batchPages)*testSectorsPerPage + 1 + len(batchPages)*testSectorsPerPage
	for cutoff := 0; cutoff < totalWrites; cutoff++ {
		cutoff := cutoff
		t.Run(fmt.Sprintf("cut=%02d", cutoff), func(t *testing.T) {
			env := newTestEnv(t)
			for i, sec := range oldSnap {
				copy(env.disk.sectors[i], sec)
			}
			n := 0
			env.disk.BeforeSectorWrite = func(idx int) (bool, int) {
				step := n
				n++
				if step == cutoff {
					// Tear the sector mid-way so byte boundaries inside
					// every sector boundary are exercised too.
					return true, testSectorSize / 2
				}
				return false, testSectorSize
			}
			if err := env.store.Flush(batch); !errors.Is(err, ErrPowerCut) {
				t.Fatalf("cut %d: expected ErrPowerCut, got %v", cutoff, err)
			}

			report, err := env.store.Recover()
			if err != nil {
				t.Fatalf("recover at %d: %v", cutoff, err)
			}

			versions := map[int]uint64{}
			for p := 0; p < testNumPages; p++ {
				v, valid := pageVersion(t, env.store, p)
				if !valid {
					t.Fatalf("cut %d page %d corrupt after recovery", cutoff, p)
				}
				versions[p] = v
			}
			batchVer := versions[batchPages[0]]
			if batchVer != 1 && batchVer != 2 {
				t.Fatalf("cut %d unexpected version %d", cutoff, batchVer)
			}
			for _, p := range batchPages {
				if versions[p] != batchVer {
					t.Fatalf("cut %d non-atomic batch: page %d ver %d, expected %d",
						cutoff, p, versions[p], batchVer)
				}
				if batchVer == 2 && !bytes.Equal(env.store.readInPlace(p), newImgs[p]) {
					t.Fatalf("cut %d page %d new bytes mismatch", cutoff, p)
				}
			}
			for p := 0; p < testNumPages; p++ {
				if !containsInt(batchPages, p) && versions[p] != 1 {
					t.Fatalf("cut %d untouched page %d changed to ver %d", cutoff, p, versions[p])
				}
			}
			if hasUnrecoverable(report) {
				t.Fatalf("cut %d unexpectedly reports unrecoverable pages", cutoff)
			}

			// Second recovery must not change a single byte.
			before := env.disk.Snapshot()
			report2, err := env.store.Recover()
			if err != nil {
				t.Fatalf("second recover at %d: %v", cutoff, err)
			}
			if !snapEqual(before, env.disk.Snapshot()) {
				t.Fatalf("cut %d second recovery changed bytes", cutoff)
			}
			if hasUnrecoverable(report2) != hasUnrecoverable(report) {
				t.Fatalf("cut %d second recovery changed unrecoverable verdict", cutoff)
			}

			// Determinism: identical sequence + crash point => identical bytes.
			env2 := newTestEnv(t)
			for i, sec := range oldSnap {
				copy(env2.disk.sectors[i], sec)
			}
			n2 := 0
			env2.disk.BeforeSectorWrite = func(idx int) (bool, int) {
				step := n2
				n2++
				if step == cutoff {
					return true, testSectorSize / 2
				}
				return false, testSectorSize
			}
			if err := env2.store.Flush(batch); !errors.Is(err, ErrPowerCut) {
				t.Fatalf("rerun flush at %d: %v", cutoff, err)
			}
			if _, err := env2.store.Recover(); err != nil {
				t.Fatalf("rerun recover at %d: %v", cutoff, err)
			}
			if !snapEqual(env.disk.Snapshot(), env2.disk.Snapshot()) {
				t.Fatalf("cut %d nondeterministic bytes across identical runs", cutoff)
			}
		})
	}

	clean := newTestEnv(t)
	for i, sec := range oldSnap {
		copy(clean.disk.sectors[i], sec)
	}
	if err := clean.store.Flush(batch); err != nil {
		t.Fatalf("clean flush: %v", err)
	}
	report, err := clean.store.Recover()
	if err != nil {
		t.Fatalf("clean recover: %v", err)
	}
	if report.MarkerValid {
		t.Fatalf("marker must be cleared after clean flush")
	}
	for _, p := range batchPages {
		if v, valid := pageVersion(t, clean.store, p); !valid || v != 2 {
			t.Fatalf("clean flush page %d ver %d valid %v", p, v, valid)
		}
	}
	before := clean.disk.Snapshot()
	if _, err := clean.store.Recover(); err != nil {
		t.Fatalf("clean second recover: %v", err)
	}
	if !snapEqual(before, clean.disk.Snapshot()) {
		t.Fatalf("clean second recovery changed bytes")
	}
}

// TestMarkerStages checks the before/while/after marker boundary directly.
func TestMarkerStages(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.Flush([][]byte{mustPage(t, 2, 1, 7)}); err != nil {
		t.Fatal(err)
	}
	batch := [][]byte{mustPage(t, 2, 2, 9)}

	// Steps 0..3 stage the page, step 4 is the marker sector, 5..8 write back.
	stages := map[int]string{
		3: "last-staged-sector-before-marker",
		4: "marker-sector-torn",
		5: "first-inplace-sector-after-marker",
	}
	for cutoff, name := range stages {
		cutoff, name := cutoff, name
		t.Run(name, func(t *testing.T) {
			env2 := newTestEnv(t)
			for i, sec := range env.disk.Snapshot() {
				copy(env2.disk.sectors[i], sec)
			}
			n := 0
			env2.disk.BeforeSectorWrite = func(idx int) (bool, int) {
				step := n
				n++
				if step == cutoff {
					return true, 0
				}
				return false, testSectorSize
			}
			if err := env2.store.Flush(batch); !errors.Is(err, ErrPowerCut) {
				t.Fatalf("%s: %v", name, err)
			}
			report, err := env2.store.Recover()
			if err != nil {
				t.Fatalf("%s recover: %v", name, err)
			}
			v, _ := pageVersion(t, env2.store, 2)
			switch cutoff {
			case 3, 4:
				if report.MarkerValid || v != 1 {
					t.Fatalf("%s must ignore batch: marker=%v ver=%d", name, report.MarkerValid, v)
				}
			case 5:
				if !report.MarkerValid || v != 2 {
					t.Fatalf("%s must roll forward: marker=%v ver=%d", name, report.MarkerValid, v)
				}
			}
		})
	}
}

// TestOldDoubleWriteResidueNeverRollsBack leaves a valid marker pointing at an
// older staged copy while in place already holds a newer page.
func TestOldDoubleWriteResidueNeverRollsBack(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.writeDWA(0, mustPage(t, 0, 1, 11)); err != nil {
		t.Fatal(err)
	}
	if err := env.disk.WriteSector(env.store.markerSec, env.store.encodeMarker(1)); err != nil {
		t.Fatal(err)
	}
	if err := env.store.writeInPlace(0, mustPage(t, 0, 3, 33)); err != nil {
		t.Fatal(err)
	}
	report, err := env.store.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !report.MarkerValid {
		t.Fatalf("marker should be valid")
	}
	if v, valid := pageVersion(t, env.store, 0); !valid || v != 3 {
		t.Fatalf("older residue must not roll page back, got ver %d valid %v", v, valid)
	}
	for _, d := range report.Decisions {
		if d.Action == "roll-forward" {
			t.Fatalf("page with newer in-place version must not be overwritten")
		}
	}
	if _, err := env.store.readMarker(); !errors.Is(err, ErrMarkerCorrupt) {
		t.Fatalf("marker should be cleared after recovery, got %v", err)
	}
}

// TestSilentCorruptionUnrecoverable injects silent corruption into an in-place
// page with no committed (valid-marker) copy and asserts it is unrecoverable:
// bytes untouched, version counted as zero.
func TestSilentCorruptionUnrecoverable(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.Flush([][]byte{mustPage(t, 0, 1, 5)}); err != nil {
		t.Fatal(err)
	}
	// Crash before the marker: staged area is residue and must be ignored.
	n := 0
	env.disk.BeforeSectorWrite = func(idx int) (bool, int) {
		step := n
		n++
		if step == 2 {
			return true, 10
		}
		return false, testSectorSize
	}
	if err := env.store.Flush([][]byte{mustPage(t, 0, 2, 6)}); !errors.Is(err, ErrPowerCut) {
		t.Fatalf("expected power cut, got %v", err)
	}

	before := env.store.readInPlace(0)
	if err := env.store.CorruptInPlacePage(0, 1, 7); err != nil {
		t.Fatal(err)
	}
	report, err := env.store.Recover()
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.MarkerValid {
		t.Fatalf("pre-marker residue must yield an invalid marker")
	}
	var decision *PageDecision
	for i := range report.Decisions {
		if report.Decisions[i].PageNo == 0 {
			decision = &report.Decisions[i]
		}
	}
	if decision == nil || !decision.Unrecoverable || decision.InPlaceVer != 0 {
		t.Fatalf("page 0 must be unrecoverable with version 0, got %+v", decision)
	}
	// Recovery must never guess: the injected corruption stays as-is.
	got := env.store.readInPlace(0)
	before[1*testSectorSize+7] ^= 0xFF
	if !bytes.Equal(got, before) {
		t.Fatalf("unrecoverable page bytes must be left untouched")
	}
	// Second recovery keeps the same verdict and the same bytes.
	snap := env.disk.Snapshot()
	report2, err := env.store.Recover()
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if !snapEqual(snap, env.disk.Snapshot()) || !hasUnrecoverable(report2) {
		t.Fatalf("second recovery must be byte-stable and keep the unrecoverable verdict")
	}
}

// TestCorruptCopyWithValidInPlace covers a corrupt doublewrite slot while the
// page is already valid in place (write-back completed for that page).
func TestCorruptCopyWithValidInPlace(t *testing.T) {
	env := newTestEnv(t)
	good := mustPage(t, 0, 2, 6)
	if err := env.store.writeInPlace(0, good); err != nil {
		t.Fatal(err)
	}
	// Marker says one page is committed, but the staged copy is torn.
	if err := env.store.CorruptDWAPage(0, 0, 3); err != nil {
		t.Fatal(err)
	}
	if err := env.disk.WriteSector(env.store.markerSec, env.store.encodeMarker(1)); err != nil {
		t.Fatal(err)
	}
	report, err := env.store.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if !report.MarkerValid || hasUnrecoverable(report) {
		t.Fatalf("valid in-place page must not be unrecoverable: %+v", report)
	}
	if !bytes.Equal(env.store.readInPlace(0), good) {
		t.Fatalf("valid in-place page must be left intact")
	}
}

func TestValidationRejectionWritesNothing(t *testing.T) {
	cases := []struct {
		name  string
		batch func(t *testing.T) [][]byte
		want  error
	}{
		{"empty", func(t *testing.T) [][]byte { return nil }, ErrEmptyBatch},
		{"too-large", func(t *testing.T) [][]byte {
			pages := [][]byte{}
			for i := 0; i < testBatchCap+1; i++ {
				pages = append(pages, mustPage(t, i, 1, i))
			}
			return pages
		}, ErrBatchTooLarge},
		{"duplicate", func(t *testing.T) [][]byte {
			return [][]byte{mustPage(t, 1, 1, 1), mustPage(t, 1, 2, 2)}
		}, ErrDuplicatePage},
		{"out-of-range-high", func(t *testing.T) [][]byte {
			return [][]byte{mustPage(t, testNumPages, 1, 1)}
		}, ErrPageOutOfRange},
		{"version-not-newer", func(t *testing.T) [][]byte {
			return [][]byte{mustPage(t, 0, 1, 1)}
		}, ErrVersionNotNewer},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			// Seed page 0 at version 1 for the version-not-newer case.
			if err := env.store.Flush([][]byte{mustPage(t, 0, 1, 1)}); err != nil {
				t.Fatal(err)
			}
			before := env.disk.Snapshot()
			anyWrite := false
			env.disk.BeforeSectorWrite = func(idx int) (bool, int) {
				anyWrite = true
				return false, testSectorSize
			}
			err := env.store.Flush(tc.batch(t))
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if anyWrite {
				t.Fatalf("rejected batch must not write any sector")
			}
			if !snapEqual(before, env.disk.Snapshot()) {
				t.Fatalf("rejected batch must not change any byte")
			}
		})
	}
}

func TestOverwriteCorruptInPlaceAccepted(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.Flush([][]byte{mustPage(t, 0, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.CorruptInPlacePage(0, 0, 9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := env.store.ReadPage(0); !errors.Is(err, ErrPageCorrupt) {
		t.Fatalf("want ErrPageCorrupt, got %v", err)
	}
	if err := env.store.Flush([][]byte{mustPage(t, 0, 2, 2)}); err != nil {
		t.Fatalf("new version must replace corrupt in-place page: %v", err)
	}
	if v, valid := pageVersion(t, env.store, 0); !valid || v != 2 {
		t.Fatalf("page should be repaired at ver 2, got ver %d valid %v", v, valid)
	}
}

func TestConcurrentReadersAndSerialBatches(t *testing.T) {
	env := newTestEnv(t)
	for p := 0; p < testNumPages; p++ {
		if err := env.store.Flush([][]byte{mustPage(t, p, 1, p)}); err != nil {
			t.Fatal(err)
		}
	}
	const batches = 20
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for p := 0; p < testNumPages; p++ {
					_, info, err := env.store.ReadPage(p)
					if err == nil && info.Version < 1 {
						t.Errorf("reader saw impossible version")
						return
					}
				}
			}
		}
	}()
	for v := 2; v <= batches+1; v++ {
		pages := make([][]byte, testNumPages)
		for p := 0; p < testNumPages; p++ {
			pages[p] = mustPage(t, p, v, v*100+p)
		}
		if err := env.store.Flush(pages); err != nil {
			t.Fatalf("batch ver %d: %v", v, err)
		}
	}
	close(stop)
	wg.Wait()
	for p := 0; p < testNumPages; p++ {
		payload, info, err := env.store.ReadPage(p)
		if err != nil {
			t.Fatalf("page %d: %v", p, err)
		}
		if info.Version != batches+1 {
			t.Fatalf("page %d final ver %d want %d", p, info.Version, batches+1)
		}
		want := make([]byte, testPageSize-headerSize-checksumSize)
		for i := range want {
			want[i] = byte((batches+1)*100 + p + i)
		}
		if !bytes.Equal(payload, want) {
			t.Fatalf("page %d payload mismatch", p)
		}
	}
}

func TestDecisionLogging(t *testing.T) {
	env := newTestEnv(t)
	if err := env.store.Flush([][]byte{mustPage(t, 0, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	n := 0
	env.disk.BeforeSectorWrite = func(idx int) (bool, int) {
		step := n
		n++
		if step == testSectorsPerPage {
			return true, 0
		}
		return false, testSectorSize
	}
	_ = env.store.Flush([][]byte{mustPage(t, 0, 2, 2)})
	if _, err := env.store.Recover(); err != nil {
		t.Fatal(err)
	}
	logText := env.logBuf.String()
	for _, want := range []string{"input:", "output:", "decision:"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}
