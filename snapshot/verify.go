package snapshot

import "fmt"

// Verify 自检：从日志按序号从头重放，逐键核对快照内容与最近快照点的一致性。
func (r *Reader) Verify() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.snapSeq < 0 || r.snapSeq > int64(len(r.log)) {
		return fmt.Errorf("%w: snapshot point %d out of log range [0,%d]",
			ErrSnapshotMismatch, r.snapSeq, len(r.log))
	}
	replayed := make(map[string]string, len(r.snap))
	for i := int64(0); i < r.snapSeq; i++ {
		rec := r.log[i]
		if rec.Seq != i+1 {
			return fmt.Errorf("%w: log[%d] has seq %d, want %d",
				ErrSnapshotMismatch, i, rec.Seq, i+1)
		}
		replayed[rec.Key] = rec.Value
	}
	if len(replayed) != len(r.snap) {
		return fmt.Errorf("%w: snapshot has %d keys, replay has %d keys at point %d",
			ErrSnapshotMismatch, len(r.snap), len(replayed), r.snapSeq)
	}
	for key, want := range replayed {
		got, ok := r.snap[key]
		if !ok {
			return fmt.Errorf("%w: key %q missing from snapshot at point %d",
				ErrSnapshotMismatch, key, r.snapSeq)
		}
		if got != want {
			return fmt.Errorf("%w: key %q snapshot=%q replay=%q at point %d",
				ErrSnapshotMismatch, key, got, want, r.snapSeq)
		}
	}
	for key := range r.snap {
		if _, ok := replayed[key]; !ok {
			return fmt.Errorf("%w: key %q unexpectedly present in snapshot at point %d",
				ErrSnapshotMismatch, key, r.snapSeq)
		}
	}
	return nil
}
