# syntax=docker/dockerfile:1
#
# Multi-stage build for the pgfleet dashboard. Because the app is a single Go
# binary with the Vue UI embedded, the final image is just the static binary on
# a distroless base.

# Stage 1 — build the Vue single-page app. Vite writes the bundle to
# ../internal/web/dist (see web/vite.config.js), so the Go stage can embed it.
FROM node:22-alpine AS web
WORKDIR /src/web
# Copy manifests first for layer caching. package-lock.json is optional (the
# repo may not carry one); npm install regenerates it, npm ci uses it if present.
COPY web/package.json web/package-lock.json* ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/ ./
RUN npm run build

# Stage 2 — build the static Go binary with the freshly built UI embedded.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Overwrite the committed dist with the optimized bundle from the web stage.
COPY --from=web /src/internal/web/dist ./internal/web/dist
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /pgfleet ./cmd/pgfleet

# Stage 3 — minimal runtime. distroless/static has no shell and runs as nonroot.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /pgfleet /usr/local/bin/pgfleet
# The web command loads config, the migration set, and discovers tenants at
# startup, so ship the config and migrations alongside the binary. The DSN is
# supplied at runtime via PGFLEET_DSN — never baked into the image.
COPY pgfleet.yaml ./pgfleet.yaml
COPY migrations ./migrations
EXPOSE 8080
ENTRYPOINT ["pgfleet"]
CMD ["web", "--addr", ":8080"]
