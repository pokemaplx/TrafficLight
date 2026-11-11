# Development

[← back to the README](../README.md)

- [How it is laid out](#how-it-is-laid-out)
- [The way of a request](#the-way-of-a-request)
- [Tests](#tests)
- [The browser side](#the-browser-side)

## How it is laid out

```
cmd/
  trafficlight/    the program: reads the config and wires everything below together
  protoupdate/     refreshes the protos a build starts out with
ui/                the browser side, built into the binary
internal/
  traffic/         what a MITM sends, still encoded
  receiver/        takes it over http
  forward/         passes it on to another endpoint
  protos/          which protos are in use, and keeping them current
  decode/          makes sense of traffic with them
  output/
    web/           the log, and the server browsers look at it with
    printer/       a plain log on stdout
  archive/         saved requests that outlive a restart
    mariadb/
  config/          config.toml and the environment
```

Packages only know the ones above them in this list, and `traffic` knows nothing:

| package | knows about |
| --- | --- |
| `traffic` | nothing |
| `receiver`, `forward` | `traffic` |
| `protos` | nothing |
| `decode` | `traffic`, `protos` |
| `archive`, `archive/mariadb` | `traffic` |
| `output/web` | `traffic`, `protos`, `decode`, `archive`, `ui` |
| `output/printer` | `traffic`, `protos`, `decode` |

Inside `internal/output/web`, every file is one concern: `store.go` is the log and its limits,
`client.go` a connected browser, `api.go` inspecting and exporting, `archive.go` what is saved to a
database, `auth.go` the password and keeping other websites out, `static.go` the page.

## The way of a request

1. `receiver` reads the body, hands it untouched to `forward`, and parses it just far enough to
   know its protos' bytes. Nothing is decoded.
2. `output/web` puts those bytes into the log (`store.go`) and drops the oldest if a limit is
   reached. Without a browser connected, that is all that happens.
3. For every connected browser, `client.go` writes the request's summary: the one line of text the
   browser lists and searches. `decode.AppendSummary` writes it straight from the bytes, without
   building the message (`decode/wire.go`). That is the hot path, and it allocates nothing.
4. When somebody inspects a proto, `api.go` decodes that one message for real (`decode.Decode`) and
   sends its text, its json and the tree the inspector draws.

`protos.Default()` is the set of protos in use. `protos.Updater` replaces it while Traffic Light
runs, and everything above asks for it again each time instead of holding on to it.

## Tests

```sh
go test ./...
```

What they hold the code to:

- **`decode` says what the original said.** `testdata/golden.jsonl.gz` is what the Python
  implementation this is a rewrite of made of protos of every kind: what it listed, what its
  inspector showed and what it exported. The web UI was written against that.
- **The fast path says what the decoder says.** `TestTheWalkerAgreesWithTheDecoder` compares
  `decode/wire.go` with protobuf's own decoder on valid messages, on messages set twice, shuffled
  and merged, and on broken bytes. `FuzzWalker` does the same on whatever the fuzzer comes up with:

  ```sh
  go test -run xxx -fuzz FuzzWalker -fuzztime 1m ./internal/decode
  ```

- **The log keeps its promises.** `output/web` tests what is dropped and what is kept, the limits,
  what browsers are told, the password, and a real browser connection end to end.

Some things need more than `go test`:

```sh
# benchmarks: what listing, inspecting and receiving a big request cost
go test -run xxx -bench . ./internal/output/web ./internal/receiver

# the race detector needs cgo, which a Go container has
docker run --rm -v "$PWD:/src:ro" -w /src golang:1.27 go test -race ./...

# the database is only tested against a real one if you name it
docker run --rm -d -p 3306:3306 -e MARIADB_ROOT_PASSWORD=test -e MARIADB_DATABASE=trafficlight mariadb:11
TRAFFICLIGHT_TEST_DATABASE_URL=mariadb://root:test@127.0.0.1/trafficlight go test ./internal/archive/mariadb
```

## The browser side

`ui/` is one page, a stylesheet and plain ES modules, with no build step and no dependencies. They
are built into the binary, so restart Traffic Light after editing one. Their JSDoc types can be
checked with TypeScript:

```sh
npx tsc -p ui/jsconfig.json
```

That is not part of the tests, and the newest TypeScript finds things to complain about in them.

The server and the page talk over a websocket (`/ws`): the server sends `hello`, the log as
`history`, then `records` as they arrive, and the page sends `pause`, `save` and `clear`. Inspecting
and exporting are plain requests under `/api`.
