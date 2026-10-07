# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology/`：多租户命名空间权限继承覆盖模块。对象类型定义在全局命名空间并被多租户共享，
  实例归属租户命名空间；权限规则分为全局默认规则与租户覆盖规则两层，支持属性级与
  行级（谓词条件）粒度、收紧/放宽（放宽需声明授权依据）、跨租户访问按实例归属租户判定、
  原子撤销与默认规则原子切换。设计取舍与被放弃的方案见 [DESIGN.md](DESIGN.md)。
- `cmd/server/`：演示程序，构造双租户场景并打印判定结果、层级依据与审计日志。

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
