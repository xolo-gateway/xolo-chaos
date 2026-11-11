# Image of chaos-provider published by goreleaser (see .goreleaser.yaml). It
# copies the binary goreleaser built for the target platform. To build the
# provider from the sources instead, see chaos-provider.Dockerfile.
FROM alpine:3.23
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/chaos-provider /usr/local/bin/chaos-provider
USER 65532:65532
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/chaos-provider"]
