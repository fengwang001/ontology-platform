package semver

// Compare 按 SemVer 2.0.0 优先级比较两个版本，返回 -1/0/1。
// build metadata 不参与比较。
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
	return comparePre(a.Pre, b.Pre)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func comparePre(a, b []PreIdent) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	// 有预发布的版本低于同三元组下无预发布的版本。
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := compareIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmpUint(uint64(len(a)), uint64(len(b)))
}

func compareIdent(a, b PreIdent) int {
	switch {
	case a.Numeric && b.Numeric:
		return cmpUint(a.Num, b.Num)
	case a.Numeric:
		// 数字标识符永远低于含字母的标识符。
		return -1
	case b.Numeric:
		return 1
	}
	return compareASCII(a.Text, b.Text)
}

func compareASCII(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return cmpUint(uint64(len(a)), uint64(len(b)))
}

// Equal 报告两个版本优先级是否相同（忽略 build metadata）。
func Equal(a, b Version) bool { return Compare(a, b) == 0 }
