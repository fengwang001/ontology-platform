package ledger

// Recover replays the WAL and applies every record not yet applied.
// shard.Set.Apply is idempotent, so repeated Recover calls converge:
// the second call applies nothing and reports replayed == 0. Recover
// also advances nextTxn past every transaction id present in the WAL,
// so post-recovery transfers can never reuse an id.
func (g *Ledger) Recover() (replayed int, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	recs, err := g.log.Replay(0)
	if err != nil {
		return 0, err
	}
	for _, r := range recs {
		if g.shards.Apply(r) {
			replayed++
		}
		if r.Txn > g.nextTxn {
			g.nextTxn = r.Txn
		}
	}
	g.meta = g.nextTxn
	return replayed, nil
}

// Checkpoint truncates the WAL to the largest Seq such that every
// record at or below it has been applied by all shards. Records past
// the first unapplied one stay in the WAL, so a crash right after the
// checkpoint is still recovered correctly by replaying the suffix.
func (g *Ledger) Checkpoint() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	recs, err := g.log.Replay(0)
	if err != nil {
		return err
	}
	var upto uint64
	for _, r := range recs {
		if r.Txn > g.shards.LastTxn(r.Shard) {
			break // first unapplied record ends the safe prefix
		}
		upto = r.Seq
	}
	return g.log.Truncate(upto)
}
