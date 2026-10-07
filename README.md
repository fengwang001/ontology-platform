# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

本仓库当前交付的是 **属性级权限随对象类型版本迁移联动的判定网关**，
设计说明见 [`docs/design.md`](docs/design.md)。能力概览：

- 对象类型版本序列：新增属性、收紧约束、弹性重命名（标识符延续）、属性废弃。
- 权限条目绑定主体 / 属性标识符 / 读或写 / 版本区间；多条授权取区间并集，
  显式吊销区间优先于授权。
- 标识符延续性：重命名后权限零迁移继续生效，旧名字与废弃标识符一律报
  `attr_not_found`；废弃条目线上立即失效但历史保留、可审计。
- 写时校验最新版本（过期整条拒绝、零状态变更），再逐属性判权，并按类型内
  全局唯一的策略执行 `reject_whole` 或 `ignore_field`。
- 读时整体移除无权限字段，返回部分视图标记，允许缺少必需属性。
- 全部判定经单一互斥锁串行化，可线性化；权限视图增量物化，在线判定对历史
  版本数与历史权限条目数均为零扫描（测试中以访问计数器可验证）。
- 与独立朴素重放模型进行随机操作序列差分对照；每次判定打印输入、输出与依据。

## 环境要求

- Go 1.26+（`go version` 确认）。若默认构建缓存目录只读，可 `export GOCACHE=/tmp/gocache`。

## 运行

```bash
# 拉取依赖
go mod tidy

# 直接运行
ADDR=:8080 go run ./cmd/server

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

# 只跑关键验证
go test ./ontology -run 'TestRename|TestRevocation|TestDeprecate|TestRejectWhole|TestViewAccess|TestRandom' -v
go test ./cmd/server -run TestHTTPEndToEnd -v
```

## 代码检查

```bash
gofmt -l .
go vet ./...
```

## HTTP 接口（最小网关）

| 方法与路径 | 说明 |
| --- | --- |
| `POST /types` | 创建类型与 v1（声明 `write_policy`） |
| `POST /types/{typeID}/evolve` | 演进新版本（`add`/`rename`/`tighten`/`deprecate`） |
| `POST /permissions/grant` / `POST /permissions/revoke` | 授权 / 吊销（闭区间，`to_ver=0` 为开放区间） |
| `GET  /permissions/audit?type_id=&subject=&attr_id=` | 审计历史权限流水（含废弃标识符条目） |
| `POST /objects` / `POST /objects/{id}/write` | 建对象 / 写（先版本后逐属性判权） |
| `GET  /objects/{id}?type_id=&subject=` | 读（整体移除式投影 + `partial_view`） |
| `GET  /decisions` | 全部判定日志（输入 / 输出 / 依据） |
