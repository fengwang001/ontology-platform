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

## 跨阈值报警流（`alarm` 包）

按键维护累计值（`int64`，从 0 开始，可增可减），在跨越阈值时发出边沿事件，
带迟滞、防抖动，所有判定确定且可复现。

### 边沿触发与迟滞带的精确边界

设阈值为 `T`（必须为正），迟滞带为 `H`（必须为正且 `H < T`），
解除报警下沿为 `L = T - H`（恒为正）。

- 关闭态：当且仅当新累计值 `v >= T` 时发出 `ALARM` 并转开启；
  **恰好等于 `T` 算触发**，`v < T`（含 `v == L`）保持关闭。
- 开启态：当且仅当新累计值 `v < L` 时发出 `CLEAR` 并转关闭；
  **恰好等于 `L` 仍保持开启**，`v >= L`（含再次经过 `T`）不产生事件。
- 其余一切更新（包括迟滞区间 `[L, T]` 内的任意抖动、同态穿越 `T`）
  返回 `NONE`，不追加到事件列表。
- 事件只在状态翻转瞬间产生：不重复（抖动不会连发）、不遗漏
  （任何跨越必由某次更新捕获，判定基于每次更新后的绝对值，不依赖变化速率）。
- 每个事件带全局唯一递增 `Seq`（按提交顺序分配），同键事件按发生顺序保存。

### 失败整体拒绝（可区分原因）

以下情况返回哨兵错误，且**不改变任何键的值、状态与事件列表**，
用 `errors.Is` 区分：

- `ErrNonPositiveThreshold`：`T <= 0`
- `ErrNonPositiveHysteresis`：`H <= 0`
- `ErrHysteresisTooLarge`：`H >= T`
- `ErrEmptyKey`：键为空字符串
- `ErrOverflow`：本次增减使累计值超出 `int64` 范围（上溢或下溢）

### 并发语义

- `Value` / `Armed` / `Events` 可与增减并发调用，`Events` 返回防御性拷贝快照。
- 同一键的增减按互斥串行化，异键互不阻塞；任一线程观察到的结果
  都等价于某种合法的串行交错顺序（线性一致）。

### 用逐步重放核对事件序列

`alarm.Replay(T, H, []Op{{Key, Delta}, ...})` 以严格串行方式逐步施加输入，
返回每一步的事件、累计值、开关状态与当时完整事件列表，作为并发执行的
串行参照。本地验证：

```bash
# 详细日志（输入 / 累计值 / 事件类型 / 判定依据），含边界与重放用例
go test -race -v ./alarm

# 反复跑以验证并发确定性
go test -race -count=20 ./alarm

# 覆盖率
go test -cover ./alarm
```

核对方法：对任意输入序列，先逐行阅读 `-v` 日志中每一步的 `cumulative` 与
`reason`（日志直接给出“与 `T`/`L` 的比较结果 → 状态翻转/保持”）；
再以同一序列调用 `Replay`，逐步比对事件类型、累计值与事件列表
（测试 `TestReplayReference`、`TestConcurrentSameKey`、
`TestConcurrentDifferentKeys` 即按此方法断言）。
