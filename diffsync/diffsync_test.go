package diffsync

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return b
}

// buildPatch runs the full receiver->sender pipeline and returns the
// serialized patch plus the in-memory delta (for stats assertions).
func buildPatch(t *testing.T, oldData, newData []byte, blockSize int) ([]byte, *Delta) {
	t.Helper()
	sig, err := GenerateSignature(oldData, blockSize)
	if err != nil {
		t.Fatalf("GenerateSignature: %v", err)
	}
	delta, err := GenerateDelta(sig, newData)
	if err != nil {
		t.Fatalf("GenerateDelta: %v", err)
	}
	if delta.Stats.StrongChecks > delta.Stats.WeakMatches {
		t.Fatalf("strong checks %d exceed weak matches %d",
			delta.Stats.StrongChecks, delta.Stats.WeakMatches)
	}
	patch, err := MarshalPatch(delta)
	if err != nil {
		t.Fatalf("MarshalPatch: %v", err)
	}
	return patch, delta
}

func instrSummary(d *Delta) (copies, literals, literalBytes int) {
	for _, ins := range d.Instructions {
		switch ins.Op {
		case OpCopy:
			copies++
		case OpLiteral:
			literals++
			literalBytes += len(ins.Data)
		}
	}
	return
}

// TestRoundTrip covers insert, delete, in-place modification, empty files
// and identity: applying the generated patch to the old content must
// reproduce the new content byte for byte.
func TestRoundTrip(t *testing.T) {
	const blockSize = 512
	base := randomBytes(t, 4*blockSize+300)

	inserted := make([]byte, 0, len(base)+200)
	inserted = append(inserted, base[:1000]...)
	inserted = append(inserted, randomBytes(t, 200)...)
	inserted = append(inserted, base[1000:]...)

	deleted := make([]byte, 0, len(base))
	deleted = append(deleted, base[:700]...)
	deleted = append(deleted, base[1500:]...)

	modified := bytes.Clone(base)
	for i := 0; i < 17; i++ {
		modified[600+i] ^= 0xff
	}

	cases := []struct {
		name     string
		old, new []byte
	}{
		{"insert-middle", base, inserted},
		{"delete-middle", base, deleted},
		{"modify-in-place", base, modified},
		{"prepend", base, append(randomBytes(t, 128), base...)},
		{"append", base, append(bytes.Clone(base), randomBytes(t, 128)...)},
		{"identical", base, bytes.Clone(base)},
		{"empty-old", nil, randomBytes(t, 1000)},
		{"empty-new", base, nil},
		{"both-empty", nil, nil},
		{"old-smaller-than-block", randomBytes(t, 100), randomBytes(t, 100)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patch, delta := buildPatch(t, tc.old, tc.new, blockSize)
			got, err := ApplyPatch(tc.old, patch)
			if err != nil {
				t.Fatalf("ApplyPatch: %v", err)
			}
			copies, literals, literalBytes := instrSummary(delta)
			t.Logf("input: old=%dB new=%dB blockSize=%d; output: patch=%dB "+
				"copies=%d literalInstrs=%d literalBytes=%d; stats: %+v; "+
				"verdict: round-trip %v",
				len(tc.old), len(tc.new), blockSize, len(patch),
				copies, literals, literalBytes, delta.Stats,
				bytes.Equal(got, tc.new))
			if !bytes.Equal(got, tc.new) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(tc.new))
			}
		})
	}
}

// TestIdenticalFilesNoLiteral: when old and new are identical the patch
// must consist solely of copy instructions.
func TestIdenticalFilesNoLiteral(t *testing.T) {
	const blockSize = 256
	data := randomBytes(t, 3*blockSize+77)
	_, delta := buildPatch(t, data, bytes.Clone(data), blockSize)
	for i, ins := range delta.Instructions {
		if ins.Op != OpCopy {
			t.Fatalf("instruction %d is %v, want all copies", i, ins.Op)
		}
		if int(ins.Index) != i {
			t.Fatalf("instruction %d copies block %d, want %d", i, ins.Index, i)
		}
	}
	t.Logf("input: identical %dB files; output: %d copy instructions, "+
		"0 literal bytes; verdict: no literal emitted", len(data), len(delta.Instructions))
}

