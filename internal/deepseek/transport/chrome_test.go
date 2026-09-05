package transport

import (
	"strconv"
	"strings"
	"testing"

	"github.com/sardanioss/httpcloak/fingerprint"
)

func TestResolveChromePresetExactMatch(t *testing.T) {
	if got := ResolveChromePreset("152"); got != "chrome-152-windows" {
		t.Fatalf("ResolveChromePreset(152) = %q, want chrome-152-windows", got)
	}
	if got := ResolveChromePreset("151"); got != "chrome-151-windows" {
		t.Fatalf("ResolveChromePreset(151) = %q, want chrome-151-windows", got)
	}
	if got := ResolveChromePreset("150"); got != "chrome-150-windows" {
		t.Fatalf("ResolveChromePreset(150) = %q, want chrome-150-windows", got)
	}
}

func TestChromeMaxSupportedMajorMatchesDependency(t *testing.T) {
	// 区间必须来自依赖真实注册的预设，而不是代码里手写的数字。
	minSupported, maxSupported := chromeMajorBounds()
	if minSupported <= 0 || maxSupported < minSupported {
		t.Fatalf("chromeMajorBounds() = (%d, %d), want a usable range", minSupported, maxSupported)
	}
	for _, n := range []int{minSupported, maxSupported} {
		if fingerprint.GetStrict("chrome-"+strconv.Itoa(n)+"-windows") == nil {
			t.Fatalf("bound %d has no chrome-%d-windows preset", n, n)
		}
	}
	if fingerprint.GetStrict("chrome-"+strconv.Itoa(maxSupported+1)+"-windows") != nil {
		t.Fatalf("ceiling %d is stale: chrome-%d-windows exists", maxSupported, maxSupported+1)
	}
	if fingerprint.GetStrict("chrome-"+strconv.Itoa(minSupported-1)+"-windows") != nil {
		t.Fatalf("floor %d is stale: chrome-%d-windows exists", minSupported, minSupported-1)
	}
	// 内置回退与兜底预设必须落在库能力范围内，否则默认指纹自己就是矛盾的。
	if got, _ := strconv.Atoi(builtinDefaultChromeMajorVersion); got != maxSupported {
		t.Fatalf("builtinDefaultChromeMajorVersion = %d, want the preset ceiling %d", got, maxSupported)
	}
	if tlsVersionFromPreset(chromeDefaultPreset) != strconv.Itoa(maxSupported) {
		t.Fatalf("chromeDefaultPreset %q disagrees with ceiling %d", chromeDefaultPreset, maxSupported)
	}
}

// isolateChromeEnv 确保测试里只有“推送值”这一来路生效：否则进程环境里残留的
// DS2API_CHROME_MAJOR_VERSION 会压住 SetChromeMajorVersion，让断言跑在错的对象上。
func isolateChromeEnv(t *testing.T) {
	t.Helper()
	prevEnv, prevRejected, prevRead := envChromeMajorVersion, envChromeRejectedValue, envChromeRead
	prevPush, prevNotice := pushedChromeMajorVersion, chromeClampNotice
	t.Setenv("DS2API_CHROME_MAJOR_VERSION", "")
	envChromeRead = false
	envChromeMajorVersion, envChromeRejectedValue = "", ""
	t.Cleanup(func() {
		envChromeMajorVersion, envChromeRejectedValue, envChromeRead = prevEnv, prevRejected, prevRead
		pushedChromeMajorVersion, chromeClampNotice = prevPush, prevNotice
	})
}

