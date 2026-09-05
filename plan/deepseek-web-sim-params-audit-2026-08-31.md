# DeepSeek 网页版模拟参数核对（2026-08-31）— 现状 vs 官方实况对比

> 目的：核对 ds2api 伪装 DeepSeek 网页版所需的**请求头**与**版本号**是否过期，给出可选项，由用户决定是否更新。
> 本轮**只取证、不改代码**。上次同步：2026-08-25（commit `82f0288`，client version 2.2.0 → 2.4.0，Chrome 150）。
>
> **✅ 结果（2026-08-31 已实施）**：用户选定 **B + D**，已落地并发布 **v4.8.0**
> （commit `c63ebd7` / `4c65cbf`，release https://github.com/t479842598/ds2apiNew/releases/tag/v4.8.0 ）。
> 实施中额外发现并修掉三个本文未预见的问题：① `DS2API_CHROME_MAJOR_VERSION` 写在 `.env` 里静默失效；
> ② 非法环境变量值会直接拼出 `Chrome/abc.0.0.0` 坏指纹；③ `scripts/lint.sh` 在含空格路径下
> 因 `eval` 拆词而根本跑不起来。另外把 Chrome 指纹从“Go/JS 双写”改为
> `constants_shared.json` 单一来源（方案 B 的强化版）。详见
> `.lrnev/scenes/00-default/specs/02-00-deepseek-upstream-adapt/` 与笔记
> `v4.8.0-ds2apiNew-Chrome指纹升级151与上游风控识别`。
>
> **✅ 第二轮复核（2026-09-05，已发布 v4.9.0 / commit `55984b4`）**——本文结论逐项重验：
> - **官网侧全部“✅ 未变”依旧成立**：当前 bundle `main.2029023598.js`（`commitId 95255d1`）与本文
>   取证用的旧 bundle 做**逐字 diff**：`appVersion` 仍 **2.4.0**、21 个 `x-*` 头全集差集为空、
>   PoW（`DeepSeekHashV1` + `X-DS-PoW-Response` 载荷）、WAF/CF 三分类、40 个 `/api/v0` 端点均一致。
>   因此本文“不需要动的”清单（`x-client-version`、`x-client-*` 集合、PoW、`x-hif-*` 不发）**全部维持**。
> - **本文第五节待验证项 1 已关闭**：GREASE 不再是二手结论——v4.8.1 从 Chromium 源码
>   `user_agent_utils.cc` 确认它是 `major_version_number` 的确定性函数（`seed = major`），
>   Go/JS 两侧改为按公式计算，升版不再手补表（F-03）。
> - **本文第五节待验证项 2 的“未验证”前提被推翻**：文中“Chrome 的 ClientHello 变化很慢，
>   150/152 的握手很可能完全相同”是错的——httpcloak v1.7.0 changelog 明示 chrome-152 是
>   *“a real wire change rather than a header refresh”*（新增 compressed CA names 扩展 +
>   sigalg 头部 placeholder，影哿 JA4）。所以**方案 A（只改 HTTP 层）不再可选**，
>   必须两层一起升。实际落地：httpcloak v1.6.11 → v1.7.2、`major_version` 151 → **152**（方案 B 重跑）。
> - **Chrome 日程比本文预测更快**：stable 已到 **153.0.8010.27**（本文预测 153 全量 09-08，
>   实际 09-05 前已全量并出了 hotfix），两周节奏已生效。httpcloak v1.7.2 的
>   `chrome-*-windows` 区间为 **[143, 152]**（**无 153**），所以本轮停在 152、落后 stable 一版。
> - **额外修掉一个本文未预见的缺口（F-05）**：#13 的修复不彻底——`ResolveChromePreset` 对超出
>   库上限的版本**静默向下探测**，于是设 153 会造出“UA 153 / TLS 152”。现改为从
>   `fingerprint.Available()` 枚举真实区间并**钳制 HTTP 层跟随 TLS 能力**，
>   `ChromeMajorVersion() == TLSChromeVersion()` 成为硬不变式，钳制时打 WARN 点名来源。
>   升级过程中还当场暴露了同类缺陷：写死的 `chromeLowestPresetMajor = 133` 在 v1.7.2 已无预设。
> - **勘误（本文 2.1 节表头未列）**：`x-settings-token` 经新旧 bundle 对比确认是**旧有头**，
>   不是新增（本文取证时漏报）。仅用于 `GET /api/v0/client/settings`、localStorage 有值才发，
>   首个请求本来就没有 → 维持不发。

