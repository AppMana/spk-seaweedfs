PATH="${SYNOPKG_PKGDEST}/bin:${PATH}"

VOLUME_YAML="${SYNOPKG_PKGVAR}/volume.yaml"
KUBE_DIR="${SYNOPKG_PKGVAR}/kube"
TLS_DIR="${SYNOPKG_PKGVAR}/tls"
RUN_DIR="${SYNOPKG_PKGVAR}/run"
ARGV_FILE="${RUN_DIR}/argv"
LOG_FILE="${SYNOPKG_PKGVAR}/log/weed.log"

BOOTSTRAP_BIN="${SYNOPKG_PKGDEST}/bin/synology-volume-bootstrap"
WEED_BIN="${SYNOPKG_PKGDEST}/bin/weed"

SVC_BACKGROUND=y
SVC_WRITE_PID=y

# Render argv via the bootstrap, then exec weed volume with the result.
SERVICE_COMMAND="/bin/sh -c 'set -e; \
  \"${BOOTSTRAP_BIN}\" --config \"${VOLUME_YAML}\" --out \"${ARGV_FILE}\" --tls-dir \"${TLS_DIR}\" >> \"${LOG_FILE}\" 2>&1; \
  set --; while IFS= read -r line; do [ -n \"\$line\" ] && set -- \"\$@\" \"\$line\"; done < \"${ARGV_FILE}\"; \
  exec \"${WEED_BIN}\" volume \"\$@\" >> \"${LOG_FILE}\" 2>&1'"

service_postinst() {
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}"
    install -d -m 755 "${SYNOPKG_PKGVAR}/log"
    : > "${LOG_FILE}"

    if [ "${SYNOPKG_PKG_STATUS}" = "INSTALL" ]; then
        # Persist the bearer token to a 0600 file owned by the package user.
        # The token is the only sensitive wizard input; everything else is
        # plain config. Apiserver CA can be installed manually post-install
        # if your cluster uses a private CA.
        umask 077
        printf '%s' "${wizard_token}" > "${KUBE_DIR}/token"
        chown "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}/token"
        chmod 600 "${KUBE_DIR}/token"
        umask 022

        # Create an empty CA file so the kube client can be pointed at it
        # uniformly; user replaces with real PEM via SSH if needed.
        if [ ! -f "${KUBE_DIR}/ca.crt" ]; then
            : > "${KUBE_DIR}/ca.crt"
            chown "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}/ca.crt"
            chmod 644 "${KUBE_DIR}/ca.crt"
        fi

        # Resolve wizard inputs and substitute into volume.yaml.
        ADVERTISE_IP="${wizard_advertise_ip}"
        if [ -z "${ADVERTISE_IP}" ]; then
            ADVERTISE_IP="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)"
        fi
        RACK="${wizard_rack}"
        if [ -z "${RACK}" ]; then
            RACK="$(hostname -s 2>/dev/null || hostname)"
        fi
        INSECURE="false"
        if [ "${wizard_insecure}" = "true" ]; then
            INSECURE="true"
        fi

        sed \
            -e "s|@APISERVER@|${wizard_apiserver}|g" \
            -e "s|@PKGVAR@|${SYNOPKG_PKGVAR}|g" \
            -e "s|@INSECURE@|${INSECURE}|g" \
            -e "s|@NAMESPACE@|${wizard_namespace}|g" \
            -e "s|@SEAWEED_NAME@|${wizard_seaweed_name}|g" \
            -e "s|@SHARE_PATH@|${SHARE_PATH}|g" \
            -e "s|@ADVERTISE_IP@|${ADVERTISE_IP}|g" \
            -e "s|@HTTP_PORT@|8080|g" \
            -e "s|@GRPC_PORT@|18080|g" \
            -e "s|@DATACENTER@|${wizard_datacenter}|g" \
            -e "s|@RACK@|${RACK}|g" \
            -e "s|@MAX_VOLUMES@|${wizard_max_volumes}|g" \
            -e "s|@DISK_TYPE@|${wizard_disk_type}|g" \
            -e "s|@MTLS_SECRET@|${wizard_mtls_secret}|g" \
            "${SYNOPKG_PKGDEST}/var/volume_template.yaml" > "${VOLUME_YAML}"

        chown "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${VOLUME_YAML}"
        chmod 600 "${VOLUME_YAML}"
    fi
}

service_preuninst() {
    # volume.yaml + kube credentials are intentionally preserved across
    # uninstall so a reinstall picks them back up. Wipe by hand if the
    # admin wants a clean slate.
    :
}

service_postupgrade() {
    # Recreate state directories in case ownership/perms drifted.
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}"
}
