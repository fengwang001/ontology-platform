# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
go run ./cmd/server

# 编译后运行
go build -o bin/server ./cmd/server
./bin/server
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestObjectType ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## 节点准入与驱逐管理器（`nodeadmission` 包）

`nodeadmission.Manager` 管理节点污点、Pod 容忍与 NoExecute 驱逐。所有方法都以单一互斥锁串行化，并发调用的结果等价于某个合法串行顺序；被拒绝的操作不改变任何状态。

构造：`NewManager(G, R)`，`G` 为终止宽限毫秒数（0 到 10^12），`R` 为每节点每次 Tick 驱逐上限（1 到 10^6），越界返回 `ErrInvalidConfig`。`now` 一律为 0 到 10^15 的毫秒。

### 容忍匹配规则

污点为 `(key, value, effect)`，效果为 `NoSchedule` / `PreferNoSchedule` / `NoExecute`；容忍为 `(key, operator, value, effect, seconds)`。

- `effect` 为空串时匹配污点的任意效果；否则必须与污点效果完全相等。
- `key` 为空串时 `operator` 必须为 `Exists`，匹配任意键；否则键必须相等。
- `operator=Exists` 时值必须为空串，匹配该键而忽略值；`operator=Equal` 时值必须相等。
- `seconds=-1` 表示永久容忍；只有效果恰为 `NoExecute` 的容忍允许 0 到 10^15 的秒数，其他容忍（含效果为空串者）的秒数必须为 -1。

### 驱逐时刻推导与取最小

驱逐时刻不单独存计时器，每次由当前污点集合与 Pod 容忍实时推导：

- 对节点上每个 `NoExecute` 污点，按容忍声明顺序取第一个匹配该污点的容忍：无匹配则 `d=addedAt`；匹配且秒数为 -1 则该污点贡献无穷；否则 `d=addedAt+seconds*1000`。
- Pod 的驱逐时刻取所有污点 `d` 的最小值；没有 NoExecute 污点或全部为无穷时永不驱逐。
- 计时从污点 `addedAt` 起算，而非从 Pod 绑定起算。`Taint` 对已存在的 `(key, effect)` 只替换值、保留 `addedAt`；`Untaint` 后重新添加视为新污点，`addedAt` 为当时的 `now`（因此把值换成 Equal 容忍不再匹配时，Pod 会因旧 `addedAt` 立即到期）。

### 限速与终止占位

- `Tick(now)` 先释放已过宽限期的终止中 Pod，再在每个节点取运行中且驱逐时刻 `<= now` 的 Pod，按（驱逐时刻升序，相同按 Pod ID 字节序）只驱逐前 `R` 个；其余留待下次 Tick。某节点 Tick 后仍有 `<= now` 的到期 Pod，当且仅当本次在该节点恰好驱逐了 `R` 个。
- 被驱逐的 Pod 进入终止中，释放时刻为 `now+G`；`now >= releaseAt` 起视为不存在（不占容量、ID 可重用）。`G=0` 时当次 Tick 内立即释放。终止中的 Pod 不再被驱逐，但仍占用 `maxPods`，且同 ID 在释放前重绑被拒。
- Tick 返回值汇总本次全部被驱逐的 Pod ID，按（驱逐时刻升序，Pod ID 字节序）排列；相同操作序列重放得到完全相同的结果。

### 拒绝原因与顺序

拒绝通过 `*RejectError` 的 `Code` 区分，按下述顺序只报第一个：

1. `ErrInvalidConfig`：构造参数 `G`/`R` 越界（整体拒绝创建）。
2. `ErrInvalidArgument`：节点名/Pod ID/污点键为空、`maxPods` 越界、效果非法、容忍形状非法（运算符/键/值/秒数）、`now` 越界。
3. `ErrClockMovedBack`：`now` 小于此前被接受的 `Taint`/`Untaint`/`Schedule`/`Tick` 见过的最大 now（`AddNode` 不参与时钟）。
4. `ErrPodExists`：Schedule 的 Pod 仍在运行或终止中。
5. `ErrNodeNotFound`：Schedule/Taint/Untaint 的节点不存在。
6. `ErrUntoleratedTaint`：不可容忍污点；详情按（键字节序，效果字节序）给出第一个不可容忍污点。
7. `ErrCapacity`：运行中加终止中 Pod 数已达 `maxPods`。

另有 `ErrNodeExists`（AddNode 重名，先判参数非法再判重名）与 `ErrTaintNotFound`（Untaint 污点不存在，先判参数非法、时钟回退、节点不存在）。

### 本地验证

```bash
# 全部测试（含 -race）
go test -race ./nodeadmission/ -v

# 只跑 2000 组随机序列与朴素模拟对照
go test ./nodeadmission/ -run TestRandomDifferential -v

# 对照测试会把最近 100 组的“输入 / 输出 / 判定依据”写入
# /tmp/diff_run.log（与朴素模拟器逐操作比对返回码、驱逐列表、
# 污点 addedAt、Pod 生命周期与每节点占用数）
go vet ./...
```
