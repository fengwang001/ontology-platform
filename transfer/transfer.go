// Package transfer 提供住院床位调配与转科服务。
//
// 所有操作可并发调用，内部以单互斥锁串行化，结果等价于某个串行顺序。
// 时钟单调：now 不得小于已接受操作的最大 now；被拒绝的操作不改任何状态。
// 拒绝按次序只报第一个：参数非法 > 时钟回退 > 病区或患者不存在 > 无管理权 >
// 状态不符 > 无可用床或房间不独占。
package transfer

import (
	"errors"
	"fmt"
	"sync"

	"ontology/bedalloc"
	"ontology/ward"
)

// 拒绝原因哨兵，按上报优先级排列。
 toBeKept
