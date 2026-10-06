package greenreg

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

func renderCanonical(s *CanonicalState) string {
	var b strings.Builder
	for _, f := range s.Facilities {
		fmt.Fprintf(&b, "F %s holder=%s start=%d end=%d hasEnd=%v balance=%d\n",
			f.ID, f.Holder, f.Start, f.End, f.HasEnd, f.Balance)
		for _, m := range f.Meters {
			fmt.Fprintf(&b, "  M period=%d qty=%d remain=%d live=%d\n", m.Period, m.Qty, m.Remain, m.Live)
		}
	}
	for _, c := range s.Certs {
		fmt.Fprintf(&b, "C %d fac=%s gen=%d holder=%s status=%d retUser=%s retPeriod=%d retSeq=%d\n",
			c.Serial, c.Facility, c.Generation, c.Holder, c.Status, c.RetireUser, c.UsagePeriod, c.RetireSeq)
	}
	for _, u := range s.Usage {
		fmt.Fprintf(&b, "U %s period=%d qty=%d units=%d active=%d\n", u.User, u.Period, u.Qty, u.Units, u.Active)
	}
	for _, e := range s.Events {
		fmt.Fprintf(&b, "E kind=%d cert=%d user=%s up=%d\n", e.Kind, e.Cert, e.User, e.UsagePeriod)
	}
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Config parameterizes a Registry.
type Config struct {
	UnitQty       int64 // energy per certificate (default 1)
	MaxAgePeriods int64 // generation period must be >= usage period - this
}

// Registry is the concurrent-safe facade of the certificate registry. Every
// public method holds one mutex for its whole duration, so concurrent callers
// observe (and are equivalent to) some serial interleaving.
type Registry struct {
	mu     sync.Mutex
	cfg    Config
	facils map[string]*facility
	pool   *certPool
	book   *retirementBook
	events []Event
	log    io.Writer
}

// New creates an empty registry.
func New(cfg Config) *Registry {
	if cfg.UnitQty <= 0 {
		cfg.UnitQty = 1
	}
	if cfg.MaxAgePeriods < 0 {
		cfg.MaxAgePeriods = 0
	}
	return &Registry{
		cfg:    cfg,
		facils: make(map[string]*facility),
		pool:   newCertPool(),
		book:   newRetirementBook(cfg.UnitQty),
	}
}

// SetLogger enables per-operation logging of input, decision and output.
func (r *Registry) SetLogger(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = w
}

func (r *Registry) record(e Event) { r.events = append(r.events, e) }

func (r *Registry) logf(format string, args ...any) {
	if r.log != nil {
		fmt.Fprintf(r.log, format+"\n", args...)
	}
}

func badPeriod(p int64) bool { return p < 0 }

// RegisterFacility records a facility, its holder and eligibility start.
func (r *Registry) RegisterFacility(id, holder string, startPeriod int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("RegisterFacility id=%q holder=%q start=%d -> %v", id, holder, startPeriod, err)
	}()
	if id == "" || holder == "" || badPeriod(startPeriod) {
		return newErr(ErrInvalid, "参数非法")
	}
	if _, ok := r.facils[id]; ok {
		return newErr(ErrInvalid, "设施已存在")
	}
	r.facils[id] = newFacility(holder, startPeriod, r.cfg.UnitQty)
	return nil
}

// SetTermination registers, postpones or clears the eligibility end period.
func (r *Registry) SetTermination(id string, endPeriod int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("SetTermination id=%q end=%d -> %v", id, endPeriod, err)
	}()
	if id == "" {
		return newErr(ErrInvalid, "参数非法")
	}
	f, ok := r.facils[id]
	if !ok {
		return newErr(ErrInvalid, "设施不存在")
	}
	if endPeriod != 0 && endPeriod <= f.start {
		return newErr(ErrInvalid, "终止期须晚于生效期")
	}
	maxP, have := f.maxIssuedGeneration()
	if e := f.setEnd(endPeriod, maxP, have); e != nil {
		return e
	}
	return nil
}

