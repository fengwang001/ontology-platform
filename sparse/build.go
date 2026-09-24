package sparse

import "ontology/segment"

// Build 顺序扫描段文件，按 interval 规则重建索引。
// 锚点只依赖段内可读信息：下标为 interval 倍数处的 seq 与读取偏移。
func Build(logPath string, interval uint64) (*Index, error) {
	_, evs, _, err := segment.ScanPrefix(logPath)
	if err != nil {
		return nil, err
	}
	var anchors []Anchor
	off := int64(segment.HeaderSize)
	for i, ev := range evs {
		if uint64(i)%interval == 0 {
			anchors = append(anchors, Anchor{Seq: ev.Seq, Offset: off})
		}
		off += int64(4 + ev.Size() + 4)
	}
	return New(interval, anchors), nil
}

// LoadOrBuild 优先读索引文件；缺失则从段重建并落盘。
func LoadOrBuild(logPath string, interval uint64) (*Index, error) {
	x, err := Load(logPath)
	if err == nil {
		return x, nil
	}
	x, err = Build(logPath, interval)
	if err != nil {
		return nil, err
	}
	if err := x.Save(logPath); err != nil {
		return nil, err
	}
	return x, nil
}
