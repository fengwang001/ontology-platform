package stagedcommit

import (
	"errors"
	"maps"
	"sync"
)

var (
	ErrEmptyKey         = errors.New("empty key is not allowed")
	ErrEmptyCommit      = errors.New("commit with empty staging area is not allowed")
	ErrDeleteNotAllowed = errors.New("delete target cannot be deleted")
	ErrPendingRecovery  = errors.New("prepared commit is waiting for recovery")
)

type Operation string

const (
	OpPut    Operation = "put"
	OpDelete Operation = "delete"
)

type LogState string

const (
	StatePrepared  LogState = "prepared"
	StateCommitted LogState = "committed"
	StateAborted   LogState = "aborted"
)

type Change struct {
	Op    Operation
	Value string
}

type LogEntry struct {
	CommitID uint64
	State    LogState
	Changes  map[string]Change
}

type Committer struct {
	mu     sync.RWMutex
	view   map[string]string
	staged map[string]Change
	log    []LogEntry
	nextID uint64
}

func New() *Committer {
	return &Committer{
		view:   make(map[string]string),
		staged: make(map[string]Change),
		nextID: 1,
	}
}

func (c *Committer) Put(key, value string) error {
	if key == "" {
		return ErrEmptyKey
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.staged[key] = Change{Op: OpPut, Value: value}
	return nil
}

func (c *Committer) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.view[key]; !exists {
		return ErrDeleteNotAllowed
	}
	if _, changed := c.staged[key]; changed {
		return ErrDeleteNotAllowed
	}

	c.staged[key] = Change{Op: OpDelete}
	return nil
}

func (c *Committer) Rollback() {
	c.mu.Lock()
	defer c.mu.Unlock()

	clear(c.staged)
}

func (c *Committer) Commit() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateCommitLocked(); err != nil {
		return 0, err
	}

	id := c.prepareLocked()
	c.applyPreparedLocked(id)
	return id, nil
}

func (c *Committer) SimulateCrashAfterPrepare() (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.validateCommitLocked(); err != nil {
		return 0, err
	}

	id := c.prepareLocked()
	clear(c.staged)
	return id, nil
}

func (c *Committer) Recover() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i := len(c.log) - 1; i >= 0; i-- {
		if c.log[i].State == StatePrepared {
			c.log[i].State = StateAborted
			return c.log[i].CommitID
		}
	}

	return 0
}

func (c *Committer) validateCommitLocked() error {
	if len(c.staged) == 0 {
		return ErrEmptyCommit
	}
	if c.hasPreparedLocked() {
		return ErrPendingRecovery
	}

	return nil
}

func (c *Committer) prepareLocked() uint64 {
	id := c.nextID
	c.nextID++
	c.log = append(c.log, LogEntry{
		CommitID: id,
		State:    StatePrepared,
		Changes:  maps.Clone(c.staged),
	})

	return id
}

func (c *Committer) applyPreparedLocked(id uint64) {
	for i := range c.log {
		if c.log[i].CommitID != id || c.log[i].State != StatePrepared {
			continue
		}

		for key, change := range c.log[i].Changes {
			switch change.Op {
			case OpPut:
				c.view[key] = change.Value
			case OpDelete:
				delete(c.view, key)
			}
		}

		c.log[i].State = StateCommitted
		clear(c.staged)
		return
	}
}

func (c *Committer) hasPreparedLocked() bool {
	for i := len(c.log) - 1; i >= 0; i-- {
		if c.log[i].State == StatePrepared {
			return true
		}
	}

	return false
}

func (c *Committer) ViewSnapshot() map[string]string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return maps.Clone(c.view)
}

func (c *Committer) StagedSnapshot() map[string]Change {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return maps.Clone(c.staged)
}

func (c *Committer) LogSnapshot() []LogEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return cloneLog(c.log)
}

func cloneLog(entries []LogEntry) []LogEntry {
	if entries == nil {
		return nil
	}

	cloned := make([]LogEntry, len(entries))
	for i, entry := range entries {
		cloned[i] = LogEntry{
			CommitID: entry.CommitID,
			State:    entry.State,
			Changes:  maps.Clone(entry.Changes),
		}
	}

	return cloned
}
