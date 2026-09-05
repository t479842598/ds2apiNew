# Changelog

## 4.9.0 (2026-09-05)

### 变更：Chrome 指纹全栈升到 152（httpcloak v1.6.11 → v1.7.2）

**背景/为什么做**：2026-09-05 复核上游，Chrome stable 已到 **153.0.8010.27**（beta 154 / dev 155），
两周一个版本的节奏已生效，比 8-31 审计预测的“153 全量 09-08”更早；我们自称 151，落后 2 个大版本。
同时 httpcloak 上游到 v1.7.2，`chrome-*-windows` 预设区间为 **[143, 152]**（**仍无 153**）。

**关键发现（推翻旧假设）**：v1.7.0 changelog 明示 chrome-152 是 *“a real wire change rather than
a header refresh”* —— ① 新增 compressed CA names TLS 扩展（28 个 CA 短标识 / 184 字节，每次握手
随机置换顺序）；② signature algorithms 列表头部 placeholder（每握手新取，影响 JA4 第三分量）。
这直接推翻了 8-31 审计里“150/152 的握手很可能完全相同（未验证）”的假设：既然 151/152 的
ClientHello **确实不同**，“只改 HTTP 层不动 TLS”的廉价方案就不再可取，必须两层一起升。

**更新了什么**：

- `github.com/sardanioss/httpcloak` v1.6.11 → **v1.7.2**（连带 `utls` v1.10.3 → v1.10.5、
  `net` v1.2.7 → v1.2.10、`quic-go` v1.2.27 → v1.2.29）。已实测本项目用到的
  `NewClient` / `WithForceHTTP2` / `WithTLSOnly` / `SetHeaderOrder` 无 breaking change。
- `constants_shared.json` 的 `chrome.major_version` 151 → **152**。GREASE 串由 4.8.1 的公式
  自动算出 `"Not?A_Brand";v="24"`，**未新增任何手维护值**（本轮验证了那条改造有效）。
- 内置回退 `builtinDefaultChromeMajorVersion` 与兜底预设 `chromeDefaultPreset` 同步到 152；
  JS/Vercel 侧 `BUILTIN_CHROME.majorVersion` 同步 152，两侧读同一 JSON 不漂移。
- **官网侧无需改动**（一手取证，新旧 bundle 逐字 diff）：bundle 虽重新发布
  （`commitId 2335d6b → 95255d1`），但 `appVersion` 仍 **2.4.0**，21 个 `x-*` 头全集、PoW
  （`DeepSeekHashV1` 与 `X-DS-PoW-Response` 载荷）、WAF/CF 判定、40 个 `/api/v0` 端点与 8-31
  完全一致。`x-client-version`、`x-client-*` 头集合、`x-hif-*` 不发的决策均维持现状。
- 勘误：`x-settings-token` 经新旧 bundle 对比确认是**旧有头**（上一轮审计漏报，非新增），
  仅用于 `GET /api/v0/client/settings`、localStorage 有值才发，首个请求本来就没有 → 维持不发。

### 修复：生效 Chrome 版本按 httpcloak 真实预设能力钳制，UA 与 TLS 不可能再错位

**背景**：上一轮（4.8.0）修掉了“改 UA 不改 TLS”，但留了一个更隐蔽的窗口：
`ResolveChromePreset` 对超出库预设上限的版本会**静默向下探测**。于是把版本设成 153 时，
HTTP 层声称 153、TLS 实为 `chrome-152-windows` —— 又造出一个真实浏览器里根本不存在的指纹。
本轮升级依赖时还当场暴露了第二个同类缺陷：写死的下限常量 `chromeLowestPresetMajor = 133`
在 v1.7.2 里已不存在对应预设（最老已是 143），新加的回归用例立刻抓到“UA 133 / TLS 152”。
**写死的数字不会跟着依赖变**，这正是本文件反复修的那类问题。

**更新了什么**：

- 新增 `transport.chromeMajorBounds()`：从 `fingerprint.Available()` 枚举 `chrome-<N>-windows`
  得出真实可用区间（**上下限都不写死**，`chrome-latest-windows` 这类别名由 `Atoi` 自然挡掉）。
- `ChromeMajorVersion()` 把三个来源（环境变量 / `constants_shared.json` / 内置回退）的请求值
  统一钳制到该区间，使 **`ChromeMajorVersion() == TLSChromeVersion()` 成为硬不变式**。
  钳制方向是 **HTTP 层跟随 TLS 能力**：真实浏览器的 UA 与 ClientHello 永远同源，而 TLS 预设
  受库约束、我们无法凭空造出来，所以唯一能动的就是 HTTP 层。
