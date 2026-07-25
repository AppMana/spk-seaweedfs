PATH="${SYNOPKG_PKGDEST}/bin:${PATH}"

VOLUME_YAML="${SYNOPKG_PKGVAR}/volume.yaml"
KUBE_DIR="${SYNOPKG_PKGVAR}/kube"
TLS_DIR="${SYNOPKG_PKGVAR}/tls"
RUN_DIR="${SYNOPKG_PKGVAR}/run"
OCI_DIR="${SYNOPKG_PKGVAR}/oci"
LOG_FILE="${SYNOPKG_PKGVAR}/log/weed.log"

SVC_BACKGROUND=y
SVC_WRITE_PID=y

# start-stop-status reads SERVICE_COMMAND with `read -r` PER LINE and
# runs each line UNQUOTED (`${service} >> "${OUT}" 2>&1 &`), appending
# each background PID to the PID file — so a multi-line value runs one
# daemon per line, and every line must stay free of shell
# metacharacters. One line (= one `weed volume` process) per instance
# from volume.yaml's volume.instances (default 1); the actual logic
# lives in run.sh, which does its own quoting once it's executing.
INSTANCES=$(sed -n 's/^[[:space:]]*instances:[[:space:]]*//p' "${VOLUME_YAML}" 2>/dev/null | head -1)
case "${INSTANCES}" in
    ''|*[!0-9]*) INSTANCES=1 ;;
esac
SERVICE_COMMAND="${SYNOPKG_PKGDEST}/bin/run.sh 0"
i=1
while [ "$i" -lt "${INSTANCES}" ]; do
    SERVICE_COMMAND="${SERVICE_COMMAND}
${SYNOPKG_PKGDEST}/bin/run.sh $i"
    i=$((i + 1))
done

service_prestart() {
    # Remove stale argv files so instances > 0 wait for THIS start's
    # bootstrap render instead of exec-ing against old master addresses.
    rm -f "${RUN_DIR}/argv" "${RUN_DIR}"/argv.*
}

service_postinst() {
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}" "${OCI_DIR}"
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
        WEED_PLAIN_HTTP="false"
        if [ "${wizard_weed_plain_http}" = "true" ]; then
            WEED_PLAIN_HTTP="true"
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
            -e "s|@WEED_IMAGE@|${wizard_weed_image}|g" \
            -e "s|@WEED_PLAIN_HTTP@|${WEED_PLAIN_HTTP}|g" \
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
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}" "${OCI_DIR}"
}
