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

## 构建图脏判定器（buildgraph）

`buildgraph/` 按显式 / 隐式 / 仅排序三类输入、输出修改时刻与构建日志
判定脏边，支持重建后重新核对。语义、错误优先级与对拍验证见
`buildgraph/README.md`：

```bash
# 与朴素逐边定义对拍 2000 组随机图，并打印输入/输出/判定依据
go test -run TestRandomDifferential -v ./buildgraph/

# 1000 与 20000 边菱形图求值次数、20000 深链不栈溢出
go test -run 'TestDeepChain|TestDiamond' -v ./buildgraph/
```
