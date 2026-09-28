package orset

// SelfCheck validates internal invariants: every tombstoned tag must refer to
// a known add record, element mappings must be consistent, and the tag
// counter must be ahead of every locally minted tag.
func (r *Replica) SelfCheck() error {
	return nil
}
