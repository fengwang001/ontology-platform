package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const (
	modelWrite = iota
	modelFlush
	modelTruncate
	modelUnlink
	modelFree
	modelReserved
	modelAvailUser
	modelAvailPriv
	modelDirty
)

type modelLedger struct {
	total, special, extent, water int64
	files                         map[int64]modelFile
	counter                       int64
}

type modelFile struct {
	allocated int64
	delayed   int64
	dirtySeq  int64
}

type modelOp struct {
	kind       int
	id         int64
	blocks     int64
	privileged bool
}

type modelResult struct {
	err     error
	value   int64
	flushed []int64
	files   []int64
	basis   string
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1101))
	for sequence := 0; sequence < 2000; sequence++ {
		t.Run(fmt.Sprintf("sequence_%04d", sequence), func(t *testing.T) {
			total := int64(rng.Intn(40) + 1)
			special := int64(rng.Intn(int(total) + 1))
			extent := int64(rng.Intn(6) + 1)
			water := int64(rng.Intn(13))
			actual, err := NewLedger(total, special, extent, water)
			if err != nil {
				t.Fatal(err)
			}
			expected := newModelLedger(total, special, extent, water)
			logs := []string{fmt.Sprintf("T=%d S=%d E=%d W=%d", total, special, extent, water)}

			for step := 0; step < 60; step++ {
				op := randomModelOp(rng)
				got := runActualOp(actual, op)
				want := expected.apply(op)
				log := fmt.Sprintf("step=%02d op=%s actual=%s model=%s basis=%s",
					step, formatModelOp(op), formatModelResult(got), formatModelResult(want), want.basis)
				logs = append(logs, log)
				t.Log(log)
				if !sameModelResult(got, want) || !sameInternalState(actual, expected) {
					t.Log(strings.Join(logs, "\n"))
					t.Fatalf("mismatch\n%s", strings.Join(logs, "\n"))
				}
			}
		})
	}
}

func newModelLedger(total, special, extent, water int64) *modelLedger {
	return &modelLedger{
		total:   total,
		special: special,
		extent:  extent,
		water:   water,
		files:   make(map[int64]modelFile),
	}
}

func (l *modelLedger) apply(op modelOp) modelResult {
	switch op.kind {
	case modelWrite:
		return l.write(op.id, op.blocks, op.privileged)
	case modelFlush:
		return l.flush(op.id)
	case modelTruncate:
		return l.truncate(op.id, op.blocks)
	case modelUnlink:
		return l.unlink(op.id)
	case modelFree:
		value := l.free()
		return modelResult{value: value, basis: fmt.Sprintf("F=T-U=%d", value)}
	case modelReserved:
		value := l.reserved()
		return modelResult{value: value, basis: fmt.Sprintf("R=sum(D+m(A+D)-m(A))=%d", value)}
	case modelAvailUser:
		value := maxInt64(0, l.free()-l.reserved()-l.special)
		return modelResult{value: value, basis: fmt.Sprintf("max(0,F-R-S)=%d", value)}
	case modelAvailPriv:
		value := maxInt64(0, l.free()-l.reserved())
		return modelResult{value: value, basis: fmt.Sprintf("max(0,F-R)=%d", value)}
	case modelDirty:
		files := l.dirty()
		return modelResult{files: files, basis: "files with D>0 ordered by dirty sequence"}
	default:
		panic("unknown model operation")
	}
}

