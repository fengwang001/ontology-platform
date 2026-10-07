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

## 浅克隆边界管理与可达性服务（`shallow` 包）

`shallow` 包实现浅克隆仓库的浅边界管理与对象可达性服务：

- 对象模型：远端完整提交图（提交含父列表、创建时刻、直接引用的内容对象），本地持有子集 + 显式浅边界。
- 深化：`DeepenDepth`（按深度）、`DeepenTime`（按时刻，各路径独立停止）、`Unshallow`（彻底去浅）；失败整体回滚。
- 可达性：`IsReachable` 为 O(1) 单点查询；`GC` 回收不可达对象并保护浅边界，幂等。
- 并发：所有操作可任意并发，结果等价于某个串行顺序。

设计取舍见 [shallow/DESIGN.md](shallow/DESIGN.md)。

```bash
go test ./shallow/ -v          # 功能测试（打印输入/输出/判定依据）
go test -race ./shallow/       # 并发与竞态
go test -run XXX -bench . ./shallow/  # 性能证明（查询 O(1)、深化与远端不可达部分无关）
```
