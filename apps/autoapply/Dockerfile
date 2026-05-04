ARG TARGETOS=linux
ARG TARGETARCH=amd64

FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /app

ARG REPOSITORY
ARG VERSION=dev
ARG REVISION=none
ARG BUILDTIME

COPY go.mod go.sum ./
RUN go mod download

COPY ./apps/autoapply ./apps/autoapply
COPY ./pkg ./pkg

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
     -ldflags="-s -w \
         -X github.com/pitabwire/frame/version.Repository=${REPOSITORY} \
         -X github.com/pitabwire/frame/version.Version=${VERSION} \
         -X github.com/pitabwire/frame/version.Commit=${REVISION} \
         -X github.com/pitabwire/frame/version.Date=${BUILDTIME}" \
     -o /app/binary ./apps/autoapply/cmd

# autoapply runs a headless Chromium for ATS form submission, so it
# needs glibc + system libs and cannot use chainguard/static like the
# rest of the fleet. We use debian-slim with only the libs Chromium
# needs and drop to a non-root user.
FROM debian:bookworm-slim
LABEL maintainer="Joakim Bwire <joakimbwire23@gmail.com>"

RUN apt-get update && apt-get install -y --no-install-recommends \
        chromium \
        ca-certificates \
        fonts-liberation \
        libasound2 \
        libatk-bridge2.0-0 \
        libdrm2 \
        libgbm1 \
        libgtk-3-0 \
        libnss3 \
        libx11-xcb1 \
        libxcomposite1 \
        libxdamage1 \
        libxfixes3 \
        libxrandr2 \
        libxshmfence1 \
        xdg-utils \
        tini \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 65532 autoapply \
    && useradd  --system --uid 65532 --gid 65532 --create-home --home-dir /home/autoapply autoapply

USER 65532:65532

EXPOSE 80

ARG REPOSITORY
ARG VERSION
ARG REVISION
ARG BUILDTIME
LABEL org.opencontainers.image.title="Stawi Jobs AutoApply"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$REVISION
LABEL org.opencontainers.image.created=$BUILDTIME
LABEL org.opencontainers.image.source=$REPOSITORY

# go-rod uses launcher.LookPath to find Chromium; pinning it avoids a
# silent fallback to a network download attempt inside the runtime image.
ENV ROD_BROWSER_BIN=/usr/bin/chromium \
    HOME=/home/autoapply \
    XDG_CACHE_HOME=/home/autoapply/.cache \
    XDG_CONFIG_HOME=/home/autoapply/.config

WORKDIR /
COPY --from=builder /app/binary /autoapply

# tini reaps Chromium subprocesses on SIGTERM so killed pods don't leak
# zombie chromium-sandbox processes (combine with launcher.Leakless(true)
# in the browser client).
ENTRYPOINT ["/usr/bin/tini", "--", "/autoapply"]
