# 近实时索引引擎设计说明

## 分层
- `translog`：分代追加日志，每条记录含 gen 内偏移、seq、op、id、body。维护 synced 落盘水位；
  Crash 截断 seq>synced 的尾部，Rotate 换代并清空，Replay 按序输出 (committed, synced] 的记录。
- `searcher`：只读搜索视图 map[id]Doc，Apply 并入内存段（含删除墓碑），Commit 整体替换提交点，
  LoadCommit 从提交点装回视图。无锁，由 engine 串行化。
- `engine`：一把 RWMutex 串行化全部操作，持有版本表、实时段、提交段、translog、maxSeq/committed/synced。

## 取舍
- Refresh 只合并内存、不落盘：可搜索性与持久性解耦；刷新过但未 Sync 的操作在 Crash 后丢失，
  持久性完全由 translog 水位保证。
- Flush 固定为 Refresh→提交（committed=synced=maxSeq）→Rotate；Rotate 丢弃整代旧日志，
  故未提交操作数恒等于日志条数（committed..maxSeq 恰好是当前代全部记录）。
- 条件写/Delete 按实时状态判定（版本表优先，其次搜索视图），不按搜索视图：实时语义先于可见性。
- Crash 后 maxSeq 回到 synced，丢失序号被重新分配：序号只承诺被接受操作的可线性化顺序，
  丢失的操作不留洞，保证“相同序列重放结果相同”。
- Recover 只重放 (committed, synced]，末尾一次 Refresh；committed 不变、日志不截断，
  因此再次 Crash/Recover 的输入完全相同，幂等。replayed=synced-committed，与提交文档数无关。
- 拒绝次序：参数非法 > 未恢复 > 版本冲突/文档不存在；被拒绝操作不占序号、不写日志、不落盘。
- Get 实时查找：先查版本表，未命中再查实时段，再查提交段，触碰记录 ≤2（版本表 + 一个视图）。

## 放弃的方案
- 刷新手落盘（违背“刷新不等于持久”，且无法演示刷新后崩溃丢失）。
- 恢复后保留丢失序号留洞（语义复杂且使重放结果依赖崩溃历史，不可重现）。
- 条件写按搜索视图判定（已 Refresh 与未 Refresh 行为会不同，违反实时一致性）。
- translog 物理文件/fsync：用内存切片+水位精确模拟断电截断，语义等价且可重复。

## 本地验证
- `go build ./...`；`gofmt -l . && go vet ./...`
- `go test -race ./...`；随机差分：1500 组序列，每个位置插入 Crash/Recover，
  与逐步朴素模型逐操作对照，日志打印输入、输出与判定依据（见 engine/engine_diff_test.go）。
