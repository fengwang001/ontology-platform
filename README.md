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

## 键值状态表

`kvstate.Table` 是带墙钟过期清理的并发安全键值表：

- `Put(key, value, eventTime, ttl)` 保存值、事件时间和该条目的过期时长。
- 每次写入都按 `lastActivity = max(lastActivity, eventTime)` 更新，乱序或迟到事件不会让最后活动时间回退。
- `Get(key)` 使用当前墙钟做一次原子判定；已过期时惰性删除并返回 `(nil, false)`，不存在的键同样返回无匹配。
- `PurgeExpired()` 使用当前墙钟扫描并删除所有过期条目，返回删除数量。
- `Check()` 只读检查内部不变量，可与读取、写入和主动清理并发调用。

过期判定规则为：

```text
wallClockNow - lastActivity >= ttl
```

差值恰好等于 `ttl` 时也算已过期。该判定与写入携带的事件时间使用两个不同时钟：事件时钟只描述业务事件何时发生，允许乱序；墙钟只负责读取或清理瞬间的过期判断。分离两者可以避免乱序事件污染清理时刻，也便于在测试中注入固定墙钟，使边界行为可复现。

空键和非正 `ttl` 会分别返回 `kvstate.ErrEmptyKey` 与 `kvstate.ErrInvalidTTL`，可通过 `errors.Is` 区分；校验发生在状态修改前，失败后表仍可继续使用。所有读取和删除路径都在同一把锁内完成“读当前墙钟、计算差值、删除或返回值”，因此不会返回已经过期的旧值。

本地验证：

```bash
# 详细测试，日志包含输入、结果与判定依据
go test -v ./kvstate

# 并发竞态与边界测试
go test -race -v ./kvstate
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
