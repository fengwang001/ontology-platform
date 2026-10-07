package provenance

import (
	"encoding/json"
	"io"
	"sync"
)

// LogEntry 是一条查询日志的完整内容：输入参数、返回路径集合，
// 以及每条路径据以判定可见的三方（源、逐跳链接、逐跳目标）记录标识。
type LogEntry struct {
	Source         ObjectID      `json:"source"`
	ValidAt        Time          `json:"valid_at"`
	AsOf           Time          `json:"as_of"`
	MaxDepth       int           `json:"max_depth"`
	SourceRecord   RecordRef     `json:"source_record"`
	SourceReason   string        `json:"source_reason"`
	Paths          []Path        `json:"paths"`
	Rejected       []RejectedHop `json:"rejected"`
	CandidatesSeen int           `json:"candidates_seen"`
}

// QueryLogger 以 JSON Lines（每事件一行 JSON）记录每次溯源查询。
// 内部互斥保证并发查询交织时日志行不互相撕裂。
type QueryLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewQueryLogger 创建写入 w 的日志器；w 为 nil 时记录为空操作。
func NewQueryLogger(w io.Writer) *QueryLogger { return &QueryLogger{w: w} }

// Log 将一次查询的输入与结果序列化为一行 JSON 并追加换行。
func (l *QueryLogger) Log(q Query, res *Result) error {
	if l == nil || l.w == nil {
		return nil
	}
	entry := LogEntry{
		Source:         q.Source,
		ValidAt:        q.ValidAt,
		AsOf:           q.AsOf,
		MaxDepth:       q.MaxDepth,
		CandidatesSeen: res.CandidatesSeen,
	}
	if res != nil {
		entry.SourceRecord = res.Source
		entry.SourceReason = res.SourceReason.String()
		entry.Paths = res.Paths
		entry.Rejected = res.Rejected
		if entry.Paths == nil {
			entry.Paths = []Path{}
		}
		if entry.Rejected == nil {
			entry.Rejected = []RejectedHop{}
		}
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(line, '\n'))
	return err
}

// LogError 在查询返回错误时也留下参数与错误类别，保证审计完整。
func (l *QueryLogger) LogError(q Query, err error) error {
	if l == nil || l.w == nil {
		return nil
	}
	payload := struct {
		Source   ObjectID `json:"source"`
		ValidAt  Time     `json:"valid_at"`
		AsOf     Time     `json:"as_of"`
		MaxDepth int      `json:"max_depth"`
		Error    string   `json:"error"`
	}{q.Source, q.ValidAt, q.AsOf, q.MaxDepth, err.Error()}
	line, mErr := json.Marshal(payload)
	if mErr != nil {
		return mErr
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, werr := l.w.Write(append(line, '\n'))
	return werr
}