// TestInPlaceModifyLiteralBound: changing a few bytes inside a single
// block of a random file must keep the literal payload within one block.
func TestInPlaceModifyLiteralBound(t *testing.T) {
	const blockSize = 512
	oldData := randomBytes(t, 8*blockSize+100)
	newData := bytes.Clone(oldData)
	// Corrupt 11 bytes inside block 3 only.
	for i := 0; i < 11; i++ {
		newData[3*blockSize+100+i] ^= 0xa5
	}
	_, delta := buildPatch(t, oldData, newData, blockSize)
	lit := delta.LiteralBytes()
	t.Logf("input: %dB random file, 11 bytes changed inside block 3; "+
		"output: literalBytes=%d (bound=%d); verdict: %v",
		len(oldData), lit, blockSize, lit <= blockSize)
	if lit > blockSize {
		t.Fatalf("literal bytes %d exceed one block size %d", lit, blockSize)
	}
}

// TestWeakCollisionNotReused builds two blocks with identical weak
// checksums but different content and asserts the sender does not reuse
// the colliding block: the strong check must reject it.
func TestWeakCollisionNotReused(t *testing.T) {
	const blockSize = 1024
	// a = sum(bytes); b = sum((n-k)*byte_k). Moving value 128 from
	// position 0 to position 512 changes b by 512*128 = 65536 = 0 mod 2^16,
	// so both blocks share the same weak checksum.
	block1 := make([]byte, blockSize)
	block1[0] = 128
	block2 := make([]byte, blockSize)
	block2[512] = 128

	w1, w2 := weakChecksum(block1), weakChecksum(block2)
	s1, s2 := sha256.Sum256(block1), sha256.Sum256(block2)
	t.Logf("input: block1[0]=128, block2[512]=128; weak1=%08x weak2=%08x "+
		"(equal=%v), strong equal=%v", w1, w2, w1 == w2, s1 == s2)
	if w1 != w2 {
		t.Fatalf("test construction broken: weak checksums differ")
	}
	if s1 == s2 {
		t.Fatalf("test construction broken: strong checksums equal")
	}

	patch, delta := buildPatch(t, block1, block2, blockSize)
	for i, ins := range delta.Instructions {
		if ins.Op == OpCopy {
			t.Fatalf("instruction %d reuses a weak-colliding block", i)
		}
	}
	if delta.Stats.StrongChecks == 0 {
		t.Fatalf("expected at least one strong check for the weak collision")
	}
	if delta.Stats.StrongChecks > delta.Stats.WeakMatches {
		t.Fatalf("strong checks %d exceed weak matches %d",
			delta.Stats.StrongChecks, delta.Stats.WeakMatches)
	}
	got, err := ApplyPatch(block1, patch)
	if err != nil {
		t.Fatalf("ApplyPatch: %v", err)
	}
	t.Logf("output: instructions=%d (all literal), weakMatches=%d "+
		"strongChecks=%d; verdict: colliding block rejected by strong "+
		"checksum, round-trip %v",
		len(delta.Instructions), delta.Stats.WeakMatches,
		delta.Stats.StrongChecks, bytes.Equal(got, block2))
	if !bytes.Equal(got, block2) {
		t.Fatalf("round-trip mismatch")
	}
}

// TestShortTailMatchesOnlyEqualLength: a short tail block may only be
// reused for an equally long segment of the new file.
func TestShortTailMatchesOnlyEqualLength(t *testing.T) {
	const blockSize = 1024
	head := randomBytes(t, blockSize)
	tail := []byte("TAIL")
	oldData := append(head, tail...)

	// The tail alone must be reused as block 1.
	_, delta := buildPatch(t, oldData, bytes.Clone(tail), blockSize)
	if len(delta.Instructions) != 1 || delta.Instructions[0].Op != OpCopy ||
		delta.Instructions[0].Index != 1 {
		t.Fatalf("tail not reused as block 1: %+v", delta.Instructions)
	}
	t.Logf("input: old=%dB (full block + 4B tail), new=4B tail; "+
		"output: copy block 1; verdict: equal-length tail matched", len(oldData))

	// A 4-byte prefix of the full block must NOT reuse block 0 (lengths
	// differ); it has to come out as literal bytes.
	_, delta = buildPatch(t, oldData, bytes.Clone(head[:4]), blockSize)
	if len(delta.Instructions) != 1 || delta.Instructions[0].Op != OpLiteral {
		t.Fatalf("short prefix wrongly reused a full block: %+v", delta.Instructions)
	}
	t.Logf("input: new=4B prefix of full block; output: literal only; " +
		"verdict: short segment did not match full block")
}

