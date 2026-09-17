FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -trimpath -o /tdx-research ./cmd/tdx-research

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libstdc++6 tzdata && rm -rf /var/lib/apt/lists/* && useradd -m -u 10001 tdx
WORKDIR /app
COPY --from=builder /tdx-research /app/tdx-research
COPY --from=builder /src/LICENSE /app/LICENSE
COPY --from=builder /src/docs/third-party/tdx2db-LICENSE /app/tdx2db-LICENSE
RUN mkdir -p /app/output/research && chown -R tdx:tdx /app/output
USER tdx
EXPOSE 8080
ENTRYPOINT ["/app/tdx-research"]
