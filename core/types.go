// Package core 定义对象实例双时态存储子系统的共享类型。
package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Key 唯一标识一个对象实例：对象类型 + 主键。
type Key struct {
	Type string
	ID   string
}

func (k Key) String() string { return k.Type + "/" + k.ID }

// Version 是一次写入产生的不可变版本。
type Version struct {
	Seq       uint64    // 系统时间坐标：存储子系统分配，同一主键内从 1 开始严格递增、不得回退
	BizStart  int64     // 业务时间起点（闭区间端点）
	Payload   string    // 业务内容；逻辑删除版本为空
	Deleted   bool      // 逻辑删除（墓碑）标记
	WallClock time.Time // 真实写入时刻（信息性字段，不参与查询坐标）
}

func (v Version) String() string {
	kind := "put"
	if v.Deleted {
		kind = "del"
	}
	return fmt.Sprintf("%s#%d@%d(%q)", kind, v.Seq, v.BizStart, v.Payload)
}

// WriteRequest 描述一次写入（含逻辑删除）。
type WriteRequest struct {
	Key        Key
	BizStart   int64
	Payload    string
	Delete     bool
	Credential string // 乐观并发凭证，格式 "v<N>"；"v0" 表示期望该主键尚无任何版本
}

// Visibility 是双时态查询的结果标记。
type Visibility string

const (
	// Found 命中有效版本。
	Found Visibility = "FOUND"
	// Deleted 命中的可见版本是墓碑（该业务时间起事实不再成立）。
	Deleted Visibility = "DELETED"
	// NotVisible 主键存在，但该 (系统时间, 业务时间) 坐标下无可见版本。
	NotVisible Visibility = "NOT_VISIBLE"
	// NeverExisted 主键从未写入过任何版本。
	NeverExisted Visibility = "NEVER_EXISTED"
)

// QueryResult 是双时态点查询的结果。
type QueryResult struct {
	Visibility Visibility
	Version    *Version // Found / Deleted 时非空
}

func (r QueryResult) String() string {
	if r.Version == nil {
		return string(r.Visibility)
	}
	return fmt.Sprintf("%s %s", r.Visibility, r.Version)
}

// Interval 是历史重建得到的一段可见区间 [Start, End)，Open 表示终点开放。
type Interval struct {
	Start   int64
	End     int64 // Open 为 true 时无意义
	Open    bool
	Version Version
}

// ParseCredential 解析乐观并发凭证，格式非法时返回错误。
func ParseCredential(s string) (uint64, error) {
	if !strings.HasPrefix(s, "v") || len(s) < 2 {
		return 0, fmt.Errorf("credential %q: want form \"v<N>\"", s)
	}
	n, err := strconv.ParseUint(s[1:], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("credential %q: %v", s, err)
	}
	return n, nil
}

// CredentialOf 生成版本号对应的凭证串。
func CredentialOf(seq uint64) string { return "v" + strconv.FormatUint(seq, 10) }
