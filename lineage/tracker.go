package lineage

import (
	"io"
	"log/slog"
	"sync"
	"time"
)

// Tracker 是并发安全的数据血缘追踪器。
type Tracker struct {
	mu sync.RWMutex

	nodes    map[string]map[string]*Node // id -> version -> node
	versions map[string][]string         // id -> 已登记版本（按产生顺序）
	kinds    map[string]Kind             // id -> 产生方式

	out map[string]map[string]map[string]bool // fromID -> fromVer -> "toID@toVer"
	in  map[string]map[string]map[string]bool // toID   -> toVer   -> "fromID@fromVer"

	log *slog.Logger
	now func() time.Time
}

// New 创建血缘追踪器，日志写入 w（传 io.Discard 可静默）。
func New(w io.Writer) *Tracker {
	if w == nil {
		w = io.Discard
	}
	return &Tracker{
		nodes:    map[string]map[string]*Node{},
		versions: map[string][]string{},
		kinds:    map[string]Kind{},
		out:      map[string]map[string]map[string]bool{},
		in:       map[string]map[string]map[string]bool{},
		log:      slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})),
		now:      time.Now,
	}
}
