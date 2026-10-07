# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

当前交付重点是跨对象类型、多实例的**原子批量更新**（`ontology.Store.ApplyBatch`）：
整批生效或整批回退、逐项基线版本核对、对象类型校验钩子、链接基数在整批最终
镜像上一次性校验、并发可串行化，且判定开销只与批次自身工作集相关。每次尝试
都留有可重放的完整日志证据。

- 设计说明（取舍、放弃的方案、验证方法）：`docs/design.md`
- API 速览：`docs/api.md`

## 环境要求

- Go 1.26+（`go version` 确认）

## 形态

当前交付为可直接引用的 Go 库（包路径 `ontology`，位于 `ontology/` 目录），
不包含独立服务进程；用法见 `docs/api.md`。

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
