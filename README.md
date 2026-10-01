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

## Raft 联合共识提交仲裁器（`raft` 包）

`raft.Arbitrator` 依据各节点的匹配索引（match）在单一配置与联合配置之间
推进提交索引（commit）。所有方法可并发调用，效果等价于某个串行顺序；
相同操作序列重放得到完全相同的 commit 序列与错误序列。

### 法定人数与提交判定

- 一个集合的法定人数为 `⌊size/2⌋+1`（如 3 人需 2 人，4 人需 3 人）。
- 单一配置：索引 N 可提交当且仅当至少一个法定人数的成员满足 `match ≥ N`。
- 联合配置 `(old, new)`：old 与 new **各自**都要有法定人数的成员满足
  `match ≥ N`；同属两个集合的节点在两边各算一次，**不是**对并集取多数。
- 每次状态变化后重算：commit 取当前配置下可提交的最大 N 与原 commit 的
  较大者——commit 永不下降，即使切换配置使原先可提交的索引暂时不再满足。

### 操作语义

- `Ack(node, idx)`：`match[node] = max(原值, idx)`（idx 小于原值不是错误），
  重算并返回 commit。node 未知时返回 `ErrUnknownNode`。
- `BeginJoint(newSet, idx)`：立即进入联合配置 `(old, newSet)`（不等联合条目
  提交），`jointIdx = idx`；newSet 中未知节点成为已知且 match 为 0。
- `FinishJoint(idx)`：立即切换为单一配置 newSet，`cfgIdx = idx`；不在
  newSet 中的节点被遗忘（match 丢弃，之后 Ack 按未知节点处理，重新加入时
  match 从 0 起）。

### 错误优先级（只报第一个，被拒操作不改变任何状态）

- `BeginJoint`：非单一配置 → newSet 为空 → 含重复 → 含空标识 → 与现有
  集合相同 → `idx ≤ cfgIdx`。
- `FinishJoint`：非联合配置 → `idx ≤ jointIdx` → `commit < jointIdx`
  （`commit == jointIdx` 允许结束）。

### 本地验证

```bash
# 单元测试 + 随机序列与逐索引暴力朴素实现对照 + 并发/重放一致性
go test -race -v ./raft/

# 查看随机对照测试中每步的输入、输出与判定原因
go test -v -run 'TestRandomSequencesMatchNaive/seed=0' ./raft/
```
