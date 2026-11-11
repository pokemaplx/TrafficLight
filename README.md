<p align="center">
  <img src="readme_assets/logo.png" alt="Traffic Light" width="160" />
</p>

<h1 align="center">Traffic Light</h1>

<p align="center">
  <strong>Beautiful traffic logging for PGO</strong><br />
  Every request the game makes, decoded in real time, in your browser.
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/go-1.24+-00ADD8?logo=go&logoColor=white" />
  <img alt="Docker" src="https://img.shields.io/badge/docker-compose-2496ED?logo=docker&logoColor=white" />
  <img alt="Outputs" src="https://img.shields.io/badge/outputs-web%20%C2%B7%20print-7FF0D4" />
</p>

<p align="center">
  <a href="#quick-start">Quick start</a> ·
  <a href="#the-web-ui">Web UI</a> ·
  <a href="#memory">Memory</a> ·
  <a href="#protos">Protos</a> ·
  <a href="#configuration">Configuration</a> ·
  <a href="#installation">Installation</a> ·
  <a href="#writing-a-mitm">Writing a MITM</a>
</p>

> [!NOTE]
> This is a fork of [ccev/TrafficLight](https://github.com/ccev/TrafficLight), rewritten in Go around a
> [web UI](#the-web-ui). It takes the same traffic and the same `config.toml`, and runs in a fraction
> of the [memory](#memory). The terminal UI and the Discord output of the original are not part of it.

![Web UI](readme_assets/web_ui.png)

## What it is

A MITM app on your phone POSTs every request the game makes to Traffic Light. Traffic Light decodes
the protobufs and shows them as they arrive, so you can see exactly what is going on while you play.

It is a single binary with the web UI and the protos inside of it:

| command | what it does |
| --- | --- |
| `trafficlight` | start the receiver and the output you picked in your config |
| `trafficlight healthcheck` | exit with 0 if a running Traffic Light takes requests |

## Quick start

```sh
git clone https://github.com/pokemaplx/TrafficLight && cd TrafficLight
go run ./cmd/trafficlight
```

The web UI opens at <http://127.0.0.1:3336>. Point your MITM's POST destination at
`http://{your computer's IP}:3335` and traffic starts showing up.

No Go? See [Installation](#installation) for Docker.

## The web UI

The log lists one row per Proto, with the response status (`ENCOUNTER_SUCCESS`, `OUT_OF_RANGE`, …),
the size, the time of day and how long after the request before it this one arrived. Click the
header to sort by any column, or the `Δ` next to **Time** to show gaps instead of the clock.

### Filtering

Type in the filter box. Every term has to match, and keys work with `-` and quotes too.

| term | matches |
| --- | --- |
| `encounter` | the method, a message name or the content contains it |
| `"two words"` | exact phrase |
| `-telemetry` | exclude |
| `m:FORT` `method:FORT` | method name or number |
| `msg:FortDetails` | request or response message name |
| `s:ERROR` `status:ERROR` | status or result of the response |
| `is:saved` | also `is:proxy`, `is:error`, `is:ok`, `is:empty`, `is:unknown` |
| `/pika(chu)?/` | regular expression |

Values after `method:`, `msg:`, `status:` and `is:` autocomplete. **Methods** shows how often each
method was called so you can hide the noisy ones, or show only the one you care about.

### Keeping requests

The log only holds the newest requests: at most `web_max_records` of them, and at most
`web_max_memory_mb` (see [Memory](#memory)). **Save** one and it is never the one dropped, it
survives **Clear**, and it stays inspectable for as long as Traffic Light runs. Press `b` or use the
bookmark on the left of a row. At most half of either limit can be saved, so there is always room
for new traffic.

Saved requests are gone when Traffic Light restarts, unless you give it a database. Set
`database_url` to a MariaDB or MySQL database, i.e.
`mariadb://user:password@127.0.0.1:3306/trafficlight`, and every request you save is written there
and put back into the log at the next start. Unsaving one, or clearing the saved ones, deletes it
there too. Only what you save goes to the database: the log itself stays in memory, and saved
requests still count towards its limits.

Traffic Light creates its one table, `trafficlight_saved`, by itself. If the database is not up
yet it waits half a minute for it, and if it goes away later, saving says so instead of pretending.

### Several Protos at once

Every Proto you open gets a tab. `Ctrl` or middle click opens one in a background tab, a double
click opens one that stays. **Split** shows two side by side, each with its own Tree/JSON/Text
view, and **Diff** compares the pair line by line.

![Diff](readme_assets/web_diff.png)

### Reading one Proto

The Inspect View is a collapsible tree with field types, enum names and their values, or the plain
JSON and Text. `Ctrl`+`F` searches inside the Proto you are reading: it counts the matches, `Enter`
steps through them, and **Matches only** hides every field that does not match. It works in all
three views.

Lost your place in the log? A pill brings you back to the Proto you are inspecting, and so does `g`.

### Getting data out

Copy any message as JSON, Text or base64, copy a link to a Proto, or export the whole log, just what
the filter shows, or just the saved requests. Exports use the same format MITMs send, so you can
POST one back to Traffic Light later and read it again.

### Keyboard

Press `?` in the app for this list.

| key | action | key | action |
| --- | --- | --- | --- |
| `/` `Ctrl`+`K` | filter | `p` | pause capturing |
| `↑` `↓` `j` `k` | previous / next | `f` | follow new requests |
| `Home` `End` | first / last | `1` | first Proto only |
| `g` | back to the selected Proto | `b` | save or unsave |
| `Enter` | open in a tab that stays | `w` | close the tab |
| `[` `]` | previous / next tab | `Ctrl`+`F` | find in this Proto |
| `n` | next match | `t` | Tree / JSON / Text |
| `m` | methods | `h` | hide the inspected method |
| `c` | copy inspected as JSON | `?` | help |

### Opening it from another device

By default the page only opens on your own computer. Set `web_host = "0.0.0.0"` to reach it from
elsewhere on your network.

> [!IMPORTANT]
> Set `web_password` before you put the web UI on the internet. It then asks for that password;
> logins last 30 days and changing the password logs everyone out.

A password is also what lets you open it through a domain. Without one, only localhost and IP
addresses work, so no website can read your traffic through DNS rebinding. Behind a reverse proxy,
keep the original Host header — Coolify and Caddy do, nginx needs `proxy_set_header Host $host;`.

## Forwarding

Set `forward_url` and Traffic Light passes every request it receives on to that endpoint as well,
i.e. a Golbat's `http://127.0.0.1:9001/raw`. Point your MITM at Traffic Light and Traffic Light at
your scanner, and you can inspect traffic without taking the endpoint away from it. Set
`forward_token` to send a bearer token with every request.

The body goes on untouched, so the other end sees exactly what your MITM sent. Forwarding is fire
and forget: your MITM is answered without waiting for the other end, nothing is ever retried, and an
endpoint that is down or slow costs you the forwarded requests and nothing else. Traffic Light says
so once when forwarding starts failing, and once when it works again.

## Other outputs

There's also an option to simply print all requests (`output = "print"`). You can use that if you
don't like the UI.

## Memory

Traffic Light keeps the log in memory, and it only keeps what your MITM sent: the bytes of every
request, still encoded. They are decoded when a browser lists them or when you inspect one, and
what that takes is thrown away right after. So what Traffic Light uses is what its log weighs plus
about 70 MB, and `web_max_memory_mb` decides what the log may weigh:

| `web_max_memory_mb` | Traffic Light uses up to about |
| --- | --- |
| `64` | 130 MB |
| `128` (default) | 200 MB |
| `512` | 630 MB |

When the log is full, the oldest request goes for every new one, like with `web_max_records`.
Requests are a few hundred bytes to a few hundred KB each, so the default holds anything from a few
hundred map responses to all 5000 requests. Traffic nobody is watching costs next to nothing: without
a browser connected, a request is stored and that is it.

To see where memory and time actually go, set `debug_addr = "127.0.0.1:6060"`. That serves Go's
profiler:

```sh
go tool pprof http://127.0.0.1:6060/debug/pprof/heap      # what is in memory right now
go tool pprof http://127.0.0.1:6060/debug/pprof/profile   # where 30 seconds of cpu went
```

## Protos

The game changes with every version, and Traffic Light can only decode what its protos describe.
A method they don't know is listed as unknown with a best guess of its content, and a field they
don't know is not shown.

Traffic Light keeps them current by itself. At startup and every `protos_update_hours` after that
it looks at `protos_url`, and when there is something new it switches to it while running: nothing
is rebuilt or restarted, and the log stays. What it can't read, or what isn't a complete set of
protos, is left alone and the ones in use stay. By default that is the `pogo_pb2.py` of the
[original Traffic Light](https://github.com/ccev/TrafficLight), which gets the reverse-engineered
protos of a new version some time after it comes out. Set `protos_url = ""` to not look anywhere.

That gap is where `methods_url` helps. The names of a new version's methods can be read from
the game itself the day it comes out, long before its protos are worked out. Point `methods_url`
at a file or a URL with a list of them and Traffic Light names the methods the protos don't know
yet, and uses the messages they mention where the protos have them:

```json
{"methods": [{"id": 106, "name": "METHOD_GET_MAP_OBJECTS",
              "request_message": "holoholo.rpc.GetMapObjectsProto",
              "response_message": "holoholo.rpc.GetMapObjectsOutProto"}]}
```

Where the protos know a method, they win. What a new method carries is still a best guess until its
protos arrive: a name is all the game gives away about it.

## Configuration

Copy `config.example.toml` to `config.toml`. Every option can also be set as an environment
variable, i.e. `TRAFFICLIGHT_OUTPUT=web`, and those win over the file.

| option | default | what it does |
| --- | --- | --- |
| `host` `port` | `0.0.0.0` `3335` | where the receiver listens for your MITM |
| `output` | `web` | `web` or `print` |
| `forward_url` | empty | also pass every request on to this endpoint |
| `forward_token` | empty | bearer token sent with the forwarded request |
| `web_host` `web_port` | `127.0.0.1` `3336` | where the web UI listens. `0.0.0.0` opens it to your network |
| `web_password` | empty | makes the web UI ask for a password |
| `web_max_records` | `5000` | how many requests the web UI keeps |
| `web_max_memory_mb` | `128` | how much the kept requests may weigh, see [Memory](#memory). `0` for no limit |
| `web_open_browser` | `true` | open a browser when Traffic Light starts |
| `protos_url` | the original Traffic Light's | where newer protos are looked for, see [Protos](#protos). Empty to not look |
| `protos_update_hours` | `6` | how often. `0` looks at startup only |
| `methods_url` | empty | a file or URL naming the methods the protos don't know yet |
| `database_url` | empty | keep saved requests in this MariaDB or MySQL database, see [Keeping requests](#keeping-requests) |
| `debug_addr` | empty | serve Go's profiler there, i.e. `127.0.0.1:6060` |

## Installation

Traffic Light is made to run on your own computer, next to the phone you are playing on.

### With Go

```sh
go install github.com/pokemaplx/TrafficLight/cmd/trafficlight@latest
```

The `trafficlight` command is then in your Go bin directory. Running it opens the receiver on port
`3335` and the web UI on `3336`, and you can point a supported MITM at it. It reads `config.toml`
from the directory you run it in, and works without one.

### From a clone

Clone the repo and, if you want to change anything, copy `config.example.toml` to `config.toml`. You
need Go 1.24 or newer.

```sh
go run ./cmd/trafficlight
```

Or build the binary once and run that: `go build ./cmd/trafficlight`.

Then set your MITM's POST destination to your endpoint from `config.toml`, by default
`http://{computer IP}:3335`.

### Docker

```sh
docker compose up --build
```

The web UI is at <http://localhost:3336> and your MITM posts to `http://{computer IP}:3335`. Set any
option from `config.example.toml` as an environment variable in `compose.yaml`, i.e.
`TRAFFICLIGHT_WEB_PASSWORD=...`, or mount your own `config.toml` the way that file shows. The image
is the binary and nothing else.

### Coolify

- Create an application from this repository with the `Dockerfile` build pack
- **Ports Exposes:** `3336`, the web UI that Coolify puts behind your domain
- **Ports Mappings:** `3335:3335`, so your MITM can reach the receiver at `http://{server IP}:3335`
- **Environment variables:** `TRAFFICLIGHT_WEB_PASSWORD`, since your domain is public, and
  `TRAFFICLIGHT_WEB_MAX_MEMORY_MB` if the default of 128 is more than you want to give it
- **Optional:** add a MariaDB from Coolify's databases and set `TRAFFICLIGHT_DATABASE_URL` to its
  internal URL, so the requests you save survive a redeploy

## Writing a MITM

To support Traffic Light, a MITM only has to POST a body like this to the receiver for every request
the game makes. It should run alongside the game, so you can play normally while inspecting traffic.

```json
{
    "rpcid": 1,
    "rpcstatus": 1,
    "rpchandle": 1, // optional field
    "protos": [
        {
            "method": 1,
            "request": "<b64 encoded string>",
            "response": "<b64 encoded string>"
        },
        {
            "method": 106,
            "request": "",
            "response": ""
        }
    ]
}
```

`contents` instead of `protos`, `type` instead of `method` and `payload` instead of `response` are
taken as well, which is what scanners like Golbat expect. A body can also be a list of these.

## Development

```sh
go test ./...                                    # everything the server does
go test -bench . ./internal/web ./internal/receiver
npx tsc -p internal/web/static/jsconfig.json     # type checks the browser side
```

The database is only tested against a real one if you name it:

```sh
TRAFFICLIGHT_TEST_DATABASE_URL=mariadb://root:test@127.0.0.1/trafficlight go test ./internal/mariadb
```

The browser side is plain ES modules in `internal/web/static/js` with no build step. They are built
into the binary, so restart Traffic Light after editing one.

`internal/pogo` is everything that knows the protos. Its tests compare what it says about a message
with what the Python implementation this is a rewrite of said (`testdata/golden.jsonl.gz`), and what
its fast path says with what protobuf's own decoder says, on valid and on broken bytes.

### Building with newer protos

The protos a build starts out with are `internal/pogo/pogo.desc`. A running Traffic Light replaces
them by itself (see [Protos](#protos)), so this only matters for what is there before the first
look, or with `protos_url` turned off:

```sh
go run ./cmd/protoupdate                       # the newest ones from the original Traffic Light
go run ./cmd/protoupdate path/to/pogo_pb2.py   # or a file, or a descriptor set from protoc
```
