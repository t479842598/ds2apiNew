package transport

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/sardanioss/httpcloak/fingerprint"
)

// Chrome 指纹的**唯一权威来源**是 protocol 包内嵌的 constants_shared.json
// （chrome.major_version），Go 与 Node/Vercel 两侧读同一文件，避免双写漂移。
// 本包只保留「无人推送时的内置回退」与「环境变量覆盖」，不重复维护版本号。
//
// 优先级：DS2API_CHROME_MAJOR_VERSION > protocol 推送的 JSON 值 > builtinDefaultChromeMajorVersion。
// 三者都只是**请求值**；真正生效的值还要过一道 clampChromeMajor，
// 见该函数注释——库里没有对应预设时，宁可降级也不能让 UA 与 TLS 分层各说各话。
const builtinDefaultChromeMajorVersion = "152"

// 预设可用区间的**上下限不写常量**，由 chromeMajorBounds 从 httpcloak 枚举得出。
// 教训就写在旁边：旧版本里写死的下限 chromeLowestPresetMajor = 133 在升到
// v1.7.2 后当场失效（该版最老的 chrome-*-windows 已是 143），UA 会声称 133、
// TLS 却只能兜底到 152 —— 正是本文件要消除的那类矛盾。写死的数字不会跟着依赖变。
//
// chromeMajorEnvFloor / chromeMajorEnvCeiling 只是**格式**下限（把 "1"、"abc"
// 这类垃圾挡在 UA 拼接之前），不代表库里真有对应预设；可用性统一由 clampChromeMajor 判。
const (
	chromeMajorEnvFloor   = 100
	chromeMajorEnvCeiling = 9999
)

// chromeDefaultPreset 是全部探测失败时的兜底预设（httpcloak v1.7.2 已含）。
const chromeDefaultPreset = "chrome-152-windows"

var (
	chromeVersionMu sync.Mutex
	// envChromeMajorVersion 非空时永远优先，运维无需重新编译即可换版。
	// 必须是合法大版本号：拼进 UA 的非法值（如 "abc"）会直接造出
	// Chrome/abc.0.0.0 这种没人用的坏指纹，比不改更危险，因此宁可忽略。
	envChromeMajorVersion  = ""
	envChromeRejectedValue = ""
	envChromeRead          = false
	// pushedChromeMajorVersion 由 protocol 在 init 时从 constants_shared.json 推送。
	pushedChromeMajorVersion = ""
	// chromeClampNotice 记录最近一次「请求版本被钳制」的说明，空串表示没发生过。
	// 必须能被启动日志读到：钳制是静默降级，不打出来运维会以为自己配的版本生效了。
	chromeClampNotice = ""
	chromeBoundsOnce  sync.Once
	chromeBoundsMin   int
	chromeBoundsMax   int
)

// chromeMajorBounds 返回 httpcloak 实际注册预设里 chrome-<N>-windows 的 N 区间。
// 枚举不到任何 windows 预设时返回 (0, 0)（依赖异常），调用方据此走兜底分支。
//
// 必须**动态枚举**而不是写死上下限：写死了就有两种失败——升级依赖却忘了同步常量，
// 会把库里明明能用的版本白白钳掉（或反过来放行一个库里不存在的版本），
// 重新造出「UA 声称 N、ClientHello 实为 M」的矛盾。枚举让区间永远等于依赖的真实能力。
func chromeMajorBounds() (int, int) {
	chromeBoundsOnce.Do(func() {
		min, max := 0, 0
		for _, name := range fingerprint.Available() {
			if !strings.HasPrefix(name, "chrome-") || !strings.HasSuffix(name, "-windows") {
				continue
			}
			mid := strings.TrimSuffix(strings.TrimPrefix(name, "chrome-"), "-windows")
			n, err := strconv.Atoi(mid)
			// chrome-latest-windows 这类别名会在这里被 Atoi 挡掉，不污染区间。
			if err != nil || n <= 0 {
				continue
			}
			if min == 0 || n < min {
				min = n
			}
			if n > max {
				max = n
			}
		}
		chromeBoundsMin, chromeBoundsMax = min, max
	})
	return chromeBoundsMin, chromeBoundsMax
}

