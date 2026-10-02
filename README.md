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

## 空闲状态清理器

`IdleStateCleaner` 提供带并发互斥的毫秒级空闲清理：

- `Touch(key, now)` 与 `Advance(now)` 都先把全局时钟推进到 `now`，并原子清理所有 `timer <= now` 的键。
- 清理次序固定为 `timer` 升序，再按 key 的字节序升序；`timer == now` 也会触发。
- 新建键的定时器为 `min(now+Max, now+Hmax)`，并计一次 `timerOps`。
- 已存在键只在 `now+Min > cur` 时才尝试延长；新时刻为 `min(now+Max, created+Hmax)`。若新时刻等于当前定时器，则不替换、不计次。
- 每个键始终只有一个有效定时器；访问只替换同一个堆条目，不额外挂定时器。
- 绝对寿命优先于最大保留：持续访问也不能使定时器超过 `created+Hmax`。寿命到达并触发后，再次 `Touch` 会作为新建处理并重置 `created`。

连续访问时，两次实际重登记之间至少覆盖长度为 `Max-Min` 的时间段，因此每经过 `Max-Min+1` 个整数时刻最多增加一次登记。`Min=10`、`Max=30`、`Hmax` 足够大时，从时刻 0 到 T 逐时访问同一键：

```text
timerOps = floor(T/(Max-Min+1)) + 1 = floor(T/21) + 1
```

例如 T=`100000` 时 `timerOps=4762`。

容量判定发生在清理和时钟推进之前。设 `due` 为当前键表中 `timer <= now` 的数量，则新建键只在：

```text
size - due + 1 > K
```

时返回 `ErrCapacityLimit`。这表示本操作将释放的到期容量可以立即用于新建。若被拒绝，时钟、键表、清理计数和 `timerOps` 都保持不变。

错误按以下优先级只返回第一个原因：

- `Touch`：参数非法（空 key 或 `now` 越界）→ 时钟回退 → 容量不足。
- `Advance`：参数非法（`now` 越界）→ 时钟回退。
- `now == T` 是允许的空推进。

### 本地验证

```bash
# 全量测试
go test ./...

# 查看 2000 组随机序列的输入、输出与判定依据
go test ./ontology -run TestRandomSequencesAgainstNaiveSimulation -v

# 竞态检测
go test -race ./ontology

# 静态检查
go vet ./...
gofmt -l .
```
