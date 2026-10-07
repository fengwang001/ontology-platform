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

## 属性取值规则的遮蔽与穿透（`ontology` 包）

单继承对象类型体系下，实例属性“当前最终生效取值规则”的判定机制：

- 规则自实例绑定的具体类型沿继承链条向上查找，取第一个显式声明者；链条唯一，结果唯一。
- 子类型重新声明的允许取值集合必须是当前生效集合的子集（声明时检查，扩大即拒绝）。
- 父类型新重新声明自动穿透到未自行声明的子类型；已自行声明的子类型继续遮蔽。
- 历史取值不追溯、不修改，收紧后仍可读取；仅新写入按当前规则校验。
- 密封（sealed）类型禁止在其下创建子类型；仍有直接子类型依赖的类型禁止删除。
- 四类判定分别暴露且“类型/属性不存在”优先：`ErrNotFound`、`ErrValueSetWidened`、
  `ErrSealedParent`、`ErrTypeHasChildren`。
- 查找开销只取决于本链条到属性定义祖先的实际深度，与无关分支数量无关。
- 变更与读写在统一锁 + 单调全序序号下可线性化；每次查找记录遍历链条、命中来源与最终规则。

设计取舍、被放弃方案与复杂度/并发论证见 `ontology/DESIGN.md`。