// chromeMaxSupportedMajor 返回预设区间的上界（0 = 枚举失败）。
func chromeMaxSupportedMajor() int {
	_, max := chromeMajorBounds()
	return max
}

// clampChromeMajor 把请求的大版本钳制到 httpcloak 真正有 windows 预设的区间，
// 返回（生效版本，说明）；说明为空表示没有发生钳制。
//
// 为什么是「HTTP 层跟着 TLS 走」而不是反过来：真实浏览器的 UA、sec-ch-ua 与
// ClientHello 出自同一个二进制、永远同源，不存在「UA 领先 TLS 一版」的浏览器。
// 而 TLS 预设受依赖库上限约束、我们无法凭空造出来，所以唯一能动的就是 HTTP 层。
// 旧实现让 ResolveChromePreset 静默向下探测，等于放任两层错位（v4.8.0 修掉的是
// 「改 UA 不改 TLS」，但库里没有该预设时仍会悄悄降级 TLS，这次把那条路也堵上）。
func clampChromeMajor(source, requested string) (string, string) {
	n, err := strconv.Atoi(strings.TrimSpace(requested))
	if err != nil {
		// 三个来源都做过校验，走到这里纯属防御：宁可用内置回退，也不拼出坏指纹。
		return builtinDefaultChromeMajorVersion, fmt.Sprintf(
			"%s requested unparsable Chrome major %q; using builtin %s", source, requested, builtinDefaultChromeMajorVersion)
	}
	minSupported, maxSupported := chromeMajorBounds()
	if maxSupported <= 0 || minSupported <= 0 {
		// 一个 windows 预设都没枚举到 = 依赖异常。此时也不能放行任意请求值：
		// ResolveChromePreset 会兜底到 chromeDefaultPreset，所以只有回退到
		// 同一个版本才能保住「UA 大版本 == TLS 预设大版本」这条不变式。
		return chromeMajorFromDefaultPreset(), fmt.Sprintf(
			"%s requested Chrome %d, but no chrome-*-windows preset was found in httpcloak; "+
				"falling back to %s to keep UA and TLS on the same version",
			source, n, chromeMajorFromDefaultPreset())
	}
	if n > maxSupported {
		return strconv.Itoa(maxSupported), fmt.Sprintf(
			"%s requested Chrome %d, but httpcloak's newest windows preset is chrome-%d-windows; "+
				"HTTP layer clamped to %d so UA and TLS stay consistent. "+
				"Note: the Vercel/Node path reads the same JSON but has no TLS preset to clamp against, "+
				"so it would still advertise %d — set the contract value to <= %d instead.",
			source, n, maxSupported, maxSupported, n, maxSupported)
	}
	if n < minSupported {
		// 往低方向同样钳到可用区间，而不是回退内置值：与上界分支对称，
		// 且真实存在「用户停在旧版 Chrome」这回事，发最老的可用预设比跳回最新更可信。
		return strconv.Itoa(minSupported), fmt.Sprintf(
			"%s requested Chrome %d, but httpcloak's oldest windows preset is chrome-%d-windows; "+
				"HTTP layer clamped to %d so UA and TLS stay consistent",
			source, n, minSupported, minSupported)
	}
	return requested, ""
}

// chromeMajorFromDefaultPreset 从兜底预设名里取大版本，保证兜底路径与
// ResolveChromePreset 的兜底结果同源（而不是另写一个数字、然后两边错开）。
func chromeMajorFromDefaultPreset() string {
	return tlsVersionFromPreset(chromeDefaultPreset)
}

// ChromeVersionClampNotice 返回最近一次钳制说明（空串表示从未钳制）。
// 每次 ChromeMajorVersion() 都会重算，所以调用前至少取过一次版本号。
func ChromeVersionClampNotice() string {
	chromeVersionMu.Lock()
	defer chromeVersionMu.Unlock()
	return chromeClampNotice
}

