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

## 级联删除控制器（`cascade` 包）

带属主引用与终结器的对象级联删除控制器，支持后台 / 前台 / 孤立三种
删除策略、多属主依赖者、终结器阻塞与前台删除传播。所有改变状态的
操作在返回前都把级联收敛到唯一稳定状态，复杂度只随实际影响面增长。

- 设计说明（关键取舍、被放弃方案、规则形式化）：`cascade/DESIGN.md`
- 独立朴素参考模型：`cascade/internal/testutil/naive`
- 随机差分、并发、性能与场景测试：`cascade/*_test.go`

```bash
go test ./cascade -v
go test -race ./cascade
go test ./cascade -run TestComplexity -v
```
