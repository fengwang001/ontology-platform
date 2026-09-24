package stage

import (
	"sync"

	"ontology/ckpt"
	"ontology/sink"
)

func (p *Pipe) committer(q2 <-chan msg, sk *sink.Sink, store *ckpt.Store,
	groups map[string]ckpt.Group, badCount *int64,
	wg *sync.WaitGroup, errb *errBox) {
	defer wg.Done()
	jobs := make(chan struct{}, p.cfg.Workers)
	var wwg sync.WaitGroup
	for i := 0; i < p.cfg.Workers; i++ {
		wwg.Add(1)
		go func() {
			defer wwg.Done()
			for range jobs {
				sk.DelayPerRecord(1)
				p.in.add(-1)
			}
		}()
	}
	crashed := sync.Once{}
	for m := range q2 {
		if m.barrier != 0 {
			wwg.Wait()
			rows := make([]sink.Row, 0, len(groups))
			for k, g := range groups {
				rows = append(rows, sink.Row{Key: k, Sum: g.Sum, Cnt: g.Cnt})
			}
			if _, err := sk.Commit(m.barrier, rows); err != nil {
				errb.set(err)
				p.Stop()
				break
			}
			p.in.add(-1)
			st := ckpt.State{Pos: m.barrier, Bad: *badCount, Groups: groups}
			if err := store.Save(st); err != nil {
				errb.set(err)
				p.Stop()
				break
			}
			continue
		}
		g := groups[m.key]
		if g.Cnt == 0 && p.cfg.MaxGroups > 0 && len(groups) >= p.cfg.MaxGroups {
			errb.set(ErrTooManyGroups)
			p.Stop()
			break
		}
		g.Sum += m.val
		g.Cnt++
		groups[m.key] = g
		if p.cfg.Crash.MidAggregate {
			crashed.Do(func() { errb.set(ErrCrashed); p.Stop() })
			if p.stopped.Load() {
				break
			}
		}
		select {
		case jobs <- struct{}{}:
		case <-p.kill:
			errb.set(ErrCrashed)
		}
		if p.stopped.Load() {
			break
		}
	}
	close(jobs)
	wwg.Wait()
}
