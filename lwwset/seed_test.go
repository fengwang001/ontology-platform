package lwwset

import "time"

func timeNowSeed() int64 {
	return time.Now().UnixNano()
}