// readEnvChromeMajorVersion 解析并校验 DS2API_CHROME_MAJOR_VERSION。
// 返回（合法值或空串，被拒绝的原始值）。
func readEnvChromeMajorVersion() (value string, rejected string) {
	raw := strings.TrimSpace(os.Getenv("DS2API_CHROME_MAJOR_VERSION"))
	if raw == "" {
		return "", ""
	}
	n, err := strconv.Atoi(raw)
	// 这里只管**格式**（把 "abc"、"1" 这类拼进 UA 会直接造出坏指纹的值挡掉）。
	// “库里有没有这个预设”不在此判断，统一交给 clampChromeMajor，避免两处规则漂移。
	if err != nil || n < chromeMajorEnvFloor || n > chromeMajorEnvCeiling {
		return "", raw
	}
	return raw, ""
}

// ensureEnvChromeRead 惰性读取环境变量。必须惰性：本项目在 main() 里才
// LoadDotEnv()，包初始化阶段读不到 .env 中的变量，写死在 var 初始化里
// 会让「在 .env 里配 DS2API_CHROME_MAJOR_VERSION」静默失效。
func ensureEnvChromeRead() {
	if envChromeRead {
		return
	}
	envChromeRead = true
	envChromeMajorVersion, envChromeRejectedValue = readEnvChromeMajorVersion()
}

// RefreshChromeVersionFromEnv 在 .env 加载完成后重新读取一次环境变量，
// 返回被拒绝的非法值（合法或未设为空），供启动日志提醒运维“你以为换了、其实没换”。
func RefreshChromeVersionFromEnv() string {
	chromeVersionMu.Lock()
	defer chromeVersionMu.Unlock()
	envChromeRead = false
	ensureEnvChromeRead()
	return envChromeRejectedValue
}

// SetChromeMajorVersion 让 protocol（共享契约的持有者）把权威版本号推送给传输层。
// 只接受合法大版本号；即使存下推送值，读取时环境变量仍然优先（env > 推送 > 内置）。
func SetChromeMajorVersion(major string) {
	major = strings.TrimSpace(major)
	if major == "" {
		return
	}
	n, err := strconv.Atoi(major)
	if err != nil || n < 1 {
		return
	}
	chromeVersionMu.Lock()
	defer chromeVersionMu.Unlock()
	pushedChromeMajorVersion = major
}

// ChromeMajorVersion 返回当前生效的 Chrome 大版本（HTTP 层与 TLS 层共用）。
// 优先级：环境变量（含 .env）> protocol 推送的 JSON 值 > 内置回退；
// 取到请求值后再按 httpcloak 的真实预设能力钳制，因此返回值永远等于
// ResolvedTLSPresetName() 里的版本号，两层不可能再各说各话。
func ChromeMajorVersion() string {
	chromeVersionMu.Lock()
	defer chromeVersionMu.Unlock()
	ensureEnvChromeRead()
	source := "DS2API_CHROME_MAJOR_VERSION"
	requested := envChromeMajorVersion
	if requested == "" {
		requested = pushedChromeMajorVersion
		source = "constants_shared.json"
	}
	if requested == "" {
		requested = builtinDefaultChromeMajorVersion
		source = "builtin default"
	}
	effective, notice := clampChromeMajor(source, requested)
	if notice != "" {
		chromeClampNotice = notice
	}
	return effective
}

