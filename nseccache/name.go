package nseccache

import "fmt"

// parseName 将域名拆为小写标签序列（右起顺序即 labels 的逆序）。
// 标签为 1..63 字节，只允许字母数字与 - _ *；整体不超过 253 字符。
// 允许单个末尾 "."。
func parseName(s string) ([]string, error) {
	if len(s) == 0 || len(s) > 253 {
		return nil, fmt.Errorf("nseccache: bad name %q", s)
	}
	root := s[len(s)-1] == '.'
	if root {
		s = s[:len(s)-1]
	}
	if len(s) == 0 {
		return nil, fmt.Errorf("nseccache: empty name")
	}
	if len(s) > 253 {
		return nil, fmt.Errorf("nseccache: name too long %q", s)
	}
	var labels []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			label := s[start:i]
			if len(label) == 0 || len(label) > 63 {
				return nil, fmt.Errorf("nseccache: bad label in %q", s)
			}
			lowered := make([]byte, len(label))
			for j := 0; j < len(label); j++ {
				ch := label[j]
				switch {
				case ch >= 'A' && ch <= 'Z':
					lowered[j] = ch + ('a' - 'A')
				case ch >= 'a' && ch <= 'z',
					ch >= '0' && ch <= '9',
					ch == '-' || ch == '_' || ch == '*':
					lowered[j] = ch
				default:
					return nil, fmt.Errorf("nseccache: bad label byte in %q", s)
				}
			}
			labels = append(labels, string(lowered))
			start = i + 1
		}
	}
	return labels, nil
}

// canonName 保存一个已验证域名的规范形式。
// labels 为左起顺序；比较与后缀操作均从右起进行。
type canonName struct {
	text   string
	labels []string
}

func newCanonName(s string) (canonName, error) {
	labels, err := parseName(s)
	if err != nil {
		return canonName{}, err
	}
	return canonName{text: joinLabels(labels), labels: labels}, nil
}

func joinLabels(labels []string) string {
	out := ""
	for i, l := range labels {
		if i > 0 {
			out += "."
		}
		out += l
	}
	return out
}

func (n canonName) String() string {
	return n.text
}

// labelAt 返回右起第 idx 个标签（idx=0 为最右标签），不存在返回 ("",false)。
func (n canonName) labelAt(idx int) (string, bool) {
	i := len(n.labels) - 1 - idx
	if i < 0 {
		return "", false
	}
	return n.labels[i], true
}

// compareCanon 按规范序比较：从最右标签起逐字节比较；
// 较短且为前缀者更小；所有标签相同则标签更少（祖先）更小。
func compareCanon(a, b canonName) int {
	depth := len(a.labels)
	if len(b.labels) < depth {
		depth = len(b.labels)
	}
	for idx := 0; idx < depth; idx++ {
		la := a.labels[len(a.labels)-1-idx]
		lb := b.labels[len(b.labels)-1-idx]
		if c := compareLabel(la, lb); c != 0 {
			return c
		}
	}
	switch {
	case len(a.labels) < len(b.labels):
		return -1
	case len(a.labels) > len(b.labels):
		return 1
	default:
		return 0
	}
}

// compareLabel 小写字节逐字节比较，较短且为前缀者更小。
func compareLabel(a, b string) int {
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
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

// isInZone 报告 n 是否等于 apex 或为其后代。
func isInZone(n, apex canonName) bool {
	if len(n.labels) < len(apex.labels) {
		return false
	}
	for i := 0; i < len(apex.labels); i++ {
		if n.labels[len(n.labels)-1-i] != apex.labels[len(apex.labels)-1-i] {
			return false
		}
	}
	return true
}

// commonSuffix 返回两个名字公共后缀标签的数量（不计入 nameCmp）。
func commonSuffix(a, b canonName) int {
	k := 0
	for {
		la, oka := a.labelAt(k)
		lb, okb := b.labelAt(k)
		if !oka || !okb || la != lb {
			return k
		}
		k++
	}
}

// suffixName 返回 n 的右起 k 个标签组成的名字。
func suffixName(n canonName, k int) canonName {
	if k <= 0 {
		return canonName{}
	}
	if k > len(n.labels) {
		k = len(n.labels)
	}
	labels := n.labels[len(n.labels)-k:]
	return canonName{text: joinLabels(labels), labels: labels}
}

// wildcardName 返回 n 之下的通配符名（"*." + n）。
func wildcardName(n canonName) canonName {
	labels := make([]string, 0, len(n.labels)+1)
	labels = append(labels, "*")
	labels = append(labels, n.labels...)
	return canonName{text: joinLabels(labels), labels: labels}
}
