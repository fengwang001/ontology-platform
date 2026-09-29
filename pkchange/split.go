package pkchange

import (
	"fmt"
	"strings"
)

func opName(o Op) string {
	switch o {
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("op(%d)", int(o))
	}
}

func eventName(k EventKind) string {
	switch k {
	case EventWrite:
		return "write"
	case EventDelete:
		return "delete"
	default:
		return fmt.Sprintf("event(%d)", int(k))
	}
}

// ValidKey 报告主键是否合法：去空白后非空且不允许包含 NUL 字节。
func ValidKey(key string) bool {
	k := strings.TrimSpace(key)
	if k == "" {
		return false
	}
	return !strings.ContainsRune(key, 0)
}

func reject(reason RejectReason, index int, key, format string, args ...any) error {
	return &RejectError{
		Reason:  reason,
		Index:   index,
		Key:     key,
		Message: fmt.Sprintf(format, args...),
	}
}

// Split 逐条校验并把变更拆成下游事件；任一条非法则整批拒绝。
// accept 在每条变更校验通过后、事件追加前被回调，用于记录判定依据，可为 nil。
//
// 校验基于「源表快照 + 批内此前已校验变更」的投影状态逐条进行：
//   - 插入：键必须不存在；
//   - 删除：键必须存在；
//   - 更新：旧键必须存在，主键变化时新键必须不存在（指向自身的更新视为主键不变）。
//
// 拆分规则：插入与删除分别产生一次写入/删除；主键不变的更新只产生一次写入；
// 主键变化的更新先删除旧键、再写入新键。
func Split(snapshot map[string]Row, changes []Change, batchLimit int, accept func(int, Change)) ([]Event, error) {
	if batchLimit > 0 && len(changes) > batchLimit {
		return nil, &RejectError{
			Reason:  ReasonBatchTooLarge,
			Index:   -1,
			Limit:   batchLimit,
			Message: fmt.Sprintf("batch size %d exceeds limit %d", len(changes), batchLimit),
		}
	}

	// projected 是本批投影状态：记录每个键当前指向的行，nil 表示已被删除。
	projected := make(map[string]*Row, len(snapshot)+len(changes))
	for k, row := range snapshot {
		rowCopy := cloneRow(row)
		projected[k] = &rowCopy
	}

	events := make([]Event, 0, len(changes)*2)

	for i, ch := range changes {
		switch ch.Op {
		case OpInsert:
			if !ValidKey(ch.Key) {
				return nil, reject(ReasonInvalidKey, i, ch.Key,
					"insert at %d rejected: invalid key %q", i, ch.Key)
			}
			if r, ok := projected[ch.Key]; ok && r != nil {
				return nil, reject(ReasonKeyExists, i, ch.Key,
					"insert at %d rejected: key %q already exists", i, ch.Key)
			}
			row := cloneRow(ch.Data)
			projected[ch.Key] = &row
			if accept != nil {
				accept(i, ch)
			}
			events = append(events, Event{Kind: EventWrite, Key: ch.Key, Data: row})

		case OpDelete:
			if !ValidKey(ch.Key) {
				return nil, reject(ReasonInvalidKey, i, ch.Key,
					"delete at %d rejected: invalid key %q", i, ch.Key)
			}
			r, ok := projected[ch.Key]
			if !ok || r == nil {
				return nil, reject(ReasonKeyNotFound, i, ch.Key,
					"delete at %d rejected: key %q not found", i, ch.Key)
			}
			projected[ch.Key] = nil
			if accept != nil {
				accept(i, ch)
			}
			events = append(events, Event{Kind: EventDelete, Key: ch.Key})

		case OpUpdate:
			if !ValidKey(ch.Key) {
				return nil, reject(ReasonInvalidKey, i, ch.Key,
					"update at %d rejected: invalid old key %q", i, ch.Key)
			}
			r, ok := projected[ch.Key]
			if !ok || r == nil {
				return nil, reject(ReasonKeyNotFound, i, ch.Key,
					"update at %d rejected: old key %q not found", i, ch.Key)
			}
			newKey := ch.NewKey
			if strings.TrimSpace(newKey) == "" || newKey == ch.Key {
				// 未指定新主键视为主键不变。
				newKey = ch.Key
			}
			if !ValidKey(newKey) {
				return nil, reject(ReasonInvalidKey, i, newKey,
					"update at %d rejected: invalid new key %q", i, newKey)
			}
			if newKey != ch.Key {
				if nr, exists := projected[newKey]; exists && nr != nil {
					return nil, reject(ReasonKeyExists, i, newKey,
						"update at %d rejected: new key %q already exists", i, newKey)
				}
			}
			row := cloneRow(ch.Data)
			if newKey != ch.Key {
				projected[ch.Key] = nil
				if accept != nil {
					accept(i, ch)
				}
				events = append(events, Event{Kind: EventDelete, Key: ch.Key})
				projected[newKey] = &row
				events = append(events, Event{Kind: EventWrite, Key: newKey, Data: row})
			} else {
				projected[ch.Key] = &row
				if accept != nil {
					accept(i, ch)
				}
				events = append(events, Event{Kind: EventWrite, Key: ch.Key, Data: row})
			}

		default:
			return nil, reject(ReasonInvalidChange, i, ch.Key,
				"change at %d rejected: unknown op %d", i, int(ch.Op))
		}
	}

	return events, nil
}

func cloneRow(row Row) Row {
	if row == nil {
		return Row{}
	}
	cp := make(Row, len(row))
	for k, v := range row {
		cp[k] = v
	}
	return cp
}