// ResolveChromePreset 把请求的 Chrome 大版本解析为实际使用的 httpcloak 预设名：
// 优先 chrome-<major>-windows；不存在则从 major 递减探测 ≤ 该版本的最新可用预设。
//
// 必须用 fingerprint.GetStrict（无回退）探测——Get 对未知名会静默回退到旧预设，
// 产生「声称 N、实为更旧」的严重矛盾，正是本函数要消除的那类缺陷。
//
// 生效版本已在 ChromeMajorVersion 里按库能力钳制过，所以正常路径下这里都是精确命中；
// 递减探测保留作为最后防线（直接拿任意字符串调用本函数的工具/测试）。
func ResolveChromePreset(major string) string {
	minSupported, maxSupported := chromeMajorBounds()
	n, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil || minSupported <= 0 || maxSupported <= 0 {
		return chromeDefaultPreset
	}
	// 与 clampChromeMajor 用同一区间把请求值拉回可用范围，这样即使调用方
	// 绕过 ChromeMajorVersion 直接传任意版本（工具/测试），解析出的预设版本
	// 仍等于它们声称的那个数字，不会偷换。
	if n > maxSupported {
		n = maxSupported
	}
	if n < minSupported {
		n = minSupported
	}
	for v := n; v >= minSupported; v-- {
		name := "chrome-" + strconv.Itoa(v) + "-windows"
		if fingerprint.GetStrict(name) != nil {
			return name
		}
	}
	return chromeDefaultPreset
}

// ResolvedTLSPresetName 返回当前进程实际使用的 TLS/H2 预设名。
// 每次调用按当前生效版本解析（预设表查找很便宜），因此 protocol 在 init
// 推送版本号之后自动跟随，不存在「先缓存了旧值」的初始化顺序陷阱。
func ResolvedTLSPresetName() string {
	return ResolveChromePreset(ChromeMajorVersion())
}

// TLSChromeVersion 返回 TLS ClientHello 实际复现的 Chrome 大版本，
// 由解析出的预设名推导，因此永远与实际生效的预设一致。
// （旧实现是写死的常量 "133"，与实际生效的 chrome-150-windows 早已不符，
// 只剩 tests/wire-capture 在打印这个误导值。）
func TLSChromeVersion() string {
	return tlsVersionFromPreset(ResolvedTLSPresetName())
}

// tlsVersionFromPreset 从 "chrome-151-windows" 这类预设名中提取大版本号。
func tlsVersionFromPreset(preset string) string {
	p := strings.TrimPrefix(preset, "chrome-")
	if i := strings.IndexByte(p, '-'); i >= 0 {
		p = p[:i]
	}
	return p
}

// chromeHeaderOrder pins the request header order. httpcloak applies it on top
// of its browser preset so every request reaches chat.deepseek.com with a
// stable, Chrome-like header order.
//
// Names must be lowercase — httpcloak lowercases keys before looking them up in
// the order map, and headers absent from this list are appended afterwards in
// lexicographic order (still deterministic).
//
// This mirrors commonly observed Chrome fetch/XHR ordering. It is worth
// re-validating against a live capture of chat.deepseek.com if the upstream
// web client changes which headers it sets. （2026-08-31 用真实 Chrome 152 实测：
// 自定义头与 sec-ch-ua 组的相对顺序每次安装/会话随机，不是稳定指纹信号，
// 这里固定顺序只为可复现，不必追逐真实浏览器的逐次顺序。）
var chromeHeaderOrder = []string{
	"content-length",
	"sec-ch-ua",
	"sec-ch-ua-mobile",
	"sec-ch-ua-platform",
	"authorization",
	"x-client-bundle-id",
	"x-client-locale",
	"x-client-platform",
	"x-client-timezone-offset",
	"x-client-version",
	"x-ds-pow-response",
	"user-agent",
	"content-type",
	"accept",
	"origin",
	"sec-fetch-site",
	"sec-fetch-mode",
	"sec-fetch-dest",
	"referer",
	"accept-encoding",
	"accept-language",
	"cookie",
	"priority",
}

// ChromeHeaderOrder returns a copy of the pinned request header order, for
// tooling that needs to reproduce or diff the exact on-wire ordering.
func ChromeHeaderOrder() []string {
	return append([]string(nil), chromeHeaderOrder...)
}

// ChromePseudoHeaderOrder returns a copy of the pinned HTTP/2 pseudo-header
// order.
func ChromePseudoHeaderOrder() []string {
	return append([]string(nil), chromePseudoHeaderOrder...)
}

// chromePseudoHeaderOrder is Chrome's :method,:authority,:scheme,:path order.
var chromePseudoHeaderOrder = []string{":method", ":authority", ":scheme", ":path"}
