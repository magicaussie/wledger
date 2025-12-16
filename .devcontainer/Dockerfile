# The official Microsoft Go image (Debian-based)
FROM mcr.microsoft.com/devcontainers/go

# Install "Air" for live reloading Go apps
RUN go install github.com/air-verse/air@latest

# Install Templ for template generation
RUN go install github.com/a-h/templ/cmd/templ@latest

# Install sqlc for database code generation
RUN go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

# recursive chown to give the 'vscode' user permission to write to /go
RUN chown -R vscode:vscode /go

# (Optional) Install other OS-level tools if desired, e.g.
# RUN apt-get update && apt-get install -y vim