func (l *modelLedger) write(id, blocks int64, privileged bool) modelResult {
	if id < 0 || blocks <= 0 {
		return modelResult{err: ErrInvalidArgument, basis: "rejected first: f<0 or n<=0"}
	}

	file := l.files[id]
	currentTotal := file.allocated + file.delayed
	nextTotal := currentTotal + blocks
	delta := blocks + modelIndexBlocks(nextTotal, l.extent) - modelIndexBlocks(currentTotal, l.extent)
	free := l.free()
	reserved := l.reserved()
	allowance := free - reserved
	caller := "privileged"
	if !privileged {
		allowance -= l.special
		caller = "non-privileged"
	}
	if delta > allowance {
		return modelResult{
			err:   ErrNoSpace,
			basis: fmt.Sprintf("rejected before writeback: %s delta=%d allowance=%d", caller, delta, allowance),
		}
	}

	l.counter++
	file.delayed += blocks
	if file.dirtySeq == 0 {
		file.dirtySeq = l.counter
	}
	l.files[id] = file

	flushed := make([]int64, 0)
	for l.delayedTotal() > l.water {
		oldest, _ := l.oldestDirty()
		l.flushFile(oldest)
		flushed = append(flushed, oldest)
	}
	return modelResult{
		flushed: flushed,
		basis:   fmt.Sprintf("accepted delta=%d allowance=%d counter=%d writeback=%v", delta, allowance, l.counter, flushed),
	}
}

func (l *modelLedger) flush(id int64) modelResult {
	if id < 0 {
		return modelResult{err: ErrInvalidArgument, basis: "rejected first: f<0"}
	}
	file, ok := l.files[id]
	if !ok {
		return modelResult{err: ErrFileNotFound, basis: "file never successfully written or unlinked"}
	}
	if file.delayed == 0 {
		return modelResult{err: ErrNoDelayedBlocks, basis: "file exists but D=0"}
	}

	oldReservation := file.delayed + modelIndexBlocks(file.allocated+file.delayed, l.extent) - modelIndexBlocks(file.allocated, l.extent)
	l.flushFile(id)
	return modelResult{basis: fmt.Sprintf("A += D; U rises by old reservation=%d; no space check", oldReservation)}
}

func (l *modelLedger) truncate(id, blocks int64) modelResult {
	if id < 0 || blocks <= 0 {
		return modelResult{err: ErrInvalidArgument, basis: "rejected first: f<0 or k<=0"}
	}
	file, ok := l.files[id]
	if !ok {
		return modelResult{err: ErrFileNotFound, basis: "file never successfully written or unlinked"}
	}
	if blocks > file.allocated+file.delayed {
		return modelResult{err: ErrFileTooLarge, basis: fmt.Sprintf("k=%d exceeds A+D=%d", blocks, file.allocated+file.delayed)}
	}

	fromDelayed := minInt64(blocks, file.delayed)
	fromAllocated := blocks - fromDelayed
	file.delayed -= fromDelayed
	file.allocated -= fromAllocated
	if file.delayed == 0 {
		file.dirtySeq = 0
	}
	l.files[id] = file
	return modelResult{basis: fmt.Sprintf("removed delayed=%d then allocated=%d; no writeback", fromDelayed, fromAllocated)}
}

func (l *modelLedger) unlink(id int64) modelResult {
	if id < 0 {
		return modelResult{err: ErrInvalidArgument, basis: "rejected first: f<0"}
	}
	file, ok := l.files[id]
	if !ok {
		return modelResult{err: ErrFileNotFound, basis: "file never successfully written or unlinked"}
	}

	delete(l.files, id)
	return modelResult{basis: fmt.Sprintf("released A=%d D=%d", file.allocated, file.delayed)}
}

func (l *modelLedger) flushFile(id int64) {
	file := l.files[id]
	file.allocated += file.delayed
	file.delayed = 0
	file.dirtySeq = 0
	l.files[id] = file
}

func (l *modelLedger) oldestDirty() (int64, bool) {
	oldestID := int64(-1)
	oldestSeq := int64(0)
	for id, file := range l.files {
		if file.delayed > 0 && (oldestID == -1 || file.dirtySeq < oldestSeq) {
			oldestID = id
			oldestSeq = file.dirtySeq
		}
	}
	return oldestID, oldestID != -1
}

func (l *modelLedger) delayedTotal() int64 {
	var total int64
	for _, file := range l.files {
		total += file.delayed
	}
	return total
}

func (l *modelLedger) used() int64 {
	var total int64
	for _, file := range l.files {
		total += file.allocated + modelIndexBlocks(file.allocated, l.extent)
	}
	return total
}

