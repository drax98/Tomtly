FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod main.go devices.json ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /fakes .

# alpine rather than scratch: the device's entrypoint needs `ip` to route back through the router.
FROM alpine:3.22
COPY --from=build /fakes /fakes
