package reconcile

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// quarantine is the component responsible for isolating structurally
// corrupted snapshots. A replica whose snapshot fails to decode or fails
// structural validation is excluded from content arbitration; the rest of
// the replicas still reconcile normally.

// decodeSnapshot parses and structurally validates one serialized snapshot.
// Any failure — malformed JSON, missing identity, or internally
// inconsistent positions — marks the whole replica as corrupted: partial
// damage is never trusted, so the replica is quarantined as a unit.
func decodeSnapshot(raw []byte) (*Snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s Snapshot
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("undecodable snapshot: %w", err)
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after snapshot")
	}
	if s.Replica.ID == "" {
		return nil, fmt.Errorf("missing replica id")
	}
	for objID, obj := range s.Objects {
		if objID == "" {
			return nil, fmt.Errorf("empty object id")
		}
		for prop, vv := range obj.Props {
			if prop == "" {
				return nil, fmt.Errorf("object %q: empty property key", objID)
			}
			if vv.WrittenAt > s.Pos {
				return nil, fmt.Errorf(
					"object %q prop %q: write position %d exceeds snapshot position %d",
					objID, prop, vv.WrittenAt, s.Pos)
			}
		}
	}
	return &s, nil
}
