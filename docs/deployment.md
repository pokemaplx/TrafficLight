# Deployment

[← back to the README](../README.md)

Traffic Light is one binary. It reads `config.toml` from the directory it runs in and works without
one, and every option can be an environment variable instead (`TRAFFICLIGHT_WEB_PASSWORD`, …).

- [With Go](#with-go)
- [Docker](#docker)
- [Coolify](#coolify)
- [Password and domains](#password-and-domains)
- [A database for saved requests](#a-database-for-saved-requests)

## With Go

```sh
go install github.com/pokemaplx/TrafficLight/cmd/trafficlight@latest
trafficlight
```

Or from a clone, with Go 1.24 or newer:

```sh
go run ./cmd/trafficlight          # or build it once: go build ./cmd/trafficlight
```

The receiver is on port `3335` and the web UI on `3336`. Set your MITM's POST destination to
`http://{computer IP}:3335`.

## Docker

```sh
docker compose up --build
```

The web UI is at <http://localhost:3336> and your MITM posts to `http://{computer IP}:3335`. Set
options as environment variables in `compose.yaml`, or mount your own `config.toml` the way that
file shows.

The image is the binary and nothing else, about 13 MB, and runs as an unprivileged user. That also
means it has no shell and no `curl`: its health check is the binary itself,
`/trafficlight healthcheck`, and a health check that runs a command inside the container won't work.

## Coolify

- Create an application from this repository with the `Dockerfile` build pack
- **Ports Exposes:** `3336`, the web UI that Coolify puts behind your domain
- **Ports Mappings:** `3335:3335`, so your MITM can reach the receiver at `http://{server IP}:3335`
- **Environment variables:** `TRAFFICLIGHT_WEB_PASSWORD`, since your domain is public, and
  `TRAFFICLIGHT_WEB_MAX_MEMORY_MB` if the default of 128 is more than you want to give it
- **Health check:** leave Coolify's own off, the image brings one
- **Optional:** add a MariaDB from Coolify's databases and set `TRAFFICLIGHT_DATABASE_URL` to its
  internal URL, so the requests you save survive a redeploy

The log is in memory, so a redeploy starts with an empty one. Logins survive it.

## Password and domains

By default the page only opens on your own computer. Set `web_host = "0.0.0.0"` to reach it from
elsewhere on your network. In a container it listens on every interface already.

> [!IMPORTANT]
> Set `web_password` before you put the web UI on the internet. It then asks for that password;
> logins last 30 days and changing the password logs everyone out.

A password is also what lets you open it through a domain. Without one, only localhost and IP
addresses work, so no website can read your traffic through DNS rebinding. Behind a reverse proxy,
keep the original Host header — Coolify and Caddy do, nginx needs `proxy_set_header Host $host;`.

The receiver has no password. Keep its port to what your MITM needs to reach.

## A database for saved requests

Optional: with `database_url` set to a MariaDB or MySQL database, the requests you save are still
there after a restart. `compose.yaml` has a MariaDB service to uncomment. How it behaves is in
[The web UI](web-ui.md#past-a-restart).

```toml
database_url = "mariadb://trafficlight:password@mariadb:3306/trafficlight"
```
