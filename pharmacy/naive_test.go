package pharmacy

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveDrug struct {
	packSize int
	split    bool
	onHand   int
	reserved int
	debt     int
}

type naiveLine struct {
	drug      string
	demand    int
	reserved  int
	dispensed int
	owed      int
}

type naiveReservation struct {
	rx       string
	line     int
	drug     string
	quantity int
	start    int
	expire   int
}

type naiveRx struct {
	patient   string
	issued    int
	allAtOnce bool
	status    string
	order     int
	lines     []naiveLine
}

type naiveModel struct {
	r           int
	now         int
	drugs       map[string]*naiveDrug
	rxs         map[string]*naiveRx
	reservedSet []naiveReservation
	order       int
}

func newNaive(r int) *naiveModel {
	return &naiveModel{r: r, drugs: map[string]*naiveDrug{}, rxs: map[string]*naiveRx{}}
}

func naiveQty(d *naiveDrug, demand, available int) int {
	if d.split {
		return min(demand, available)
	}
	needed := ((demand + d.packSize - 1) / d.packSize) * d.packSize
	if available >= needed {
		return needed
	}
	return (available / d.packSize) * d.packSize
}

func (m *naiveModel) tick(now int) {
	for m.now < now {
		m.now++
		m.processAt(m.now)
	}
}

func (m *naiveModel) processAt(at int) {
	for id, p := range m.rxs {
		if p.status == StatusActive && at == p.issued+4321 {
			p.status = StatusExpired
			for i := range p.lines {
				m.cancelDebt(id, i)
			}
		}
	}
	released := map[string]bool{}
	for i := range m.reservedSet {
		res := &m.reservedSet[i]
		if res.quantity > 0 && res.expire == at {
			p := m.rxs[res.rx]
			m.drugs[res.drug].reserved -= res.quantity
			p.lines[res.line].reserved -= res.quantity
			if p.status == StatusActive {
				ln := &p.lines[res.line]
				remaining := ln.demand - ln.dispensed
				if remaining > 0 && ln.reserved == 0 {
					increase := remaining - ln.owed
					if increase < 0 {
						increase = 0
					}
					ln.owed = remaining
					m.drugs[res.drug].debt += increase
				}
			}
			res.quantity = 0
			released[res.drug] = true
		}
	}
	m.compactReservations()
	for drugID := range released {
		m.allocate(drugID, at)
	}
}

func (m *naiveModel) compactReservations() {
	kept := m.reservedSet[:0]
	for _, res := range m.reservedSet {
		if res.quantity > 0 {
			kept = append(kept, res)
		}
	}
	m.reservedSet = kept
}

func (m *naiveModel) allocate(drugID string, at int) {
	d := m.drugs[drugID]
	type candidate struct {
		order int
		rx    string
		line  int
		owed  int
	}
	var candidates []candidate
	for id, p := range m.rxs {
		if p.status != StatusActive {
			continue
		}
		for i, ln := range p.lines {
			if ln.drug == drugID && ln.owed > 0 {
				candidates = append(candidates, candidate{p.order, id, i, ln.owed})
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].order != candidates[j].order {
			return candidates[i].order < candidates[j].order
		}
		return candidates[i].rx < candidates[j].rx
	})
	for _, cand := range candidates {
		p := m.rxs[cand.rx]
		ln := &p.lines[cand.line]
		if ln.owed <= 0 || d.onHand-d.reserved <= 0 {
			continue
		}
		qty := naiveQty(d, ln.owed, d.onHand-d.reserved)
		if qty <= 0 {
			if !d.split {
				break
			}
			continue
		}
		allocated := min(qty, ln.owed)
		ln.owed -= allocated
		d.debt -= allocated
		ln.reserved += qty
		d.reserved += qty
		m.reservedSet = append(m.reservedSet, naiveReservation{
			rx: cand.rx, line: cand.line, drug: drugID,
			quantity: qty, start: at, expire: at + m.r + 1,
		})
		if !d.split {
			break
		}
	}
}

func (m *naiveModel) cancelDebt(rxID string, lineIndex int) {
	p := m.rxs[rxID]
	ln := &p.lines[lineIndex]
	m.drugs[ln.drug].debt -= ln.owed
	ln.owed = 0
}

type testOp struct {
	name string
	args string
	err  error
}

