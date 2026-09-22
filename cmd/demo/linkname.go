package main

import (
	_ "unsafe"

	"ontology/saga"
)

//go:linkname resumeReads ontology/saga.intrnlResumeReadCount
func resumeReads(o *saga.Orchestrator) int