// TestChromeMajorVersionNeverContradictsTLSPreset 是本次修复的核心回归位：
// 旧实现允许「UA 声称 153、ClientHello 实为 chrome-152-windows」这种在真实
// 浏览器里根本不存在的指纹。对任何请求值，两层都必须报同一个大版本。
func TestChromeMajorVersionNeverContradictsTLSPreset(t *testing.T) {
	isolateChromeEnv(t)
	defer func(prevPush string, prevNotice string) {
		pushedChromeMajorVersion = prevPush
		chromeClampNotice = prevNotice
	}(pushedChromeMajorVersion, chromeClampNotice)

	for _, requested := range []string{"100", "133", "142", "149", "150", "151", "152", "153", "154", "999"} {
		pushedChromeMajorVersion = ""
		chromeClampNotice = ""
		SetChromeMajorVersion(requested)
		major := ChromeMajorVersion()
		if got, want := TLSChromeVersion(), major; got != want {
			t.Fatalf("requested %s: TLS says %q but UA/sec-ch-ua says %q (preset %q)",
				requested, got, want, ResolvedTLSPresetName())
		}
		if !strings.HasPrefix(ResolvedTLSPresetName(), "chrome-"+major+"-windows") {
			t.Fatalf("requested %s: effective major %q != preset %q", requested, major, ResolvedTLSPresetName())
		}
	}
}

func TestChromeMajorVersionClampsAboveCeilingWithNotice(t *testing.T) {
	isolateChromeEnv(t)
	defer func(prevPush string, prevNotice string) {
		pushedChromeMajorVersion = prevPush
		chromeClampNotice = prevNotice
	}(pushedChromeMajorVersion, chromeClampNotice)

	maxSupported := chromeMaxSupportedMajor()
	pushedChromeMajorVersion = ""
	chromeClampNotice = ""
	SetChromeMajorVersion(strconv.Itoa(maxSupported + 1))
	if got := ChromeMajorVersion(); got != strconv.Itoa(maxSupported) {
		t.Fatalf("ChromeMajorVersion above ceiling = %q, want clamped %d", got, maxSupported)
	}
	// 静默降级是本函数要消除的毛病：钳制必须留下可读的告警。
	if notice := ChromeVersionClampNotice(); !strings.Contains(notice, "clamped") {
		t.Fatalf("clamp notice missing after clamping, got %q", notice)
	}
}

// TestClampNoticeNamesTheActualSource 验证告警里的来源标签是对的。
// 写错来源比不写更坏：运维会去改 JSON，而真正该改的是 .env（或反之）。
func TestClampNoticeNamesTheActualSource(t *testing.T) {
	prevEnv, prevRejected, prevRead := envChromeMajorVersion, envChromeRejectedValue, envChromeRead
	prevPush, prevNotice := pushedChromeMajorVersion, chromeClampNotice
	defer func() {
		envChromeMajorVersion, envChromeRejectedValue, envChromeRead = prevEnv, prevRejected, prevRead
		pushedChromeMajorVersion, chromeClampNotice = prevPush, prevNotice
	}()

	maxSupported := chromeMaxSupportedMajor()
	requested := strconv.Itoa(maxSupported + 1)

	t.Setenv("DS2API_CHROME_MAJOR_VERSION", requested)
	pushedChromeMajorVersion = ""
	chromeClampNotice = ""
	RefreshChromeVersionFromEnv()
	if got := ChromeMajorVersion(); got != strconv.Itoa(maxSupported) {
		t.Fatalf("env %s: effective = %q, want %d", requested, got, maxSupported)
	}
	if notice := ChromeVersionClampNotice(); !strings.Contains(notice, "DS2API_CHROME_MAJOR_VERSION") {
		t.Fatalf("notice must name the env var as source, got %q", notice)
	}

	t.Setenv("DS2API_CHROME_MAJOR_VERSION", "")
	envChromeRead = false
	envChromeMajorVersion = ""
	chromeClampNotice = ""
	pushedChromeMajorVersion = ""
	SetChromeMajorVersion(requested)
	ChromeMajorVersion()
	if notice := ChromeVersionClampNotice(); !strings.Contains(notice, "constants_shared.json") {
		t.Fatalf("notice must name constants_shared.json as source, got %q", notice)
	}
}

