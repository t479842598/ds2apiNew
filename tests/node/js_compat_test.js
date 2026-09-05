'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

const chatStream = require('../../api/chat-stream.js');
const deepseekConstants = require('../../internal/js/shared/deepseek-constants.js');
const { parseToolCallsDetailed, parseStandaloneToolCallsDetailed } = require('../../internal/js/helpers/stream-tool-sieve.js');

const { parseChunkForContent, estimateTokens } = chatStream.__test;

const compatRoot = path.resolve(__dirname, '../../tests/compat');

function readJSON(filePath) {
  return JSON.parse(fs.readFileSync(filePath, 'utf8'));
}

test('js shared constants derive client headers from shared json', () => {
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const client = shared.client;
  assert.equal(deepseekConstants.CLIENT_VERSION, client.version);
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-version'], client.version);
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-platform'], 'web');
  // 真实网页版抓包确认 platform=web 同样携带此头。
  assert.equal(deepseekConstants.BASE_HEADERS['x-client-bundle-id'], 'com.deepseek.chat');
  assert.equal(deepseekConstants.BASE_HEADERS['Content-Type'], 'application/json');
  // web 平台应使用 Chrome User-Agent，与 Go 侧 httpcloak 预设同源（见 constants_shared.json）。
  assert.ok(deepseekConstants.BASE_HEADERS['User-Agent'].includes('Chrome'), `unexpected user agent=${deepseekConstants.BASE_HEADERS['User-Agent']}`);
  for (const h of ['sec-ch-ua', 'sec-ch-ua-mobile', 'sec-ch-ua-platform', 'sec-fetch-site', 'sec-fetch-mode', 'sec-fetch-dest', 'Referer', 'Origin', 'Accept-Language']) {
    assert.ok(deepseekConstants.BASE_HEADERS[h], `expected browser header missing: ${h}`);
  }
  for (const h of ['accept-encoding', 'accept-charset']) {
    assert.equal(deepseekConstants.BASE_HEADERS[h], undefined, `unexpected header present: ${h}`);
  }
});

// 跨语言单一来源守卫：Chrome 指纹必须来自 constants_shared.json（Go 侧读同一文件）。
// 谁在 JS 里单独改版本号或 GREASE 串而不改 JSON，本用例直接失败。
test('js shared constants derive Chrome fingerprint from shared json', () => {
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const chrome = shared.chrome;
  assert.ok(chrome && chrome.major_version, 'chrome.major_version missing from constants_shared.json');
  assert.ok(chrome.grease_brands && Object.keys(chrome.grease_brands).length > 0, 'chrome.grease_brands missing');

  const envMajor = String(process.env.DS2API_CHROME_MAJOR_VERSION || '').trim();
  if (/^\d+$/.test(envMajor) && Number(envMajor) >= 133) {
    assert.equal(deepseekConstants.CHROME_MAJOR_VERSION, envMajor, 'valid env override must win');
    return;
  }
  const major = chrome.major_version;
  assert.equal(deepseekConstants.CHROME_MAJOR_VERSION, major);
  assert.ok(deepseekConstants.CHROME_USER_AGENT.includes(`Chrome/${major}.0.0.0`), `UA=${deepseekConstants.CHROME_USER_AGENT}`);

  const expectedGrease = chrome.grease_brands[major] || chrome.grease_brands[chrome.grease_fallback_major];
  assert.ok(expectedGrease, `no GREASE brand for major=${major} and no fallback`);
  assert.equal(
    deepseekConstants.CHROME_SEC_CH_UA,
    `${expectedGrease}, "Chromium";v="${major}", "Google Chrome";v="${major}"`,
  );
  assert.equal(deepseekConstants.BASE_HEADERS['sec-ch-ua'], deepseekConstants.CHROME_SEC_CH_UA);
  assert.equal(deepseekConstants.BASE_HEADERS['User-Agent'], deepseekConstants.CHROME_USER_AGENT);
});

