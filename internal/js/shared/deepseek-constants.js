'use strict';

const fs = require('fs');
const path = require('path');

const DEFAULT_CLIENT = Object.freeze({
  name: 'DeepSeek',
  platform: 'web',
  androidApiLevel: '',
  locale: 'zh_CN',
});

const DEFAULT_BASE_HEADERS = Object.freeze({
  Host: 'chat.deepseek.com',
  Accept: 'application/json',
  'Content-Type': 'application/json',
  // 真实网页版抓包确认 platform=web 同样携带此头，早前当作 App 专属移除是误判。
  'x-client-bundle-id': 'com.deepseek.chat',
});

// ---- Chrome 指纹 ----
// 唯一权威来源是 Go 侧内嵌的 constants_shared.json（chrome 块）：本文件与
// internal/deepseek/protocol 读同一文件，从机制上消除“两边各写一份 UA 常量
// 然后悄悄错开”的漂移。下面的内置值只在 JSON 缺失/不完整时兜底。
// 注意：Node 路径（Vercel）走原生 fetch，拿不到 uTLS，TLS/HTTP2 指纹无法伪装，
// 这里只能保证 HTTP 头自洽。详见 docs/DEPLOY.md 的风险说明。
//
// 关于版本上限：Go 侧会把生效大版本钳制到 httpcloak 真实存在的 windows 预设
// （见 transport.clampChromeMajor），而 Node 侧没有 TLS 预设可对钳，做不到等价钳制。
// 所以 JSON 里的 chrome.major_version 不得写超过 httpcloak 最高预设的值，
// 否则两边会漂移（Go 降级、Node 照旧）——Go 启动日志会就此发 warn 提示。
const BUILTIN_CHROME = Object.freeze({
  majorVersion: '152',
  greaseFallbackMajor: '152',
  greaseBrands: {
    '150': '"Not;A=Brand";v="8"',
    '151': '"Not=A?Brand";v="99"',
    '152': '"Not?A_Brand";v="24"',
  },
});

// 与 Go 侧 transport.readEnvChromeMajorVersion 同一规则：非法值必须忽略，
// 拼进 UA 的垃圾值会造出 Chrome/abc.0.0.0 这种没人用的坏指纹。
function normalizeChromeMajor(raw) {
  const value = String(raw == null ? '' : raw).trim();
  if (value === '' || !/^\d+$/.test(value)) {
    return '';
  }
  const major = Number(value);
  if (!Number.isFinite(major) || major < 133 || major > 999) {
    return '';
  }
  return value;
}

// GREASE 品牌串是 Chrome 大版本的确定性函数，公式取自 Chromium 源码
// components/embedder_support/user_agent_utils.cc::GetGreasedUserAgentBrandVersion，
// 与 Go 侧 protocol.ComputeChromeGreaseBrand 逐字一致：
//   brand   = "Not" + chars[major % 11] + "A" + chars[(major + 1) % 11] + "Brand"
//   version = versions[major % 3]
const CHROME_GREASE_CHARS = Object.freeze([' ', '(', ':', '-', '.', '/', ')', ';', '=', '?', '_']);
const CHROME_GREASE_VERSIONS = Object.freeze(['8', '99', '24']);

function computeChromeGreaseBrand(major) {
  const n = Number(major);
  if (!Number.isInteger(n) || n <= 0) {
    return null;
  }
  const brand = 'Not' + CHROME_GREASE_CHARS[n % CHROME_GREASE_CHARS.length] +
    'A' + CHROME_GREASE_CHARS[(n + 1) % CHROME_GREASE_CHARS.length] + 'Brand';
  const version = CHROME_GREASE_VERSIONS[n % CHROME_GREASE_VERSIONS.length];
  return JSON.stringify(brand) + ';v=' + JSON.stringify(version);
}

// 与 Go 侧同一优先级：JSON 钉值（逃生口）> 算法计算 > 回退最新已知。
function resolveChromeGreaseBrand(major, brands, fallbackMajor) {
  const pinned = brands[String(major)];
  if (typeof pinned === 'string' && pinned.trim() !== '') {
    return pinned;
  }
  const computed = computeChromeGreaseBrand(major);
  if (computed) {
    return computed;
  }
  return brands[String(fallbackMajor)] || computeChromeGreaseBrand(fallbackMajor) ||
    computeChromeGreaseBrand(BUILTIN_CHROME.majorVersion);
}

