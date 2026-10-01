package release

import (
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveVersion 是朴素实现使用的独立数据结构，刻意与生产代码零共享逻辑。
type naiveVersion struct {
	m, n, p int
	channel string
	num     int
}

func naiveParse(s string) (naiveVersion, bool) {
	var v naiveVersion
	corePart := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		corePart = s[:i]
		suffix := s[i+1:]
		dot := strings.IndexByte(suffix, '.')
		if dot < 0 {
			return v, false
		}
		var chName, suffixNum string
		chName, suffixNum = suffix[:dot], suffix[dot+1:]
		v.channel = chName
		if l := len(v.channel); l < 1 || l > 16 {
			return v, false
		}
		for i := 0; i < len(v.channel); i++ {
			if v.channel[i] < 'a' || v.channel[i] > 'z' {
				return v, false
			}
		}
		if strings.IndexByte(suffixNum, '.') >= 0 {
			return v, false
		}
		n, ok := decNum(suffixNum)
		if !ok || n < 1 {
			return v, false
		}
		v.num = n
	}
	nums := strings.Split(corePart, ".")
	if len(nums) != 3 {
		return v, false
	}
	parsed := [3]int{}
	for i, x := range nums {
		n, ok := decNum(x)
		if !ok {
			return v, false
		}
		parsed[i] = n
	}
	v.m, v.n, v.p = parsed[0], parsed[1], parsed[2]
	return v, true
}

// decNum 接受无前导零、范围 [0, 999999] 的十进制串（0 本身合法）。
func decNum(s string) (int, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	return n, n <= 999999
}

func (v naiveVersion) key() string {
	if v.channel == "" {
		return fmt.Sprintf("%d.%d.%d", v.m, v.n, v.p)
	}
	return fmt.Sprintf("%d.%d.%d-%s.%d", v.m, v.n, v.p, v.channel, v.num)
}

// naiveLess 按题目排序规则比较。
func naiveLess(a, b naiveVersion) bool {
	if a.m != b.m {
		return a.m < b.m
	}
	if a.n != b.n {
		return a.n < b.n
	}
	if a.p != b.p {
		return a.p < b.p
	}
	// 同核心：预发布 < 稳定。
	if (a.channel == "") != (b.channel == "") {
		return a.channel != ""
	}
	if a.channel != b.channel {
		return a.channel < b.channel
	}
	return a.num < b.num
}

type naiveReleaser struct {
	pub []naiveVersion
}

func (nr *naiveReleaser) tag(s string) (string, Reason) {
	v, ok := naiveParse(s)
	if !ok {
		return "", ErrInvalidVersion
	}
	for _, x := range nr.pub {
		if x.key() == v.key() {
			return "", ErrAlreadyTagged
		}
	}
	nr.pub = append(nr.pub, v)
	return v.key(), ""
}

func (nr *naiveReleaser) list() []string {
	cp := append([]naiveVersion(nil), nr.pub...)
	sort.SliceStable(cp, func(i, j int) bool { return naiveLess(cp[i], cp[j]) })
	out := make([]string, len(cp))
	for i, v := range cp {
		out[i] = v.key()
	}
	return out
}

func validChannelNaive(c string) bool {
	if len(c) < 1 || len(c) > 16 {
		return false
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 'a' || c[i] > 'z' {
			return false
		}
	}
	return true
}

func validTypeNaive(t string) bool {
	if t == "" {
		return false
	}
	for i := 0; i < len(t); i++ {
		if t[i] < 'a' || t[i] > 'z' {
			return false
		}
	}
	return true
}

