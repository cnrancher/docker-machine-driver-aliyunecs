# syntax=docker/dockerfile:1
FROM golang:1.24.0 AS builder

WORKDIR /src
COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

ARG GOOS
ARG GOARCH
ARG DRIVER_NAME=docker-machine-driver-aliyunecs

RUN mkdir -p /out/bin

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    test -n "${GOOS}"; \
    test -n "${GOARCH}"; \
    ext=""; \
    if [ "${GOOS}" = "windows" ]; then ext=".exe"; fi; \
    arch="${GOOS}-${GOARCH}"; \
    bin="/out/bin/${DRIVER_NAME}${ext}"; \
    echo "Building ${bin} (GOOS=${GOOS} GOARCH=${GOARCH})"; \
    CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
      go build -trimpath -ldflags "-s -w" -o "${bin}" ./; \
    tar czvf "/out/bin/${DRIVER_NAME}-${arch}.tgz" -C "/out/bin" "$(basename "${bin}")"; \
    rm -f "${bin}"

FROM scratch AS artifact
COPY --from=builder /out/ /out/