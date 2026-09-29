# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 物化视图双缓冲重建切换（ontology 包）

`ontology.View` 实现物化视图的双缓冲重建与原子切换，重建期间前台不停服务。

### 双缓冲模型

- **前台缓冲**：始终对外服务。每个新事件立即应用到前台，并按到达顺序追加到全量日志。
- **后台缓冲**：`BeginRebuild` 时记录快照点（当前日志长度），后台缓冲从空开始，
  通过 `ReplayNext` 按日志顺序逐条重放快照点内的历史事件。
- **待补齐列表**：重建期间到达的新事件除应用到前台外，同时记入待补齐列表，
  留待切换前统一补齐到后台缓冲。

### 切换与补齐规则

- `Switch` 在同一把写锁内依次完成：补完快照内剩余历史事件的重放 →
  按顺序把待补齐列表补齐到后台缓冲 → 原子地切换前台指针 → 清空重建状态。
  因此切换结果与“从空朴素重放全量日志”严格一致。
- `AbortRebuild` 丢弃后台缓冲与待补齐列表，前台视图保持不变。
- 切换原子性：所有读（`Get`/`Snapshot`）走 `RWMutex` 读锁，前台指针的替换
  在写锁内一次性完成，任一时刻读到的都是一致的完整视图，不会读到重建中间态。

### 错误约定

四类非法输入各有互不相同的哨兵错误，可用 `errors.Is` 判定；
批内任一事件被拒则整批不生效，失败不改变任何状态：

| 场景 | 错误 |
| --- | --- |
| 非重建中切换或中止 | `ErrNotRebuilding` |
| 重建中重复开始 | `ErrAlreadyRebuilding` |
| 非重建中重放 | `ErrReplayNotRebuilding` |
| 非法事件（键为空 / 增量为零） | `ErrEmptyKey` / `ErrZeroDelta` |

### 本地验证

```bash
# 全部测试（含重建重放、双写、切换补齐、中止、非法输入、并发读写）
go test -v ./ontology/

# 竞态检测
go test -race ./...
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
