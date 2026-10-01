# 节点镜像磁盘回收器（imagereclaim）

`imagereclaim.Reclaimer` 实现一个带层共享记账、两阶段拉取预留、每仓库保留保护与
高/低水位回收的节点镜像磁盘回收器。所有方法可并发调用，内部用一把互斥锁串行化，
语义等价于某个串行顺序；相同操作序列重放得到完全相同的结果。

## 构造

```go
r, err := imagereclaim.New(C, K)
```

- `C`：磁盘容量，`1 <= C <= 10^15` 字节。
- `K`：每仓库保护镜像数，`0 <= K <= 100`。
- 越界返回 `*imagereclaim.Error{Code: ErrConfigInvalid}`，对象不创建。

## 层去重记账

- 每个层按层 ID 全局去重，只保存一份字节与一个引用计数。
- `Used()` 等于“被至少一个镜像（含拉取中镜像）引用的不同层”的字节数之和，
  即引用计数大于等于 1 的层大小之和。
- 新引用已存在的层时只增加引用计数，不重复计字节；引用同一层时字节数必须与
  已存的完全相同，否则报 `ErrLayerConflict`（错误中带冲突层 ID）。拉取中镜像的层
  同样参与占用与冲突比较。
- 镜像删除时逐层递减引用计数；计数归零的层才从磁盘移除并把其字节计入释放量。

## 两阶段拉取的占用规则

- `BeginPull(img, layers, now)`：登记一个“拉取中”镜像，并**立即**占用其新增层字节
  （已存在层只增加引用）。若占用后 `used > C`，整个操作失败且不留任何痕迹
  （`ErrNoSpace`）。
- `CommitPull(img, now)`：拉取中镜像变为就绪，置 `lastUsed = now`、`run = 0`。
- `AbortPull(img, now)`：删除拉取中镜像，仅释放引用归零的独占层。
- `Pull(img, layers, now)` 等价于 `BeginPull` 后紧跟 `CommitPull` 的原子组合。
- 运行状态：
  - `Run(img, now)`：就绪镜像 `run++` 且 `lastUsed = now`；拉取中报 `ErrImagePulling`。
  - `Stop(img, now)`：就绪镜像 `run--` 且 `lastUsed = now`；`run == 0` 时报
    `ErrNotRunning`；拉取中报 `ErrImagePulling`。

仓库名 `repo(img)` 为镜像 ID 中最后一个 `:` 之前的子串；没有 `:` 时为整个 ID。

## GC 语义

```go
res, err := r.GC(now, high, low, minAge)
```

参数约束：`0 < low < high <= 100`，`minAge >= 0`，`now >= 0`。成功的 GC 同样推进
已接受的最大时间戳（时钟回退判定覆盖 GC）。

### 触发条件与回收目标

- 当 `used*100 < high*C` 时不做任何回收，返回空结果（无不足标记）。
- 否则需要回收 `need = used - floor(C*low/100)` 字节（低水位按整数向下取整）。

### 保护集（GC 开始时一次算定）

对每个仓库，取其全部**就绪**镜像（含运行中者，不含拉取中者），按
`lastUsed` 降序、`lastUsed` 相同时按镜像 ID 字节序升序排列，前 `K` 个进入保护集。
保护集在本次 GC 中固定，不因删除而重算。

### 候选与删除顺序

候选必须同时满足：就绪、`run == 0`、不在保护集、`now-lastUsed >= minAge`。
候选按 `lastUsed` 升序、相同时按镜像 ID 字节序升序逐个删除：

- 每删除一个镜像，把引用计数归零的层字节累计进 `Freed`。
- `Freed >= need` 立即停止；即使某次删除新增释放为 0（层全部被保留镜像共享），
  该镜像也照常删除，并继续考察下一候选。
- 候选用尽仍 `Freed < need` 时，已删除的保持删除，结果中 `Short = true`。

GC 永不删除受保护、运行中或拉取中的镜像。

## 拒绝原因及优先级

错误统一为 `*imagereclaim.Error`，按以下顺序只报第一个：

1. 参数非法 `ErrInvalidParam`：镜像 ID 空、层列表空、层 ID 空、层字节越界
   （`1..10^12`）、同一镜像内层 ID 重复、`now < 0`、GC 参数不合法。
2. 时钟回退 `ErrClockRewind`：`now` 小于此前已接受操作见过的最大时间戳。
3. 镜像已存在 `ErrImageExists`（含拉取中者）。
4. 镜像不存在 `ErrImageNotFound`。
5. 状态不符：对拉取中镜像 `Run/Stop` 报 `ErrImagePulling`；对就绪镜像
   `CommitPull/AbortPull` 报 `ErrImageReady`，二者可区分。
6. 层冲突 `ErrLayerConflict`（带层 ID，优先于空间不足）。
7. 空间不足 `ErrNoSpace`。
8. 未在运行 `ErrNotRunning`。

构造参数越界单独报 `ErrConfigInvalid`。被拒绝的操作不改变镜像、层引用与 `used`，
也不推进最大时间戳。

## 本地验证

```bash
# 全量测试（含 2000 组随机朴素对照、并发、重放）
go test ./...

# 竞态检测
go test -race ./imagereclaim

# 只看边界用例
go test -run 'TestSpecExample|TestGCThresholdBoundary|TestMinAgeBoundary|TestTieBreaks' ./imagereclaim

# 随机对照日志（输入、两侧输出、used 与判定依据逐行打印）
go test -run TestRandomAgainstNaive -v ./imagereclaim
# 测试输出中会给出形如 /tmp/imagereclaim-diff-*.log 的日志路径

go vet ./...
gofmt -l .
```

随机对照测试把操作序列同时喂给正式实现与一份独立编写的朴素模拟器
（`naive_test.go` 中“每步重新汇总去重层”的直白实现），逐条比较拒绝码、冲突层 ID、
被删镜像序列、释放字节、不足标记、`used` 以及完整镜像/层引用快照；并发测试在
`-race` 下验证“并发拉取同一镜像恰一次成功”与 `0 <= used <= C` 不变量；重放测试
验证同一序列两次运行的 GC 结果与最终 `used` 完全一致。
