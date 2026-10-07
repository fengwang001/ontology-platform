# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 模块

- `ontology`：基于角色与标签的访问控制引擎。标签授予对象类型/链接类型
  构成的关系网络并沿网络传播（支持多父、多路径、环与传播阻断点），实例
  按归属继承标签；角色对标签的允许/显式拒绝授权沿角色层级继承，按
  「最近距离优先、同距拒绝优先」裁决；判定成本与网络规模无关；全部并发
  调用可串行化。设计取舍与被放弃的方案见 `docs/DESIGN.md`。
- `cmd/server`：引擎的 JSON/HTTP 演示服务，启动时加载演示场景。

### 快速示例

```go
e := ontology.NewEngine()
_ = e.DeclareObjectType("Folder")
_ = e.DeclareObjectType("Document")
_ = e.DeclareLinkType("contains", "Folder", "Document")
_ = e.DeclareTag("confidential")
_ = e.AttachTag("confidential", "Folder")
_ = e.DeclarePropagation("confidential", "contains", ontology.Downstream)
_ = e.DeclareRole("admin")
_ = e.DeclareSubject("bob", "admin")
_ = e.SetGrant("admin", "confidential", ontology.Allow)
_ = e.DeclareInstance("doc-1", "Document")

dec, err := e.Authorize("bob", "doc-1", "read") // dec.Allowed == true
```

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
