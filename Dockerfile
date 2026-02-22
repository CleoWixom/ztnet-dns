FROM golang:1.22 AS build
WORKDIR /src
COPY . .
RUN go mod tidy && go test ./... && go build -o /out/ztnet-dns ./...

FROM gcr.io/distroless/base-debian12
COPY --from=build /out/ztnet-dns /usr/local/bin/ztnet-dns
ENTRYPOINT ["/usr/local/bin/ztnet-dns"]
