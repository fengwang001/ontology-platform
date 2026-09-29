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

## 化身号成员状态传播

`NewNode(self, members, lambda, B, timeout, logWriter)` 创建本地节点。消息结构为 `Message{Member, Status, Incarnation}`，状态包括 `Alive`、`Suspect`、`Dead`。

### 覆盖规则

- `Dead`（确认失效）覆盖任意存活或可疑状态；已经确认失效后，任何后续消息都不能恢复。
- `Alive(i)` 只在 `i > j` 时覆盖 `Alive(j)` 或 `Suspect(j)`。
- `Suspect(i)` 在 `i >= j` 时覆盖 `Alive(j)`，在 `i > j` 时覆盖 `Suspect(j)`。
- 不满足上述优先级的消息会被丢弃，不进入捎带缓冲，也不会改变任何视图。
- 上述规则是顺序无关的偏序合并；同一批消息乱序或重复投递，最终逐项视图相同。

### 自证与超时

- 节点收到关于自己的 `Suspect(i)`，且 `i` 不小于本地当前化身号时，会立即把本地视图改为 `Alive(i+1)`，并把该存活更新加入外发缓冲。
- 节点收到关于自己的 `Dead` 后立即退出；退出后接收、推进时钟和获取外发消息都会返回 `RejectNodeExited`。
- `AdvanceClock` 使用注入的单调时钟。可疑状态持续到 `now - suspectTime >= timeout` 时升级为 `Dead`；时长恰好等于超时值也会升级。

### 捎带传播

- 每条被接受的更新最多传播 `lambda * ceil(log2(n+1))` 次，其中 `n` 是名单人数。
- 每次调用 `Outgoing` 最多返回 `B` 条更新。
- 候选更新先按已发送次数升序选择，次数相同时按成员标识字典序升序选择。
- 同一成员的新更新会替换缓冲中的旧更新，并把已发送次数重置为零；完全相同的重复更新不会重复入队。

### 拒绝原因

整批操作会先校验再落状态，因此被拒绝时不会改变任何视图或缓冲。可通过 `RejectError.Reason` 区分：

- `negative_incarnation`：化身号为负。
- `unknown_member`：成员不在名单中，或构造时自身不在名单中。
- `invalid_parameter`：`lambda <= 0`、`B <= 0`、超时非正、状态非法或时钟回退。
- `node_exited`：对已退出节点继续操作。

所有输入、合并输出、外发结果和判定原因都会写入构造时提供的日志 `io.Writer`；传 `nil` 时丢弃日志。

### 本地验证

```bash
go test ./...
go test -race -v ./...
go vet ./...
gofmt -l .
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
