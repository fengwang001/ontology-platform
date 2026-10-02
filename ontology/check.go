package ontology

// checkPath 按固定次序检验一条结构路径，返回首个违规；全部通过返回 nil。
func checkPath(path []*cert, name string, now int64) *VerifyFailure {
	n := len(path) - 1

	// 一：先逐个证书查有效期，再逐个查吊销。
	for i := 0; i <= n; i++ {
		c := path[i]
		if now < c.data.NotBefore || now >= c.data.NotAfter {
			return &VerifyFailure{Reason: FailExpired, Index: i}
		}
	}
	for i := 0; i <= n; i++ {
		c := path[i]
		if c.revokedAt >= 0 && now >= c.revokedAt {
			return &VerifyFailure{Reason: FailRevoked, Index: i}
		}
	}

	// 二：下标 1..n 必须为 CA。
	for i := 1; i <= n; i++ {
		if !path[i].data.IsCA {
			return &VerifyFailure{Reason: FailNotCA, Index: i}
		}
	}

	// 三：PathLen 约束，m 只统计下标 1..i-1 中的非自签发证书。
	for i := 1; i <= n; i++ {
		pl := path[i].data.PathLen
		if pl < 0 {
			continue
		}
		var m int64
		for j := 1; j < i; j++ {
			c := path[j]
			if string(c.data.Subject) != string(c.data.Issuer) {
				m++
			}
		}
		if m > pl {
			return &VerifyFailure{Reason: FailPathLen, Index: i}
		}
	}

	// 四：下标 1..n 的子树约束，先 Excluded 后 Permitted。
	for i := 1; i <= n; i++ {
		c := path[i]
		for _, s := range c.data.Excluded {
			if matchSubtree(name, s) {
				return &VerifyFailure{Reason: FailExcluded, Index: i}
			}
		}
		if len(c.data.Permitted) > 0 {
			ok := false
			for _, s := range c.data.Permitted {
				if matchSubtree(name, s) {
					ok = true
					break
				}
			}
			if !ok {
				return &VerifyFailure{Reason: FailNotPermitted, Index: i}
			}
		}
	}

	// 五：终端证书 SAN 匹配 name。
	for _, s := range path[0].data.SAN {
		if matchSAN(name, s) {
			return nil
		}
	}
	return &VerifyFailure{Reason: FailSAN, Index: 0}
}

// matchSubtree 判定 name 是否命中子树约束 s。
func matchSubtree(name, s string) bool {
	if len(s) > 0 && s[0] == '.' {
		return len(name) > len(s) && name[len(name)-len(s):] == s
	}
	if name == s {
		return true
	}
	suffix := "." + s
	return len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix
}

// matchSAN 判定 name 是否被一条 SAN 匹配。
func matchSAN(name, san string) bool {
	if len(san) >= 2 && san[0] == '*' && san[1] == '.' {
		suffix := san[1:] // ".s"
		if len(name) <= len(suffix) || name[len(name)-len(suffix):] != suffix {
			return false
		}
		left := name[:len(name)-len(suffix)]
		if len(left) == 0 {
			return false
		}
		for i := 0; i < len(left); i++ {
			if left[i] == '.' {
				return false
			}
		}
		return true
	}
	return name == san
}
