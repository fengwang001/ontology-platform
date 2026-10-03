package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type simAuth struct {
	account    string
	identifier string
	status     string
	expires    int64
}

type simOrder struct {
	account     string
	identifiers []string
	authIDs     []int
	expires     int64
	status      string
	certSerial  int64
}

type simulator struct {
	cfg       Config
	clock     int64
	clockSet  bool
	auths     []simAuth
	orders    []simOrder
	failures  map[string][]int64
	nonces    map[uint64]bool
	pool      []uint64
	nextNonce uint64
	certs     int64
}

type simResult struct {
	kind   string
	detail string
	order  *Order
	auth   *Authorization
	status string
}

func newSimulator(cfg Config) *simulator {
	return &simulator{
		cfg:      cfg,
		failures: make(map[string][]int64),
		nonces:   make(map[uint64]bool),
	}
}

func (s *simulator) nonce() uint64 {
	s.nextNonce++
	nonce := s.nextNonce
	if int64(len(s.pool)) == s.cfg.NonceCapacity {
		delete(s.nonces, s.pool[0])
		s.pool = s.pool[1:]
	}
	s.pool = append(s.pool, nonce)
	s.nonces[nonce] = true
	return nonce
}

func (s *simulator) clockCheck(now int64, mutate bool) *simResult {
	if s.clockSet && now < s.clock {
		return &simResult{kind: KindClockRollback, detail: fmt.Sprint(s.clock)}
	}
	if mutate {
		s.clock = now
		s.clockSet = true
	}
	return nil
}

func (s *simulator) authEffective(id int, now int64) string {
	auth := s.auths[id-1]
	if (auth.status == StatusPending || auth.status == StatusValid) && now >= auth.expires {
		return StatusExpired
	}
	return auth.status
}

func (s *simulator) orderStatus(id int, now int64) string {
	order := s.orders[id-1]
	if order.status == StatusValid {
		return StatusValid
	}
	if now >= order.expires {
		return StatusInvalid
	}
	allValid := true
	for _, authID := range order.authIDs {
		switch s.authEffective(authID, now) {
		case StatusValid:
		case StatusPending:
			allValid = false
		default:
			return StatusInvalid
		}
	}
	if allValid {
		return StatusReady
	}
	return StatusPending
}

func (s *simulator) failureKey(account, identifier string) string {
	return account + "\x00" + identifier
}

func (s *simulator) failureCount(account, identifier string, now int64) int {
	key := s.failureKey(account, identifier)
	kept := s.failures[key][:0]
	count := 0
	for _, timestamp := range s.failures[key] {
		if timestamp+s.cfg.FailureWindow > now {
			count++
			kept = append(kept, timestamp)
		}
	}
	s.failures[key] = kept
	return count
}

func (s *simulator) findReuse(account, identifier string, now int64) int {
	best := 0
	var bestExpires int64
	for id := range s.auths {
		auth := s.auths[id]
		if auth.account != account || auth.identifier != identifier {
			continue
		}
		if s.authEffective(id+1, now) != StatusValid {
			continue
		}
		if best == 0 || auth.expires > bestExpires || auth.expires == bestExpires && id+1 < best {
			best = id + 1
			bestExpires = auth.expires
		}
	}
	return best
}

func (s *simulator) pendingCount(account string, now int64) int {
	count := 0
	for id := range s.auths {
		if s.auths[id].account == account && s.authEffective(id+1, now) == StatusPending {
			count++
		}
	}
	return count
}

func (s *simulator) newOrder(account string, identifiers []string, nonce uint64, now int64) *simResult {
	if account == "" || !validIdentifierList(identifiers) || !validNow(now) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, true); result != nil {
		return result
	}
	if !s.nonces[nonce] {
		return &simResult{kind: KindBadNonce}
	}

	for _, identifier := range identifiers {
		count := s.failureCount(account, identifier, now)
		if int64(count) >= s.cfg.FailureThreshold {
			return &simResult{kind: KindRateLimited, detail: fmt.Sprintf("%s:%d", identifier, count)}
		}
	}

	selected := make([]int, len(identifiers))
	q := 0
	for i, identifier := range identifiers {
		selected[i] = s.findReuse(account, identifier, now)
		if selected[i] == 0 {
			q++
		}
	}
	p := s.pendingCount(account, now)
	if p+q > int(s.cfg.PendingAuthLimit) {
		return &simResult{kind: KindQuotaExceeded, detail: fmt.Sprintf("%d:%d", p, q)}
	}

	order := simOrder{
		account:     account,
		identifiers: append([]string(nil), identifiers...),
		authIDs:     make([]int, len(identifiers)),
		expires:     now + s.cfg.OrderTTL,
		status:      StatusActive,
	}
	for i, identifier := range identifiers {
		if selected[i] != 0 {
			order.authIDs[i] = selected[i]
			continue
		}
		s.auths = append(s.auths, simAuth{
			account:    account,
			identifier: identifier,
			status:     StatusPending,
			expires:    now + s.cfg.AuthPendingTTL,
		})
		order.authIDs[i] = len(s.auths)
	}
	s.orders = append(s.orders, order)
	s.consumeNonce(nonce)
	return &simResult{order: s.copyOrder(len(s.orders))}
}

