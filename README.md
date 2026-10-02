# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块链接与求值器

`linker/` 提供带循环依赖、暂时性死区（TDZ）与错误粘滞的模块链接与
求值器。`AddModule` 登记不可变模块；`Evaluate(root)` 原子完成“链接 →
深度优先后序求值”，并返回本次追加到完成次序的模块与根结果；
`Status` / `Order` 查询状态与完成次序。设计细节（链接原子性、导出
解析与解析集合、循环跳过、TDZ、错误粘滞）见 `linker/DESIGN.md`。

```bash
go test -race -v ./linker
# 2000 组随机模块图与朴素模拟器对照（含输入/输出/判定日志）
go test -run TestRandomDifferential2000 -v -args -difflog=/tmp/difflog.txt
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
