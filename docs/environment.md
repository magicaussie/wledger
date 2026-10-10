# Environment variables

WLEDger reads its configuration from environment variables. This file documents
the supported variables. Never commit real secrets — provide them via your
deployment environment or a local `.env` file that is not tracked by Git.

## Application

| Variable | Default | Purpose |
| --- | --- | --- |
| `WLEDGER_PUBLIC_URL` | *(empty)* | Externally reachable base URL, used to build absolute callback URLs (e.g. DigiKey OAuth). Must be HTTPS and match the redirect URI registered with the provider. |
| `WLEDGER_API_TOKEN` | *(empty)* | Machine-to-machine bearer token for the `/api/v1` API (Home Assistant + MCP). When empty, the API routes are not mounted. Generate one, e.g. `openssl rand -hex 32`. |
| `WLEDGER_INSECURE_COOKIES` | *(unset)* | Session-cookie hardening opt-out. See below. |

### Session cookie security (`WLEDGER_INSECURE_COOKIES`)

The session cookie is marked **`Secure` by default**. This is correct for
production, including when the app runs behind a TLS-terminating reverse proxy:
the browser-facing connection is HTTPS, so the browser stores and returns the
cookie normally.

Set `WLEDGER_INSECURE_COOKIES` to a truthy value (`1`, `true`, `yes`, `on`,
case-insensitive) **only** for deliberate local HTTP development, where a
`Secure` cookie would not be stored by the browser:

```sh
# Local HTTP development only — never in production.
WLEDGER_INSECURE_COOKIES=1
```

Any other value (or unset) keeps the cookie `Secure`.

## MCP server (`cmd/mcp-server`)

| Variable | Default | Purpose |
| --- | --- | --- |
| `WLEDGER_API_URL` | `http://localhost:8080` | Base URL of the WLEDger API the MCP server calls. |
| `WLEDGER_API_TOKEN` | *(required)* | Bearer token for `/api/v1` and for inbound MCP authentication. The server refuses to start a network transport without it. |
| `MCP_TRANSPORT` | `stdio` | `stdio`, `sse`, or `http`. |
| `MCP_HTTP_ADDR` | `127.0.0.1:9100` | Listen address for the `sse`/`http` transports. |
| `MCP_ALLOWED_ORIGINS` | *(empty)* | Comma-separated browser origins allowed to call MCP. Loopback origins are always trusted; requests without an `Origin` header are allowed. |