func (s *simulator) report(authID int, ok bool, now int64) *simResult {
	if authID < 1 || !validNow(now) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, true); result != nil {
		return result
	}
	if authID > len(s.auths) {
		return &simResult{kind: KindNotFound}
	}
	auth := &s.auths[authID-1]
	current := s.authEffective(authID, now)
	if current != StatusPending {
		return &simResult{kind: KindConflict, detail: current}
	}
	if ok {
		auth.status = StatusValid
		auth.expires = now + s.cfg.AuthValidTTL
	} else {
		auth.status = StatusInvalid
		key := s.failureKey(auth.account, auth.identifier)
		s.failures[key] = append(s.failures[key], now)
	}
	return &simResult{auth: s.copyAuth(authID)}
}

func (s *simulator) deactivate(account string, authID int, nonce uint64, now int64) *simResult {
	if account == "" || authID < 1 || !validNow(now) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, true); result != nil {
		return result
	}
	if !s.nonces[nonce] {
		return &simResult{kind: KindBadNonce}
	}
	if authID > len(s.auths) {
		return &simResult{kind: KindNotFound}
	}
	if s.auths[authID-1].account != account {
		return &simResult{kind: KindNotFound}
	}
	current := s.authEffective(authID, now)
	if current != StatusPending && current != StatusValid {
		return &simResult{kind: KindConflict, detail: current}
	}
	s.auths[authID-1].status = StatusDeactivated
	s.consumeNonce(nonce)
	return &simResult{auth: s.copyAuth(authID)}
}

func (s *simulator) finalize(account string, orderID int, csr []string, nonce uint64, now int64) *simResult {
	if account == "" || orderID < 1 || !validNow(now) || !validIdentifierList(csr) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, true); result != nil {
		return result
	}
	if !s.nonces[nonce] {
		return &simResult{kind: KindBadNonce}
	}
	if orderID > len(s.orders) {
		return &simResult{kind: KindNotFound}
	}
	if s.orders[orderID-1].account != account {
		return &simResult{kind: KindNotFound}
	}
	current := s.orderStatus(orderID, now)
	if current != StatusReady {
		return &simResult{kind: KindConflict, detail: current}
	}
	if !sameStringSet(s.orders[orderID-1].identifiers, csr) {
		return &simResult{kind: KindCSRMismatch}
	}
	s.certs++
	s.orders[orderID-1].status = StatusValid
	s.orders[orderID-1].certSerial = s.certs
	s.consumeNonce(nonce)
	return &simResult{order: s.copyOrder(orderID)}
}

func (s *simulator) status(orderID int, now int64) *simResult {
	if orderID < 1 || !validNow(now) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, false); result != nil {
		return result
	}
	if orderID > len(s.orders) {
		return &simResult{kind: KindNotFound}
	}
	return &simResult{order: s.copyOrder(orderID), status: s.orderStatus(orderID, now)}
}

func (s *simulator) authorization(authID int, now int64) *simResult {
	if authID < 1 || !validNow(now) {
		return &simResult{kind: KindInvalidArgument}
	}
	if result := s.clockCheck(now, false); result != nil {
		return result
	}
	if authID > len(s.auths) {
		return &simResult{kind: KindNotFound}
	}
	return &simResult{auth: s.copyAuth(authID), status: s.authEffective(authID, now)}
}

func (s *simulator) consumeNonce(nonce uint64) {
	delete(s.nonces, nonce)
	for i, value := range s.pool {
		if value == nonce {
			s.pool = append(s.pool[:i], s.pool[i+1:]...)
			break
		}
	}
}

func (s *simulator) copyAuth(id int) *Authorization {
	auth := s.auths[id-1]
	return &Authorization{
		ID:         fmt.Sprintf("z%d", id),
		Account:    []byte(auth.account),
		Identifier: auth.identifier,
		Status:     auth.status,
		Expires:    auth.expires,
	}
}

