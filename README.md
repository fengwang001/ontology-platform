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

## 子模块固定与更新协调器（`submod` 包）

超级仓库在指定路径上把另一个仓库固定在某个提交，支持嵌套子模块、按固定
递归对齐、按跟踪分支整体推进、添加与强制移除挂载；循环挂载、悬空固定、
未提交修改、非快进等危险更新以可区分错误整体拒绝，一次递归更新要么全部
落地要么完全不发生。所有操作可并发，效果等价于某个串行顺序。

```bash
go test -race -v ./submod/   # 每个用例打印输入、实际输出与判定依据
```

设计取舍与验证方法见 [DESIGN.md](DESIGN.md)。