---

## 一、取证方法（可复现）

| 来源 | 做法 | 结论可信度 |
|---|---|---|
| **官方前端 bundle** | 本机直连与 Clash 节点（127.0.0.1:7897）访问 `chat.deepseek.com` 均被 CloudFront **403 Request blocked**；改从阿里云 `182.92.127.90`（国内 IP 放行）抓首页 → 拿到 `main.a006649905.js`（1.47 MB，`commitId:"2335d6b"`，`appVersion:"2.4.0"`，`clientPlatform:"web"`），在 bundle 里定位头装配函数与常量 | 高（一手，今日抓取） |
| **真实浏览器基准** | 本机 Google Chrome **152.0.7977.65**，headless + CDP 打到本地 echo server（127.0.0.1:8900），抓 on-wire 的 `User-Agent` / `sec-ch-ua` / 头顺序（document 与 fetch 两种） | 高（一手实测） |
| **Chrome 版本日程** | chromereleases.googleblog.com + developer.chrome.com release notes（子代理检索） | 中（二手，多源一致） |
| **httpcloak 能力边界** | 本地 `go mod cache` v1.6.8 与 GitHub raw v1.6.11 的 `fingerprint/presets.go` | 高（一手代码） |
| **第三方交叉验证** | `fa0311/latest-user-agent` 实时抓包库：Chrome 152 → `"Not?A_Brand";v="24"`，与我本机实测**逐字一致** | 高（独立复现） |

---

## 二、对比表

### 2.1 版本号类

| # | 项目 | 我们现在 | 官方/真实（2026-08-31） | 判定 | 代码位置 |
|---|---|---|---|---|---|
| 1 | `x-client-version` | `2.4.0` | bundle 内 `appVersion:"2.4.0"`、头装配 `version:"2.4.0"` | ✅ **未变，不用改** | `internal/deepseek/protocol/constants_shared.json:5` |
| 2 | `x-client-platform` | `web` | `clientPlatform:"web"` | ✅ 一致 | 同上 |
| 3 | `x-client-locale` | `zh_CN` | `eo.getLocale()`（同形式） | ✅ 一致 | 同上 |
| 4 | `x-client-timezone-offset` | 按 locale 查 IANA 实时算（含 DST） | `60 * dayjs().utcOffset()` | ✅ 等价 | `constants.go` `TimezoneOffsetFor` |
| 5 | PoW 算法/难度 | `DeepSeekHashV1`，difficulty 回退 `144000` | bundle 同算法、同难度语义 | ✅ 一致 | `pow/deepseek_pow.go` |
| 6 | Android App 版本 | —（我们用 web） | App 已 **2.4.3**（2026-08-28，APKMirror） | ℹ️ 与 web 头无关，**不要**跟着改 | — |

**结论：DeepSeek 网页版客户端版本号自 8-25 同步以来没有变化，`x-client-*` 五个头的集合也没有新增必需项。** 官方 bundle 里出现的全部 `x-*` 头：`x-client-bundle-id / x-client-locale / x-client-platform / x-client-timezone-offset / x-client-version`（请求必需，我们全有）+ `x-hif-leim / x-hif-dliq`（有值才发）+ `x-thinking-enabled / x-model-type / x-file-size`（仅 upload_file）+ `x-debug-*`（调试开关）+ 若干**响应头**（`x-hif-ttl`、`x-fetch-after-sec`、`x-ds-sse-heartbeat-timeout-secs`、`x-ds-trace-id`）。

### 2.2 Chrome 指纹类（**这里才是真过期**）

