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

## 加密序号分配器（`keystream` 包）

`keystream` 为每个租户分配全局不重复的（密钥版本，序号）。租户注册后从
版本 1、序号 0 起；每版本序号为 `0..N-1`，用尽后进入下一版本并从 0 起，
版本数至多 `V`。

### 线性位置与高水位

把「跨版本累计槽位」编码成一个单调不减的整数 `pos`：

- 版本号 `version = pos / N + 1`，版本内序号 `seq = pos mod N`。
- 每个租户维护两个位置：
  - `reserved`：已向持久层预留的高水位（持久化的唯一事实来源）。
  - `issued`：内存中已发放/作废的位置，恒有 `issued <= reserved`。
- 可发放区间是 `[issued, reserved)`；`issued == reserved` 时触发下一批预留。

### 批预留与版本边界截断

每次预留只写一条高水位记录，逻辑上预留 `B` 个序号；批次不跨版本边界：

```
本版本剩余 = N - reserved mod N
本批大小   = min(B, 本版本剩余)
新高水位   = reserved + 本批大小
```

例：`N=5, B=3` 时高水位轨迹为 `3 -> 5 -> 8 -> 10 -> ...`，其中 `[3,5)`
与 `[8,10)` 是被版本边界截断的两批。只有新高水位确认落盘后才发放批内
序号，因此持久层里「已预留到何处」与「哪些序号可能曾被发出」是一致的。

### 两种预留失败

预留返回的错误统一为 `*PersistError`，可用 `errors.As` 与业务错误
（`ErrEmptyTenant` / `ErrTenantNotFound` / `ErrKeyExhausted` 等）区分：

- **明确失败（`Unknown=false`，确定未落盘）**：内存与持久层高水位都
  不变，本次分配失败；下一次分配重试同一批，不浪费任何序号。
- **结果未知（`Unknown=true`，可能已落盘）**：按最坏情况处理——把该批
  视为已预留且整批作废（`reserved` 与 `issued` 一并推进到批尾），本次
  分配失败；下一次分配从该批之后重新预留。任何非 `PersistError` 的错误
  （如 `context` 取消/超时）都无法证明未落盘，同样按结果未知处理。

### 崩溃恢复起点

崩溃意味着内存发放位置丢失，但持久层高水位 `reserved` 仍然有效。
恢复时令 `issued = reserved = 持久层高水位`：

- 已预留但未发放的序号一律作废，绝不重放，因此崩溃后不会与崩溃前已
  发出的任何（版本，序号）重复。
- 每次崩溃浪费的序号数等于「上一批中尚未发出的个数」，批大小不超过
  `B`，故每次崩溃浪费严格不超过 `B`。
- 用 `New(store, logger)` 新建分配器后调用 `RecoverAll(ctx)`（或对单个
  租户调用 `Recover(ctx, tenant)`）即可从高水位继续。

### 并发语义

每个租户一把互斥锁加条件变量：批未耗尽时并发调用各自领取批内序号；
批耗尽时只有一个协程执行预留，其余协程在条件变量上等待并共用同一次
预留结果，不会各自预留重叠范围。任意交错、失败与崩溃下，同一版本内
发放的序号互不相同；无崩溃与失败时，版本内发放的序号恰为从 0 起的
连续前缀。相同的操作序列配合相同的故障脚本产生相同的发放结果
（见 `ScriptedStore`）。

### 本地验证

```bash
# 全量用例（含边界截断、崩溃恢复、两种失败、耗尽、多租户、百并发、确定性）
go test -race -v ./keystream

# 反复跑以压并发交错
go test -race -count=20 ./keystream

# 覆盖率
go test -coverprofile=coverage.out ./keystream
go tool cover -func=coverage.out
```

测试日志通过 `testing.T.Logf` 打印每次操作的输入、输出与判定依据
（`reason=...`），用 `-v` 即可查看。