// 与 Go 侧 protocol/upstream_block_test.go 同一张判定表：两边必须一致。
test('js classifyUpstreamBlock matches the Go classification table', () => {
  const { classifyUpstreamBlock } = deepseekConstants;
  const headersOf = (obj) => ({ get: (name) => obj[String(name).toLowerCase()] || null });

  const hits = [
    [405, { 'x-amzn-waf-action': 'captcha' }, 'waf_captcha', '[upstream_waf_captcha]'],
    [202, { 'x-amzn-waf-action': 'challenge' }, 'waf_challenge', '[upstream_waf_challenge]'],
    [403, { 'cf-mitigated': 'challenge' }, 'cf_challenge', '[upstream_cf_challenge]'],
    [429, { 'cf-mitigated': 'challenge' }, 'cf_challenge', '[upstream_cf_challenge]'],
  ];
  for (const [status, headers, kind, logTag] of hits) {
    const got = classifyUpstreamBlock(status, headersOf(headers));
    assert.ok(got, `status=${status} ${JSON.stringify(headers)} should be classified`);
    assert.equal(got.kind, kind);
    assert.equal(got.logTag, logTag);
  }

  const misses = [
    [401, {}],
    [403, {}],
    [429, {}],
    [403, { 'cf-mitigated': 'managed' }],
    [405, { 'x-amzn-waf-action': 'challenge' }],
    [202, {}],
    [200, { 'x-amzn-waf-action': 'captcha' }],
  ];
  for (const [status, headers] of misses) {
    assert.equal(classifyUpstreamBlock(status, headersOf(headers)), null, `status=${status} ${JSON.stringify(headers)} must not be classified`);
  }
  assert.equal(classifyUpstreamBlock(403, null), null);
});

// GREASE 串由 Chromium 源码公式计算（与 Go 侧 protocol.ComputeChromeGreaseBrand 同一公式）。
// 本用例把公式输出与已实测/已钉值历史交叉比对：不一致即公式移植错。
test('js computeChromeGreaseBrand reproduces pinned history and matches Go formula', () => {
  const { computeChromeGreaseBrand } = deepseekConstants;
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const pinned = (shared.chrome && shared.chrome.grease_brands) || {};
  assert.ok(Object.keys(pinned).length >= 3, 'expected pinned GREASE history in the contract');
  for (const [major, want] of Object.entries(pinned)) {
    assert.equal(computeChromeGreaseBrand(major), want, `algorithm disagrees with pinned value for ${major}`);
  }
  // 未钉住的新版本应能算出（升版不需手补表）。
  assert.equal(computeChromeGreaseBrand('153'), '"Not_A Brand";v="8"');
  assert.equal(computeChromeGreaseBrand('149'), '"Not)A;Brand";v="24"');
  for (const bad of ['', 'abc', '0', '-1', null, undefined]) {
    assert.equal(computeChromeGreaseBrand(bad), null, `${JSON.stringify(bad)} must be rejected`);
  }
});

test('js compat: sse fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'sse_chunks');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
    const fixture = readJSON(path.join(fixtureDir, file));
    const expected = readJSON(path.join(expectedDir, `sse_${name}.json`));
    const got = parseChunkForContent(fixture.chunk, Boolean(fixture.thinking_enabled), fixture.current_type || 'text');
    assert.deepEqual(got.parts, expected.parts, `${name}: parts mismatch`);
    assert.equal(got.finished, expected.finished, `${name}: finished mismatch`);
    assert.equal(got.newType, expected.new_type, `${name}: newType mismatch`);
    assert.equal(Boolean(got.contentFilter), Boolean(expected.content_filter), `${name}: contentFilter mismatch`);
    assert.equal(got.errorMessage || '', expected.error_message || '', `${name}: errorMessage mismatch`);
  }
});

test('js compat: toolcall fixtures', () => {
  const fixtureDir = path.join(compatRoot, 'fixtures', 'toolcalls');
  const expectedDir = path.join(compatRoot, 'expected');
  const files = fs.readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort();
  assert.ok(files.length > 0);

  for (const file of files) {
    const name = file.replace(/\.json$/i, '');
      const fixture = readJSON(path.join(fixtureDir, file));
      const expected = readJSON(path.join(expectedDir, `toolcalls_${name}.json`));
      const mode = typeof fixture.mode === 'string' ? fixture.mode.trim().toLowerCase() : '';
      const parser = mode === 'standalone' ? parseStandaloneToolCallsDetailed : parseToolCallsDetailed;
      const got = parser(fixture.text, fixture.tool_names || []);
      assert.deepEqual(got.calls, expected.calls, `${name}: calls mismatch`);
      assert.equal(got.sawToolCallSyntax, expected.sawToolCallSyntax, `${name}: sawToolCallSyntax mismatch`);
      assert.equal(got.rejectedByPolicy, expected.rejectedByPolicy, `${name}: rejectedByPolicy mismatch`);
      assert.deepEqual(got.rejectedToolNames, expected.rejectedToolNames, `${name}: rejectedToolNames mismatch`);
    }
  });

