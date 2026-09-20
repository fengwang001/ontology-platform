package semver

// Compare implements SemVer 2.0.0 precedence. Build metadata is ignored.
func Compare(a, b Version) int {
	if c := cmpUint(a.Major, b.Major); c != 0 {
		return c
	}
	if c := cmpUint(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := cmpUint(a.Patch, b.Patch); c != 0 {
		return c
	}
	switch {
	case len(a.Pre) == 0 && len(b.Pre) == 0:
		return 0
	case len(a.Pre) == 0:
		return 1 // release outranks prerelease
	case len(b.Pre) == 0:
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := cmpIdent(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}
	return cmpUint(uint64(len(a.Pre)), uint64(len(b.Pre)))
}

// Less reports whether a has lower precedence than b.
func Less(a, b Version) bool { return Compare(a, b) < 0 }

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpIdent compares two prerelease identifiers: numeric identifiers compare
// numerically and always rank below alphanumeric ones, which compare in
// ASCII lexicographical order.
func cmpIdent(a, b string) int {
	aNum, bNum := isDigits(a), isDigits(b)
	switch {
	case aNum && bNum:
		// No leading zeros, so longer means larger.
		if c := cmpUint(uint64(len(a)), uint64(len(b))); c != 0 {
			return c
		}
		return cmpString(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return cmpString(a, b)
}

func cmpString(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
