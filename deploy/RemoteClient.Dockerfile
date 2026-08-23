# syntax=docker/dockerfile:1.7

# The GUI is compiled against Ubuntu 22.04's glibc/Qt baseline, then its Qt
# closure is bundled into the AppDir. The Go daemon is a separate static build.
ARG GO_IMAGE=golang@sha256:ab1d1823abb55a9504d2e3e003b75b36dbeb1cbcc4c92593d85a84ee46becc6c
ARG QT_BUILD_IMAGE=ubuntu@sha256:c7eb020043d8fc2ae0793fb35a37bff1cf33f156d4d4b12ccc7f3ef8706c38b1
ARG AISUMMONER_DEFAULT_SERVER_ORIGIN=https://122.51.70.33:10001

FROM ${GO_IMAGE} AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOMAXPROCS=2 go build -p 2 -trimpath -ldflags="-s -w" \
    -o /out/aisummoner-client ./cmd/aisummoner-client

FROM ${QT_BUILD_IMAGE} AS qt-compile
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
       build-essential cmake ninja-build patchelf file binutils libgl-dev \
       qt6-base-dev qt6-base-dev-tools \
    && rm -rf /var/lib/apt/lists/*
ARG AISUMMONER_DEFAULT_SERVER_ORIGIN
WORKDIR /src
COPY desktop/remote-client ./desktop/remote-client
RUN cmake -S desktop/remote-client -B /build/remote-client -G Ninja \
      -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTING=ON \
      -DAISUMMONER_DEFAULT_SERVER_ORIGIN="${AISUMMONER_DEFAULT_SERVER_ORIGIN}" \
    && cmake --build /build/remote-client --parallel 2 \
    && QT_QPA_PLATFORM=offscreen ctest --test-dir /build/remote-client --output-on-failure \
    && cmake --install /build/remote-client --prefix /out/AISummoner-Remote.AppDir/usr

FROM qt-compile AS appdir-build
COPY --from=go-build /out/aisummoner-client /out/AISummoner-Remote.AppDir/usr/bin/aisummoner-client
COPY deploy/appimage-qt /assets
COPY deploy/collect-qt-appdir.sh /usr/local/bin/collect-qt-appdir
RUN install -m 0755 /assets/AppRun /out/AISummoner-Remote.AppDir/AppRun \
    && install -d -m 0755 \
       /out/AISummoner-Remote.AppDir/usr/share/applications \
       /out/AISummoner-Remote.AppDir/usr/share/icons/hicolor/scalable/apps \
    && install -m 0644 /assets/aisummoner-remote.desktop \
       /out/AISummoner-Remote.AppDir/usr/share/applications/aisummoner-remote.desktop \
    && install -m 0644 /assets/aisummoner-remote.svg \
       /out/AISummoner-Remote.AppDir/usr/share/icons/hicolor/scalable/apps/aisummoner-remote.svg \
    && install -m 0644 /assets/qt.conf /out/AISummoner-Remote.AppDir/usr/bin/qt.conf \
    && ln -s usr/share/applications/aisummoner-remote.desktop \
       /out/AISummoner-Remote.AppDir/aisummoner-remote.desktop \
    && ln -s usr/share/icons/hicolor/scalable/apps/aisummoner-remote.svg \
       /out/AISummoner-Remote.AppDir/aisummoner-remote.svg \
    && ln -s aisummoner-remote.svg /out/AISummoner-Remote.AppDir/.DirIcon \
    && chmod 0755 /out/AISummoner-Remote.AppDir/usr/bin/aisummoner-client \
    && collect-qt-appdir /out/AISummoner-Remote.AppDir

FROM scratch AS remote-client-appdir
COPY --from=appdir-build /out/AISummoner-Remote.AppDir /
