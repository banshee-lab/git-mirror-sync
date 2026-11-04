FROM node:24-alpine AS frontend-builder
WORKDIR /src/frontend
COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY frontend .
RUN pnpm exec ng build

FROM golang:1.27-alpine AS go-builder
WORKDIR /src
RUN apk add --no-cache build-base
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend-builder /src/frontend/dist/frontend/browser /src/internal/webapp/dist
ENV CGO_ENABLED=0
RUN go build -o /out/forgejo-sync ./cmd/forgejo-sync/

FROM alpine:3.24
RUN apk add --no-cache git openssh-client ca-certificates wget
RUN addgroup -S git && adduser -S -h /home/git git -G git
RUN mkdir -p /var/cache/forgejo-sync /var/cache/forgejo-sync/keys && chown -R git:git /var/cache/forgejo-sync
USER git
WORKDIR /home/git
COPY --from=go-builder /out/forgejo-sync /usr/local/bin/forgejo-sync
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/forgejo-sync"]
