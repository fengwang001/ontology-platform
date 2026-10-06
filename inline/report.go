package inline

import (
	"strconv"
	"strings"
)

// Fate of one examined call site instance.
type Fate int

const (
	FateInlined Fate = iota + 1
	FateRejected
)

func (f Fate) String() string {
	switch f {
	case FateInlined:
		return "inlined"
	case FateRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// SiteFate is one row of the decision report.
type SiteFate struct {
	// Path identifies the instance from the root: site indices taken along the
	// expansion. The original site is [i]; a copied site is [i, j, ...].
	Path   []int
	Owner  string
	Callee string
	Heat   float64
	Fate   Fate
	// Reason is valid only when Fate == FateRejected.
	Reason Reason
}

// Report is the immutable result of one root's decision. Its numbers are the
// very numbers used during the decision; they are never recomputed.
type Report struct {
	Function    string
	InitialSize int
	FinalSize   int

	// Fates are recorded in examination order, which is itself deterministic
	// (heat desc, path asc).
	Sites []SiteFate

	// DeepestPath is the deepest expansion chain reached (root first).
	DeepestPath []string
}

func (r *Report) recordFate(site instance, fate Fate, reason Reason) {
	r.Sites = append(r.Sites, SiteFate{
		Path:   append([]int(nil), site.path...),
		Owner:  site.owner,
		Callee: site.callee,
		Heat:   site.heat,
		Fate:   fate,
		Reason: reason,
	})
}

func (a *Account) accountInto(r *Report) { r.FinalSize = a.current }

func (r *Report) count(f Fate) int {
	n := 0
	for _, s := range r.Sites {
		if s.Fate == f {
			n++
		}
	}
	return n
}

// Inlined reports how many site instances were accepted.
func (r *Report) Inlined() int { return r.count(FateInlined) }

// Rejected reports how many site instances were refused.
func (r *Report) Rejected() int { return r.count(FateRejected) }

// SiteByPath returns the row for an exact instance path, or nil.
func (r *Report) SiteByPath(path []int) *SiteFate {
	for i := range r.Sites {
		if equalPath(r.Sites[i].Path, path) {
			return &r.Sites[i]
		}
	}
	return nil
}

func equalPath(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Render writes a deterministic human-readable report.
func (r *Report) Render() string {
	var b strings.Builder
	b.WriteString("function " + r.Function + "\n")
	b.WriteString("size initial=" + itoa(r.InitialSize) + " final=" + itoa(r.FinalSize) + "\n")
	b.WriteString("deepest-chain " + strings.Join(r.DeepestPath, " -> ") + "\n")
	for _, s := range r.Sites {
		row := "site " + pathString(s.Path) +
			" owner=" + s.Owner + " callee=" + s.Callee + " heat=" + ftoa(s.Heat) + " "
		if s.Fate == FateInlined {
			row += "inlined\n"
		} else {
			row += "rejected reason=" + s.Reason.String() + "\n"
		}
		b.WriteString(row)
	}
	return b.String()
}

func pathString(p []int) string {
	var b strings.Builder
	b.WriteString("[")
	for i, x := range p {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(itoa(x))
	}
	b.WriteString("]")
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func ftoa(f float64) string {
	// %g is deterministic for identical float64 inputs on all platforms.
	return formatFloat(f)
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
