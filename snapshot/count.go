package snapshot

// checkDeclaredCount compares the section's declared record count with the
// number of records actually parsed. It is only invoked for sections that
// scanned cleanly: once structural corruption truncates a section, the
// "actual" count is unknowable (the remaining bytes cannot be parsed), so
// the count check would be meaningless — the structural diagnostic, which
// has higher priority, already covers that section. This is the fixed
// priority resolving a genuine ambiguity, not one category hiding another.
func checkDeclaredCount(t SectionType, declared, actual int) *Diagnostic {
	switch {
	case declared > actual:
		return &Diagnostic{
			Category:    CatCountMismatch,
			Section:     t,
			RecordIndex: -1,
			Direction:   DeclaredGreaterThanActual,
			Declared:    declared,
			Actual:      actual,
		}
	case declared < actual:
		return &Diagnostic{
			Category:    CatCountMismatch,
			Section:     t,
			RecordIndex: -1,
			Direction:   DeclaredLessThanActual,
			Declared:    declared,
			Actual:      actual,
		}
	default:
		return nil
	}
}