// derive 朴素重放规则，返回 (结果串或空, 原因或空, 判定依据)。
func (nr *naiveReleaser) derive(commits []Commit, channel string) (string, Reason, string) {
	if channel != "" && !validChannelNaive(channel) {
		return "", ErrInvalidChannel, "channel invalid: " + channel
	}
	for i, c := range commits {
		if !validTypeNaive(c.Type) {
			return "", ErrInvalidCommitType, fmt.Sprintf("commits[%d] invalid type %q", i, c.Type)
		}
	}

	// S：最大稳定版本。
	sm, sn, sp := 0, 0, 0
	foundStable := false
	for _, v := range nr.pub {
		if v.channel == "" {
			c := [3]int{v.m, v.n, v.p}
			best := [3]int{sm, sn, sp}
			if !foundStable || c[0] > best[0] ||
				(c[0] == best[0] && c[1] > best[1]) ||
				(c[0] == best[0] && c[1] == best[1] && c[2] > best[2]) {
				sm, sn, sp = v.m, v.n, v.p
				foundStable = true
			}
		}
	}

	// P：核心 > S 的预发布版本中的最大核心。
	pm, pn, pp := 0, 0, 0
	hasP := false
	for _, v := range nr.pub {
		if v.channel == "" {
			continue
		}
		gt := v.m > sm ||
			(v.m == sm && v.n > sn) ||
			(v.m == sm && v.n == sn && v.p > sp)
		if !gt {
			continue
		}
		c := [3]int{v.m, v.n, v.p}
		best := [3]int{pm, pn, pp}
		if !hasP || c[0] > best[0] ||
			(c[0] == best[0] && c[1] > best[1]) ||
			(c[0] == best[0] && c[1] == best[1] && c[2] > best[2]) {
			pm, pn, pp = v.m, v.n, v.p
			hasP = true
		}
	}

	// L：所有提交级别的最大值。
	bestLevel := 0 // 0 none,1 patch,2 minor,3 major
	for _, c := range commits {
		lvl := 0
		switch {
		case c.Breaking && sm >= 1:
			lvl = 3
		case c.Breaking:
			lvl = 2
		case c.Type == "feat" && sm >= 1:
			lvl = 2
		case c.Type == "feat":
			lvl = 1
		case c.Type == "fix" || c.Type == "perf":
			lvl = 1
		}
		if lvl > bestLevel {
			bestLevel = lvl
		}
	}

	hasT := bestLevel > 0
	tm, tn, tp := sm, sn, sp
	switch bestLevel {
	case 1:
		tp = sp + 1
	case 2:
		tn, tp = sn+1, 0
	case 3:
		tm, tn, tp = sm+1, 0, 0
	}

	// K：T 与 P 取大者。
	km, kn, kp := 0, 0, 0
	switch {
	case hasT && hasP:
		tb, pb := [3]int{tm, tn, tp}, [3]int{pm, pn, pp}
		if tb[0] > pb[0] || (tb[0] == pb[0] && tb[1] > pb[1]) ||
			(tb[0] == pb[0] && tb[1] == pb[1] && tb[2] >= pb[2]) {
			km, kn, kp = tm, tn, tp
		} else {
			km, kn, kp = pm, pn, pp
		}
	case hasT:
		km, kn, kp = tm, tn, tp
	case hasP:
		km, kn, kp = pm, pn, pp
	default:
		return "", ErrNoRelease, fmt.Sprintf("S=%d.%d.%d, L=none, no P", sm, sn, sp)
	}

	if km > 999999 || kn > 999999 || kp > 999999 {
		return "", ErrOverflow,
			fmt.Sprintf("S=%d.%d.%d L=%d T=%d.%d.%d P-exists=%v K=%d.%d.%d overflow",
				sm, sn, sp, bestLevel, tm, tn, tp, hasP, km, kn, kp)
	}

	coreStr := fmt.Sprintf("%d.%d.%d", km, kn, kp)
	if channel == "" {
		return coreStr, "",
			fmt.Sprintf("S=%d.%d.%d L=%d T=%d.%d.%d P-exists=%v K=%s stable",
				sm, sn, sp, bestLevel, tm, tn, tp, hasP, coreStr)
	}
	maxN := 0
	for _, v := range nr.pub {
		if v.channel == channel && v.m == km && v.n == kn && v.p == kp && v.num > maxN {
			maxN = v.num
		}
	}
	nextN := maxN + 1
	if nextN > 999999 {
		return "", ErrOverflow,
			fmt.Sprintf("K=%s channel=%s maxN=%d N=%d overflow", coreStr, channel, maxN, nextN)
	}
	return fmt.Sprintf("%s-%s.%d", coreStr, channel, nextN), "",
		fmt.Sprintf("S=%d.%d.%d L=%d T=%d.%d.%d P-exists=%v K=%s channel=%s maxN=%d N=%d",
			sm, sn, sp, bestLevel, tm, tn, tp, hasP, coreStr, channel, maxN, nextN)
}

func (nr *naiveReleaser) release(commits []Commit, channel string) (string, Reason, string) {
	s, reason, why := nr.derive(commits, channel)
	if reason != "" {
		return "", reason, why
	}
	v, ok := naiveParse(s)
	if !ok {
		panic("naive produced invalid version: " + s)
	}
	nr.pub = append(nr.pub, v)
	return s, "", why
}

var rngChannels = []string{"", "rc", "beta", "alpha", "z", "dev", "x"}
var rngTypes = []string{"feat", "fix", "perf", "docs", "style", "chore", "ci", "test"}

