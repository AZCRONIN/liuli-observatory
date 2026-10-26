# 琉璃观测台：三阶段构建，最终镜像只有一个静态二进制 + IP 库 + 时区数据

# ---- 前端 ----
FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---- 后端（前端打包产物嵌入二进制）----
FROM golang:1.26-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath GOTOOLCHAIN=local
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web/embed.go ./web/embed.go
COPY --from=web /src/web/dist ./web/dist
RUN go build -ldflags="-s -w" -o /out/liuliobs ./cmd/liuliobs \
 && go build -ldflags="-s -w" -o /out/loggen ./cmd/loggen

# ---- 离线 IP 库（ip2region，构建时下载，运行时不联网）----
FROM alpine:3.22 AS geo
RUN apk add --no-cache curl \
 && mkdir /geo \
 && curl -fsSL -o /geo/ip2region_v4.xdb https://github.com/lionsoul2014/ip2region/raw/master/data/ip2region_v4.xdb \
 && curl -fsSL -o /geo/ip2region_v6.xdb https://github.com/lionsoul2014/ip2region/raw/master/data/ip2region_v6.xdb

# ---- 运行 ----
FROM alpine:3.22
RUN apk add --no-cache tzdata ca-certificates \
 && adduser -D -H -u 10001 liuli \
 && mkdir -p /data && chown liuli:liuli /data
COPY --from=build /out/liuliobs /out/loggen /app/
COPY --from=geo /geo /app/ip2region
ENV TZ=Asia/Shanghai \
    LIULI_MODE=all-in-one \
    LIULI_DATA=/data \
    LIULI_LISTEN=0.0.0.0:8090 \
    LIULI_IP2REGION=/app/ip2region \
    LIULI_LOG_ROOT=/logs
USER liuli
VOLUME /data
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s CMD wget -qO- http://127.0.0.1:8090/api/health >/dev/null || exit 1
ENTRYPOINT ["/app/liuliobs"]
