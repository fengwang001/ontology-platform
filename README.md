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

## state：带墙钟过期清理的键值状态表

`state` 包提供并发安全的键值状态表 `Table`，支持按最后活动时间过期、惰性清除与主动清理。

### 过期判定规则

- 每个条目记录值与**最后活动时间**（lastActivity）。
- 过期判定：`墙钟当前时间 - lastActivity >= ttl` 即过期，**恰好等于 ttl 也算过期**（闭边界）。
- 写入 `Put(key, value, eventTime)` 时，`lastActivity = max(旧值, eventTime)`，只进不退，因此事件时间戳允许乱序、迟到到达。
- 读取 `Get` 时若条目已过期则**惰性清除**并返回未命中；不存在的键同样返回未命中。
- `Cleanup()` 主动清除当前所有已过期条目并返回清除个数；`Check()` 自检返回当前已过期（待清理）条目数，不修改状态。
- 非法输入被拒绝且状态不变：空键返回 `ErrEmptyKey`，非正 ttl 返回 `ErrInvalidTTL`，均可用 `errors.Is` 区分判定；拒绝后表可继续正常使用。

### 双时钟分离的原因

表内存在两个互不相干的时钟：

- **事件时钟**：由写入方随事件携带的 `eventTime`，只用于推进 `lastActivity`。事件可能乱序、迟到、来自不同机器，时钟不可信，因此只取 max、绝不回退，也不参与过期判定的“现在”。
- **清理时钟（墙钟）**：由 `Table` 的 `now` 函数提供（默认 `time.Now`），是过期判定中“现在”的唯一来源。

若混用两个时钟（例如用事件时间当“现在”），迟到的旧事件会把“现在”拉回过去，导致过期判定不可复现、依赖事件到达顺序。分离后：过期判定只依赖墙钟与已稳定的 `lastActivity`，结果与事件到达顺序无关，边界行为（恰好等于 ttl）确定且可复现。测试通过注入假墙钟（`fakeClock`）冻结时间，进一步保证可复现性。

### 并发语义

所有方法（`Put`/`Get`/`Cleanup`/`Check`/`Len`）均可并发调用。`Get` 在锁内一次性取墙钟并判定，返回的值必然与本次调用观察到的墙钟时间及 `lastActivity` 一致，不存在“已过期却返回旧值”的中间态。

### 本地验证

```bash
# 全部测试（含竞态检测与详细日志，日志打印输入、结果与判定依据）
go test -race -v ./state/

# 指定场景
go test -race -v -run TestBoundaryExactlyTTL ./state/        # 边界：恰好等于 ttl
go test -race -v -run TestOutOfOrderEvents ./state/          # 乱序/迟到事件不回退
go test -race -v -run TestCleanupOnlyExpired ./state/        # 主动清理只清过期项
go test -race -v -run TestInvalidInputs ./state/             # 非法输入拒绝且状态不变
go test -race -v -run TestConcurrentNoIntermediateState ./state/  # 并发无中间态

# 代码检查
gofmt -l . && go vet ./...
```
