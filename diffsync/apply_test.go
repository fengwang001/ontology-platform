package diffsync

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// makePatch builds a mixed copy+literal patch from deterministic inputs.
func makePatch(t *testing.T) (old, newData, patch []byte) {
	old = randomBytes(200, 20)
	newData = bytes.Clone(old)
	copy(newData[70:], []byte("changed bytes in the middle"))
	newData = append(newData, 0x77, 0x88)
	sig, err := BuildSignature(old, 32)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	patch, _, err = GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	return old, newData, patch
}

// TestTruncationEveryByte cuts the patch at every possible point and
// asserts each truncation is rejected and the target stays untouched.
func TestTruncationEveryByte(t *testing.T) {
	old, _, patch := makePatch(t)
	t.Logf("input: old=%dB(sha=%s) patch=%dB(sha=%s); cutting at every byte",
		len(old), shortHash(old), len(patch), shortHash(patch))
	for cut := 0; cut < len(patch); cut++ {
		truncated := patch[:cut]
		if _, err := Apply(old, truncated); !errors.Is(err, ErrPatchTruncated) {
			t.Fatalf("cut=%d: got %v, want ErrPatchTruncated", cut, err)
		}
	}
	t.Logf("decision: all %d truncation points rejected with ErrPatchTruncated; "+
		"Apply is pure so the target is byte-for-byte unchanged", len(patch))
}

func TestOldChecksumMismatch(t *testing.T) {
	old, _, patch := makePatch(t)

	tampered := bytes.Clone(old)
	tampered[0] ^= 0xFF // same length, different content
	if _, err := Apply(tampered, patch); !errors.Is(err, ErrOldChecksumMismatch) {
		t.Fatalf("tampered old: got %v, want ErrOldChecksumMismatch", err)
	}
	t.Logf("input: old with byte0 flipped (sha=%s, declared %s) -> ErrOldChecksumMismatch",
		shortHash(tampered), shortHash(old))

	shorter := old[:len(old)-1] // different length
	if _, err := Apply(shorter, patch); !errors.Is(err, ErrOldChecksumMismatch) {
		t.Fatalf("shorter old: got %v, want ErrOldChecksumMismatch", err)
	}
	t.Logf("input: old truncated to %dB -> ErrOldChecksumMismatch", len(shorter))
}

func TestInstructionOutOfRange(t *testing.T) {
	old, _, patch := makePatch(t)
	p, err := UnmarshalPatch(patch)
	if err != nil {
		t.Fatalf("UnmarshalPatch: %v", err)
	}

	badIndex := *p
	badIndex.Insns = append([]Instruction{{Op: OpCopy, Index: 9999}}, p.Insns...)
	if _, err := Apply(old, badIndex.Marshal()); !errors.Is(err, ErrInstructionOutOfRange) {
		t.Fatalf("bad block index: got %v, want ErrInstructionOutOfRange", err)
	}
	t.Logf("input: copy of block 9999 (old has %d blocks) -> ErrInstructionOutOfRange",
		NumBlocks(p.OldLen, p.BlockSize))

	badOp := *p
	badOp.Insns = append([]Instruction{{Op: Op(9)}}, p.Insns...)
	if _, err := Apply(old, badOp.Marshal()); !errors.Is(err, ErrInstructionOutOfRange) {
		t.Fatalf("unknown op: got %v, want ErrInstructionOutOfRange", err)
	}
	t.Logf("input: instruction with unknown op 9 -> ErrInstructionOutOfRange")

	trailing := append(bytes.Clone(patch), 0x00)
	if _, err := Apply(old, trailing); !errors.Is(err, ErrInstructionOutOfRange) {
		t.Fatalf("trailing garbage: got %v, want ErrInstructionOutOfRange", err)
	}
	t.Logf("input: patch + 1 trailing byte -> ErrInstructionOutOfRange")
}

