ARG GO_IMAGE=golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea
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
