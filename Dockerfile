FROM golang:1.26-bookworm AS builder

WORKDIR /app

COPY go.mod ./

RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /fvc ./cmd

FROM scratch AS export
COPY --from=builder /fvc /fvc

FROM debian:bookworm-slim AS final

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /fvc /usr/local/bin/fcv

ENTRYPOINT ["fvc"]
