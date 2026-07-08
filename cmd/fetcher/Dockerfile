# syntax=docker/dockerfile:1

# --- build stage ---
FROM golang:1.24-alpine AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w \
      -X github.com/netqo/pulse/internal/version.Version=${VERSION} \
      -X github.com/netqo/pulse/internal/version.Commit=${COMMIT} \
      -X github.com/netqo/pulse/internal/version.Date=${DATE}" \
    -o /out/fetcher ./cmd/fetcher

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/fetcher /usr/local/bin/fetcher
EXPOSE 9101
USER nonroot:nonroot
# The image is distroless (no shell), so the health check runs the binary's own
# "healthcheck" subcommand, which probes the local /healthz endpoint.
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/fetcher", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/fetcher"]
