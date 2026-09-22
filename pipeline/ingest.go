package pipeline

import (
	"errors"

	"ontology/budget"
	"ontology/record"
)

type ingestJob struct {
	key   string
	value []byte
	size  uint64
	reply chan ingestReply
}

type ingestReply struct {
	seq uint64
	err error
}

// Ingest enqueues a record. After Close it returns ErrClosed; an
// oversized record returns ErrRecordTooLarge and is not accepted.
func (p *Pipeline) Ingest(key string, value []byte) (uint64, error) {
	r := record.Record{Key: key, Value: value}
	j := ingestJob{
		key:   key,
		value: append([]byte(nil), value...),
		size:  uint64(r.Size()),
		reply: make(chan ingestReply, 1),
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return 0, ErrClosed
	}
	jobs := p.jobs
	p.mu.Unlock()
	jobs <- j
	rep := <-j.reply
	return rep.seq, rep.err
}

func (p *Pipeline) runCoordinator() {
	for {
		select {
		case <-p.stopSig:
			close(p.doneSig)
			return
		case j := <-p.jobs:
			p.mu.Lock()
			stopping := p.stopping
			p.mu.Unlock()
			if stopping {
				j.reply <- ingestReply{err: ErrClosed}
				continue
			}
			p.acceptOrPark(j)
		}
	}
}

// acceptOrPark either charges and appends a job, or parks it until a
// spill frees budget. It is the only place residency/budget mutates.
func (p *Pipeline) acceptOrPark(j ingestJob) {
	ok, err := p.budget.TryCharge(j.size)
	if err != nil {
		if errors.Is(err, budget.ErrRecordTooLarge) {
			j.reply <- ingestReply{err: ErrRecordTooLarge}
		} else {
			j.reply <- ingestReply{err: err}
		}
		return
	}
	if !ok {
		p.waiting = append(p.waiting, j)
		p.underPressure()
		return
	}
	p.appendCharged(j)
}

func (p *Pipeline) appendCharged(j ingestJob) {
	r := record.Record{Key: j.key, Value: j.value, Seq: p.nextSeq}
	p.nextSeq++
	p.resident = append(p.resident, r)
	p.resBytes += j.size
	j.reply <- ingestReply{seq: r.Seq}
	if p.resBytes >= p.budget.Limit() {
		p.underPressure()
	}
}

// underPressure spills the current batch (blocking further acceptance
// while budget is occupied), then retries parked jobs in arrival order.
func (p *Pipeline) underPressure() {
	if err := p.spillOnce(false); err != nil {
		p.spillErr = err
		return
	}
	waiting := p.waiting
	p.waiting = nil
	for _, j := range waiting {
		p.acceptOrPark(j)
	}
}
