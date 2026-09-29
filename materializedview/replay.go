// Package materializedview rebuilds an ordered-key count materialized view
// with chunked checkpoints and atomic generation switching.
package materializedview

type Event struct {
	Key string
}

type CountView map[string]int

// ReplaySource computes the deterministic result obtained by replaying every
// ordered source event from an empty view.
func ReplaySource(events []Event) CountView {
	result := make(CountView, len(events))
	for _, event := range events {
		result[event.Key]++
	}
	return result
}

func cloneView(view CountView) CountView {
	result := make(CountView, len(view))
	for key, count := range view {
		result[key] = count
	}
	return result
}
