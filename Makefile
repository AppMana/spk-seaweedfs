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
TARGET_ARCH     ?= x64
TARGET_DSM      ?= 7.2
SPK_TARGET      := arch-$(TARGET_ARCH)-$(TARGET_DSM)

BOOTSTRAP_BIN   := $(REPO_ROOT)/diyspk/seaweedfs/src/bin/synology-volume-bootstrap
BOOTSTRAP_SRC   := $(shell find $(REPO_ROOT)/cmd/synology-volume-bootstrap -name '*.go') \
                   $(REPO_ROOT)/cmd/synology-volume-bootstrap/go.mod \
                   $(REPO_ROOT)/cmd/synology-volume-bootstrap/go.sum

STAGE_TARGETS   := $(SPKSRC_DIR)/cross/seaweedfs \
                   $(SPKSRC_DIR)/diyspk/seaweedfs

.PHONY: all spk stage clean-stage bootstrap test test-bootstrap test-supervisor test-package-source test-package-metadata

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
	@log=$$(mktemp /tmp/synology-bootstrap-tests.XXXXXXXX); python3 $(REPO_ROOT)/tests/bootstrap_suite.py --log "$$log"

test-supervisor:
	sh $(REPO_ROOT)/tests/run-supervisor.sh
	sh $(REPO_ROOT)/tests/run-bootstrap-recovery.sh

test-package-source:
	sh $(REPO_ROOT)/tests/package-source.sh

test-package-metadata:
	sh $(REPO_ROOT)/tests/package-metadata.sh

.PHONY: test-runner-contracts
test-runner-contracts:
	python3 -m unittest discover -s $(REPO_ROOT)/tests -p 'test_*.py' -v

.PHONY: test-native test-dsm-live test-dsm-storage test-dsm-package test-dsm-kubernetes
test-native:
	cd $(REPO_ROOT)/lab/native && $(GO) test -race -run '^TestDSM' ./...

# Requires prepared private disks and explicit matched runtime/image inputs.
test-dsm-live:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 14m -run '^TestLiveDSMPrivateBoot$$' ./...

test-dsm-storage:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 14m -run '^TestLiveDSMStorageIntegrity$$' ./...

test-dsm-package:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 34m -run '^TestLiveDSMPackageInstallUpgrade$$' ./...

.PHONY: test-dsm-fresh test-dsm-retention
test-dsm-fresh:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 24m -run '^TestLiveDSMPackageFreshInstall$$' ./...

test-dsm-retention:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 24m -run '^TestLiveDSMPackageUninstallRetention$$' ./...

test-dsm-kubernetes:
	@test -n "$$DSM_PRIVATE_DIR" && test -n "$$DSM_VM_IMAGE" && test -n "$$DSM_PEER_IMAGE" && test -n "$$LABCONTAINERS_LABD" && test -n "$$DSM_KUBERNETES_INPUTS"
	cd $(REPO_ROOT)/lab/native && $(GO) test -v -count=1 -timeout 30m -run '^TestLiveDSMKubernetesJoin$$' ./...

test: test-bootstrap test-supervisor test-package-source test-package-metadata test-runner-contracts test-native

# Stage our authoritative source into the spksrc submodule. Recreate each
# destination so deleted source files cannot linger in the package context.
stage: bootstrap
	rm -rf $(STAGE_TARGETS)
	mkdir -p $(SPKSRC_DIR)/cross $(SPKSRC_DIR)/diyspk
	cp -a $(REPO_ROOT)/cross/seaweedfs $(SPKSRC_DIR)/cross/seaweedfs
	cp -a $(REPO_ROOT)/diyspk/seaweedfs $(SPKSRC_DIR)/diyspk/seaweedfs

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
