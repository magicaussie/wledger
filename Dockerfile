# Stage 1: Build CSS with Tailwind
FROM node:23-alpine AS css-builder
WORKDIR /build

# Install dependencies
COPY package.json package-lock.json ./
RUN npm ci

# Copy web directory (needed for templ files scanning by tailwind)
COPY web ./web

# Generate CSS
RUN npx @tailwindcss/cli -i ./web/static/css/input.css -o ./web/static/css/output.css --minify

# Stage 2: Build Go Binary
FROM golang:1.25-alpine AS go-builder
WORKDIR /build

# Install build dependencies (CGO requires gcc/musl-dev)
RUN apk add --no-cache gcc musl-dev

# Install templ and sqlc
RUN go install github.com/a-h/templ/cmd/templ@v0.3.977
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0

# Copy modules manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Generate sqlc and templ files
RUN sqlc generate
RUN templ generate

# Build binary
# CGO_ENABLED=1 is required for go-sqlite3
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -tags fts5 -o wledger ./cmd/server
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -tags fts5 -o mcp-server ./cmd/mcp-server

# Stage 3: Runtime
FROM alpine:latest
WORKDIR /wledger

# Install runtime dependencies (Python + Chromium needed by the amazon and spotlight
# supplier providers, which shell out to scripts/*_helper.py using Selenium;
# Node is needed by the aliexpress helper which uses Puppeteer against Chromium)
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    python3 \
    py3-pip \
    nodejs \
    npm \
    chromium \
    chromium-chromedriver

RUN pip3 install --no-cache-dir --break-system-packages amzpy selenium lxml

# Copy binary
COPY --from=go-builder /build/wledger .
COPY --from=go-builder /build/mcp-server .

# Copy supplier helper scripts
COPY --from=go-builder /build/scripts/amazon_helper.py ./scripts/amazon_helper.py
COPY --from=go-builder /build/scripts/spotlight_helper.py ./scripts/spotlight_helper.py
COPY --from=go-builder /build/scripts/aliexpress/aliexpress_helper.mjs ./scripts/aliexpress/aliexpress_helper.mjs
COPY --from=go-builder /build/scripts/aliexpress/package.json ./scripts/aliexpress/package.json

# Install AliExpress helper npm dependencies (Puppeteer uses system Chromium,
# so puppeteer-core does not download a bundled browser)
RUN cd /wledger/scripts/aliexpress && npm install --no-audit --no-fund

# Create symlink for chromedriver so Selenium can find it
RUN mkdir -p /root/.cache/selenium/chromedriver/linux64/152.0.7977.42 && \
    ln -sf /usr/bin/chromedriver /root/.cache/selenium/chromedriver/linux64/152.0.7977.42/chromedriver

# Copy static files (including the one generated in Node stage)
# First copy all static files from source (images, js)
COPY --from=go-builder /build/web/static ./web/static
# Then overwrite the CSS with the minified version from css-builder
COPY --from=css-builder /build/web/static/css/output.css ./web/static/css/output.css

# Copy locales
COPY --from=go-builder /build/locales ./locales

# Copy database schema for migrations
COPY --from=go-builder /build/sql/schema ./sql/schema

# Create necessary directories
RUN mkdir -p data app/uploads app/logs

# Expose port
EXPOSE 8080

# Define volumes for persistence
VOLUME ["/wledger/data", "/wledger/app/uploads", "/wledger/app/logs"]

# Run
CMD ["./wledger"]
