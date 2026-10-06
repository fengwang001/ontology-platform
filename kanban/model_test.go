package kanban

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

type modelOperation struct {
	kind         string
	card         string
	assignee     string
	to           int
	prerequisite string
	version      int64
	expedite     bool
	now          int64
}

type modelCard struct {
	id        string
	assignee  string
	column    int
	version   int64
	expedited bool
}

type naiveModel struct {
	columns       []Column
	g             int
	cards         map[string]*modelCard
	prerequisites map[string]map[string]struct{}
	lastNow       int64
}

func newNaiveModel(columns []Column, g int) *naiveModel {
	return &naiveModel{
		columns:       append([]Column(nil), columns...),
		g:             g,
		cards:         make(map[string]*modelCard),
		prerequisites: make(map[string]map[string]struct{}),
	}
}

func (m *naiveModel) done() int { return len(m.columns) - 1 }

func (m *naiveModel) progress(column int) bool { return column > 0 && column < m.done() }

func modelValid(op modelOperation, columns int) bool {
	if op.card == "" || op.now < 0 || op.now > 1_000_000_000_000 || op.version < 0 {
		return false
	}
	if op.kind == "create" {
		return op.assignee != ""
	}
	if op.kind == "move" && op.to < 0 {
		return false
	}
	if op.kind == "assign" && op.assignee == "" {
		return false
	}
	if (op.kind == "addDep" || op.kind == "removeDep") && op.prerequisite == "" {
		return false
	}
	return true
}

func modelSnapshot(m *naiveModel) string {
	ids := make([]string, 0, len(m.cards))
	for id := range m.cards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := fmt.Sprintf("now=%d|", m.lastNow)
	for _, id := range ids {
		card := m.cards[id]
		result += fmt.Sprintf("%s:%s:c%d:v%d:e%d;", id, card.assignee, card.column, card.version, boolInt(card.expedited))
	}
	return result
}

func serviceSnapshot(t *testing.T, service *Service) string {
	t.Helper()
	snapshot, err := service.GetBoard("b")
	if err != nil {
		t.Fatalf("GetBoard(): %v", err)
	}
	ids := make([]string, 0, len(snapshot.Cards))
	for id := range snapshot.Cards {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := fmt.Sprintf("now=%d|", snapshot.LastNow)
	for _, id := range ids {
		card := snapshot.Cards[id]
		result += fmt.Sprintf("%s:%s:c%d:v%d:e%d;", id, card.Assignee, card.Column, card.Version, boolInt(card.Expedited))
	}
	return result
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func errorsString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *naiveModel) columnCount(column int, ignored string) int {
	count := 0
	for id, card := range m.cards {
		if id != ignored && card.column == column {
			count++
		}
	}
	return count
}

func (m *naiveModel) assigneeCount(assignee, ignored string) int {
	count := 0
	for id, card := range m.cards {
		if id != ignored && card.assignee == assignee && m.progress(card.column) {
			count++
		}
	}
	return count
}

func (m *naiveModel) capacity(card *modelCard, to int, expedited bool) (string, error) {
	if !expedited && to != m.done() && m.columns[to].Limit > 0 &&
		m.columnCount(to, card.id)+1 > m.columns[to].Limit {
		return "column full", ErrColumnLimitFull
	}
	if expedited || !m.progress(to) {
		return "", nil
	}
	if m.assigneeCount(card.assignee, card.id)+1 > m.g {
		return "assignee full", ErrAssigneeLimitFull
	}
	return "", nil
}

func (m *naiveModel) reverseEdges(id string) map[string]struct{} {
	result := make(map[string]struct{})
	for cardID, prerequisites := range m.prerequisites {
		if _, ok := prerequisites[id]; ok {
			result[cardID] = struct{}{}
		}
	}
	return result
}

func (m *naiveModel) path(from, to string) bool {
	visited := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == to {
			return true
		}
		if visited[current] {
			continue
		}
		visited[current] = true
		for prerequisite := range m.prerequisites[current] {
			stack = append(stack, prerequisite)
		}
	}
	return false
}

func (m *naiveModel) anotherExpedited(ignored string) bool {
	for id, card := range m.cards {
		if id != ignored && card.expedited && m.progress(card.column) {
			return true
		}
	}
	return false
}