function resolveChromeContract() {
  let parsed = {};
  try {
    parsed = readSharedConstants() || {};
  } catch (_err) {
    parsed = {};
  }
  const chrome = parsed.chrome || {};
  const brands = { ...BUILTIN_CHROME.greaseBrands };
  if (chrome.grease_brands && typeof chrome.grease_brands === 'object') {
    for (const [major, brand] of Object.entries(chrome.grease_brands)) {
      if (typeof brand === 'string' && brand.trim() !== '') {
        brands[String(major)] = brand;
      }
    }
  }
  // 环境变量永远优先，与 Go 侧 transport 保持同一优先级规则。
  const envMajor = normalizeChromeMajor(process.env.DS2API_CHROME_MAJOR_VERSION);
  const jsonMajor = normalizeChromeMajor(chrome.major_version) || BUILTIN_CHROME.majorVersion;
  const majorVersion = envMajor || jsonMajor;
  const fallbackMajor = String(chrome.grease_fallback_major || BUILTIN_CHROME.greaseFallbackMajor);
  // GREASE 串：钉值优先，否则按 Chromium 算法计算（升版不再需要手补表）。
  const greaseBrand = resolveChromeGreaseBrand(majorVersion, brands, fallbackMajor);
  return { majorVersion, greaseBrand };
}

const CHROME_CONTRACT = resolveChromeContract();
const CHROME_MAJOR_VERSION = CHROME_CONTRACT.majorVersion;
const CHROME_USER_AGENT = `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/${CHROME_MAJOR_VERSION}.0.0.0 Safari/537.36`;
// 品牌「顺序」每次安装/会话随机、不是指纹信号，顺序沿用此模板不换。
const CHROME_SEC_CH_UA = `${CHROME_CONTRACT.greaseBrand}, "Chromium";v="${CHROME_MAJOR_VERSION}", "Google Chrome";v="${CHROME_MAJOR_VERSION}"`;

const WEB_BROWSER_HEADERS = Object.freeze({
  'User-Agent': CHROME_USER_AGENT,
  'sec-ch-ua': CHROME_SEC_CH_UA,
  'sec-ch-ua-mobile': '?0',
  'sec-ch-ua-platform': '"Windows"',
  Origin: 'https://chat.deepseek.com',
  Referer: 'https://chat.deepseek.com/',
  'sec-fetch-site': 'same-origin',
  'sec-fetch-mode': 'cors',
  'sec-fetch-dest': 'empty',
  // 浏览器 fetch 发 */*，不是 application/json。
  Accept: '*/*',
  // Chrome 12x+ 在 fetch/XHR 上会带 priority。
  // 这里不设置 accept-encoding：Node 的 fetch/undici 自己协商并解压，
  // 手动覆盖会让它把压缩后的字节原样交出来。
  priority: 'u=1, i',
});

// locale -> IANA 时区。偏移在调用时实时计算（含夏令时），与 Go 侧一致。
const LOCALE_TIMEZONES = Object.freeze({
  zh_CN: 'Asia/Shanghai',
  zh_TW: 'Asia/Taipei',
  en_US: 'America/Los_Angeles',
  en_GB: 'Europe/London',
  ja_JP: 'Asia/Tokyo',
  ko_KR: 'Asia/Seoul',
  de_DE: 'Europe/Berlin',
  fr_FR: 'Europe/Paris',
  ru_RU: 'Europe/Moscow',
  es_ES: 'Europe/Madrid',
});

const DEFAULT_TIMEZONE_OFFSET = '28800';