func TestChromeMajorVersionClampsBelowFloor(t *testing.T) {
	defer func(prevPush string, prevNotice string) {
		pushedChromeMajorVersion = prevPush
		chromeClampNotice = prevNotice
	}(pushedChromeMajorVersion, chromeClampNotice)

	// JSON 推送只校验“是正整数”，所以 100 这种库里没有预设的旧版本能进来；
	// 必须钳到可用区间下界，而不是拼出 Chrome/100.0.0.0 + 一个不相干的预设。
	minSupported, _ := chromeMajorBounds()
	pushedChromeMajorVersion = ""
	chromeClampNotice = ""
	SetChromeMajorVersion("100")
	if got, want := ChromeMajorVersion(), strconv.Itoa(minSupported); got != want {
		t.Fatalf("ChromeMajorVersion(100) = %q, want clamped %q", got, want)
	}
	if got, want := TLSChromeVersion(), strconv.Itoa(minSupported); got != want {
		t.Fatalf("below-floor clamp left TLS at %q, want %q", got, want)
	}
	if notice := ChromeVersionClampNotice(); notice == "" {
		t.Fatal("below-floor clamp must be reported")
	}
}

func TestClampChromeMajorPassesThroughInRange(t *testing.T) {
	minSupported, maxSupported := chromeMajorBounds()
	for _, major := range []string{strconv.Itoa(minSupported), "150", "151", strconv.Itoa(maxSupported)} {
		got, notice := clampChromeMajor("test", major)
		if got != major || notice != "" {
			t.Fatalf("clampChromeMajor(%q) = (%q, %q), want pass-through", major, got, notice)
		}
	}
}

func TestResolveChromePresetFallsDownToLatestAvailable(t *testing.T) {
	// 直接拿超限值调用本函数（绕过 ChromeMajorVersion 的钳制，例如工具代码）时，
	// 仍必须解析到 ≤ 请求值的最新可用预设，绝不落到一个更旧或更新的版本上。
	maxSupported := chromeMaxSupportedMajor()
	if got, want := ResolveChromePreset("999"), "chrome-"+strconv.Itoa(maxSupported)+"-windows"; got != want {
		t.Fatalf("ResolveChromePreset(999) = %q, want %q", got, want)
	}
	// 区间内的空洞（如 android/ios 缺 149，windows 理论上也可能出现空洞）必须向下探到真实存在的预设。
	if got := ResolveChromePreset(strconv.Itoa(maxSupported)); got != "chrome-"+strconv.Itoa(maxSupported)+"-windows" {
		t.Fatalf("ResolveChromePreset(ceiling) = %q, want the ceiling preset itself", got)
	}
}

func TestReadEnvChromeMajorVersionValidation(t *testing.T) {
	cases := []struct {
		raw      string
		want     string
		rejected string
	}{
		{"", "", ""},
		{"151", "151", ""},
		{" 152 ", "152", ""},
		{"abc", "", "abc"},
		{"15x", "", "15x"},
		{"1", "", "1"},
		{"99", "", "99"},
		// 格式合法但库里没预设的版本不再在读取阶段拒绝：它们会被
		// clampChromeMajor 拉回可用区间并留下告警，比“静默丢弃运维的意图”更诚实。
		{"132", "132", ""},
		{"1000", "1000", ""},
		{"10000", "", "10000"},
	}
	for _, tc := range cases {
		t.Setenv("DS2API_CHROME_MAJOR_VERSION", tc.raw)
		got, rejected := readEnvChromeMajorVersion()
		if got != tc.want || rejected != tc.rejected {
			t.Fatalf("raw=%q got=%q rejected=%q, want got=%q rejected=%q", tc.raw, got, rejected, tc.want, tc.rejected)
		}
	}
}

