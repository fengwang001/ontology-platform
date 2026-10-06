package backupretention

import (
	"fmt"
	"io"
	"strings"
)

// layerName 给出稳定的层名，供日志使用。
func layerName(l Layer) string {
	switch l {
	case LayerDaily:
		return "daily"
	case LayerWeekly:
		return "weekly"
	case LayerMonthly:
		return "monthly"
	default:
		return "?"
	}
}

// logf 输出一条操作日志；未配置 logger 时为空操作。
// 调用方必须持有 s.mu（日志本身只是字节写入，状态读取仍在锁内完成）。
func (s *Service) logf(format string, args ...any) {
	if s.logWriter == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if !strings.HasSuffix(msg, "\n") {
		msg += "\n"
	}
	_, _ = io.WriteString(s.logWriter, msg)
}

func layersString(ls []Layer) string {
	parts := make([]string, 0, len(ls))
	for _, l := range ls {
		parts = append(parts, layerName(l))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}
