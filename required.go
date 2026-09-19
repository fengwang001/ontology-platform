package ontology

// ValidateRequired checks requiredness of every declared link type in one
// pass and reports ALL missing required relations at once. CreateLink never
// enforces requiredness; it is only validated here ("at commit time").
func (s *Store) ValidateRequired() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var missing []MissingRequired
	for _, name := range sortedLinkTypeNames(s.st) {
		lt := s.st.linkTypes[name]
		if lt.SourceRequired {
			for _, id := range objectsOfType(s.st, lt.Source) {
				if len(s.st.fwd[name][id]) == 0 {
					missing = append(missing, MissingRequired{id, name, "source"})
				}
			}
		}
		if lt.TargetRequired {
			for _, id := range objectsOfType(s.st, lt.Target) {
				if len(s.st.rev[name][id]) == 0 {
					missing = append(missing, MissingRequired{id, name, "target"})
				}
			}
		}
	}
	if len(missing) > 0 {
		return &RequiredError{Missing: missing}
	}
	return nil
}
