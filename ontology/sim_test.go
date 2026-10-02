package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// Deliberately naive reference model, written straight from the spec.
type naiveAuthz struct {
	id      int64
	account string
	ident   string
	status  string
	expires int64
}

type naiveOrder struct {
	id      int64
	account string
	idents  []string
	authzs  []int64
	expires int64
	status  string
	certSN  int64
}

type naiveFailure struct {
	ident string
	t     int64
}

type naiveMachine struct {
	cfg                         Config
	clock                       int64
	pool                        map[int64]bool
	nonceSeq                    int64
	authzs                      map[int64]*naiveAuthz
	orders                      map[int64]*naiveOrder
	fails                       map[string][]naiveFailure
	authzSeq, orderSeq, certSeq int64
}

func newNaive(c Config) *naiveMachine {
	return &naiveMachine{
		cfg:    c,
		pool:   map[int64]bool{},
		authzs: map[int64]*naiveAuthz{},
		orders: map[int64]*naiveOrder{},
		fails:  map[string][]naiveFailure{},
	}
}

func (n *naiveMachine) effStatus(a *naiveAuthz, now int64) string {
	if (a.status == "pending" || a.status == "valid") && now >= a.expires {
		return "expired"
	}
	return a.status
}

func (n *naiveMachine) orderStatus(o *naiveOrder, now int64) string {
	if o.status == "valid" {
		return "valid"
	}
	if now >= o.expires {
		return "invalid"
	}
	allValid := true
	for _, id := range o.authzs {
		switch n.effStatus(n.authzs[id], now) {
		case "invalid", "deactivated", "expired":
			return "invalid"
		case "valid":
		default:
			allValid = false
		}
	}
	if allValid {
		return "ready"
	}
	return "pending"
}

func (n *naiveMachine) nonce() int64 {
	n.nonceSeq++
	v := n.nonceSeq
	live := []int64{}
	for x, on := range n.pool {
		if on {
			live = append(live, x)
		}
	}
	if int64(len(live)) >= n.cfg.C {
		sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
		n.pool[live[0]] = false
	}
	n.pool[v] = true
	return v
}

func naiveValidIdent(s string) bool {
	if s == "" {
		return false
	}
	body := s
	if strings.HasPrefix(body, "*.") {
		body = body[2:]
	}
	if body == "" {
		return false
	}
	for _, c := range body {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

func naiveParse(ref, prefix string) (int64, bool) {
	if !strings.HasPrefix(ref, prefix) {
		return 0, false
	}
	s := ref[len(prefix):]
	if s == "" {
		return 0, false
	}
	var id int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		id = id*10 + int64(c-'0')
	}
	return id, true
}

func joinZ(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("z%d", id)
	}
	return strings.Join(parts, ",")
}

// simOp is one operation in a generated sequence.
type simOp struct {
	kind    string // nonce|neworder|report|deactivate|finalize|status|authz
	account string
	idents  []string
	nonce   int64
	now     int64
	ref     string
	ok      bool
}

func (o simOp) String() string {
	switch o.kind {
	case "nonce":
		return "Nonce()"
	case "neworder":
		return fmt.Sprintf("NewOrder(%q,%v,%d,%d)", o.account, o.idents, o.nonce, o.now)
	case "report":
		return fmt.Sprintf("Report(%s,%v,%d)", o.ref, o.ok, o.now)
	case "deactivate":
		return fmt.Sprintf("Deactivate(%q,%s,%d,%d)", o.account, o.ref, o.nonce, o.now)
	case "finalize":
		return fmt.Sprintf("Finalize(%q,%s,%v,%d,%d)", o.account, o.ref, o.idents, o.nonce, o.now)
	case "status":
		return fmt.Sprintf("Status(%s,%d)", o.ref, o.now)
	case "authz":
		return fmt.Sprintf("GetAuthz(%s,%d)", o.ref, o.now)
	}
	return o.kind
}

// simResult is the comparable outcome of an operation.
type simResult struct {
	errKind  string
	errIdent string
	errCount int
	errP     int
	errQ     int
	errStat  string
	id       string
	status   string
	expires  int64
	authzs   string
	certSN   int64
}

func (n *naiveMachine) checkClock(now int64) string {
	if now < 0 || now > 1e15 {
		return KindParam
	}
	if now < n.clock {
		return KindClock
	}
	return ""
}

func (n *naiveMachine) run(op simOp) simResult {
	switch op.kind {
	case "nonce":
		return simResult{certSN: n.nonce()}
	case "neworder":
		return n.newOrder(op)
	case "report":
		return n.report(op)
	case "deactivate":
		return n.deactivate(op)
	case "finalize":
		return n.finalize(op)
	case "status":
		return n.status(op)
	case "authz":
		return n.getAuthz(op)
	}
	return simResult{errKind: "unknown"}
}

