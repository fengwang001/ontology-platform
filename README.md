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

## 按键哈希一致采样（`sampling` 包）

对变更事件流按键做稳定采样：同一键的所有事件要么全部可见、要么全部不可见；
不同实例对同一键的判定完全一致且可复现。

### 哈希、桶与判定规则

- 哈希算法：FNV-1a 32 位（与 Go 标准库 `hash/fnv` 的 `New32a` 一致，纯函数、
  不依赖运行时 map 哈希），逐字节处理键，因此对 UTF-8 键也跨语言/跨进程稳定。
- 桶值：`bucket(key) = fnv1a32(key) mod 10000`，范围固定为 `[0, 10000)`。
- 采样率：整数万分比，取值 `[0, 10000]`，例如 100 表示 1%、5000 表示 50%。
- 判定规则（严格小于）：键被采出当且仅当 `bucket(key) < rate`。
  - `rate = 0`：不采任何键；`rate = 10000`：采出全部键。
  - 边界：桶值恰好等于采样率时**不采**（如 rate=1000 时桶 1000 不采、桶 999 采）。

### 行为

- `New(rate, maxKnownKeys, opts...)`：创建采样器，`maxKnownKeys <= 0` 表示不限制
  已知键数量；可用 `WithLogger` 注入日志，逐步打印输入、桶值与判定依据
  （`rule=bucket<rate`）。
- `Feed(events)`：返回被采样的事件，保持原顺序；批中出现过的所有键登记为已知键。
- `SetRate(rate)`：返回 `(新纳入, 被移出)` 的已知键列表，均按
  **（桶值升序, 键升序）** 排序；采样率不变时两份均为空列表。
- `Sampled(key)`：只读判定单个键，不改变已知键集合。
- `SelfCheck()`：校验采样率范围、已知键数量上限与登记桶值的一致性。

### 单调性

采样集合只取决于桶阈值，因此：

- 调高采样率：已采键继续被采，`被移出` 必为空，`新纳入` 为落入新区间的已知键。
- 调低采样率：`新纳入` 必为空，`被移出` 为掉出区间的已知键。
- 同一键在任一固定采样率下的所有事件判定相同；判定结果可跨实例、跨时间复现。

### 错误类别（整体拒绝、失败不留痕）

错误均为 `*InvalidInput`，可用 `errors.Is` 按互不相同的哨兵原因区分：

- `ErrRateOutOfRange`：采样率越界（构造或 `SetRate` 时 `<0` 或 `>10000`）。
- `ErrEmptyKey`：事件键或待判定键为空字符串。
- `ErrTooManyKeys`：本次喂入登记后已知键数会超过上限（整批拒绝，
  批中尚未登记的新键一个都不会留下）。

任何一次被拒绝都不会改变采样率或已知键集合。

### 并发

`Feed` / `SetRate` / `Sampled` / `Rate` / `KnownKeys` / `SelfCheck`
内部使用读写锁串行化状态变更，可被多个执行体并发调用。

### 本地验证

```bash
# 全量测试（含竞态检测）
go test -race -v ./sampling

# 覆盖率
go test -cover ./sampling

# 步骤日志（输入、桶值、判定依据）随 go test -v 输出
go test -v -run TestSameKeyAllOrNothing ./sampling

# 静态检查与格式
go vet ./...
gofmt -l .
```

测试覆盖：同键全留/全丢与顺序保持、跨实例一致、桶值边界（0、rate-1、rate、
满采样）、与 `hash/fnv` 朴素判定对照、升/降率单调性与排序、三类非法输入、
拒绝后状态不变以及并发读写（`-race`）。
