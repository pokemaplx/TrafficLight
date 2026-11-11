# Protos

[← back to the README](../README.md)

The game changes with every version, and Traffic Light can only decode what its protos describe.
A method they don't know is listed as unknown with a best guess of its content, and a field they
don't know is not shown.

- [Keeping them current](#keeping-them-current)
- [Methods the protos don't know yet](#methods-the-protos-dont-know-yet)
- [Building with newer protos](#building-with-newer-protos)

## Keeping them current

Traffic Light does that by itself. At startup and every `protos_update_hours` after that it looks
at `protos_url`, and when there is something new it switches to it while running: nothing is rebuilt
or restarted, and the log stays. Requests are decoded when somebody looks at them, so the ones
already in the log are read with the new protos as well once a browser reloads.

What it can't read, or what isn't a complete set of protos, is left alone and the ones in use stay.
It says so once, and once more when looking works again.

By default `protos_url` is the `pogo_pb2.py` of the
[original Traffic Light](https://github.com/ccev/TrafficLight), which gets the reverse-engineered
protos of a new version some time after it comes out. It can be any URL or file that holds one of:

- a `pogo_pb2.py`, the way protoc writes it for Python
- a descriptor set, compiled with `protoc --descriptor_set_out`

| | |
| --- | --- |
| `protos_url = ""` | don't look anywhere, stay with the protos of the build |
| `protos_update_hours = 0` | look at startup only |

## Methods the protos don't know yet

Between a new version of the game and its protos there is a gap, and `methods_url` covers part of
it. The names of a version's methods can be read from the game itself the day it comes out, long
before anyone has worked out its protos. Point `methods_url` at a file or a URL with a list of them:

```json
{
  "methods": [
    {
      "id": 106,
      "name": "METHOD_GET_MAP_OBJECTS",
      "request_message": "holoholo.rpc.GetMapObjectsProto",
      "response_message": "holoholo.rpc.GetMapObjectsOutProto"
    }
  ]
}
```

Traffic Light then names the methods the protos don't know, and uses the messages a method
mentions where the protos have a message of that name. A bare list works as well, and the messages
are optional. The file is looked at on the same schedule as the protos.

- Where the protos know a method, they win.
- What a new method carries is still a best guess until its protos arrive: a name is all the game
  gives away about it.

## Building with newer protos

The protos a build starts out with are `internal/protos/pogo.desc`. A running Traffic Light replaces
them by itself, so this only matters for what is there before the first look, or with `protos_url`
turned off:

```sh
go run ./cmd/protoupdate                       # the newest ones from the original Traffic Light
go run ./cmd/protoupdate path/to/pogo_pb2.py   # or a file, or a descriptor set from protoc
```

Nothing is generated from them. The file holds the descriptors of every message, and Traffic Light
reads it at startup.
