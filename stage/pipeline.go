package stage

import (
	"context"
	"os"
	"sync"
	"time"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/sink"
	"ontology/source"
)

type errBox struct {
	mu  sync.Mutex
	err error
}

func (b *errBox) set(e error) {
	b.mu.Lock()
	if b.err == nil {
		b.err = e
	}
	b.mu.Unlock()
}
func (b *errBox) get() error { b.mu.Lock(); defer b.mu.Unlock(); return b.err }

// Run 执行一次管线（含崩溃恢复初始化），返回报告。
func (p *Pipe) Run(ctx context.Context) Report {
	store := ckpt.NewStore(p.cfg.CkptDir)
	state, fellBack, _ := store.Load()
	_, _ = sink.CleanupTemp(p.cfg.OutDir)
	groups := state.Snapshot()
	badCount := state.Bad
	startPos := state.Position()

	q1 := make(chan msg, p.cfg.QueueCap)
	q2 := make(chan msg, p.cfg.QueueCap)
	parser := &parse.Parser{Bad: badCount}
	var pre func()
	if p.cfg.Crash.PreRename {
		pre = func() { os.Exit(88) }
	}
	sk := sink.New(p.cfg.OutDir, time.Duration(p.cfg.SinkDelay), pre, -1)

	var wg sync.WaitGroup
	errb := &errBox{}
	wg.Add(3)
	go p.feeder(ctx, q1, startPos, &wg, errb)
	go p.parsed(q1, q2, parser, &wg, errb)
	go p.committer(q2, sk, store, groups, &parser.Bad, &wg, errb)
	wg.Wait()

	rep := Report{Bad: parser.Bad, Blocked: p.blocked.Load(),
		MaxInFlight: p.in.maximum(), FellBack: fellBack, Err: errb.get()}
	if path, pos, _ := sink.LatestOutput(p.cfg.OutDir); path != "" {
		rep.OutputPath, rep.Pos = path, pos
		rep.OutputBytes, _ = os.ReadFile(path)
	}
	return rep
}

func (p *Pipe) parsed(q1 <-chan msg, q2 chan<- msg, parser *parse.Parser,
	wg *sync.WaitGroup, errb *errBox) {
	defer wg.Done()
	defer close(q2)
	for m := range q1 {
		if m.barrier != 0 {
			p.in.add(-1)
			if err := p.forward(q2, m); err != nil {
				errb.set(err)
				return
			}
			continue
		}
		rec, ok := parser.Parse(source.Frame{Pos: m.pos, Data: []byte(m.key)})
		if !ok {
			p.in.add(-1)
			continue
		}
		m.key, m.val = rec.Key, rec.Val
		if err := p.forward(q2, m); err != nil {
			errb.set(err)
			return
		}
	}
}

func (p *Pipe) forward(q2 chan<- msg, m msg) error {
	select {
	case q2 <- m:
		return nil
	case <-p.kill:
		return ErrCrashed
	}
}
