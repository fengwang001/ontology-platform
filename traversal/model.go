package traversal

import "time"

type Mode string

const (
	SilentDrop         Mode = "SILENT_DROP"
	ExplicitTruncation Mode = "EXPLICIT_TRUNCATION"
)

type Object struct {
	ID         string
	Properties map[string]string
}

type Link struct {
	ID         string
	Type       string
	FromID     string
	ToID       string
	Properties map[string]string
}

type TruncationMarker struct {
	Hop     int
	Dropped int
}

type ResultItem struct {
	Hop    int
	Object *Object
	Link   *Link
}

type PageRequest struct {
	StartObjectID string
	Cursor        string
	BatchSize     int
	Mode          Mode
	HopLimits     []int
	MaxHops       int
}

type Page struct {
	Items       []ResultItem
	NextCursor  string
	Complete    bool
	Truncations []TruncationMarker
}

type RequestLog struct {
	Time        time.Time
	Cursor      string
	Mode        Mode
	Items       []ResultItem
	Truncations []TruncationMarker
	Error       error
}

type TraversalError struct {
	Code    string
	Message string
}

func (err *TraversalError) Error() string {
	return err.Message
}

const (
	ErrStartObjectNotFound = "START_OBJECT_NOT_FOUND"
	ErrInvalidCursor       = "INVALID_CURSOR"
	ErrInvalidBatchSize    = "INVALID_BATCH_SIZE"
	ErrModeChanged         = "MODE_CHANGED"
)
