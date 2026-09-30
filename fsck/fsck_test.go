package fsck

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// --- test helpers ---------------------------------------------------------

func mkImage(t *testing.T, inodeCount, totalBlocks uint32) *Image {
	t.Helper()
	img, err := Format(512, totalBlocks, inodeCount)
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return img
}

func addFile(img *Image, ino uint32, blocks ...uint32) {
	img.Inodes[ino] = Inode{Type: InodeFile, Nlink: 1, Size: uint32(len(blocks)) * img.SB.BlockSize}
	copy(img.Inodes[ino].Blocks[:], blocks)
}

func addDir(img *Image, ino uint32) {
	img.Inodes[ino] = Inode{Type: InodeDir, Nlink: 1}
}

func mustAddEntry(t *testing.T, img *Image, dir uint32, name string, target uint32) {
	t.Helper()
	if err := img.addEntry(dir, DirEntry{Inode: target, Name: name}); err != nil {
		t.Fatalf("addEntry dir=%d name=%q: %v", dir, name, err)
	}
}

func setContent(img *Image, block uint32, s string) {
	data := img.BlockData(block)
	for i := range data {
		data[i] = 0
	}
	copy(data, s)
}

func writeVol(t *testing.T, img *Image) *Volume {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fs.img")
	if err := os.WriteFile(path, img.Serialize(), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	return OpenVolume(path)
}

func logReport(t *testing.T, label string, rep *Report) {
	t.Helper()
	t.Logf("%s: %d finding(s)", label, len(rep.Findings))
	for _, f := range rep.Findings {
		t.Logf("  finding: %s", f)
	}
}

func logActions(t *testing.T, label string, log *RepairLog) {
	t.Helper()
	t.Logf("%s: %d action(s), dataLoss=%t", label, len(log.Actions), log.DataLoss)
	for _, a := range log.Actions {
		t.Logf("  action: %s", a)
	}
}

func repairAndRecheck(t *testing.T, img *Image) *RepairLog {
	t.Helper()
	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check before repair: %v", err)
	}
	logReport(t, "input image findings (basis for repair)", rep)
	log, err := Repair(img)
	if err != nil {
		t.Fatalf("repair: %v", err)
	}
	logActions(t, "repair output", log)
	after, err := Check(img)
	if err != nil {
		t.Fatalf("check after repair: %v", err)
	}
	logReport(t, "post-repair check", after)
	if !after.Clean() {
		t.Fatalf("post-repair check not clean:\n%s", after.String())
	}
	return log
}

func hasFinding(rep *Report, cat Category, inode uint32) bool {
	for _, f := range rep.Findings {
		if f.Category == cat && f.Inode == inode {
			return true
		}
	}
	return false
}

// --- shared blocks --------------------------------------------------------

func TestSharedBlockCopied(t *testing.T) {
	img := mkImage(t, 8, 32)
	addFile(img, 1, 4)
	addFile(img, 2, 4) // inode 2 shares block 4 with inode 1
	setContent(img, 4, "shared-payload")
	mustAddEntry(t, img, RootInode, "a", 1)
	mustAddEntry(t, img, RootInode, "b", 2)

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: inodes 1 and 2 share block 4", rep)
	if !hasFinding(rep, CatSharedBlock, 1) {
		t.Fatalf("expected shared-block finding for inode 1")
	}

	repairAndRecheck(t, img)

	if img.Inodes[1].Blocks[0] != 4 {
		t.Fatalf("lowest inode 1 must keep block 4, got %d", img.Inodes[1].Blocks[0])
	}
	got := img.Inodes[2].Blocks[0]
	if got == 0 || got == 4 {
		t.Fatalf("inode 2 must receive a fresh block, got %d", got)
	}
	if !bytes.Equal(img.BlockData(got), img.BlockData(4)) {
		t.Fatalf("copied block content mismatch")
	}
	t.Logf("judgement: inode 1 keeps block 4, inode 2 received block %d with identical content", got)
}

