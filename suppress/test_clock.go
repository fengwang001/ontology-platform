package suppress

import "time"

func timeNowNanos() int64 {
	return time.Now().UnixNano()
}
