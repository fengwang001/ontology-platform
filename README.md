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

## 动作多态分派

动作只声明输入输出形状与前置/后置契约，具体执行逻辑由各对象类型自注册，
按实例当前绑定类型沿继承来源链分派，并支持运行期热替换、放宽声明审计与
并发线性一致。

- 设计与取舍：`docs/dispatch-design.md`
- 实现：`types.go`（声明/记录）、`object.go`（类型/实例/撤销）、
  `registry.go`（不可变快照与沿链查找）、`dispatcher.go`（分派与全序）、
  `audit.go`（放宽声明与事后审计）
- 测试：`dispatch_test.go`（直接/间接/显式放弃全组合与四类失败）、
  `replace_test.go`（热替换在途调用与放宽审计）、
  `concurrency_test.go`（并发交织对照朴素串行模型）、
  `complexity_test.go`（查找步数与无关注册规模无关、审计轨迹完整性）

关键语义：

- 直接注册优先；类型可 `Waive` 显式放弃直接处理，跳过本层继续向上，
  “放弃”与“从未注册”是两种可区分状态。
- 调用发起时刻 = 进入分派临界区、钉住不可变快照的时刻；替换前在途调用
  用旧逻辑执行到底，替换后新调用用新逻辑。
- 判定优先级：两种无法分派（执行前）→ 查找段对象被撤销（整体失败、不写入）
  → 逻辑自身前置/后置/执行错误。
- 放宽（拒绝改放行）须随注册声明 `RelaxScope`；不阻止注册，但 `Audit`
  可事后发现未声明放宽、超出声明范围与未声明收紧。
