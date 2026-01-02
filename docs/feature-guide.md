---
title: Feature Guide
layout: default
nav_order: 4
---

# Feature Guide

This guide covers the core features of WLEDger V2, helping you get the most out of your inventory system.

## Inventory Management

At its heart, WLEDger is a robust database for your electronic components.

### Parts & Organization

* **Rich Details:** Store comprehensive data for every part, including:
  * **Name & Description:** Full-text searchable.
  * **Manufacturer & MPN:** Track specific part numbers and manufacturers.
  * **Category:** Organize parts into logical groups.
  * **Tags:** Flexible tagging system for quick filtering (e.g., `5v`, `sensor`, `my cool project`).
* **Images:** Upload photos of your parts. WLEDger automatically generates optimized thumbnails.
* **Documents & Links:**
  * **Documents:** Upload datasheets, images, or whatever you like. Access it across devices.
  * **External Links:** Save URLs to supplier pages (Mouser, DigiKey), documentation, tutorials - whatever you want.

### Stock Control

* **Multi-Location Support:** A single part type (e.g., "10k Resistor") can be stored in multiple locations (e.g., "Bin A1" and "Bin B").
* **Quantity Tracking:** specific counts for each location.
* **Min Stock & Reorder Levels:** Adjustable indicators for when your stock is getting low.

### Fast Search

WLEDger uses **SQLite FTS5** (Full-Text Search) to provide instant results.

* **Universal Search:** The search bar queries Names, Descriptions, Tags, Manufacturer, Serial Number, and Part Number fields simultaneously.
* **Result Ranking:** Find the parts most relevant to your search with built in search ranking.
* **Instant Filtering:** Results update instantly as you type from any page.

### Dashboard

The dashboard provides a high-level overview of your entire inventory and the status of your physical storage bins.

* **Walls & Organization:** Organize your storage containers and controllers into logical **Walls**. This allows you to group parts by physical location (e.g., "Workbench Wall", "Garage Shelves") or logical category.
* **Visual Status Tracking:** Instantly see the status of parts in your inventory (Critical, Low, OK) visually represented per-bin and per-container.
* **Inventory Stats:** View total unique part counts, total items in stock, total inventory value, and the online/offline status of all your WLED controllers at a glance.

---

## Hardware Integration (WLED)

This is what makes WLEDger special: connecting your digitally managed inventory to physical LEDs.

### Controllers

* **Multiple Controllers:** Manage multiple WLED-powered devices (e.g., "Main Workbench", "Component Cabinet", "Shelf Unit").
* **Status Monitoring:** View the online/offline status and IP address of each controller.

### The Grid Painter

Mapping physical LEDs to digital bins is the core of WLEDger's locating system. The **Grid Painter** provides a powerful visual interface to accomplish this without writing a single line of code or manual coordinate.

* **Layout Types:**
    * **Matrix:** Ideal for 2D grids (e.g., 8x8 or 16x16 LED panels). Automatically handles row and column addressing.
    * **Linear:** Perfect for linear arrangements like shelf edges or drawer runs.
    * **Compound:** For advanced users with complex storage. Define multiple sections within a single container, each with its own grid or strip configuration.
* **Auto Mapping:** Use the Auto Map feature to quickly generate a sequential bin layout based on your grid dimensions.
* **Manual Painting:** For irregular bin shapes or custom wiring paths, use the manual painter to click and drag over specific LEDs to define a bin.
* **Bin IDs:** Each mapped bin is assigned a unique ID (e.g., `A1-1`), which is used to associate parts with their physical locations.

### Locating Parts

* **One-Click Locate:** Click the "Locate" button on any part, and the bin(s) holding that part will light up instantly.
* **Configure Locate LED:** Configure a locate timeout to turn off an LED automatically after a specific amount of time, or set it to stay on forever until you turn it off.

---

## Inspiration (LLM Prompts)

Not sure what to build with all that stuff? WLEDger can help you create prompts using *your inventor* for use with your favorite LLM tool.

### Prompt Generator

* **Context-Aware:** The system generates a structured prompt containing a summarized table of your *actual* inventory.
* **Project Ideas:** Paste the generated prompt into an LLM (Gemini, ChatGPT, Claude, etc) to get project suggestions that you can build *right now* with the parts you have on hand.
* **Customizable:** The prompt templates are fully customizable. Edit the default prompts, or create and save your own for frequent use.

---

## Data & System Tools



Your data belongs to you, and WLEDger provides tools to manage it and troubleshoot issues.



### Bulk Import



* **CSV Import:** Import inventory from other software or spreadsheets easily. Upload a CSV file, or copy and paste raw CSV into the Import UI directly.



### Backup & Restore



* **Full System Export:** Create a single `.zip` file containing:

  * **Database Dump:** A complete JSON export of all your data.

  * **Assets:** All uploaded images and datasheets.

* **Atomic Restore:** Restore your entire system from a backup file with one click. *Note: This overwrites ALL current data.*



### Troubleshooting



* **Debug Mode:** Enable "Debug Logs" in the General Settings to increase the verbosity of application logs. This is useful for troubleshooting hardware connection issues or complex inventory operations.



---

## User Management

Secure your inventory with Role-Based Access Control (RBAC).

* **Admin:** Full access to all settings, hardware configuration, and user management.
* **Editor:** Can add/edit/delete LLM prompts, parts, and manage stock, but cannot change system settings or hardware maps.
* **Viewer:** Read-only access to inventory and search. Can "Locate" parts.
* **Guest:** (Optional) Restrict access for unauthenticated users, or let unauthenticated users browse your inventory and locate parts in "read-only" mode.
