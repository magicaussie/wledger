# Run all dev watchers
dev:
	@make -j3 dev-templ dev-tailwind dev-server

# Watch Templ files
dev-templ:
	templ generate --watch --proxy="http://localhost:8080"

# Watch CSS (Tailwind v4)
dev-tailwind:
	./node_modules/.bin/tailwindcss -i ./web/static/css/input.css -o ./web/static/css/output.css --content "./web/**/*.templ" --watch

# Watch Go Server
dev-server:
	air -c air.toml

# Build for production
build:
	templ generate
	./node_modules/.bin/tailwindcss -i ./web/static/css/input.css -o ./web/static/css/output.css --content "./web/**/*.templ" --minify
	go build -tags fts5 -o bin/wledger cmd/server/main.go
