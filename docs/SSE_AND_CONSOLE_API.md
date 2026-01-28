# SSE Events & Console History API

Two endpoints for real-time server event streaming and console log retrieval.

---

## GET /api/events

Streams real-time Server-Sent Events for one or more servers over a single connection. Events include console output, state changes, and resource statistics.

### Authentication

Bearer token required.

```
Authorization: Bearer <token>
```

### Query Parameters

| Parameter | Type   | Required | Description                      |
|-----------|--------|----------|----------------------------------|
| `servers` | string | Yes      | Comma-separated server UUIDs     |

### Example Request

```bash
curl -H "Authorization: Bearer <token>" -N \
  "http://localhost:8080/api/events?servers=abc123-def456,xyz789-ghi012"
```

### Response Headers

```
Content-Type: text/event-stream
Cache-Control: no-cache
Connection: keep-alive
X-Accel-Buffering: no
```

### Event Types

#### `status`

Sent when a server's state changes. Also sent once per server on initial connection.

```
event: status
data: {"server_id":"abc123-def456","state":"running"}

```

| Field       | Type   | Description                                                        |
|-------------|--------|--------------------------------------------------------------------|
| `server_id` | string | UUID of the server                                                |
| `state`     | string | One of `offline`, `starting`, `running`, `stopping`, or `deleted` |

The special state `deleted` is sent if a server is removed while the SSE connection is open. No further events will be sent for that server.

#### `stats`

Sent when resource usage is updated. Also sent once per server on initial connection.

```
event: stats
data: {"server_id":"abc123-def456","memory_bytes":1073741824,"memory_limit_bytes":2147483648,"cpu_absolute":45.2,"network":{"rx_bytes":1024,"tx_bytes":2048},"uptime":360000,"state":"running","disk_bytes":5368709120}

```

| Field                | Type   | Description                          |
|----------------------|--------|--------------------------------------|
| `server_id`          | string | UUID of the server                   |
| `memory_bytes`       | uint64 | Current memory usage in bytes        |
| `memory_limit_bytes` | uint64 | Memory limit in bytes                |
| `cpu_absolute`       | float64| CPU usage relative to entire system  |
| `network.rx_bytes`   | uint64 | Network bytes received               |
| `network.tx_bytes`   | uint64 | Network bytes transmitted            |
| `uptime`             | int64  | Container uptime in milliseconds     |
| `state`              | string | Current server state                 |
| `disk_bytes`         | int64  | Disk space used in bytes             |

#### `console output`

Sent for each line of console output. Includes both raw container output and daemon messages (e.g. "Pulling Docker container image...").

```
event: console output
data: {"server_id":"abc123-def456","line":"[21:30:15 INFO]: Player joined the game"}

```

| Field       | Type   | Description          |
|-------------|--------|----------------------|
| `server_id` | string | UUID of the server  |
| `line`      | string | Console output line  |

#### Keepalive

A comment line sent every 15 seconds to prevent proxy timeouts. This is not a named event and will be ignored by standard SSE clients.

```
: keepalive

```

### Behavior

- On connection, the server immediately sends one `status` and one `stats` event per requested server.
- The connection stays open indefinitely until the client disconnects.
- If a server is deleted while the connection is open, a `status` event with `"state": "deleted"` is sent for that server. The connection remains open for the remaining servers.
- Every payload includes `server_id` so the client can demultiplex events from multiple servers.

### Errors

| Code | Condition                                |
|------|------------------------------------------|
| 400  | Missing or empty `servers` query param   |
| 404  | One or more server UUIDs not found       |

```json
{"error": "Server abc123 was not found."}
```

### Full Wire Example

```
event: status
data: {"server_id":"abc123-def456","state":"running"}

event: stats
data: {"server_id":"abc123-def456","memory_bytes":1073741824,"memory_limit_bytes":2147483648,"cpu_absolute":45.2,"network":{"rx_bytes":1024,"tx_bytes":2048},"uptime":360000,"state":"running","disk_bytes":5368709120}

event: status
data: {"server_id":"xyz789-ghi012","state":"offline"}

event: stats
data: {"server_id":"xyz789-ghi012","memory_bytes":0,"memory_limit_bytes":2147483648,"cpu_absolute":0,"network":{"rx_bytes":0,"tx_bytes":0},"uptime":0,"state":"offline","disk_bytes":1073741824}

event: console output
data: {"server_id":"abc123-def456","line":"[21:30:15 INFO]: Player joined the game"}

: keepalive

event: status
data: {"server_id":"abc123-def456","state":"stopping"}

```

---

## GET /api/servers/:server/console

Returns recent console log history and current state for a single server.

### Authentication

Bearer token required. The server must exist (enforced by middleware).

```
Authorization: Bearer <token>
```

### Path Parameters

| Parameter | Type   | Description  |
|-----------|--------|--------------|
| `server`  | string | Server UUID  |

### Query Parameters

| Parameter | Type | Default | Description                  |
|-----------|------|---------|------------------------------|
| `size`    | int  | 100     | Number of lines (clamped 1-100) |

### Example Request

```bash
curl -H "Authorization: Bearer <token>" \
  "http://localhost:8080/api/servers/abc123-def456/console?size=50"
```

### Response

**200 OK**

```json
{
    "state": "running",
    "line_count": 3,
    "lines": [
        "[21:30:10 INFO]: Server started",
        "[21:30:12 INFO]: Loading world...",
        "[21:30:15 INFO]: Player joined the game"
    ]
}
```

| Field        | Type     | Description                                          |
|--------------|----------|------------------------------------------------------|
| `state`      | string   | Current server state (`offline`, `starting`, `running`, `stopping`) |
| `line_count` | int      | Number of lines returned                             |
| `lines`      | string[] | Console log lines (most recent last)                 |
