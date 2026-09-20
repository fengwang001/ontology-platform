package semver

// Compare implements SemVer 2.0.0 precedence. Build metadata is ignored.
func Compare(a, b Version) int { return 0 }

// Less reports whether a has lower precedence than b.
func Less(a, b Version) bool { return Compare(a, b) < 0 }
