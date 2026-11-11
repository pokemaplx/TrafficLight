# Memory

[← back to the README](../README.md)

Traffic Light keeps the log in memory, and it only keeps what your MITM sent: the bytes of every
request, still encoded. They are decoded when a browser lists them or when you inspect one, and
what that takes is thrown away right after.

So what Traffic Light uses is what its log weighs plus about 70 MB, and `web_max_memory_mb` decides
what the log may weigh:

| `web_max_memory_mb` | Traffic Light uses up to about |
| --- | --- |
| `64` | 130 MB |
| `128` (default) | 200 MB |
| `512` | 630 MB |

Idle, with an empty log, it is about 30 MB.

When the log is full, the oldest request goes for every new one, like with `web_max_records`.
Requests are a few hundred bytes to a few hundred KB each, so the default holds anything from a few
hundred map responses to all 5000 requests. Set it to `0` to only limit the number of requests.

Traffic nobody is watching costs next to nothing: without a browser connected, a request is stored
and that is it.

## What a browser costs

A browser gets the text of every request in the log, to filter and search in. That is about five
times what the log weighs, sent when it connects and kept by the browser tab for as long as it is
open. The server writes that text on the way out and keeps none of it, but the tab does: a full log
of 128 MB is several hundred MB in the browser.

Every connected browser also costs the server some CPU for each new request, to write and compress
its text.

## Seeing where it goes

Set `debug_addr = "127.0.0.1:6060"` and Traffic Light serves Go's profiler there:

```sh
go tool pprof http://127.0.0.1:6060/debug/pprof/heap      # what is in memory right now
go tool pprof http://127.0.0.1:6060/debug/pprof/profile   # where 30 seconds of cpu went
```

In a container, `docker stats` shows what it uses as a whole.

## Tuning the runtime

Traffic Light tells Go's garbage collector to clean up ten times as often as it would by itself,
since the log is plain bytes that cost next to nothing to look past, and sets a soft limit well
above what is expected. Both can be overridden with Go's own `GOGC` and `GOMEMLIMIT` variables.