// TestInvalidBlockSize: non-positive block sizes are rejected everywhere.
func TestInvalidBlockSize(t *testing.T) {
	for _, bs := range []int{0, -1, -4096} {
		if _, err := GenerateSignature([]byte("x"), bs); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("GenerateSignature(bs=%d) = %v, want ErrInvalidBlockSize", bs, err)
		}
		if _, err := GenerateDelta(&Signature{BlockSize: bs}, []byte("x")); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("GenerateDelta(bs=%d) = %v, want ErrInvalidBlockSize", bs, err)
		}
	}
	// A patch declaring blockSize 0 must be rejected with the same reason.
	oldData := randomBytes(t, 64)
	patch, err := MarshalPatch(&Delta{
		BlockSize:   1, // marshal a valid patch first, then corrupt below
		OldLength:   int64(len(oldData)),
		OldChecksum: sha256.Sum256(oldData),
		NewLength:   0,
		NewChecksum: sha256.Sum256(nil),
	})
	if err != nil {
		t.Fatalf("MarshalPatch: %v", err)
	}
	patch[4] = 0 // blockSize field -> 0 (big-endian uint32 at offset 4..8)
	patch[5] = 0
	patch[6] = 0
	patch[7] = 0
	if _, err := ApplyPatch(oldData, patch); !errors.Is(err, ErrInvalidBlockSize) {
		t.Fatalf("ApplyPatch(blockSize=0) = %v, want ErrInvalidBlockSize", err)
	}
	t.Log("input: blockSize 0/-1/-4096 and a patch with blockSize=0; " +
		"verdict: all rejected with ErrInvalidBlockSize")
}

// TestOldChecksumMismatch: a patch generated for a different old file is
// rejected before any instruction runs.
func TestOldChecksumMismatch(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 1000)
	otherOld := randomBytes(t, 1000)
	patch, _ := buildPatch(t, oldData, randomBytes(t, 900), blockSize)

	_, err := ApplyPatch(otherOld, patch)
	if !errors.Is(err, ErrOldChecksumMismatch) {
		t.Fatalf("ApplyPatch with wrong old file = %v, want ErrOldChecksumMismatch", err)
	}
	// Same length, one byte different: still a mismatch.
	almostOld := bytes.Clone(oldData)
	almostOld[0] ^= 1
	if _, err := ApplyPatch(almostOld, patch); !errors.Is(err, ErrOldChecksumMismatch) {
		t.Fatalf("ApplyPatch with tampered old file = %v, want ErrOldChecksumMismatch", err)
	}
	t.Log("input: patch for old A applied to old B and to 1-byte-tampered A; " +
		"verdict: both rejected with ErrOldChecksumMismatch")
}

// TestResultChecksumMismatch: any corruption that changes the reconstructed
// content (or the declared result checksum) is caught by the final check.
func TestResultChecksumMismatch(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 4*blockSize)
	newData := bytes.Clone(oldData)
	newData[0] ^= 0xff // first instruction becomes a literal
	patch, delta := buildPatch(t, oldData, newData, blockSize)
	if delta.Instructions[0].Op != OpLiteral {
		t.Fatalf("expected first instruction to be literal, got %v", delta.Instructions[0].Op)
	}

	// Corrupt the declared new-file checksum (offset: 4 magic + 4
	// blockSize + 8 oldLen + 32 oldSum + 8 newLen = 56).
	tampered := bytes.Clone(patch)
	tampered[56] ^= 0x01
	if _, err := ApplyPatch(oldData, tampered); !errors.Is(err, ErrResultChecksumMismatch) {
		t.Fatalf("tampered new checksum = %v, want ErrResultChecksumMismatch", err)
	}

	// Corrupt one literal payload byte (first instruction starts at
	// offset 92: opcode at 92, length at 93..97, data at 97).
	if patch[92] != byte(OpLiteral) {
		t.Fatalf("test construction broken: patch[92]=%d, want literal opcode", patch[92])
	}
	tampered = bytes.Clone(patch)
	tampered[97] ^= 0x01
	if _, err := ApplyPatch(oldData, tampered); !errors.Is(err, ErrResultChecksumMismatch) {
		t.Fatalf("tampered literal payload = %v, want ErrResultChecksumMismatch", err)
	}
	t.Log("input: patch with flipped declared checksum / flipped literal byte; " +
		"verdict: both rejected with ErrResultChecksumMismatch")
}

