# syntax=docker/dockerfile:1
FROM node:24.21.0-bookworm-slim@sha256:0e0ff40c39bc087845bfb27465a0df4ea419520094bc35842ff83dd8cbe6f9b6 AS build
WORKDIR /work/apps/web
COPY apps/web/package.json apps/web/package-lock.json ./
RUN npm ci
COPY apps/web/ ./
COPY apps/api/internal/graph/schema.graphqls /work/apps/api/internal/graph/schema.graphqls
RUN npm run build
FROM nginx:1.30.5-alpine3.24@sha256:a5f2157a0302eb0c5e300415effb63a9e70ed1eb9c107283819bf6d149ab607c
COPY apps/web/nginx.conf /etc/nginx/nginx.conf
COPY apps/web/vault-tls.conf /etc/nginx/vault-tls.conf
COPY --from=build /work/apps/web/dist/ /usr/share/nginx/html/
USER 101:101
EXPOSE 8081 8443
ENTRYPOINT ["nginx","-g","daemon off;"]
