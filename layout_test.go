package ontology

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestAttributeExactFitAndOneByteOverflow(t *testing.T) {
	m, err := NewManager(16, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 5)); err != nil {
		t.Fatal(err)
	}
	info, err := m.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != Inline || info.DataBlocks != 0 || info.ExtID != 0 {
		t.Fatalf("unexpected stat: %+v", info)
	}
	if len(info.Xattrs) != 1 || info.Xattrs[0].Location != InInode {
		t.Fatalf("attribute should be in inode: %+v", info.Xattrs)
	}

	if err := m.Resize(file, 1); err != nil {
		t.Fatal(err)
	}
	info, err = m.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != Inline || info.ExtID != file || info.ExternalBytes != 16 || m.UsedBlocks() != 1 {
		t.Fatalf("attribute should move external: info=%+v used=%d", info, m.UsedBlocks())
	}
	if info.Xattrs[0].Location != External {
		t.Fatalf("attribute should be external: %+v", info.Xattrs)
	}
}

func TestPlacementStopsAtFirstAttributeThatDoesNotFit(t *testing.T) {
	m, err := NewManager(20, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(file, 13); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(file, "a", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(file, "b", nil); err != nil {
		t.Fatal(err)
	}

	info, err := m.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.ExternalBytes != 16 || len(info.Xattrs) != 2 {
		t.Fatalf("both attributes should be external: %+v", info)
	}
	for _, xattr := range info.Xattrs {
		if xattr.Location != External {
			t.Fatalf("%s should be external", xattr.Name)
		}
	}
}

func TestModeTransitionsAndReturnToInlineAtZero(t *testing.T) {
	m, err := NewManager(16, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 5)); err != nil {
		t.Fatal(err)
	}

	for _, size := range []int64{1, 16} {
		if err := m.Resize(file, size); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		info, err := m.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode != Inline || info.ExtID != file {
			t.Fatalf("size %d should stay inline with external attr: %+v", size, info)
		}
	}

	if err := m.Resize(file, 17); err != nil {
		t.Fatal(err)
	}
	info, err := m.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode != Block || info.DataBlocks != 3 {
		t.Fatalf("size 17 should use three data blocks: %+v", info)
	}

	if err := m.Resize(file, 16); err != nil {
		t.Fatal(err)
	}
	info, _ = m.Stat(file)
	if info.Mode != Block || info.DataBlocks != 2 {
		t.Fatalf("shrink to A should remain block: %+v", info)
	}

	if err := m.Resize(file, 0); err != nil {
		t.Fatal(err)
	}
	info, _ = m.Stat(file)
	if info.Mode != Inline || info.DataBlocks != 0 || info.ExtID != 0 || info.Xattrs[0].Location != InInode {
		t.Fatalf("zero should restore inline placement: %+v", info)
	}
}

func TestExternalCapacityExactAndOneByteOver(t *testing.T) {
	m, err := NewManager(16, 24, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(file, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 16)); err != nil {
		t.Fatal(err)
	}
	info, _ := m.Stat(file)
	if info.ExternalBytes != 24 {
		t.Fatalf("external bytes = %d, want 24", info.ExternalBytes)
	}

	small, err := NewManager(16, 23, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	other, err := small.Create()
	if err != nil {
		t.Fatal(err)
	}
	if err := small.Resize(other, 1); err != nil {
		t.Fatal(err)
	}
	if err := small.SetXattr(other, "a", bytes.Repeat([]byte("v"), 16)); !errors.Is(err, ErrAttributeTooBig) {
		t.Fatalf("error = %v, want ErrAttributeTooBig", err)
	}
}

func TestExternalBlockShareSplitAndRelease(t *testing.T) {
	m, err := NewManager(16, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := m.Create()
	second, _ := m.Create()
	for _, id := range []int{first, second} {
		if err := m.Resize(id, 1); err != nil {
			t.Fatal(err)
		}
		if err := m.SetXattr(id, "a", []byte("12345")); err != nil {
			t.Fatal(err)
		}
	}
	if m.UsedBlocks() != 1 {
		t.Fatalf("shared ext usage = %d, want 1", m.UsedBlocks())
	}
	info, _ := m.Stat(second)
	if info.ExtID != first {
		t.Fatalf("ExtID = %d, want %d", info.ExtID, first)
	}

	if err := m.SetXattr(second, "a", []byte("different")); err != nil {
		t.Fatal(err)
	}
	if m.UsedBlocks() != 2 {
		t.Fatalf("split ext usage = %d, want 2", m.UsedBlocks())
	}

	if err := m.RemoveXattr(second, "a"); err != nil {
		t.Fatal(err)
	}
	if m.UsedBlocks() != 1 {
		t.Fatalf("one ext remains: usage = %d, want 1", m.UsedBlocks())
	}
	if err := m.Resize(first, 0); err != nil {
		t.Fatal(err)
	}
	if m.UsedBlocks() != 0 {
		t.Fatalf("last ext release: usage = %d, want 0", m.UsedBlocks())
	}
}

func TestCloneSharesExternalBlockButNotDataBlocks(t *testing.T) {
	m, err := NewManager(16, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := m.Create()
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 9)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(file, 17); err != nil {
		t.Fatal(err)
	}
	before := m.UsedBlocks()
	clone, err := m.Clone(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.UsedBlocks(); got != before+3 {
		t.Fatalf("clone usage = %d, want %d", got, before+3)
	}
	info, _ := m.Stat(clone)
	if info.ExtID != file || info.Mode != Block || info.DataBlocks != 3 {
		t.Fatalf("clone stat = %+v", info)
	}
}

func TestPoolExactShortByOneAndSharedClone(t *testing.T) {
	m, err := NewManager(16, 64, 8, 7)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := m.Create()
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 9)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(file, 17); err != nil {
		t.Fatal(err)
	}
	if m.UsedBlocks() != 4 {
		t.Fatalf("usage = %d, want 4", m.UsedBlocks())
	}

	clone, err := m.Clone(file)
	if err != nil {
		t.Fatal(err)
	}
	if m.UsedBlocks() != 7 {
		t.Fatalf("shared clone should exactly fill pool: clone=%d usage=%d", clone, m.UsedBlocks())
	}
}

func TestDistinctExternalBlockShortByOne(t *testing.T) {
	m, err := NewManager(16, 64, 8, 7)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := m.Create()
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("v"), 9)); err != nil {
		t.Fatal(err)
	}
	if err := m.Resize(file, 17); err != nil {
		t.Fatal(err)
	}
	other, _ := m.Create()
	if err := m.Resize(other, 17); err != nil {
		t.Fatal(err)
	}
	err = m.SetXattr(other, "a", bytes.Repeat([]byte("w"), 9))
	if !errors.Is(err, ErrPoolFull) {
		t.Fatalf("distinct ext should be short by one, got %v", err)
	}
	if got := m.UsedBlocks(); got != 7 {
		t.Fatalf("rejected set usage = %d, want 7", got)
	}
}

func TestOverwriteAndRejectionsDoNotChangeState(t *testing.T) {
	m, err := NewManager(16, 64, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	file, _ := m.Create()
	original := bytes.Repeat([]byte("o"), 5)
	if err := m.SetXattr(file, "a", original); err != nil {
		t.Fatal(err)
	}
	if err := m.SetXattr(file, "a", bytes.Repeat([]byte("n"), 9)); err != nil {
		t.Fatal(err)
	}
	value, err := m.GetXattr(file, "a")
	if err != nil || !bytes.Equal(value, bytes.Repeat([]byte("n"), 9)) {
		t.Fatalf("overwrite failed value=%q err=%v", value, err)
	}
	value[0] = 'X'
	valueAgain, err := m.GetXattr(file, "a")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(valueAgain, bytes.Repeat([]byte("n"), 9)) {
		t.Fatalf("GetXattr returned mutable storage: %q", valueAgain)
	}

	if err := m.Resize(99, 1); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing file error = %v", err)
	}
	if err := m.SetXattr(99, "a", nil); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing file error = %v", err)
	}
	if err := m.RemoveXattr(file, "missing"); !errors.Is(err, ErrNoAttribute) {
		t.Fatalf("missing xattr error = %v", err)
	}
	if _, err := m.GetXattr(file, "missing"); !errors.Is(err, ErrNoAttribute) {
		t.Fatalf("missing xattr error = %v", err)
	}

	next, _ := m.Create()
	if next != 2 {
		t.Fatalf("file id = %d, want 2", next)
	}
	if _, err := m.Clone(99); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("missing clone error = %v", err)
	}
	after, _ := m.Create()
	if after != 3 {
		t.Fatalf("rejected clone consumed id: next = %d, want 3", after)
	}
}

func TestConcurrentOperations(t *testing.T) {
	m, err := NewManager(16, 64, 8, 16)
	if err != nil {
		t.Fatal(err)
	}
	root, err := m.Create()
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			name := string(rune('a' + worker%8))
			for step := 0; step < 40; step++ {
				switch step % 7 {
				case 0:
					_, _ = m.Create()
				case 1:
					_ = m.Resize(root, int64(step%20))
				case 2:
					_ = m.SetXattr(root, name, bytes.Repeat([]byte{byte(worker)}, step%24))
				case 3:
					_, _ = m.GetXattr(root, name)
				case 4:
					_ = m.RemoveXattr(root, name)
				case 5:
					_, _ = m.Clone(root)
				default:
					_, _ = m.Stat(root)
				}
				if used := m.UsedBlocks(); used > 16 {
					t.Errorf("used blocks = %d, exceeds pool", used)
					return
				}
			}
		}(worker)
	}
	wait.Wait()
}
