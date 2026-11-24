GO ?= go
BIN := linkding-wishlist-ssg
GO_FILES := $(shell find . -name '*.go' -not -path './.git/*')

.PHONY: all build test fmt fmt-check vet check clean

all: build

build:
	$(GO) build -o $(BIN) ./cmd/linkding-wishlist-ssg

test:
	$(GO) test ./...

fmt:
	gofmt -w $(GO_FILES)

fmt-check:
	@files="$$(gofmt -l $(GO_FILES))"; \
	if [ -n "$$files" ]; then \
		echo "gofmt needed:"; \
		echo "$$files"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

check: fmt-check vet test

clean:
	rm -rf build $(BIN)
