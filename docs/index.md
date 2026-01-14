---
title: Welcome to WLEDger
slug: /
sidebar_position: 1
---

# Welcome to WLEDger V2

WLEDger is a high-performance inventory management system for electronics hobbyists, makers, and workshops. It bridges the gap between your digital inventory and physical storage by integrating with [WLED](https://kno.wled.ge/) controllers to physically light up the exact location of your parts.

Built with **Go**, **HTMX**, and **SQLite**, WLEDger V2 is designed to be fast, lightweight, and easy to deploy on anything from a Raspberry Pi to a dedicated server.

## Key Features

* **Physical Location Tracking:** Instantly locate parts by lighting up specific LEDs on your storage bins, shelves, or drawers.
* **Instant Search:** Powered by SQLite **FTS5**, search your entire inventory, tags, and descriptions in milliseconds.
* **Visual Grid Painter:** A powerful visual tool to map your physical LEDs to your storage bins using Matrix, Strip, or Compound layouts.
* **Custom Walls / Dashboards:** Organize your storage containers and controllers into logical "Walls" for a clean, high-level overview of your entire storage setup.
* **Rich Part Details:** Store images, datasheets, supplier links, and documents for every component.
* **LLM Prompt Integration:** Generate inspiration for projects to build based on your inventory, or create your own prompts to accomplish common tasks. Copy the prompt and paste it into into your favorite LLM (Gemini, ChatGPT, Claude, etc), and your parts are appended to the prompt automatically.
* **Backup & Restore:** Full system backup to a portable ZIP file.
* **Self-Hosted:** Hosting is *easy*. Run it locally via Docker or build it from source into a single binary.

### Documentation

This documentation is divided into three main sections:

1. **[Quick Start](./Software/quickstart-guide.md)**
    * Start here! A complete guide to installing WLEDger using Docker and setting up your first WLED controller.

2. **[Build Guide](./Hardware/build-guide.md)**
    * Learn about the hardware requirements: microcontrollers, LED strips, power supplies, and storage bin recommendations.

3. **[Developer Guide](./Software/developer-guide.md)**
    * For those who want to contribute or understand the code. Explains the Go/Templ/HTMX architecture and development workflow.

4. **[Feature Guide](./Software/feature-guide.md)**
    * Learn about all the features WLEDger offers to make the most out of your setup.

---

### How It Works

1. **Store It:** Add your parts to WLEDger (manually or via CSV import).
2. **Map It:** Use the **Hardware** tab to tell WLEDger which LEDs correspond to which physical bins.
3. **Find It:** Click "Locate" on any part, and watch the correct bin light up in your workshop.

### Community & Support

* **GitHub:** [tuxedocurly/wledger](https://github.com/tuxedocurly/wledger)
* **Discord:** [Join the TuxedoDevices Discord](https://discord.com/invite/HABg37gjrd)
