package main

import (
	_ "unsafe"

	"ontology/journal"
	"ontology/saga"
	"ontology/step"
)

//go:linkname demoJournal ontology/saga.intrnlJournal
func demoJournal(o *saga.Orchestrator) *journal.Journal

//go:linkname journalAppend ontology/saga.intrnlAppend
func journalAppend(o *saga.Orchestrator, id string, idx int, key string,
	dir journal.Direction, res journal.Result) (journal.Record, error)

//go:linkname registerForDemo ontology/saga.intrnlRegister
func registerForDemo(o *saga.Orchestrator, id string, steps []step.Step)
