package kanban

import "sync"

type Service struct {
	mu     sync.Mutex
	boards map[string]*board
	logger Logger
}

func NewService() *Service {
	return &Service{boards: make(map[string]*board)}
}

func NewServiceWithLogger(logger Logger) *Service {
	return &Service{boards: make(map[string]*board), logger: logger}
}

func (s *Service) CreateBoard(id string, config BoardConfig) error {
	if err := validateBoardConfig(id, config); err != nil {
		return s.fail("CreateBoard", id, config, nil, err, "board configuration is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.boards[id]; exists {
		return s.failLocked("CreateBoard", id, config, nil, ErrInvalidArgument, "board already exists")
	}
	s.boards[id] = newBoard(config)
	s.logLocked("CreateBoard", id, config, nil, nil, "board created")
	return nil
}

func (s *Service) CreateCard(req CreateCardRequest) (CardResult, error) {
	if !nonEmpty(req.BoardID) || !nonEmpty(req.User) || !nonEmpty(req.Card) || !nonEmpty(req.Assignee) || req.Now < 0 || req.Now > 1_000_000_000_000 {
		return CardResult{}, s.fail("CreateCard", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return CardResult{}, s.failLocked("CreateCard", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Now < b.lastNow {
		return CardResult{}, s.failLocked("CreateCard", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	if _, exists := b.cards[req.Card]; exists {
		return CardResult{}, s.failLocked("CreateCard", req.BoardID, req, nil, ErrInvalidArgument, "card already exists")
	}
	if !b.columnFits(b.todoColumn(), false) {
		return CardResult{}, s.failLocked("CreateCard", req.BoardID, req, nil, ErrColumnLimitFull, "todo column limit is full")
	}

	b.addCard(req.Card, req.Assignee)
	b.lastNow = req.Now
	result := CardResult{Card: b.snapshot(b.cards[req.Card]), Reason: "card created in todo column"}
	s.logLocked("CreateCard", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) Move(req MoveRequest) (MoveResult, error) {
	if !validVersionedRequest(req.BoardID, req.User, req.Card, req.ExpectVersion, req.Now) || req.To < 0 {
		return MoveResult{}, s.fail("Move", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Expedite && (req.To <= b.todoColumn() || req.To >= b.doneColumn()) {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, ErrInvalidArgument, "expedite is allowed only when entering a progress column")
	}
	if req.Now < b.lastNow {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	card, exists := b.cards[req.Card]
	if !exists {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, ErrCardNotFound, "card does not exist")
	}
	if req.ExpectVersion != card.version {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, ErrVersionConflict, "expected version does not match current version")
	}
	if req.To >= len(b.config.Columns) {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, req, ErrIllegalTransition, "target column does not exist")
	}
	if err := validateMoveTransition(card.column, req.To, b.doneColumn()); err != nil {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, err, "transition is not adjacent, leftward, or available")
	}

	if card.column == b.todoColumn() {
		if reason, err := b.blockedByPrerequisite(card.id); err != nil {
			return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, err, reason)
		}
	}
	expedited, reason, err := b.planExpedite(card, req.To, req.Expedite)
	if err != nil {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, err, reason)
	}
	if reason, err := b.checkCapacity(card, req.To, card.assignee, expedited); err != nil {
		return MoveResult{}, s.failLocked("Move", req.BoardID, req, nil, err, reason)
	}

	b.placeCard(card, req.To, expedited)
	card.version++
	b.lastNow = req.Now
	result := MoveResult{Card: b.snapshot(card), Reason: "move accepted"}
	s.logLocked("Move", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) Reopen(req ReopenRequest) (CardResult, error) {
	if !validVersionedRequest(req.BoardID, req.User, req.Card, req.ExpectVersion, req.Now) {
		return CardResult{}, s.fail("Reopen", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, err, "board does not exist")
	}
	target := b.doneColumn() - 1
	if req.Expedite && !b.isProgress(target) {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrInvalidArgument, "expedite is allowed only when entering a progress column")
	}
	if req.Now < b.lastNow {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	card, exists := b.cards[req.Card]
	if !exists {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrCardNotFound, "card does not exist")
	}
	if req.ExpectVersion != card.version {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrVersionConflict, "expected version does not match current version")
	}
	if card.column != b.doneColumn() {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrIllegalTransition, "only a done card can be reopened")
	}
	if reason, err := b.blockedByDependent(card.id); err != nil {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, err, reason)
	}
	expedited := false
	if req.Expedite {
		if b.expeditedCardID != "" && b.expeditedCardID != card.id {
			return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, ErrExpediteOccupied, "another expedited card is already in progress")
		}
		expedited = true
	}
	if reason, err := b.checkCapacity(card, target, card.assignee, expedited); err != nil {
		return CardResult{}, s.failLocked("Reopen", req.BoardID, req, nil, err, reason)
	}

	b.placeCard(card, target, expedited)
	card.version++
	b.lastNow = req.Now
	result := CardResult{Card: b.snapshot(card), Reason: "reopen accepted"}
	s.logLocked("Reopen", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) Assign(req AssigneeRequest) (CardResult, error) {
	if !validVersionedRequest(req.BoardID, req.User, req.Card, req.ExpectVersion, req.Now) || !nonEmpty(req.Assignee) {
		return CardResult{}, s.fail("Assign", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return CardResult{}, s.failLocked("Assign", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Now < b.lastNow {
		return CardResult{}, s.failLocked("Assign", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	card, exists := b.cards[req.Card]
	if !exists {
		return CardResult{}, s.failLocked("Assign", req.BoardID, req, nil, ErrCardNotFound, "card does not exist")
	}
	if req.ExpectVersion != card.version {
		return CardResult{}, s.failLocked("Assign", req.BoardID, req, nil, ErrVersionConflict, "expected version does not match current version")
	}

	if b.isProgress(card.column) && !card.expedited && req.Assignee != card.assignee {
		next := b.assigneeWIP[req.Assignee] + 1
		if next > b.config.AssigneeLimit {
			return CardResult{}, s.failLocked("Assign", req.BoardID, req, nil, ErrAssigneeLimitFull, "new assignee work-in-progress would exceed G")
		}
	}

	b.changeAssignee(card, req.Assignee)
	card.version++
	b.lastNow = req.Now
	result := CardResult{Card: b.snapshot(card), Reason: "assignee change accepted"}
	s.logLocked("Assign", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) AddDep(req DependencyRequest) (CardResult, error) {
	if !validVersionedRequest(req.BoardID, req.User, req.Card, req.ExpectVersion, req.Now) || !nonEmpty(req.Prerequisite) {
		return CardResult{}, s.fail("AddDep", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Now < b.lastNow {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	card, exists := b.cards[req.Card]
	if !exists {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrCardNotFound, "card does not exist")
	}
	if req.ExpectVersion != card.version {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrVersionConflict, "expected version does not match current version")
	}
	prerequisite, exists := b.cards[req.Prerequisite]
	if !exists {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrCardNotFound, "prerequisite card does not exist")
	}
	if b.dependencies.hasEdge(card.id, prerequisite.id) {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrDependencyExists, "dependency already exists")
	}
	if b.dependencies.createsCycle(card.id, prerequisite.id) {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrDependencyCycle, "dependency would create a cycle")
	}
	if card.column != b.todoColumn() && prerequisite.column != b.doneColumn() {
		return CardResult{}, s.failLocked("AddDep", req.BoardID, req, nil, ErrDependencyBlocked, "a card that left todo cannot receive an unfinished prerequisite")
	}

	b.dependencies.addEdge(card.id, prerequisite.id)
	card.version++
	b.lastNow = req.Now
	result := CardResult{Card: b.snapshot(card), Reason: "dependency added"}
	s.logLocked("AddDep", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) RemoveDep(req DependencyRequest) (CardResult, error) {
	if !validVersionedRequest(req.BoardID, req.User, req.Card, req.ExpectVersion, req.Now) || !nonEmpty(req.Prerequisite) {
		return CardResult{}, s.fail("RemoveDep", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Now < b.lastNow {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	card, exists := b.cards[req.Card]
	if !exists {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, ErrCardNotFound, "card does not exist")
	}
	if req.ExpectVersion != card.version {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, ErrVersionConflict, "expected version does not match current version")
	}
	if _, exists := b.cards[req.Prerequisite]; !exists {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, ErrCardNotFound, "prerequisite card does not exist")
	}
	if !b.dependencies.hasEdge(card.id, req.Prerequisite) {
		return CardResult{}, s.failLocked("RemoveDep", req.BoardID, req, nil, ErrDependencyNotFound, "dependency does not exist")
	}

	b.dependencies.removeEdge(card.id, req.Prerequisite)
	card.version++
	b.lastNow = req.Now
	result := CardResult{Card: b.snapshot(card), Reason: "dependency removed"}
	s.logLocked("RemoveDep", req.BoardID, req, result, nil, result.Reason)
	return result, nil
}

func (s *Service) SetColumnLimit(req ColumnLimitRequest) error {
	if !nonEmpty(req.BoardID) || !nonEmpty(req.User) || req.Column < 0 || req.Limit < 0 || req.Now < 0 || req.Now > 1_000_000_000_000 {
		return s.fail("SetColumnLimit", req.BoardID, req, nil, ErrInvalidArgument, "request fields are invalid")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.boardLocked(req.BoardID)
	if err != nil {
		return s.failLocked("SetColumnLimit", req.BoardID, req, nil, err, "board does not exist")
	}
	if req.Now < b.lastNow {
		return s.failLocked("SetColumnLimit", req.BoardID, req, nil, ErrClockRewound, "now is before the last accepted operation")
	}
	if req.Column >= len(b.config.Columns)-1 {
		return s.failLocked("SetColumnLimit", req.BoardID, req, nil, ErrInvalidArgument, "only non-done columns have a limit")
	}

	b.config.Columns[req.Column].Limit = req.Limit
	b.lastNow = req.Now
	s.logLocked("SetColumnLimit", req.BoardID, req, nil, nil, "column limit updated without evicting cards")
	return nil
}

func (s *Service) boardLocked(boardID string) (*board, error) {
	b, ok := s.boards[boardID]
	if !ok {
		return nil, ErrInvalidArgument
	}
	return b, nil
}

func (s *Service) fail(operation, boardID string, input, output any, err error, reason string) error {
	s.log(Decision{Operation: operation, BoardID: boardID, Input: input, Output: output, Err: err, Reason: reason})
	return err
}

func (s *Service) failLocked(operation, boardID string, input, output any, err error, reason string) error {
	s.logLocked(operation, boardID, input, output, err, reason)
	return err
}

func (s *Service) logLocked(operation, boardID string, input, output any, err error, reason string) {
	s.log(Decision{Operation: operation, BoardID: boardID, Input: input, Output: output, Err: err, Reason: reason})
}

func (s *Service) log(decision Decision) {
	if s.logger != nil {
		s.logger.Log(decision)
	}
}
