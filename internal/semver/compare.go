package semver

// Compare 按 SemVer 2.0.0 优先级规则比较两个版本：
// a < b 返回负数，a == b 返回 0，a > b 返回正数。
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
	}
	return 0
}

// comparePre 比较预发布标识符序列。
// 有预发布的版本低于没有预发布的同三元组版本。
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePreIdent(a[i], b[i]); c != 0 {
			return c
		}
	}
	// 前缀全部相等时，段数多的更大。
	return cmpInt(len(a), len(b))
}

// comparePreIdent 比较单个预发布标识符：
// 两边都是纯数字按数值比较；数字段永远低于含字母的段；
// 否则按 ASCII 字典序比较。
func comparePreIdent(a, b string) int {
	aNum, bNum := isDigits(a), isDigits(b)
	switch {
	case aNum && bNum:
		// 纯数字段没有前导零，先比长度再比字典序即数值序。
		if c := cmpInt(len(a), len(b)); c != 0 {
			return c
		}
		return cmpStr(a, b)
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return cmpStr(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
