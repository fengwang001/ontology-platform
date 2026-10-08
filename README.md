# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统

- **批量导入**（`ontology/` 包）：一次请求批量创建对象与链接实例，
  支持批次内引用解析与拓扑排序、环检测整体拒绝、失败沿引用传播、
  链接基数约束（批次内按声明顺序放行 + 与既有数据 O(1) 联合校验）、
  BestEffort / Atomic 两种失败处理模式（后者按落地逆序回滚）、
  五类两两可区分的条目判定与逐条目审计日志。
  设计取舍、被放弃方案与复杂度论证见 [DESIGN.md](DESIGN.md)。

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
