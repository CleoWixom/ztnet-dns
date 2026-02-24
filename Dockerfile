FROM golang:1.24.13 AS build
WORKDIR /src
COPY . .
RUN go mod tidy && \
    go test ./... && \
    make build-coredns

FROM gcr.io/distroless/base-debian12
COPY --from=build /tmp/coredns-ztnet-build/coredns /usr/local/bin/coredns
ENTRYPOINT ["/usr/local/bin/coredns"]
