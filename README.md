# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 时间类属性属性级权限子系统（`tzperm/`）

面向带有时间类属性的对象，按“对象所属地区当时生效的默认时区”统一归一化，
支持地区默认时区版本迁移、夏令时切换、每日时间窗口（半开区间，可跨午夜）、
四类错误优先级单选、拒绝不泄露、单锁线性一致性与 O(log n) 版本查找。

- 设计与取舍：`tzperm/DESIGN.md`
- 测试说明：`tzperm/TESTING.md`
- 朴素对照模型：`tzperm/tznaive/`

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
