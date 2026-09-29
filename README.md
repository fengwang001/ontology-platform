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

## 双写缓冲批量页刷写（`doublewrite`）

`doublewrite/` 实现批量脏页的原子刷写与撕裂页修复：同批页恢复后要么全新、
要么全旧，绝不新旧混合。写序固定为「擦旧完成标记 → 整批写双写区 → 写完成
标记（提交点）→ 逐页写回原位」；恢复时标记有效则按版本前滚（旧原位/撕裂
原位覆盖、更新的原位不回滚），标记无效则整体忽略双写区，原位损坏且无可用
副本时报不可修复（版本按 0、不猜测内容）。重复页号、越界、超容量、版本不
递增均整批拒绝且不写扇区；读刷可并发、批次串行、恢复幂等、同序列同断点
逐字节一致。设计细节与验证方法见 `doublewrite/README.md`。

## 代码检查

```bash
gofmt -l .
go vet ./...
```
