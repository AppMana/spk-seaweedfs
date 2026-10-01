SPK_NAME = seaweedfs
SPK_VERS = 4.47
SPK_REV = 15
SPK_ICON = src/seaweedfs.png

DEPENDS = cross/seaweedfs

UNSUPPORTED_ARCHS = $(PPC_ARCHS) $(ARMv5_ARCHS) $(ARMv7_ARCHS) $(ARMv7L_ARCHS) $(ARMv8_ARCHS) $(i686_ARCHS)
# Volume membership joining a Kubernetes-managed cluster is x86_64 only for v0.1.

MAINTAINER = appmana
DISPLAY_NAME = SeaweedFS Volume
DESCRIPTION = SeaweedFS volume server that joins an existing seaweedfs-operator-managed cluster. Reads cluster topology from the Kubernetes API via a service-account token, then runs the upstream weed volume daemon advertised on the LAN.
HOMEPAGE = https://github.com/seaweedfs/seaweedfs
LICENSE = Apache-2.0
CHANGELOG = "Lab qualification candidate: pinned SeaweedFS 4.47 reliability branch, large-volume offsets, and supervised volume daemon. Not approved for deployment until exact-artifact DSM lifecycle qualification passes."

WIZARDS_DIR = src/wizard

STARTABLE = yes
# Native dependency registration is performed by bin/register-service.sh
# as an administrator; DSM ignores custom units in start_dep_services.
SERVICE_USER = auto
SERVICE_SETUP = src/service-setup.sh
SSS_SCRIPT = src/start-stop-status.sh
SERVICE_WIZARD_SHARENAME = wizard_data_share
FWPORTS = src/seaweedfs.sc

POST_STRIP_TARGET = seaweedfs_extra_install
POST_SERVICE_TARGET = seaweedfs_systemd_install

include ../../mk/spksrc.spk.mk

.PHONY: seaweedfs_systemd_install
seaweedfs_systemd_install:
	install -d -m 755 $(DSM_CONF_DIR)/systemd
	install -m 644 src/pkg-seaweedfs-volume.service $(DSM_CONF_DIR)/systemd/pkg-seaweedfs-volume.service
	install -m 755 ../../mk/spksrc.service.start-stop-status $(DSM_SCRIPTS_DIR)/volume-control

# The bootstrap binary is built by the top-level Makefile (host Go,
# pure Go, GOOS=linux GOARCH=amd64) and staged at src/bin/. We install
# it next to weed plus the volume.yaml template the wizard substitutes.
.PHONY: seaweedfs_extra_install
seaweedfs_extra_install:
	install -m 755 -d $(STAGING_DIR)/bin
	install -m 755 src/bin/synology-volume-bootstrap $(STAGING_DIR)/bin/synology-volume-bootstrap
	install -m 755 src/run.sh $(STAGING_DIR)/bin/run.sh
	install -m 755 src/register-service.sh $(STAGING_DIR)/bin/register-service.sh
	install -m 755 -d $(STAGING_DIR)/var
	install -m 644 src/volume_template.yaml $(STAGING_DIR)/var/volume_template.yaml
