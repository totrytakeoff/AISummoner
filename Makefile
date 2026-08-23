.PHONY: build build-server build-client test test-race run run-client web-install web-test web-build compose-config docker-build client-artifact client-appimage remote-ui remote-ui-test remote-appimage

GOMAXPROCS ?= 2
GOFLAGS ?= -p=2
CLIENT_BINARY ?= dist/aisummoner-client
APPIMAGETOOL ?= appimagetool
APPIMAGE_RUNTIME_FILE ?=
APPIMAGE_OUTPUT ?= dist/AISummoner-Client-0.1.0-x86_64.AppImage
REMOTE_UI_BUILD ?= build/remote-client
REMOTE_APPIMAGE_OUTPUT ?= dist/AISummoner-Remote-0.1.0-x86_64.AppImage

build:
	GOMAXPROCS=$(GOMAXPROCS) go build $(GOFLAGS) ./...

build-server:
	GOMAXPROCS=$(GOMAXPROCS) go build $(GOFLAGS) ./cmd/aisummoner-server

build-client:
	GOMAXPROCS=$(GOMAXPROCS) go build $(GOFLAGS) ./cmd/aisummoner-client

test:
	GOMAXPROCS=$(GOMAXPROCS) go test $(GOFLAGS) ./...

test-race:
	GOMAXPROCS=$(GOMAXPROCS) go test $(GOFLAGS) -race ./internal/...

run:
	go run ./cmd/aisummoner-server

run-client:
	go run ./cmd/aisummoner-client start --server http://127.0.0.1:8080 --dev

web-install:
	npm --prefix web ci

web-test:
	NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web test -- --run

web-build:
	NODE_OPTIONS=--max-old-space-size=2048 npm --prefix web run build

compose-config:
	sh deploy/validate-compose.sh

docker-build:
	docker build -f deploy/OpenCode.Dockerfile -t aisummoner-opencode:1.18.11 .
	docker build -f deploy/Dockerfile --target server -t aisummoner-server:mvp0 .

client-artifact:
	docker build -f deploy/Dockerfile --target client-artifact --output type=local,dest=dist .

client-appimage:
	APPIMAGE_RUNTIME_FILE="$(APPIMAGE_RUNTIME_FILE)" ./deploy/build-client-appimage.sh "$(CLIENT_BINARY)" "$(APPIMAGE_OUTPUT)" "$(APPIMAGETOOL)"

remote-ui:
	cmake -S desktop/remote-client -B "$(REMOTE_UI_BUILD)" -DCMAKE_BUILD_TYPE=RelWithDebInfo -DBUILD_TESTING=ON
	cmake --build "$(REMOTE_UI_BUILD)" --parallel 2

remote-ui-test: remote-ui
	QT_QPA_PLATFORM=offscreen ctest --test-dir "$(REMOTE_UI_BUILD)" --output-on-failure

remote-appimage:
	./deploy/build-remote-client-appimage.sh "$(REMOTE_APPIMAGE_OUTPUT)"
