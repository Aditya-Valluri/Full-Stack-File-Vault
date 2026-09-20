# syntax=docker/dockerfile:1
FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
RUN mkdir -p /out /data/staging /data/blobs && chmod 700 /data/staging /data/blobs && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/collect ./cmd/collect && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/provision-user ./cmd/provision-user && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/init-storage ./cmd/init-storage && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/probe ./cmd/probe
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /app
COPY --from=build /out/ /app/
COPY --from=build --chown=65532:65532 /data/ /data/
COPY db/migrations/ /migrations/
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
