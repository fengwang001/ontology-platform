package canon

import (
	"strings"

	"ontology/host"
	"ontology/query"
	"ontology/segpath"
)

// assemble runs the per-component normalization pipeline and builds the
// canonical string plus the rewrite report. scans accumulates the number
// of input bytes examined by this pass.
func (n *Normalizer) assemble(u *rawURL, scans *int64) (string, Report, error) {
	var rep Report
	rep.Scheme = u.scheme
	if u.scheme != u.schemeRaw {
		rep.Kinds |= KindScheme
	}
	if u.fragment {
		rep.Kinds |= KindFragment
	}
	*scans += int64(len(u.authority))
	h, port, hasPort, err := host.Normalize(u.authority)
	if err != nil {
		return "", rep, err
	}
	origHost, origPort, hadPort := splitAuthority(u.authority)
	if hasPort && isDefaultPort(u.scheme, port) {
		hasPort = false
	}
	if h != origHost {
		rep.Kinds |= KindHost
	}
	if hadPort && (!hasPort || origPort != port) {
		rep.Kinds |= KindPort
	}
	if !hasPort {
		port = ""
	}
	rep.Host, rep.Port = h, port
	if u.path == "" {
		u.path = "/"
		rep.Kinds |= KindEmptyPath
	}
	*scans += int64(len(u.path))
	pi, err := segpath.Normalize(u.path)
	if err != nil {
		return "", rep, err
	}
	rep.Path, rep.Segments = pi.Path, pi.Segments
	if pi.DotFired {
		rep.Kinds |= KindDotSegment
	}
	if pi.EscFired {
		rep.Kinds |= KindEscape
	}
	qs, err := n.normalizeQuery(u, &rep, scans)
	if err != nil {
		return "", rep, err
	}
	var b strings.Builder
	b.WriteString(u.scheme)
	b.WriteString("://")
	b.WriteString(h)
	if hasPort {
		b.WriteByte(':')
		b.WriteString(port)
	}
	b.WriteString(pi.Path)
	if u.hasQuery {
		b.WriteByte('?')
		b.WriteString(qs)
	}
	rep.HasQuery = u.hasQuery
	rep.Canonical = b.String()
	rep.Rewritten = rep.Kinds != 0
	return rep.Canonical, rep, nil
}

func (n *Normalizer) normalizeQuery(u *rawURL, rep *Report, scans *int64) (string, error) {
	if !u.hasQuery {
		return "", nil
	}
	*scans += int64(len(u.query))
	items, st, err := query.Parse(u.query)
	if err != nil {
		return "", err
	}
	if st.EscFired || st.Dropped {
		rep.Kinds |= KindQuery
	}
	if n.cfg.Mode == ModeSorted {
		before := query.Build(items)
		query.Sort(items)
		if query.Build(items) != before {
			rep.Kinds |= KindQueryOrder
		}
	}
	rep.Query = items
	return query.Build(items), nil
}

func isDefaultPort(scheme, port string) bool {
	return scheme == "http" && port == "80" || scheme == "https" && port == "443"
}

// splitAuthority extracts the original host and port substrings.
func splitAuthority(authority string) (hostPart, port string, hasPort bool) {
	if strings.HasPrefix(authority, "[") {
		if j := strings.IndexByte(authority, ']'); j >= 0 {
			hostPart = authority[:j+1]
			if j+1 < len(authority) && authority[j+1] == ':' {
				return hostPart, authority[j+2:], true
			}
			return hostPart, "", false
		}
		return authority, "", false
	}
	if i := strings.IndexByte(authority, ':'); i >= 0 {
		return authority[:i], authority[i+1:], true
	}
	return authority, "", false
}
