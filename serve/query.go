package serve

import "ontology/coalesce"

// 只读查询：以下方法不推进任何状态，可在任意时刻安全调用。
// 未成功 Build 前一律返回零值（且 IsMultipart 为 false）。

// Ranges 返回归一化后的区间列表副本（按起点升序、互不重叠相邻）。
func (a *Assembler) Ranges() []coalesce.Interval {
	a.mu.rlock()
	defer a.mu.runlock()
	if !a.built {
		return nil
	}
	out := make([]coalesce.Interval, len(a.ranges))
	copy(out, a.ranges)
	return out
}

// TotalSize 返回响应体总字节数（封装时含全部头部与结束边界）。
func (a *Assembler) TotalSize() int64 {
	a.mu.rlock()
	defer a.mu.runlock()
	if !a.built {
		return 0
	}
	return int64(len(a.body))
}

// Written 返回截至目前已成功写出的字节数。
func (a *Assembler) Written() int64 {
	a.mu.rlock()
	defer a.mu.runlock()
	return a.written
}

// IsMultipart 报告响应是否为 multipart 封装。
// 仅一个区间（含合并后只剩一个）时为 false，即裸字节。
func (a *Assembler) IsMultipart() bool {
	a.mu.rlock()
	defer a.mu.runlock()
	return a.built && a.encaps
}

// Boundary 返回封装使用的边界串；未封装时为空串。
func (a *Assembler) Boundary() string {
	a.mu.rlock()
	defer a.mu.runlock()
	return a.boundary
}

// ContentType 返回应使用的 Content-Type 头值；裸字节时返回 application/octet-stream。
func (a *Assembler) ContentType() string {
	a.mu.rlock()
	defer a.mu.runlock()
	if !a.built {
		return ""
	}
	if !a.encaps {
		return "application/octet-stream"
	}
	return contentTypeBoundary(a.boundary)
}
