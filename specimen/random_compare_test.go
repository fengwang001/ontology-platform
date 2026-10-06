package specimen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type randomSource struct {
	state uint64
}

func (r *randomSource) next() uint64 {
	r.state ^= r.state << 13
	r.state ^= r.state >> 7
	r.state ^= r.state << 17
	if r.state == 0 {
		r.state = 1
	}
	return r.state
}

func (r *randomSource) bounded(bound int) int {
	if bound <= 0 {
		return 0
	}
	return int(r.next() % uint64(bound))
}

type randomPlan struct {
	random  *randomSource
	now     int64
	items   map[string]string
	waiting map[string]struct{}
	tubes   map[string]struct{}
	active  map[string]struct{}
}

func newRandomPlan(seed uint64) *randomPlan {
	return &randomPlan{
		random:  &randomSource{state: seed | 1},
		now:     1,
		items:   map[string]string{},
		waiting: map[string]struct{}{},
		tubes:   map[string]struct{}{},
		active:  map[string]struct{}{},
	}
}

func (p *randomPlan) chooseID(values map[string]struct{}) string {
	if len(values) == 0 {
		return ""
	}
	index := p.random.bounded(len(values))
	for id := range values {
		if index == 0 {
			return id
		}
		index--
	}
	return ""
}

func (p *randomPlan) build(sequenceLength int) []modelOp {
	projectNames := []string{"P1", "P2", "P3", "P4", "P5"}
	ops := make([]modelOp, 0, sequenceLength+len(projectNames))
	for _, project := range projectNames {
		p.now++
		method := TransportAmbient
		if p.random.bounded(2) == 0 {
			method = TransportCold
		}
		ops = append(ops, modelOp{
			name:        "catalog",
			now:         p.now,
			project:     project,
			tubeType:    "tube",
			method:      method,
			hemolysis:   p.random.bounded(5),
			collectedAt: int64(2 + p.random.bounded(20)),
		})
	}
	for len(ops) < sequenceLength+len(projectNames) {
		p.now += int64(1 + p.random.bounded(3))
		kind := p.random.bounded(10)
		switch {
		case kind < 3:
			project := projectNames[p.random.bounded(len(projectNames))]
			projects := []string{project}
			if p.random.bounded(3) == 0 {
				other := projectNames[p.random.bounded(len(projectNames))]
				if other != project {
					projects = append(projects, other)
				}
			}
			ops = append(ops, modelOp{name: "apply", now: p.now, patient: "H1", projects: projects, priority: "normal"})
		case kind < 6 && len(p.waiting) > 0:
			first := p.chooseID(p.waiting)
			ids := []string{first}
			delete(p.waiting, first)
			if p.random.bounded(3) == 0 {
				if second := p.chooseID(p.waiting); second != "" {
					ids = append(ids, second)
					delete(p.waiting, second)
				}
			}
			ops = append(ops, modelOp{
				name:        "collect",
				now:         p.now,
				patient:     "H1",
				tubeType:    "tube",
				itemIDs:     ids,
				collectedAt: p.now - int64(p.random.bounded(2)),
			})
		case kind < 7 && len(p.tubes) > 0:
			ops = append(ops, modelOp{name: "dispatch", now: p.now, tubeType: p.chooseID(p.tubes), method: []string{TransportCold, TransportAmbient}[p.random.bounded(2)]})
		case kind < 8 && len(p.tubes) > 0:
			p.now += int64(p.random.bounded(25))
			ops = append(ops, modelOp{name: "sign", now: p.now, tubeType: p.chooseID(p.tubes), hemolysis: p.random.bounded(5)})
		case kind < 9 && len(p.active) > 0:
			ops = append(ops, modelOp{name: "cancel", now: p.now, project: p.chooseID(p.active)})
		default:
			ops = append(ops, modelOp{name: "query", now: p.now, patient: "H1"})
		}
	}
	return ops
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	if specimenError, ok := err.(*Error); ok {
		return specimenError.Code
	}
	return err.Error()
}

func normalizedValue(value any) string {
	switch typed := value.(type) {
	case []ItemView:
		return mustJSON(sortedViews(append([]ItemView{}, typed...)))
	case *PatientView:
		items := append([]ItemView{}, typed.Items...)
		return mustJSON(sortedViews(items))
	case []SignDecision:
		copied := append([]SignDecision{}, typed...)
		sortDecisions(copied)
		return mustJSON(copied)
	default:
		return mustJSON(value)
	}
}

func sortDecisions(values []SignDecision) {
	for index := 0; index < len(values); index++ {
		for next := index + 1; next < len(values); next++ {
			if values[next].ItemID < values[index].ItemID {
				values[index], values[next] = values[next], values[index]
			}
		}
	}
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func runActual(sys *System, op modelOp) (any, error) {
	switch op.name {
	case "catalog":
		return nil, sys.UpsertCatalogItem(op.now, op.project, CatalogRequirement{TubeType: op.tubeType, MaxDeliverySeconds: op.collectedAt, ColdRequired: op.method == TransportCold, HemolysisTolerance: op.hemolysis})
	case "apply":
		return sys.Apply(op.now, op.patient, op.projects, op.priority)
	case "collect":
		return sys.Collect(op.now, op.patient, op.tubeType, op.itemIDs, op.collectedAt)
	case "dispatch":
		return nil, sys.Dispatch(op.now, op.tubeType, op.method)
	case "sign":
		return sys.Sign(op.now, op.tubeType, op.hemolysis)
	case "cancel":
		return nil, sys.Cancel(op.now, op.project)
	case "query":
		return sys.Query(op.now, op.patient)
	default:
		return nil, fmt.Errorf("unknown operation")
	}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "random-sequences.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	for seed := int64(1); seed <= 1500; seed++ {
		sys, model := NewSystem(), newNaiveModel()
		plan := newRandomPlan(uint64(seed))
		ops := plan.build(35)
		for step, op := range ops {
			actualValue, actualErr := runActual(sys, op)
			modelValue, modelErr := model.execute(op)
			actualCode, modelCode := errorCode(actualErr), errorCode(modelErr)
			actualJSON, modelJSON := normalizedValue(actualValue), normalizedValue(modelValue)
			if actualErr != nil {
				actualJSON = "null"
			}
			if modelErr != nil {
				modelJSON = "null"
			}
			basis := ""
			if decisions, ok := actualValue.([]SignDecision); ok && actualErr == nil {
				basis = "reasons=" + mustJSON(decisions)
			}
			fmt.Fprintf(logFile, "seed=%d step=%d input=%s actual=(%s,%s) naive=(%s,%s) %s\n", seed, step, mustJSON(op), actualCode, actualJSON, modelCode, modelJSON, basis)
			if actualCode != modelCode || actualJSON != modelJSON {
				t.Fatalf("seed=%d step=%d op=%s actual=(%s,%s) naive=(%s,%s) log=%s", seed, step, mustJSON(op), actualCode, actualJSON, modelCode, modelJSON, logPath)
			}
			plan.syncFromModel(model)
		}
	}
}

func (p *randomPlan) syncFromModel(m *naiveModel) {
	p.items = map[string]string{}
	p.waiting = map[string]struct{}{}
	p.active = map[string]struct{}{}
	for id, it := range m.items {
		if terminalStatus(it.status) {
			continue
		}
		p.items[id] = it.projectID
		p.active[id] = struct{}{}
		if it.status == StatusWaiting {
			p.waiting[id] = struct{}{}
		}
	}
	p.tubes = map[string]struct{}{}
	for id, tube := range m.tubes {
		if tube.active {
			p.tubes[id] = struct{}{}
		}
	}
}
