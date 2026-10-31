ARG GO_IMAGE=golang:1.27.3-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/oscar-corrtest ./cmd/oscar-corrtest
RUN install -d -o 65532 -g 65532 /var/lib/oscar-corrtest

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/oscar-corrtest /oscar-corrtest
COPY --from=build --chown=65532:65532 /var/lib/oscar-corrtest /var/lib/oscar-corrtest
USER 65532:65532
EXPOSE 8787
ENTRYPOINT ["/oscar-corrtest"]
CMD ["help"]
