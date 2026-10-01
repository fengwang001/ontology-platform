package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

type modelKind string

const (
	modelWrite      modelKind = "write"
	modelRevoke     modelKind = "revoke"
	modelCommit     modelKind = "commit"
	modelCheckpoint modelKind = "checkpoint"
)

type modelRecord struct {
	kind    modelKind
	tid     int
	block   int
	payload []byte
	upto    int
}

type modelOperation struct {
	kind    modelKind
	block   int
	payload []byte
	upto    int
}

type naiveRecovery struct {
	image        map[int]Block
	applied      int
	skipped      int
	stale        int
	checkpointed int
	ignored      int
	reasons      []string
}

func cloneBlock(value Block) Block {
	payload := make([]byte, len(value.Payload))
	copy(payload, value.Payload)
	if value.Payload == nil {
		payload = []byte{}
	}
	return Block{Ver: value.Ver, Payload: payload}
}

func recoverNaive(records []modelRecord, disk map[int]Block, n int) naiveRecovery {
	prefix := records[:n]
	committed := make(map[int]bool)
	checkpoint := 0
	for _, rec := range prefix {
		if rec.kind == modelCommit {
			committed[rec.tid] = true
		}
		if rec.kind == modelCheckpoint {
			checkpoint = rec.upto
		}
	}

	lastAction := make(map[int]map[int]modelKind)
	result := naiveRecovery{}
	for _, rec := range prefix {
		switch rec.kind {
		case modelWrite, modelRevoke:
			if !committed[rec.tid] {
				result.ignored++
				continue
			}
			if lastAction[rec.tid] == nil {
				lastAction[rec.tid] = make(map[int]modelKind)
			}
			lastAction[rec.tid][rec.block] = rec.kind
		}
	}

	revocations := make(map[int]int)
	for tid, actions := range lastAction {
		for block, action := range actions {
			if action == modelRevoke && tid > revocations[block] {
				revocations[block] = tid
			}
		}
	}

	result.image = make(map[int]Block, len(disk))
	for block, value := range disk {
		result.image[block] = cloneBlock(value)
	}

	for _, rec := range prefix {
		if rec.kind != modelWrite || !committed[rec.tid] {
			continue
		}
		decision := ""
		switch {
		case rec.tid <= checkpoint:
			decision = "checkpointed"
			result.checkpointed++
		case revocations[rec.block] >= rec.tid:
			decision = "skipped"
			result.skipped++
		case disk[rec.block].Ver >= rec.tid:
			decision = "stale"
			result.stale++
		default:
			decision = "applied"
			result.applied++
			result.image[rec.block] = Block{
				Ver:     rec.tid,
				Payload: cloneBytes(rec.payload),
			}
		}
		result.reasons = append(result.reasons, fmt.Sprintf(
			"Write(t=%d,b=%d,len=%d):K=%d,rev=%d,diskVer=%d=>%s",
			rec.tid, rec.block, len(rec.payload), checkpoint, revocations[rec.block],
			disk[rec.block].Ver, decision))
	}
	return result
}

func cloneBytes(value []byte) []byte {
	result := make([]byte, len(value))
	copy(result, value)
	return result
}