func (s *simulator) copyOrder(id int) *Order {
	order := s.orders[id-1]
	result := &Order{
		ID:               fmt.Sprintf("o%d", id),
		Account:          []byte(order.account),
		Identifiers:      append([]string(nil), order.identifiers...),
		AuthorizationIDs: make([]string, len(order.authIDs)),
		Expires:          order.expires,
		Status:           order.status,
		CertSerial:       order.certSerial,
	}
	for i, authID := range order.authIDs {
		result.AuthorizationIDs[i] = fmt.Sprintf("z%d", authID)
	}
	return result
}

type testOp struct {
	name        string
	account     string
	identifiers []string
	authID      int
	orderID     int
	nonce       uint64
	now         int64
	ok          bool
}

func randomIdentifiers(random *rand.Rand, values []string) []string {
	count := 1 + random.Intn(3)
	chosen := make([]string, 0, count)
	used := make(map[int]bool)
	for len(chosen) < count {
		index := random.Intn(len(values))
		if !used[index] {
			used[index] = true
			chosen = append(chosen, values[index])
		}
	}
	return chosen
}

func chooseNonce(random *rand.Rand, sim *simulator, machine *StateMachine) uint64 {
	if len(sim.pool) > 0 && random.Intn(5) != 0 {
		return sim.pool[random.Intn(len(sim.pool))]
	}
	return machine.nonceCounter + 1 + uint64(random.Intn(4))
}

func productionResultKind(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	var ontologyError *Error
	if errorAs(err, &ontologyError) {
		detail := ontologyError.Current
		if ontologyError.Kind == KindRateLimited {
			detail = fmt.Sprintf("%s:%d", ontologyError.Identifier, ontologyError.Count)
		}
		if ontologyError.Kind == KindQuotaExceeded {
			detail = fmt.Sprintf("%d:%d", ontologyError.P, ontologyError.Q)
		}
		return ontologyError.Kind, detail
	}
	return "unknown", err.Error()
}

func errorAs(err error, target **Error) bool {
	for err != nil {
		if ontologyError, ok := err.(*Error); ok {
			*target = ontologyError
			return true
		}
		next, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = next.Unwrap()
	}
	return false
}

func compareObjects(t *testing.T, log *strings.Builder, actualOrder *Order, expectedOrder *Order) {
	t.Helper()
	if !equalOrder(actualOrder, expectedOrder) {
		fmt.Fprintf(log, "MISMATCH order actual=%+v expected=%+v\n", actualOrder, expectedOrder)
		t.Fatalf("simulation mismatch\n%s", log.String())
	}
}

func equalOrder(a, b *Order) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func equalAuth(a *Authorization, b *Authorization) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}

