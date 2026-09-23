// Package batch 定义导入批次：批次 ID、记录（业务键+值）与记录来源。
package batch

import "errors"

// Record 是批次中的一条记录。Key 为业务键（空串合法），Value 为有效载荷。
type Record struct {
	Key   string
	Value []byte
}

// Batch 是一次导入的静态描述。
type Batch struct {
	ID      string
	Records []Record
}

// Source 按需提供记录，使大批次无需整体驻留内存。
type Source interface {
	Count() int
	At(i int) (Record, error)
	Close() error
}

// DupError 描述同一批次内重复的业务键及其两处位置。
type DupError struct {
	Key       string
	FirstPos  int
	SecondPos int
}

func (e *DupError) Error() string { return "duplicate business key: " + e.Key }

var (
	// ErrEmptyID 表示批次 ID 为空串。
	ErrEmptyID = errors.New("batch id must not be empty")
)

// Validate 校验批次 ID 非空与批次内业务键唯一。
// 重复时返回 *DupError，指出键与两处位置（取最先出现的一对）。
func Validate(b *Batch) error {
	if b.ID == "" {
		return ErrEmptyID
	}
	seen := make(map[string]int, len(b.Records))
	for i, rec := range b.Records {
		if first, ok := seen[rec.Key]; ok {
			return &DupError{Key: rec.Key, FirstPos: first, SecondPos: i}
		}
		seen[rec.Key] = i
	}
	return nil
}

// ValidateSource 流式校验来源：ID 非空、业务键唯一。
// 同一时刻仅持有 map 元数据；用于不整体载入记录的大批次。
func ValidateSource(id string, src Source) error {
	if id == "" {
		return ErrEmptyID
	}
	seen := make(map[string]int, src.Count())
	for i := 0; i < src.Count(); i++ {
		rec, err := src.At(i)
		if err != nil {
			return err
		}
		if first, ok := seen[rec.Key]; ok {
			return &DupError{Key: rec.Key, FirstPos: first, SecondPos: i}
		}
		seen[rec.Key] = i
	}
	return nil
}