// RegisterGeneration registers or corrects the metered quantity of a period.
func (r *Registry) RegisterGeneration(id string, period, qty int64) (issued []int64, revoked []int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("RegisterGeneration id=%q period=%d qty=%d -> issued=%v revoked=%v err=%v", id, period, qty, issued, revoked, err)
	}()
	if id == "" || badPeriod(period) || qty < 0 {
		return nil, nil, newErr(ErrInvalid, "参数非法")
	}
	f, ok := r.facils[id]
	if !ok {
		return nil, nil, newErr(ErrInvalid, "设施不存在")
	}
	m, exists := f.meters[period]
	if !exists && qty == 0 {
		return nil, nil, newErr(ErrInvalid, "新登记电量须为正")
	}
	if !f.eligible(period) {
		return nil, nil, newErr(ErrEligibility, "发电期不在资格区间内")
	}

	input := f.balance
	live := int64(0)
	if exists {
		input = m.in
		live = m.live
	}
	pl := planIssuance(qty, input, live, r.cfg.UnitQty)

	issued = make([]int64, 0, pl.issue)
	for i := int64(0); i < pl.issue; i++ {
		c := r.pool.issue(id, period, f.holder)
		issued = append(issued, c.Serial)
		r.record(Event{Kind: EvIssued, Cert: c.Serial, Facility: id, Generation: period, Holder: f.holder})
	}
	if pl.revoke > 0 {
		revoked = r.revokeLive(id, period, pl.revoke)
	}
	f.applyIssuance(period, qty, input, live+pl.issue-pl.revoke, pl)
	return issued, revoked, nil
}

// revokeLive removes n live certificates of a period: held first (serial
// descending), then retired (retirement moment descending).
func (r *Registry) revokeLive(id string, period, n int64) []int64 {
	revoked := make([]int64, 0, n)
	held := r.pool.heldForPeriod(id, period)
	for _, c := range held {
		if int64(len(revoked)) >= n {
			break
		}
		c.Status = StatusRevoked
		revoked = append(revoked, c.Serial)
		r.record(Event{Kind: EvRevoked, Cert: c.Serial, Facility: id, Generation: period})
	}
	if int64(len(revoked)) < n {
		retired := r.book.retiredForRevocation(r.pool, id, period)
		for _, c := range retired {
			if int64(len(revoked)) >= n {
				break
			}
			user, up := c.RetireUser, c.UsagePeriod
			r.book.voidRevoked(c)
			revoked = append(revoked, c.Serial)
			r.record(Event{Kind: EvRevoked, Cert: c.Serial, Facility: id, Generation: period})
			r.record(Event{Kind: EvDeclarationVoided, Cert: c.Serial, User: user, UsagePeriod: up})
		}
	}
	return revoked
}

// Transfer moves a batch of certificates wholesale from one holder to another.
func (r *Registry) Transfer(from, to string, serials []int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("Transfer from=%q to=%q certs=%v -> %v", from, to, serials, err)
	}()
	if from == "" || to == "" || from == to || len(serials) == 0 {
		return newErr(ErrInvalid, "参数非法/自转")
	}
	sorted, e := r.validateHeldBatch(serials, from, -1, 0)
	if e != nil {
		return e
	}
	for _, c := range sorted {
		c.Holder = to
		r.record(Event{Kind: EvTransferred, Cert: c.Serial, From: from, To: to})
	}
	return nil
}