| # | 项目 | 我们现在 | 真实 Chrome 152 实测 | 判定 | 代码位置 |
|---|---|---|---|---|---|
| 7 | UA 里的 Chrome 大版本 | `Chrome/150.0.0.0` | stable 已到 **152**（151 于 2026-07-28、152 于 **2026-08-25** 发布；153 全量 2026-09-08，**且从 153 起改为两周一个版本**） | ❌ **落后 2 个大版本**，且以后会老化得更快 | `transport/chrome.go:19-24` |
| 8 | `sec-ch-ua` GREASE 品牌 | `"Not;A=Brand";v="8"` | `"Not?A_Brand";v="24"` | ❌ **GREASE 串和版本都过期**（对 150 是正确的，对 152 是错的） | `protocol/constants.go:47` |
| 9 | `sec-ch-ua` 品牌顺序 | Not 品牌 → Chromium → Google Chrome | 本机实测 `Chromium → Not?A_Brand → Google Chrome`；第三方抓包 `Not?A_Brand → Chromium` | ℹ️ **顺序每次安装/会话随机**，不是稳定信号，**不值得追** | 同上 |
| 10 | `sec-ch-ua-platform` | `"Windows"`（与 Windows UA 自洽） | 本机是 `"macOS"` | ✅ 内部自洽即可，保持 Windows | `protocol/constants.go` |
| 11 | TLS ClientHello 预设 | `httpcloak.NewClient("chrome-150-windows")` **硬编码字符串** | httpcloak v1.6.8（我们用的）最高就是 `chrome-150-*`；上游 **v1.6.11 新增 `chrome-151-*`（仍无 152）** | ⚠️ 受依赖版本限制，最多追到 151 | `transport/httpcloak.go:66` |
| 12 | `TLSChromeVersion` 常量 | `"133"`，注释称"uTLS 只建模到 133" | 实际生效的预设是 **chrome-150**；133 只被 `tests/wire-capture` 打印用 | ❌ **常量与实现不一致，注释已失效并误导**（uTLS 早已支持到 150/151） | `transport/chrome.go:26-33` |
| 13 | `DS2API_CHROME_MAJOR_VERSION` 覆盖 | 只改 **HTTP 层**（UA + sec-ch-ua） | TLS 层预设写死 `chrome-150-windows`，**不跟随该变量** | ❌ **设计缺陷**：设成 152 就正好制造注释里警告过的"UA 与 TLS 指纹互相矛盾" | `chrome.go:19` vs `httpcloak.go:66` |
| 14 | 请求头顺序表（24 项） | 固定顺序，`priority` 放最后 | 真实 Chrome 把 JS 自定义头与 `sec-ch-ua` 组**交错打乱**，`priority` 出现在 `origin` 之前 | ℹ️ 顺序本身随机化，**低价值**，不建议动 | `transport/chrome.go:44-70` |

### 2.3 风控/异常处理类（官方新增，我们完全没有）

| # | 项目 | 我们现在 | 官方 bundle 逻辑 | 判定 |
|---|---|---|---|---|
| 15 | WAF/CF 挑战识别 | 只有 `401/403 → 刷新 token`（`client_auth.go:451`），405/202/`cf-mitigated` 全无处理 | 显式三分类：<br>• `405` + 响应头 `x-amzn-waf-action: captcha` → **captcha**<br>• `202` + `x-amzn-waf-action: challenge` → **challenge**<br>• `403` 或 `429` + `cf-mitigated: challenge` → **Cloudflare challenge**<br>且 http 配置里 `cloudflareEnabled: true` | ❌ **缺失**。这正是 8-26 那次"账号被禁言 / CloudFront 403 / 裸请求 429"事件里我们只能靠人肉看日志判断的东西 |
| 16 | `x-hif-dliq` / `x-hif-leim` | 不发（有决策与理由：设备级标识，伪造会造成"N 账号同一设备"关联） | 官方 `e.leim \|\| storage.get()` —— **有值才发**，与我们的判断一致 | ✅ 维持现状。⚠️ 但第三方（deepseek-vision-mcp）称缺失时报 `unsupported_client_by_model`，**是否只影响多模态未验证** |

---

## 三、可选方案

### 方案 A：只把 HTTP 层追到 Chrome 152（最小改动）
- 改 `chrome.go` 默认值 `150 → 152`，`chromeSecChUA` 改成 `"Not?A_Brand";v="24", "Chromium";v="152", "Google Chrome";v="152"`；同步 `.env.example` 注释、`constants_test.go`。
- **代价**：UA 说 152、TLS 说 150 → 差 2 版。Chrome 的 ClientHello 变化很慢，150/152 的握手很可能完全相同，但**这一点未验证**。
- 工作量：约 15 分钟，1 个 commit。

