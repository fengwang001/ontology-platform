# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 实例存储与跨类型聚合子系统

`ontology/` 包实现带乐观并发的实例存储与纯粹派生的聚合视图：一次提交对多个
视图整体可见、分组迁移原子、删除贡献即时扣除，并发效果等价于全局串行顺序并
可由提交日志确定性重放。

- 设计说明（关键取舍、被放弃方案）：`docs/design.md`
- 使用示例与 API：`docs/usage.md`
- 朴素全扫对照模型：`ontology/naive.go`

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