// TestInstructionOutOfRange: a copy instruction referencing a block beyond
// the old file is rejected.
func TestInstructionOutOfRange(t *testing.T) {
	oldData := randomBytes(t, 600) // 3 blocks at blockSize 256
	patch, err := MarshalPatch(&Delta{
		BlockSize:    256,
		OldLength:    int64(len(oldData)),
		OldChecksum:  sha256.Sum256(oldData),
		NewLength:    256,
		NewChecksum:  sha256.Sum256(oldData[:256]),
		Instructions: []Instruction{{Op: OpCopy, Index: 3}},
	})
	if err != nil {
		t.Fatalf("MarshalPatch: %v", err)
	}
	if _, err := ApplyPatch(oldData, patch); !errors.Is(err, ErrInstructionOutOfRange) {
		t.Fatalf("ApplyPatch = %v, want ErrInstructionOutOfRange", err)
	}
	// Empty old file: any copy is out of range.
	patch, err = MarshalPatch(&Delta{
		BlockSize:    256,
		OldLength:    0,
		OldChecksum:  sha256.Sum256(nil),
		NewLength:    0,
		NewChecksum:  sha256.Sum256(nil),
		Instructions: []Instruction{{Op: OpCopy, Index: 0}},
	})
	if err != nil {
		t.Fatalf("MarshalPatch: %v", err)
	}
	if _, err := ApplyPatch(nil, patch); !errors.Is(err, ErrInstructionOutOfRange) {
		t.Fatalf("ApplyPatch (empty old) = %v, want ErrInstructionOutOfRange", err)
	}
	t.Log("input: copy block 3 of a 3-block file, copy block 0 of an empty file; " +
		"verdict: both rejected with ErrInstructionOutOfRange")
}

// TestMalformedPatch: bad magic, unknown opcodes and trailing garbage are
// rejected as malformed.
func TestMalformedPatch(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 800)
	patch, _ := buildPatch(t, oldData, randomBytes(t, 700), blockSize)

	badMagic := bytes.Clone(patch)
	badMagic[0] = 'X'
	if _, err := ApplyPatch(oldData, badMagic); !errors.Is(err, ErrMalformedPatch) {
		t.Fatalf("bad magic = %v, want ErrMalformedPatch", err)
	}

	badOp := bytes.Clone(patch)
	badOp[92] = 0x7f // first instruction opcode
	if _, err := ApplyPatch(oldData, badOp); !errors.Is(err, ErrMalformedPatch) {
		t.Fatalf("bad opcode = %v, want ErrMalformedPatch", err)
	}

	trailing := append(bytes.Clone(patch), 0x00)
	if _, err := ApplyPatch(oldData, trailing); !errors.Is(err, ErrMalformedPatch) {
		t.Fatalf("trailing garbage = %v, want ErrMalformedPatch", err)
	}
	t.Log("input: bad magic / unknown opcode / trailing byte; " +
		"verdict: all rejected with ErrMalformedPatch")
}

// TestTruncationEveryPoint cuts a valid patch at every possible byte
// offset and asserts each truncation is rejected as ErrTruncatedPatch and
// leaves the on-disk target byte-for-byte unchanged.
func TestTruncationEveryPoint(t *testing.T) {
	const blockSize = 128
	oldData := randomBytes(t, 5*blockSize+40)
	newData := bytes.Clone(oldData)
	newData[10] ^= 0xff                   // literal at the front
	newData = append(newData, 0x42, 0x43) // literal at the end
	patch, delta := buildPatch(t, oldData, newData, blockSize)
	t.Logf("input: old=%dB new=%dB patch=%dB instructions=%d; "+
		"testing every one of %d truncation points",
		len(oldData), len(newData), len(patch), len(delta.Instructions), len(patch))

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.bin")
	dstPath := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(oldPath, oldData, 0o644); err != nil {
		t.Fatal(err)
	}
	sentinel := []byte("pre-existing destination content")
	if err := os.WriteFile(dstPath, sentinel, 0o644); err != nil {
		t.Fatal(err)
	}

	for cut := 0; cut < len(patch); cut++ {
		truncated := patch[:cut]
		if _, err := ApplyPatch(oldData, truncated); !errors.Is(err, ErrTruncatedPatch) {
			t.Fatalf("cut=%d: ApplyPatch = %v, want ErrTruncatedPatch", cut, err)
		}
		if err := ApplyPatchToFile(oldPath, truncated, dstPath); !errors.Is(err, ErrTruncatedPatch) {
			t.Fatalf("cut=%d: ApplyPatchToFile = %v, want ErrTruncatedPatch", cut, err)
		}
		got, err := os.ReadFile(dstPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, sentinel) {
			t.Fatalf("cut=%d: destination modified after rejected patch", cut)
		}
	}
	t.Logf("verdict: all %d truncation points rejected with "+
		"ErrTruncatedPatch, destination byte-for-byte unchanged", len(patch))
}