func TestSharedBlockNoFreeTruncate(t *testing.T) {
	// dataStart = 3, data blocks 3..7; block 3 holds the root directory,
	// inode 1 consumes 4..7 and inode 2 shares block 7, so no free block
	// remains for a copy.
	img := mkImage(t, 4, 8)
	addFile(img, 1, 4, 5, 6, 7)
	addFile(img, 2, 7)
	mustAddEntry(t, img, RootInode, "a", 1)
	mustAddEntry(t, img, RootInode, "b", 2)

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: inode 2 shares block 7, all data blocks used", rep)
	if !hasFinding(rep, CatSharedBlock, 1) {
		t.Fatalf("expected shared-block finding")
	}

	log := repairAndRecheck(t, img)
	if !log.DataLoss {
		t.Fatalf("expected data loss to be reported")
	}
	if countBlocks(&img.Inodes[2]) != 0 {
		t.Fatalf("inode 2 block list must be truncated empty, got %v", img.Inodes[2].Blocks)
	}
	if img.Inodes[2].Size != 0 {
		t.Fatalf("inode 2 size must be clamped to 0, got %d", img.Inodes[2].Size)
	}
	t.Logf("judgement: no free block -> inode 2 truncated, data loss reported")
}

// --- unreachable inodes ---------------------------------------------------

func TestUnreachableDirCycle(t *testing.T) {
	img := mkImage(t, 8, 32)
	addDir(img, 3)
	addDir(img, 4)
	mustAddEntry(t, img, 3, "x", 4)
	mustAddEntry(t, img, 4, "y", 3) // cycle 3 <-> 4, unreachable from root

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: dirs 3 and 4 form an unreachable cycle", rep)
	if !hasFinding(rep, CatUnreachableInode, 3) || !hasFinding(rep, CatUnreachableInode, 4) {
		t.Fatalf("expected unreachable-inode findings for 3 and 4")
	}

	repairAndRecheck(t, img)

	rec, ok := img.findEntry(RootInode, RecoveryDirName)
	if !ok {
		t.Fatalf("recovery dir was not created")
	}
	e, ok := img.findEntry(rec.Inode, "3")
	if !ok || e.Inode != 3 {
		t.Fatalf("cycle minimum inode 3 must be attached to recovery dir, got %+v", e)
	}
	if _, ok := img.findEntry(rec.Inode, "4"); ok {
		t.Fatalf("only the cycle minimum must be attached, not inode 4")
	}
	reach := img.reachable()
	if !reach[3] || !reach[4] {
		t.Fatalf("both cycle members must be reachable after repair")
	}
	t.Logf("judgement: cycle min 3 attached under %q as %q, 4 reachable via 3", RecoveryDirName, "3")
}

func TestUnreachableEmptyFreedAndContentAttached(t *testing.T) {
	img := mkImage(t, 8, 32)
	addFile(img, 2, 5) // unreachable file with content -> attach
	setContent(img, 5, "recovered")
	addFile(img, 3) // unreachable empty file -> freed
	addDir(img, 4)  // unreachable empty dir -> freed

	repairAndRecheck(t, img)

	rec, ok := img.findEntry(RootInode, RecoveryDirName)
	if !ok {
		t.Fatalf("recovery dir was not created")
	}
	if e, ok := img.findEntry(rec.Inode, "2"); !ok || e.Inode != 2 {
		t.Fatalf("content-bearing inode 2 must be attached, got %+v", e)
	}
	if img.Inodes[3].Type != InodeFree || img.Inodes[4].Type != InodeFree {
		t.Fatalf("empty inodes 3 and 4 must be freed")
	}
	t.Logf("judgement: inode 2 attached by name, empty inodes 3,4 freed")
}

// --- directory entry inconsistencies --------------------------------------

