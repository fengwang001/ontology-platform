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

## 包级变量初始化次序求解（initorder）

`initorder` 包提供初始化次序求解会话：按源码次序登记变量初始化单元
与函数声明，`Solve` 给出唯一确定的初始化次序与每个单元的传递依赖
（判定依据），错误分为参数非法、重复声明、未声明引用、初始化环四类。
设计与验证方法见 [initorder/DESIGN.md](initorder/DESIGN.md)。

```bash
go test -race -v ./initorder/   # 含朴素模型差分与并发测试
go test ./initorder/ -run XXX -bench .  # 开销基准
```
