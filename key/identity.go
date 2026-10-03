// Package key defines cache variant identity and Vary matching.
package key

import "sort"

// Identity identifies a stored response variant:
// Owner is "" for public variants and the subject for private variants;
// Names are the ordered Vary header names; Vals are their stored values
// in the same order.
type Identity struct {
	Owner string
	Names []string
	Vals  []string
}

// Build constructs an Identity from a (possibly unsorted, possibly
// duplicated) Vary list and the stored headers. Missing headers are "".
func Build(owner string, vary []string, headers map[string]string) Identity {
	names := sortedDedup(vary)
	vals := make([]string, len(names))
	for i, name := range names {
		vals[i] = headers[name]
	}
	return Identity{Owner: owner, Names: names, Vals: vals}
}

// Equal reports whether two identities are the same variant.
func (i Identity) Equal(o Identity) bool {
	if i.Owner != o.Owner || len(i.Names) != len(o.Names) {
		return false
	}
	for k := range i.Names {
		if i.Names[k] != o.Names[k] || i.Vals[k] != o.Vals[k] {
			return false
		}
	}
	return true
}

// Matches reports whether requestHeaders satisfy this identity's Vary
// constraints; a missing header is treated as the empty string.
func (i Identity) Matches(requestHeaders map[string]string) bool {
	for k, name := range i.Names {
		if requestHeaders[name] != i.Vals[k] {
			return false
		}
	}
	return true
}

func sortedDedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	w := 0
	for r := 0; r < len(out); r++ {
		if r > 0 && out[r] == out[r-1] {
			continue
		}
		out[w] = out[r]
		w++
	}
	return out[:w]
}
