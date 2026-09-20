package semver

// Compare 按 SemVer 2.0.0 优先级比较两个版本：
// 返回 -1 / 0 / 1。build metadata 不参与比较。
func Compare(a, b Version) int {
	if c := compareInt(a.Major, b.Major); c != 0 {
		return c
	}
	if c := compareInt(a.Minor, b.Minor); c != 0 {
		return c
	}
	if c := compareInt(a.Patch, b.Patch); c != 0 {
		return c
	}
	return comparePrerelease(a.Pre, a.hasPre, b.Pre, b.hasPre)
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparePrerelease 实现预发布优先级规则。
func comparePrerelease(a []string, aHas bool, b []string, bHas bool) int {
	switch {
	case !aHas && !bHas:
		return 0
	case !aHas:
		return 1 // 有预发布的版本更低
	case !bHas:
		return -1
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := compareIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	return compareInt(len(a), len(b))
}

// compareIdentifier 比较单个预发布标识符：
// 数字与数字按数值；数字段低于含字母的段；其余按 ASCII 字典序。
func compareIdentifier(a, b string) int {
	aNum, bNum := isAllDigits(a), isAllDigits(b)
	switch {
	case aNum && bNum:
		return compareNumericStrings(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	default:
		return compareASCII(a, b)
	}
}

// compareNumericStrings 按数值比较两个纯数字字符串（无溢出风险）。
func compareNumericStrings(a, b string) int {
	// 两者均无前导零（Parse 保证），长度长者数值更大。
	if c := compareInt(len(a), len(b)); c != 0 {
		return c
	}
	return compareASCII(a, b)
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
	return compareInt(len(a), len(b))
}
