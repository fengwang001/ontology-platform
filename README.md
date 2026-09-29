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

## 按键哈希一致采样（`ontology` 包）

对变更事件流按键做一致采样：同一键的所有事件要么全部可见、要么全部不可见；
判定只依赖键与固定哈希，因此多个实例结论一致、可复现；调高采样率时已采键不会被移出。

### 哈希、桶与采样判定

- 哈希：`hash/fnv` 的 **FNV-1a 64 位**，确定性、不随进程或机器变化。
- 桶值：`bucket = fnv1a64(key) % 10000`，范围固定为 `[0, 10000)`，共 `MaxRate = 10000` 个桶。
- 采样率：整数万分比，合法区间 `[0, 10000]`（`0` 全丢，`10000` 全留）。
- 判定规则（严格小于）：`sampled = bucket < rate`，即朴素判定 `BucketSampled(bucket, rate)`。
  - `rate=0`：桶 `0` 也不可见（不是 `<=`）。
  - `rate=9999`：桶 `9999` 不可见；`rate=10000` 时才可见。

### API 与行为

- `NewSampler[V](rate, opts...)`：构造采样器，可选 `WithMaxKnownKeys(n)`、`WithLogger(fn)`。
- `Feed([]Event[V])`：返回被采出的事件，**保持原顺序**；批中所有键登记为已知键。
  整批先校验后提交，任一事件非法即整体拒绝、不留痕。
- `AdjustRate(newRate)`：返回 `RateChange{OldRate, NewRate, Added, Removed}`，
  `Added`/`Removed` 为相对旧率新纳入/被移出的**已知键**，按 `(桶值, 键名)` 升序排序；
  采样率不变时两份列表均为空切片。
- `ShouldSample(key)`：按当前率判定单键（不登记已知键），空键返回拒绝错误。
- `SelfCheck()`：校验采样率范围、每个已知键桶值范围、与朴素判定一致及单调边界。
- `Rate()` / `KnownKeys()`：读取当前采样率与已知键数量。

### 单调性与边界

- 采样率只影响 `bucket < rate` 的阈值，键的桶值永不变，因此**同一键所有事件判定相同**。
- 升高采样率：`Removed` 必为空，已采键继续被采（已采集合单调扩张）。
- 降低采样率：`Added` 必为空；降到 `0` 时所有已知键进入 `Removed`。
- 边界判定严格使用 `<`：`bucket == rate` 时不可见。

### 错误类别（整体拒绝、失败不留痕）

三类原因互不相同，由 `*RejectError{Reason}` 携带，可用 `AsReject(err)` 程序化区分：

| 原因 | 触发条件 |
| --- | --- |
| `ReasonRateOutOfRange` | 采样率 `< 0` 或 `> 10000`（构造或 `AdjustRate`） |
| `ReasonEmptyKey` | `Feed` 批中任一事件键为空，或 `ShouldSample("")` |
| `ReasonTooManyKnownKeys` | 接受本批后已知键数量超过 `WithMaxKnownKeys` 上限（默认 1,000,000） |

任何拒绝都不会改变采样率，也不会登记任何新键（批内合法键同样不登记）。

### 并发

所有方法可被多个执行体并发调用：内部以 `sync.RWMutex` 保护采样率与已知键集合，
判定本身为无状态纯函数。测试以 `go test -race` 覆盖并发喂入、调率、判定与自检。

### 日志

通过 `WithLogger` 注入日志函数后，每一步都会打印输入键、桶值、当前采样率、
判定结果与依据（`bucket < rate => true/false`），调率时逐键打印 `added`/`removed`。

### 本地验证

```bash
# 全量测试（含每步输入、桶值与判定依据日志）
go test -v ./ontology

# 竞态检测（建议多跑几轮）
go test -race -count=10 ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./ontology
go tool cover -func=coverage.out

# 格式与静态检查
gofmt -l .
go vet ./...
```

若 `go` 不在 `PATH`（如安装在 `/usr/local/go/bin`），先执行
`export PATH=$PATH:/usr/local/go/bin`；若默认构建缓存目录只读，可设置
`export GOCACHE=/tmp/go-cache`。
