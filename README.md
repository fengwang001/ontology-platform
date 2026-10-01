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

## Raft 联合共识提交仲裁器（`quorum` 包）

`quorum.Arbiter` 在 Raft 成员变更期间判定日志索引的可提交性，覆盖
单一配置（single）与联合配置（joint consensus, C_old,new）两种形态。

### 法定人数

- 一个含 `size` 个成员的集合，其法定人数为 `floor(size/2)+1`（严格过半）；
  因此偶数集合 `size=4` 需要 3 票，而不是 2 票。
- 单一配置下，索引 `N` 可提交当且仅当当前集合中至少一个法定人数的成员
  满足 `match >= N`。
- 联合配置 `(old,new)` 下，`N` 可提交当且仅当 **old 与 new 各自**都有
  法定人数满足 `match >= N`；同时属于两个集合的节点在两边各计一次，
  绝不对两个集合的并集取多数（否则旧配置少数派可能被绕过）。

### 立即生效规则

- `BeginJoint(newSet, idx)` 成功后联合配置立即生效：之后（包括联合条目
  自身提交之前）的所有判定都按双法定人数执行。
- `FinishJoint(idx)` 成功后新单一配置立即生效，`cfgIdx=idx`，不在新集合
  中的节点被遗忘（其 `match` 丢弃，之后 `Ack` 它们按未知节点拒绝）；
  `newSet` 中此前未知的节点在进入联合配置时即成为已知节点，`match=0`。

### commit 的重算

- 每次成功的 `Ack`、`BeginJoint`、`FinishJoint` 后都重算：取当前配置下
  可提交的最大索引与原 `commit` 的较大者，因此 `commit` 永不下降——
  即使切换到更严格的联合配置后旧索引暂时不再满足双法定人数。
- `FinishJoint` 切换到更宽松的新单一配置后，`commit` 可以一次性上跳到
  新集合多数派已复制的最高索引。
- `Ack(node, idx)` 将该节点 `match` 置为 `max(原值, idx)`；`idx` 小于
  现有 match 不是错误（no-op）。

### 错误与优先级

被拒绝的操作不改变任何状态。各方法按下列顺序只报第一个错误：

- `Ack`：节点未知（`ErrUnknownNode`）。
- `BeginJoint`：当前不是单一配置（`ErrNotSingle`）→ `newSet` 为空
  （`ErrEmptySet`）→ 含重复标识（`ErrDuplicateNode`）→ 含空标识
  （`ErrEmptyNodeID`）→ 与现有集合相同（`ErrSameSet`）→
  `idx <= cfgIdx`（`ErrIndexNotAfterCfg`）。
- `FinishJoint`：当前不是联合配置（`ErrNotJoint`）→
  `idx <= jointIdx`（`ErrIndexNotAfterJoint`）→
  `commit < jointIdx`（`ErrJointNotCommitted`）。即 `commit` 恰好等于
  `jointIdx` 时允许结束，小 1 被拒绝。
- `New`：初始集合为空（`ErrEmptyNodes`）、含空标识或重复同样被拒绝。

所有方法均在互斥锁下执行，可安全并发调用，结果等价于某个串行顺序；
串行重放同一操作序列得到完全相同的 commit 序列与错误序列。

### 日志

仲裁器默认通过标准 `log` 包打印每次操作的输入、输出与判定依据
（如 `joint: majority of old AND majority of new required`）；
可用 `SetLogger` 替换为自定义 `Logger`，传 `nil` 关闭。

### 本地验证

```bash
# 全量测试（含 200 组随机序列与逐索引暴力朴素实现的对照）
go test ./quorum -v

# 竞态检测（含并发 Ack 串行化测试）
go test -race ./...

go vet ./...
gofmt -l .
```
