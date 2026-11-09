FROM node:22-alpine AS web
RUN corepack enable
WORKDIR /web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ .
RUN pnpm build

FROM golang:1.27-alpine AS service
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Only the end-to-end stack sets this, for an image that never ships. Left empty, as it is by default, the tests decide the build.
ARG SKIP_GO_TESTS=
RUN if [ -z "$SKIP_GO_TESTS" ]; then CGO_ENABLED=0 go test ./...; fi && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /home-control ./cmd/home-control
RUN mkdir -p /image/data && chown 65534:65534 /image/data

FROM scratch
COPY --from=service /home-control /home-control
COPY --from=service /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=service --chown=65534:65534 /image/data /data
COPY --from=web /web/dist /web
COPY sdk/ /sdk/
ENV CONFIG_DIR=/config SDK_DIR=/sdk DATA_DIR=/data WEB_DIR=/web
USER 65534:65534
EXPOSE 8099
HEALTHCHECK --interval=30s --timeout=6s --start-period=30s CMD ["/home-control", "healthcheck"]
ENTRYPOINT ["/home-control"]
