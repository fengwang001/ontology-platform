# ontology-platform

本体服务平台（对标 Palantir Foundry Ontology）。

## 语义化发版推导器

根包 `ontology` 提供并发安全的 `Releaser`：

- `NewReleaser()` 创建空的已发布版本集合。
- `Tag(version string) error` 登记一个已发布版本。
- `Versions() []Version` 返回已发布版本的升序副本。
- `Next(commits []Commit, channel string) (Version, error)` 只推导，不改变状态。
- `Release(commits []Commit, channel string) (Version, error)` 按相同规则推导，成功后登记结果。

### 版本语法与比较

支持两种版本形式：

- 稳定版：`M.m.p`
- 预发布版：`M.m.p-c.N`

`M`、`m`、`p` 是 `0` 到 `999999` 的十进制数，不允许前导零，但 `0` 本身合法。`c` 是 1 到 16 个小写字母组成的通道名；`N` 是 1 到 `999999` 的十进制数，不允许前导零。

排序先按 `(M,m,p)` 数值比较。核心相同时：

1. 任一通道的预发布版都小于稳定版；
2. 预发布版先按通道名字节序排序；
3. 通道相同再按 `N` 数值排序。

### 基线与预发布线

- 稳定基线 `S`：所有已发布稳定版中最大者；没有稳定版时为 `0.0.0`。
- 预发布线 `P`：只考虑核心严格大于 `S` 的已发布预发布版，取其中最大核心；不存在则无。多个通道可以共享同一预发布线核心。
- 预发布线只升不降：核心不大于 `S` 的旧预发布版不会参与预发布线。

### 提交级别映射

每条提交先独立映射，`Breaking` 优先于类型，再取所有提交的最大级别。

| 条件 | `S.Major >= 1` | `S.Major == 0` |
| --- | --- | --- |
| `Breaking == true` | Major | Minor |
| `feat` | Minor | Patch |
| `fix`、`perf` | Patch | Patch |
| 其他合法类型 | 无级别 | 无级别 |

级别顺序为：无级别 `< Patch < Minor < Major`。

### 目标核心与预发布序号

当最高级别为 Patch、Minor 或 Major 时，提交升级目标 `T` 分别为：

- Patch：`(M,m,p+1)`
- Minor：`(M,m+1,0)`
- Major：`(M+1,0,0)`

最终目标核心 `K` 的取法：

- `T` 与 `P` 都存在：取核心较大者；
- 只有一个存在：取存在者；
- 都不存在：无可发布版本。

因此，只有无级别提交但已有更高预发布线时，仍可发布该预发布线的稳定版或其他通道预发布版；没有预发布线时返回 `ErrNoRelease`。

通道为空时，结果是核心 `K` 的稳定版。通道为 `c` 时，查找所有已发布的 `K-c.N`，取最大 `N` 加 1；不存在则序号为 1。

只检查最终结果：如果 `T` 自身溢出，但被更大的预发布线核心 `P` 取代为 `K`，不因 `T` 报错。稳定版没有序号，只检查 `K` 的三个分量。

### 错误优先级

`Tag` 的错误顺序：

1. `ErrInvalidVersion`：版本串不符合语法或范围；
2. `ErrDuplicateVersion`：相同版本已经发布。

`Next` 与 `Release` 的错误顺序：

1. `ErrInvalidChannel`：通道非空且不是 1 到 16 个小写字母；
2. `ErrInvalidCommitType`：按下标最小的非法提交类型（非空小写字母串才合法）；
3. `ErrNoRelease`：提交无级别且不存在预发布线；
4. `ErrCoreOverflow`：最终核心任一分量大于 `999999`；
5. `ErrPrereleaseOverflow`：最终预发布序号大于 `999999`。

所有失败操作都会整体拒绝，不改变已发布集合。

### 并发与确定性

`Releaser` 使用读写互斥保护状态：`Next` 与 `Versions` 可并发读取，`Tag` 与 `Release` 与任何操作互斥。并发相同入参的预发布 `Release` 会按某个串行顺序依次取得 `N+1`、`N+2`……不会产生重复版本。相同操作序列按相同顺序重放，会得到完全相同的版本结果与已发布集合。

## 本地验证

环境要求 Go 1.26+。

```bash
# 全量测试
go test ./...

# 竞态检测；-v 会输出 2000 组随机对拍的输入、输出、基线、预发布线、最高级别与提交目标
go test -race -v ./...

# 仅运行随机对拍
go test -v -run TestRandomDifferential

# 格式化与静态检查
gofmt -w release.go release_test.go
go vet ./...
```

如果当前 shell 没有把 Go 加入 `PATH`，可使用本机安装路径，例如：

```bash
GOCACHE=/tmp/go-build-cache /usr/local/go/bin/go test -race -v ./...
```
