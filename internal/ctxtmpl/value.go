package ctxtmpl

// applyValue escapes a raw interpolation value for the current context,
// performs URL validation when applicable and advances parser state.
func (sc *scanner) applyValue(raw string) (string, error) {
	ic, err := sc.atInterpolation()
	if err != nil {
		return "", err
	}
	if ic.urlValue && ic.atURLStart {
		if err := validateURLStart(raw); err != nil {
			return "", err
		}
	}
	var escaped string
	switch ic.kind {
	case ctxText:
		escaped = escapeHTMLText(raw)
	case ctxComment:
		escaped = escapeComment(raw)
	default:
		escaped = escapeAttrValue(raw, ic.kind)
	}
	sc.afterInterpolation()
	return escaped, nil
}
