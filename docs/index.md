---
title: Welcome to WLEDger
layout: default
nav_order: 1
---

# Welcome to WLEDger V2

WLEDger is a high-performance inventory management system for electronics hobbyists, makers, and workshops. It bridges the gap between your digital inventory and physical storage by integrating with [WLED](https://kno.wled.ge/) controllers to physically light up the exact location of your parts.

Built with **Go**, **HTMX**, and **SQLite**, WLEDger V2 is designed to be fast, lightweight, and easy to deploy on anything from a Raspberry Pi to a dedicated server.

### Key Features

*   **🔦 Physical Location Tracking:** Instantly locate parts by lighting up specific LEDs on your storage bins, shelves, or drawers.
*   **🔍 Instant Search:** Powered by SQLite **FTS5**, search your entire inventory, tags, and descriptions in milliseconds.
*   **🧩 Visual Grid Painter:** A powerful visual tool to map your physical LED matrix to your storage bins—no manual coordinate entry required.
*   **📸 Rich Part Details:** Store images, datasheets, supplier links, and documents for every component.
*   **🤖 AI Integration:** Generate "Inspiration" prompts based on your inventory to feed into LLMs (like ChatGPT or Claude) for project ideas.
*   **📦 Backup & Restore:** Full system backup (Database + Images) to a portable ZIP file.
*   **☁️ Self-Hosted:** Your data is yours. Run it locally via Docker or a binary.

### Documentation

This documentation is divided into three main sections:

1.  **[Quick Start](./quickstart-guide.md)**
    *   Start here! A complete guide to installing WLEDger using Docker and setting up your first controller.

2.  **[Hardware Guide](./build-guide.md)**
    *   Learn about the hardware requirements: microcontrollers, LED strips, power supplies, and bin recommendations.

3.  **[Developer Guide](./developer-guide.md)**
    *   For those who want to contribute or understand the code. Explains the Go/Templ/HTMX architecture and development workflow.

---

### How It Works

1.  **Store It:** Add your parts to WLEDger (manually or via CSV import).
2.  **Map It:** Use the **Grid Painter** to tell WLEDger which LEDs correspond to which physical bins.
3.  **Find It:** Click "Locate" on any part, and watch the correct bin light up in your workshop.

### Community & Support

*   **GitHub:** [tuxedocurly/wledger](https://github.com/tuxedocurly/wledger)
*   **Discord:** [Join the TuxedoDevices Discord](https://discord.com/invite/HABg37gjrd)