func (l *modelLedger) reserved() int64 {
	var total int64
	for _, file := range l.files {
		total += file.delayed + modelIndexBlocks(file.allocated+file.delayed, l.extent) - modelIndexBlocks(file.allocated, l.extent)
	}
	return total
}

func (l *modelLedger) free() int64 {
	return l.total - l.used()
}

func (l *modelLedger) dirty() []int64 {
	type dirtyFile struct {
		id  int64
		seq int64
	}
	files := make([]dirtyFile, 0)
	for id, file := range l.files {
		if file.delayed > 0 {
			files = append(files, dirtyFile{id: id, seq: file.dirtySeq})
		}
	}
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].seq < files[i].seq || (files[j].seq == files[i].seq && files[j].id < files[i].id) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	ids := make([]int64, len(files))
	for i, file := range files {
		ids[i] = file.id
	}
	return ids
}

func modelIndexBlocks(blocks, extent int64) int64 {
	if blocks <= 0 {
		return 0
	}
	return (blocks-1)/extent + 1
}

func randomModelOp(rng *rand.Rand) modelOp {
	op := modelOp{
		kind:       rng.Intn(modelDirty + 1),
		id:         int64(rng.Intn(7)),
		blocks:     int64(rng.Intn(12) + 1),
		privileged: rng.Intn(2) == 1,
	}
	if rng.Intn(8) == 0 {
		op.id = -1
	}
	if rng.Intn(8) == 0 {
		op.blocks = 0
	}
	return op
}

func runActualOp(l *Ledger, op modelOp) modelResult {
	switch op.kind {
	case modelWrite:
		flushed, err := l.Write(op.id, op.blocks, op.privileged)
		return modelResult{err: err, flushed: flushed}
	case modelFlush:
		return modelResult{err: l.Flush(op.id)}
	case modelTruncate:
		return modelResult{err: l.Truncate(op.id, op.blocks)}
	case modelUnlink:
		return modelResult{err: l.Unlink(op.id)}
	case modelFree:
		return modelResult{value: l.Free()}
	case modelReserved:
		return modelResult{value: l.Reserved()}
	case modelAvailUser:
		return modelResult{value: l.Avail(false)}
	case modelAvailPriv:
		return modelResult{value: l.Avail(true)}
	case modelDirty:
		return modelResult{files: l.Dirty()}
	default:
		panic("unknown actual operation")
	}
}

func sameModelResult(got, want modelResult) bool {
	return sameSentinelError(got.err, want.err) &&
		got.value == want.value &&
		sameInt64Slice(got.flushed, want.flushed) &&
		sameInt64Slice(got.files, want.files)
}

func sameInternalState(got *Ledger, want *modelLedger) bool {
	got.mu.Lock()
	defer got.mu.Unlock()
	if got.counter != want.counter || len(got.files) != len(want.files) {
		return false
	}
	for id, wantFile := range want.files {
		gotFile, ok := got.files[id]
		if !ok || gotFile != fileState(wantFile) {
			return false
		}
	}
	return true
}

func formatModelOp(op modelOp) string {
	names := []string{"write", "flush", "truncate", "unlink", "free", "reserved", "avail-user", "avail-priv", "dirty"}
	return fmt.Sprintf("%s(f=%d,n=%d,priv=%t)", names[op.kind], op.id, op.blocks, op.privileged)
}

func formatModelResult(result modelResult) string {
	if result.err != nil {
		return fmt.Sprintf("err=%v", result.err)
	}
	if result.value != 0 {
		return fmt.Sprintf("value=%d", result.value)
	}
	if result.flushed != nil || result.files != nil {
		return fmt.Sprintf("flushed=%v files=%v", result.flushed, result.files)
	}
	return "ok"
}

func sameSentinelError(got, want error) bool {
	if want == nil {
		return got == nil
	}
	if got == nil {
		return false
	}
	for _, target := range []error{ErrInvalidArgument, ErrFileNotFound, ErrNoDelayedBlocks, ErrFileTooLarge, ErrNoSpace} {
		if want == target {
			return got == target
		}
	}
	return got.Error() == want.Error()
}

func sameInt64Slice(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
