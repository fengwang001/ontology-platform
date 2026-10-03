package notify

import (
	"strings"
	"unicode/utf8"
)

const maxEff = 1_000_000_000_000
const maxBodyBytes = 1000

var concreteChannels = map[string]bool{"email": true, "sms": true, "push": true}

func validName(name string) bool {
	if len(name) < 1 || len(name) > 32 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '.' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validVarName(v string) bool {
	if len(v) < 1 || len(v) > 32 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validChannelKey(ch string) bool {
	return ch == "*" || concreteChannels[ch]
}

func isLowerLetter(c byte) bool { return c >= 'a' && c <= 'z' }
func isUpperLetter(c byte) bool { return c >= 'A' && c <= 'Z' }

// validLang 按区分大小写的子标签规则校验语言标签。
func validLang(loc string) bool {
	subs := strings.Split(loc, "-")
	if len(subs) < 1 || len(subs) > 3 {
		return false
	}
	primary := subs[0]
	if len(primary) != 2 && len(primary) != 3 {
		return false
	}
	for i := 0; i < len(primary); i++ {
		if !isLowerLetter(primary[i]) {
			return false
		}
	}
	if len(subs) == 1 {
		return true
	}
	second := subs[1]
	isScript := len(second) == 4 && isUpperLetter(second[0]) &&
		isLowerLetter(second[1]) && isLowerLetter(second[2]) && isLowerLetter(second[3])
	isRegion := len(second) == 2 && isUpperLetter(second[0]) && isUpperLetter(second[1])
	if !isScript && !isRegion {
		return false
	}
	if len(subs) == 2 {
		return true
	}
	// 第三个子标签只允许跟在文字（script）之后。
	if !isScript {
		return false
	}
	region := subs[2]
	if len(region) != 2 || !isUpperLetter(region[0]) || !isUpperLetter(region[1]) {
		return false
	}
	return true
}

// fallbackChain 返回语言自身、逐级去尾，最后追加去重后的默认语言。
func fallbackChain(loc, dl string) []string {
	subs := strings.Split(loc, "-")
	chain := make([]string, 0, len(subs)+1)
	for n := len(subs); n >= 1; n-- {
		chain = append(chain, strings.Join(subs[:n], "-"))
	}
	for _, l := range chain {
		if l == dl {
			return chain
		}
	}
	return append(chain, dl)
}

func validBody(b string) bool {
	return len(b) > 0 && len(b) <= maxBodyBytes && utf8.ValidString(b)
}