func (m *naiveModel) apply(op modelOperation) (string, error) {
	if !modelValid(op, len(m.columns)) {
		return "invalid argument", ErrInvalidArgument
	}
	if op.now < m.lastNow {
		return "clock rewound", ErrClockRewound
	}

	switch op.kind {
	case "create":
		if _, exists := m.cards[op.card]; exists {
			return "card already exists", ErrInvalidArgument
		}
		if m.columns[0].Limit > 0 && m.columnCount(0, "") >= m.columns[0].Limit {
			return "todo column full", ErrColumnLimitFull
		}
		m.cards[op.card] = &modelCard{id: op.card, assignee: op.assignee, version: 1}
		m.prerequisites[op.card] = map[string]struct{}{}
		m.lastNow = op.now
		return "card created", nil
	case "move":
		card, ok := m.cards[op.card]
		if !ok {
			return "card missing", ErrCardNotFound
		}
		if op.version != card.version {
			return "version conflict", ErrVersionConflict
		}
		if op.to >= len(m.columns) {
			return "target missing", ErrIllegalTransition
		}
		if op.expedite && !m.progress(op.to) {
			return "expedite target invalid", ErrInvalidArgument
		}
		if card.column == m.done() || op.to == card.column || (op.to > card.column && op.to != card.column+1) {
			return "illegal transition", ErrIllegalTransition
		}
		if card.column == 0 {
			for prerequisite := range m.prerequisites[card.id] {
				pre := m.cards[prerequisite]
				if pre == nil || pre.column != m.done() {
					return "dependency blocked", ErrDependencyBlocked
				}
			}
		}
		expedited := false
		if m.progress(op.to) {
			expedited = op.expedite || (card.expedited && m.progress(card.column))
			if op.expedite && m.anotherExpedited(card.id) {
				return "expedite occupied", ErrExpediteOccupied
			}
		}
		if reason, err := m.capacity(card, op.to, expedited); err != nil {
			return reason, err
		}
		card.column = op.to
		card.expedited = expedited
		card.version++
		m.lastNow = op.now
		return "move accepted", nil
	case "reopen":
		card, ok := m.cards[op.card]
		if !ok {
			return "card missing", ErrCardNotFound
		}
		if op.version != card.version {
			return "version conflict", ErrVersionConflict
		}
		if card.column != m.done() {
			return "illegal reopen", ErrIllegalTransition
		}
		for dependent := range m.reverseEdges(card.id) {
			dep := m.cards[dependent]
			if dep != nil && dep.column != 0 {
				return "dependent blocked", ErrDependencyBlocked
			}
		}
		target := m.done() - 1
		if op.expedite && m.anotherExpedited(card.id) {
			return "expedite occupied", ErrExpediteOccupied
		}
		if reason, err := m.capacity(card, target, op.expedite); err != nil {
			return reason, err
		}
		card.column = target
		card.expedited = op.expedite
		card.version++
		m.lastNow = op.now
		return "reopen accepted", nil
	case "assign":
		card, ok := m.cards[op.card]
		if !ok {
			return "card missing", ErrCardNotFound
		}
		if op.version != card.version {
			return "version conflict", ErrVersionConflict
		}
		if m.progress(card.column) && !card.expedited && op.assignee != card.assignee &&
			m.assigneeCount(op.assignee, card.id)+1 > m.g {
			return "assignee full", ErrAssigneeLimitFull
		}
		card.assignee = op.assignee
		card.version++
		m.lastNow = op.now
		return "assign accepted", nil
	case "addDep":
		card, ok := m.cards[op.card]
		if !ok {
			return "card missing", ErrCardNotFound
		}
		if op.version != card.version {
			return "version conflict", ErrVersionConflict
		}
		if _, exists := m.cards[op.prerequisite]; !exists {
			return "prerequisite missing", ErrCardNotFound
		}
		if _, exists := m.prerequisites[card.id][op.prerequisite]; exists {
			return "dependency exists", ErrDependencyExists
		}
		if op.card == op.prerequisite || m.path(op.prerequisite, op.card) {
			return "dependency cycle", ErrDependencyCycle
		}
		if card.column != 0 && m.cards[op.prerequisite].column != m.done() {
			return "dependency unfinished", ErrDependencyBlocked
		}
		m.prerequisites[card.id][op.prerequisite] = struct{}{}
		card.version++
		m.lastNow = op.now
		return "dependency added", nil
	case "removeDep":
		card, ok := m.cards[op.card]
		if !ok {
			return "card missing", ErrCardNotFound
		}
		if op.version != card.version {
			return "version conflict", ErrVersionConflict
		}
		if _, exists := m.cards[op.prerequisite]; !exists {
			return "prerequisite missing", ErrCardNotFound
		}
		if _, exists := m.prerequisites[card.id][op.prerequisite]; !exists {
			return "dependency missing", ErrDependencyNotFound
		}
		delete(m.prerequisites[card.id], op.prerequisite)
		card.version++
		m.lastNow = op.now
		return "dependency removed", nil
	}
	return "unknown operation", ErrInvalidArgument
}

