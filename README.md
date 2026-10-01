# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 分区副本重分配控制器（`reassign` 包）

`reassign.Controller` 在保持分区可用的前提下，把分区的副本集合迁移到目标列表：
新副本追平（进入 ISR）后才移除旧副本。所有方法可并发调用，内部以单互斥锁串行化，
效果等价于某个串行顺序；相同事件序列得到完全相同的状态。

### 数据模型

- 每个分区：有序副本列表、同步副本集（ISR，副本列表的子集）、领导者（ISR 成员或无）。
- 每个节点：存活或宕机；节点须先经 `AddNode` 注册。

### 完成规则

- `StartReassign(id, T)` 后，进行中的副本列表为「原列表后接 T 中新增节点（按 T 次序）」。
- 开始与每次 `ReportCaughtUp` 后检查：若 T 的成员全在 ISR，则完成——
  - 副本列表改为 T 的次序，ISR 取与 T 的交集；
  - 领导者不在 T 内时，取 T 中第一个在 ISR 的成员（按 T 次序），否则不变。
- T 与原列表成员相同、仅次序不同时，开始即完成且领导者不变。

### 取消规则

`CancelReassign(id)` 把副本列表恢复为原列表，ISR 取与原列表的交集；
领导者不在原列表中时，取原列表中第一个在 ISR 的成员（无则无领导者）。

### 领导者选取

- 追平上报使存活且不在 ISR 的副本列表成员加入 ISR；分区无领导者时首个加入者成为领导者。
- 节点宕机使其退出所有分区的 ISR；若为领导者，改选副本列表中第一个在 ISR 的成员（无则无）。
- 节点恢复只改存活标记，须再次追平上报才重新进入 ISR。

### 错误次序

被拒绝的操作不改变任何状态，且按下列次序只报第一个错误（均为可 `errors.Is` 区分的哨兵错误）：

- 开始：`ErrPartitionNotFound` → `ErrReassignInProgress` → `ErrInvalidTarget`（空、重复、含未知节点）
  → `ErrTargetNodeDown` → `ErrTargetUnchanged` → `ErrConcurrencyLimit`。
- 追平上报：`ErrPartitionNotFound` → `ErrNotInReplicas` → `ErrNodeDown` → `ErrAlreadyInISR`。
- 取消：`ErrPartitionNotFound` → `ErrNoReassign`。

### 本地验证

```bash
go test -race -v ./reassign/   # 日志打印每个用例的输入、输出与判定依据
go vet ./... && gofmt -l .
```

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
