# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 子系统：跨链接聚合视图的增量维护

本仓库当前交付一个视图子系统：把通过某条链接关系相连的一组对象（可分
属不同对象类型）按各自的时间类属性，在统一基准时区（UTC）下分组并排
序。分组/排序所依据的时区是**对象时间值写入时刻**生效的默认时区定义
版本；时区定义版本迁移不影响已入组对象；迟到事件按写入时刻锚定的版本
解释，与到达顺序无关。设计取舍、被放弃的方案与验证方法见
[DESIGN.md](DESIGN.md)。

### 包结构

- 根包 `ontology`：子系统实现
  - `types.go` — 写入时刻锚定记录、事件、分组结果
  - `engine.go` — 类型/时区版本/迁移校验/写入/事件投递日志（全局串行化）
  - `view.go` — 视图增量维护（物化分组、归一化、确定性裁决）
  - `errors.go` — 四类可区分错误与报告优先级
  - `audit.go` — 判定审计日志（输入、所依据时区版本、结论）
  - `model.go` — 朴素全量重建参照模型（测试对照用）
- `cmd/server` — 端到端演示（迁移 + 迟到事件 + 审计输出）

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