// 「只配了母语」的 Chrome 默认形态，与 Go 侧保持一致（见 constants.go 的说明）。
const LOCALE_ACCEPT_LANGUAGES = Object.freeze({
  zh_CN: 'zh-CN,zh;q=0.9',
  zh_TW: 'zh-TW,zh;q=0.9',
  en_US: 'en-US,en;q=0.9',
  en_GB: 'en-GB,en;q=0.9',
  ja_JP: 'ja-JP,ja;q=0.9',
  ko_KR: 'ko-KR,ko;q=0.9',
  de_DE: 'de-DE,de;q=0.9',
  fr_FR: 'fr-FR,fr;q=0.9',
  ru_RU: 'ru-RU,ru;q=0.9',
  es_ES: 'es-ES,es;q=0.9',
});

const DEFAULT_SKIP_PATTERNS = Object.freeze([
  'quasi_status',
  'elapsed_secs',
  'token_usage',
  'pending_fragment',
  'conversation_mode',
  'fragments/-1/status',
  'fragments/-2/status',
  'fragments/-3/status',
]);

const DEFAULT_SKIP_EXACT_PATHS = Object.freeze([
  'response/search_status',
]);

function asNonEmptyString(value) {
  return typeof value === 'string' && value !== '' ? value : '';
}

function normalizeClient(raw) {
  const client = raw && typeof raw === 'object' && !Array.isArray(raw) ? raw : {};
  return {
    name: asNonEmptyString(client.name) || DEFAULT_CLIENT.name,
    platform: asNonEmptyString(client.platform) || DEFAULT_CLIENT.platform,
    version: asNonEmptyString(client.version),
    androidApiLevel: asNonEmptyString(client.android_api_level) || DEFAULT_CLIENT.androidApiLevel,
    locale: asNonEmptyString(client.locale) || DEFAULT_CLIENT.locale,
  };
}

// 返回该 IANA 时区此刻相对 UTC 的偏移秒数（含夏令时）。
// 做法是把同一时刻按目标时区格式化，再当成 UTC 反解，两者之差即偏移。
function zoneOffsetSeconds(zone, now) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: zone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).formatToParts(now);

  const field = {};
  for (const part of parts) field[part.type] = part.value;
  const asUTC = Date.UTC(
    Number(field.year),
    Number(field.month) - 1,
    Number(field.day),
    Number(field.hour) % 24,
    Number(field.minute),
    Number(field.second),
  );
  // 抹掉毫秒，避免格式化时的秒级截断带来 ±1 秒抖动。
  return Math.round((asUTC - Math.floor(now.getTime() / 1000) * 1000) / 1000);
}

function timezoneOffsetFor(locale) {
  const zone = LOCALE_TIMEZONES[asNonEmptyString(locale)];
  if (!zone) return DEFAULT_TIMEZONE_OFFSET;
  try {
    return String(zoneOffsetSeconds(zone, new Date()));
  } catch {
    return DEFAULT_TIMEZONE_OFFSET;
  }
}

function acceptLanguageFor(locale) {
  const key = asNonEmptyString(locale);
  return key && LOCALE_ACCEPT_LANGUAGES[key] ? LOCALE_ACCEPT_LANGUAGES[key] : 'zh-CN,zh;q=0.9';
}

function isWebPlatform(platform) {
  return asNonEmptyString(platform).toLowerCase() === 'web';
}

function buildBaseHeaders(parsed, client) {
  const rawBaseHeaders = parsed && typeof parsed.base_headers === 'object' && !Array.isArray(parsed.base_headers)
    ? parsed.base_headers
    : {};
  const baseHeaders = { ...DEFAULT_BASE_HEADERS, ...rawBaseHeaders };

  const locale = client.locale || 'zh_CN';
  baseHeaders['x-client-timezone-offset'] = timezoneOffsetFor(locale);

  if (isWebPlatform(client.platform)) {
    Object.assign(baseHeaders, WEB_BROWSER_HEADERS);
    baseHeaders['Accept-Language'] = acceptLanguageFor(locale);
  } else if (client.name && client.version) {
    baseHeaders['User-Agent'] = `${client.name}/${client.version}`;
  }

  if (client.platform) {
    baseHeaders['x-client-platform'] = client.platform;
  }
  if (client.version) {
    baseHeaders['x-client-version'] = client.version;
  }
  if (client.locale) {
    baseHeaders['x-client-locale'] = client.locale;
  }
  return baseHeaders;
}

