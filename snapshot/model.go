// Package snapshot implements chunked ontology snapshot export, integrity
// verification and cross-type aggregate loading.
package snapshot

// Link is a cross-type reference embedded in a Record.
type Link struct {
	Field  string `json:"field"`
	Target string `json:"target_type"`
	ID     string `json:"id"`
}

// Record is a single ontology object inside a chunk.
type Record struct {
	ID    string            `json:"id"`
	Props map[string]string `json:"props,omitempty"`
	Links []Link            `json:"links,omitempty"`
}

// Block status values, assigned strictly by verification order.
const (
	StatusOK             = "ok"
	StatusChecksumFailed = "checksum_failed"
	StatusCountMismatch  = "count_mismatch"
)

// Reference verdict values.
const (
	RefResolved        = "resolved"
	RefDangling        = "dangling"
	RefTargetUntrusted = "target_untrusted"
)
