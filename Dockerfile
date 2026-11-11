# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/travel-docs .

# Static binary on a minimal image without a shell, running as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/travel-docs /travel-docs
EXPOSE 8080
ENTRYPOINT ["/travel-docs"]
