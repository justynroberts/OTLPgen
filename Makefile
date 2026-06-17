VERSION ?= v0.1.0
LDFLAGS := -s -w -X main.version=$(VERSION)
# -trimpath strips absolute build paths so binaries are bit-reproducible:
# rebuilding the same source yields identical bytes (a clean `git status`),
# which makes "is dist/ current?" answerable without spurious diffs.
BUILDFLAGS := -trimpath -ldflags "$(LDFLAGS)"
BIN     := otlpgen
DIST    := dist

PLATFORMS := \
	darwin/arm64 \
	darwin/amd64 \
	linux/amd64 \
	linux/arm64 \
	windows/amd64

.PHONY: build run print-config tidy clean dist

build:
	go build $(BUILDFLAGS) -o $(BIN) .

run: build
	./$(BIN) --one-shot

print-config: build
	./$(BIN) --print-config

tidy:
	go mod tidy

# Cross-compile a binary for every target platform into dist/.
dist:
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out=$(DIST)/$(BIN)-$$os-$$arch$$ext; \
		echo "building $$out"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 \
			go build $(BUILDFLAGS) -o $$out . || exit 1; \
	done
	@echo "done -> $(DIST)/"

clean:
	rm -rf $(BIN) $(DIST)
