# Cookie 解析与适用性判定设计

## 1. 默认 Path

默认路径只看 Set-Cookie 来源 URL 的路径：找到最后一个 `/`，取它之前的部分。
例如 `/a/b/c` 的最后一个 `/` 位于 `/a/b/`，所以得到 `/a/b`。
`/a` 的最后一个 `/` 位于开头，它之前为空，因此回退为 `/`；规则不会把没有结尾分隔符的
`/a` 当成目录，否则 `/app` 会错误地向 `/application` 发送 cookie。`/` 自身也回退为 `/`。
若来源路径不以 `/` 开头，同样没有可靠的目录前缀，使用 `/`。

## 2. 路径匹配不是朴素前缀

请求路径必须等于 cookie 路径，或者以 cookie 路径加 `/` 开头。
即 `req == path` 或 `strings.HasPrefix(req, path+"/")`。
朴素 `strings.HasPrefix(req, path)` 会让 `/ab` 命中 `/a`。
这会把 `/a` 应用的会话 cookie 发给同一主机上的 `/ab` 页面；二者只是字符串前缀相近，
并不属于同一目录或资源层级，可能形成应用间越权与 CSRF/会话串用。

## 3. Domain 授权与拒绝

无 `Domain` 时 cookie 是 host-only：规范化后的来源主机必须与 cookie 域完全相等，
子域和父域都不匹配。
有 `Domain` 时先转小写并去掉一个前导点；cookie 域可匹配自身及任意以 `.domain`
结尾的主机。要接受该属性，来源主机必须等于 cookie 域，或是其直接/间接子域：
`host == domain || strings.HasSuffix(host, "."+domain)`。
因此 `b.example.com` 不能为 `example.org` 或 `example.com` 授权，只能为自身及
`b.example.com` 以下的域授权。单标签域（如 `com`）也拒绝：它是公共后缀层级，
允许它会让不同注册域之间共享 cookie，而且来源主机不可能是其真正的子域。

## 4. 覆盖与发送顺序

身份是 `(name, domain, path)`；再次设置同一身份时替换 value、过期时刻等可变状态，
但创建时间保留第一次成功存入的时间。
发送时先比较路径：路径更长的更具体，因此排在前面。路径长度相同则创建更早的在前，
使同具体程度的 cookie 保持稳定、可预测的先后关系。
若覆盖时刷新创建时间，同一三元组每次更新都会在等长路径组中“跳到后面”，
仅因值更新就改变发送顺序；旧 cookie 也可借重放 Set-Cookie 操纵其他同名 cookie 的相对顺序。