- 钳制不再静默：新增 `ChromeVersionClampNotice()`，启动时以 WARN 打印，**点名真实来源**
  （`DS2API_CHROME_MAJOR_VERSION` / `constants_shared.json` / `builtin default`），并提醒
  Vercel/Node 侧没有 TLS 预设可对钳、不会等价钳制，因此契约值不应写过库上限。
- `DS2API_CHROME_MAJOR_VERSION` 的校验降级为**纯格式**（数字且 ∈ [100, 9999]）；
  “库里有没有该预设”统一交给钳制判断，不再两处规则各说各话。
- `ResolveChromePreset` 改用同一枚举边界，绕过 `ChromeMajorVersion` 直接调用它的工具代码
  也不会拿到与声称版本不符的预设。
- 新增回归用例：`TestChromeMajorVersionNeverContradictsTLSPreset`（穷举 100/133/142/…/999，
  断言两层版本恒等）、`TestChromeMaxSupportedMajorMatchesDependency`（断言钳制边界确实来自依赖，
  且边界两侧的存在性符合预期）、`TestClampNoticeNamesTheActualSource`（告警来源标签正确）、
  `TestChromeMajorVersionClampsAboveCeilingWithNotice` / `...BelowFloor`。

**实测验证**（`tests/wire-capture -ours`）：默认 152 → UA/sec-ch-ua/TLS 三层均 152 且无告警；
`DS2API_CHROME_MAJOR_VERSION=153` → 两层一起降 152 + WARN；`=133` → 两层均 143 + WARN；
`=150` → 两层精确 150 无告警。

## 4.8.1 (2026-08-31)

### 变更：GREASE 品牌串改为按 Chromium 算法计算，升 Chrome 版本不再需要手维护表

**背景/为什么做**：4.8.0 把 Chrome 指纹统一到 151 时，`sec-ch-ua` 的 GREASE 品牌串靠
`grease_brands` 手维护表提供，其中 151 的值只有两个二手来源（HtmlUnit 硬编码 +
第三方抓包库），当时在发布说明里标为“未本机实测”。本次从 Chromium 源码找到了它的
生成规则，彻底消除这个不确定项。

**更新了什么**：

- 源码依据：`components/embedder_support/user_agent_utils.cc::GetGreasedUserAgentBrandVersion`，
  品牌串是 Chrome 大版本的**确定性函数**：
  `chars = {" ","(",":","-",".","/",")",";","=","?","_"}`、`vers = {"8","99","24"}`，
  `brand = "Not" + chars[major%11] + "A" + chars[(major+1)%11] + "Brand"`，`version = vers[major%3]`。
  同一文件里的 `ShuffleBrandList(list, seed)` 也从源码层面证实了品牌顺序按 seed 洗牌（不是指纹信号）。
- 新增 `protocol.ComputeChromeGreaseBrand()`（Go）与 `computeChromeGreaseBrand()`（JS），
  两侧共用同一公式。实测与推导互证：150→`"Not;A=Brand";v="8"`、151→`"Not=A?Brand";v="99"`、
  152→`"Not?A_Brand";v="24"`（152 与本机真实 Chrome 152.0.7977.65 抓包逐字一致）。
- `constants_shared.json` 的 `chrome.grease_brands` 降级为**历史钉值兼逃生口**：命中则优先用
  （万一 Chromium 轮换算法，改 JSON 即可），未命中则自动计算。今后把 `major_version` 提到
  新版本（如 153/154）**不再需要人工补 GREASE 串**。
- 新增回归用例：`TestComputeChromeGreaseBrandMatchesPinnedHistory`（算法输出必须逐字复现钉值）、
  `TestComputeChromeGreaseBrandFutureVersions`（固定 149/153/154/155 的输出）、
  Node 侧同名交叉用例。

### 修复：`RefreshToken` 不再“先删再试”，避免瞬时故障抹掉可能仍有效的 token

**背景**：旧实现一进 `RefreshToken` 就 `UpdateAccountToken(accountID, "")` 清空旧 token 再去登录。
但登录用的是账号密码、**不依赖旧 token**，提前清空对登录本身无帮助；一旦登录因出口被拦
（AWS WAF / Cloudflare challenge）或网络抖动失败，一个本来可能仍有效的 token 就没了，
下次请求必须重新走登录——而登录恰是风控最敏感的一步（参见 2026-08-26 那轮封号排查）。

**更新了什么**：

