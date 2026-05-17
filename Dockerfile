FROM golang:1.26-bookworm AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fvc ./cmd/fvc

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/fvcd ./cmd/fvcd

FROM scratch AS export
COPY --from=builder /out/fvc /fvc
COPY --from=builder /out/fvcd /fvcd

FROM debian:bookworm-slim AS final

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/fvc /usr/local/bin/fvc
COPY --from=builder /out/fvcd /usr/local/bin/fvcd

ENTRYPOINT ["fvcd"]
