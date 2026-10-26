FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm test && npm run build

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist/ ./cmd/reader/static/
ARG TARGETOS
ARG TARGETARCH
RUN go test ./... && CGO_ENABLED=0 GOOS=${TARGETOS:-$(go env GOOS)} GOARCH=${TARGETARCH:-$(go env GOARCH)} go build -trimpath -ldflags="-s -w" -o /out/reader ./cmd/reader

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/* && useradd --uid 10001 --create-home reader && mkdir -p /data/books /data/tmp && chown -R reader:reader /data
COPY --from=backend /out/reader /usr/local/bin/reader
USER 10001:10001
ENV READER_HTTP_ADDR=:8080 READER_DATA_DIR=/data
WORKDIR /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["reader", "healthcheck"]
ENTRYPOINT ["reader"]
CMD ["serve"]
