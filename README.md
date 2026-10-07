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

## 链接基数约束与批量导入子系统

实现位于 `ontology/`，三个模块协作：

- `ontology/ledger.go`：基数账本（逐实例增量计数、单条创建/删除、全局同锁串行化）
- `ontology/importer.go`：批量导入（全有或全无 / 尽力而为、累积占用、回退）
- `ontology/errors.go`：错误归一化（参数非法 / 起点超限 / 终点超限 / 链接不存在）

快速上手见 `ontology/example_test.go`；设计取舍、被放弃方案与复杂度证明见
[`docs/design.md`](docs/design.md)。

```bash
# 随机差分对照（朴素全量重数模型，逐条打印输入/输出/依据）
go test ./ontology/ -run TestDifferential -v

# 删除-创建竞争与单名额不超卖（竞态检测）
go test -race ./ontology/ -run 'TestDeleteCreateRace|TestConcurrentBatches' -v

# 校验复杂度不随链接总量增长
go test ./ontology/ -run TestValidationCost -v
go test -bench=BenchmarkCreateDelete -run=^$ ./ontology/
```