func TestNaiveModelComparison1500(t *testing.T) {
	rng := rand.New(rand.NewSource(1626))
	totalOps := 0
	for sequence := 0; sequence < 1500; sequence++ {
		config := BoardConfig{
			Columns: []Column{
				{Name: "todo", Limit: 0},
				{Name: "dev", Limit: rng.Intn(3)},
				{Name: "review", Limit: rng.Intn(2) + 1},
				{Name: "done"},
			},
			AssigneeLimit: 1 + rng.Intn(3),
		}
		var decisions []Decision
		service := NewServiceWithLogger(LoggerFunc(func(decision Decision) {
			decisions = append(decisions, decision)
		}))
		if err := service.CreateBoard("b", config); err != nil {
			t.Fatalf("sequence %d: CreateBoard(): %v", sequence, err)
		}
		model := newNaiveModel(config.Columns, config.AssigneeLimit)
		now := int64(0)

		cardCount := 3 + rng.Intn(5)
		for i := 0; i < cardCount; i++ {
			op := modelOperation{
				kind:     "create",
				card:     fmt.Sprintf("c%d", i),
				assignee: fmt.Sprintf("u%d", rng.Intn(3)),
				now:      now + int64(rng.Intn(2)),
			}
			_, modelErr := model.apply(op)
			_, serviceErr := service.CreateCard(CreateCardRequest{BoardID: "b", User: "tester", Card: op.card, Assignee: op.assignee, Now: op.now})
			if errorsString(modelErr) != errorsString(serviceErr) {
				logDecisions(t, sequence, decisions)
				t.Fatalf("sequence %d create %#v: model=%v service=%v", sequence, op, modelErr, serviceErr)
			}
			if modelErr == nil {
				now = op.now
			}
			totalOps++
		}

		opCount := 18 + rng.Intn(30)
		for i := 0; i < opCount; i++ {
			cardID := fmt.Sprintf("c%d", rng.Intn(cardCount))
			card := model.cards[cardID]
			op := modelOperation{
				card:    cardID,
				version: card.version,
				now:     now + int64(rng.Intn(3)),
			}
			if rng.Intn(10) == 0 {
				op.now = now - 1
			}
			if rng.Intn(8) == 0 {
				op.version = card.version + int64(1+rng.Intn(3))
			}

			switch rng.Intn(6) {
			case 0:
				op.kind = "move"
				op.to = card.column + 1
				if rng.Intn(4) == 0 {
					op.to = rng.Intn(card.column + 1)
				}
				if rng.Intn(5) == 0 {
					op.to = rng.Intn(6)
				}
				if model.progress(op.to) && rng.Intn(3) == 0 {
					op.expedite = true
				}
			case 1:
				op.kind = "reopen"
				op.expedite = rng.Intn(3) == 0
			case 2:
				op.kind = "assign"
				op.assignee = fmt.Sprintf("u%d", rng.Intn(4))
			case 3, 4:
				op.kind = "addDep"
				op.prerequisite = fmt.Sprintf("c%d", rng.Intn(cardCount))
			default:
				op.kind = "removeDep"
				op.prerequisite = fmt.Sprintf("c%d", rng.Intn(cardCount))
			}

			reason, modelErr := model.apply(op)
			serviceErr := dispatchOperation(service, op)
			if errorsString(modelErr) != errorsString(serviceErr) {
				logDecisions(t, sequence, decisions)
				t.Fatalf("sequence %d op %#v reason %q: model=%v service=%v\nmodel=%s\nservice=%s",
					sequence, op, reason, modelErr, serviceErr, modelSnapshot(model), serviceSnapshot(t, service))
			}
			if modelErr == nil {
				now = op.now
			}
			last := decisions[len(decisions)-1]
			t.Logf("seq=%d input=%#v output=%+v err=%v basis=%s state=%s", sequence, op, last.Output, last.Err, last.Reason, modelSnapshot(model))
			if modelSnapshot(model) != serviceSnapshot(t, service) {
				t.Fatalf("sequence %d state mismatch after %#v", sequence, op)
			}
			totalOps++
		}
	}
	t.Logf("compared %d random operations", totalOps)
}

func dispatchOperation(service *Service, op modelOperation) error {
	switch op.kind {
	case "move":
		_, err := service.Move(MoveRequest{BoardID: "b", User: "tester", Card: op.card, To: op.to, ExpectVersion: op.version, Expedite: op.expedite, Now: op.now})
		return err
	case "reopen":
		_, err := service.Reopen(ReopenRequest{BoardID: "b", User: "tester", Card: op.card, ExpectVersion: op.version, Expedite: op.expedite, Now: op.now})
		return err
	case "assign":
		_, err := service.Assign(AssigneeRequest{BoardID: "b", User: "tester", Card: op.card, Assignee: op.assignee, ExpectVersion: op.version, Now: op.now})
		return err
	case "addDep":
		_, err := service.AddDep(DependencyRequest{BoardID: "b", User: "tester", Card: op.card, Prerequisite: op.prerequisite, ExpectVersion: op.version, Now: op.now})
		return err
	case "removeDep":
		_, err := service.RemoveDep(DependencyRequest{BoardID: "b", User: "tester", Card: op.card, Prerequisite: op.prerequisite, ExpectVersion: op.version, Now: op.now})
		return err
	}
	return ErrInvalidArgument
}

func logDecisions(t *testing.T, sequence int, decisions []Decision) {
	t.Helper()
	for index, decision := range decisions {
		t.Logf("seq=%d #%d input=%#v output=%+v err=%v basis=%s", sequence, index, decision.Input, decision.Output, decision.Err, decision.Reason)
	}
}
