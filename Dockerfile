FROM --platform=$BUILDPLATFORM golang:1.26.5-trixie AS build

ARG TARGETOS
ARG TARGETARCH

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/knowledge-commons ./cmd/knowledge-commons
RUN mkdir -p /out/data && chown 10001:10001 /out/data

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/knowledge-commons /usr/local/bin/knowledge-commons
COPY --from=build --chown=10001:10001 /out/data /data

ENV KC_HTTP_ADDRESS=:8080 \
    KC_STORAGE_PROVIDER=sqlite \
    KC_DATA_PATH=/data/knowledge-commons.db

EXPOSE 8080
VOLUME ["/data"]
USER 10001:10001
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 CMD ["/usr/local/bin/knowledge-commons", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/knowledge-commons"]
