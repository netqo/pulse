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
    -o /out/seed ./cmd/seed

# --- runtime stage ---
# seed is a one-shot batch job: it runs to completion and exits, so it needs no
# health check or exposed port.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=build /out/seed /usr/local/bin/seed
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/seed"]
