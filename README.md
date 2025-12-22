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

## ✨ Core Features

### 💡 "Pick-to-Light" System
Connect your WLED-powered LED strips or matrices to your storage.
- **Visual Locate:** Click "Locate" on a part, and its bin glows instantly.
- **Stock Status Colors:** Configure different colors for "In Stock", "Low Stock", and "Critical".
- **Grid & Strip Support:** Map LEDs to physical bins using linear strips or 2D grids.

### 📦 Powerful Inventory Management
- **Fast Search:** Instant results using SQLite FTS5 (Full-Text Search).
- **Barcode Scanning:** Built-in support for scanning parts via your phone's camera.
- **Rich Data:** Store datasheets (PDFs), images, supplier links, and cost data.
- **Tagging:** Organize parts with flexible tagging.

### LLM Inspiration
Don't let your parts gather dust.

Create and use prompt templates that export your entire inventory list, or a subset of your inventory filtered by tag. Copy the prompt, and paste it into your favorite LLM.

To get you started, I've included some great prompts out.

- **Project Ideas:** Get high quality project ideas you can build with your *current* inventory.
- **Integration Guides:** Get quick guidance on how to use your parts in a real circuit.
- **Learning Paths:** Generate curriculum based on the hardware you own to learn new skills.

### 🛡️ Enterprise-Grade Usability
- **Role-Based Access Control:** Admin, Editor, viewer, and (optional) Guest roles.
- **Audit Logging:** Track every change, stock adjustment, and deletion.
- **Responsive Design:** Designed with desktop and mobile in mind.

---

## 🛠️ The Tech Stack (V2)

WLEDger V2 has been completely rewritten for performance, type safety, and simplicity. It uses the **GTH** (Go, Templ, HTMX) stack + Alpine.js.

*   **Backend:** [Go](https://go.dev/) (1.25+) - Fast, compiled, and robust.
*   **Database:** [SQLite](https://www.sqlite.org/) + `sqlc` - Zero-config, reliable storage with type-safe queries.
*   **Frontend:** [Templ](https://templ.guide/) + [HTMX](https://htmx.org/) - Server-side rendering with SPA-like interactivity.
*   **Interactivity:** [Alpine.js](https://alpinejs.dev/) - Lightweight JavaScript for UI state.
*   **Styling:** [Tailwind CSS v4](https://tailwindcss.com/) + [DaisyUI v5](https://daisyui.com/).
*   **Hardware:** [WLED](https://kno.wled.ge/) - The gold standard for controlling LEDs.

---

## Why I Made This

I have hundreds of electronic components in my lab. I couldn't remember what parts I had, or where they were stored, leading to over-buying and under-utilizing. I wanted a system that solved all angles.

Existing tools were either enterprise focused (and not open source) or lacking in features/WLED integration. WLEDger bridges that gap, providing a purpose-built, open source solution for the physical reality of a maker's workshop.

---

## 🚀 Getting Started

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
- Go 1.25+
- Node.js (for Tailwind CSS generation)
- Make

```bash
# 1. Clone the repo
git clone https://github.com/tuxedocurly/wledger.git
cd wledger

# 2. Generate Code
make generate

# 3. Build the binary
make build

# 4. Run
./bin/wledger
```

### Development

We use `air` for live reloading and `make` for task orchestration.

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
|:---:|:---:|
| ![Part Details](https://placehold.co/600x400?text=Part+Details) | ![Mobile Scanner](https://placehold.co/600x400?text=Scanner+UI) |

| Grid View | Dark Mode |
|:---:|:---:|
| ![Grid View](https://placehold.co/600x400?text=Hardware+Grid) | ![Dark Mode](https://placehold.co/600x400?text=Dark+Mode) |

---

## 🤝 Contributing

Contributions are welcome!
1.  Fork the repository.
2.  Create a feature branch (`git checkout -b feature/amazing-feature`).
3.  Commit your changes.
4.  Open a Pull Request.

## 📄 License

This project is licensed under the MIT License - see the `LICENSE` file for details.

---

*Built with ❤️ for the maker community by me, TuxedoMakes.*