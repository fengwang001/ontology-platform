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

## 发版推导器 `release`

`release` 包按约定式提交推导下一个语义化版本，登记与推导均为并发安全，可串行重放复现。
入口为 `release.New()` 返回的 `*release.Releaser`，提供 `Tag`、`Versions`、`Next`、`Release`。

### 版本语法与比较

- 稳定版 `M.m.p`，预发布版 `M.m.p-c.N`。
- `M/m/p/N` 为十进制数、无前导零（`0` 合法）且不超过 `999999`；`N >= 1`。
- `c` 为 1 到 16 个小写字母的通道名；核心指 `(M,m,p)`。
- `Versions()` 升序规则：先比核心数值；同核心时预发布小于稳定；同核心预发布先比通道字节序再比 `N`。

### 基线与预发布线

- 基线 `S`：已发布稳定版本中的最大者；没有稳定版时为 `0.0.0`（与发布顺序无关）。
- 预发布线 `P`：核心严格大于 `S` 的已发布预发布版本中的最大核心（可跨多个通道）；没有则不存在。
  稳定版发布到该核心后，其预发布不再属于线，因此预发布线只升不降。

### 级别映射（breaking 优先于类型）

| 提交 | `S.major >= 1` | `S.major == 0` |
| --- | --- | --- |
| `breaking=true` | Major | Minor（降级） |
| `feat` | Minor | Patch（降级） |
| `fix` / `perf` | Patch | Patch |
| 其余类型 | 无级别 | 无级别 |

所有提交映射级别的最大值为 `L`：无级别 < Patch < Minor < Major。

### 目标核心与预发布序号

- 有级别时 `T` 为 `S` 按 `L` 升级：Major→`(M+1,0,0)`，Minor→`(M,m+1,0)`，Patch→`(M,m,p+1)`。
- 目标核心 `K`：`T` 与 `P` 都有取较大者（相等取 `T`），只有其一方取之，都没有则报「无可发布」。
- 通道为空：结果为 `K` 的稳定版。
- 通道为 `c`：`N` = 已发布的核心等于 `K` 且通道为 `c` 的最大 `N` 加 1，没有则为 1，结果为 `K-c.N`。
- `Next` 只推导不登记；`Release` 推导成功后原子登记再返回。

示例：`S=1.2.3`、已发布 `2.0.0-rc.1`，提交仅 `fix` 时，通道 `rc` 得 `2.0.0-rc.2`，空通道得 `2.0.0`；
`S=0.4.0` 且提交含 `feat` 与 breaking 时得 `0.5.0`。

### 错误与优先级

拒绝原因以 `*release.Error` 的 `Reason` 字段区分，取值 `ErrInvalidVersion`、`ErrAlreadyTagged`、
`ErrInvalidChannel`、`ErrInvalidCommitType`、`ErrNoRelease`、`ErrOverflow`。

- `Tag`：版本串非法 → 相同版本重复登记，只报第一个。
- `Next`/`Release`：非法通道 → 非法提交类型（下标最小者）→ 无可发布 → 最终 `K` 分量或结果 `N` 超过 `999999`。
- 溢出只检查最终 `K` 与 `N`：`T` 自身超限但被更大的 `P` 取代时不报溢出。
- 任何拒绝都不改变已发布集合；并发 `Release` 由互斥保证串行等价、序号互异、集合无重复。

### 本地验证

```bash
# 规则用例 + 竞态检测
go test -race -v ./release/

# 仅对拍（2000 组随机已发布集合与提交序列，日志含输入/输出/判定依据）
go test -run TestDifferentialRandom -v ./release/
```
