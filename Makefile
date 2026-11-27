GO ?= go
BIN := linkding-wishlist-ssg
GO_FILES := $(shell find . -name '*.go' -not -path './.git/*')

DEPLOY_HOST ?= myriad-docker
DEPLOY_DIR  ?= /home/lmorchard/docker/linkding-wishlist-ssg

.PHONY: all build build-linux site test fmt fmt-check vet check clean deploy-bin deploy-site deploy

all: build

build:
	$(GO) build -o $(BIN) ./cmd/linkding-wishlist-ssg

build-linux:
	GOOS=linux GOARCH=amd64 $(GO) build -o $(BIN)-linux-amd64 ./cmd/linkding-wishlist-ssg

site: build
	./$(BIN) build --store ./wishlist.db --cache-thumbs --out ./dist

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
	rm -rf build dist $(BIN) $(BIN)-linux-amd64

deploy-bin: build-linux
	scp $(BIN)-linux-amd64 $(DEPLOY_HOST):$(DEPLOY_DIR)/$(BIN).new
	ssh $(DEPLOY_HOST) "mv $(DEPLOY_DIR)/$(BIN).new $(DEPLOY_DIR)/$(BIN) && chmod +x $(DEPLOY_DIR)/$(BIN) && systemctl --user restart linkding-wishlist-preview.service"
	rm -f $(BIN)-linux-amd64
	rsync -avz ./data/ $(DEPLOY_HOST):$(DEPLOY_DIR)/data/

deploy-site:
	ssh $(DEPLOY_HOST) "cd $(DEPLOY_DIR) && ./$(BIN) build --store ./wishlist.db --cache-thumbs --out ./dist"

deploy: deploy-bin deploy-site
