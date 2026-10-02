package ontology

// caveatKind classifies a caveat by its prefix.
type caveatKind int

const (
	cavUnknown caveatKind = iota
	cavExp
	cavOps
	cavRes
	cavAmt
)

type parsedCaveat struct {
	kind  caveatKind
	valid bool
	expN  int64 // exp / amt numeric value
	ops   []string
	res   string
}

func parseDecimal(s string, max int64) (int64, bool) {
	if len(s) == 0 {
		return 0, false
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		if n > (max-int64(c-'0'))/10 {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

func validOpsItem(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

// validResPrefix enforces: starts with "/", is exactly "/" or ends with a
// non-slash, contains no "//".
func validResPrefix(p string) bool {
	if len(p) == 0 || p[0] != '/' {
		return false
	}
	if p == "/" {
		return true
	}
	if p[len(p)-1] == '/' {
		return false
	}
	for i := 0; i+1 < len(p); i++ {
		if p[i] == '/' && p[i+1] == '/' {
			return false
		}
	}
	return true
}

func parseCaveat(c []byte) parsedCaveat {
	var p parsedCaveat
	s := string(c)
	colon := -1
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			colon = i
			break
		}
	}
	if colon < 0 {
		return p
	}
	prefix, val := s[:colon], s[colon+1:]
	switch prefix {
	case "exp":
		p.kind = cavExp
		n, ok := parseDecimal(val, 1e15)
		if ok {
			p.valid, p.expN = true, n
		}
	case "amt":
		p.kind = cavAmt
		n, ok := parseDecimal(val, 1_000_000_000_000)
		if ok {
			p.valid, p.expN = true, n
		}
	case "ops":
		p.kind = cavOps
		valid := len(val) > 0
		start := 0
		for i := 0; i <= len(val) && valid; i++ {
			if i < len(val) && val[i] != ',' {
				continue
			}
			if !validOpsItem(val[start:i]) {
				valid = false
				break
			}
			p.ops = append(p.ops, val[start:i])
			start = i + 1
		}
		p.valid = valid
	case "res":
		p.kind = cavRes
		if validResPrefix(val) {
			p.valid, p.res = true, val
		}
	default:
		// unknown prefix
	}
	return p
}

// satisfied assumes p.valid and reports whether the request satisfies it.
func (p parsedCaveat) satisfied(req Request, now int64) bool {
	switch p.kind {
	case cavExp:
		return now < p.expN
	case cavAmt:
		return req.Amount <= p.expN
	case cavOps:
		for _, op := range p.ops {
			if op == req.Op {
				return true
			}
		}
		return false
	case cavRes:
		if p.res == "/" {
			return true // req.Res is guaranteed to start with "/"
		}
		if req.Res == p.res {
			return true
		}
		return len(req.Res) > len(p.res) &&
			req.Res[:len(p.res)] == p.res &&
			req.Res[len(p.res)] == '/'
	default:
		return false
	}
}
