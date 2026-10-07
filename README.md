# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 环境要求

- Go 1.26+（`go version` 确认）

## 结构

- `ontology/`：单继承对象类型体系与属性取值规则的可见性判定机制
  （遮蔽与穿透、声明时收窄校验、密封类型、删除保护、实例读写、
  线性化并发语义与查找审计）。
- `docs/DESIGN.md`：设计说明（关键取舍、被放弃的方案、本地验证方法）。

## 构建

```bash
go build ./...
go vet ./...
```

## 测试

```bash
# 全量测试
go test ./...

# 带竞态检测与详细输出
go test -race -v ./...

# 单个包 / 单个用例
go test ./ontology
go test -run TestLookup ./ontology
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```