### 方案 B（推荐）：升 httpcloak 到 v1.6.11，全栈统一到 Chrome 151
- `go get github.com/sardanioss/httpcloak@v1.6.11` → 预设 `chrome-151-windows`；UA/`sec-ch-ua` 同步 151（GREASE = `"Not=A?Brand";v="99"`，**此值来自第三方抓包，需实测复核**）；`TLSChromeVersion` 常量改为跟随实际预设。
- **理由**：TLS 与 HTTP 层**自洽**，只落后 stable 1 个版本——真实世界里"落后一版"是绝对主流形态（152 才发布 6 天，大量用户还在 151），比强行追平更不容易显得异常。
- 代价：动依赖，需跑全量 gate（lint / refactor-line / Go+Node 单测 / webui build）+ 线上验证。
- 工作量：约 1–2 小时。

### 方案 C：不改版本，只修 bug 与文档
- 让 TLS 预设由 `ChromeMajorVersion` 推导（`"chrome-" + major + "-windows"`，带白名单回退），修掉 #13 的"改 UA 不改 TLS"陷阱；把 `TLSChromeVersion=133` 这个失效常量删掉或改成真实值；更新注释。
- 适合"暂时不想动指纹，但希望以后一条环境变量就能安全换版"的情况。

### 方案 D（可与 A/B/C 叠加）：补 WAF/CF 挑战识别（#15）
- 在响应分类处加 `x-amzn-waf-action` / `cf-mitigated` 判定，把"出口 IP 被 WAF/CF 拦"与"账号被封/token 失效"区分开，打独立告警标签（如 `[upstream_waf_captcha]` / `[upstream_cf_challenge]`），并让账号健康检查与 mihomo 节点切换据此避开该节点。
- **价值**：直接对上 8-26 的封号痛点，把现在靠人肉 SSH 看日志的判断变成自动分类；不改任何对外指纹，风险最低。

---

## 四、我的建议

**B + D**：B 让指纹重新自洽（并顺手修掉 #12/#13 两个真实缺陷），D 补上风控可观测性。
若只想低成本先动，就 **A + D**；若这轮完全不想动指纹，至少做 **C + D**（纯修 bug + 加可观测性，零指纹风险）。

**不需要动的**：`x-client-version`（2.4.0 仍是官方现值）、`x-client-*` 头集合、PoW 字段与算法、`x-hif-*` 不发 的既有决策。

---

## 五、待验证 / 未证实项

1. Chrome **151** 的 GREASE 品牌串（`"Not=A?Brand";v="99"`）来自第三方抓包与 HtmlUnit 源码，**未本机实测**（本机只有 152）。走方案 B 前建议先用同法实测复核。
2. "UA 152 + TLS 150" 是否真会被 DeepSeek 判异常：未验证。DeepSeek 服务端是否比对 `sec-ch-ua` 与 TLS JA3 的一致性，属于黑盒。
3. `x-hif-leim` / `x-hif-dliq` 缺失是否会导致**纯文本对话**报 `unsupported_client_by_model`：未验证（现有 8-26 实测记录显示不带也能 200）。
4. 官方 bundle 里还有我们未使用的端点（`chat/resume_stream`、`chat/edit_message`、`chat/regenerate`、`chat_session/update_title`、`client/span`、`index/query`、`share/*`、`file/fork_file_task` 等）：本次只核对请求头/版本，未评估功能价值。

---

## 六、若决定更新，收尾流程（按项目既有约定）

1. `task_create` 落到对应 spec（这属于已完成特性加东西，不新开 spec）→ `task_update(in_progress)`。
2. 改代码 → `gofmt -w` → 四条本地 gate（`./scripts/lint.sh`、`check-refactor-line-gate.sh`、`run-unit-all.sh`、`npm run build --prefix webui`）。
3. `CHANGELOG.md` 新条目 → 打 tag 发 GitHub Release → 同步 Obsidian 开发笔记（`项目研发/ds2apiNew/`，文件名 `vX.Y.Z-ds2apiNew-标题简写`，并更新 `DS2API定制.md` 时间线与条数、核对 `00-总览.md`）。
4. 部署 `/opt/ds2api`（docker compose，image tag `ds2api:freebuff-<版本>`）——**部署前需征得同意**。
5. 若确认 GREASE/版本规律，`memory_save` 记一条"换 Chrome 版本时必须同时核对的三处"约定；踩到新坑则 `error_record`。
