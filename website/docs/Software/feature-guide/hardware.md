---
title: Hardware (WLED Controllers)
sidebar_position: 4
---

# Hardware Integration (WLED)

WLEDger's defining feature is its ability to bridge the gap between digital inventory and physical components. By connecting to a microcontroller running WLED, the system can physically illuminate the exact location of a part, eliminating the need to search through drawers and bins manually.

:::info Manage Inventory Without WLED
You can use WLEDger without *any* WLED controller or LEDs if you'd like. To do so, create a placeholder "controller" with an invalid IP (`0.0.0.0`) and configure your storage containers (bins, shelves, etc).
:::


---

## WLED Controllers

WLEDger communicates with any WLED controllers (ESP32/Dig2Go/etc) via their JSON API. Requests are sent to your controller in real-time.

### Controller Options

| Field | Purpose | Usage Tips |
| :--- | :--- | :--- |
| **Name** | A friendly name (e.g., `Main Workbench`). | Helps you identify which physical area is being controlled. |
| **IP Address** | The network address of the WLED device. | Ensure your WLED device has a static IP or a DHCP reservation to prevent loss of connectivity. |
| **Port** | Customize the WLED port (default `80`). | In most cases, you should leave this as the default port `80`. |

---

## Configure a Controller

The `controller configuration` contains tools to define your physical storage layout and map your LEDs.

### Layout Types

There are multiple types of containers you can create within a controller.

| Type | Best For | Description |
| :--- | :--- | :--- |
| **Linear** | Shelf edges, single-row drawers. | A sequential run of LEDs. Indices increment by 1. |
| **Matrix** | 2D panels or drawer grids. | Defines a grid (e.g., 8x10). |
| **Compound** | Irregular storage containers. | Allows grouping multiple linear or matrix segments into one logical container. |

### 1. Add a Controller

  1. In WLEDger, go to **Hardware**.
  2. Click **"Add Controller"**.
  3. Enter a name (e.g., "Main Workbench") and the **IP Address** of your WLED device.
  4. WLEDger will attempt to ping the device. If successful, the status will change to `online`.

### 2. Create a Container

  1. Click **'Configure'** on the newly added controller.
  2. A default container will already be created. You can add multiple containers if you have multiple storage containers wired to the same controller
  3. Modify the name of the container, if desired.
  4. Specify the WLED Segment ID for this container (the default is `0`. If you are using only 1 GPIO pin on your WLED controller, you should leave this as the default `0`. If you add a second container to the same controller that uses a different GPIO pin, that segment should be `1`, and so on.).
  5. Specify the container [layout type](#layout-types)
  6. Specify where your data starts (used for the `Auto Map` buttons)
  7. Configure the `Total LEDs`, `Rows` and `Cols`, or `Rows` and `Cols` and `Sections` depending on the container layout type selected.

### 3. Map LEDs / Bins

  1. Scroll down and you'll see a visual representation of your container and its bins.
  2. Use the green `Auto Map` buttons to map your LED data lines if they are `Linear` or `Serpentine`. For more complex data line mapping, you can manually configure the flow of LED data by clicking on each bin, 1-by-1.
  3. Click `Save Changes` to apply your changes and save the container configuration.

  ### 4. Save Changes

  1. Click `Save Changes` to apply your changes and save the container configuration.

---

## Create parts

Now that your controller is configured, [create some parts](inventory.md#create-and-edit) and assign them to the bins in your container(s)!

## Locating Parts

The end goal of hardware integration is the "Locate" feature. This can be triggered from a Part Card on the Inventory page.

### How it Works
1.  **Navigate:** Go to `Inventory`.
2.  **Locate:** Click the `Locate` (eye) icon.
3.  **Physical Feedback:** The specific LED(s) for the part `bin(s)` will light up!