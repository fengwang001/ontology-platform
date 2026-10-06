# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 微电网储能调度控制器（`microgrid` 包，根包）

管理单台储能的荷电、充放功率限制、关键负荷备用、并网/孤岛模式、
时隙计划提交与后缀撤销、执行偏差重验与维护锁定。全部电量为整数，
公开方法互斥串行化，可确定性重放。

- 设计取舍与复杂度论证：`DESIGN.md`
- 入口：`microgrid.New(params, initialSOC)`，操作见 `controller.go`
- 边界测试：`controller_test.go`
- 朴素模型随机对照与逐条日志：`naive_test.go`
- 性能基准（登记开销随后续计划数增长）：`benchmark_test.go`

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
