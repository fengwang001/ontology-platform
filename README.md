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

## 级联删除子系统

核心包为 `ontology`：

- `NewStore` 创建进程内线程安全图存储。
- `RegisterLinkType` 注册 `cascade`、`set_null`、`restrict` 三种规则之一，并用 `PreserveOnExists` 标记“存在即保留”的链接类型。
- `CreateObject` 创建对象。
- `AddLink` 原子新增有向链接。
- `DeleteObject` 在一个事务中完成级联删除、孤儿定点清理、拒绝规则检查和提交。
- `DeleteResult` 返回最终删除对象、物理移除边、置空边和逐步规则轨迹。
- `Logs` 返回每次删除请求的输入、结果、规则判断和去重探测次数。

完整设计、终止性证明、顺序无关性论证、放弃方案和测试说明见 `DESIGN.md`。

示例：

```go
store := ontology.NewStore()
_ = store.RegisterLinkType(ontology.LinkTypeConfig{
    Name: "owned-by", OnDelete: ontology.Cascade, PreserveOnExists: true,
})
_ = store.CreateObject("root")
_ = store.CreateObject("child")
_ = store.AddLink(ontology.Link{Type: "owned-by", Source: "root", Target: "child"})

result, err := store.DeleteObject("root")
```
