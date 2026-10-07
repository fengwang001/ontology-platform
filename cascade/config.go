package cascade

import "sort"

// Config holds the per-link-type rules and keep-alive markers.
type Config struct {
	types map[string]LinkType
}

// NewConfig builds a config from link-type definitions.
func NewConfig(defs map[string]LinkType) *Config {
	cp := make(map[string]LinkType, len(defs))
	for name, def := range defs {
		cp[name] = def
	}
	return &Config{types: cp}
}

// ruleFor returns the rule that governs the endpoint opposite deletedEnd.
// ok is false when the link type is not configured.
func (c *Config) ruleFor(link Link, deletedEnd string) (Rule, bool) {
	def, ok := c.types[link.Type]
	if !ok {
		return 0, false
	}
	if deletedEnd == link.Src {
		return def.OutRule, true
	}
	return def.InRule, true
}

// keepers returns inbound keep-alive links of obj whose source survives.
// Planning runs on an immutable snapshot; every link touching a deleted
// object is removed, so only the source needs testing.
func (c *Config) keepers(links []Link, obj string, deleted map[string]bool) []Link {
	var out []Link
	for _, l := range links {
		if l.Dst != obj {
			continue
		}
		def, ok := c.types[l.Type]
		if ok && def.KeepAlive && !deleted[l.Src] {
			out = append(out, l)
		}
	}
	return out
}

// snapshot is an immutable copy of the graph used while planning one request.
type snapshot struct {
	objects map[string]bool
	links   []Link
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if m[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
