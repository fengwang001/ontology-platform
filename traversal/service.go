package traversal

import (
	"crypto/rand"
	"sync"
	"time"
)

type Service struct {
	mu            sync.Mutex
	graph         *Graph
	key           []byte
	states        map[string]*traversalState
	logs          []RequestLog
	historyChecks int64
}

func NewService(graph *Graph) *Service {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return &Service{
		graph:  graph,
		key:    key,
		states: make(map[string]*traversalState),
	}
}

func (s *Service) FirstPage(request PageRequest) (Page, error) {
	request.Cursor = ""
	return s.Page(request)
}

func (s *Service) NextPage(request PageRequest) (Page, error) {
	return s.Page(request)
}

func (s *Service) Page(request PageRequest) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.StartObjectID != "" && request.Cursor != "" {
		return s.failRequest(request, &TraversalError{Code: ErrInvalidCursor, Message: "cursor does not belong to this traversal"})
	}

	var state *traversalState
	snapshot := Snapshot{}
	if request.Cursor != "" {
		payload, err := decodeCursor(request.Cursor, s.key)
		if err != nil {
			return s.failRequest(request, &TraversalError{Code: ErrInvalidCursor, Message: "cursor cannot be parsed"})
		}
		state = s.states[payload.TraversalID]
		s.historyChecks++
		if state == nil {
			return s.failRequest(request, &TraversalError{Code: ErrInvalidCursor, Message: "unknown traversal snapshot"})
		}
		state.historyChecks++
		if payload.Sequence != state.nextSeq {
			return s.failRequest(request, &TraversalError{Code: ErrInvalidCursor, Message: "cursor has already been used or points behind the current page"})
		}
	} else {
		snapshot = s.graph.Snapshot()
		if !snapshot.HasObject(request.StartObjectID) {
			return s.failRequest(request, &TraversalError{Code: ErrStartObjectNotFound, Message: "start object not found"})
		}
	}

	if request.BatchSize <= 0 {
		return s.failRequest(request, &TraversalError{Code: ErrInvalidBatchSize, Message: "batch size must be a positive integer"})
	}

	mode := request.Mode
	if state != nil {
		if request.Mode != state.mode {
			return s.failRequest(request, &TraversalError{Code: ErrModeChanged, Message: "traversal mode cannot be changed after the first request"})
		}
		mode = state.mode
	}
	if mode != SilentDrop && mode != ExplicitTruncation {
		return s.failRequest(request, &TraversalError{Code: ErrModeChanged, Message: "unsupported traversal mode"})
	}

	if state == nil {
		engine, err := newEngine(snapshot, request.StartObjectID, mode, request.HopLimits, request.MaxHops)
		if err != nil {
			return s.failRequest(request, err)
		}
		id, err := newTraversalID()
		if err != nil {
			return s.failRequest(request, err)
		}
		state = &traversalState{
			id:        id,
			snapshot:  snapshot,
			mode:      mode,
			hopLimits: append([]int(nil), request.HopLimits...),
			maxHops:   request.MaxHops,
			engine:    engine,
			nextSeq:   1,
		}
		s.states[id] = state
	}

	page := Page{Items: []ResultItem{}, Truncations: []TruncationMarker{}}
	for len(page.Items) < request.BatchSize {
		step, ok := state.engine.next()
		if !ok {
			state.complete = true
			page.Complete = true
			break
		}
		if step.truncation != nil {
			page.Truncations = append(page.Truncations, *step.truncation)
			continue
		}
		page.Items = append(page.Items, cloneResultItem(step.item))
	}

	if !page.Complete {
		_, hasMore := state.engine.peek()
		if !hasMore {
			page.Complete = true
			state.complete = true
		}
	}
	if !page.Complete {
		cursor, err := encodeCursor(cursorPayload{TraversalID: state.id, Sequence: state.nextSeq + 1}, s.key)
		if err != nil {
			return s.failRequest(request, err)
		}
		page.NextCursor = cursor
	}
	state.nextSeq++
	s.logs = append(s.logs, RequestLog{
		Time:        time.Now(),
		Cursor:      request.Cursor,
		Mode:        mode,
		Items:       cloneItems(page.Items),
		Truncations: append([]TruncationMarker(nil), page.Truncations...),
	})
	return clonePage(page), nil
}

func (s *Service) failRequest(request PageRequest, err error) (Page, error) {
	traversalErr, ok := err.(*TraversalError)
	if !ok {
		traversalErr = &TraversalError{Code: ErrInvalidCursor, Message: err.Error()}
	}
	s.logs = append(s.logs, RequestLog{
		Time:   time.Now(),
		Cursor: request.Cursor,
		Mode:   request.Mode,
		Items:  []ResultItem{},
		Error:  traversalErr,
	})
	return Page{}, traversalErr
}

func (s *Service) Logs() []RequestLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := make([]RequestLog, len(s.logs))
	for i, entry := range s.logs {
		logs[i] = RequestLog{
			Time:        entry.Time,
			Cursor:      entry.Cursor,
			Mode:        entry.Mode,
			Items:       cloneItems(entry.Items),
			Truncations: append([]TruncationMarker(nil), entry.Truncations...),
			Error:       entry.Error,
		}
	}
	return logs
}

func (s *Service) HistoryChecks() (int64, map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	perTraversal := make(map[string]int64, len(s.states))
	for id, state := range s.states {
		perTraversal[id] = state.historyChecks
	}
	return s.historyChecks, perTraversal
}

func clonePage(page Page) Page {
	page.Items = cloneItems(page.Items)
	page.Truncations = append([]TruncationMarker(nil), page.Truncations...)
	return page
}

func cloneItems(items []ResultItem) []ResultItem {
	cloned := make([]ResultItem, len(items))
	for i, item := range items {
		cloned[i] = cloneResultItem(item)
	}
	return cloned
}

func cloneResultItem(item ResultItem) ResultItem {
	cloned := ResultItem{Hop: item.Hop}
	if item.Object != nil {
		object := cloneObject(*item.Object)
		cloned.Object = &object
	}
	if item.Link != nil {
		link := cloneLink(*item.Link)
		cloned.Link = &link
	}
	return cloned
}
