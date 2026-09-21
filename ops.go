package ontology

// Set operations merge two run lists directly in the compressed domain.
// They never expand runs into per-bit representations. Each returns the
// result bitmap plus the number of run-advance steps performed, so callers
// can verify that cost is O(runs), not O(bits).

// Union returns the set union of b and o, merging runs on the fly.
func (b *Bitmap) Union(o *Bitmap) (*Bitmap, int) {
	a, c := b.snapshot(), o.snapshot()
	out := make([]run, 0, len(a)+len(c))
	steps := 0
	i, j := 0, 0
	for i < len(a) || j < len(c) {
		var next run
		if j >= len(c) || (i < len(a) && a[i].start <= c[j].start) {
			next = a[i]
			i++
		} else {
			next = c[j]
			j++
		}
		steps++
		if n := len(out); n > 0 && uint64(next.start) <= out[n-1].end()+1 {
			if e := next.end(); e > out[n-1].end() {
				out[n-1].length = e - uint64(out[n-1].start) + 1
			}
			continue
		}
		out = append(out, next)
	}
	return &Bitmap{runs: out}, steps
}

// Intersect returns the set intersection of b and o.
func (b *Bitmap) Intersect(o *Bitmap) (*Bitmap, int) {
	a, c := b.snapshot(), o.snapshot()
	var out []run
	steps := 0
	i, j := 0, 0
	for i < len(a) && j < len(c) {
		lo := uint64(a[i].start)
		if s := uint64(c[j].start); s > lo {
			lo = s
		}
		hi := a[i].end()
		if e := c[j].end(); e < hi {
			hi = e
		}
		if lo <= hi {
			out = append(out, run{start: uint32(lo), length: hi - lo + 1})
		}
		if a[i].end() < c[j].end() {
			i++
		} else {
			j++
		}
		steps++
	}
	return &Bitmap{runs: out}, steps
}

// Difference returns the set of bits in b but not in o.
func (b *Bitmap) Difference(o *Bitmap) (*Bitmap, int) {
	a, c := b.snapshot(), o.snapshot()
	var out []run
	steps := 0
	j := 0
	for i := 0; i < len(a); i++ {
		start := uint64(a[i].start)
		end := a[i].end()
		for j < len(c) && c[j].end() < start {
			j++
			steps++
		}
		for k := j; k < len(c) && uint64(c[k].start) <= end; k++ {
			cs, ce := uint64(c[k].start), c[k].end()
			if cs > start {
				out = append(out, run{start: uint32(start), length: cs - start})
			}
			steps++
			if ce >= end {
				start = end + 1
				break
			}
			start = ce + 1
		}
		if start <= end {
			out = append(out, run{start: uint32(start), length: end - start + 1})
		}
		steps++
	}
	return &Bitmap{runs: out}, steps
}
