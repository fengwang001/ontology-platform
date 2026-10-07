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

## 模块

- `delegation`：角色委托链模块。角色持有者可将对象类型上属性级与行级
  权限的子集在有限有效期内委托给其他主体（可标记是否允许再委托）；
  上游权限收缩即时级联使下游委托整体失效，多路径独立有效，支持成环
  检测、历史判定不可追溯重放与并发可线性化。设计取舍见
  `docs/delegation-design.md`。

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
