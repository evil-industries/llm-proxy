# Node is a build-time dependency only. Buildx selects native ARM64/AMD64 stages.
FROM node:24-bookworm-slim AS console-builder
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --ignore-scripts
COPY frontend/ ./
RUN npx svelte-kit sync && npm run build && npm run test:static

FROM golang:1.26-bookworm AS builder

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends build-essential git && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./

RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=1 GOOS=linux go build -buildvcs=false -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o ./CLIProxyAPI ./cmd/server/

FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends tzdata ca-certificates && rm -rf /var/lib/apt/lists/*

RUN mkdir /CLIProxyAPI

COPY --from=builder ./app/CLIProxyAPI /CLIProxyAPI/CLIProxyAPI

COPY config.example.yaml /CLIProxyAPI/config.example.yaml
COPY --from=console-builder /frontend/build /CLIProxyAPI/console

WORKDIR /CLIProxyAPI

EXPOSE 8317

ENV TZ=Asia/Shanghai \
    CONSOLE_STATIC_DIR=/CLIProxyAPI/console

RUN cp /usr/share/zoneinfo/${TZ} /etc/localtime && echo "${TZ}" > /etc/timezone

CMD ["./CLIProxyAPI"]
