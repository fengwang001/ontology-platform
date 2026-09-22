package merge

import "ontology/record"

// Source is one sorted input to the K-way merge. Within a source the
// records must be ordered by record.Less. RunID is the 1-based creation
// order of the spill run; the final in-memory batch is presented with a
// RunID strictly larger than every spilled run.
type Source struct {
	RunID   uint64
	Records []record.Record
}

// head returns the current key candidate for tie-break derivation.
// Each source owns a local, dense ordinal for equal keys implicitly via
// its record order; the merge never needs to read it explicitly because
// (RunID, position within source) is the correct global tie-break:
//
//	records of a key are disjoint across runs (each ingest lands in
//	exactly one run), and run creation order equals arrival order, so
//	within one key run r's records all arrived before run r+1's.
