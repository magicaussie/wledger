# Run all dev watchers
dev:
	@make -j3 dev-templ dev-tailwind dev-server

# 1. Watch Templ files
dev-templ:
	templ generate --watch --proxy="http://localhost:8080"

# 2. Watch CSS (Tailwind v4)
dev-tailwind:
	./node_modules/.bin/tailwindcss -i ./web/static/css/input.css -o ./web/static/css/output.css --watch

# 3. Watch Go Server (Explicit config)
dev-server:
	air -c air.toml

# Build for production
build:
	templ generate
	./node_modules/.bin/tailwindcss -i ./web/static/css/input.css -o ./web/static/css/output.css --minify
	go build -o bin/wledger cmd/server/main.go
