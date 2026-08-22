# syntax=docker/dockerfile:1.7

ARG NODE_IMAGE=node:22.18.0-alpine3.22
ARG GO_IMAGE=golang:1.24.6-alpine3.22
ARG RUNTIME_IMAGE=alpine:3.22

FROM ${NODE_IMAGE} AS web-build
WORKDIR /src
ENV NODE_OPTIONS=--max-old-space-size=2048
COPY web/package.json web/package-lock.json ./web/
RUN npm --prefix web ci
COPY web/index.html web/tsconfig.json web/vite.config.ts ./web/
COPY web/src ./web/src
RUN npm --prefix web test -- --run \
    && npm --prefix web run build

FROM ${GO_IMAGE} AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
COPY --from=web-build /src/web/dist ./web/dist
# Replace the tracked clean-checkout placeholder only inside this build stage.
RUN rm -rf ./internal/staticweb/assets \
    && mkdir -p ./internal/staticweb/assets \
    && cp -a ./web/dist/. ./internal/staticweb/assets/ \
    && test -f ./internal/staticweb/assets/index.html \
    && ! grep -q "AISummoner WebUI assets were not included" ./internal/staticweb/assets/index.html
RUN CGO_ENABLED=0 GOMAXPROCS=2 go build -p 2 -trimpath -ldflags="-s -w" \
    -o /out/aisummoner-server ./cmd/aisummoner-server \
    && CGO_ENABLED=0 GOMAXPROCS=2 go build -p 2 -trimpath -ldflags="-s -w" \
    -o /out/aisummoner-client ./cmd/aisummoner-client

FROM ${RUNTIME_IMAGE} AS server
RUN apk add --no-cache ca-certificates tzdata \
    && mkdir -p /var/lib/aisummoner/data /var/lib/aisummoner/workspaces \
    && chown -R 10001:10001 /var/lib/aisummoner
COPY --from=go-build /out/aisummoner-server /usr/local/bin/aisummoner-server
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/aisummoner-server"]

FROM scratch AS client-artifact
COPY --from=go-build /out/aisummoner-client /aisummoner-client
