# Build stage
FROM golang:1.24-bookworm AS builder

WORKDIR /src

# Copy module files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source
COPY . .

# Build the worker binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/worker ./cmd/worker/main.go

# Runtime stage
FROM debian:bookworm

# Install system dependencies required for Playwright/Browsers
# We install the base deps here. playwright.Install() will handle the specific browser binaries.
RUN apt-get update && apt-get install -y \
    ca-certificates \
    curl \
    gnupg \
    libnss3 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxrandr2 \
    libgbm1 \
    libasound2 \
    libpango-1.0-0 \
    libpangocairo-1.0-0 \
    libxfixes3 \
    libx11-xcb1 \
    libxcb-dri3-0 \
    libxcursor1 \
    libxi6 \
    libxtst6 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Copy the binary from the builder
COPY --from=builder /bin/worker /usr/local/bin/worker

# Pre-install Playwright drivers and browsers
# We use the INSTALL_ONLY flag we added to main.go
ENV INSTALL_ONLY=true
RUN worker
ENV INSTALL_ONLY=false

# Default entrypoint
ENTRYPOINT ["worker"]
