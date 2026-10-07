# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：跨链接聚合时序视图

维护一个跨链接聚合的视图：把通过链接关系相连、分属不同对象类型的一组对象，
按各自时间类属性在统一基准时区（UTC）下分组并排序。核心语义：

- 每个对象按其时间属性**写入时刻**生效的默认时区定义版本解释（写入时刻锚定），
  与事件到达顺序、迁移记录到达顺序均无关；
- 默认时区定义版本迁移绝不移动已归入分组的对象（不倒退、不重复计入）；
- 组内同值按 `(对象类型 ID, 对象 ID)` 字典序裁决，结果确定且可重复；
- 四类错误（链接端点类型缺失 / 分组属性废弃 / 写入时刻无默认时区 / 迁移校验失败）
  按固定优先级单次只报一类；
- 维护、迁移、查询可并发，结果等价于某个全局串行顺序；
- 查询开销与历史事件总量、历史迁移次数无关（插桩计数 + 基准测试可独立验证）。

设计取舍、被放弃方案与验证方法详见 [docs/DESIGN.md](docs/DESIGN.md)。

## 代码结构

- `ontology/` — 子系统核心包（时区、类型注册表、增量视图、朴素对照模型、决策日志）
- `cmd/server/` — 端到端演示程序
- `docs/DESIGN.md` — 设计说明

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