func TestRandomComparisonWithNaive(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261006))
	for iteration := 0; iteration < 1500; iteration++ {
		r := 1 + rng.Intn(20)
		fast := NewSystem(Options{ReservationWindow: r})
		naive := newNaive(r)
		var log strings.Builder
		fmt.Fprintf(&log, "iteration=%d r=%d\n", iteration, r)
		drugIDs := []string{"A", "B", "C"}
		for _, id := range drugIDs {
			split := rng.Intn(2) == 0
			pack := 1
			if !split {
				pack = 2 + rng.Intn(5)
			}
			_ = fast.RegisterDrug(id, DrugConfig{PackSize: pack, Splittable: split})
			naive.drugs[id] = &naiveDrug{packSize: pack, split: split}
		}
		steps := 30 + rng.Intn(40)
		now := 0
		for step := 0; step < steps; step++ {
			now += rng.Intn(4)
			switch rng.Intn(6) {
			case 0:
				id := drugIDs[rng.Intn(len(drugIDs))]
				qty := 1 + rng.Intn(12)
				err := fast.ReceiveDrug(id, now, qty)
				if err == nil {
					naive.tick(now)
					naive.drugs[id].onHand += qty
					naive.allocate(id, now)
				}
				fmt.Fprintf(&log, "step=%d receive %s qty=%d now=%d err=%v\n", step, id, qty, now, err)
			case 1:
				id := fmt.Sprintf("rx-%d-%d", iteration, step)
				lineCount := 1
				ids := append([]string{}, drugIDs...)
				rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
				var input PrescriptionInput
				input.PatientID = "patient"
				input.IssuedAt = now - rng.Intn(4500)
				if rng.Intn(5) == 0 {
					input.IssuedAt = now - rng.Intn(300)
				}
				input.AllAtOnce = rng.Intn(3) == 0
				for i := 0; i < lineCount; i++ {
					input.Lines = append(input.Lines, LineInput{DrugID: ids[i], Quantity: 1 + rng.Intn(12)})
				}
				err := fast.AcceptPrescription(id, input, now)
				if err == nil {
					naive.acceptNaive(id, input, now)
				}
				fmt.Fprintf(&log, "step=%d accept %s input=%+v now=%d err=%v\n", step, id, input, now, err)
			case 2, 3:
				id := pickNaiveRx(rng, naive)
				if id == "" {
					continue
				}
				report, err := fast.Dispense(id, now)
				if err == nil {
					naive.dispenseNaive(id, now)
				}
				fmt.Fprintf(&log, "step=%d dispense %s now=%d report=%+v err=%v\n", step, id, now, report, err)
			case 4:
				id := pickNaiveRx(rng, naive)
				if id == "" {
					continue
				}
				err := fast.CancelPrescription(id, now)
				if err == nil {
					naive.cancelNaive(id, now)
				}
				fmt.Fprintf(&log, "step=%d cancel %s now=%d err=%v\n", step, id, now, err)
			case 5:
				id := drugIDs[rng.Intn(len(drugIDs))]
				naive.tick(now)
				fastReport, _ := fast.Drug(id, now)
				d := naive.drugs[id]
				if fastReport.OnHand != d.onHand || fastReport.ActiveReservation != d.reserved || fastReport.OutstandingDebt != d.debt {
					t.Fatalf("drug mismatch fast=%+v naive=%+v\n%s", fastReport, d, log.String())
				}
				fmt.Fprintf(&log, "step=%d query %s now=%d report=%+v\n", step, id, now, fastReport)
			}
		}
	}
}

func pickNaiveRx(rng *rand.Rand, m *naiveModel) string {
	ids := make([]string, 0, len(m.rxs))
	for id := range m.rxs {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ""
	}
	return ids[rng.Intn(len(ids))]
}

func (m *naiveModel) acceptNaive(id string, input PrescriptionInput, now int) {
	m.tick(now)
	m.order++
	p := &naiveRx{
		patient: input.PatientID, issued: input.IssuedAt,
		allAtOnce: input.AllAtOnce, status: StatusActive, order: m.order,
	}
	for _, item := range input.Lines {
		p.lines = append(p.lines, naiveLine{drug: item.DrugID, demand: item.Quantity, owed: item.Quantity})
		m.drugs[item.DrugID].debt += item.Quantity
	}
	m.rxs[id] = p
	for _, item := range input.Lines {
		m.allocate(item.DrugID, now)
	}
}

func (m *naiveModel) dispenseNaive(id string, now int) {
	m.tick(now)
	p := m.rxs[id]
	for i := range p.lines {
		ln := &p.lines[i]
		qty := ln.reserved
		if qty == 0 {
			continue
		}
		ln.dispensed += qty
		ln.reserved = 0
		d := m.drugs[ln.drug]
		d.onHand -= qty
		d.reserved -= qty
	}
	for i := range m.reservedSet {
		if m.reservedSet[i].rx == id {
			m.reservedSet[i].quantity = 0
		}
	}
	m.compactReservations()
	complete := true
	for _, ln := range p.lines {
		if ln.owed > 0 || ln.reserved > 0 || ln.dispensed < ln.demand {
			complete = false
		}
	}
	if complete {
		p.status = StatusCompleted
	}
}

func (m *naiveModel) cancelNaive(id string, now int) {
	m.tick(now)
	p := m.rxs[id]
	p.status = StatusCancelled
	released := map[string]bool{}
	for i := range p.lines {
		ln := &p.lines[i]
		m.cancelDebt(id, i)
		if ln.reserved > 0 {
			m.drugs[ln.drug].reserved -= ln.reserved
			released[ln.drug] = true
			ln.reserved = 0
		}
	}
	for i := range m.reservedSet {
		if m.reservedSet[i].rx == id {
			m.reservedSet[i].quantity = 0
		}
	}
	m.compactReservations()
	for drugID := range released {
		m.allocate(drugID, m.now)
	}
}
