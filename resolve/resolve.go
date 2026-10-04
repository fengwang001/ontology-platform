package resolve

import (
	"errors"
	"math/big"
	"sort"
	"sync"

	"ontology/hold"
	"ontology/syncpt"
)

var (
	ErrInvalid  = errors.New("invalid argument")
	ErrNoDevice = errors.New("device is not registered")
	ErrSealed   = errors.New("boot is sealed")
	ErrFull     = errors.New("pending record limit reached")
	ErrDupSync  = syncpt.ErrDupSync
	ErrSkew     = syncpt.ErrSkew
)

type Source int

const (
	Interp Source = iota
	Back
	Fwd
	Est
)

type Record = hold.Record

type Emit struct {
	Record Record
	Wall   int64
	Source Source
}

type bootState struct {
	syncs     *syncpt.Set
	pending   *hold.Buffer
	sealed    bool
	estimated bool
	start     int64
	maxK      int64
}

type deviceState struct {
	boots   map[int64]*bootState
	maxBoot int64
	pending int
}

type Resolver struct {
	mu   sync.Mutex
	dev  map[int64]*deviceState
	pmax int
}

func New(pmax int) (*Resolver, error) {
	if pmax < 1 || pmax > 1_000_000 {
		return nil, ErrInvalid
	}
	return &Resolver{dev: make(map[int64]*deviceState), pmax: pmax}, nil
}

func (r *Resolver) Register(dev int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.dev[dev]; !ok {
		r.dev[dev] = &deviceState{boots: make(map[int64]*bootState)}
	}
}

