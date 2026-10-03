package introspect

import (
	"errors"
	"sync"
)

var ErrInvalidArgument = errors.New("invalid argument")

type Result struct {
	Active bool
	Sub    string
	Iat    int64
	Exp    int64
	Scopes []string
}

type Func func(token string) (Result, error)

type Entry struct {
	Result Result
	F0     int64
	Until  int64
}

type Source int

const (
	SourceCache Source = iota
	SourceFresh
)

type Cache struct {
	mu                   sync.RWMutex
	positive             int64
	negative             int64
	upstream             Func
	entries              map[string]Entry
	flights              map[string]*flight
	calls                int64
	revokeTouchedEntries int64
}

func New(positive, negative int64, upstream Func) *Cache {
	if positive < 1 || positive > 1_000_000_000 || negative < 1 || negative > 1_000_000_000 || upstream == nil {
		panic(ErrInvalidArgument)
	}
	return &Cache{
		positive: positive,
		negative: negative,
		upstream: upstream,
		entries:  make(map[string]Entry),
		flights:  make(map[string]*flight),
	}
}

func (c *Cache) Resolve(token string, now int64) (Entry, Source, Entry, error) {
	if entry, ok := c.getFresh(token, now); ok {
		return entry, SourceCache, Entry{}, nil
	}

	c.mu.Lock()
	if entry, ok := c.lookupFreshLocked(token, now); ok {
		c.mu.Unlock()
		return entry, SourceCache, Entry{}, nil
	}
	if call := c.flights[token]; call != nil {
		c.mu.Unlock()
		<-call.done
		if call.err == nil {
			if entry, ok := c.get(token); ok {
				return entry, SourceFresh, Entry{}, nil
			}
		}
		stale, _ := c.get(token)
		return Entry{}, SourceFresh, stale, call.err
	}

	call := &flight{done: make(chan struct{})}
	c.flights[token] = call
	c.calls++
	c.mu.Unlock()

	res, err := c.upstream(token)
	call.res = res
	call.err = err

	c.mu.Lock()
	if err == nil {
		c.entries[token] = newEntry(res, now, c.positive, c.negative)
	}
	delete(c.flights, token)
	entry, _ := c.lookupLocked(token)
	stale, _ := c.lookupLocked(token)
	c.mu.Unlock()
	close(call.done)

	if err != nil {
		return Entry{}, SourceFresh, stale, err
	}
	return entry, SourceFresh, Entry{}, nil
}

func (c *Cache) Calls() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.calls
}

func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

func (c *Cache) RevokeTouchedEntries() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.revokeTouchedEntries
}

func (c *Cache) getFresh(token string, now int64) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lookupFreshLocked(token, now)
}

func (c *Cache) get(token string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lookupLocked(token)
}

func (c *Cache) lookupFreshLocked(token string, now int64) (Entry, bool) {
	entry, ok := c.lookupLocked(token)
	return entry, ok && now < entry.Until
}

func (c *Cache) lookupLocked(token string) (Entry, bool) {
	entry, ok := c.entries[token]
	return entry, ok
}

func newEntry(res Result, f0, positive, negative int64) Entry {
	until := f0 + negative
	if res.Active {
		until = f0 + positive
		if res.Exp < until {
			until = res.Exp
		}
	}
	return Entry{Result: res, F0: f0, Until: until}
}
