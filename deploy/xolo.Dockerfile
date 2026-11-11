# Builds Xolo from the sibling working copy, as the e2e suite does: the
# campaign measures the current code, not a published image. The build
# context is the parent directory of both repositories.
FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY xolo ./xolo
WORKDIR /src/xolo
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/xolo-server ./cmd/server && \
    for p in $(ls plugins | grep -v internal); do \
      CGO_ENABLED=0 go build -o /out/plugins/$p ./plugins/$p; \
    done

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /out/xolo-server /usr/local/bin/xolo-server
COPY --from=build /out/plugins /plugins
ENV XOLO_PLUGINS_DIR=/plugins
EXPOSE 3002
CMD ["/usr/local/bin/xolo-server"]
