---
title: Developer Guide
sidebar_position: 2
---

# Developer Guide

Welcome to the WLEDger Developer Guide. This document details the architecture, project structure, and development workflow.

:::warning Under Construction
This doc is a work in progress. While the development workflow is quite simple, some things may be out of date.
:::

## Tech Stack

* **Backend:** [Go 1.25+](https://go.dev/)
* **Web Framework:** [Chi v5](https://github.com/go-chi/chi) (Router)
* **Database:** [SQLite](https://www.sqlite.org/) with [FTS5](https://www.sqlite.org/fts5.html) (Full-Text Search).
* **Data Access:** [SQLC](https://sqlc.dev/) (Type-safe SQL code generation).
* **Templating:** [Templ](https://templ.guide/) (Type-safe HTML templating for Go).
* **Frontend Interactivity:** [HTMX](https://htmx.org/) (Server-driven UI updates) + [Alpine.js](https://alpinejs.dev/) (Client-side state for complex UI).
* **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI](https://daisyui.com/).

## Project Structure

The project follows the following layout convention:

``` BASH
wledger/
├── cmd/
│   └── server/         # Main entry point (main.go).
├── internal/           # Private application code.
│   ├── audit/          # Record actions into the audit_logs table.
│   ├── auth/           # RBAC and Session logic.
│   ├── backup/         # Backup & restore logic.
│   ├── config/         # Contains constants used throughout the app.
│   ├── dashboard/      # Dashboard stats and wall organization service.
│   ├── db/             # Generated SQLC database code and migration runner.
│   ├── documents/      # Document and datasheet management.
│   ├── handler/        # HTTP Handlers (decoupled from main).
│   ├── hardware/       # WLED controller and bin mapping service.
│   ├── images/         # Image handling and resizing logic.
│   ├── importer/       # CSV Import logic.
│   ├── inspiration/    # LLM Prompt storage logic.
│   ├── logger/         # Global app logger with dynamic level switching.
│   ├── middleware/     # Handles context passing, auth, and enforces checks.
│   ├── parts/          # Parts service for part CRUD operations.
│   ├── router/         # Chi router configuration and route registration.
│   ├── stock/          # Stock management across multiple locations.
│   ├── tags/           # Tags service for tag CRUD operations.
│   ├── uierror/        # UI-specific error handling and feedback.
│   ├── utils/          # Small utilities.
│   └── wled/           # WLED API client and integration.
├── sql/
│   ├── schema/         # Database schema migrations (managed by goose).
│   └── queries/        # SQL queries used by SQLC.
```

## Development Workflow

### Prerequisites

* Go 1.25+
* Node.js 23+ (for Tailwind CSS generation)
* Make

### Running Locally

To start the development environment with hot-reloading (Air for Go, Templ watcher, Tailwind watcher):

> See the Makefile for a list of all supported operations.

```bash
make install_dependencies  # Run once
make dev
```

The server will start at `http://localhost:8080`.

### Database Changes

1.  **Schema Migrations:** WLEDger uses [goose](https://github.com/pressly/goose) for migrations. Add a new SQL file to `sql/schema/` using the naming convention `00X_description.sql`.
2.  **SQL Queries:** Modify or add SQL files in `sql/queries/`.
3.  **Generate Code:** Run the generator to update the Go code in `internal/db/`:

    ```bash
    make generate
    ```

## Architecture Highlights

### Database & Search

WLEDger uses SQLite with the **FTS5** extension for lightning-fast search.

*   **Migrations:** Managed by `goose`, migrations are embedded into the binary and run automatically on startup.
*   **Triggers:** Database triggers (`sql/schema/002_fts_triggers.sql`) automatically sync changes in the `parts` table to the `parts_fts` virtual table.
*   **Tags:** Tags are denormalized into a searchable string column for performance.

### Handlers & Routing

HTTP logic is strictly separated from the application entry point.

*   **Router:** Located in `internal/router`, it configures middleware and registers routes.
*   **Handlers:** Located in `internal/handler`, these are organized by domain (auth, parts, settings, etc.) and receive dependencies via struct injection.

### Logging

WLEDger uses `slog` with a custom wrapper in `internal/logger`.

*   **Dynamic Levels:** The log level can be switched between `INFO` and `DEBUG` at runtime without restarting the application.
*   **Output:** Logs are written to both `stdout` and a rotating file in `app/logs/app.log`.

## Building for Production

To build a simplified, optimized binary:

```bash
make build
```

This produces a binary in `bin/wledger`.
**Note:** The binary requires CGO enabled because of the SQLite driver.

## Contributing

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes.
4. Push to the branch.
5. Open a Pull Request.
