# Traffic

[← back to the README](../README.md)

- [Writing a MITM](#writing-a-mitm)
- [Forwarding](#forwarding)
- [Limits](#limits)

## Writing a MITM

To support Traffic Light, a MITM only has to POST a body like this to the receiver for every request
the game makes. It should run alongside the game, so you can play normally while inspecting traffic.

```json
{
    "rpcid": 1,
    "rpcstatus": 1,
    "rpchandle": 1,
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

- `rpchandle` is optional, and so are `request` and `response`.
- `contents` instead of `protos`, `type` instead of `method` and `payload` instead of `response` are
  taken as well, which is what scanners like Golbat expect.
- A body can also be a list of these.
- Fields Traffic Light doesn't know are ignored, so the body your scanner wants can be sent as it is.

The receiver answers `OK`, `400` for a body it can't read, and `413` for one over 64 MB. It has no
password: whoever can reach its port can send it traffic, so don't open it to more than your MITM.

## Forwarding

Set `forward_url` and Traffic Light passes every request it receives on to that endpoint as well,
i.e. a Golbat's `http://127.0.0.1:9001/raw`. Point your MITM at Traffic Light and Traffic Light at
your scanner, and you can inspect traffic without taking the endpoint away from it. Set
`forward_token` to send a bearer token with every request.

The body goes on untouched, so the other end sees exactly what your MITM sent. Forwarding is fire
and forget: your MITM is answered without waiting for the other end, nothing is ever retried, and an
endpoint that is down or slow costs you the forwarded requests and nothing else. Traffic Light says
so once when forwarding starts failing, and once when it works again.

Anything that is JSON is forwarded, even if Traffic Light itself can't make sense of it.

## Limits

| what | limit | when it is reached |
| --- | --- | --- |
| one POST from a MITM | 64 MB | it is refused with `413` |
| the log | `web_max_records` and `web_max_memory_mb` | the oldest request that isn't saved goes |
| saved requests | 200, and half of either limit of the log | saving says so and doesn't save |
| a browser that falls behind | 1000 messages or 64 MB waiting for it | it is disconnected, and starts over when it reconnects |
| forwarding | 100 requests on their way, 10 seconds each | what comes on top is dropped, not retried |

A request bigger than the whole log is still taken: it pushes everything else out until the next
one arrives.
