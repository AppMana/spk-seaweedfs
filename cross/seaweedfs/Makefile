PKG_NAME = seaweedfs
PKG_VERS = 4.23
PKG_EXT = tar.gz
PKG_DIST_NAME = $(PKG_VERS).$(PKG_EXT)
PKG_DIST_SITE = https://github.com/seaweedfs/seaweedfs/archive/refs/tags
PKG_DIST_FILE = $(PKG_NAME)-$(PKG_VERS).$(PKG_EXT)
PKG_DIR = $(PKG_NAME)-$(PKG_VERS)

BUILD_DEPENDS = native/go

UNSUPPORTED_ARCHS = $(PPC_ARCHS) $(ARMv5_ARCHS)

HOMEPAGE = https://github.com/seaweedfs/seaweedfs
COMMENT  = SeaweedFS is a fast distributed storage system for blobs, objects, files, and a data lake.
LICENSE  = Apache-2.0

GO_SRC_DIR = $(WORK_DIR)/$(PKG_DIR)
GO_BIN_DIR = $(GO_SRC_DIR)/weed/weed
COMPILE_TARGET = seaweedfs_compile

include ../../mk/spksrc.cross-go.mk

.PHONY: seaweedfs_compile
seaweedfs_compile:
	@$(MSG) "- Build weed binary"
	@cd $(GO_SRC_DIR) && env $(ENV) go build \
		-ldflags "-s -w -X github.com/seaweedfs/seaweedfs/weed/util/version.COMMIT=$(PKG_VERS)" \
		-buildvcs=false \
		-o $(GO_BIN_DIR) \
		./weed
