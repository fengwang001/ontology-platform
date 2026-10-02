package ontology

import (
	"container/heap"
	"math"
	"sync"
)

// MacFunc is the injectable deterministic MAC.
type MacFunc func(key, data []byte) []byte

// Config constructs a service.
type Config struct {
	K   []byte
	Mac MacFunc
	Cm  int
	Lc  int
	Rm  int
}

// Token is a chained macaroon.
type Token struct {
	ID      []byte
	Caveats [][]byte
	Sig     []byte
}

// Request is a Verify request.
type Request struct {
	Op     string
	Res    string
	Amount int64
}

// RevokeResult reports a revocation outcome.
type RevokeResult struct {
	Revoked bool
	Expired bool
}

const boundInfinity = math.MaxInt64

type revKey struct {
	id string
	k  int
}

type revRecord struct {
	key   revKey
	sig   []byte
	bound int64 // smallest valid exp N among the first k caveats; boundInfinity if none
	index int   // heap index
}

type revHeap []*revRecord

func (h revHeap) Len() int           { return len(h) }
func (h revHeap) Less(i, j int) bool { return h[i].bound < h[j].bound }
func (h revHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}
func (h *revHeap) Push(x any) {
	r := x.(*revRecord)
	r.index = len(*h)
	*h = append(*h, r)
}
func (h *revHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return r
}

// Service is a concurrent-safe macaroon service.
type Service struct {
	mu         sync.Mutex
	cfg        Config
	clock      int64
	clockSet   bool
	table      map[revKey]*revRecord
	heap       revHeap
	active     int
	macCalls   int64
	revQueries int64
	heapPops   int64
}

// countersMu guards Attenuate, which has no service instance.
var (
	countersMu        sync.Mutex
	attenuateMacCalls int64
)

func validConfig(cfg Config) bool {
	if len(cfg.K) == 0 || cfg.Mac == nil {
		return false
	}
	if cfg.Cm < 1 || cfg.Cm > 32 || cfg.Lc < 1 || cfg.Lc > 256 || cfg.Rm < 1 || cfg.Rm > 1e6 {
		return false
	}
	// Config must be sound: a MAC result must be usable as the next key.
	if len(cfg.Mac(cfg.K, []byte("cfg"))) == 0 {
		return false
	}
	return true
}

// New validates Config and returns a service.
func New(cfg Config) (*Service, error) {
	if !validConfig(cfg) {
		return nil, &Error{Kind: RejectInvalid}
	}
	return &Service{
		cfg:   cfg,
		clock: 0,
		table: make(map[revKey]*revRecord),
	}, nil
}

func validTokenShape(tok *Token, cfg Config) bool {
	if tok == nil || len(tok.ID) < 1 || len(tok.ID) > 64 || len(tok.Sig) == 0 {
		return false
	}
	if len(tok.Caveats) > cfg.Cm {
		return false
	}
	for _, c := range tok.Caveats {
		if len(c) < 1 || len(c) > cfg.Lc {
			return false
		}
	}
	return true
}

func validMintCaveats(caveats [][]byte, cfg Config) bool {
	if len(caveats) > cfg.Cm {
		return false
	}
	for _, c := range caveats {
		if len(c) < 1 || len(c) > cfg.Lc {
			return false
		}
		p := parseCaveat(c)
		if p.kind == cavUnknown || !p.valid {
			return false
		}
	}
	return true
}

func validRequest(req Request) bool {
	if len(req.Op) == 0 || len(req.Res) == 0 || req.Res[0] != '/' {
		return false
	}
	if req.Amount < 0 || req.Amount > 1e12 {
		return false
	}
	return true
}

func validNow(now int64) bool { return now >= 0 && now <= 1e15 }