func TestRandomizedNaiveSimulation(t *testing.T) {
	cfg := Config{
		AuthPendingTTL:   10,
		AuthValidTTL:     25,
		OrderTTL:         40,
		FailureWindow:    8,
		FailureThreshold: 2,
		NonceCapacity:    5,
		PendingAuthLimit: 4,
	}
	identifierPool := []string{"a.com", "b.com", "c.com", "d.com", "e.com", "*.a.com"}

	for seed := int64(1); seed <= 2000; seed++ {
		random := rand.New(rand.NewSource(seed))
		machine, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		sim := newSimulator(cfg)
		var log strings.Builder
		now := int64(0)

		for step := 0; step < 90; step++ {
			if random.Intn(8) != 0 {
				now += int64(random.Intn(7))
			}
			op := testOp{name: "nonce", now: now}

			switch random.Intn(10) {
			case 0:
				op.name = "nonce"
			case 1, 2, 3:
				op.name = "new"
				op.account = fmt.Sprintf("acc-%d", random.Intn(3))
				op.identifiers = randomIdentifiers(random, identifierPool)
				op.nonce = chooseNonce(random, sim, machine)
				if random.Intn(10) == 0 {
					op.now = now - 1
				}
			case 4, 5:
				op.name = "report"
				op.authID = 1 + random.Intn(len(sim.auths)+2)
				op.ok = random.Intn(2) == 0
			case 6:
				op.name = "deactivate"
				op.account = fmt.Sprintf("acc-%d", random.Intn(3))
				op.authID = 1 + random.Intn(len(sim.auths)+2)
				op.nonce = chooseNonce(random, sim, machine)
			case 7:
				op.name = "finalize"
				op.account = fmt.Sprintf("acc-%d", random.Intn(3))
				op.orderID = 1 + random.Intn(len(sim.orders)+2)
				if random.Intn(2) == 0 && len(sim.orders) > 0 {
					op.identifiers = append([]string(nil), sim.orders[random.Intn(len(sim.orders))].identifiers...)
				} else {
					op.identifiers = randomIdentifiers(random, identifierPool)
				}
				op.nonce = chooseNonce(random, sim, machine)
			case 8:
				op.name = "status"
				op.orderID = 1 + random.Intn(len(sim.orders)+2)
			default:
				op.name = "authorization"
				op.authID = 1 + random.Intn(len(sim.auths)+2)
			}

			fmt.Fprintf(&log, "seed=%d step=%d op=%s now=%d", seed, step, op.name, op.now)
			if op.account != "" {
				fmt.Fprintf(&log, " account=%s", op.account)
			}
			if op.identifiers != nil {
				fmt.Fprintf(&log, " identifiers=%v", op.identifiers)
			}
			if op.authID != 0 {
				fmt.Fprintf(&log, " auth=z%d", op.authID)
			}
			if op.orderID != 0 {
				fmt.Fprintf(&log, " order=o%d", op.orderID)
			}
			if op.nonce != 0 {
				fmt.Fprintf(&log, " nonce=%d", op.nonce)
			}
			if op.name == "report" {
				fmt.Fprintf(&log, " ok=%t", op.ok)
			}
			log.WriteString("\n")

			var expected *simResult
			var actualKind, actualDetail string
			var actualOrder *Order
			var actualAuth *Authorization
			var actualStatus string

			switch op.name {
			case "nonce":
				expectedNonce := sim.nonce()
				actualNonce := machine.Nonce()
				fmt.Fprintf(&log, "basis=nonce actual=%d expected=%d\n", actualNonce, expectedNonce)
				if actualNonce != expectedNonce {
					t.Fatalf("nonce mismatch\n%s", log.String())
				}
				continue
			case "new":
				var order *Order
				order, err = machine.NewOrder([]byte(op.account), op.identifiers, op.nonce, op.now)
				expected = sim.newOrder(op.account, op.identifiers, op.nonce, op.now)
				actualKind, actualDetail = productionResultKind(err)
				actualOrder = order
			case "report":
				var auth *Authorization
				auth, err = machine.Report(fmt.Sprintf("z%d", op.authID), op.ok, op.now)
				expected = sim.report(op.authID, op.ok, op.now)
				actualKind, actualDetail = productionResultKind(err)
				actualAuth = auth
			case "deactivate":
				var auth *Authorization
				auth, err = machine.Deactivate([]byte(op.account), fmt.Sprintf("z%d", op.authID), op.nonce, op.now)
				expected = sim.deactivate(op.account, op.authID, op.nonce, op.now)
				actualKind, actualDetail = productionResultKind(err)
				actualAuth = auth
			case "finalize":
				var order *Order
				order, err = machine.Finalize([]byte(op.account), fmt.Sprintf("o%d", op.orderID), op.identifiers, op.nonce, op.now)
				expected = sim.finalize(op.account, op.orderID, op.identifiers, op.nonce, op.now)
				actualKind, actualDetail = productionResultKind(err)
				actualOrder = order
			case "status":
				var result *StatusResult
				result, err = machine.Status(fmt.Sprintf("o%d", op.orderID), op.now)
				expected = sim.status(op.orderID, op.now)
				actualKind, actualDetail = productionResultKind(err)
				if result != nil {
					actualOrder = result.Order
					actualStatus = result.Status
				}
			default:
				var auth *Authorization
				auth, actualStatus, err = machine.Authorization(fmt.Sprintf("z%d", op.authID), op.now)
				expected = sim.authorization(op.authID, op.now)
				actualKind, actualDetail = productionResultKind(err)
				actualAuth = auth
			}

			fmt.Fprintf(&log, "basis=result actual=(%s,%s) expected=(%s,%s)\n", actualKind, actualDetail, expected.kind, expected.detail)
			if actualKind != expected.kind || actualDetail != expected.detail {
				t.Fatalf("simulation result mismatch\n%s", log.String())
			}
			if expected.order != nil {
				compareObjects(t, &log, actualOrder, expected.order)
			}
			if expected.auth != nil && !equalAuth(actualAuth, expected.auth) {
				fmt.Fprintf(&log, "MISMATCH auth actual=%+v expected=%+v\n", actualAuth, expected.auth)
				t.Fatalf("simulation auth mismatch\n%s", log.String())
			}
			if expected.status != "" && actualStatus != expected.status {
				fmt.Fprintf(&log, "MISMATCH effective status actual=%s expected=%s\n", actualStatus, expected.status)
				t.Fatalf("simulation status mismatch\n%s", log.String())
			}
			if seed <= 10 && step < 20 {
				t.Log(strings.TrimRight(log.String(), "\n"))
				log.Reset()
			}
		}
	}
}
