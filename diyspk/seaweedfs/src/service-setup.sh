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

# DSM starts package services with a 1024-descriptor soft limit and a
# 4096 HARD limit, so run.sh's `ulimit -n 65536` clamps to 4096 and there
# is nothing a package-level script can do about it: a process cannot
# raise its own hard limit. A volume server holds .dat + .idx + .ldb open
# per volume, so a few hundred volumes plus replication sockets exhausts
# it — that is the 2026-08-05 SIGSEGV and 359 under-replicated volumes.
#
# The hard limit is inherited from the systemd unit DSM generates for the
# package, so raise it there. LimitNOFILE sets soft AND hard, which is
# what actually lifts the 4096 ceiling. 65536 stays well under the
# default fs.nr_open (1048576); a value above fs.nr_open makes the unit
# fail to start, so do not raise this to `infinity`.
#
# MemoryMax/TasksMax are installed here too rather than documented as a
# manual `seaweedfs.slice.d/memory.conf` step. The manual step was never
# performed on appmana-017-ds, which is how two unbounded volume servers
# drove the NAS into global reclaim until it could no longer fork() —
# DSM's own web UI, sshd and smbd all died while the kernel-side NFS and
# iSCSI targets kept serving. A limit that depends on someone remembering
# to install it is not a limit.
install_resource_limits() {
    UNIT="pkgctl-${SYNOPKG_PKGNAME}.service"
    DROPIN_DIR="/etc/systemd/system/${UNIT}.d"

    command -v systemctl >/dev/null 2>&1 || return 0

    install -d -m 755 "${DROPIN_DIR}" || return 0
    cat > "${DROPIN_DIR}/appmana-limits.conf" <<'LIMITS'
# Installed by the seaweedfs package. /etc/systemd/system survives DSM
# updates; /usr/lib/systemd/system does not, so it must live here.
[Service]
LimitNOFILE=65536
MemoryAccounting=true
MemoryMax=5G
TasksMax=4096
LIMITS
    chmod 644 "${DROPIN_DIR}/appmana-limits.conf"

    systemctl daemon-reload >/dev/null 2>&1 || true
}

service_prestart() {
    # Remove stale argv files so instances > 0 wait for THIS start's
    # bootstrap render instead of exec-ing against old master addresses.
    rm -f "${RUN_DIR}/argv" "${RUN_DIR}"/argv.*
}

service_postinst() {
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}" "${OCI_DIR}"
    install -d -m 755 "${SYNOPKG_PKGVAR}/log"
    : > "${LOG_FILE}"

    install_resource_limits

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

    # Re-assert on upgrade: a DSM update can regenerate the package unit.
    install_resource_limits
}
