FROM golang:1.26-alpine AS build
WORKDIR /src
# The plugin SDK is resolved through a replace to ../xolo, so the build context
# is the parent directory holding both checkouts (see docker-compose.yml).
COPY xolo/pkg/pluginsdk ./xolo/pkg/pluginsdk
COPY xolo-chaos ./xolo-chaos
WORKDIR /src/xolo-chaos
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /out/chaos-provider ./cmd/chaos-provider && \
    CGO_ENABLED=0 go build -o /out/chaos-monkey ./plugins/chaos-monkey

FROM alpine:3.23
COPY --from=build /out/chaos-provider /usr/local/bin/chaos-provider
COPY --from=build /out/chaos-monkey /plugins/chaos-monkey
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/chaos-provider"]