test('js compat: token fixtures', () => {
  const fixture = readJSON(path.join(compatRoot, 'fixtures', 'token_cases.json'));
  const expected = readJSON(path.join(compatRoot, 'expected', 'token_cases.json'));
  const expectedByName = new Map(expected.cases.map((c) => [c.name, c.tokens]));
  for (const c of fixture.cases) {
    assert.ok(expectedByName.has(c.name), `missing expected case: ${c.name}`);
    const got = estimateTokens(c.text);
    assert.equal(got, expectedByName.get(c.name), `${c.name}: tokens mismatch`);
  }
});

// Node/Vercel 侧必须与 Go 侧钳到同一个版本。契约里显式声明的 max_supported_major
// 是两侧唯一的共同依据：Go 从 fingerprint.Available() 枚举，Node 读不到依赖注册表。
// 缺这个字段、或超限请求没被钳下来，都算失败。
test('js chrome major clamps to contract max_supported_major like the go side', () => {
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const chrome = shared.chrome || {};
  assert.ok(chrome.max_supported_major, 'chrome.max_supported_major missing from constants_shared.json');
  const ceiling = Number(chrome.max_supported_major);
  assert.ok(Number.isInteger(ceiling) && ceiling > 0, `max_supported_major not a version: ${chrome.max_supported_major}`);
  // 契约生效值本身不得超过声明上限，否则 Go 会钳、Node 也会钳但依据不同，等于没对齐。
  assert.ok(Number(chrome.major_version) <= ceiling,
    `chrome.major_version=${chrome.major_version} exceeds max_supported_major=${ceiling}`);

  const prev = process.env.DS2API_CHROME_MAJOR_VERSION;
  process.env.DS2API_CHROME_MAJOR_VERSION = String(ceiling + 1);
  try {
    const resolved = deepseekConstants.__test.resolveChromeContract();
    assert.equal(resolved.majorVersion, chrome.max_supported_major,
      'over-ceiling request must clamp to the contract ceiling, same as the Go side');
    assert.ok(String(resolved.clampNotice).includes('clamped'), 'clamp must be reported, not silent');
    // 钳制后 GREASE 必须按新版本算，否则会出现 UA 报 152、GREASE 报 153 的新矛盾。
    assert.ok(resolved.greaseBrand.includes(';v='), `grease brand missing for clamped major: ${resolved.greaseBrand}`);
    const expectedGrease = chrome.grease_brands
      && chrome.grease_brands[chrome.max_supported_major];
    if (expectedGrease) {
      assert.equal(resolved.greaseBrand, expectedGrease, 'GREASE must follow the clamped major');
    }
  } finally {
    if (prev === undefined) delete process.env.DS2API_CHROME_MAJOR_VERSION;
    else process.env.DS2API_CHROME_MAJOR_VERSION = prev;
  }
});

// 未超限时不得误钳：否则每次正常启动都会假告警，运维很快就不看日志了。
test('js chrome major is untouched when within contract ceiling', () => {
  const shared = readJSON(path.resolve(__dirname, '../../internal/deepseek/protocol/constants_shared.json'));
  const chrome = shared.chrome || {};
  const prev = process.env.DS2API_CHROME_MAJOR_VERSION;
  try {
    delete process.env.DS2API_CHROME_MAJOR_VERSION;
    const resolved = deepseekConstants.__test.resolveChromeContract();
    assert.equal(resolved.majorVersion, chrome.major_version, 'default contract value must pass through');
    assert.equal(resolved.clampNotice, '', 'no clamp notice expected within range');
  } finally {
    if (prev === undefined) delete process.env.DS2API_CHROME_MAJOR_VERSION;
    else process.env.DS2API_CHROME_MAJOR_VERSION = prev;
  }
});