func TestSetChromeMajorVersionRejectsGarbage(t *testing.T) {
	defer func(prev string) { pushedChromeMajorVersion = prev }(pushedChromeMajorVersion)
	pushedChromeMajorVersion = ""
	SetChromeMajorVersion("abc")
	if pushedChromeMajorVersion != "" {
		t.Fatalf("garbage push must be ignored, got %q", pushedChromeMajorVersion)
	}
	SetChromeMajorVersion("150")
	if pushedChromeMajorVersion != "150" {
		t.Fatalf("valid push not stored, got %q", pushedChromeMajorVersion)
	}
}

func TestResolveChromePresetInvalidMajorUsesDefault(t *testing.T) {
	// 非数字不得 panic，也不得拼进预设名，统一走兜底预设。
	for _, major := range []string{"", "abc", "9999999999999999999999"} {
		if got := ResolveChromePreset(major); got != chromeDefaultPreset {
			t.Fatalf("ResolveChromePreset(%q) = %q, want %q", major, got, chromeDefaultPreset)
		}
	}
	// 数字越界不兜底到默认值，而是钳到区间边界——这样本函数的输出永远等于
	// clampChromeMajor 给出的生效版本，不存在“UA 一个版、TLS 另一个版”的窗口。
	minSupported, maxSupported := chromeMajorBounds()
	if got, want := ResolveChromePreset("1"), "chrome-"+strconv.Itoa(minSupported)+"-windows"; got != want {
		t.Fatalf("ResolveChromePreset(1) = %q, want %q", got, want)
	}
	if got, want := ResolveChromePreset("9999"), "chrome-"+strconv.Itoa(maxSupported)+"-windows"; got != want {
		t.Fatalf("ResolveChromePreset(9999) = %q, want %q", got, want)
	}
}

func TestChromeMajorVersionPrecedence(t *testing.T) {
	// 环境变量 > protocol 推送值 > 内置回退。环境变量惰性读取（.env 在 main 里才加载），
	// 先触发一次读取再判断是否被设置。
	_ = ChromeMajorVersion()
	if envChromeMajorVersion == "" {
		defer func(prev string) { pushedChromeMajorVersion = prev }(pushedChromeMajorVersion)
		pushedChromeMajorVersion = ""
		if got := ChromeMajorVersion(); got != builtinDefaultChromeMajorVersion {
			t.Fatalf("no env/no push -> %q, want builtin %q", got, builtinDefaultChromeMajorVersion)
		}
		SetChromeMajorVersion("150")
		if got := ChromeMajorVersion(); got != "150" {
			t.Fatalf("pushed 150 -> %q", got)
		}
		if got := ResolvedTLSPresetName(); got != "chrome-150-windows" {
			t.Fatalf("ResolvedTLSPresetName after push = %q, want chrome-150-windows", got)
		}
		if got := TLSChromeVersion(); got != "150" {
			t.Fatalf("TLSChromeVersion after push = %q, want 150", got)
		}
		// 空推送值不得清空当前版本。
		SetChromeMajorVersion("  ")
		if got := ChromeMajorVersion(); got != "150" {
			t.Fatalf("empty push must not clear version, got %q", got)
		}
	}
}

func TestTLSVersionTracksResolvedPreset(t *testing.T) {
	// TLS 版本必须永远等于实际解析出的预设里的版本号（旧实现写死 133 的缺陷回归位）。
	preset := ResolvedTLSPresetName()
	if got, want := TLSChromeVersion(), tlsVersionFromPreset(preset); got != want {
		t.Fatalf("TLSChromeVersion() = %q, want %q (preset %q)", got, want, preset)
	}
	if !strings.HasPrefix(preset, "chrome-") || !strings.HasSuffix(preset, "-windows") {
		t.Fatalf("unexpected preset name %q", preset)
	}
}

func TestTLSVersionFromPreset(t *testing.T) {
	cases := map[string]string{
		"chrome-151-windows": "151",
		"chrome-150":         "150",
		"chrome-133":         "133",
	}
	for preset, want := range cases {
		if got := tlsVersionFromPreset(preset); got != want {
			t.Fatalf("tlsVersionFromPreset(%q) = %q, want %q", preset, got, want)
		}
	}
}
