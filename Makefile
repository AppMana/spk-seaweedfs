## spk-seaweedfs build orchestrator
##
## Authority lives at the top level of this repo (cmd/, cross/, diyspk/).
## At build time we stage these into the spksrc submodule (spksrc/cross/,
## spksrc/diyspk/) so spksrc's relative include paths resolve. The
## submodule itself stays pristine; staged paths are gitignored on the
## submodule side and overwritten on every `make stage`.

REPO_ROOT       := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
SPKSRC_DIR      := $(REPO_ROOT)/spksrc
GO              ?= go
TARGET_ARCH     ?= x86_64
TARGET_DSM      ?= 7.2
SPK_TARGET      := arch-$(TARGET_ARCH)-$(TARGET_DSM)

BOOTSTRAP_BIN   := $(REPO_ROOT)/diyspk/seaweedfs/src/bin/synology-volume-bootstrap
BOOTSTRAP_SRC   := $(shell find $(REPO_ROOT)/cmd/synology-volume-bootstrap -name '*.go') \
                   $(REPO_ROOT)/cmd/synology-volume-bootstrap/go.mod \
                   $(REPO_ROOT)/cmd/synology-volume-bootstrap/go.sum

STAGE_TARGETS   := $(SPKSRC_DIR)/cross/seaweedfs \
                   $(SPKSRC_DIR)/diyspk/seaweedfs

.PHONY: all spk stage clean-stage bootstrap test-bootstrap

all: spk

# Build the bootstrap binary with the host Go.
# Pure Go, no cgo, statically linked. Output lives inside the diyspk
# tree so spksrc copies it into the package via POST_STRIP_TARGET.
bootstrap: $(BOOTSTRAP_BIN)

$(BOOTSTRAP_BIN): $(BOOTSTRAP_SRC)
	@mkdir -p $(dir $@)
	cd $(REPO_ROOT)/cmd/synology-volume-bootstrap && \
	  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	  $(GO) build -ldflags "-s -w" -trimpath -o $@ ./

test-bootstrap:
	cd $(REPO_ROOT)/cmd/synology-volume-bootstrap && $(GO) test ./...

# Stage our authoritative source into the spksrc submodule.
# Use rsync --delete so removing files locally also removes them from
# the staged copy. The staged paths are git-ignored under spksrc/.
stage: bootstrap
	@for d in $(STAGE_TARGETS); do mkdir -p $$d; done
	rsync -a --delete $(REPO_ROOT)/cross/seaweedfs/ $(SPKSRC_DIR)/cross/seaweedfs/
	rsync -a --delete $(REPO_ROOT)/diyspk/seaweedfs/ $(SPKSRC_DIR)/diyspk/seaweedfs/

# Build the SPK. Delegates to spksrc's per-arch target.
spk: stage
	$(MAKE) -C $(SPKSRC_DIR)/diyspk/seaweedfs $(SPK_TARGET)

# Surface the produced .spk file path.
.PHONY: show-spk
show-spk:
	@find $(SPKSRC_DIR)/packages -name 'seaweedfs_*.spk' -printf '%p\n' 2>/dev/null

clean-stage:
	rm -rf $(SPKSRC_DIR)/cross/seaweedfs $(SPKSRC_DIR)/diyspk/seaweedfs

.PHONY: clean
clean: clean-stage
	rm -f $(BOOTSTRAP_BIN)
	$(MAKE) -C $(SPKSRC_DIR) clean 2>/dev/null || true
