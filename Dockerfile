# syntax=docker/dockerfile:1
FROM python:3.11-slim AS build

COPY --from=ghcr.io/astral-sh/uv:0.12.21 /uv /bin/uv

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy \
    UV_PYTHON_DOWNLOADS=never

WORKDIR /app

# dependencies get their own layer, so changing the code doesn't reinstall them
RUN --mount=type=cache,target=/root/.cache/uv \
    --mount=type=bind,source=uv.lock,target=uv.lock \
    --mount=type=bind,source=pyproject.toml,target=pyproject.toml \
    uv sync --locked --no-install-project --no-dev

COPY . .
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --locked --no-dev --no-editable


# only the installed environment makes it into the image, not uv or the sources
FROM python:3.11-slim

RUN useradd --system trafficlight
WORKDIR /app
COPY --from=build /app/.venv /app/.venv

# the web UI by default. Mount your own config.toml or set TRAFFICLIGHT_* variables to change it
RUN echo 'output = "web"' > config.toml

# environment variables win over config.toml. Inside a container, the UI has to listen on every interface
ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    TRAFFICLIGHT_WEB_HOST=0.0.0.0 \
    TRAFFICLIGHT_WEB_OPEN_BROWSER=false

USER trafficlight
EXPOSE 3335 3336
# Traffic Light quits cleanly on Ctrl+C
STOPSIGNAL SIGINT
# healthy once the receiver takes requests, which it does with every output. An empty list doesn't add a record
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["python", "-c", "import os, urllib.request; urllib.request.urlopen(urllib.request.Request('http://127.0.0.1:%s/' % os.environ.get('TRAFFICLIGHT_PORT', '3335'), data=b'[]'), timeout=4)"]
CMD ["trafficlight", "run"]
