package pkchange

import "log/slog"

// logChange 记录一条通过校验的变更及其批内下标。
type logChange struct {
	Index  int
	Change Change
}

func describeChanges(changes []Change) []slog.Value {
	out := make([]slog.Value, 0, len(changes))
	for i, ch := range changes {
		out = append(out, slog.GroupValue(
			slog.Int("index", i),
			slog.String("op", ch.Op.String()),
			slog.String("key", ch.Key),
			slog.String("new_key", ch.NewKey),
			slog.Any("data", ch.Data),
		))
	}
	return out
}

func describeAccepted(accepted []logChange) []slog.Value {
	out := make([]slog.Value, 0, len(accepted))
	for _, ac := range accepted {
		ch := ac.Change
		out = append(out, slog.GroupValue(
			slog.Int("index", ac.Index),
			slog.String("op", ch.Op.String()),
			slog.String("key", ch.Key),
			slog.String("new_key", ch.NewKey),
			slog.String("decision", decisionFor(ch)),
		))
	}
	return out
}

func decisionFor(ch Change) string {
	switch ch.Op {
	case OpInsert:
		return "insert->write"
	case OpDelete:
		return "delete->delete"
	case OpUpdate:
		if ch.NewKey != "" && ch.NewKey != ch.Key {
			return "update-key-changed->delete-then-write"
		}
		return "update-same-key->write"
	default:
		return "unknown"
	}
}

func describeEvents(events []Event) []slog.Value {
	out := make([]slog.Value, 0, len(events))
	for _, ev := range events {
		out = append(out, slog.GroupValue(
			slog.String("kind", ev.Kind.String()),
			slog.String("key", ev.Key),
			slog.Any("data", ev.Data),
		))
	}
	return out
}

func describePartitions(partitions []Partition) []slog.Value {
	out := make([]slog.Value, 0, len(partitions))
	for _, p := range partitions {
		keys := make([]string, 0, len(p.Events))
		for _, ev := range p.Events {
			keys = append(keys, ev.Kind.String()+":"+ev.Key)
		}
		out = append(out, slog.GroupValue(
			slog.Int("partition", p.Index),
			slog.Int("event_count", len(p.Events)),
			slog.Any("events", keys),
		))
	}
	return out
}
