
# thinpool — 精简配置存储池空间管理服务

线程安全的精简卷（thin-provisioned volume）空间管理库：
首次写入才分配物理块、支持超分配上限、每卷保留空间保护、范围回收、
卷扩缩容、保留调整、删除复用与整数水位告警事件。

## 快速上手

```go
package main

import (
	"fmt"

	"ontology/thinpool"
)

func main() {
	p, err := thinpool.New(thinpool.Config{
		PhysicalBlocks: 100, // 物理块总数
		OvercommitPct:  200, // 虚拟块总和上限 = 100*200/100 = 200
		WarningPct:     70,  // 使用率取等即升档
		CriticalPct:    90,
	})
	if err != nil {
		panic(err)
	}

	// 名称非空；虚拟 50 块、保留 20 块。
	if err := p.CreateVolume("vol-a", 50, 20); err != nil {
		panic(err)
	}

	// 首次写入分配一个物理块；已映射块重复写入不分配。
	allocatedNow, err := p.Write("vol-a", 0)
	fmt.Println(allocatedNow, err) // true <nil>

	// 回收范围 [start, start+length)，左闭右开；length=0 为无操作。
	freed, err := p.Reclaim("vol-a", 0, 1)
	fmt.Println(freed, err) // 1 <nil>

	// 扩缩容与保留调整。
	_ = p.Resize("vol-a", 60)
	_ = p.SetReservation("vol-a", 10)

	// 删除释放全部物理块，名称可立即重用。
	_ = p.DeleteVolume("vol-a")

	for _, e := range p.Events() {
		fmt.Printf("event #%d %s->%s at allocated=%d\n",
			e.Seq, e.From, e.To, e.Allocated)
	}
}
```

## API 概览

| 方法 | 语义 |
| --- | --- |
| `New(Config)` | 创建池；非法配置返回 `invalid_argument` |
| `CreateVolume(name, virtual, reservation)` | 建卷，校验超分/保留/欠额 |
| `Write(name, virtualBlock)` | 首次写分配、重复写幂等、越界报参数非法 |
| `Reclaim(name, start, length)` | 回收左闭右开范围，返回释放块数 |
| `DeleteVolume(name)` | 释放整卷，名称可重用 |
| `Resize(name, newVirtual)` | 扩容查超分；缩小查截断区间数据与保留超卷 |
| `SetReservation(name, newReservation)` | 保留超卷/超池/欠额不足校验 |
| `Snapshot()` | 池与各卷一致状态拷贝（占用、欠额、档位） |
| `Events()` | 水位档位变化事件序列拷贝 |

## 错误类别

用 `errors.As` 取得 `*thinpool.Error`，按 `Kind` 判别。报出次序固定为：

1. `KindInvalidArgument` 参数非法
2. `KindVolumeNotFound` 卷不存在
3. `KindVolumeExists` 卷已存在
4. `KindOvercommit` 超分配
5. `KindReservationPool` 保留超池
6. `KindReservationVolume` 保留超卷
7. `KindDataInRange` 卷内仍有数据
8. `KindReservationShortfall` 保留不足
9. `KindPoolExhausted` 池耗尽

一次操作至多报一类（最靠前者）；被拒绝的操作不改变任何状态、不产生事件。

## 并发与确定性

所有方法可并发调用，内部以单把互斥锁串行化，结果等价于某个串行顺序。
映射结构、记账量与事件均为确定性内存状态，相同操作序列在任意实例上重放
得到完全一致的快照与事件序列。

## 进一步阅读

- `DESIGN.md`：不变量、关键取舍、被放弃方案与验证方法。
- `pool_test.go`：需求点名的边界场景。
- `naive_model_test.go` / `differential_test.go`：独立朴素逐块模型与随机对照。
