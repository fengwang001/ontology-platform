// Package undo reverses a rename batch from its log, in reverse order.
package undo

import (
	"errors"
	"os"

	"ontology/apply"
	"ontology/name"
)

// Undo reverses the batch recorded in logPath. It applies the maximal
// recoverable prefix of the log in reverse order and returns the indices
// of steps that could not be undone (missing tail of a truncated log or
// failed renames). A successful undo renames the log to logPath+".done";
// undoing an already undone log is an idempotent no-op.
func Undo(ns *name.Set, logPath string) (unrecovered []int, err error) {
	if _, statErr := os.Stat(logPath + ".done"); statErr == nil {
		return nil, nil
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return nil, err
	}
	steps, total, perr := apply.ParseLog(data)
	if errors.Is(perr, apply.ErrHeaderIncomplete) {
		return nil, perr
	}
	ns.Lock()
	defer ns.Unlock()
	for i := len(steps) - 1; i >= 0; i-- {
		if rerr := ns.RenameLocked(steps[i].To, steps[i].From); rerr != nil {
			unrecovered = append(unrecovered, i)
		}
	}
	for i := len(steps); i < total; i++ {
		unrecovered = append(unrecovered, i)
	}
	if perr != nil {
		return unrecovered, perr
	}
	if len(unrecovered) > 0 {
		return unrecovered, nil
	}
	if err := os.Rename(logPath, logPath+".done"); err != nil {
		return nil, err
	}
	return nil, nil
}
