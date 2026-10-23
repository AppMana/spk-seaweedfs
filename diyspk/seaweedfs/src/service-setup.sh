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
# from volume.yaml (volume.servers, or volume.instances; default 1), as the
# bootstrap counts them; the actual logic lives in run.sh, which does its
# own quoting once it's executing. An unreadable configuration starts one
# line, whose bootstrap then reports the error in weed.log.
INSTANCES=$("${SYNOPKG_PKGDEST}/bin/synology-volume-bootstrap" --config "${VOLUME_YAML}" --print-server-count 2>/dev/null) || INSTANCES=1
case "${INSTANCES}" in
    ''|*[!0-9]*|0) INSTANCES=1 ;;
esac
SERVICE_COMMAND="${SYNOPKG_PKGDEST}/bin/run.sh 0"
i=1
while [ "$i" -lt "${INSTANCES}" ]; do
    SERVICE_COMMAND="${SERVICE_COMMAND}
${SYNOPKG_PKGDEST}/bin/run.sh $i"
    i=$((i + 1))
done

# Limits are owned by conf/systemd/pkg-seaweedfs-volume.service. DSM acquires
# that unit as a package resource; these unprivileged hooks cannot edit /etc.

# The wizard's "Volume servers" step (wizard/servers-step.sh) asks, for each
# DSM volume, whether to run a server there, from which directory and with
# what memory budget (auto, or MiB). Checked rows become volume.servers, in
# volume order; servers that stay keep their ports. Nothing checked keeps
# volume.yaml as is. register-service.sh (administrator) then creates new
# directories and applies the memory limit derived from the installed memory.
apply_wizard_servers() {
    spec=
    for n in $(env | sed -n 's/^wizard_server_volume\([0-9][0-9]*\)=true$/\1/p' | sort -n); do
        eval "dir=\${wizard_server_volume${n}_dir:-}"
        eval "mb=\${wizard_server_volume${n}_memory_mb:-}"
        case "$dir" in
            *,*|*=*|*..*) echo "volume server directory $dir is not a plain path" >&2; return 1 ;;
            "/volume$n/"?*) ;;
            *) echo "volume server directory $dir is not on /volume$n" >&2; return 1 ;;
        esac
        case "$mb" in
            ''|auto) mb=auto ;;
            *[!0-9]*) echo "memory for $dir must be auto or a whole number of MiB" >&2; return 1 ;;
        esac
        spec="$spec${spec:+,}$dir=$mb"
    done
    [ -n "$spec" ] || return 0
    "${SYNOPKG_PKGDEST}/bin/synology-volume-bootstrap" --config "${VOLUME_YAML}" --set-servers "$spec"
}

service_prestart() {
    # Remove stale argv files so instances > 0 wait for THIS start's
    # bootstrap render instead of exec-ing against old master addresses.
    rm -f "${RUN_DIR}/argv" "${RUN_DIR}"/argv.* "${RUN_DIR}"/gomemlimit*
}

service_postinst() {
    install -d -m 700 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${KUBE_DIR}" "${TLS_DIR}" "${RUN_DIR}" "${OCI_DIR}"
    install -d -m 755 -o "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${SYNOPKG_PKGVAR}/log"
    : > "${LOG_FILE}"
    chown "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${LOG_FILE}"

    # DSM also reports INSTALL when reinstalling retained package state.
    # Never replace existing configuration or credentials with wizard defaults.
    if [ "${SYNOPKG_PKG_STATUS}" = "INSTALL" ] && [ ! -e "${VOLUME_YAML}" ] && [ ! -L "${VOLUME_YAML}" ]; then
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
        # The default server lives in an @ directory on the lowest-numbered
        # DSM volume; the wizard's "Volume servers" step can replace it.
        DATA_VOLUME=$(awk '$2 ~ /^\/volume[0-9]+$/ { sub(/^\/volume/, "", $2); print $2 }' "${SEAWEEDFS_MOUNTS:-/proc/mounts}" | sort -n | head -n 1)
        if [ -z "${DATA_VOLUME}" ]; then
            echo "no mounted DSM volume for the volume server's data" >&2
            return 1
        fi
        DATA_DIR="/volume${DATA_VOLUME}/@seaweedfs"
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
            -e "s|@DATA_DIR@|${DATA_DIR}|g" \
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
            "${SYNOPKG_PKGVAR}/volume_template.yaml" > "${VOLUME_YAML}"

        chown "${SC_USER:-sc-${SYNOPKG_PKGNAME}}" "${VOLUME_YAML}"
        chmod 600 "${VOLUME_YAML}"
        apply_wizard_servers
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
    apply_wizard_servers
}
