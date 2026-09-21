# syntax=docker/dockerfile:1
FROM node:24.21.0-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS web
WORKDIR /work/apps/web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci
COPY apps/web/ ./
COPY apps/api/internal/graph/schema.graphqls /work/apps/api/internal/graph/schema.graphqls
RUN VITE_TEMPORARY_DEMO=true npm run build

FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
RUN mkdir -p /out /data/staging /data/blobs && chmod 700 /data/staging /data/blobs && \
 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collect ./cmd/collect && \
 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/probe ./cmd/probe && \
 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/render-demo ./cmd/render-demo

FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
WORKDIR /app
COPY --from=build /out/ /app/
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /data/ /data/
COPY --from=web /work/apps/web/dist/ /app/public/
COPY db/migrations/ /migrations/
USER 65532:65532
EXPOSE 10000
# Startup runs repeatable setup because free services have no pre-deploy phase.
CMD ["/app/render-demo"]
