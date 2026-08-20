ARG GO_IMAGE=golang:1.27.0-bookworm@sha256:d22fb682b72b6ebf58365871c437cf75794131831a6b8e6f6ebc5302c67c1cad
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOWORK=off CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/oscar-corrtest ./cmd/oscar-corrtest

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/oscar-corrtest /oscar-corrtest
USER 65532:65532
EXPOSE 8787
ENTRYPOINT ["/oscar-corrtest"]
CMD ["serve", "--listen", "127.0.0.1:8787", "--data-dir", "/var/lib/oscar-corrtest"]
