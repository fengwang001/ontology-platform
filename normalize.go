package unique

// NormalizeOptions controls key normalization.
type NormalizeOptions struct {
	TrimSpace bool
	CaseFold  bool
}

func normalize(value string, opts NormalizeOptions) string {
	_ = value
	_ = opts
	return value
}
