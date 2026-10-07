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

## 聚合视图子系统

`ontology` 包实现跨对象类型、沿固定长度链接路径传播的“可达终点属性最大值”聚合视图，支持：

- 明确的“无可达终点”状态（非数值默认值）；
- 任意一跳链接增删的精确增量维护（不多算/不遗漏起点）；
- 终点属性变小后的最大值来源重定，开销不超过该起点真实可达终点数；
- 重复到达自动去重、含环路径有界正确处理；
- 跳类型集合结构性扩展、事务式失败回滚、读写锁下的串行等价并发；
- 四类固定优先级错误（类型不匹配 > 实例不存在 > 运行时环拒绝 > 维护回滚）。

核心 API：

- 建模：`AddObjectType`、`AddLinkType`（可多次调用声明多组类型对）、`CreateObject`；
- 变更：`AddLink` / `RemoveLink`（`LinkOptions{RejectCycle:true}` 启用运行时环拒绝）、`SetAttr`；
- 视图：`RegisterView(ViewSpec)`、`AddTypeToHop`、`Query`、`RescanCost`；
- 观测：`SetLogger` 打印每次变更输入、受影响起点集合与判定依据。

设计取舍、被放弃方案与验证说明见 [DESIGN.md](DESIGN.md)。

```bash
export PATH=$PATH:/usr/local/go/bin
go test -race -v ./...
```
