FROM node:24 AS webui-builder

WORKDIR /app/webui
COPY webui/package.json webui/package-lock.json ./
RUN npm ci
COPY config.example.json /app/config.example.json
COPY webui ./
RUN npm run build

FROM golang:1.26 AS go-builder
WORKDIR /app
ARG TARGETOS
ARG TARGETARCH
ARG BUILD_VERSION
# 国内构建可传 --build-arg GOPROXY=https://goproxy.cn,direct 走模块代理。
ARG GOPROXY=
RUN set -eux; if [ -n "${GOPROXY}" ]; then go env -w GOPROXY="${GOPROXY}"; fi
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN set -eux; \
    GOOS="${TARGETOS:-$(go env GOOS)}"; \
    GOARCH="${TARGETARCH:-$(go env GOARCH)}"; \
    BUILD_VERSION_RESOLVED="${BUILD_VERSION:-}"; \
    if [ -z "${BUILD_VERSION_RESOLVED}" ] && [ -f VERSION ]; then BUILD_VERSION_RESOLVED="$(cat VERSION | tr -d "[:space:]")"; fi; \
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" go build -buildvcs=false -ldflags="-s -w -X ds2api/internal/version.BuildVersion=${BUILD_VERSION_RESOLVED}" -o /out/ds2api ./cmd/ds2api

FROM busybox:1.36.1-musl AS busybox-tools