func makeRandomLog(t *testing.T, r *rand.Rand) (*MetadataLog, []modelRecord, map[int]Block, []modelOperation, int) {
	t.Helper()
	capacity := 1 + r.Intn(5)
	log, err := NewMetadataLog(capacity)
	if err != nil {
		t.Fatal(err)
	}

	records := make([]modelRecord, 0)
	operations := make([]modelOperation, 0)
	committed := 0
	openRecords := 0
	lastCheckpoint := 0

	for i := 0; i < 10+r.Intn(10); i++ {
		var op modelOperation
		if openRecords == capacity {
			op = modelOperation{kind: modelCommit}
		} else {
			switch r.Intn(5) {
			case 0, 1:
				payload := []byte{byte('a' + r.Intn(26)), byte('0' + r.Intn(10))}
				if r.Intn(8) == 0 {
					payload = []byte{}
				}
				op = modelOperation{kind: modelWrite, block: r.Intn(5), payload: payload}
			case 2:
				op = modelOperation{kind: modelRevoke, block: r.Intn(5)}
			case 3, 4:
				if committed == lastCheckpoint {
					op = modelOperation{kind: modelWrite, block: r.Intn(5), payload: []byte{byte('a' + r.Intn(26))}}
				} else {
					op = modelOperation{kind: modelCheckpoint, upto: lastCheckpoint + r.Intn(committed-lastCheckpoint+1)}
				}
			}
		}

		tid := committed + 1
		var err error
		switch op.kind {
		case modelWrite:
			err = log.Write(op.block, op.payload)
		case modelRevoke:
			err = log.Revoke(op.block)
		case modelCommit:
			err = log.Commit()
		case modelCheckpoint:
			err = log.Checkpoint(op.upto)
		}
		if err != nil {
			t.Fatalf("operation %#v: %v", op, err)
		}

		operations = append(operations, op)
		switch op.kind {
		case modelWrite:
			records = append(records, modelRecord{kind: modelWrite, tid: tid, block: op.block, payload: cloneBytes(op.payload)})
			openRecords++
		case modelRevoke:
			records = append(records, modelRecord{kind: modelRevoke, tid: tid, block: op.block})
			openRecords++
		case modelCommit:
			records = append(records, modelRecord{kind: modelCommit, tid: tid})
			committed++
			openRecords = 0
		case modelCheckpoint:
			records = append(records, modelRecord{kind: modelCheckpoint, upto: op.upto})
			lastCheckpoint = op.upto
		}
	}

	if openRecords > 0 {
		if err := log.Commit(); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, modelOperation{kind: modelCommit})
		records = append(records, modelRecord{kind: modelCommit, tid: committed + 1})
	}

	disk := make(map[int]Block)
	for block := 0; block < 6; block++ {
		if r.Intn(2) == 0 {
			disk[block] = Block{
				Ver:     r.Intn(committed + 3),
				Payload: []byte(fmt.Sprintf("disk-%d", block)),
			}
		}
	}

	return log, records, disk, operations, capacity
}

func formatOperations(operations []modelOperation) string {
	parts := make([]string, len(operations))
	for i, op := range operations {
		switch op.kind {
		case modelWrite:
			parts[i] = fmt.Sprintf("W(b=%d,len=%d)", op.block, len(op.payload))
		case modelRevoke:
			parts[i] = fmt.Sprintf("R(b=%d)", op.block)
		case modelCommit:
			parts[i] = "C"
		case modelCheckpoint:
			parts[i] = fmt.Sprintf("K(%d)", op.upto)
		}
	}
	return strings.Join(parts, ",")
}

func TestRandomLogsMatchNaiveRecovery(t *testing.T) {
	for iteration := 0; iteration < 2000; iteration++ {
		seed := int64(1104000 + iteration)
		r := rand.New(rand.NewSource(seed))
		log, records, disk, operations, capacity := makeRandomLog(t, r)

		if log.Records() != len(records) {
			t.Fatalf("seed %d: records = %d, model has %d", seed, log.Records(), len(records))
		}

		for n := 0; n <= len(records); n++ {
			want := recoverNaive(records, disk, n)
			got, err := log.Recover(disk, n)
			if err != nil {
				t.Fatalf("seed %d n %d: %v", seed, n, err)
			}
			t.Logf("seed=%d cap=%d input=[%s] disk=%v n=%d output={applied:%d skipped:%d stale:%d checkpointed:%d ignored:%d image:%v} decisions=[%s] actualImage:%v",
				seed, capacity, formatOperations(operations), disk, n,
				want.applied, want.skipped, want.stale, want.checkpointed, want.ignored, want.image,
				strings.Join(want.reasons, "; "), got.Image)

			if !reflect.DeepEqual(got.Image, want.image) {
				t.Fatalf("seed %d n %d image = %#v, want %#v", seed, n, got.Image, want.image)
			}
			requireCounts(t, got, want.applied, want.skipped, want.stale, want.checkpointed, want.ignored)
			total := got.Applied + got.Skipped + got.Stale + got.Checkpointed
			committedWrites := 0
			for _, rec := range records[:n] {
				if rec.kind == modelWrite {
					committed := false
					for _, scanned := range records[:n] {
						if scanned.kind == modelCommit && scanned.tid == rec.tid {
							committed = true
						}
					}
					if committed {
						committedWrites++
					}
				}
			}
			if total != committedWrites {
				t.Fatalf("seed %d n %d classified = %d, committed writes = %d", seed, n, total, committedWrites)
			}
		}
	}
}
