# Local WLED API simulator

`internal/wledsim` is a reusable, in-memory `http.Handler` for tests and local
WLEDger development. It makes **no outbound network calls**, writes no files,
and never contacts WLED hardware. No application configuration or production
behavior is changed by adding this package.

## Optional development command

From the repository root:

```sh
go run ./cmd/wled-simulator -count 200
```

Defaults: `127.0.0.1:8181`, 200 RGB LEDs, one electrical output on GPIO 2,
MAC `02:00:00:00:00:01`. Stop with Ctrl-C; state is not persisted.

For another instance:

```sh
go run ./cmd/wled-simulator -bind 127.0.0.1:8182 -count 120 -mac 02:00:00:00:00:02
```

Use a **different stable MAC for each simulated controller**, and save the same
MAC in WLEDger. The CLI's default is stable but not globally unique across
instances. `-mac` accepts a nonzero six-byte unicast address; locally administered
addresses beginning with `02` are recommended. Package callers omitting the MAC
receive a randomly generated locally administered unicast MAC, stable only for
that `Simulator` instance.

The bind flag requires a literal loopback IP, such as `127.0.0.1:8181` or
`[::1]:8181`. Wildcards, non-loopback addresses, and hostnames (including
`localhost`) are rejected before listening. There is no hostname lookup or
unauthenticated remote fault-control API. Do not expose the port through a proxy,
tunnel, or port forwarding. Loopback is not authentication: other local processes
can read/write simulated state. CLI HTTP read/write deadlines bound requests.

If using a containerized WLEDger instance, its `127.0.0.1` is not the host's
loopback. This command intentionally does not offer a public-interface escape
hatch; run the local app and simulator in the same network namespace instead.

For read-only mapping discovery, configure the local test controller as
`wled-esp32`, with the matching count, address/port, and MAC. The discovery
compatibility test exercises the real application's mapping validation. This
does not authorize changing production controllers, enabling hardware output,
or bypassing any existing WLEDger output gate.

## Reuse in tests

```go
sim, err := wledsim.New(wledsim.Config{
    Count: 20,
    MAC:   "02:00:00:00:00:03",
    Outputs: []wledsim.Output{
        {Start: 0, Length: 8, Pins: []int{2, -1, -1}},
        {Start: 8, Length: 12, Pins: []int{4, -1, -1}, Reverse: true, Order: 1},
    },
})
if err != nil {
    t.Fatal(err)
}
server := httptest.NewServer(sim) // actual loopback HTTP, ephemeral port
 defer server.Close()
// Point the client under test at server.URL, never at a physical device.
```

Import the package as
`github.com/tuxedocurly/wledger/internal/wledsim` from inside this module.
Construction copies the caller's output/pin slices. Count is bounded to
1–1,000,000. Output ranges must fit within the count and pins must be supplied;
type defaults to WLED RGB type 22. Output starts, lengths, reversal, pins, order,
type, and skip are returned explicitly by `/json/cfg`. The simulator permits
noncontiguous output layouts and duplicate pins for negative mapping tests;
WLEDger's mapping verifier rejects these. For successful discovery, outputs
must be contiguous in list order, cover the count, use distinct active GPIOs,
and have no skipped LEDs.

## Supported HTTP subset

Based on `internal/wled/testdata/wled-0.15-{info,cfg,state}.json` and the current
WLED client's payloads:

| Endpoint | Behavior |
| --- | --- |
| `GET /json/info` | WLED 0.15.0 / ESP32 identity, MAC, RGB count, maxseg 32 |
| `GET /json/cfg` | `hw.led` with explicit `ins`, total, matrix/map/ledmap disabled |
| `GET /json/state` | Master on/brightness and segment geometry, on, brightness, solid fx, colors |
| `POST /json/state` | Atomic partial update; JSON `{"success":true}` acknowledgment |

The initial state has master on, brightness 128, and one on/solid segment
covering the count with brightness 255 and black pixels. `seg` accepts either
an object or an array. Supported updates:

- Master and segment `on`, and `bri` in 0–255.
- Segment `id` (0–31), `start`, `stop` (exclusive), and `fx: 0`.
- `col`: up to three RGB triplets or six-digit hex strings; primary color fills
  the segment. Secondary colors are retained but do not affect solid rendering.
- `i`: repeated indexed commands, either `index, color` or
  `start, stop, color`. Indices are **segment-relative**; range stops are
  exclusive. RGB triplets and six-digit hex strings are supported.

For example:

```json
{"on":true,"bri":128,"tt":0,"seg":[{"id":0,"on":true,"i":[2,5,[255,0,0]]}]}
```

This sets logical pixels 2–4 to red in the default segment. `tt` and unknown
fields are ignored; transitions are instantaneous. New segments require explicit
start and stop. Non-solid effects, overlapping/empty/out-of-bounds segments,
invalid colors/brightness/ranges, malformed JSON, and oversized bodies are
rejected without changing state. This is not full WLED emulation: segment
removal, implicit sequential pixel colors, RGBW, palettes, presets, animation,
segment grouping/reversal/mirroring changes, gamma, electrical current limiting,
UDP/realtime streaming, and configuration writes are not implemented. Tests
should not rely on ignored fields taking effect.

`State()`, `Pixels()`, `RenderedPixels()`, and `Commands()` are mutex-protected
snapshots with independent backing slices:

- `Pixels()` returns raw RGB values in controller-wide logical order. Output
  reversal is reported as metadata, not applied again to logical indices.
- `RenderedPixels()` applies master/segment on and brightness using
  `floor(channel * masterBri * segmentBri / 65025)`. Turning off or changing
  brightness preserves raw colors. This is a deterministic test approximation,
  not WLED's exact optical output.
- `Commands()` records every request, including GETs, failures, and canceled
  requests, with arrival sequence, method, path, and body (up to 1 MiB).
  Arrival order need not match concurrent completion order. The log grows for
  the lifetime of the instance; create a new simulator to reset it.

## Fault injection (Go methods only)

```go
err := sim.SetFault(wledsim.Fault{Offline: true}) // HTTP 503, not TCP failure
err = sim.SetFault(wledsim.Fault{Status: 500})   // HTTP errors 400–599
err = sim.SetFault(wledsim.Fault{Delay: 200 * time.Millisecond})
err = sim.SetFault(wledsim.Fault{})              // reset
```

Check returned errors in real tests. Controls are synchronized and sampled per
request. Delay occurs before fault response or mutation and observes request
cancellation. A client deadline before that delay elapses prevents the write;
like real HTTP, cancellation after a write has already been applied does not
roll it back. Offline takes precedence over an injected status. Faults affect
all endpoints. No remote endpoint changes faults, topology, or identity.

## Verification

```sh
go test -race ./internal/wledsim ./cmd/wled-simulator
go vet ./internal/wledsim ./cmd/wled-simulator
```

Tests use ephemeral loopback `httptest` servers, never external controllers.
Coverage includes real application output discovery, identity and output
formats, solid colors, segment-relative pixel ranges, on/off and brightness,
atomic validation failures, HTTP errors/offline/timeouts, snapshot ownership,
concurrent requests/log access, and rejection of unsafe CLI binds. Tests do not
start the long-running CLI server or change production databases.
