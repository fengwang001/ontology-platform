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

## 时间属性权限校验子系统

`temporalauth/` 为带时间类属性的对象提供属性级查看权限校验，统一处理录入时区、
对象所属地区默认时区（支持版本迁移）与查询者所在时区三者的归一化，并覆盖夏令时
gap/overlap、半开时间窗口、错误优先级、信息不泄露、并发线性一致以及单次判定
O(log n) 复杂度。设计与取舍见 `temporalauth/DESIGN.md`。

```bash
go test ./temporalauth
go test -race ./temporalauth
go test ./temporalauth -run TestRandomizedCrossCheck -v
go test ./temporalauth -run XXX -bench BenchmarkViewVsRegionVersions
```