func randomVersion(rng *rand.Rand) string {
	// 核心取小值以便制造升级、预发布线与少量溢出边界。
	m := rng.Intn(4)
	n := rng.Intn(4)
	p := rng.Intn(4)
	if rng.Intn(20) == 0 {
		p = 999999
	}
	if rng.Intn(3) == 0 {
		ch := rngChannels[1+rng.Intn(len(rngChannels)-1)]
		num := 1 + rng.Intn(3)
		if rng.Intn(30) == 0 {
			num = 999999
		}
		return fmt.Sprintf("%d.%d.%d-%s.%d", m, n, p, ch, num)
	}
	return fmt.Sprintf("%d.%d.%d", m, n, p)
}

func randomCommits(rng *rand.Rand) []Commit {
	n := rng.Intn(5)
	cs := make([]Commit, n)
	for i := range cs {
		t := rngTypes[rng.Intn(len(rngTypes))]
		if rng.Intn(12) == 0 {
			t = []string{"", "Feat", "2fix", "a-b"}[rng.Intn(4)]
		}
		cs[i] = Commit{Type: t, Breaking: rng.Intn(4) == 0}
	}
	return cs
}

func randomChannel(rng *rand.Rand) string {
	if rng.Intn(10) == 0 {
		return []string{"RC", "1", "a b", "", "toolongchannelname"}[rng.Intn(5)]
	}
	return rngChannels[rng.Intn(len(rngChannels))]
}

func commitsLog(cs []Commit) string {
	parts := make([]string, len(cs))
	for i, c := range cs {
		if c.Breaking {
			parts[i] = c.Type + "!"
		} else {
			parts[i] = c.Type
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	const cases = 2000
	for tc := 0; tc < cases; tc++ {
		real := New()
		naive := &naiveReleaser{}

		var logb strings.Builder
		fmt.Fprintf(&logb, "case %d\n", tc)

		// 随机初始已发布集合（含非法/重复 Tag 的尝试）。
		for i, k := 0, rng.Intn(8); i < k; i++ {
			var s string
			if rng.Intn(15) == 0 {
				s = []string{"01.0.0", "1.2", "1.2.3-X.1", "1.2.3-rc.0"}[rng.Intn(4)]
			} else {
				s = randomVersion(rng)
			}
			realErr := real.Tag(s)
			_, naiveReason := naive.tag(s)
			fmt.Fprintf(&logb, "  tag %-24s -> real=%-28q naive=%s\n",
				s, reasonStr(realErr), naiveReason)
			if reasonStr(realErr) != string(naiveReason) {
				t.Fatalf("case %d tag mismatch on %q:\n%s", tc, s, logb.String())
			}
		}

		if got, want := strings.Join(versionsStr(real), ","), strings.Join(naive.list(), ","); got != want {
			t.Fatalf("case %d Versions mismatch:\nreal=%s\nnaive=%s\n%s", tc, got, want, logb.String())
		}

		// 随机推导/发布序列。
		for step, maxStep := 0, 3+rng.Intn(5); step < maxStep; step++ {
			cs := randomCommits(rng)
			ch := randomChannel(rng)

			// Next 对拍（不改变任何状态）。
			rv, rerr := real.Next(cs, ch)
			nv, nreason, why := naive.derive(cs, ch)
			fmt.Fprintf(&logb, "  next ch=%-5q commits=%-30s -> real=%-24s naive=%-24s | %s\n",
				ch, commitsLog(cs), resultStr(rv, rerr), nv+reasonTag(nreason), why)
			if resultStr(rv, rerr) != nv+reasonTag(nreason) {
				t.Fatalf("case %d next mismatch:\n%s", tc, logb.String())
			}

			// Release 对拍（各自登记，成功后集合应一致）。
			rv2, rerr2 := real.Release(cs, ch)
			nv2, nreason2, why2 := naive.release(cs, ch)
			fmt.Fprintf(&logb, "  rel  ch=%-5q commits=%-30s -> real=%-24s naive=%-24s | %s\n",
				ch, commitsLog(cs), resultStr(rv2, rerr2), nv2+reasonTag(nreason2), why2)
			if resultStr(rv2, rerr2) != nv2+reasonTag(nreason2) {
				t.Fatalf("case %d release mismatch:\n%s", tc, logb.String())
			}
			if got, want := strings.Join(versionsStr(real), ","), strings.Join(naive.list(), ","); got != want {
				t.Fatalf("case %d post-release Versions mismatch:\nreal=%s\nnaive=%s\n%s",
					tc, got, want, logb.String())
			}
		}
		log.Print(strings.TrimRight(logb.String(), "\n"))
	}
}

func reasonStr(err error) string {
	if err == nil {
		return ""
	}
	return string(reasonOf(err))
}

func resultStr(v Version, err error) string {
	if err != nil {
		return "ERR:" + reasonStr(err)
	}
	return v.String()
}

func reasonTag(r Reason) string {
	if r == "" {
		return ""
	}
	return "ERR:" + string(r)
}
