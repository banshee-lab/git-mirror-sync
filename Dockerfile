FROM golang:1.27-alpine AS builder

RUN apk add --no-cache build-base

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN go build -o /app/forgejo-sync ./cmd/forgejo-sync/

FROM alpine:latest

RUN apk add --no-cache git openssh-client

RUN addgroup -S git && adduser -S git -G git

RUN mkdir -p /var/cache/forgejo-sync && chown -R git:git /var/cache/forgejo-sync

USER git
WORKDIR /home/git

COPY --from=builder /app/forgejo-sync /usr/local/bin/forgejo-sync

HEALTHCHECK --interval=30s --timeout=3s \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8081/healthz || exit 1

ENTRYPOINT ["forgejo-sync"]
