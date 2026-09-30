package norm

import (
	"ontology/eol"
	"ontology/span"
	"ontology/ws"
)

// flushTail resolves pending \r/spaces at Close.
func (n *Normalizer) flushTail() error {
	var evs []eol.Event
	if n.Fragment {
		evs = n.sp.FlushRaw(evs)
	} else {
		evs = n.sp.Flush(evs)
	}
	var items []ws.Item
	for _, ev := range evs {
		items = n.wd.Push(ev, items)
	}
	if n.Fragment {
		items = n.wd.FlushRaw(items)
		n.tailRaw = len(items)
	} else {
		items = n.wd.Flush(items)
	}
	for _, it := range items {
		if err := n.emit(it); err != nil {
			return err
		}
	}
	return nil
}

// FinishTail applies an end policy to finalized output and map (used by par).
func FinishTail(out []byte, mp *span.Map, pol Policy) []byte {
	j := &job{out: append([]byte(nil), out...), mp: mp, pol: pol}
	j.finish()
	return j.out
}

type job struct {
	out []byte
	mp  *span.Map
	pol Policy
}

func (n *Normalizer) applyPolicy() {
	j := &job{out: n.out, mp: &n.mp, pol: n.cfg.EndPolicy}
	j.finish()
	n.out = j.out
}

func (j *job) finish() {
	switch j.pol {
	case EnsureOne:
		if len(j.out) == 0 {
			return
		}
		k := j.tailNL()
		j.truncate(k)
		j.out = append(j.out, '\n')
		j.mp.Add(k, j.mp.LenOrig(), 1, 0)
	case TrimEmpty:
		k := j.tailNL()
		if k == 0 {
			j.truncate(0)
			return
		}
		j.truncate(k)
		j.out = append(j.out, '\n')
		j.mp.Add(k, j.mp.LenOrig(), 1, 0)
	}
}

func (j *job) tailNL() int {
	k := len(j.out)
	for k > 0 && j.out[k-1] == '\n' {
		k--
	}
	return k
}

// truncate drops output after o and map segments that start at/after o.
func (j *job) truncate(o int) {
	j.out = j.out[:o]
	j.mp.Truncate(o)
}