func (n *naiveMachine) newOrder(op simOp) simResult {
	if op.account == "" {
		return simResult{errKind: KindParam}
	}
	if len(op.idents) < 1 || len(op.idents) > 10 {
		return simResult{errKind: KindParam}
	}
	seen := map[string]bool{}
	for _, id := range op.idents {
		if !naiveValidIdent(id) || seen[id] {
			return simResult{errKind: KindParam}
		}
		seen[id] = true
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	if !n.pool[op.nonce] {
		return simResult{errKind: KindNonce}
	}
	for _, id := range op.idents {
		kept := n.fails[op.account][:0]
		cnt := 0
		for _, f := range n.fails[op.account] {
			if f.t+n.cfg.H > op.now {
				kept = append(kept, f)
				if f.ident == id {
					cnt++
				}
			}
		}
		n.fails[op.account] = kept
		if cnt >= n.cfg.F {
			return simResult{errKind: KindRateLimited, errIdent: id, errCount: cnt}
		}
	}
	p := 0
	for _, a := range n.authzs {
		if a.account == op.account && n.effStatus(a, op.now) == "pending" {
			p++
		}
	}
	chosen := make([]int64, len(op.idents))
	q := 0
	for i, id := range op.idents {
		var best *naiveAuthz
		for _, a := range n.authzs {
			if a.account == op.account && a.ident == id && n.effStatus(a, op.now) == "valid" {
				if best == nil || a.expires > best.expires ||
					a.expires == best.expires && a.id < best.id {
					best = a
				}
			}
		}
		if best == nil {
			q++
		} else {
			chosen[i] = best.id
		}
	}
	if p+q > n.cfg.Pm {
		return simResult{errKind: KindQuota, errP: p, errQ: q}
	}
	n.pool[op.nonce] = false
	n.clock = op.now
	for i, id := range op.idents {
		if chosen[i] != 0 {
			continue
		}
		n.authzSeq++
		a := &naiveAuthz{id: n.authzSeq, account: op.account, ident: id,
			status: "pending", expires: op.now + n.cfg.Ta}
		n.authzs[a.id] = a
		chosen[i] = a.id
	}
	n.orderSeq++
	o := &naiveOrder{id: n.orderSeq, account: op.account,
		idents: append([]string(nil), op.idents...), authzs: chosen,
		expires: op.now + n.cfg.To, status: "active"}
	n.orders[o.id] = o
	return simResult{id: fmt.Sprintf("o%d", o.id),
		status:  n.orderStatus(o, op.now),
		expires: o.expires,
		authzs:  joinZ(chosen)}
}

func (n *naiveMachine) report(op simOp) simResult {
	id, ok := naiveParse(op.ref, "z")
	if !ok {
		return simResult{errKind: KindParam}
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	a := n.authzs[id]
	if a == nil {
		return simResult{errKind: KindNotFound}
	}
	if st := n.effStatus(a, op.now); st != "pending" {
		return simResult{errKind: KindState, errStat: st}
	}
	n.clock = op.now
	if op.ok {
		a.status = "valid"
		a.expires = op.now + n.cfg.Tv
	} else {
		a.status = "invalid"
		n.fails[a.account] = append(n.fails[a.account],
			naiveFailure{ident: a.ident, t: op.now})
	}
	return simResult{}
}

func (n *naiveMachine) deactivate(op simOp) simResult {
	if op.account == "" {
		return simResult{errKind: KindParam}
	}
	id, ok := naiveParse(op.ref, "z")
	if !ok {
		return simResult{errKind: KindParam}
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	if !n.pool[op.nonce] {
		return simResult{errKind: KindNonce}
	}
	a := n.authzs[id]
	if a == nil || a.account != op.account {
		return simResult{errKind: KindNotFound}
	}
	if st := n.effStatus(a, op.now); st != "pending" && st != "valid" {
		return simResult{errKind: KindState, errStat: st}
	}
	n.pool[op.nonce] = false
	n.clock = op.now
	a.status = "deactivated"
	return simResult{}
}

func (n *naiveMachine) finalize(op simOp) simResult {
	if op.account == "" {
		return simResult{errKind: KindParam}
	}
	id, ok := naiveParse(op.ref, "o")
	if !ok {
		return simResult{errKind: KindParam}
	}
	seen := map[string]bool{}
	for _, s := range op.idents {
		if !naiveValidIdent(s) || seen[s] {
			return simResult{errKind: KindParam}
		}
		seen[s] = true
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	if !n.pool[op.nonce] {
		return simResult{errKind: KindNonce}
	}
	o := n.orders[id]
	if o == nil || o.account != op.account {
		return simResult{errKind: KindNotFound}
	}
	if st := n.orderStatus(o, op.now); st != "ready" {
		return simResult{errKind: KindState, errStat: st}
	}
	set := map[string]bool{}
	for _, s := range op.idents {
		set[s] = true
	}
	match := len(set) == len(o.idents)
	if match {
		for _, s := range o.idents {
			if !set[s] {
				match = false
			}
		}
	}
	if !match {
		return simResult{errKind: KindCSR}
	}
	n.pool[op.nonce] = false
	n.clock = op.now
	n.certSeq++
	o.status = "valid"
	o.certSN = n.certSeq
	return simResult{certSN: n.certSeq}
}

func (n *naiveMachine) status(op simOp) simResult {
	id, ok := naiveParse(op.ref, "o")
	if !ok {
		return simResult{errKind: KindParam}
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	o := n.orders[id]
	if o == nil {
		return simResult{errKind: KindNotFound}
	}
	return simResult{status: n.orderStatus(o, op.now)}
}

func (n *naiveMachine) getAuthz(op simOp) simResult {
	id, ok := naiveParse(op.ref, "z")
	if !ok {
		return simResult{errKind: KindParam}
	}
	if k := n.checkClock(op.now); k != "" {
		return simResult{errKind: k}
	}
	a := n.authzs[id]
	if a == nil {
		return simResult{errKind: KindNotFound}
	}
	return simResult{id: fmt.Sprintf("z%d", a.id),
		status: n.effStatus(a, op.now), expires: a.expires}
}

func errResult(err error) simResult {
	e := err.(*Error)
	return simResult{
		errKind:  e.Kind,
		errIdent: e.Ident,
		errCount: e.Count,
		errP:     e.Pending,
		errQ:     e.Need,
		errStat:  e.Status,
	}
}

func realRun(m *Machine, op simOp) simResult {
	switch op.kind {
	case "nonce":
		return simResult{certSN: m.Nonce()}
	case "neworder":
		o, err := m.NewOrder([]byte(op.account), op.idents, op.nonce, op.now)
		if err != nil {
			return errResult(err)
		}
		return simResult{id: o.ID, status: o.Status, expires: o.Expires,
			authzs: strings.Join(o.AuthzIDs, ",")}
	case "report":
		if err := m.Report(op.ref, op.ok, op.now); err != nil {
			return errResult(err)
		}
		return simResult{}
	case "deactivate":
		if err := m.Deactivate([]byte(op.account), op.ref, op.nonce, op.now); err != nil {
			return errResult(err)
		}
		return simResult{}
	case "finalize":
		sn, err := m.Finalize([]byte(op.account), op.ref, op.idents, op.nonce, op.now)
		if err != nil {
			return errResult(err)
		}
		return simResult{certSN: sn}
	case "status":
		s, err := m.Status(op.ref, op.now)
		if err != nil {
			return errResult(err)
		}
		return simResult{status: s}
	case "authz":
		a, err := m.GetAuthz(op.ref, op.now)
		if err != nil {
			return errResult(err)
		}
		return simResult{id: a.ID, status: a.Status, expires: a.Expires}
	}
	return simResult{errKind: "unknown"}
}

func resultString(r simResult) string {
	return fmt.Sprintf("err=%s ident=%s cnt=%d p=%d q=%d stat=%s id=%s status=%s exp=%d authzs=%s n=%d",
		r.errKind, r.errIdent, r.errCount, r.errP, r.errQ, r.errStat,
		r.id, r.status, r.expires, r.authzs, r.certSN)
}

func resultsEqual(a, b simResult) bool {
	return a == b
}

func pickAcc(rng *rand.Rand) string {
	return []string{"acc1", "acc2", "acc3"}[rng.Intn(3)]
}

var idPool = []string{"a.com", "b.com", "c.com", "*.a.com", "x.io", "d-e.f"}

// TestDifferentialWitnessLog always replays one fixed sequence and prints
// every input, projected output and deciding fields (kind/status/p/q/...).
func TestDifferentialWitnessLog(t *testing.T) {
	cfg := Config{Ta: 100, Tv: 1000, To: 500, H: 60, F: 2, C: 3, Pm: 3}
	m, _ := New(cfg)
	nm := newNaive(cfg)

	ops := []simOp{
		{kind: "nonce"}, {kind: "nonce"}, {kind: "nonce"}, {kind: "nonce"},
		{kind: "neworder", account: "acc", idents: []string{"a.com", "b.com"}, nonce: 1, now: 0},
		{kind: "neworder", account: "acc", idents: []string{"a.com", "b.com"}, nonce: 2, now: 0},
		{kind: "report", ref: "z1", ok: true, now: 10},
		{kind: "status", ref: "o1", now: 10},
		{kind: "report", ref: "z2", ok: true, now: 20},
		{kind: "status", ref: "o1", now: 20},
		{kind: "finalize", account: "acc", ref: "o1",
			idents: []string{"b.com", "a.com"}, nonce: 3, now: 30},
		{kind: "finalize", account: "acc", ref: "o1",
			idents: []string{"b.com", "a.com"}, nonce: 4, now: 30},
		{kind: "neworder", account: "acc",
			idents: []string{"a.com", "c.com"}, nonce: 4, now: 450},
		{kind: "status", ref: "o2", now: 549},
		{kind: "status", ref: "o2", now: 550},
	}
	for _, op := range ops {
		got := realRun(m, op)
		want := nm.run(op)
		t.Logf("%-65s | got: %s | want: %s", op.String(),
			resultString(got), resultString(want))
		if !resultsEqual(got, want) {
			t.Fatalf("mismatch: %s vs %s", resultString(got), resultString(want))
		}
	}
}

// TestDifferentialRandom2000 replays 2000 random sequences against the
// naive specification model. On divergence the full input/output log with
// the deciding fields is printed.
func TestDifferentialRandom2000(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const sequences = 2000
	const opsPerSeq = 60
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{
			Ta: int64(1 + rng.Intn(300)),
			Tv: int64(1 + rng.Intn(2000)),
			To: int64(1 + rng.Intn(1000)),
			H:  int64(1 + rng.Intn(100)),
			F:  1 + rng.Intn(4),
			C:  int64(1 + rng.Intn(6)),
			Pm: 1 + rng.Intn(6),
		}
		m, err := New(cfg)
		if err != nil {
			t.Fatalf("seed %d cfg: %v", seed, err)
		}
		nm := newNaive(cfg)

		now := int64(0)
		var issued []int64
		var authzIDs, orderIDs []int64
		var log []string

		pickNonce := func() int64 {
			if len(issued) == 0 || rng.Intn(4) == 0 {
				return int64(1 + rng.Intn(12))
			}
			return issued[rng.Intn(len(issued))]
		}
		pickRef := func(prefix string, ids []int64) string {
			if len(ids) == 0 || rng.Intn(5) == 0 {
				return fmt.Sprintf("%s%d", prefix, 1+rng.Intn(10))
			}
			return fmt.Sprintf("%s%d", prefix, ids[rng.Intn(len(ids))])
		}

		for step := 0; step < opsPerSeq; step++ {
			switch rng.Intn(8) {
			case 0:
			case 1:
				if now > 2 && rng.Intn(3) == 0 {
					now -= 1 + rng.Int63n(3)
				}
			default:
				now += rng.Int63n(12)
			}
			var op simOp
			switch rng.Intn(20) {
			case 0, 1, 2:
				op = simOp{kind: "nonce"}
			case 3, 4, 5, 6:
				k := 1 + rng.Intn(4)
				perm := rng.Perm(len(idPool))[:k]
				ids := make([]string, k)
				for j, p := range perm {
					ids[j] = idPool[p]
				}
				op = simOp{kind: "neworder", account: pickAcc(rng),
					idents: ids, nonce: pickNonce(), now: now}
			case 7, 8, 9:
				op = simOp{kind: "report", ref: pickRef("z", authzIDs),
					ok: rng.Intn(2) == 0, now: now}
			case 10, 11:
				op = simOp{kind: "deactivate", account: pickAcc(rng),
					ref: pickRef("z", authzIDs), nonce: pickNonce(), now: now}
			case 12, 13, 14:
				k := 1 + rng.Intn(4)
				csr := make([]string, k)
				for j := range csr {
					csr[j] = idPool[rng.Intn(len(idPool))]
				}
				op = simOp{kind: "finalize", account: pickAcc(rng),
					ref: pickRef("o", orderIDs), idents: csr,
					nonce: pickNonce(), now: now}
			case 15, 16:
				op = simOp{kind: "status", ref: pickRef("o", orderIDs), now: now}
			default:
				op = simOp{kind: "authz", ref: pickRef("z", authzIDs), now: now}
			}

			got := realRun(m, op)
			want := nm.run(op)

			if op.kind == "nonce" {
				issued = append(issued, got.certSN)
			}
			if op.kind == "neworder" && got.errKind == "" {
				var oid int64
				fmt.Sscanf(got.id, "o%d", &oid)
				orderIDs = append(orderIDs, oid)
				for _, z := range strings.Split(got.authzs, ",") {
					var zid int64
					fmt.Sscanf(z, "z%d", &zid)
					authzIDs = append(authzIDs, zid)
				}
			}

			log = append(log, fmt.Sprintf("%-70s => %s", op.String(), resultString(got)))
			if !resultsEqual(got, want) {
				t.Fatalf("seed %d step %d cfg=%+v\nop: %s\ngot:  %s\nwant: %s\nlog:\n%s",
					seed, step, cfg, op, resultString(got), resultString(want),
					strings.Join(log, "\n"))
			}
		}
	}
}
