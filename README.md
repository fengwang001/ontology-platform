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

## 权限抢占的乐观并发（`ontology` 包）

实现位于 `ontology/`：不同权限等级的动作在各自乐观重试循环中并发更新
同一实例，严格更高权限的写入生效后，基线已被推进的在途低权限动作会被
**立即抢占终止**（`StatusPreempted`），不消耗重试预算、不产生额外状态；
同权限竞争永远只是普通冲突；抢占判定为 O(1)（单调权限高水位 +
每版本基线高水位），不随竞争者数量增长。

- 设计与取舍、被放弃方案、正确性论证：[`DESIGN.md`](DESIGN.md)
- 实现：`ontology/types.go`、`ontology/instance.go`、`ontology/executor.go`
- 确定性交织调度与事件流：`ontology/scheduler.go`
- 独立按权限串行参照模型（O(历史) 朴素扫描做交叉验证）：`ontology/reference.go`
- 测试：同权限多方、交替抢占、抢占先于预算耗尽、高权限自身冲突重试、
  高水位单调性（`scenarios_test.go`），300 组随机多权限序列差分对照
  （`differential_test.go`），`-race` 真实并发测试，以及 O(1) 基准
  （`bench_test.go`）。

快速验证：

```bash
go test -race ./...
go test -v ./ontology -run TestRandomDifferential
go test -bench=Preemption -benchmem ./ontology
```
