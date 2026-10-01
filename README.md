# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 滚动更新步进计划器

`Config` 使用整数参数 `DesiredReplicas N`、`MaxSurgePercent PS`、`MaxUnavailablePercent PU`、`MinReadyMillis MR`、`StallSteps P` 创建 `RollingUpdatePlanner`。

### 上限与可用数

- 超量上限使用向上取整：`S = ceil(N * PS / 100)`。
- 不可用上限使用向下取整：`U = floor(N * PU / 100)`。
- 当 `S == 0 && U == 0` 时，将 `U` 调整为 `1`，保证至少可以推进一个旧实例。
- 实例分为 `a`（旧版本就绪）、`b`（旧版本未就绪）、`c`（新版本就绪）、`d`（新版本未就绪）。
- 新版本就绪实例按 `readyAt` 分批保存；批在 `now - readyAt >= MR` 时稳定，稳定批之和为 `cs(now)`；`MR == 0` 时 `cs == c`。
- 可用数只计入旧版本就绪和稳定的新版本就绪：`A(now) = a + cs(now)`。

### Step 计算次序

`Step(now)` 在调用开始时锁定状态，并用调用开始时的总数与可用数一次算出：

```text
up = max(0, min(N + S - (a+b+c+d), N - (c+d)))
r1 = b
r2 = min(a, max(0, A(now) - (N-U)))
```

三个量同时生效：`d += up`、`b -= r1`、`a -= r2`。因此同一步新建数不会因为本步清理 `b` 而变大，缩减数使用的也是调用开始时的 `A`。

生效后先判断 `Done`：若完成则停滞计数清零；否则 `up+r1+r2 > 0` 清零，其他情况加一。返回值中的 `stalled` 等于停滞计数是否已达到 `P`。

### 状态变更与拒绝顺序

- `NewReady(k, now)`：从 `d` 转 `k` 到 `c`，追加 `readyAt=now` 的批次，并清零停滞计数。
- `NewFail(k, now)`：按 `readyAt` 从大到小扣除批次，从 `c` 转回 `d`，不清零停滞计数。
- `OldUnready(k, now)`：从 `a` 转 `k` 到 `b`，不占用不可用额度，不清零停滞计数。
- 操作按“参数非法 → 时钟回退 → 超出范围”的顺序返回第一个错误；`NewReady`、`NewFail`、`OldUnready` 的超范围错误分别可区分。
- `Done()` 等查询不加拒绝；被拒绝的操作不会修改实例数、就绪批次、已接受时间或停滞计数。
- `Done()` 要求 `a+b == 0`、`c == N`，并且以最近一次接受操作的 `now` 计算 `cs == N`。

所有操作通过同一把互斥锁串行化，因此并发调用等价于某个合法串行顺序。

### 本地验证

```bash
# 普通测试
go test ./...

# 竞态检测
go test -race ./...

# 查看 2000 组随机序列与朴素模型对照的输入、输出和判定依据
go test -run TestRandomSequencesMatchNaiveModel -v
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