# 下载与目标架构匹配的 Mihomo 内核（裸 ELF gzip 资产），
# 使镜像开箱即带代理桥能力，无需用户手动下载。
# 国内构建可传 --build-arg MIHOMO_MIRROR=https://ghfast.top/ 走加速前缀；
# 传入后优先尝试镜像前缀，失败再回落官方源。
FROM debian:bookworm-slim AS mihomo-downloader
ARG MIHOMO_VERSION=v1.19.29
ARG MIHOMO_MIRROR=
# 国内构建可传 --build-arg APT_MIRROR=http://mirrors.aliyun.com 避开
# deb.debian.org 卡死（一次 apt-get update 能耗掉二十多分钟）。
# 注意：必须用 **http** 而非 https——这两个阶段正要安装 ca-certificates，
# 镜像里还没有 CA 根证书库，https 源会因 TLS 校验失败而拿不到包列表
# （表现为“Package ca-certificates is not available”）。
ARG APT_MIRROR=
ARG TARGETARCH
# 允许离线构建：把预先下载好的 mihomo 压缩包放进 build context 的
# docker/mihomo-local/ 即可。国内服务器直连 GitHub releases 实测只有 ~10KB/s
# （17MB 要 27 分钟，常常直接超时失败），而本地代理下载同一文件只要 1 秒。
# 目录里带一个 .gitkeep，保证没放 .gz 时 COPY 也不会报错（退回线上下载）。
COPY docker/mihomo-local/ /tmp/mihomo-local/
RUN set -eux; \
    if [ -n "${APT_MIRROR}" ]; then \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        [ -f "${f}" ] && sed -i "s|http://deb.debian.org/debian|${APT_MIRROR}/debian|g; s|http://security.debian.org/debian-security|${APT_MIRROR}/debian-security|g" "${f}" || true; \
      done; \
    fi; \
    apt-get update; \
    apt-get install -y --no-install-recommends curl ca-certificates gzip; \
    rm -rf /var/lib/apt/lists/*; \
    case "${TARGETARCH:-amd64}" in \
      amd64|arm64) MIHOMO_ARCH="${TARGETARCH:-amd64}" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    ASSET="mihomo-linux-${MIHOMO_ARCH}-${MIHOMO_VERSION}.gz"; \
    RELEASE_URL="https://github.com/MetaCubeX/mihomo/releases/download/${MIHOMO_VERSION}/${ASSET}"; \
    mkdir -p /out; \
    ok=0; \
    LOCAL_ASSET=""; \
    for cand in "/tmp/mihomo-local/${ASSET}" /tmp/mihomo-local/mihomo-linux-${MIHOMO_ARCH}-*.gz; do \
      if [ -f "${cand}" ]; then LOCAL_ASSET="${cand}"; break; fi; \
    done; \
    if [ -n "${LOCAL_ASSET}" ]; then \
      echo "using pre-downloaded mihomo archive: ${LOCAL_ASSET}"; \
      cp "${LOCAL_ASSET}" /tmp/mihomo.gz; ok=1; \
    else \
      for url in "${MIHOMO_MIRROR}${RELEASE_URL}" "${RELEASE_URL}"; do \
        [ -z "${url}" ] && continue; \
        if curl -fL --retry 3 --connect-timeout 20 -o /tmp/mihomo.gz "${url}"; then ok=1; break; fi; \
      done; \
    fi; \
    [ "${ok}" = "1" ]; \
    gzip -dc /tmp/mihomo.gz > /out/mihomo; \
    rm -f /tmp/mihomo.gz; \
    chmod 0755 /out/mihomo

FROM debian:bookworm-slim AS runtime-base
WORKDIR /app
ARG APT_MIRROR=
# 必须安装 CA 根证书：否则容器内拉取 HTTPS 机场订阅会报
# x509: certificate signed by unknown authority。
# update-ca-certificates 确保系统根证书库就绪（含内网/企业自建 CA 追加场景）。
RUN set -eux; \
    if [ -n "${APT_MIRROR}" ]; then \
      for f in /etc/apt/sources.list /etc/apt/sources.list.d/debian.sources; do \
        [ -f "${f}" ] && sed -i "s|http://deb.debian.org/debian|${APT_MIRROR}/debian|g; s|http://security.debian.org/debian-security|${APT_MIRROR}/debian-security|g" "${f}" || true; \
      done; \
    fi; \
    apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && update-ca-certificates \
    && groupadd -r ds2api && useradd -r -g ds2api -d /app -s /sbin/nologin ds2api \
    && mkdir -p /app/data /data && chown -R ds2api:ds2api /app /data
COPY --from=busybox-tools /bin/busybox /usr/local/bin/busybox
COPY --from=mihomo-downloader /out/mihomo /usr/local/bin/mihomo
EXPOSE 5001
CMD ["/usr/local/bin/ds2api"]

FROM runtime-base AS runtime-from-source
COPY --from=go-builder /out/ds2api /usr/local/bin/ds2api

COPY --from=go-builder --chown=ds2api:ds2api /app/config.example.json /app/config.example.json
COPY --from=webui-builder --chown=ds2api:ds2api /app/static/admin /app/static/admin
USER ds2api

FROM busybox-tools AS dist-extract
ARG TARGETARCH
COPY dist/docker-input/linux_amd64.tar.gz /tmp/ds2api_linux_amd64.tar.gz
COPY dist/docker-input/linux_arm64.tar.gz /tmp/ds2api_linux_arm64.tar.gz
RUN set -eux; \
    case "${TARGETARCH}" in \
      amd64) ARCHIVE="/tmp/ds2api_linux_amd64.tar.gz" ;; \
      arm64) ARCHIVE="/tmp/ds2api_linux_arm64.tar.gz" ;; \
      *) echo "unsupported TARGETARCH: ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    tar -xzf "${ARCHIVE}" -C /tmp; \
    PKG_DIR="$(find /tmp -maxdepth 1 -type d -name "ds2api_*_linux_${TARGETARCH}" | head -n1)"; \
    test -n "${PKG_DIR}"; \
    mkdir -p /out/static; \
    cp "${PKG_DIR}/ds2api" /out/ds2api; \
    cp "${PKG_DIR}/config.example.json" /out/config.example.json; \
    cp -R "${PKG_DIR}/static/admin" /out/static/admin

FROM runtime-base AS runtime-from-dist
COPY --from=dist-extract /out/ds2api /usr/local/bin/ds2api

COPY --from=dist-extract --chown=ds2api:ds2api /out/config.example.json /app/config.example.json
COPY --from=dist-extract --chown=ds2api:ds2api /out/static/admin /app/static/admin
USER ds2api

FROM runtime-from-source AS final
