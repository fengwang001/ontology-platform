package cascade

// Rule is the per-link-type cascade rule applied to the opposite endpoint
// of a link when one endpoint is being deleted.
type Rule int

const (
	// RuleCascade deletes the opposite endpoint as a new deletion request.
	RuleCascade Rule = iota
	// RuleSetNull keeps the opposite endpoint; the link is removed.
	RuleSetNull
	// RuleRestrict rejects the whole deletion if the opposite endpoint survives.
	RuleRestrict
)

// LinkType is the configured type of a directed link.
type LinkType struct {
	// OutRule governs the target when the source is deleted.
	InRule  Rule
	OutRule Rule
	// KeepAlive marks inbound links of this type as "existence implies
	// retention": while such an inbound link still exists, the endpoint is
	// not an orphan.
	KeepAlive bool
}

// Link is a directed typed edge from Src to Dst.
type Link struct {
	ID   string
	Type string
	Src  string
	Dst  string
}
