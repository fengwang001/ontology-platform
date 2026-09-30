package fsck

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// --- rejections -----------------------------------------------------------

func TestRejectCorruptHeader(t *testing.T) {
	img := mkImage(t, 8, 32)
	raw := img.Serialize()
	raw[0] = 'X' // break magic
	path := filepath.Join(t.TempDir(), "fs.img")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	v := OpenVolume(path)
	if _, err := v.Check(); !errors.Is(err, ErrCorruptHeader) {
		t.Fatalf("check: expected ErrCorruptHeader, got %v", err)
	}
	if _, _, err := v.Repair(RepairOptions{}); !errors.Is(err, ErrCorruptHeader) {
		t.Fatalf("repair: expected ErrCorruptHeader, got %v", err)
	}
	t.Logf("judgement: broken magic -> ErrCorruptHeader, image untouched")

	// Broken checksum (layout field modified) must also be rejected.
	raw2 := img.Serialize()
	raw2[16] ^= 0xFF
	if err := os.WriteFile(path, raw2, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenVolume(path).Check(); !errors.Is(err, ErrCorruptHeader) {
		t.Fatalf("check: expected ErrCorruptHeader for bad checksum, got %v", err)
	}
	t.Logf("judgement: corrupted layout field breaks checksum -> ErrCorruptHeader")
}

func TestRejectRootNotDir(t *testing.T) {
	img := mkImage(t, 8, 32)
	img.Inodes[RootInode] = Inode{Type: InodeFile, Nlink: 1}
	v := writeVol(t, img)
	if _, err := v.Check(); !errors.Is(err, ErrRootNotDir) {
		t.Fatalf("check: expected ErrRootNotDir, got %v", err)
	}
	if _, _, err := v.Repair(RepairOptions{}); !errors.Is(err, ErrRootNotDir) {
		t.Fatalf("repair: expected ErrRootNotDir, got %v", err)
	}
	t.Logf("judgement: root inode is a file -> ErrRootNotDir")
}

func TestRejectRecoveryNameConflict(t *testing.T) {
	img := mkImage(t, 8, 32)
	addFile(img, 1, 4) // non-directory occupying the recovery name
	mustAddEntry(t, img, RootInode, RecoveryDirName, 1)
	addFile(img, 2, 5) // unreachable file with content, needs recovery dir
	setContent(img, 5, "x")
	img.Bitmap[4] = true
	img.Bitmap[5] = true

	path := filepath.Join(t.TempDir(), "fs.img")
	before := img.Serialize()
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := OpenVolume(path).Repair(RepairOptions{})
	if !errors.Is(err, ErrRecoveryNameConflict) {
		t.Fatalf("expected ErrRecoveryNameConflict, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected repair must leave the image byte-identical")
	}
	t.Logf("judgement: %q held by a file -> ErrRecoveryNameConflict, image unchanged", RecoveryDirName)
}

func TestRejectNoFreeInode(t *testing.T) {
	img := mkImage(t, 2, 16) // only root + one inode
	addFile(img, 1, img.SB.DataStart)
	setContent(img, img.SB.DataStart, "x")
	img.Bitmap[img.SB.DataStart] = true

	path := filepath.Join(t.TempDir(), "fs.img")
	before := img.Serialize()
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := OpenVolume(path).Repair(RepairOptions{})
	if !errors.Is(err, ErrNoFreeInode) {
		t.Fatalf("expected ErrNoFreeInode, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("rejected repair must leave the image byte-identical")
	}
	t.Logf("judgement: recovery dir needed but no free inode -> ErrNoFreeInode, image unchanged")
}

func TestRejectMounted(t *testing.T) {
	img := mkImage(t, 8, 32)
	v := writeVol(t, img)
	m, err := v.MountWrite()
	if err != nil {
		t.Fatalf("mount: %v", err)
	}
	if _, _, err := v.Repair(RepairOptions{}); !errors.Is(err, ErrMounted) {
		t.Fatalf("repair during write mount: expected ErrMounted, got %v", err)
	}
	if _, err := v.MountWrite(); !errors.Is(err, ErrMounted) {
		t.Fatalf("second write mount: expected ErrMounted, got %v", err)
	}
	m.Close()
	if _, _, err := v.Repair(RepairOptions{}); err != nil {
		t.Fatalf("repair after unmount: %v", err)
	}
	t.Logf("judgement: repair and write mount are mutually exclusive")
}

// --- interruption ---------------------------------------------------------

func TestInterruptDuringRepair(t *testing.T) {
	for _, failAt := range []string{"mid-write", "before-rename"} {
		t.Run(failAt, func(t *testing.T) {
			img := mkImage(t, 8, 32)
			addFile(img, 1, 4)
			addFile(img, 2, 4) // shared block -> repair has work to do
			mustAddEntry(t, img, RootInode, "a", 1)
			mustAddEntry(t, img, RootInode, "b", 2)

			dir := t.TempDir()
			path := filepath.Join(dir, "fs.img")
			before := img.Serialize()
			if err := os.WriteFile(path, before, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := OpenVolume(path).Repair(RepairOptions{FailAt: failAt})
			if !errors.Is(err, ErrInjected) {
				t.Fatalf("expected ErrInjected, got %v", err)
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatalf("interrupted repair (%s) must leave the image byte-identical", failAt)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 1 {
				t.Fatalf("temporary files must be cleaned up, found %d entries", len(entries))
			}
			t.Logf("judgement: injected failure at %s -> original image byte-identical, no temp files", failAt)
		})
	}
}

// --- concurrency ------------------------------------------------------------

func TestConcurrentCheck(t *testing.T) {
	img := mkImage(t, 16, 32)
	addFile(img, 1, 4)
	addFile(img, 2, 4)
	mustAddEntry(t, img, RootInode, "a", 1)
	mustAddEntry(t, img, RootInode, "b", 2)
	mustAddEntry(t, img, RootInode, "ghost", 9)
	v := writeVol(t, img)

	base, err := v.Check()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	want := base.String()
	t.Logf("input findings (baseline):\n%s", want)

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rep, err := v.Check()
			if err != nil {
				errs <- err
				return
			}
			if rep.String() != want {
				errs <- errors.New("concurrent check returned a different report")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent check: %v", err)
	}
	t.Logf("judgement: %d concurrent read-only checks all returned the identical report", n)
}

// --- determinism ------------------------------------------------------------

func TestDeterminism(t *testing.T) {
	build := func() *Image {
		img := mkImage(t, 16, 32)
		addFile(img, 1, 4)
		addFile(img, 2, 4)
		mustAddEntry(t, img, RootInode, "a", 1)
		mustAddEntry(t, img, RootInode, "b", 2)
		mustAddEntry(t, img, RootInode, "ghost", 10)
		addFile(img, 5, 6)
		setContent(img, 6, "lost-data")
		img.Inodes[1].Nlink = 7
		img.Bitmap[4] = false
		img.Bitmap[6] = false
		img.Bitmap[12] = true
		return img
	}

	dir := t.TempDir()
	var repaired [][]byte
	var reports []string
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, string(rune('a'+i))+".img")
		if err := os.WriteFile(path, build().Serialize(), 0o644); err != nil {
			t.Fatal(err)
		}
		v := OpenVolume(path)
		rep, _, err := v.Repair(RepairOptions{})
		if err != nil {
			t.Fatalf("repair %d: %v", i, err)
		}
		reports = append(reports, rep.String())
		raw, _ := os.ReadFile(path)
		repaired = append(repaired, raw)

		// Rechecking the repaired image must be clean.
		after, err := v.Check()
		if err != nil {
			t.Fatalf("recheck %d: %v", i, err)
		}
		if !after.Clean() {
			t.Fatalf("recheck %d not clean:\n%s", i, after.String())
		}
	}
	for i := 1; i < 3; i++ {
		if !bytes.Equal(repaired[0], repaired[i]) {
			t.Fatalf("repaired images differ between runs 0 and %d", i)
		}
		if reports[0] != reports[i] {
			t.Fatalf("finding lists differ between runs 0 and %d", i)
		}
	}
	t.Logf("judgement: 3 identical corrupt images -> byte-identical repairs and finding lists")
	t.Logf("repaired findings baseline:\n%s", reports[0])
}
