// Package tzone 提供时区装载与本地日历日界判定，不依赖其他包。
package tzone

import (
	"errors"
	"time"
)

// ErrBadZone 表示传入了非法的 IANA 时区名。
var ErrBadZone = errors.New("tzone: invalid time zone name")

// dateFormat 是桶标识的本地日历日期格式。
const dateFormat = "2006-01-02"

// Zone 持有一个已校验的 IANA 时区。
type Zone struct {
	loc *time.Location
}

// Load 装载 IANA 时区；名字非法时返回 ErrBadZone，不产生任何状态。
func Load(name string) (*Zone, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrBadZone
	}
	return &Zone{loc: loc}, nil
}

// Date 返回 Unix 秒 ts 在该时区下的本地日历日期 YYYY-MM-DD。
// 本地午夜是日界，午夜属于新的一天。
func (z *Zone) Date(ts int64) string {
	return time.Unix(ts, 0).In(z.loc).Format(dateFormat)
}

// Location 暴露底层 *time.Location，供参照实现逐事件核对。
func (z *Zone) Location() *time.Location {
	return z.loc
}
