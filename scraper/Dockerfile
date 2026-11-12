# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/scraper . \
    && mkdir -p /out/data

# Static binary, no shell, non-root. The state file lives in /data.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/scraper /scraper
# Owned by the nonroot user, so a fresh named volume mounted here is writable.
COPY --from=build --chown=65532:65532 /out/data /data
WORKDIR /data
ENV SCRAPER_STATE=/data/state.json
ENTRYPOINT ["/scraper"]
