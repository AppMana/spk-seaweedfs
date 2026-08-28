PKG_NAME = seaweedfs
PKG_VERS = 4.40-appmana.post.2
PKG_COMMIT = 358f47b1f817549b725e589b245acf9755b74f43
PKG_EXT = tar.gz
PKG_DIST_NAME = $(PKG_COMMIT).$(PKG_EXT)
PKG_DIST_SITE = https://github.com/AppMana/forks-seaweedfs/archive
PKG_DIST_FILE = $(PKG_NAME)-$(PKG_VERS).$(PKG_EXT)
PKG_DIR = forks-seaweedfs-$(PKG_COMMIT)

GO_FUSE_COMMIT = 1bdeec4d57d1e9ee85d4938f36f2ed876dd7bd5e
GO_FUSE_DIST_FILE = forks-go-fuse-$(GO_FUSE_COMMIT).tar.gz
GO_FUSE_DIST_SITE = https://github.com/AppMana/forks-go-fuse/archive/$(GO_FUSE_COMMIT).tar.gz
GO_FUSE_SHA256 = c6bea46abe20fe4ffe62fccb0e3e1f91b9d417c1e702cfe23cd03d46b2821b06

BUILD_DEPENDS = native/go

UNSUPPORTED_ARCHS = $(PPC_ARCHS) $(ARMv5_ARCHS)

HOMEPAGE = https://github.com/AppMana/forks-seaweedfs
COMMENT  = SeaweedFS is a fast distributed storage system for blobs, objects, files, and a data lake.
LICENSE  = Apache-2.0

GO_SRC_DIR = $(WORK_DIR)/$(PKG_DIR)
GO_BIN_DIR = $(GO_SRC_DIR)/weed/weed
POST_DOWNLOAD_TARGET = seaweedfs_download_go_fuse
POST_EXTRACT_TARGET = seaweedfs_extract_go_fuse
COMPILE_TARGET = seaweedfs_compile

include ../../mk/spksrc.cross-go.mk

.PHONY: seaweedfs_download_go_fuse seaweedfs_extract_go_fuse
seaweedfs_download_go_fuse: download_target
	@if [ ! -f "$(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE)" ]; then \
	  wget --secure-protocol=TLSv1_2 --timeout=30 --tries=3 --waitretry=15 \
	    --retry-connrefused --max-redirect=20 -nv \
	    -O "$(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE).part" "$(GO_FUSE_DIST_SITE)"; \
	  mv "$(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE).part" "$(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE)"; \
	fi
	@echo "$(GO_FUSE_SHA256)  $(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE)" | sha256sum -c -

seaweedfs_extract_go_fuse: extract_target
	@rm -rf "$(WORK_DIR)/forks-go-fuse" \
	  "$(WORK_DIR)/forks-go-fuse-$(GO_FUSE_COMMIT)"
	@tar -xzpf "$(DISTRIB_DIR)/$(GO_FUSE_DIST_FILE)" -C "$(WORK_DIR)"
	@mv "$(WORK_DIR)/forks-go-fuse-$(GO_FUSE_COMMIT)" "$(WORK_DIR)/forks-go-fuse"

# `5BytesOffset` is upstream's build tag for the large-volume variant.
# It swaps weed/util/constants_4bytes.go (30 GB per volume) for
# constants_5bytes.go (8000 GB per volume). Synology DAS volumes are
# routinely larger than 30 GB, so we always build with this tag.
.PHONY: seaweedfs_compile
seaweedfs_compile:
	@$(MSG) "- Build weed binary (5BytesOffset, 8000GB per volume)"
	@cd $(GO_SRC_DIR) && env $(ENV) go build \
		-tags 5BytesOffset \
		-ldflags "-s -w -X github.com/seaweedfs/seaweedfs/weed/util/version.COMMIT=$(PKG_VERS)" \
		-buildvcs=false \
		-o $(GO_BIN_DIR) \
		./weed