func (r *Resolver) Record(dev, boot, k int64, payload any) ([]Emit, error) {
	if !validRecord(dev, boot, k) {
		return nil, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.dev[dev]
	if !ok {
		return nil, ErrNoDevice
	}
	if d.maxBoot != 0 && boot < d.maxBoot {
		return nil, ErrSealed
	}
	if boot > d.maxBoot {
		if postSealPending(d, d.maxBoot) >= r.pmax {
			return nil, ErrFull
		}
	}
	b := getBoot(d, boot)
	rec := Record{Dev: dev, Boot: boot, K: k, Payload: payload}
	if boot == d.maxBoot {
		if emit, ok := emitWithSyncs(b, rec); ok {
			return emit, nil
		}
		if d.pending >= r.pmax {
			return nil, ErrFull
		}
	}
	sealed := []Emit{}
	if boot > d.maxBoot {
		sealed = r.sealOpen(d)
		d.maxBoot = boot
	}
	b.pending.Add(rec)
	d.pending++
	if !b.sealed && k > b.maxK {
		b.maxK = k
	}
	return sealed, nil
}

func (r *Resolver) Sync(dev, boot, k, w int64) ([]Emit, error) {
	if !validSync(dev, boot, k, w) {
		return nil, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.dev[dev]
	if !ok {
		return nil, ErrNoDevice
	}
	if d.maxBoot != 0 && boot < d.maxBoot {
		return nil, ErrSealed
	}
	if boot > d.maxBoot && postSealPending(d, d.maxBoot) >= r.pmax {
		return nil, ErrFull
	}
	existing := d.boots[boot]
	if existing != nil {
		if err := existing.syncs.Check(k, w); err != nil {
			return nil, err
		}
	} else {
		prospective := syncpt.NewSet()
		if err := prospective.Check(k, w); err != nil {
			return nil, err
		}
	}
	sealed := []Emit{}
	if boot > d.maxBoot {
		sealed = r.sealOpen(d)
		d.maxBoot = boot
	}
	b := getBoot(d, boot)
	if err := b.syncs.Add(k, w); err != nil {
		return nil, err
	}
	out := sealed
	firstSync := b.syncs.Len() == 1
	if firstSync {
		b.start = w - k
	}
	released := b.pending.ReleaseLE(k)
	d.pending -= len(released)
	for _, rec := range released {
		out = append(out, emitWithSyncsMust(b, rec))
	}
	if firstSync {
		out = append(out, r.estimateBefore(d, boot, b.start)...)
	}
	return out, nil
}

func getBoot(d *deviceState, boot int64) *bootState {
	b := d.boots[boot]
	if b == nil {
		b = &bootState{
			syncs:   syncpt.NewSet(),
			pending: hold.NewBuffer(),
		}
		d.boots[boot] = b
	}
	return b
}

func postSealPending(d *deviceState, oldBoot int64) int {
	if oldBoot == 0 {
		return 0
	}
	current := d.boots[oldBoot]
	if current == nil || current.syncs.Len() == 0 {
		return d.pending
	}
	return d.pending - current.pending.Len()
}

func (r *Resolver) sealOpen(d *deviceState) []Emit {
	if d.maxBoot == 0 {
		return nil
	}
	b := d.boots[d.maxBoot]
	if b == nil {
		return nil
	}
	b.sealed = true
	if b.syncs.Len() == 0 {
		if maxK, ok := b.pending.MaxK(); ok {
			b.maxK = maxK
		}
		return nil
	}
	last, _ := b.syncs.Last()
	records := b.pending.ReleaseAll()
	d.pending -= len(records)
	out := make([]Emit, 0, len(records))
	for _, rec := range records {
		out = append(out, Emit{Record: rec, Wall: last.W + (rec.K - last.K), Source: Fwd})
	}
	return out
}

func (r *Resolver) estimateBefore(d *deviceState, boot int64, nextStart int64) []Emit {
	ids := bootIDsBelow(d, boot)
	out := []Emit{}
	next := nextStart
	for i := len(ids) - 1; i >= 0; i-- {
		b := d.boots[ids[i]]
		if b.syncs.Len() > 0 || b.estimated {
			break
		}
		if !b.sealed {
			break
		}
		start := next - 1 - b.maxK
		b.start = start
		b.estimated = true
		records := b.pending.ReleaseAll()
		d.pending -= len(records)
		for _, rec := range records {
			out = append(out, Emit{Record: rec, Wall: start + rec.K, Source: Est})
		}
		next = start
	}
	return out
}

func bootIDsBelow(d *deviceState, boot int64) []int64 {
	ids := make([]int64, 0, len(d.boots))
	for id := range d.boots {
		if id < boot {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func emitWithSyncs(b *bootState, rec Record) ([]Emit, bool) {
	if b.syncs.Len() == 0 {
		return nil, false
	}
	p, hasP := b.syncs.AtOrBelow(rec.K)
	n, hasN := b.syncs.AtOrAbove(rec.K)
	if hasN && n.K == rec.K {
		return []Emit{{Record: rec, Wall: n.W, Source: Interp}}, true
	}
	if hasP && hasN && p.K != n.K {
		return []Emit{{Record: rec, Wall: interpolate(p, n, rec.K), Source: Interp}}, true
	}
	if !hasP && hasN {
		return []Emit{{Record: rec, Wall: n.W - (n.K - rec.K), Source: Back}}, true
	}
	return nil, false
}

func emitWithSyncsMust(b *bootState, rec Record) Emit {
	out, ok := emitWithSyncs(b, rec)
	if !ok {
		panic("released record cannot be resolved")
	}
	return out[0]
}

func interpolate(p, n syncpt.Point, k int64) int64 {
	num := new(big.Int).Mul(big.NewInt(k-p.K), big.NewInt(n.W-p.W))
	num.Div(num, big.NewInt(n.K-p.K))
	return p.W + num.Int64()
}

func validRecord(dev, boot, k int64) bool {
	return boot >= 1 && boot <= 1_000_000 && k >= 0 && k <= 1_000_000_000
}

func validSync(dev, boot, k, w int64) bool {
	return validRecord(dev, boot, k) && w >= 0 && w <= 1_000_000_000_000_000
}

func (s Source) String() string {
	switch s {
	case Interp:
		return "Interp"
	case Back:
		return "Back"
	case Fwd:
		return "Fwd"
	case Est:
		return "Est"
	default:
		return "Unknown"
	}
}

func (r *Resolver) getPendingForTest(dev int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dev[dev].pending
}
