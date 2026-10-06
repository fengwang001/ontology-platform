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

## 重复保险理赔分摊模块（insurance）

`insurance/` 包实现重复保险下的多保单理赔分摊与限额消耗：

- 同一标的被多张保单覆盖时，普通条款保单先按独立赔付额比例分摊（向下取整，余数按生效日、编号逐单位补发），超额条款保单仅在仍有未补偿损失时介入；
- 事故损失额更正后，自该事故起按登记序号重算同标的后续事故，各保单剩余保额与从头重放一致；
- 支持保险人注销保单（注销日当天及以后的事故不再覆盖）、可区分错误类别、全操作并发安全；
- 登记开销只与覆盖保单数相关、更正开销只与受影响事故数相关，均由 `Stats()` 计数器与专门测试证明。

设计取舍、被放弃方案与验证方法见 [DESIGN.md](DESIGN.md)。

```bash
go test ./insurance/        # 单元 + 朴素模型随机对照 + 并发 + 性能证明
go test -race ./insurance/  # 竞态检测
```