// TestDeterministic: the same inputs always produce byte-identical patches,
// also when generated concurrently.
func TestDeterministic(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 3000)
	newData := randomBytes(t, 2800)
	want, _ := buildPatch(t, oldData, newData, blockSize)

	again, _ := buildPatch(t, oldData, newData, blockSize)
	if !bytes.Equal(want, again) {
		t.Fatal("two sequential generations differ")
	}

	sig, err := GenerateSignature(oldData, blockSize)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([][]byte, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := GenerateDelta(sig, newData)
			if err != nil {
				t.Error(err)
				return
			}
			p, err := MarshalPatch(d)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = p
		}(i)
	}
	wg.Wait()
	for i, p := range results {
		if !bytes.Equal(want, p) {
			t.Fatalf("concurrent generation %d differs", i)
		}
	}
	t.Logf("input: old=%dB new=%dB; output: %d identical patches of %dB; "+
		"verdict: deterministic under sequential and concurrent generation",
		len(oldData), len(newData), len(results)+1, len(want))
}

// TestConcurrentUse: one signature serves many senders while other
// goroutines apply patches; run with -race.
func TestConcurrentUse(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 4096)
	sig, err := GenerateSignature(oldData, blockSize)
	if err != nil {
		t.Fatal(err)
	}
	rng := mathrand.New(mathrand.NewSource(1))
	news := make([][]byte, 8)
	for i := range news {
		n := bytes.Clone(oldData)
		for k := 0; k < 20; k++ {
			n[rng.Intn(len(n))] ^= byte(rng.Intn(256))
		}
		news[i] = n
	}

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for round := 0; round < 4; round++ {
				newData := news[(w+round)%len(news)]
				d, err := GenerateDelta(sig, newData)
				if err != nil {
					errs <- err
					return
				}
				p, err := MarshalPatch(d)
				if err != nil {
					errs <- err
					return
				}
				got, err := ApplyPatch(oldData, p)
				if err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(got, newData) {
					errs <- errors.New("concurrent round-trip mismatch")
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
	t.Log("input: 1 shared signature, 8 sender+applier goroutines x 4 rounds; " +
		"verdict: all round-trips byte-identical, no data race")
}

// TestApplyPatchToFileAtomic: success writes the new content; any failure
// leaves a pre-existing destination untouched.
func TestApplyPatchToFileAtomic(t *testing.T) {
	const blockSize = 256
	oldData := randomBytes(t, 1500)
	newData := randomBytes(t, 1200)
	patch, _ := buildPatch(t, oldData, newData, blockSize)

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.bin")
	dstPath := filepath.Join(dir, "dst.bin")
	if err := os.WriteFile(oldPath, oldData, 0o644); err != nil {
		t.Fatal(err)
	}

	// Success path: dst is created with exactly the new content.
	if err := ApplyPatchToFile(oldPath, patch, dstPath); err != nil {
		t.Fatalf("ApplyPatchToFile: %v", err)
	}
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatal("destination content mismatch after successful apply")
	}

	// Failure path: corrupt the patch, dst must stay byte-for-byte intact.
	before := bytes.Clone(got)
	badPatch := bytes.Clone(patch)
	badPatch[56] ^= 0x01 // declared new checksum
	if err := ApplyPatchToFile(oldPath, badPatch, dstPath); !errors.Is(err, ErrResultChecksumMismatch) {
		t.Fatalf("ApplyPatchToFile corrupted = %v, want ErrResultChecksumMismatch", err)
	}
	got, err = os.ReadFile(dstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, before) {
		t.Fatal("destination modified by rejected patch")
	}

	// No leftover temp files in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "old.bin" && e.Name() != "dst.bin" {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
	t.Logf("input: old=%dB new=%dB; output: dst=%dB on success, unchanged "+
		"on corrupted patch; verdict: atomic apply, no partial writes",
		len(oldData), len(newData), len(before))
}