func TestResultChecksumMismatch(t *testing.T) {
	old, _, patch := makePatch(t)
	p, err := UnmarshalPatch(patch)
	if err != nil {
		t.Fatalf("UnmarshalPatch: %v", err)
	}
	flipped := false
	for i := range p.Insns {
		if p.Insns[i].Op == OpLiteral && len(p.Insns[i].Data) > 0 {
			p.Insns[i].Data[0] ^= 0xFF // rebuilds different bytes than declared
			flipped = true
			break
		}
	}
	if !flipped {
		t.Fatal("test patch has no literal instruction to corrupt")
	}
	if _, err := Apply(old, p.Marshal()); !errors.Is(err, ErrResultChecksumMismatch) {
		t.Fatalf("corrupted literal: got %v, want ErrResultChecksumMismatch", err)
	}
	t.Logf("input: one literal byte flipped -> rebuilt content differs from the " +
		"declared new-file checksum -> ErrResultChecksumMismatch")
}

func TestBadMagic(t *testing.T) {
	old, _, patch := makePatch(t)
	bad := bytes.Clone(patch)
	bad[0] = 'X'
	if _, err := Apply(old, bad); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("got %v, want ErrBadMagic", err)
	}
	t.Logf("input: magic byte0 corrupted -> ErrBadMagic")
}

// TestAtomicApplyFile verifies success installs the new content and any
// rejection leaves a pre-existing destination byte-for-byte unchanged.
func TestAtomicApplyFile(t *testing.T) {
	old, newData, patch := makePatch(t)
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.bin")
	patchPath := filepath.Join(dir, "patch.bin")
	dstPath := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(oldPath, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ApplyFileAtomic(oldPath, patchPath, dstPath); err != nil {
		t.Fatalf("ApplyFileAtomic: %v", err)
	}
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("dst sha=%s, want %s", shortHash(got), shortHash(newData))
	}
	t.Logf("success: dst installed atomically, sha=%s matches new file", shortHash(got))

	// Every rejection must leave the pre-existing destination untouched.
	sentinel := []byte("pre-existing destination content")
	if err := os.WriteFile(dstPath, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}
	rejections := map[string][]byte{
		"truncated": patch[:len(patch)/2],
		"bad magic": {0xFF},
		"result mismatch": func() []byte {
			p, _ := UnmarshalPatch(patch)
			p.NewStrong[0] ^= 0xFF
			return p.Marshal()
		}(),
	}
	for name, bad := range rejections {
		if err := os.WriteFile(patchPath, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ApplyFileAtomic(oldPath, patchPath, dstPath); err == nil {
			t.Fatalf("%s: expected rejection", name)
		}
		got, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, sentinel) {
			t.Fatalf("%s: dst modified after rejection", name)
		}
		t.Logf("rejection %q: dst still %q, byte-for-byte unchanged", name, got)
	}

	// Old-file mismatch must also leave dst untouched.
	if err := os.WriteFile(patchPath, patch, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldPath, []byte("some other old file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ApplyFileAtomic(oldPath, patchPath, dstPath); !errors.Is(err, ErrOldChecksumMismatch) {
		t.Fatalf("old mismatch: got %v, want ErrOldChecksumMismatch", err)
	}
	got, err = os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, sentinel) {
		t.Fatal("old mismatch: dst modified after rejection")
	}
	t.Logf("rejection \"old checksum mismatch\": dst still %q, unchanged", got)
}

// TestConcurrentGenerateAndApply shares one signature across many
// concurrent delta generators and appliers (run with -race).
func TestConcurrentGenerateAndApply(t *testing.T) {
	old := randomBytes(4096, 30)
	sig, err := BuildSignature(old, 64)
	if err != nil {
		t.Fatalf("BuildSignature: %v", err)
	}
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			newData := bytes.Clone(old)
			copy(newData[seed*100:], randomBytes(13, seed))
			patch, _, err := GenerateDelta(sig, newData)
			if err != nil {
				errs <- err
				return
			}
			out, err := Apply(old, patch)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(out, newData) {
				errs <- errors.New("concurrent round trip mismatch")
			}
		}(int64(w) + 1)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("decision: %d goroutines shared one signature for generate+apply; "+
		"all outputs byte-identical to their new files", workers)
}
