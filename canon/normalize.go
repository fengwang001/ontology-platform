package canon

import (
	"strings"
	"sync"

	"ontology/host"
	"ontology/pct"
	"ontology/query"
	"ontology/segpath"
)

// Normalizer canonicalizes URLs with immutable configuration and a safe cache.
type Normalizer struct {
	cfg Config

	mu    sync.RWMutex
	cache map[string]*Result
}

// New creates a Normalizer.
func New(cfg Config) *Normalizer {
	return &Normalizer{cfg: cfg, cache: make(map[string]*Result)}
}

// Normalize canonicalizes one URL; identical repeated queries are identical.
func (n *Normalizer) Normalize(raw string) (*Result, error) {
	n.mu.RLock()
	if r, ok := n.cache[raw]; ok {
		n.mu.RUnlock()
		return r, nil
	}
	n.mu.RUnlock()

	r, err := n.compute(raw)
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	if existing, ok := n.cache[raw]; ok {
		n.mu.Unlock()
		return existing, nil
	}
	n.cache[raw] = r
	n.mu.Unlock()
	return r, nil
}

func (n *Normalizer) compute(raw string) (*Result, error) {
	if n.cfg.Limits.MaxURLLen > 0 && len(raw) > n.cfg.Limits.MaxURLLen {
		return nil, limitErr(ErrTooLong)
	}
	var rw []RewriteKind
	var scanned int64
	count := func(k int) { scanned += int64(k) }

	if strings.ContainsRune(raw, '#') {
		rw = append(rw, RewriteFragment)
	}
	p := splitURL(raw)

	var hostText, port string
	if p.hasAuth {
		h, err := host.Parse(p.authority, defaultPortFor(p.scheme), pct.ByteScanner(count))
		if err != nil {
			return nil, err
		}
		hostText = h.String()
		port = h.Port()
		if !strings.EqualFold(p.authority, h.Authority()) {
			rw = append(rw, hostRewriteKinds(p.authority, h)...)
		}
	}

	sp, err := segpath.Parse(p.path, pct.ByteScanner(count))
	if err != nil {
		return nil, err
	}
	if n.cfg.Limits.MaxSegments > 0 && len(sp.Segments()) > n.cfg.Limits.MaxSegments {
		return nil, limitErr(ErrTooManySegments)
	}
	rw = append(rw, pathRewriteKinds(p.path, sp)...)

	var items []query.Item
	queryText := ""
	if p.hasQuery {
		var err error
		items, queryText, err = query.Parse(p.query, n.cfg.QueryMode, pct.ByteScanner(count))
		if err != nil {
			return nil, err
		}
		if n.cfg.Limits.MaxQueryItems > 0 && len(items) > n.cfg.Limits.MaxQueryItems {
			return nil, limitErr(ErrTooManyQueryItems)
		}
		origOrder, _ := query.ParseCanonicalOrder(p.query)
		rw = append(rw, queryRewriteKinds(p.query, queryText)...)
		if n.cfg.QueryMode == query.ModeSorted && origOrder != queryText {
			rw = append(rw, RewriteQuerySort)
		}
	}

	out := buildURL(p.scheme, p.hasAuth, hostText, sp, p.hasQuery, queryText)
	r := &Result{
		url:      out,
		scheme:   p.scheme,
		host:     hostText,
		port:     port,
		path:     sp.Segments(),
		query:    append([]query.Item(nil), items...),
		changed:  out != raw,
		rewrites: dedupKinds(rw),
		scanned:  scanned,
	}
	return r, nil
}

func buildURL(scheme string, hasAuth bool, hostText string, sp *segpath.Path, hasQuery bool, q string) string {
	var b strings.Builder
	if scheme != "" {
		b.WriteString(scheme)
		b.WriteString("://")
	}
	if hasAuth {
		if scheme == "" {
			b.WriteString("//")
		}
		b.WriteString(hostText)
	}
	path := sp.String()
	if hasAuth && path == "" {
		path = "/"
	}
	b.WriteString(path)
	if hasQuery {
		b.WriteByte('?')
		b.WriteString(q)
	}
	return b.String()
}

func hostRewriteKinds(rawAuth string, h *host.Host) []RewriteKind {
	var kinds []RewriteKind
	body := rawAuth
	if i := strings.LastIndexByte(rawAuth, ':'); i > 0 && !strings.HasPrefix(rawAuth, "[") {
		body = rawAuth[:i]
	}
	if body != strings.ToLower(body) {
		kinds = append(kinds, RewriteHostCase)
	}
	if strings.HasSuffix(body, ".") && len(body) > 1 {
		kinds = append(kinds, RewriteTrailingDot)
	}
	if strings.Contains(body, "[") {
		kinds = append(kinds, RewriteIPv6)
	}
	if strings.Contains(rawAuth, ":") {
		if h.Port() == "" {
			kinds = append(kinds, RewriteDefaultPort)
		} else {
			kinds = append(kinds, RewritePortNorm)
		}
	}
	return kinds
}

func dedupKinds(in []RewriteKind) []RewriteKind {
	seen := make(map[RewriteKind]bool, len(in))
	out := in[:0]
	for _, k := range in {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
