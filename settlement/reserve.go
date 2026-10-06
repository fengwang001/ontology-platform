package settlement

// reserveQueue holds active reserve batches in retention-day (hence also
// release-day, because H is fixed per merchant) order.
//
// Batches are created on at most one batch per settled business day, popped in
// order when due, and consumed from the front when covering a deficit. A batch
// that is fully consumed is dropped and never releases; a partially consumed
// batch releases only its remaining Available balance when due.
//
// Cost per settled business day is proportional to the number of batches that
// mature plus the number touched by consumption on that day only. Historical
// batches are never scanned.
type reserveQueue struct {
	batches []ReserveBatch
	balance Amount // sum of Available over active batches
}

func newReserveQueue() *reserveQueue { return &reserveQueue{} }

func (q *reserveQueue) push(b ReserveBatch) {
	if b.Amount <= 0 {
		return
	}
	q.batches = append(q.batches, b)
	q.balance += b.Amount
}

// releaseDue pops batches whose ReleaseDay <= d and returns the total released
// available amount together with the released batch snapshots. Fully consumed
// batches carry zero Available and are simply dropped.
func (q *reserveQueue) releaseDue(d Day) (Amount, []ReserveBatch) {
	var released Amount
	out := make([]ReserveBatch, 0)
	i := 0
	for ; i < len(q.batches); i++ {
		b := q.batches[i]
		if b.ReleaseDay > d {
			break
		}
		avail := b.Available()
		q.balance -= avail
		released += avail
		if avail != 0 {
			out = append(out, b)
		}
	}
	q.batches = q.batches[i:]
	return released, out
}

// consumeFromOlder spends from active, not-yet-due batches in retention-day
// order to fill deficit (> 0) up to zero. deficit is reduced in place and the
// remaining still-negative gap is returned. (Due batches must have been
// released first; this method only touches the surviving queue front.)
func (q *reserveQueue) consumeFromOlder(deficit Amount) Amount {
	for deficit > 0 && len(q.batches) > 0 {
		b := &q.batches[0]
		avail := b.Available()
		if avail <= deficit {
			deficit -= avail
			q.balance -= avail
			q.batches = q.batches[1:]
		} else {
			b.Consumed += deficit
			q.balance -= deficit
			deficit = 0
		}
	}
	return deficit
}

func (q *reserveQueue) reserveBalance() Amount { return q.balance }

func (q *reserveQueue) snapshot() []ReserveBatch {
	out := make([]ReserveBatch, len(q.batches))
	copy(out, q.batches)
	return out
}