func TestMultiParentDir(t *testing.T) {
	img := mkImage(t, 8, 32)
	addDir(img, 1)
	addDir(img, 2)
	addDir(img, 3)
	mustAddEntry(t, img, RootInode, "d1", 1)
	mustAddEntry(t, img, RootInode, "d2", 2)
	mustAddEntry(t, img, 1, "sub", 3)
	mustAddEntry(t, img, 2, "sub", 3) // dir 3 collected by parents 1 and 2

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: dir 3 collected by parents 1 and 2", rep)
	if !hasFinding(rep, CatMultiParentDir, 3) {
		t.Fatalf("expected multi-parent-dir finding for inode 3")
	}

	repairAndRecheck(t, img)

	if _, ok := img.findEntry(1, "sub"); !ok {
		t.Fatalf("entry must be kept in lowest parent 1")
	}
	if _, ok := img.findEntry(2, "sub"); ok {
		t.Fatalf("entry must be removed from parent 2")
	}
	t.Logf("judgement: only lowest parent 1 keeps the entry for dir 3")
}

func TestDanglingDirent(t *testing.T) {
	img := mkImage(t, 8, 32)
	mustAddEntry(t, img, RootInode, "ghost", 5) // inode 5 is free

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: root entry ghost -> free inode 5", rep)
	if !hasFinding(rep, CatDanglingDirent, RootInode) {
		t.Fatalf("expected dangling-dirent finding")
	}

	repairAndRecheck(t, img)

	if _, ok := img.findEntry(RootInode, "ghost"); ok {
		t.Fatalf("dangling entry must be removed")
	}
	t.Logf("judgement: dangling entry removed from root")
}

// --- bitmap ---------------------------------------------------------------

func TestBitmapMismatchOnly(t *testing.T) {
	img := mkImage(t, 8, 32)
	addFile(img, 1, 4)
	mustAddEntry(t, img, RootInode, "a", 1)
	img.Bitmap = img.expectedBitmap()
	img.Bitmap[9] = true // free block wrongly marked used

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: only bitmap bit 9 is wrong", rep)
	if len(rep.Findings) != 1 || rep.Findings[0].Category != CatBitmapMismatch || rep.Findings[0].Block != 9 {
		t.Fatalf("expected exactly one bitmap-mismatch finding for block 9, got %v", rep.Findings)
	}

	repairAndRecheck(t, img)
	if !img.Bitmap[4] || img.Bitmap[9] {
		t.Fatalf("bitmap not rebuilt from actual references")
	}
	t.Logf("judgement: bitmap rebuilt, bit 9 cleared, bit 4 kept")
}

// --- combined repair ------------------------------------------------------

func TestRepairThenRecheckClean(t *testing.T) {
	img := mkImage(t, 16, 32)
	// shared block
	addFile(img, 1, 4)
	addFile(img, 2, 4)
	mustAddEntry(t, img, RootInode, "a", 1)
	mustAddEntry(t, img, RootInode, "b", 2)
	// dangling entry
	mustAddEntry(t, img, RootInode, "ghost", 10)
	// unreachable file with content
	addFile(img, 5, 6)
	setContent(img, 6, "lost-data")
	// bad link count
	img.Inodes[1].Nlink = 7
	// bitmap corruption
	img.Bitmap[4] = false
	img.Bitmap[6] = false
	img.Bitmap[12] = true

	rep, err := Check(img)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	logReport(t, "input: combined corruptions", rep)
	for _, cat := range []Category{CatSharedBlock, CatDanglingDirent, CatUnreachableInode, CatBadLinkCount, CatBitmapMismatch} {
		found := false
		for _, f := range rep.Findings {
			if f.Category == cat {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a finding of category %s", cat)
		}
	}

	log := repairAndRecheck(t, img)
	_ = log

	if img.Inodes[1].Nlink != 1 || img.Inodes[2].Nlink != 1 {
		t.Fatalf("link counts not corrected: %d %d", img.Inodes[1].Nlink, img.Inodes[2].Nlink)
	}
	rec, ok := img.findEntry(RootInode, RecoveryDirName)
	if !ok {
		t.Fatalf("recovery dir missing")
	}
	if e, ok := img.findEntry(rec.Inode, "5"); !ok || e.Inode != 5 {
		t.Fatalf("unreachable inode 5 must be attached, got %+v", e)
	}
	t.Logf("judgement: all categories repaired, recheck zero findings")
}
