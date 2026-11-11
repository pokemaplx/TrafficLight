# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build

WORKDIR /src

# dependencies get their own layer, so changing the code doesn't download them again
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /trafficlight ./cmd/trafficlight


# Traffic Light is a single binary with the web UI and the protos inside of it, so that binary
# is the whole image. The certificates are for forwarding to an https endpoint
FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /trafficlight /trafficlight

# mount your own config.toml here, or set TRAFFICLIGHT_* variables
WORKDIR /app

# environment variables win over config.toml. Inside a container, the UI has to listen on every interface
ENV TRAFFICLIGHT_WEB_HOST=0.0.0.0 \
    TRAFFICLIGHT_WEB_OPEN_BROWSER=false

USER 65534:65534
EXPOSE 3335 3336
# Traffic Light quits cleanly on Ctrl+C
STOPSIGNAL SIGINT
# healthy once the receiver takes requests. An empty list doesn't add a record
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/trafficlight", "healthcheck"]
ENTRYPOINT ["/trafficlight"]
CMD ["run"]
