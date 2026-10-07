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

## 动作副作用补偿回滚子系统

位于 `compensation/`：有序副作用子操作失败时按逆序补偿，逆操作失败/异常不中断补偿，
失败对象冻结并进入污染态，支持并发守卫与全局唯一逆操作登记编号。

- 设计说明：[compensation/DESIGN.md](compensation/DESIGN.md)
- 包文档：[compensation/README.md](compensation/README.md)
- 可运行演示（打印输入、每步生效/撤销结果与最终判定）：`go run ./cmd/compensation-demo`
- 随机对拍测试（独立朴素顺序模型，400 组/轮）：`go test -race -run TestDifferential ./compensation/`
