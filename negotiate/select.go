package negotiate

// Select picks the most suitable offer for the given HTTP Accept header.
//
// offers are the media types supported by the server, ordered by the
// server's own preference from high to low. It returns the selected
// offer verbatim and the raw text of the Accept rule that matched it.
//
// Selection semantics: for each offer, the most specific matching rule
// wins (exact > type wildcard > full wildcard), regardless of q; ties
// go to the higher q, then to the earlier rule. A winning rule with
// q=0 explicitly rejects that offer. Among the remaining offers the
// highest q wins; equal q keeps the server's order. The offers slice
// is never modified.
func Select(accept string, offers []string) (string, string, error) {
	rules, err := parseAccept(accept)
	if err != nil {
		return "", "", err
	}
	if len(offers) == 0 {
		return "", "", ErrNotAcceptable
	}
	bestQ := 0
	bestOffer := ""
	bestRule := ""
	for _, offer := range offers {
		mt, err := parseMediaType(offer)
		if err != nil {
			continue
		}
		r, ok := bestRuleFor(rules, mt)
		if !ok || r.milliQ == 0 {
			continue
		}
		if r.milliQ > bestQ {
			bestQ = r.milliQ
			bestOffer = offer
			bestRule = r.raw
		}
	}
	if bestQ == 0 {
		return "", "", ErrNotAcceptable
	}
	return bestOffer, bestRule, nil
}

// bestRuleFor returns the highest-priority rule matching offer:
// most specific first, then highest q, then earliest in the header.
func bestRuleFor(rules []rule, offer mediaType) (rule, bool) {
	best := -1
	for i, r := range rules {
		if !matches(r.media, offer) {
			continue
		}
		if best < 0 || betterRule(r, rules[best]) {
			best = i
		}
	}
	if best < 0 {
		return rule{}, false
	}
	return rules[best], true
}

// betterRule reports whether a outranks b. Equal specificity and q
// keep the earlier rule, so results are deterministic.
func betterRule(a, b rule) bool {
	sa, sb := specificity(a.media), specificity(b.media)
	if sa != sb {
		return sa > sb
	}
	return a.milliQ > b.milliQ
}

// matches reports whether a parsed Accept media range matches an offer.
// A range with parameters only matches offers carrying the same
// parameter names and values.
func matches(pattern, offer mediaType) bool {
	if pattern.typ != "*" && pattern.typ != offer.typ {
		return false
	}
	if pattern.sub != "*" && pattern.sub != offer.sub {
		return false
	}
	for name, value := range pattern.params {
		offerValue, ok := offer.params[name]
		if !ok || offerValue != value {
			return false
		}
	}
	return true
}
