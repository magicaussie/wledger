---
title: Quick Start
layout: default
nav_order: 2
---

# Quick Start Guide

Get WLEDger V2 up and running in minutes.

## Docker (Recommended)

The easiest way to run WLEDger is with Docker Compose. This ensures you have all dependencies without cluttering your system.

### 1. Create a Project Directory

Create a folder for WLEDger on your server or computer:

```bash
mkdir wledger && cd wledger && mkdir data uploads logs
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

## Hardware Setup

To get the full experience, you'll need to connect WLEDger to a WLED controller.

**Prerequisites:**

* A microcontroller running [WLED](https://kno.wled.ge/) (e.g. Sparkle Motion, Dig2Go, ESP32, Wemos D1 Mini).
* Addressable LEDs (WS2812B/NeoPixel) connected to the controller.
* The IP address of your WLED controller.

### 1. Add Controller in WLEDger

1. In WLEDger, go to **Settings > Hardware**.
2. Click **"Add Controller"**.
3. Enter a name (e.g., "Main Workbench") and the **IP Address** of your WLED device.
4. WLEDger will attempt to ping the device. If successful, it will be added.

### 2. Configure the LED Grid

1. Click **'Configure'** on the newly added controller to view its details.
2. You should now be in the hardware configuration screen.
3. This visual tool allows you to map your physical containers and LEDs to virtual "Bins" using different layouts:
    * **Matrix:** Use this if you have a square grid (e.g., 8x8).
    * **Strip:** Use this for linear shelves or single rows of bins.
    * **Compound:** Use this to define custom sections for complex bin arrangements.
4. Use the **Auto Map** feature or manually click/drag to define your bins. Each bin will be assigned a unique ID.
5. Click **"Save"**.

### 3. Organize with Walls (Optional)

By default, the dashboard will show you all of your configured controllers, containers, and bins. If you want a more organized view, you can use "walls" to create customizable dashboard sections. This is particularly useful for large or complex setups.

1. Go to the **Dashboard**.
2. Click **"Create Wall"**.
3. Give your Wall a name (e.g., "Main Storage Wall") and description.
4. Once created, click **"Edit Wall"** to add controller containers to this Wall.

> You can create as many walls as you want! You can also mix and match containers from various controllers on a single wall. Nice!

### 4. Assign Parts

> You can save time by bulk importing your parts. Navigate to "Inventory" and click the "Import" button. Download the CSV template an import it using Google Sheets, Excel, etc to quickly define all your parts. Export the file as a CSV, then upload it using the "Import" button on the Inventory page, or copy and paste the raw CSV data.

1. Go to **Inventory**.
2. Create a new part or edit an existing one.
3. In the "Stock" section, select **"Add Stock"** and select the controller + bin you would like to add stock to.
4. Save, and navigate back to your dashboard to see the stock status for that part.

### 5. Locate

Go back to the **Inventory**, find your part, and click the **Locate** button (eye icon). Your LED/bin should light up!

---

## Next Steps

* **[Hardware Guide](./build-guide.md):** Detailed advice on building your own LED storage cabinets.
* **[Developer Guide](./developer-guide.md):** Learn how the code works and how to contribute.
* **[Feature Guide](./feature-guide.md):** Learn about all of WLEDger's features, what they do, and how to use them.
