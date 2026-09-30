# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## flowctl：多流发送端两级流量控制记账器

`flowctl` 包实现连接级 + 流级两级窗口的发送记账，所有方法并发安全，
相同调用序列重放结果完全相同。

### 核心规则

- **两级扣减**：`Send(id, n)` 仅在连接窗口与该流窗口都够用时放行，
  全有或全无，两处同时扣减 `n`；`n` 还须为正且不超过帧长上限 `F`。
- **窗口上限**：所有窗口（连接窗口、各流窗口、初始窗口 `I`）上限均为
  `2^31 - 1`；构造时窗口初值为负或超限、`F` 非正或超限均拒绝构造。
- **开流 / 关流**：`OpenStream(id)` 使用未用过的正整数编号，窗口取当时
  的 `I`；`CloseStream(id)` 后编号不可复用，其未用窗口**不回补**连接窗口。
- **窗口增量**：`Increment(ConnID, delta)` 增加连接窗口，
  `Increment(id, delta)` 增加某流窗口；`delta` 须为正，加后超过上限则拒绝。
- **追溯调整**：`AdjustInitial(I')` 将每个**未关闭**流的窗口加上
  `I' - I`（可为负，窗口为负时不可发送，直至被增量补正），连接窗口不变，
  此后新流取 `I'`；若任一未关闭流调整后超过上限，则整体拒绝、所有流都不改。
- **拒绝顺序**（只报第一个，被拒绝的操作不改变任何状态）：
  - 发送：流不存在 → 流已关闭 → `n` 非正 → `n` 超过 `F` →
    `n` 超过连接窗口 → `n` 超过流窗口（窗口为负归此项）。
  - 增量：流不存在 → 流已关闭 → 增量非正 → 连接窗口溢出 → 流窗口溢出。
  - 调整：`I'` 为负或超限 → 任一未关闭流调整后超限。
  - 开流：编号非正 → 编号已用过。

### 本地验证

```bash
# 运行全部测试（日志打印每步的输入、输出与判定依据）
go test -v ./flowctl

# 带竞态检测验证并发安全
go test -race ./flowctl
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
