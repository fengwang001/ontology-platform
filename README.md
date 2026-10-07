# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 读取时裁决模块

`ontology/` 包对同一对象类型同时施加行级可见性谓词与属性级字段遮蔽，
并对写入路径施加对称强制检查。设计取舍、被放弃方案与验证方法见
[`docs/DESIGN.md`](docs/DESIGN.md)，API 用法见 [`docs/USAGE.md`](docs/USAGE.md)。

要点：

- 行级谓词只在原始值上求值；属性遮蔽只影响呈现，不反向影响行裁决。
- 存在零值 / 真正缺失 / 未声明属性三态在内部与外部均唯一可区分。
- 行级、属性级合并模式各自独立声明（允许优先 / 拒绝优先），冲突有唯一裁决。
- 写入不可见实例与实例不存在返回同一不透明错误；写支持整体拒绝或静默丢弃。
- 每实例读写锁保证线性化；拒绝不产生任何可观察状态变化。
- trace 计数公开证明单次开销只与命中策略数相关，不随总策略/实例数增长。
- 随机差分测试将引擎与独立的朴素参照实现逐项对照。

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
