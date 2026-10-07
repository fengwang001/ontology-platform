# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 结构

- `ontology/`：对象实例存储与权限分级抢占式乐观并发控制
  （`Store` 判定与日志、`Executor` 重试循环、按权限分层的水位线）。
- `refmodel/`：独立实现的分层串行参照模型，消费判定日志做重放核验。
- `cmd/demo/`：低权限动作被高权限写入抢占终止的可运行演示。
- `docs/design.md`：设计说明（关键取舍、被放弃的方案、验证方法）。

## 运行

```bash
# 抢占演示
go run ./cmd/demo
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 抢占判定开销基准（与并发动作总数无关的证据）
go test ./ontology/ -run xxx -bench BenchmarkPreemptCheck

# 单个包 / 单个用例
go test ./ontology
go test -run TestAlternatingPreemption ./ontology

# 覆盖率
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
