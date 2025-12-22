---
title: Feature Guide
layout: default
nav_order: 4
---

# Feature Guide

This guide covers the core features of WLEDger V2, helping you get the most out of your inventory system.

## 📦 Inventory Management

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

### ⚡ Fast Search

WLEDger uses **SQLite FTS5** (Full-Text Search) to provide instant results.

* **Universal Search:** The search bar queries Names, Descriptions, Tags, Manufacturer, Serial Number, and Part Number fields simultaneously.
* **Result Ranking:** Find the parts most relevant to your search with built in search ranking.
* **Instant Filtering:** Results update instantly as you type.

### Dashboard

The dashboard provides stats about your entire inventory and WLED hardware setup(s) at a glance.

* **Multiple Controllers:** Visualize the status of the parts in your inventory (Critical, Low, OK), organized per-bin & per-controller. There's no need to physically check your bins anymore!
* **Stats:** View total part count, inventory value, and online controller status in one place.

---

## 💡 Hardware Integration (WLED)

This is what makes WLEDger special: connecting your digitally managed inventory to physical LEDs.

### Controllers

* **Multiple Controllers:** Manage multiple WLED-powered devices (e.g., "Main Workbench", "Component Cabinet", "Shelf Unit").
* **Status Monitoring:** View the online/offline status and IP address of each controller.

### The Grid Painter

Setting up LED mapping can be tedious. The **Grid Painter** makes it visual and easy.

* **Visual Mapping:** Instead of typing coordinates, you see a grid representing your LEDs. Map them using the Auto Map feature, or manually map a custom data line run for ultimate flexibility.
* **Matrix Support:** Easily map 2D grids (e.g., 8x8 panels) with automatic row/column addressing.
* **Strip Support:** Map linear strips for shelves or drawers.
* **Compound Layouts:** Have a container with different sized bins? Choose a compound layout to define sections of grids.

### Locating Parts

* **One-Click Locate:** Click the "Locate" button on any part, and the bin(s) holding that part will light up instantly.
* **Configure Locate LED:** Configure a locate timeout to turn off an LED automatically after a specific amount of time, or set it to stay on forever until you turn it off.

---

## 🤖 Inspiration (LLM Prompts)

Not sure what to build with all that stuff? WLEDger can help you create prompts using *your inventor* for use with your favorite LLM tool.

### Prompt Generator

* **Context-Aware:** The system generates a structured prompt containing a summarized table of your *actual* inventory.
* **Project Ideas:** Paste the generated prompt into an LLM (Gemini, ChatGPT, Claude, etc) to get project suggestions that you can build *right now* with the parts you have on hand.
* **Customizable:** The prompt templates are fully customizable. Edit the default prompts, or create and save your own for frequent use.

---

## 🛠 Data Tools

Your data belongs to you. WLEDger provides powerful tools to manage it.

### Bulk Import

* **CSV Import:** Import inventory from other software or spreadsheets easily. Upload a CSV file, or copy and paste raw CSV into the Import UI directly.

### Backup & Restore

* **Full System Export:** Create a single `.zip` file containing:
  * **Database Dump:** A complete JSON export of all your data.
  * **Assets:** All uploaded images and datasheets.
* **Atomic Restore:** Restore your entire system from a backup file with one click. *Note: This overwrites ALL current data.*

---

## 🔐 User Management

Secure your inventory with Role-Based Access Control (RBAC).

* **Admin:** Full access to all settings, hardware configuration, and user management.
* **Editor:** Can add/edit/delete LLM prompts, parts, and manage stock, but cannot change system settings or hardware maps.
* **Viewer:** Read-only access to inventory and search. Can "Locate" parts.
* **Guest:** (Optional) Restrict access for unauthenticated users, or let unauthenticated users browse your inventory and locate parts in "read-only" mode.
