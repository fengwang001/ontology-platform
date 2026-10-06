# OTA 升级活动编排器设计说明

## 结构
- `plan`：排序后的必经版本切片 + 二分查找 `Next(v)`（严格大于 v 的最小必经版本，无则 T）。
- `slot`：在途名额计数器（Acquire/Release/Free），构造时校验容量。
- `campaign`：设备状态机、就绪堆、索引超时堆、令牌、熔断、时钟；单互斥锁串行化全部操作。

## 关键取舍
- 单互斥锁：令牌全局连续无洞、入口结算需全序，串行化天然满足"等价于某个串行顺序"；放弃分片锁/无锁结构（正确性难证，收益不大）。
- 就绪堆 peek 后再 pop：弹出的必是合格设备，`popReady == 实际派发数 ≤ 派发数+1`，与设备总数无关；放弃懒删除（中止会产生大量失效条目，使弹出数随设备总数增长），中止时整体清空就绪堆（此刻所有 Pending 已 Cancelled，堆本应为空）。
- 索引超时堆：Report 落地时按堆索引移除该设备的超时条目，堆中恰好就是全部 InFlight；入口结算 peek 后 pop，`popTimeout == 实际超时数 ≤ 超时数+1`；同样放弃懒删除。
- 被拒操作"只读预检、不落地结算"：结算是 now 的纯函数，拒绝分支用只读预检（ErrNotInFlight 用 `dl<=now` 判定、ErrAborted 扫描超时堆计算"将新增 Failed 数"），被接受的操作才真正结算；放弃"先结算再快照回滚"（回滚字段多、易漏、难维护）。
- 中止后在途设备继续跑完：成功到 T 记 Done、到非 T 必经版本记 Cancelled 但保留版本、失败达 R 记 Failed；放弃"中止即强制取消在途"（违背"其结果照常计入"）。
- 熔断只在 failed==F 那一刻触发一次：全部 Pending 变 Cancelled；之后在途结果仍计入 failed，但不再回到 Pending。
- 时钟只被接受的操作推进；拒绝次序 ErrInvalid > ErrClockBack > ErrUnknown/ErrExists > ErrNotInFlight > ErrStale > ErrVersion > ErrAborted，只报第一个，被拒操作不改任何状态。

## 本地验证
- `go test ./...`：表驱动用例（题面两个例子、v 恰等于必经版本、dl 恰等超时、拒绝次序、令牌连续、popped 的 100/10000 设备对照）。
- 1500 组随机操作序列与朴素模拟（全表扫描 + 排序）逐步对照，日志打印输入、输出与判定依据。
- `go test -race ./campaign` 验证并发等价于串行；`gofmt -l .`、`go vet ./...`。
