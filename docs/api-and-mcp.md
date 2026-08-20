# WLEDger API & MCP / Voice Integration

WLEDger exposes a small machine-to-machine JSON API plus a Model Context
Protocol (MCP) server. This lets Home Assistant, voice assistants (via an LLM
agent), and other tools control the LED inventory: locate a part or bin, turn
all LEDs off, search parts, and read stock.

## Setting the token

The API and MCP server use a shared bearer token. Set it before starting the
containers:

```bash
export WLEDGER_API_TOKEN=$(openssl rand -hex 32)
```

In `docker-compose.yaml` the token is read from the environment (`.env`), so you
can also put it in a `.env` file next to the compose file:

```bash
echo "WLEDGER_API_TOKEN=$(openssl rand -hex 32)" >> .env
```

- The `wledger` service mounts `/api/v1` only when `WLEDGER_API_TOKEN` is set.
- The `mcp-server` service exposes MCP over streamable HTTP on `:9100`.

## HTTP API

Base URL: `http://<host>:8090/api/v1`
Auth: `Authorization: Bearer <WLEDGER_API_TOKEN>`

| Method | Path                     | Description                                     |
| ------ | ------------------------ | ----------------------------------------------- |
| GET    | `/health`            | Liveness (`{"status":"ok"}`).                 |
| POST   | `/global-off`        | Turn off all LEDs on all controllers.           |
| POST   | `/parts/{id}/locate` | Flash the LEDs of every bin a part is in.       |
| POST   | `/bins/{id}/locate`  | Flash a single bin's LEDs.                      |
| GET    | `/parts?q=`          | Search parts by name, part number, or barcode.  |
| GET    | `/parts/{id}`        | Part detail with stock/location assignments.    |
| GET    | `/hardware`          | List LED controllers and online status.         |

Example:

```bash
curl -H "Authorization: Bearer $WLEDGER_API_TOKEN" \
  "http://localhost:8090/api/v1/parts?q=camera"

curl -X POST -H "Authorization: Bearer $WLEDGER_API_TOKEN" \
  "http://localhost:8090/api/v1/parts/12/locate"
```

## MCP server (`cmd/mcp-server`)

The MCP server calls the API on the caller's behalf. It exposes these tools:

- `search_parts(query)` — find a part by name/part number/barcode
- `locate_part(part_id?, query?)` — flash the bins a part is stored in
- `locate_bin(bin_id)` — flash one bin
- `global_off()` — turn everything off
- `list_controllers()` — list LEDs controllers / online status

### Connect Home Assistant

1. In HA **Settings → Devices & Services → Add Integration → Model Context Protocol server**.
2. Add a **Streamable HTTP** server URL:
   `http://<wledger-host>:9100/mcp` (and the private key/token if you put it
   behind a proxy).
3. HA's conversation agent can now call the WLEDger tools, so you scripted
   things like "locate the part that starts with camera lens".

### Connect a local LLM/voice agent (open-webui, hermes)

Point the client's MCP server list at:
```text
http://<wledger-host>:9100/mcp
```
The server speaks streamable HTTP (session-based `Mcp-Session-Id`), which
open-webui / hermes / Claude Desktop support.

### stdio mode (for clients that launch the process)

```bash
MCP_TRANSPORT=stdio ./mcp-server
```
Used e.g. by Claude Desktop where the client runs the binary as a command.

## Security notes

- The API token is write-capable for `locate`/`global-off` and read-capable for
  parts/hardware. It does **not** grant full admin (no config, no part
  edit/delete, no user management).
- Keep `WLEDGER_API_TOKEN` secret; do not expose `/api/v1` or `:9100` publicly
  without a proxy + HTTPS.
- `locate` flashes LEDs; if you drive it from automation, add your own cooldown
  so you don't hammer the controller.