package indexstore

// wal 是只追加的写入日志：LSN 从 1 开始连续。append 与主表更新在同一个
// 提交步骤内落盘（见 store.put），因此崩溃后主表与日志末尾严格一致。
type wal struct {
	disk *Disk
	// content 是磁盘上当前已提交的全部 WAL 文本（每条一行，含末尾 \n）。
	content string
}

func newWAL(d *Disk) *wal {
	return &wal{disk: d}
}

// load 读出全部日志条目（按 LSN 升序）。
func (w *wal) load() ([]LogEntry, error) {
	if v, ok := w.disk.get("wal"); ok {
		w.content = v
	}
	var out []LogEntry
	text := w.content
	for len(text) > 0 {
		var line string
		if i := indexByte(text, '\n'); i >= 0 {
			line, text = text[:i], text[i+1:]
		} else {
			line, text = text, ""
		}
		if line == "" {
			continue
		}
		e, err := decodeEntry(line)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// appendWith 在与主表改动相同的原子步骤里追加一条日志（骨架）。
func (w *wal) appendWith(step string, e LogEntry, tableChanges map[string]string, tableDeletes []string) bool {
	changes := map[string]string{}
	for k, v := range tableChanges {
		changes[k] = v
	}
	changes["wal"] = w.content + encodeEntry(e) + "\n"
	if !w.disk.stagedCommit(step, changes, tableDeletes) {
		return false
	}
	w.content = changes["wal"]
	return true
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
