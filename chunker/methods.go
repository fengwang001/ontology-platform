package chunker

import (
	"time"
)

type entry struct {
	data       []byte
	extensions []Extension
}

func New(config Config) *Chunker {
	return &Chunker{config: normalizedConfig(config)}
}

func (c *Chunker) Add(now time.Time, data []byte, extensions []Extension) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(data) == 0 {
		return 0
	}
	if len(c.entries) == 0 {
		c.start = now
	}
	copied := append([]byte(nil), data...)
	c.entries = append(c.entries, entry{data: copied, extensions: append([]Extension(nil), extensions...)})
	return len(copied)
}

func (c *Chunker) Due(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries) > 0 && !now.Before(c.start.Add(c.config.Window))
}

func (c *Chunker) Drain(now time.Time, force bool) []Item {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) == 0 {
		return nil
	}
	total := c.totalLocked()
	if !force && total < c.config.MinSize && now.Before(c.start.Add(c.config.Window)) {
		return nil
	}
	items := make([]Item, 0, (total+c.config.MaxSize-1)/c.config.MaxSize)
	for len(c.entries) > 0 {
		total = c.totalLocked()
		if !force && len(items) == 0 && total < c.config.MinSize && now.Before(c.start.Add(c.config.Window)) {
			break
		}
		payload, extensions := c.takeLocked(c.config.MaxSize)
		items = append(items, Item{Payload: payload, Extensions: extensions})
	}
	if len(c.entries) == 0 {
		c.start = time.Time{}
	}
	return items
}

func (c *Chunker) PendingPayload() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totalLocked()
}

func (c *Chunker) EncodedCost(lineCost func(Item) int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.planCostLocked(c.entries, lineCost)
}

func (c *Chunker) CostAfter(data []byte, extensions []Extension, lineCost func(Item) int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := append(append([]entry(nil), c.entries...), entry{data: data, extensions: extensions})
	return c.planCostLocked(entries, lineCost)
}

func (c *Chunker) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot := Snapshot{Start: c.start, Entries: make([]Entry, 0, len(c.entries))}
	for _, item := range c.entries {
		snapshot.Entries = append(snapshot.Entries, Entry{
			Data:       append([]byte(nil), item.data...),
			Extensions: append([]Extension(nil), item.extensions...),
		})
	}
	return snapshot
}

func (c *Chunker) Restore(snapshot Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make([]entry, 0, len(snapshot.Entries))
	for _, item := range snapshot.Entries {
		c.entries = append(c.entries, entry{
			data:       append([]byte(nil), item.Data...),
			extensions: append([]Extension(nil), item.Extensions...),
		})
	}
	c.start = snapshot.Start
}

func (c *Chunker) totalLocked() int {
	total := 0
	for _, item := range c.entries {
		total += len(item.data)
	}
	return total
}

func (c *Chunker) takeLocked(limit int) ([]byte, []Extension) {
	payload := make([]byte, 0, min(limit, c.totalLocked()))
	var extensions []Extension
	for len(payload) < limit && len(c.entries) > 0 {
		first := &c.entries[0]
		if len(payload) == 0 {
			extensions = append([]Extension(nil), first.extensions...)
		}
		needed := limit - len(payload)
		if len(first.data) <= needed {
			payload = append(payload, first.data...)
			c.entries = c.entries[1:]
			continue
		}
		payload = append(payload, first.data[:needed]...)
		first.data = first.data[needed:]
	}
	return payload, extensions
}

func (c *Chunker) planCostLocked(entries []entry, lineCost func(Item) int) int {
	total := 0
	for len(entries) > 0 {
		payload, extensions := takeFromEntries(&entries, c.config.MaxSize)
		total += len(payload) + lineCost(Item{Payload: payload, Extensions: extensions})
	}
	return total
}

func takeFromEntries(entries *[]entry, limit int) ([]byte, []Extension) {
	payload := make([]byte, 0, limit)
	var extensions []Extension
	for len(payload) < limit && len(*entries) > 0 {
		first := &(*entries)[0]
		if len(payload) == 0 {
			extensions = append([]Extension(nil), first.extensions...)
		}
		needed := limit - len(payload)
		if len(first.data) <= needed {
			payload = append(payload, first.data...)
			*entries = (*entries)[1:]
			continue
		}
		payload = append(payload, first.data[:needed]...)
		first.data = first.data[needed:]
	}
	return payload, extensions
}