// chain recomputes s0..sn, charging exactly n+1 MAC calls.
func (s *Service) chain(tok *Token) [][]byte {
	sigs := make([][]byte, len(tok.Caveats)+1)
	sigs[0] = s.cfg.Mac(s.cfg.K, tok.ID)
	s.macCalls++
	for j, c := range tok.Caveats {
		sigs[j+1] = s.cfg.Mac(sigs[j], c)
		s.macCalls++
	}
	return sigs
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// gc removes all records whose bound <= now. Pops per GC call are bounded by
// the number of records reclaimed in this call plus one.
func (s *Service) gc(now int64) {
	for len(s.heap) > 0 && s.heap[0].bound <= now {
		r := heap.Pop(&s.heap).(*revRecord)
		delete(s.table, r.key)
		s.active--
		s.heapPops++
	}
}

// activeCount returns the number of live (bound > now) records without
// mutating state; used by Revoke's limit decision before acceptance.
func (s *Service) activeCount(now int64) int {
	count := 0
	for _, r := range s.heap {
		if r.bound > now {
			count++
		}
	}
	return count
}

// lookup checks (id, j, sig) for j in order and returns the smallest live hit.
func (s *Service) lookup(id string, sigs [][]byte, now int64) (int, bool) {
	for j, sig := range sigs {
		s.revQueries++
		r, ok := s.table[revKey{id, j}]
		if !ok || r.bound <= now {
			continue
		}
		if bytesEqual(r.sig, sig) {
			return j, true
		}
	}
	return 0, false
}

// Mint mints a token.
func (s *Service) Mint(id []byte, caveats [][]byte, now int64) (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(id) < 1 || len(id) > 64 || !validMintCaveats(caveats, s.cfg) {
		return nil, &Error{Kind: RejectInvalid}
	}
	if !validNow(now) {
		return nil, &Error{Kind: RejectInvalid}
	}
	if s.clockSet && now < s.clock {
		return nil, &Error{Kind: RejectClockRollback}
	}
	s.clock, s.clockSet = now, true
	s.gc(now)
	tok := &Token{ID: id, Caveats: caveats}
	sigs := s.chain(tok)
	tok.Sig = sigs[len(sigs)-1]
	return tok, nil
}

// Attenuate is a state-free pure function: it never inspects the old token's
// authenticity.
func Attenuate(cfg Config, tok *Token, caveat []byte) (*Token, error) {
	if !validConfig(cfg) || tok == nil || len(tok.Sig) == 0 ||
		len(caveat) < 1 || len(caveat) > cfg.Lc {
		return nil, &Error{Kind: RejectInvalid}
	}
	if len(tok.Caveats) >= cfg.Cm {
		return nil, &Error{Kind: RejectLimit}
	}
	countersMu.Lock()
	attenuateMacCalls++
	countersMu.Unlock()
	sig := cfg.Mac(tok.Sig, caveat)
	newCaveats := make([][]byte, 0, len(tok.Caveats)+1)
	newCaveats = append(newCaveats, tok.Caveats...)
	newCaveats = append(newCaveats, caveat)
	return &Token{ID: tok.ID, Caveats: newCaveats, Sig: sig}, nil
}

// Verify checks a token in the mandated order.
func (s *Service) Verify(tok *Token, req Request, now int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTokenShape(tok, s.cfg) || !validRequest(req) || !validNow(now) {
		return &Error{Kind: RejectInvalid}
	}
	if s.clockSet && now < s.clock {
		return &Error{Kind: RejectClockRollback}
	}
	// Accepted through the two gates: advance clock and reclaim.
	s.clock, s.clockSet = now, true
	s.gc(now)
	sigs := s.chain(tok)
	if !bytesEqual(sigs[len(sigs)-1], tok.Sig) {
		return &Error{Kind: RejectBadSignature}
	}
	if j, ok := s.lookup(string(tok.ID), sigs, now); ok {
		return &Error{Kind: RejectRevoked, Prefix: j}
	}
	for i, c := range tok.Caveats {
		p := parseCaveat(c)
		switch {
		case p.kind == cavUnknown:
			return &Error{Kind: RejectCaveat, Index: i + 1, Problem: CaveatUnknown}
		case !p.valid:
			return &Error{Kind: RejectCaveat, Index: i + 1, Problem: CaveatMalformed}
		case !p.satisfied(req, now):
			return &Error{Kind: RejectCaveat, Index: i + 1, Problem: CaveatUnsatisfied}
		}
	}
	return nil
}

func prefixBound(caveats [][]byte, k int) int64 {
	bound := int64(boundInfinity)
	found := false
	for i := 0; i < k; i++ {
		p := parseCaveat(caveats[i])
		if p.kind == cavExp && p.valid && (!found || p.expN < bound) {
			bound, found = p.expN, true
		}
	}
	return bound
}

// Revoke revokes the length-k prefix of a token.
func (s *Service) Revoke(tok *Token, k int, now int64) (*RevokeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validTokenShape(tok, s.cfg) || !validNow(now) || k < 0 || k > len(tok.Caveats) {
		return nil, &Error{Kind: RejectInvalid}
	}
	if s.clockSet && now < s.clock {
		return nil, &Error{Kind: RejectClockRollback}
	}
	sigs := s.chain(tok)
	if !bytesEqual(sigs[len(sigs)-1], tok.Sig) {
		return nil, &Error{Kind: RejectBadSignature}
	}
	key := revKey{string(tok.ID), k}
	if r, ok := s.table[key]; ok {
		if r.bound <= now {
			// Stale (logically expired): same as expired non-insert.
			s.clock, s.clockSet = now, true
			s.gc(now)
			return &RevokeResult{Expired: true}, nil
		}
		s.clock, s.clockSet = now, true
		s.gc(now)
		return &RevokeResult{Revoked: true}, nil
	}
	bound := prefixBound(tok.Caveats, k)
	if bound <= now {
		// Expired: never enters the table; still an accepted operation.
		s.clock, s.clockSet = now, true
		s.gc(now)
		return &RevokeResult{Expired: true}, nil
	}
	// New insertion: limit is judged against post-GC live count, without
	// mutating anything on rejection.
	if s.activeCount(now) >= s.cfg.Rm {
		return nil, &Error{Kind: RejectLimit}
	}
	s.clock, s.clockSet = now, true
	s.gc(now)
	r := &revRecord{key: key, sig: append([]byte(nil), sigs[k]...), bound: bound}
	heap.Push(&s.heap, r)
	s.table[key] = r
	s.active++
	return &RevokeResult{Revoked: true}, nil
}
