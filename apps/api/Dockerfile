ARG TARGETOS
ARG TARGETARCH

# === UI builder: build the admin SPA dist ===
FROM --platform=$BUILDPLATFORM node:22-alpine AS ui-builder
WORKDIR /ui
COPY ui/admin/package.json ui/admin/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui/admin/ ./
RUN npm run build:embed

# === Go builder ===
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

ARG REPOSITORY
ARG VERSION=dev
ARG REVISION=none
ARG BUILDTIME

COPY go.mod go.sum ./
RUN go mod download

COPY ./apps/api ./apps/api
COPY ./pkg ./pkg

# Overlay the admin SPA build into the //go:embed-anchored adminui dir
# so the api binary ships with the static assets baked in.
COPY --from=ui-builder /ui/dist/ ./apps/api/cmd/adminui/

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
     -ldflags="-s -w \
         -X github.com/pitabwire/frame/version.Repository=${REPOSITORY} \
         -X github.com/pitabwire/frame/version.Version=${VERSION} \
         -X github.com/pitabwire/frame/version.Commit=${REVISION} \
         -X github.com/pitabwire/frame/version.Date=${BUILDTIME}" \
     -o /app/binary ./apps/api/cmd

FROM cgr.dev/chainguard/static:latest
LABEL maintainer="Bwire Peter <bwire517@gmail.com>"

USER 65532:65532

EXPOSE 80

ARG REPOSITORY
ARG VERSION
ARG REVISION
ARG BUILDTIME
LABEL org.opencontainers.image.title="Stawi Jobs API"
LABEL org.opencontainers.image.version=$VERSION
LABEL org.opencontainers.image.revision=$REVISION
LABEL org.opencontainers.image.created=$BUILDTIME
LABEL org.opencontainers.image.source=$REPOSITORY

WORKDIR /
COPY --from=builder /app/binary /api

ENTRYPOINT ["/api"]
