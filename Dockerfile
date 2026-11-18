# syntax=docker/dockerfile:1

# Stage 1: build the React interface into web/dist, which the Go binary
# embeds.
FROM node:22-bookworm-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json* web/.npmrc ./
RUN if [ -f package-lock.json ]; then npm ci; else npm install; fi
COPY web/ ./
RUN npm run build

# Stage 2: export the built interface and the lockfile to the host with
# `docker build --target web-export --output web .`, for machines without a
# working npm setup.
FROM scratch AS web-export
COPY --from=web /web/dist /dist
COPY --from=web /web/package-lock.json /package-lock.json

# Stage 3: build the server as a static binary. MEGA is reached through
# the Go packages of mega-cli, which are compiled in.
FROM golang:1.27-bookworm AS build
ARG VERSION=dev
ENV CGO_ENABLED=0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY internal/ ./internal/
COPY web/embed.go ./web/embed.go
COPY --from=web /web/dist ./web/dist
RUN go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/athenaeum .

# Stage 4: runtime with calibre, whose ebook-convert and ebook-meta do the
# format conversion and metadata embedding.
FROM debian:trixie-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends calibre ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /data /kindle
COPY --from=build /out/athenaeum /usr/local/bin/

# The list of libraries and the first library, with its database, which
# holds the MEGA session encrypted, live in /data. Other libraries are
# opened in the interface by their path inside the container, so mount
# the folders they should live in. Set ATHENAEUM_SECRET to keep the key
# that protects the session out of that volume; otherwise it is a file
# in the library. The Kindle is
# expected to be mounted at /kindle. Qt runs without a display, and its
# browser engine, used for PDF output, cannot sandbox itself as root.
ENV ATHENAEUM_ADDR=:8080 \
    ATHENAEUM_HOME=/data \
    ATHENAEUM_KINDLE_PATH=/kindle \
    QT_QPA_PLATFORM=offscreen \
    QTWEBENGINE_CHROMIUM_FLAGS=--no-sandbox
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/athenaeum", "-healthcheck"]
ENTRYPOINT ["/usr/local/bin/athenaeum"]
