# syntax=docker/dockerfile:1.7

FROM node:22.18.0-bookworm-slim

ARG OPENCODE_VERSION=1.18.11

RUN npm install --global --omit=dev "opencode-ai@${OPENCODE_VERSION}" \
    && test "$(opencode --version)" = "${OPENCODE_VERSION}" \
    && npm cache clean --force \
    && useradd --uid 10001 --user-group --create-home --shell /usr/sbin/nologin opencode \
    && mkdir -p /var/lib/aisummoner/workspaces \
    && chown -R 10001:10001 /var/lib/aisummoner

USER 10001:10001
WORKDIR /var/lib/aisummoner/workspaces
ENTRYPOINT ["opencode"]