// 上游风控拦截分类：与 Go 侧 internal/deepseek/protocol/upstream_block.go 同一规则，
// 判定依据全部是「响应状态码 + 响应头」（取自官方前端 main.js commitId 2335d6b）。
// 命中时返回带 logTag 的分类，供调用方打独立告警标签并与账号异常区分；
// 普通 401/403/429（无对应响应头）返回 null，不影响既有处理。
const UPSTREAM_BLOCK_KINDS = Object.freeze({
  waf_captcha: { logTag: '[upstream_waf_captcha]', status: 405, header: 'x-amzn-waf-action', expected: 'captcha' },
  waf_challenge: { logTag: '[upstream_waf_challenge]', status: 202, header: 'x-amzn-waf-action', expected: 'challenge' },
});

function classifyUpstreamBlock(status, headers) {
  const code = Number(status) || 0;
  const get = (name) => {
    if (!headers) return '';
    if (typeof headers.get === 'function') return String(headers.get(name) || '');
    return String(headers[name.toLowerCase()] || headers[name] || '');
  };
  const wafAction = get('x-amzn-waf-action').trim().toLowerCase();
  for (const [kind, rule] of Object.entries(UPSTREAM_BLOCK_KINDS)) {
    if (code === rule.status && wafAction === rule.expected) {
      return { kind, logTag: rule.logTag, wafAction, cfMitigated: '' };
    }
  }
  if ((code === 403 || code === 429) && get('cf-mitigated').trim().toLowerCase() === 'challenge') {
    return { kind: 'cf_challenge', logTag: '[upstream_cf_challenge]', wafAction, cfMitigated: 'challenge' };
  }
  return null;
}

function sharedConstantsPaths() {
  return [
    path.resolve(__dirname, '../../deepseek/protocol/constants_shared.json'),
    path.resolve(process.cwd(), 'internal/deepseek/protocol/constants_shared.json'),
  ];
}

function readSharedConstants() {
  try {
    return require('../../deepseek/protocol/constants_shared.json');
  } catch (_err) {
    // Fall through to filesystem candidates for test and local execution variants.
  }
  for (const sharedPath of sharedConstantsPaths()) {
    try {
      const raw = fs.readFileSync(sharedPath, 'utf8');
      return JSON.parse(raw);
    } catch (_err) {
      // Try the next candidate path; fall back to in-file structural defaults below.
    }
  }
  return {};
}

function loadSharedConstants() {
  const parsed = readSharedConstants();
  const client = normalizeClient(parsed && parsed.client);
  const skipPatterns = Array.isArray(parsed && parsed.skip_contains_patterns)
    ? parsed.skip_contains_patterns.filter((v) => typeof v === 'string' && v !== '')
    : [...DEFAULT_SKIP_PATTERNS];
  const skipExactPaths = Array.isArray(parsed && parsed.skip_exact_paths)
    ? parsed.skip_exact_paths.filter((v) => typeof v === 'string' && v !== '')
    : [...DEFAULT_SKIP_EXACT_PATHS];
  return {
    client,
    baseHeaders: buildBaseHeaders(parsed, client),
    skipPatterns,
    skipExactPaths,
  };
}

const shared = loadSharedConstants();

module.exports = {
  CLIENT: Object.freeze({ ...shared.client }),
  CLIENT_VERSION: shared.client.version,
  CHROME_MAJOR_VERSION,
  computeChromeGreaseBrand,
  classifyUpstreamBlock,
  BASE_HEADERS: Object.freeze(shared.baseHeaders),
  SKIP_PATTERNS: Object.freeze(shared.skipPatterns),
  SKIP_EXACT_PATHS: new Set(shared.skipExactPaths),
  WEB_BROWSER_HEADERS: Object.freeze({ ...WEB_BROWSER_HEADERS }),
  CHROME_USER_AGENT,
  CHROME_SEC_CH_UA,
  timezoneOffsetFor,
  acceptLanguageFor,
  isWebPlatform,
  __test: {
    buildBaseHeaders,
    normalizeClient,
    sharedConstantsPaths,
    timezoneOffsetFor,
    acceptLanguageFor,
  },
};
