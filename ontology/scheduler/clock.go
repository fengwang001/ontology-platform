package scheduler

import "time"

// now 间接化以便测试中控制时间。
var now = time.Now
