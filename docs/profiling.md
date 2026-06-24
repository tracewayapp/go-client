# Continuous profiling

The SDK can periodically capture Go runtime profiles and ship them to Traceway,
where they power flame graphs and allocation views alongside your traces and
metrics.

Profiling is **opt-in** and runs in a single background goroutine. When enabled,
each cycle captures:

- a **CPU profile** (`runtime/pprof` `StartCPUProfile` / `StopCPUProfile`) over a
  ~30s window, and
- a **heap profile** (`pprof.Lookup("heap")`),

then POSTs each one as a separate request to `<server>/api/profiles/ingest`.

## Enabling

```go
err := traceway.Init(
    connectionString,
    traceway.WithProfiling("checkout-api"),
)
```

`WithProfiling(serviceName)` enables profiling. `serviceName` is the application
name the profiles are grouped under in the dashboard. If you pass an empty
string, it defaults to the running binary's name (`filepath.Base(os.Args[0])`).

### Interval

```go
traceway.WithProfiling("checkout-api"),
traceway.WithProfilingInterval(2 * time.Minute),
```

`WithProfilingInterval` controls how often a cycle runs. The default is **60s**.
The CPU window is **30s**, automatically capped to `interval / 2` so a short
interval can never be overrun by an in-flight CPU capture. The first profile is
captured one interval after `Init` (not at startup).

## What gets sent

Each profile is uploaded as its own request, reusing the token and server from
the connection string:

| Part | Value |
| --- | --- |
| Method / path | `POST <scheme>://<host>/api/profiles/ingest` |
| `Authorization` | `Bearer <project_token>` |
| Query `service` | the app name from `WithProfiling` |
| Query `serverName` | the host (same value as `WithServerName`, defaults to the hostname) |
| Query `appVersion` | the value from `WithVersion` |
| `Content-Type` | `application/octet-stream` |
| Body | the raw pprof bytes |

The ingest URL is derived from the connection string's report URL by keeping its
scheme and host and replacing the path with `/api/profiles/ingest`.

pprof output is already gzip-compressed, so the body is sent **as-is** — no extra
`Content-Encoding: gzip`.

## Notes

- A heap snapshot is taken **without** forcing a `runtime.GC()` first, to avoid
  injecting GC latency spikes into the host application.
- Profiling never affects your application's behavior on failure. A capture or
  upload error skips that one profile for the cycle; the other profile is still
  attempted. Errors are silent unless `WithDebug(true)` is set.
- If another part of the process already holds the CPU profiler,
  `StartCPUProfile` fails for that cycle; the heap profile is unaffected.

## Middleware

`WithProfiling` lives on the core `traceway` package. Apps that initialize via
`traceway.Init` directly (for example, worker/task processes using `MeasureTask`)
can enable it today. A pass-through option for the HTTP middleware wrappers
(`tracewayhttp`, `tracewaygin`, `tracewayfiber`, `tracewaychi`,
`tracewayfasthttp`) is a follow-up.
