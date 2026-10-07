package orphan

// LogEntry records the inputs, outputs and the inbound-edge combination one
// decision was based on. Entries are emitted for every retention evaluation
// and every generation advancement (promotion / cleanup).
type LogEntry struct {
	Op                string         // "evaluate" | "promote" | "cleanup_begin" | "cleanup_end"
	TimeMs            int64          // input: logical decision time
	Object            string         // input: target object id
	PresentTypes      []string       // input: link types with >=1 inbound edge
	PresentCounts     map[string]int // input: inbound edge count per type
	IndependentChecks int            // layer-1 presence probes performed
	JointGroupsTried  int            // layer-2 groups probed
	JointMemberChecks int            // layer-2 member-presence probes performed
	Reason            string         // output explanation
	Retained          bool           // output of the retention judgment
	FromGen           Generation     // queue membership before transition
	ToGen             Generation     // queue membership after transition
	SinceMs           int64          // recorded judgment time after transition
}
