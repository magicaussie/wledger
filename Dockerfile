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

# Install templ
RUN go install github.com/a-h/templ/cmd/templ@latest

# Copy modules manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Generate templ files
RUN templ generate

# Build binary
# CGO_ENABLED=1 is required for go-sqlite3
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -tags fts5 -o wledger ./cmd/server

# Stage 3: Runtime
FROM alpine:latest
WORKDIR /wledger

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy binary
COPY --from=go-builder /build/wledger .

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
