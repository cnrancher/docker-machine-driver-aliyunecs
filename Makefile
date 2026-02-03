DRIVER_NAME ?= docker-machine-driver-aliyunecs
DIST_DIR    ?= dist
DOCKERFILE ?= Dockerfile
BUILDER    ?= local-builder

TARGETS ?= \
  linux/amd64 \
  linux/arm64 \
  darwin/amd64 \
  darwin/arm64 \
  windows/amd64

GO ?= go
LDFLAGS ?= -s -w
CGO_ENABLED ?= 0

DIST_BINS := $(foreach t,$(TARGETS),$(DIST_DIR)/$(DRIVER_NAME).$(subst /,-,$(t)))

.DEFAULT_GOAL := docker-dist

.PHONY: dist clean list tidy vet docker-ensure-builder docker-clean docker-dist docker-dist-flat

dist: tidy vet $(DIST_BINS)
	@echo "All binaries are in $(DIST_DIR)/"
	@ls -la $(DIST_DIR)

list:
	@echo "Targets:"
	@$(foreach t,$(TARGETS),echo "  - $(t)";)

tidy:
	@$(GO) mod tidy

vet:
	@$(GO) vet ./...

clean:
	@rm -rf $(DIST_DIR)

$(DIST_DIR)/$(DRIVER_NAME).%:
	@mkdir -p $(DIST_DIR)
	@os="$(word 1,$(subst -, ,$*))"; \
	arch="$(word 2,$(subst -, ,$*))"; \
	echo "==> Building $(DRIVER_NAME) for $$os/$$arch"; \
	CGO_ENABLED=$(CGO_ENABLED) GOOS="$$os" GOARCH="$$arch" \
	  $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$@" .; \
	tar -czf "$(DIST_DIR)/$(DRIVER_NAME)_$$os-$$arch.tgz" -C "$(DIST_DIR)" "$(notdir $@)"

docker-ensure-builder:
	@docker buildx inspect "$(BUILDER)" >/dev/null 2>&1 || docker buildx create --name "$(BUILDER)" --use >/dev/null
	@docker buildx use "$(BUILDER)" >/dev/null

docker-clean:
	@rm -rf "$(DIST_DIR)"

docker-dist: docker-ensure-builder
	@mkdir -p "$(DIST_DIR)"
	@set -e; \
	for t in $(TARGETS); do \
	  os="$${t%/*}"; arch="$${t#*/}"; \
	  echo "==> [docker] Building $(DRIVER_NAME) for $$os/$$arch via $(DOCKERFILE)"; \
	  docker buildx build \
	    -f "$(DOCKERFILE)" \
	    --target artifact \
	    --build-arg GOOS="$$os" \
	    --build-arg GOARCH="$$arch" \
	    --build-arg DRIVER_NAME="$(DRIVER_NAME)" \
	    --output "type=local,dest=$(DIST_DIR)/$$os-$$arch" \
	    . ; \
	  ls -la "$(DIST_DIR)/$$os-$$arch/out/bin" || true; \
	done
	@echo "Done. Outputs under $(DIST_DIR)/*/out/bin/"

docker-dist-flat: docker-dist
	@echo "==> [docker] Flatten artifacts into $(DIST_DIR)/"
	@set -e; \
	find "$(DIST_DIR)" -type f -path "*/out/bin/*" -maxdepth 6 -print -exec cp -v {} "$(DIST_DIR)/" \;
	@echo "Flatten done. Files in $(DIST_DIR)/:"
	@ls -la "$(DIST_DIR)"

