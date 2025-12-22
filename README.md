# WLEDger V2

<p align="center">
  <img src="docs/assets/wledger-logo.png" alt="WLEDger Logo" width="500">
</p>

<p align="center">
  <a href="https://github.com/tuxedocurly/wledger/issues"><img src="https://img.shields.io/badge/GitHub-Report%20Bug-black?style=for-the-badge&logo=github" alt="Report Bug" /></a>&nbsp;&nbsp;&nbsp;
  <a href="https://discord.gg/HABg37gjrd"><img src="https://img.shields.io/badge/Discord-Get%20Support-5865F2?style=for-the-badge&logo=discord&logoColor=white" alt="Join Discord" /></a>&nbsp;&nbsp;&nbsp;
  <a href="https://ko-fi.com/tuxedomakes"><img src="https://img.shields.io/badge/Ko--Fi-Support%20Me-FF5E5B?style=for-the-badge&logo=ko-fi&logoColor=white" alt="Support me on Ko-Fi" /></a>
</p>

> **The Ultimate Inventory Management System for Makers.**
> *Organize your electronic components and find them instantly with WLED.*

## What is WLEDger?

WLEDger (WLED + Ledger) is a modern, high-performance inventory system designed specifically for electronics hobbyists, makers, and labs. It solves the problem of "I know I have this part, but where is it?" by integrating with **WLED** controllers.

When you search for a component, WLEDger doesn't just tell you "Bin A1" — **it lights up the specific bin on your storage rack.**

## Core Features

### 💡 Visual LED Locating System

Connect your WLED-powered LED strips or matrices to your storage.

- **Visual Locate:** Click "Locate" on a part, and its bin glows instantly.
- **Stock Status Colors:** Configure different colors for "Locate", "In Stock", "Low Stock", and "Critical".
- **Grid & Strip Support:** Map LEDs to physical bins using linear strips or 2D grids.

### 📦 Powerful Inventory Management

- **Fast Search:** Instant results using SQLite FTS5 (Full-Text Search).
- **Barcode Scanning:** Built-in support for scanning parts via your phone's camera.
- **Rich Data:** Store datasheets (PDFs), images, supplier links, and cost data.
- **Tagging:** Organize parts with flexible tagging.

### 🧠 LLM Inspiration

Don't let your parts gather dust.

Use LLM prompt templates to generate queries about your inventory. Copy the prompt, and paste it into your favorite LLM.

WLEDger comes with some great default prompts to get you started.

- **Project Ideas:** High quality project ideas to inpsire you to build with your *current* inventory.
- **Integration Guides:** Quick guidance on how to use your parts in a real circuit.
- **Learning Paths:** Curriculum based on the hardware you own to learn new skills.

### 🛡️ Enterprise-Grade Usability

- **Role-Based Access Control:** Admin, Editor, viewer, and (optional) Guest roles.
- **Audit Logging:** Track every change, stock adjustment, and deletion.
- **Responsive Design:** Designed with desktop and mobile in mind.

### 📂 Bulk Import, Backup, and Restore

Flexibility in how you manage your parts, and the data you create in WLEDger.

- **Bulk Import:** Import all your parts at once! No UI clickin' required.
- **Backup:** Easily backup all your data. Exports include all your images, docs, and part info in a human-readable format.
- **Restore:** Restore your database from a backup in a single click.

---

## 🛠️ The Tech Stack (V2)

WLEDger V2 has been written for performance, type safety, and extensibility.

- **Backend:** [Go](https://go.dev/) (1.25+) - Fast, compiled, and robust.
- **Database:** [SQLite](https://www.sqlite.org/) + `sqlc` - Zero-config, reliable storage with type-safe queries.
- **Frontend:** [Templ](https://templ.guide/) + [HTMX](https://htmx.org/) - Server-side rendering with SPA-like interactivity.
- **Interactivity:** [Alpine.js](https://alpinejs.dev/) - Lightweight JavaScript for UI state.
- **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI v5](https://daisyui.com/).
- **Hardware:** [WLED](https://kno.wled.ge/) - The gold standard for controlling LEDs.

---

## Why I Made This

I have hundreds of electronic components in my lab. I couldn't remember what parts I had, or where they were stored, leading to over-buying and under-utilizing. I wanted a system that solved all angles.

Existing tools were expensive, closed source or lacking in features/WLED integration. WLEDger bridges these gaps, providing a purpose-built, open source solution for the physical reality of a maker's workshop.

---

## Getting Started

### Option 1: Docker (Recommended)

The easiest way to run WLEDger is via Docker.

```bash
docker run -d \
  -p 8080:8080 \
  -v ./data:/app/data \
  -v ./uploads:/app/app/uploads \
  --name wledger \
  ghcr.io/tuxedocurly/wledger:latest
```

Visit `http://localhost:8080` to see your instance.

### Option 2: Build from Source

**Prerequisites:**

- **Go:** Version 1.25+
- **Node.js:** Version 20+ (for running `npm`)
- **Make:** A `make` compatible command line tool.

These tools are required for code generation, dependency management, and running the application.

```bash
# 1. Clone the repository
git clone https://github.com/tuxedocurly/wledger.git
cd wledger

# 2. Install dependencies
# This will install the required Go tools and npm packages.
make install_dependencies

# 3. Build the binary
make build

# 4. Run the application
./bin/wledger
```

### Development

I use `air` for live reloading and `make` for task orchestration.

```bash
make dev
```

This will start:

- Go server (with Air)
- Templ generation watcher
- Tailwind CSS watcher

---

## 📸 Screenshots

| Part Details | Mobile Scanner |
| :---: | :---: |
| ![Part Details](https://placehold.co/600x400?text=Part+Details) | ![Mobile Scanner](https://placehold.co/600x400?text=Scanner+UI) |

| Grid View | Dark Mode |
| :---: | :---: |
| ![Grid View](https://placehold.co/600x400?text=Hardware+Grid) | ![Dark Mode](https://placehold.co/600x400?text=Dark+Mode) |

---

## 🤝 Contributing

Contributions are welcome!

1. Fork the repository.
2. Create a feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes.
4. Open a Pull Request.

## 📄 License

This project is released under the MIT License - see the `LICENSE` file for details.

---

*Built with ❤️ for the maker community.*
