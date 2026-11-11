# The web UI

[← back to the README](../README.md)

The log lists one row per Proto, with the response status (`ENCOUNTER_SUCCESS`, `OUT_OF_RANGE`, …),
the size, the time of day and how long after the request before it this one arrived. Click the
header to sort by any column, or the `Δ` next to **Time** to show gaps instead of the clock.

- [Filtering](#filtering)
- [Keeping requests](#keeping-requests)
- [Several Protos at once](#several-protos-at-once)
- [Reading one Proto](#reading-one-proto)
- [Getting data out](#getting-data-out)
- [Keyboard](#keyboard)

## Filtering

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

## Keeping requests

The log only holds the newest requests: at most `web_max_records` of them, and at most
`web_max_memory_mb` (see [Memory](memory.md)). **Save** one and it is never the one dropped, it
survives **Clear**, and it stays inspectable for as long as Traffic Light runs. Press `b` or use the
bookmark on the left of a row.

At most 200 requests can be saved, and never more than half of either limit, so there is always
room for new traffic.

### Past a restart

Saved requests are gone when Traffic Light restarts, unless you give it a database. Set
`database_url` to a MariaDB or MySQL database:

```toml
database_url = "mariadb://user:password@127.0.0.1:3306/trafficlight"
```

Every request you save is then written there and put back into the log at the next start, with
the time it arrived at. Unsaving one, or clearing the saved ones, deletes it there too.

- Only what you save goes to the database. The log itself stays in memory, and saved requests
  still count towards its limits.
- Traffic Light creates its one table, `trafficlight_saved`, by itself.
- If the database is not up yet, Traffic Light waits half a minute for it at startup.
- If it goes away later, saving and unsaving say so instead of pretending, and work again once it
  is back. Your MITM never waits for the database.
- Special characters in the password have to be percent-encoded.

## Several Protos at once

Every Proto you open gets a tab. `Ctrl` or middle click opens one in a background tab, a double
click opens one that stays. **Split** shows two side by side, each with its own Tree/JSON/Text
view, and **Diff** compares the pair line by line.

![Diff](images/web_diff.png)

## Reading one Proto

The Inspect View is a collapsible tree with field types, enum names and their values, or the plain
JSON and Text. `Ctrl`+`F` searches inside the Proto you are reading: it counts the matches, `Enter`
steps through them, and **Matches only** hides every field that does not match. It works in all
three views.

Lost your place in the log? A pill brings you back to the Proto you are inspecting, and so does `g`.

A method the protos don't know is listed as unknown, with a best guess of its content: fields by
their number, without names. See [Protos](protos.md).

## Getting data out

Copy any message as JSON, Text or base64, copy a link to a Proto, or export the whole log, just what
the filter shows, or just the saved requests. Exports use the same format MITMs send, so you can
POST one back to Traffic Light later and read it again.

## Keyboard

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

While paused, incoming traffic is not added to the log. It is still forwarded.
