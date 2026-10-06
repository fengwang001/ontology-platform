package defassign

// edge is a control-flow edge. token lists the path-decision tokens carried
// by the edge; back marks loop back edges, which the definite-assignment
// analysis ignores (a read inside a loop may not observe assignments made
// by a previous iteration) but the liveness analysis follows.
type edge struct {
	to    int
	token []string
	back  bool
}

// cfg is the lowered control-flow graph. Node IDs coincide with Registry
// node IDs; no synthetic nodes are introduced.
type cfg struct {
	succ [][]edge
}

// openEdge is a dangling edge endpoint produced while assembling fragments:
// from is the source node and token accumulates decision tokens.
type openEdge struct {
	from  int
	token []string
}

// frag is a partially built graph fragment.
type frag struct {
	entries []openEdge // points where control enters the fragment
	exits   []openEdge // fall-through exits
	breaks  []openEdge // break exits, bound by the enclosing loop
	returns []openEdge // return exits, leave the program
}

func concatToken(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

type cfgBuilder struct {
	reg *Registry
	cfg *cfg
}

// buildCFG lowers the registered program tree to a control-flow graph and
// returns it together with the entry points of the root fragment.
func buildCFG(reg *Registry) (*cfg, []openEdge) {
	b := &cfgBuilder{reg: reg, cfg: &cfg{succ: make([][]edge, reg.NumNodes())}}
	root, _ := reg.Root()
	f := b.build(root)
	return b.cfg, f.entries
}

func (b *cfgBuilder) addEdge(from, to int, token []string, back bool) {
	b.cfg.succ[from] = append(b.cfg.succ[from], edge{to: to, token: token, back: back})
}

// connect links every exit to every entry, concatenating decision tokens.
func (b *cfgBuilder) connect(exits, entries []openEdge, back bool) {
	for _, e := range exits {
		for _, en := range entries {
			b.addEdge(e.from, en.from, concatToken(e.token, en.token), back)
		}
	}
}

func (b *cfgBuilder) buildList(ids []int) frag {
	var res frag
	var pending []openEdge
	started := false
	for _, id := range ids {
		f := b.build(id)
		if len(f.entries) == 0 {
			continue // empty fragment (e.g. empty block): pass through
		}
		if !started {
			res.entries = f.entries
			started = true
		} else {
			b.connect(pending, f.entries, false)
		}
		pending = f.exits
		res.breaks = append(res.breaks, f.breaks...)
		res.returns = append(res.returns, f.returns...)
	}
	res.exits = pending
	return res
}

func (b *cfgBuilder) build(id int) frag {
	n := b.reg.Node(id)
	self := openEdge{from: id}
	switch n.Kind {
	case KindAssign, KindRead:
		return frag{entries: []openEdge{self}, exits: []openEdge{self}}
	case KindBreak:
		return frag{
			entries: []openEdge{self},
			breaks:  []openEdge{{from: id, token: []string{tokBreak(id)}}},
		}
	case KindReturn:
		return frag{
			entries: []openEdge{self},
			returns: []openEdge{{from: id, token: []string{tokReturn(id)}}},
		}
	case KindBlock:
		return b.buildList(n.Children)
	case KindIf:
		f := frag{entries: []openEdge{self}}
		if n.Cond != CondFalse {
			thenTok := []string{tokIfThen(id)}
			tf := b.buildList(n.Then)
			b.connect([]openEdge{{from: id, token: thenTok}}, tf.entries, false)
			if len(tf.entries) == 0 {
				f.exits = append(f.exits, openEdge{from: id, token: thenTok})
			}
			f.exits = append(f.exits, tf.exits...)
			f.breaks = append(f.breaks, tf.breaks...)
			f.returns = append(f.returns, tf.returns...)
		}
		if n.Cond != CondTrue {
			elseTok := []string{tokIfElse(id)}
			ef := b.buildList(n.Else)
			b.connect([]openEdge{{from: id, token: elseTok}}, ef.entries, false)
			if len(ef.entries) == 0 {
				f.exits = append(f.exits, openEdge{from: id, token: elseTok})
			}
			f.exits = append(f.exits, ef.exits...)
			f.breaks = append(f.breaks, ef.breaks...)
			f.returns = append(f.returns, ef.returns...)
		}
		return f
	case KindLoop:
		f := frag{entries: []openEdge{self}}
		bodyTok := []string{tokLoopBody(id)}
		bf := b.buildList(n.Body)
		b.connect([]openEdge{{from: id, token: bodyTok}}, bf.entries, false)
		if len(bf.entries) == 0 {
			// Empty body: falls straight through and loops back to itself.
			f.exits = append(f.exits, openEdge{from: id, token: bodyTok})
			b.addEdge(id, id, nil, true)
		} else {
			f.exits = append(f.exits, bf.exits...)
			b.connect(bf.exits, bf.entries, true) // back edge, liveness only
		}
		if !n.AtLeastOnce {
			f.exits = append(f.exits, openEdge{from: id, token: []string{tokLoopZero(id)}})
		}
		f.exits = append(f.exits, bf.breaks...)
		f.returns = append(f.returns, bf.returns...)
		return f
	case KindTry:
		f := frag{entries: []openEdge{self}}
		bodyTok := []string{tokTryBody(id)}
		bf := b.buildList(n.Body)
		b.connect([]openEdge{{from: id, token: bodyTok}}, bf.entries, false)
		var cleanupIn []openEdge
		if len(bf.entries) == 0 {
			cleanupIn = append(cleanupIn, openEdge{from: id, token: bodyTok})
		}
		cleanupIn = append(cleanupIn, bf.exits...)
		f.breaks = append(f.breaks, bf.breaks...)
		f.returns = append(f.returns, bf.returns...)
		for i, h := range n.Handlers {
			// Handler entry state is the state before the protected body:
			// a throw may happen at any point of the body.
			hTok := []string{tokTryHandler(id, i)}
			hf := b.buildList(b.reg.Node(h).Body)
			b.connect([]openEdge{{from: id, token: hTok}}, hf.entries, false)
			if len(hf.entries) == 0 {
				cleanupIn = append(cleanupIn, openEdge{from: id, token: hTok})
			}
			cleanupIn = append(cleanupIn, hf.exits...)
			f.breaks = append(f.breaks, hf.breaks...)
			f.returns = append(f.returns, hf.returns...)
		}
		// Unhandled throw out of the body reaches the cleanup region with
		// the pre-try state.
		cleanupIn = append(cleanupIn, openEdge{from: id, token: []string{tokTryUnhandled(id)}})
		cf := b.buildList(n.Cleanup)
		b.connect(cleanupIn, cf.entries, false)
		if len(cf.entries) == 0 {
			f.exits = append(f.exits, cleanupIn...)
		} else {
			f.exits = append(f.exits, cf.exits...)
		}
		f.breaks = append(f.breaks, cf.breaks...)
		f.returns = append(f.returns, cf.returns...)
		return f
	}
	return frag{}
}
