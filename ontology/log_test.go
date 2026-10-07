package ontology_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/ontology"
)

type recordingLogger struct {
	mu     sync.Mutex
	events []ontology.Event
}

func (l *recordingLogger) Log(event ontology.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *recordingLogger) snapshot() []ontology.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ontology.Event(nil), l.events...)
}

func printEvent(t *testing.T, event ontology.Event) {
	t.Helper()
	t.Logf("event tx=%d op=%s object=%q property=%q committed=%t recovered=%t reason=%q writes=%v added=%v removed=%v attempted=%v",
		event.TxID,
		event.Op,
		event.Object,
		event.Property,
		event.Committed,
		event.Recovered,
		event.Reason,
		event.Writes,
		entries(event.IndexChanges, true),
		entries(event.IndexChanges, false),
		entries(event.AttemptedChanges, true),
	)
}

func entries(changes map[string]ontology.EntryChange, added bool) []string {
	keys := make([]string, 0, len(changes))
	for key := range changes {
		keys = append(keys, key)
	}
	sortStrings(keys)
	out := make([]string, 0)
	for _, key := range keys {
		change := changes[key]
		selected := change.Removed
		if added {
			selected = change.Added
		}
		for _, entry := range selected {
			out = append(out, fmt.Sprintf("%s/%s/%v=%s", entry.Index, entry.Property, entry.Key.Data, entry.Object))
		}
	}
	return out
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
