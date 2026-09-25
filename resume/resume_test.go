package resume

import (
	"testing"
	"time"

	"ontology/chunker"
)

func TestValidate(t *testing.T) {
	chunks := func(n ...int) []chunker.Chunk {
		out := make([]chunker.Chunk, len(n))
		for i, k := range n {
			out[i] = chunker.Chunk{Data: make([]byte, k)}
		}
		return out
	}
	cases := []struct {
		name string
		st   State
		ok   bool
	}{
		{"empty", State{}, true},
		{"identity-ok", State{Accepted: 10, Confirmed: 4, Queue: chunks(6)}, true},
		{"identity-ok-with-pend", State{Accepted: 10, Confirmed: 4, Queue: chunks(4), Pend: make([]byte, 2), Open: true}, true},
		{"negative", State{Accepted: -1}, false},
		{"accepted-lt-confirmed", State{Accepted: 1, Confirmed: 2}, false},
		{"identity-broken", State{Accepted: 10, Confirmed: 4, Queue: chunks(5)}, false},
		{"empty-chunk", State{Accepted: 1, Confirmed: 0, Queue: chunks(0)}, false},
		{"negative-frontoff", State{FrontOff: -1}, false},
		{"pend-without-open", State{Pend: make([]byte, 1), Open: false}, false},
		{"start-timestamp", State{Start: time.Unix(1, 0)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.st.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
