# 设计说明（kitchen：商家出餐节奏与压单控制）

## 模块划分
1. `types.go`：Config/OrderRequest/AdmitResult/OrderInfo/PressureEvent 等对外类型。
2. `errors.go`：单一 `*Error{Code,...}`，10 个错误码枚举，`errors.As` 程序化区分。
3. `estimate.go`：纯函数排产模拟器 `allocateSlots`，制作位释放时刻最小堆 +
   预约/即时双有序队列贪心，返回每单推定开工时刻，O((P+W)log P)。
4. `pressure.go`：压单滞回状态机，进入/退出仅在规定时点判定，事件严格交替
   （panic 守护该不变量）。
5. `kitchen.go`：显式制作位表（占用者+变空时刻）、事件驱动 `pump`、准入/
   完成/取消/暂停/恢复生命周期，一把 `sync.Mutex` 串行化全部操作。

## 关键取舍
- 显式制作位 + 事件驱动 pump，而非“每 tick 重算整个世界”：提前完成只需把
  对应位 freeAt 锚到报告时刻，后续开工可精确复现，且不依赖后台线程。
- 订单到声明完成时刻时制作位可被后续订单“接续”，但完成状态仍以商家报告为准；
  自动接续的旧单进 `unreleased` 索引，迟到报告只记账并补做一次压单退出推定。
- 拒绝路径（暂停/预约过近/爆单/状态错误）先用纯函数副本预演判定，
  确认接受后才推进时钟与队列，保证“被拒绝操作零副作用”。
- 压单承诺取“接单当时”的推定完成时刻；正常单承诺 = now+duration+进入阈值。
- 预约单未到目标开工时刻不进任何空位竞争；同时刻按 预约 > 即时 插队。

## 被放弃的方案
- 纯全局重推（每次从全部在制订单重算）：会把“提前完成”的位移合丢失，
  导致后续单开工时刻错误回溯；改为显式 freeAt 锚点。
- 后台逐秒 goroutine 自动推进：破坏“同一操作序列重放确定性”，改为由
  被接受操作（或显式 Tick 心跳）驱动 pump。
- 朴素模型直接复用生产排产：失去对照意义；测试中的 naive 模型完全独立实现。

## 本地验证
```bash
go test ./...                 # 单元 + 40 组随机逐秒对照
go test -race ./...           # 并发安全（60 goroutine 接单不突破并行上限）
go test -cover ./kitchen/     # 约 93%
go test -bench BenchmarkAdmit -run '^$' ./kitchen/
# 历史 0/2000/20000、live 50/400：耗时只随 live 增长，不随 history 增长。
KITCHEN_DIFF_VERBOSE=1 go test -run TestDifferentialAgainstNaive -v ./kitchen/
```
