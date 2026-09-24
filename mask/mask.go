package mask

import (
	"crypto/sha256"
	"encoding/hex"

	"ontology/record"
	"ontology/rule"
)

const truncatedSuffix = "\u2026<truncated>"

type compiledRule interface {
	Action() rule.Action
	Keep() int
	Replacement() string
}

func Apply(rec *record.Record, set *rule.Set) *record.Record {
	copied, _ := walkMap(rec.Fields, "", nil, set).(map[string]any)
	if copied == nil {
		copied = map[string]any{}
	}
	return &record.Record{Level: rec.Level, TraceID: rec.TraceID, Fields: copied}
}

func walkMap(input map[string]any, prefix string, suppressed map[string]bool, set *rule.Set) any {
	rules, shadows := set.RulesAt(prefix)
	output := make(map[string]any, len(input))
	for key, value := range input {
		fullPath := joinPath(prefix, key)
		if suppressed[key] {
			output[key] = value
			continue
		}
		if compiled, ok := rules[key]; ok {
			output[key] = transformString(asString(value), compiled)
			continue
		}
		switch typed := value.(type) {
		case map[string]any:
			output[key] = walkMap(typed, fullPath, shadows[key], set)
		case []any:
			output[key] = walkSlice(typed, fullPath, shadows[key], set)
		case string:
			output[key] = matchValue(typed, set)
		default:
			output[key] = value
		}
	}
	return output
}

func walkSlice(input []any, prefix string, suppressed map[string]bool, set *rule.Set) any {
	output := make([]any, len(input))
	for index, value := range input {
		switch typed := value.(type) {
		case map[string]any:
			output[index] = walkMap(typed, prefix, suppressed, set)
		case []any:
			output[index] = walkSlice(typed, prefix, suppressed, set)
		case string:
			output[index] = matchValue(typed, set)
		default:
			output[index] = value
		}
	}
	return output
}

func matchValue(value string, set *rule.Set) any {
	matched := set.MatchValues(value)
	if len(matched) == 0 {
		return value
	}
	return transformString(value, matched[0])
}

func transformString(value string, compiled compiledRule) string {
	switch compiled.Action() {
	case rule.Replace:
		return compiled.Replacement()
	case rule.Truncate:
		runes := []rune(value)
		keep := compiled.Keep()
		if keep < 0 {
			keep = 0
		}
		if keep > len(runes) {
			keep = len(runes)
		}
		return string(runes[:keep]) + truncatedSuffix
	default:
		digest := sha256.Sum256([]byte(value))
		return hex.EncodeToString(digest[:])
	}
}

func joinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
