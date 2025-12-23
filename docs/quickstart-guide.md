---
title: Quick Start
layout: default
nav_order: 2
---

# Quick Start Guide

Get WLEDger V2 up and running in minutes.

## 🐳 Option 1: Docker (Recommended)

The easiest way to run WLEDger is with Docker Compose. This ensures you have all dependencies without cluttering your system.

### 1. Create a Project Directory

Create a folder for WLEDger on your server or computer:

```bash
mkdir wledger
cd wledger
```

### 2. Create `docker-compose.yml`

Create a file named `docker-compose.yml` with the following content:

```yaml
services:
  wledger:
    # Format: user/repository:tag
    image: tuxedomakes/wledger:latest

    # A custom name for the container instance.
    container_name: wledger

    ports:
      # Maps ports in the format "HOST:CONTAINER"
      - "8080:8080"

    volumes:
      # Maps files in the format "HOST_PATH:CONTAINER_PATH"
      - ./data:/wledger/data
      - ./uploads:/wledger/app/uploads
      - ./logs:/wledger/app/logs

    # Restart policy
    restart: unless-stopped
```

### 3. Start the Server

Run the container in the background:

```bash
docker compose up -d
```

### 4. Setup Wizard

1. Open your browser and navigate to `http://localhost:8080`.
2. You will be greeted by the **Setup Wizard**.
3. Create your **Admin Account** (Email and Password).
4. Once completed, you will be logged in and redirected to the Dashboard.

---

## 🛠 Option 2: Run from Source

If you prefer to run the binary directly or are developing on WLEDger, you'll need **Go 1.25+** and **Node.js 23+**.

1. **Clone the Repository:**

    ```bash
    git clone https://github.com/tuxedocurly/wledger.git
    cd wledger
    ```

2. **Install Dependencies:**

    ```bash
    make install_dependencies
    ```

3. **Build and Run:**

    ```bash
    make build
    ./bin/wledger
    ```

    * *Note: This will build the production binary. For development with live reloading, use `make dev`.*

4. Access the app at `http://localhost:8080`.

---

## Hardware Setup

To get the full experience, you'll need to connect WLEDger to a WLED controller.

**Prerequisites:**

* A microcontroller running [WLED](https://kno.wled.ge/) (e.g., ESP32, Wemos D1 Mini).
* Addressable LED LEDs (WS2812B/NeoPixel) connected to the controller.
* The IP address of your WLED controller.

### 1. Add Controller in WLEDger

1. In WLEDger, go to **Settings > Hardware**.
2. Click **"Add Controller"**.
3. Enter a name (e.g., "Main Workbench") and the **IP Address** of your WLED device.
4. WLEDger will attempt to ping the device. If successful, it will be added.

### 2. Configure the LED Grid

1. Click on the newly added controller to view its details.
2. Click **"Open Grid Painter"**.
3. This visual tool allows you to map your physical LEDs to virtual "Bins".
    * **Matrix:** Use this if you have a square grid (e.g., 8x8).
    * **Strip:** Use this for linear shelves.
4. Drag or click to define your bins. Each bin will be assigned an ID (e.g., `A1-1`).
5. Click **"Save Map"**.

### 3. Assign Parts

1. Go to **Inventory > Parts**.
2. Create a new part or edit an existing one.
3. In the "Stock" section, select one of your new Bins from the dropdown.
4. Click **"Add Stock"**.

### 4. Locate

Go back to the **Parts List**, find your part, and click the **Locate** button. Your LEDs should light up!

---

## Next Steps

* **[Hardware Guide](./build-guide.md):** Detailed advice on building your own LED storage cabinets.
* **[Developer Guide](./developer-guide.md):** Learn how the code works and how to contribute.