- 先登录、后决策：失败时由 `transientRefreshFailure(err)` 判断性质——
  上游拦截（`FailureUpstreamBlocked`，通过结构性接口识别，避开 `auth` 反向 import `client` 的循环）、
  `context` 取消/超时、`*url.Error`、`net.Error`、`io.EOF`、连接重置等“没拿到 HTTP 响应”的失败
  → **保留旧 token**；登录确实被上游拒绝（密码错、`USER_IS_BANNED` 等）→ 仍按原行为
  走 `MarkTokenInvalid` 清空。
- 新增 `internal/auth/refresh_token_test.go`：覆盖分类表、拦截/网络失败保留 token、
  被拒仍清空、成功写入新 token 四类；并用“先播种再验”的夹具用例防止断言空转。
  已做变异检验：把旧的提前清空改回去，两个“保留”用例如期失败。
- 文档：`docs/DEVELOPMENT.md` 新增第 6/7 节，写清“Chrome 版本怎么升”与拦截/token 刷新语义。

**验证**：gofmt / lint（0 issues）/ refactor-gate / Go 全量单测 + Node 163 项 / WebUI 构建全部通过；
`go build` 在 darwin / linux / windows 三个 GOOS 均通过（`transientRefreshFailure` 用到的
`syscall` 错误码在 Windows 上同样可用）。

**诚实说明**：本修复的实际严重性低于 4.8.0 发布时的描述——token 本来就不写盘（
`writeConfigJSONLocked` 会 `ClearAccountTokens`），且 token 为空时 `ensureManagedToken` 会在
下一次请求自动重新登录，所以不会把账号打死。收益是：避免在瞬时故障下丢掉仍有效的 token、
避免多余的重新登录（降低风控暴露），以及去掉每次刷新的一次多余配置写入。

## 4.8.0 (2026-08-31)

### 新增：Chrome 指纹统一升级到 151（TLS 与 HTTP 层同源）+ 上游风控拦截识别

**背景/为什么做**：2026-08-31 对 chat.deepseek.com 官方前端 bundle 与真实浏览器重新取证发现，
上一轮（08-25）同步的模拟参数已经过时：真实 Chrome stable 已到 152（151 于 07-28、152 于 08-25
发布，且自 153 起改为两周一个版本），而我们仍自称 Chrome 150、`sec-ch-ua` 用着 150 时代的
GREASE 品牌串。同时 DeepSeek 风控已接入 AWS WAF + Cloudflare challenge，返回 405/202/403/429
带挑战响应头，而客户端完全不识别，导致“出口 IP 被拦”与“账号被封/token 失效”无法区分，
只能靠人肉看日志判断（即 08-26 那轮封号排查的痛点）。

**更新了什么**：

- **Chrome 指纹升级到 151，两层自洽**：`httpcloak` 升级 v1.6.8 → v1.6.11（新增 `chrome-151-*` 预设），
  TLS ClientHello 与 HTTP 头（`User-Agent` / `sec-ch-ua`）现在由**同一个版本源**驱动。
  默认 `Chrome/151.0.0.0` + `sec-ch-ua: "Not=A?Brand";v="99", "Chromium";v="151", "Google Chrome";v="151"`。
  选 151 而非 152：httpcloak 尚无 152 预设，全栈自洽比“UA 领先但 TLS 跟不上”更真实。
- **指纹参数改为跨语言单一来源**：`constants_shared.json` 新增 `chrome` 块
  （`major_version` + `grease_brands` 表 + `grease_fallback_major`），Go 侧 `go:embed` 与
  Node/Vercel 侧 `require` 读同一文件，消除“Go/JS 各写一份 UA 常量后悄悄错开”这类缺陷；
  `tests/node/js_compat_test.js` 新增守卫用例，只改一侧不改 JSON 会直接测失败。
- **TLS 预设自动“向下取最新可用”**：新增 `transport.ResolveChromePreset()`，用
  `fingerprint.GetStrict`（无回退）探测；设 `DS2API_CHROME_MAJOR_VERSION=152` 时 HTTP 层声称 152、
  TLS 自动用 `chrome-151-windows`，不再制造版本矛盾（旧实现把预设硬编码为
  `"chrome-150-windows"`，改环境变量只改 UA 不改 TLS）。
- **新增上游风控拦截识别与告警分类**：`protocol.ClassifyUpstreamBlock()` 按官方 bundle 同一规则
  识别三类挑战，并在登录/会话创建/POW/上传/流式 completion 五个出口打独立日志标签：
  `405`+`x-amzn-waf-action: captcha` → `[upstream_waf_captcha]`；
  `202`+`x-amzn-waf-action: challenge` → `[upstream_waf_challenge]`；
  `403`/`429`+`cf-mitigated: challenge` → `[upstream_cf_challenge]`。
  日志带 `kind`/`url`/`status`/`waf_action`/`cf_mitigated`/`account` 字段，可直接检索。
