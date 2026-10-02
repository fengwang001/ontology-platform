package journal

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// formatRecord renders a record compactly for test logs.
func formatRecord(r Record) string {
	switch r.Kind {
	case WriteRecord:
		return fmt.Sprintf("Write(tid=%d,b=%d,p=%q)", r.Tid, r.Block, r.Payload)
	case RevokeRecord:
		return fmt.Sprintf("Revoke(tid=%d,b=%d)", r.Tid, r.Block)
	case CommitRecord:
		return fmt.Sprintf("Commit(tid=%d)", r.Tid)
	case CheckpointRecord:
		return fmt.Sprintf("Checkpoint(upto=%d)", r.Upto)
	}
	return "?"
}

func formatRecords(records []Record) string {
	parts := make([]string, len(records))
	for i, r := range records {
		parts[i] = fmt.Sprintf("#%d %s", i+1, formatRecord(r))
	}
	return strings.Join(parts, " | ")
}

func formatDisk(disk map[int]Block) string {
	keys := make([]int, 0, len(disk))
	for b := range disk {
		keys = append(keys, b)
	}
	sort.Ints(keys)
	parts := make([]string, 0, len(keys))
	for _, b := range keys {
		parts = append(parts, fmt.Sprintf("b%d=(ver %d,%q)", b, disk[b].Ver, disk[b].Payload))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// naiveRecover is an independent, step-by-step replay of the rules: it
// groups each transaction's records, derives the revoke table per
// transaction, then judges every Write of every committed transaction in
// log order. It also returns a human-readable justification per Write.
func naiveRecover(records []Record, disk map[int]Block, n int) (Result, []string) {
	prefix := records[:n]

	committed := map[int]bool{}
	highWater := 0
	for _, r := range prefix {
		switch r.Kind {
		case CommitRecord:
			committed[r.Tid] = true
		case CheckpointRecord:
			highWater = r.Upto
		}
	}

	// Revoke table: per transaction, the last Write/Revoke per block.
	txnOps := map[int][]Record{}
	var txnOrder []int
	for _, r := range prefix {
		if r.Kind == WriteRecord || r.Kind == RevokeRecord {
			if _, seen := txnOps[r.Tid]; !seen {
				txnOrder = append(txnOrder, r.Tid)
			}
			txnOps[r.Tid] = append(txnOps[r.Tid], r)
		}
	}
	revoked := map[int]int{}
	for _, tid := range txnOrder {
		if !committed[tid] {
			continue
		}
		last := map[int]RecordKind{}
		for _, op := range txnOps[tid] {
			last[op.Block] = op.Kind
		}
		for b, kind := range last {
			if kind == RevokeRecord && tid > revoked[b] {
				revoked[b] = tid
			}
		}
	}

	res := Result{Image: map[int]Block{}}
	var why []string
	for _, r := range prefix {
		switch r.Kind {
		case WriteRecord:
			if !committed[r.Tid] {
				res.Ignored++
				why = append(why, fmt.Sprintf("%s -> Ignored (txn %d not committed in prefix)", formatRecord(r), r.Tid))
				continue
			}
			switch {
			case r.Tid <= highWater:
				res.Checkpointed++
				why = append(why, fmt.Sprintf("%s -> Checkpointed (tid %d <= K %d)", formatRecord(r), r.Tid, highWater))
			case revoked[r.Block] >= r.Tid:
				res.Skipped++
				why = append(why, fmt.Sprintf("%s -> Skipped (revoke table b%d=%d >= tid %d)", formatRecord(r), r.Block, revoked[r.Block], r.Tid))
			case diskVer(disk, r.Block) >= r.Tid:
				res.Stale++
				why = append(why, fmt.Sprintf("%s -> Stale (disk ver %d >= tid %d)", formatRecord(r), diskVer(disk, r.Block), r.Tid))
			default:
				cp := make([]byte, len(r.Payload))
				copy(cp, r.Payload)
				res.Image[r.Block] = Block{Ver: r.Tid, Payload: cp}
				res.Applied++
				why = append(why, fmt.Sprintf("%s -> Applied (tid %d > K %d, revoke %d < tid, disk ver %d < tid)",
					formatRecord(r), r.Tid, highWater, revoked[r.Block], diskVer(disk, r.Block)))
			}
		case RevokeRecord:
			if !committed[r.Tid] {
				res.Ignored++
				why = append(why, fmt.Sprintf("%s -> Ignored (txn %d not committed in prefix)", formatRecord(r), r.Tid))
			}
		}
	}
	return res, why
}

func snapshotRecords(j *Journal) []Record {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Record(nil), j.records...)
}

func cloneDisk(disk map[int]Block) map[int]Block {
	out := make(map[int]Block, len(disk))
	for b, blk := range disk {
		out[b] = Block{Ver: blk.Ver, Payload: append([]byte{}, blk.Payload...)}
	}
	return out
}

// buildRandomLog appends a random but valid operation sequence and
// returns the journal plus the disk to replay against.
func buildRandomLog(t *testing.T, rng *rand.Rand) (*Journal, map[int]Block) {
	t.Helper()
	cap := 1 + rng.Intn(5)
	j, err := New(cap)
	if err != nil {
		t.Fatalf("New(%d): %v", cap, err)
	}
	txns := 1 + rng.Intn(6)
	maxBlock := rng.Intn(5)
	lastChk := 0
	maybeCheckpoint := func(committed int) {
		if rng.Intn(3) == 0 && committed >= lastChk {
			upto := lastChk
			if committed > lastChk {
				upto = lastChk + rng.Intn(committed-lastChk+1)
			}
			if err := j.Checkpoint(upto); err != nil {
				t.Fatalf("Checkpoint(%d): %v", upto, err)
			}
			lastChk = upto
		}
	}
	committed := 0
	for txn := 0; txn < txns; txn++ {
		ops := 1 + rng.Intn(cap) // 1..cap Write/Revoke records
		for i := 0; i < ops; i++ {
			b := rng.Intn(maxBlock + 1)
			if rng.Intn(3) == 0 {
				if err := j.Revoke(b); err != nil {
					t.Fatalf("Revoke(%d): %v", b, err)
				}
			} else {
				size := rng.Intn(9)
				if rng.Intn(20) == 0 {
					size = MaxPayload
				}
				p := make([]byte, size)
				rng.Read(p)
				if err := j.Write(b, p); err != nil {
					t.Fatalf("Write(%d): %v", b, err)
				}
			}
			maybeCheckpoint(committed)
		}
		// Occasionally leave the last transaction open.
		if txn == txns-1 && rng.Intn(4) == 0 {
			break
		}
		if err := j.Commit(); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		committed++
		maybeCheckpoint(committed)
	}

	disk := map[int]Block{}
	for b := 0; b <= maxBlock; b++ {
		if rng.Intn(2) == 0 {
			continue
		}
		p := make([]byte, rng.Intn(5))
		rng.Read(p)
		disk[b] = Block{Ver: rng.Intn(txns + 2), Payload: p}
	}
	return j, disk
}

// TestRandomLogsAgainstNaive replays 2000 random logs and compares every
// prefix of Recover against the step-by-step naive replay, logging the
// inputs, outputs and per-write justifications.
func TestRandomLogsAgainstNaive(t *testing.T) {
	const cases = 2000
	for seed := int64(0); seed < cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		j, disk := buildRandomLog(t, rng)
		records := snapshotRecords(j)
		diskBefore := cloneDisk(disk)

		t.Logf("case %d: records: %s", seed, formatRecords(records))
		t.Logf("case %d: disk: %s", seed, formatDisk(disk))

		for n := 0; n <= len(records); n++ {
			got, err := j.Recover(disk, n)
			if err != nil {
				t.Fatalf("case %d n=%d: Recover: %v", seed, n, err)
			}
			want, why := naiveRecover(records, disk, n)
			t.Logf("case %d n=%d: got %+v", seed, n, got)
			for _, line := range why {
				t.Logf("case %d n=%d:   %s", seed, n, line)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d n=%d mismatch:\nrecords: %s\ndisk: %s\ngot  %+v\nwant %+v",
					seed, n, formatRecords(records), formatDisk(disk), got, want)
			}
			if sum := got.Applied + got.Skipped + got.Stale + got.Checkpointed; sum != committedWritesInPrefix(j, n) {
				t.Fatalf("case %d n=%d: counter sum %d != committed writes %d",
					seed, n, sum, committedWritesInPrefix(j, n))
			}
		}
		if !reflect.DeepEqual(disk, diskBefore) {
			t.Fatalf("case %d: disk mutated by Recover", seed)
		}
	}
}
