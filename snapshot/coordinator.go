package snapshot

import "sync"

type coordinatorState struct {
	events          []Event
	nextLSN         int64
	pending         map[string][]Record
	material        *Snapshot
	acceptedRecords int
	export          *activeExport
}

type activeExport struct {
	boundary int64
	frames   chan<- ExportFrame
	limits   Limits
}

type commitResult struct {
	lsn int64
	err error
}

type Coordinator struct {
	once  sync.Once
	queue chan func()
	state *coordinatorState
}

func NewCoordinator() *Coordinator {
	coordinator := &Coordinator{}
	coordinator.ensureStarted()
	return coordinator
}

func (c *Coordinator) Begin(txID string, records ...Record) error {
	result := make(chan error, 1)
	c.enqueue(func() {
		if txID == "" {
			result <- atomicityError(0, "", "transaction id is empty")
			return
		}
		if _, exists := c.state.pending[txID]; exists {
			result <- atomicityError(0, txID, "transaction %q is already active", txID)
			return
		}
		c.state.pending[txID] = cloneRecords(records)
		result <- nil
	})
	return <-result
}

func (c *Coordinator) Append(txID string, records ...Record) error {
	result := make(chan error, 1)
	c.enqueue(func() {
		batch, exists := c.state.pending[txID]
		if !exists {
			result <- atomicityError(0, txID, "transaction %q is not active", txID)
			return
		}
		c.state.pending[txID] = append(batch, cloneRecords(records)...)
		result <- nil
	})
	return <-result
}

func (c *Coordinator) Commit(txID string) (int64, error) {
	result := make(chan commitResult, 1)
	c.enqueue(func() {
		records, exists := c.state.pending[txID]
		if !exists {
			result <- commitResult{err: atomicityError(0, txID, "transaction %q is not active", txID)}
			return
		}
		validator := NewValidator()
		for _, record := range records {
			copyRecord := record
			if err := validator.ValidateEvent(Event{Type: EventPrepare, TxID: txID, Record: &copyRecord}, NoLimits); err != nil {
				delete(c.state.pending, txID)
				result <- commitResult{err: err}
				return
			}
		}
		if err := validator.CheckIntegrity(c.state.material, txID, records); err != nil {
			delete(c.state.pending, txID)
			result <- commitResult{err: err}
			return
		}

		c.state.nextLSN++
		lsn := c.state.nextLSN
		if c.state.export != nil {
			if err := checkResourceLimits(int(lsn), c.state.acceptedRecords+len(records), c.state.export.limits); err != nil {
				c.state.nextLSN--
				delete(c.state.pending, txID)
				result <- commitResult{err: err}
				c.deliverExportError(c.state.export.frames, err)
				return
			}
		}
		events := make([]Event, 0, len(records)+1)
		for _, record := range records {
			copyRecord := record
			events = append(events, Event{Type: EventPrepare, TxID: txID, Record: &copyRecord})
			applyRecord(c.state.material, record)
		}
		c.state.acceptedRecords += len(records)
		events = append(events, Event{Type: EventCommit, TxID: txID, CommitLSN: lsn})

		frames, exportErr := c.appendAccepted(events)
		if exportErr != nil {
			c.state.nextLSN--
			delete(c.state.pending, txID)
			result <- commitResult{err: exportErr}
			c.deliverExportError(frames, exportErr)
			return
		}
		delete(c.state.pending, txID)
		result <- commitResult{lsn: lsn}
	})
	value := <-result
	return value.lsn, value.err
}

func (c *Coordinator) Abort(txID string) error {
	result := make(chan error, 1)
	c.enqueue(func() {
		if _, exists := c.state.pending[txID]; !exists {
			result <- atomicityError(0, txID, "transaction %q is not active", txID)
			return
		}
		delete(c.state.pending, txID)
		result <- nil
	})
	return <-result
}

func (c *Coordinator) Export(requestedBoundary int64, limits Limits) <-chan ExportFrame {
	frames := make(chan ExportFrame, 16)
	c.enqueue(func() {
		history := append([]Event(nil), c.state.events...)
		exportFrames, err := NewExporter(history).Export(requestedBoundary, limits)
		for _, frame := range exportFrames {
			if frame.Done {
				continue
			}
			frames <- frame
			if frame.Error != nil {
				close(frames)
				return
			}
		}
		if err != nil {
			frames <- ExportFrame{Error: exportErrorPtr(err)}
			close(frames)
			return
		}
		boundary := int64(0)
		for _, frame := range exportFrames {
			if frame.Snapshot != nil {
				boundary = frame.Snapshot.Boundary
			}
		}
		if c.state.export != nil {
			close(c.state.export.frames)
		}
		c.state.export = &activeExport{boundary: boundary, frames: frames, limits: limits}
	})
	return frames
}

func (c *Coordinator) appendAccepted(events []Event) (chan<- ExportFrame, error) {
	c.state.events = append(c.state.events, events...)
	if c.state.export == nil {
		return nil, nil
	}
	commitEvent := events[len(events)-1]
	if commitEvent.CommitLSN <= c.state.export.boundary {
		return c.state.export.frames, boundaryError("accepted commit %d cannot precede active export boundary %d", commitEvent.CommitLSN, c.state.export.boundary)
	}
	records := make([]Record, 0, len(events)-1)
	for _, event := range events[:len(events)-1] {
		if event.Record != nil {
			records = append(records, *event.Record)
		}
	}
	c.state.export.frames <- ExportFrame{Deltas: []Delta{{
		CommitLSN: commitEvent.CommitLSN,
		TxID:      commitEvent.TxID,
		Records:   records,
	}}}
	return c.state.export.frames, nil
}

func (c *Coordinator) deliverExportError(frames chan<- ExportFrame, err error) {
	if frames == nil || c.state.export == nil {
		return
	}
	frames <- ExportFrame{Error: exportErrorPtr(err)}
	close(frames)
	c.state.export = nil
}

func (c *Coordinator) enqueue(action func()) {
	c.ensureStarted()
	c.queue <- action
}

func (c *Coordinator) ensureStarted() {
	c.once.Do(func() {
		c.queue = make(chan func())
		c.state = &coordinatorState{
			pending:  map[string][]Record{},
			material: newSnapshot(0),
		}
		go func() {
			for action := range c.queue {
				action()
			}
		}()
	})
}

func cloneRecords(records []Record) []Record {
	result := make([]Record, len(records))
	copy(result, records)
	return result
}
