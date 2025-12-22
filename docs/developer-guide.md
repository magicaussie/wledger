---
title: Developer Guide
layout: default
nav_order: 3
---

# Developer Guide

Welcome to the WLEDger V2 Developer Guide. This document details the architecture, project structure, and development workflows.

## Tech Stack

WLEDger V2 moves away from the previous stack to a robust, type-safe, and high-performance Go architecture.

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
│   └── server/         # Main entry point (main.go) and HTTP handlers.
├── internal/           # Private application code.
│   ├── audit/          # Record actions into the audit_logs table.
│   ├── auth/           # RBAC and Session logic.
│   ├── backup/         # Backup & restore logic.
│   ├── config/         # Contains constants used throughout the app.
│   ├── db/             # Generated SQLC database code (DO NOT EDIT).
│   ├── images/         # Image handling and resizing logic.
│   ├── importer/       # CSV Import logic.
│   ├── inspiration/    # LLM Prompt storage logic.
│   ├── logger/         # Global app logger using lumberjack.
│   ├── middleware/     # Handles context passing, auth, and enforces checks (e.g. reset password).
│   ├── parts/          # Parts service for part CRUD operations.
│   ├── tags/           # Tags service for tag CRUD operations.
│   ├── utils/          # Small utilities.
│   └── wled/           # WLED API client and integration.
├── sql/
│   ├── schema/         # Database schema migrations.
│   └── queries/        # SQL queries used by SQLC.
├── web/
│   ├── components/     # Reusable Templ components (.templ).
│   ├── icons/          # Reusable SVG icons (.templ). All icons take a `size int` parameter.
│   ├── layouts/        # Full page layouts (.templ) meant for "base" templates.
│   ├── pages/          # Full page layouts (.templ).
│   └── static/         # Static assets (images, generated CSS, Alpine.js).
└── Makefile            # Build and development commands.
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

1. Modify or add SQL files in `sql/schema/` (for structure) or `sql/queries/` (for operations).
2. Run the generator:

    ```bash
    make generate
    ```

    This updates the Go code in `internal/db/`.

### UI Changes

1. Edit `.templ` files in `web/`.
2. If running `make dev`, changes are picked up automatically.
3. If adding custom CSS classes, edit `web/static/css/input.css`.

## Architecture Highlights

### Database & Search

WLEDger uses SQLite with the **FTS5** extension for lightning-fast search.

* **Triggers:** Database triggers (`sql/schema/002_fts_triggers.sql`) automatically sync changes in the `parts` table to the `parts_fts` virtual table.
* **Tags:** Tags are denormalized into a searchable string column for performance.

### Authentication (RBAC)

Role-Based Access Control is implemented in `internal/auth/`.

* **Roles:** `admin`, `editor`, `viewer`, `guest`.
* **Middleware:** `RequireRole("admin")` middleware protects sensitive routes.
* **Sessions:** Stored in the SQLite database via `scs`.

### WLED Integration

The `internal/wled` package handles communication with controllers.

* **Grid Painter:** This complex UI component (`web/components/grid_painter.templ`) uses **Alpine.js** to handle the interactive grid mapping on the client side, then sends the JSON map to the server.

### Backup System

The backup system (`internal/backup`) creates a ZIP archive containing:

1. `human_readable_parts.json`: A complete human-readable dump of user parts.
2. `restore_data.json`: A complete dump of the SQLite DB tables.
3. `uploads/`: The directory containing all user-uploaded images and documents.
Restoring is complete, and destructive: the system verifies the ZIP, clears the current DB, imports the data, and swaps the image directories.

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
