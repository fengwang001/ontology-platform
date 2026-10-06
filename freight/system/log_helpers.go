package system

import "ontology/freight/model"

func resultLog(fb *model.FeeBreakdown, err error) string {
	if err != nil {
		return toJSON(map[string]any{
			"ok":     false,
			"code":   string(model.CodeOf(err)),
			"reason": err.Error(),
		})
	}
	return toJSON(map[string]any{"ok": true, "breakdown": fb})
}

func contractLog(c *model.Contract) string { return toJSON(c) }

func waybillLog(w *model.Waybill) string { return toJSON(w) }

func quoteLog(req QuoteRequest) string { return toJSON(req) }

func quoteResultLog(lines []QuoteLine) string {
	return toJSON(map[string]any{"ok": true, "lines": lines})
}