// validateHeldBatch performs batch parameter checks first, then per-cert
// checks in ascending serial order with the fixed rejection precedence.
// usagePeriod >= 0 additionally enforces the generation/usage period window.
func (r *Registry) validateHeldBatch(serials []int64, holder string, usagePeriod, maxAge int64) ([]*Certificate, *RegistryError) {
	seen := make(map[int64]bool, len(serials))
	sorted := append([]int64(nil), serials...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	for _, s := range sorted {
		if s <= 0 {
			return nil, newErr(ErrInvalid, "序号不存在或批内重复")
		}
		if seen[s] {
			return nil, newErr(ErrInvalid, "批内序号重复")
		}
		seen[s] = true
	}
	out := make([]*Certificate, 0, len(sorted))
	for _, s := range sorted {
		c := r.pool.get(s)
		if c == nil {
			return nil, failErr(ErrInvalid, "序号不存在", s)
		}
		if c.Status != StatusHeld {
			return nil, failErr(ErrStateNotAllowed, "证书非持有状态", s)
		}
		if c.Holder != holder {
			return nil, failErr(ErrNotHolder, "非持有人", s)
		}
		if usagePeriod >= 0 && (c.Generation > usagePeriod || c.Generation < usagePeriod-maxAge) {
			return nil, failErr(ErrPeriodMismatch, "发电期超出允许期限", s)
		}
		out = append(out, c)
	}
	return out, nil
}

// RegisterUsage registers or corrects a consumer's usage for one period.
func (r *Registry) RegisterUsage(user string, period, qty int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("RegisterUsage user=%q period=%d qty=%d -> %v", user, period, qty, err)
	}()
	if user == "" || badPeriod(period) || qty < 0 {
		return newErr(ErrInvalid, "参数非法")
	}
	if e := r.book.registerUsage(user, period, qty); e != nil {
		return e
	}
	return nil
}

// Retire retires a batch of the caller's certificates against one usage period.
func (r *Registry) Retire(user string, usagePeriod int64, serials []int64) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	defer func() {
		r.logf("Retire user=%q usagePeriod=%d certs=%v -> %v", user, usagePeriod, serials, err)
	}()
	if user == "" || len(serials) == 0 || badPeriod(usagePeriod) {
		return newErr(ErrInvalid, "参数非法")
	}
	u := r.book.usage[user][usagePeriod]
	if u == nil {
		return newErr(ErrInvalid, "用电期未登记用电量")
	}
	certs, e := r.validateHeldBatch(serials, user, usagePeriod, r.cfg.MaxAgePeriods)
	if e != nil {
		return e
	}
	if u.units+int64(len(certs))*r.cfg.UnitQty > u.qty {
		return failErr(ErrOverUsage, "注销总量超过用电量", certs[0].Serial)
	}
	r.book.applyRetire(user, usagePeriod, certs)
	for _, c := range certs {
		r.record(Event{Kind: EvRetired, Cert: c.Serial, User: user, UsagePeriod: usagePeriod})
	}
	return nil
}

// Events returns a copy of the recorded lifecycle events.
func (r *Registry) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Snapshot returns a deterministic textual dump of the whole registry, used to
// prove replay equivalence against the naive model.
func (r *Registry) Snapshot() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := &CanonicalState{Certs: r.pool.certs, Events: r.events}
	for _, id := range sortedKeys(r.facils) {
		f := r.facils[id]
		cf := CanonicalFacility{ID: id, Holder: f.holder, Start: f.start, End: f.end, HasEnd: f.hasEnd, Balance: f.balance}
		ps := make([]int64, 0, len(f.meters))
		for p := range f.meters {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		for _, p := range ps {
			m := f.meters[p]
			cf.Meters = append(cf.Meters, CanonicalMeter{Period: p, Qty: m.qty, Remain: m.remain, Live: m.live})
		}
		st.Facilities = append(st.Facilities, cf)
	}
	for _, u := range sortedKeys(r.book.usage) {
		ps := make([]int64, 0, len(r.book.usage[u]))
		for p := range r.book.usage[u] {
			ps = append(ps, p)
		}
		sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
		for _, p := range ps {
			up := r.book.usage[u][p]
			st.Usage = append(st.Usage, CanonicalUsage{User: u, Period: p, Qty: up.qty, Units: up.units, Active: up.active})
		}
	}
	return renderCanonical(st)
}
