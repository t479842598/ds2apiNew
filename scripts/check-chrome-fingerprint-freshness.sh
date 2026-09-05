#!/usr/bin/env bash
# 检查 DeepSeek 网页版 Chrome 指纹是否过期，以及"现在能不能升"。
#
# 用法： bash scripts/check-chrome-fingerprint-freshness.sh
# 只读，不修改任何文件。需要网络（curl）：Chrome stable 查 Google versionhistory，
# httpcloak 预设区间查 GitHub raw。本地网络差时先走代理：
#   export HTTPS_PROXY=http://127.0.0.1:7897 HTTP_PROXY=http://127.0.0.1:7897
#
# 输出三件事：① 契约里钉的版本；② 真实 Chrome stable；③ httpcloak 提供到哪——
# 然后给出明确结论与下一步动作（升 httpcloak 还是改 JSON 还是什么都不用做）。
set -u
cd "$(dirname "$0")/.." || exit 1

SHARED=internal/deepseek/protocol/constants_shared.json
GOMOD=go.mod
[ -f "$SHARED" ] || { echo "FATAL: $SHARED not found (run from repo root)" >&2; exit 2; }
[ -f "$GOMOD" ] || { echo "FATAL: $GOMOD not found" >&2; exit 2; }

need() { command -v "$1" >/dev/null 2>&1 || { echo "FATAL: $1 not found" >&2; exit 2; }; }
need curl; need python3; need grep

# ---------- 读取契约 ----------
read -r MAJOR CEILING <<< "$(python3 - "$SHARED" <<'PY'
import json,sys
c = json.load(open(sys.argv[1]))["chrome"]
print(c.get("major_version","?"), c.get("max_supported_major","?"))
PY
)"
[ -n "${MAJOR}" ] && [ -n "${CEILING}" ] || { echo "FATAL: cannot parse $SHARED chrome block" >&2; exit 2; }

# ---------- httpcloak 版本（go.mod 里声明） ----------
HC_VER=$(grep -oE 'github.com/sardanioss/httpcloak v[0-9][^ ]*' "$GOMOD" | head -1 | awk '{print $2}')
[ -n "${HC_VER}" ] || { echo "FATAL: httpcloak version not found in $GOMOD" >&2; exit 2; }

# ---------- 网络部分（失败即报错退出，不给出错误结论） ----------
UA="check-fingerprint/1.0 (+https://github.com/t479842598/ds2apiNew)"
echo "== 拉取外部数据（httpcloak $HC_VER 预设区间 / Chrome stable） =="

PRESETS=$(curl -fsSL --connect-timeout 15 -m 30 -A "$UA" \
  "https://raw.githubusercontent.com/sardanioss/httpcloak/$HC_VER/fingerprint/presets.go" 2>/dev/null) \
  || { echo "FATAL: 拉不到 httpcloak $HC_VER presets.go（网络/代理问题）" >&2; exit 3; }

STABLE=$(curl -fsSL --connect-timeout 15 -m 30 -A "$UA" \
  "https://versionhistory.googleapis.com/v1/chrome/platforms/win/channels/stable/versions?pageSize=1" 2>/dev/null \
  | python3 -c "import json,sys; d=json.load(sys.stdin); vs=d.get('versions',[]); print(vs[0]['version'] if vs else '')" 2>/dev/null) \
  || STABLE=""

if [ -z "$STABLE" ]; then
  echo "FATAL: 拉不到 Chrome stable 版本（Google versionhistory API 不可达）" >&2
  echo "提示：本地网络差可先 export HTTPS_PROXY=http://127.0.0.1:7897 再跑" >&2
  exit 3
fi

# 从 presets.go 枚举 chrome-<N>-windows 的区间（min/max）
read -r HC_MIN HC_MAX <<< "$(printf '%s' "$PRESETS" | grep -oE '"chrome-[0-9]+-windows"' | grep -oE '[0-9]+' | sort -n | awk 'NR==1{min=$1} {max=$1} END{print min, max}')"
[ -n "${HC_MIN:-}" ] && [ -n "${HC_MAX:-}" ] || { echo "FATAL: presets.go 里解析不到 chrome-*-windows" >&2; exit 3; }

STABLE_MAJ=${STABLE%%.*}

# ---------- 报告 ----------
echo
echo "== 现状 =="
echo "契约 chrome.major_version   : $MAJOR"
echo "契约 chrome.max_supported   : $CEILING"
echo "httpcloak $HC_VER 实际提供   : chrome-$HC_MIN-windows .. chrome-$HC_MAX-windows"
echo "Chrome stable (win)         : $STABLE  (大版本 $STABLE_MAJ)"
if [ "$CEILING" != "$HC_MAX" ]; then
  echo
  echo "!! 契约声明的上限($CEILING) ≠ httpcloak 实际上限($HC_MAX)——契约已经过期。"
  echo "   Go 侧守卫测试会报 FAIL；请把 constants_shared.json 的 max_supported_major 改成 $HC_MAX"
  echo "   （并把 major_version 同步到 ≤ $HC_MAX 的值）"
  exit 1
fi

echo
echo "== 结论 =="
if [ "$MAJOR" -eq "$STABLE_MAJ" ]; then
  echo "已追平 stable（当前 $MAJOR）。"
elif [ "$MAJOR" -lt "$STABLE_MAJ" ]; then
  echo "落后 stable $((STABLE_MAJ - MAJOR)) 个大版本。"
fi

if [ "$STABLE_MAJ" -le "$HC_MAX" ]; then
  echo "→ 可以升：httpcloak 已有 chrome-$STABLE_MAJ 预设，把 constants_shared.json 的"
  echo "   major_version（和 max_supported_major）改成 $STABLE_MAJ，按 docs/DEVELOPMENT.md"
  echo "   第 6 节做，跑 tests/wire-capture -ours 自检即可。"
  exit 0
else
  if [ "$STABLE_MAJ" -gt "$HC_MAX" ]; then
    echo "→ 不能升到 stable：httpcloak 最高只到 chrome-$HC_MAX，没有 chrome-$STABLE_MAJ 预设。"
    if [ "$MAJOR" -eq "$HC_MAX" ]; then
      echo "   你已经停在上限 chrome-$HC_MAX。下一步是等 httpcloak 出 chrome-$STABLE_MAJ 预设"
      echo "   （go get 升级后把 major_version 改到 $STABLE_MAJ），在那之前保持现状即可。"
    else
      echo "   先升级依赖（go get github.com/sardanioss/httpcloak@最新版）让它带上 chrome-$STABLE_MAJ；"
      echo "   在那之前保持 major_version ≤ $HC_MAX（不破钳制不变式）。当前最高只能升到 $HC_MAX。"
    fi
  fi
  exit 0
fi