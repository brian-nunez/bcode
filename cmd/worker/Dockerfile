# Build stage
FROM golang:1.24-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/worker ./cmd/worker/main.go

# Runtime stage
FROM debian:bookworm

# Install system dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates curl gnupg libnss3 libatk1.0-0 libatk-bridge2.0-0 \
    libcups2 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxrandr2 \
    libgbm1 libasound2 libpango-1.0-0 libpangocairo-1.0-0 libxfixes3 \
    libx11-xcb1 libxcb-dri3-0 libxcursor1 libxi6 libxtst6 libgtk-3-0 \
    libcairo-gobject2 libgdk-pixbuf2.0-0 git \
    && rm -rf /var/lib/apt/lists/*

# Install Go in the runtime stage briefly to install the driver/browsers
# or just copy the binary and use it.
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /app

# Install playwright and browsers
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN go run github.com/playwright-community/playwright-go/cmd/playwright@v0.5200.1 install --with-deps chromium

# Final cleanup of Go to keep image slim (optional, but good)
# RUN rm -rf /usr/local/go

COPY --from=builder /bin/worker /usr/local/bin/worker
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright

ENTRYPOINT ["worker"]
