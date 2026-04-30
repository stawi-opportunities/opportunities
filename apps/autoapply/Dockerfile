FROM golang:1.26-bookworm AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /autoapply ./apps/autoapply/cmd

# Install Chromium for go-rod headless browser automation.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium \
    ca-certificates \
    fonts-liberation \
    libasound2 \
    libatk-bridge2.0-0 \
    libgtk-3-0 \
    libnss3 \
    libx11-xcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxrandr2 \
    xdg-utils \
    && rm -rf /var/lib/apt/lists/*

# go-rod looks up the browser by path; point it at the system Chromium.
ENV ROD_BROWSER_BIN=/usr/bin/chromium

COPY --from=builder /autoapply /autoapply
ENTRYPOINT ["/autoapply"]
