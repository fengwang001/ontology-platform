package audit

import (
	"errors"
	"fmt"

	"ontology/handle"
	"ontology/slot"
	"ontology/table"
)

var ErrInconsistent = errors.New("audit: table state is inconsistent")

// Check verifies the table invariants using handles claimed to be live.
func Check[T any](t *table.Table[T], live []handle.Handle) error {
	snap := t.Snapshot()
	if snap.Live+snap.FreeCount+snap.Exhausted != len(snap.Slots) {
		return fmt.Errorf("%w: partition count", ErrInconsistent)
	}
	if snap.Live != len(live) {
		return fmt.Errorf("%w: live count", ErrInconsistent)
	}
	seenFree := make([]bool, len(snap.Slots))
	for _, index := range snap.Free {
		if index < 0 || index >= len(snap.Slots) || seenFree[index] {
			return fmt.Errorf("%w: invalid free list", ErrInconsistent)
		}
		if snap.Slots[index].State != slot.Free {
			return fmt.Errorf("%w: free list contains non-free slot", ErrInconsistent)
		}
		seenFree[index] = true
	}
	seenLive := make([]bool, len(snap.Slots))
	for _, h := range live {
		tableID, index, generation := h.Decode()
		if h.IsZero() || tableID != snap.TableID || index >= uint64(len(snap.Slots)) ||
			seenLive[index] || snap.Slots[index].State != slot.Live ||
			snap.Slots[index].Generation != generation {
			return fmt.Errorf("%w: handle does not match a live slot", ErrInconsistent)
		}
		seenLive[index] = true
	}
	for index, info := range snap.Slots {
		switch info.State {
		case slot.Live:
			if !seenLive[index] {
				return fmt.Errorf("%w: live slot without handle", ErrInconsistent)
			}
		case slot.Free:
			if !seenFree[index] {
				return fmt.Errorf("%w: free slot missing from list", ErrInconsistent)
			}
		case slot.Exhausted:
		default:
			return fmt.Errorf("%w: unknown state", ErrInconsistent)
		}
	}
	return nil
}
