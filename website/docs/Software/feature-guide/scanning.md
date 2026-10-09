---
title: Scanning (QR & Barcodes)
sidebar_position: 7
---

# Scanning (QR & Barcodes)

WLEDger can identify every level of your physical storage with a scan. Print a
label, stick it on the shelf, and a barcode/QR scanner (or your phone's camera)
takes you straight to the right place — no typing, no searching.

## Storage Hierarchy

WLEDger models your storage as a four-level hierarchy. Each level has its own
scan code, so a scan always resolves to the correct place.

| Level | What it is | Scan code | Scanning it opens |
| :--- | :--- | :--- | :--- |
| **Cabinet** | A WLED controller | `wledger:cabinet:<id>` | The cabinet's LED layout |
| **Drawer** | A container on a controller | `wledger:drawer:<id>` | The drawer view (see below) |
| **Bin** | A bin inside a drawer | `wledger:bin:<id>` | The inventory filtered to that bin |
| **Item** | A part | `wledger:part:<code>` or a plain barcode | The part's page |

:::tip
The terms **Cabinet**, **Drawer**, and **Bin** are the user-facing names. In the
database these map to controllers, containers, and bins respectively.
:::

## Printing Labels

WLEDger generates print-ready QR label sheets:

- **Bin, Drawer & Cabinet labels** — go to **Hardware → Labels**. This sheet
  includes a QR code for each cabinet, each drawer, and each bin.
- **Product labels** — go to **Inventory → Labels**. This sheet includes a QR
  code (and the barcode, if set) for every part.

Open the sheet and click **Print Labels**. Cut out the labels and attach them to
the matching physical cabinet, drawer, bin, or part.

## Scanning

WLEDger accepts scans from two sources:

- **USB barcode/QR scanner** — most "keyboard-wedge" scanners type the code and
  press Enter. Just scan; WLEDger routes it automatically.
- **Webcam / phone camera** — use the built-in scanner modal to scan with a
  camera.

When a code is scanned, WLEDger resolves it and navigates to the right page. If
you're in the middle of an action that needs a bin (for example, assigning
stock), scanning a bin's QR selects that bin instead of navigating away.

## The Drawer View

Scanning a drawer QR opens the **drawer view** (`/drawers/<id>`). This page:

- **Lights up the drawer's LEDs on load**, so you can physically find it.
- Lists every **bin** in the drawer and the **parts** currently stored in each.
- Offers a **Locate** button to re-highlight the drawer, and an **Edit Layout**
  shortcut (for editors/admins) to open the cabinet's grid painter.

## Locating

Scanning is the fastest way to *find* something, but you can also locate from
the UI:

- **Locate a part** — click the 👁️ (eye) icon on a part card to light up every
  bin that part is stored in.
- **Locate a drawer** — open the drawer view and click **Locate** to light up
  the whole drawer.
