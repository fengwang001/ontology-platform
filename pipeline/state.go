package pipeline

// Phase is a checkpointed state-machine phase.
type Phase string

const (
	// PhaseIngest: accepting records and spilling under pressure.
	PhaseIngest Phase = "ingest"
	// PhaseSpill: flush of the final resident batch after Close.
	PhaseSpill Phase = "spill"
	// PhaseMerge: producing the sorted output file from all runs.
	PhaseMerge Phase = "merge"
	// PhaseFinalize: output file complete; nothing left to do.
	PhaseFinalize Phase = "finalize"
)

// checkpoint is the on-disk recovery state, atomically rewritten at
// every phase boundary.
type checkpoint struct {
	Phase         Phase  `json:"phase"`
	Accepted      uint64 `json:"accepted"`       // gap-free accepted record count
	Runs          uint64 `json:"runs"`           // completed spill runs on disk
	PendingSpill  bool   `json:"pending_spill"`  // entered PhaseSpill boundary
	MergedRecords uint64 `json:"merged_records"` // produced output records (merge progress is atomic anyway)
	Comparisons   uint64 `json:"comparisons"`
	PeakResident  uint64 `json:"peak_resident"`
}