- **拦截与账号异常彻底分开**：新增失败类型 `FailureUpstreamBlocked`（`upstream_blocked`），
  命中挑战**不再**触发 token 刷新/切号（刷新解决不了出口 IP 被拦），也不会被误归为封号。
  对调用方统一返回 **502 Bad Gateway**（`code: upstream_blocked`）：此前会话创建/PoW 被拦时
  会落到 `401 Failed to get PoW (invalid token...)`，诱导客户端做一次毫无用处的重新登录；
  现由 `completionruntime.blockedUpstreamError()` 集中判断，覆盖 OpenAI Chat / Responses /
  Claude / Gemini 四个入口与流式/非流式/分段/空输出重试全部路径（协议适配层不复制该判断）。
  处置方向改为换出口节点/避开被拉黑地区段（配合 `mihomo.node_exclude`）。
- **流式 completion 不再把挑战页当 SSE 解析**：`CallCompletion` 遇到已识别的挑战响应直接
  返回 `FailureUpstreamBlocked`（其他非 200 行为完全不变）。
- **Vercel/Node 路径同步接入**（遵守“新特性需确认 Vercel 路径已连通”的仓库约定）：
  `internal/js/shared/deepseek-constants.js` 导出与 Go 侧同一张判定表的
  `classifyUpstreamBlock(status, headers)`；`vercel_stream_impl.js` 在 completion 非 2xx 分支
  打同样的 `[upstream_*]` 日志标签，并在命中挑战时返回 502（`upstream_blocked`），
  其余非 2xx 仍按原样透传上游状态码。Node 用例与 Go 用例共用同一张表，两边判定不会错开。
- **启动日志可观测**：新增 `[chrome] web-client fingerprint` 行，打印生效的 Chrome 大版本与
  实际 TLS 预设名，部署后一眼确认指纹是否真的切过去了。

### 修复：`DS2API_CHROME_MAJOR_VERSION` 两个真实缺陷

- **写在 `.env` 里静默失效**：该变量原本在包初始化阶段读取，而 `.env` 是 `main()` 里
  `config.LoadDotEnv()` 才加载的，导致按文档在 `.env` 配版本号根本不生效。现改为首次使用时
  惰性读取 + `.env` 加载完成后重同步。
- **非法值会拼出坏指纹**：`DS2API_CHROME_MAJOR_VERSION=abc` 原本直接产出
  `Chrome/abc.0.0.0` 这种没人用的 UA（比不改更危险）。现校验为纯数字且 ∈ [133,999]，
  非法则忽略并回退契约值，同时打 `[chrome] invalid DS2API_CHROME_MAJOR_VERSION ignored` 告警。
- 删除已失效的 `TLSChromeVersion = "133"` 写死常量（与实际生效预设早已不符、只误导阅读者），
  改为由解析结果推导的 `TLSChromeVersion()`；`tests/wire-capture` 输出同步修正。

**未变更（核对后确认无需动）**：`x-client-version` 仍为 `2.4.0`（官方 bundle 今日仍为
`appVersion:"2.4.0"`，Android App 的 2.4.3 与 web 头无关）、`x-client-*` 头集合无新增必需项、
PoW 字段与算法一致、继续不发设备级头 `x-hif-dliq`/`x-hif-leim`。

**验证**：gofmt / lint（0 issues）/ refactor-gate / Go 全量单测 + Node 162 项 / WebUI 构建全部通过；
`tests/wire-capture -ours` 核对 151/150/152/非法 四种取值的 UA、sec-ch-ua 与 TLS 预设均自洽。
**真实环环境实测**（非合成用例）：

- 阿里云国内出口：`GET /api/v0/client/settings` 用 Chrome 151 指纹 + `chrome-151-windows` TLS
  返回 **HTTP 200**，分类器正确返回 `none`（无误判）。
- 欧洲 VPS（193.123.167.208）：`GET https://chat.deepseek.com/` 返回 **202 + `x-amzn-waf-action: challenge`**，
  分类器正确识别为 **`waf_challenge`**——对着 DeepSeek 真实 AWS WAF 命中，证明识别规则有效；
  同一机器的 API 路径仍返回 200（说明新指纹能通过 CloudFront 层）。

