# 跨系统自增编号对齐器 — 设计说明

## 结构
- `stride`：纯函数。`AlignUp(x, c, m)` 求不小于 x 且模 m 余 c 的最小整数；`Mode(active, m)` 给步长（活跃 1 个为 1，否则为 m）。
- `cut`：成员变更。`State{Next, Active, ActiveCount, J}` 上的 `Join`/`Leave`/`HighWater`，按"参数→权限→状态"次序校验。
- `seq`：门面。`Allocator` 持有 `cut.State` 与一把互斥锁，提供 `Issue/Reserve/Observe/Join/Leave`，全部操作在锁内完成，并发调用等价于某个串行序。

## 取舍与被放弃的方案
1. **同余类交错签发（对齐处跳号） vs 预留互不重叠号段**：选交错。号段方案需静态切分编号空间，系统加入/退出时要重新分段，低水位系统的号段被浪费且无法回收；交错方案只在 Join/Leave/Observe 的对齐处跳号，跳号全部计入 J，可精确复现，且活跃数 ≥2 时各系统编号模 m 余 c_s 天然互不相交。
2. **单系统恢复取含离开者的全局高水位 vs 保持步长 m 不变**：选高水位接续。保持步长 m 会让幸存系统沿自己的同余类继续，可能重发已退出系统已签发的编号（规格例一：系统 3 已签发的 14 会被系统 2 重发）；取 H'（含刚离开者）并把步长降为 1 后，新编号严格大于任何已签发编号。代价是最后一次 Leave 可能跳一段号（计入 J）。
3. **Join 起点取全局高水位 vs 取自身 next**：选 `alignUp(max(next_s, H), c_s)`。只取自身 next 会在系统退出又重入时回退到旧计数器，重发他系统已用的号；取 H 保证新起点不低于任何系统的 next。H 必须在对齐 t 之前取定，否则结果依赖内部计算顺序、无法复现。

## 关键不变量
- 任意编号至多被一个系统签发一次且 ≤ MaxID；每系统签发严格递增；next 单调不减。
- 活跃数 ≥2 期间，各系统转入步长 m 后签发的编号 ≡ c_s (mod m)，同余类互不相交。
- 被拒操作不改变 next/Active/J；校验次序：参数越界 → 权限不足 → 状态类（ErrInactive/ErrState/ErrLast）→ ErrExhausted。
- Issue/Reserve 只触碰 1 个系统的状态（ActiveCount 由 Join/Leave 增量维护，签发路径不扫描全部系统）。

## 本地验证
- `go test ./...`：全量单测，含规格中四个例子的逐步断言。
- `go test -race ./...`：并发签发下的全局唯一性。
- `go test -v ./seq -run TestRandomReplay`：2000 组随机操作序列与朴素模拟对拍，日志打印输入、输出与判定依据。
- `gofmt -l . && go vet ./...`。