文档同步：`docs/MIHOMO_BRIDGE.md` 新增「上游风控拦截分类（WAF / Cloudflare）」章节、
`.env.example` 更新默认值与预设解析说明。

## 4.7.2 (2026-08-28)

### 变更：node_exclude 接入管理台，欧美节点默认恢复可选

- **管理台接入**：WebUI「代理桥」页新增「节点排除（node_exclude）」编辑框
  （一行一个关键字），`PUT /admin/mihomo/settings` 同步支持该字段，
  `GET /admin/mihomo/status` 回显当前值。保存后立即生效并按新列表重滤
  订阅缓存节点、回收失效节点的端口映射与账号绑定；旧客户端请求不带
  该字段时保持原值不被清空。注意：被旧关键字过滤掉的节点已从缓存移除，
  放宽关键字后需刷新订阅才会恢复。
- **示例配置默认不再排除美/英节点**：4.7.0 预置的 `["🇺🇸", "🇬🇧"]` 会让
  照抄示例部署的实例整体剔除美国/英国节点，现改为空数组默认不过滤，
  全部订阅节点（含欧美）均可选择。
- 已部署实例不受示例配置影响：可在管理台「代理桥」页直接编辑，
  或手工清空各自 `config.json` 中 `mihomo.node_exclude` 后重启。

## 4.7.1 (2026-08-26)

### 修复：自定义部署镜像缺少 CA 根证书导致账号登录失败

v4.7.0 若使用自定义 Dockerfile 部署（非仓库自带多阶段构建），基于
`debian:bookworm-slim` 却遗漏 `ca-certificates` 时，容器内 `/etc/ssl/certs`
为空，Go 标准库 TLS 校验全部失败（`x509: certificate signed by unknown authority`），
账号无法登录刷新 token，表现为"账号登录不了"。

- 部署注意事项：自定义部署 Dockerfile 的 `FROM debian:*` 之后必须
  `RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates`。
- 验证方法：构建后 `docker run --rm <image> sh -c "ls /etc/ssl/certs/ca-certificates.crt"`。
- 生产环境 apt 源建议替换为 `mirrors.aliyun.com`，避免 `deb.debian.org` 下载卡死。

## 4.7.0 (2026-08-26)

### 新增：Mihomo 节点过滤（mihomo.node_exclude）

DeepSeek 网页端风控升级（AWS WAF + CloudFront 按 IP 信誉拦截）导致部分机场节点
（美国/英国数据中心段）被拉黑，账号被禁言。新增按节点名关键字排除节点的配置，
等效于在机场侧下掉风险节点，订阅刷新后依然生效：

- `mihomo.node_exclude`：字符串数组，节点名包含任一关键字即从节点池剔除。
  新增/刷新订阅时落库前自动过滤，启动加载旧缓存时同样过滤，无需手工清洗
  `mihomo_subscriptions.json`。
- 被排除节点不参与 mihomo 运行时 proxies 生成、健康检查与账号自动分配。
- 配置示例：`"node_exclude": ["🇺🇸", "🇬🇧"]`。

## 4.6.2 (2026-08-25)

### 修复：专家（PRO）模型丢失上下文

PRO 模型（`deepseek-v4-pro`）不支持文件上传，超长提示词依赖 `expert_prompt_segment`
分段发送：前 N-1 段用 `FireCompletionAndStop` 发送并中断，最后一段携带
`parent_message_id` 链从上游会话树取回前文。该链路依赖上游确认"被 stop 的消息
已提交到会话树"，一旦某段未落库，后续分段无法把前文并入上下文，表现为
"PRO 模型读不到上下文"。

本次改动：

- `FireCompletionAndStop` 在 `stop_stream` 后未收到提交确认（未等到 `event: close`
  且连接被超时强制关闭）时，返回 `ErrSegmentCommitUnconfirmed`，不再当作成功继续。
- 分段发送失败或提交未确认时，回退为单消息发送：把剩余分段按序拼接还原原文，
  以最后一个已确认提交的分段 id 作为 parent，保证最终请求携带尽可能完整的上下文
  而不是直接报错或静默丢上下文。回退路径记录 `[expert_segment_fallback]` 告警日志。
- 专家模型丢弃非文本附件（图片、PDF 等）时记录 `[expert_attachment_dropped]`
  告警日志（含文件名/MIME），便于线上确认"PRO 模型看不到附件"的原因；
  `expert_text_file_inline` 被关闭时同样会提示。
- 新增 `internal/completionruntime/segments_test.go` 覆盖分段链正常/失败/未确认三条路径。
- 同步更新 `docs/prompt-compatibility.md` 分段与文件内联章节。
