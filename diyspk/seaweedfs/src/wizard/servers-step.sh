# Shared by install_uifile.sh and upgrade_uifile.sh (assembled by
# assemble-wizards.sh). Prints the "Volume servers" wizard step: one row per
# mounted DSM volume, with a checkbox, the server's directory and its memory
# budget, starting from the current configuration when it can be read.
# service-setup.sh turns the answers into volume.servers.

json_escape() {
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'
}

servers_step() {
    config=${SEAWEEDFS_WIZARD_CONFIG:-/var/packages/seaweedfs/var/volume.yaml}
    bootstrap=${SEAWEEDFS_WIZARD_BOOTSTRAP:-/var/packages/seaweedfs/target/bin/synology-volume-bootstrap}
    mounts=${SEAWEEDFS_WIZARD_MOUNTS:-/proc/mounts}
    tab=$(printf '\t')

    # dir<TAB>port<TAB>grpcPort<TAB>metricsPort<TAB>memoryMB per server. A
    # package older than volume.servers has no --print-servers; its one
    # server is volume.dir.
    current=$("$bootstrap" --config "$config" --print-servers 2>/dev/null) || current=
    if [ -z "$current" ] && [ -r "$config" ]; then
        legacy=$(sed -n 's/^  dir:[[:space:]]*//p' "$config" | head -n 1)
        [ -z "$legacy" ] || current="${legacy}${tab}8080${tab}18080${tab}0${tab}0"
    fi
    # New servers get an @-prefixed directory: DSM never registers those as
    # shared folders, while a plain top-level directory can be restored as a
    # share at mount and renamed on a name collision (seaweed -> seaweed_1).
    # An existing @-prefixed name is reused so all servers match.
    name=@seaweedfs
    first=$(printf '%s\n' "$current" | head -n 1 | cut -f1)
    case "$(basename "${first:-/x}")" in @?*) name=$(basename "$first") ;; esac

    checks= dirs= memory=
    for n in $(awk '$2 ~ /^\/volume[0-9]+$/ { sub(/^\/volume/, "", $2); print $2 }' "$mounts" | sort -n -u); do
        volume=/volume$n
        row=$(printf '%s\n' "$current" | awk -F "$tab" -v v="$volume/" 'index($1, v) == 1 { print; exit }')
        free=$(df -Pk "$volume" 2>/dev/null | awk 'NR == 2 { printf "%.1f TB free", $4 * 1024 / 1e12 }')
        if [ -n "$row" ]; then
            dir=$(printf '%s' "$row" | cut -f1)
            port=$(printf '%s' "$row" | cut -f2)
            mb=$(printf '%s' "$row" | cut -f5)
            [ "$mb" != 0 ] || mb=3072
            checked=true
            label="$volume: current server, port $port${free:+, $free}"
        else
            dir="$volume/$name"
            mb=1536
            checked=false
            label="$volume: new server${free:+, $free}"
        fi
        checks="$checks${checks:+,}
{\"key\": \"wizard_server_volume$n\", \"desc\": \"$(json_escape "$label")\", \"defaultValue\": $checked}"
        dirs="$dirs${dirs:+,}
{\"key\": \"wizard_server_volume${n}_dir\", \"desc\": \"Directory on $volume\", \"defaultValue\": \"$(json_escape "$dir")\",
 \"validator\": {\"allowBlank\": false, \"regex\": {\"expr\": \"/^\\\\/volume$n\\\\/[^\\\\/.][^\\\"]*$/\", \"errorText\": \"A directory on $volume.\"}}}"
        memory="$memory${memory:+,}
{\"key\": \"wizard_server_volume${n}_memory_mb\", \"desc\": \"Memory for the $volume server (MiB)\", \"defaultValue\": \"$mb\",
 \"validator\": {\"allowBlank\": false, \"regex\": {\"expr\": \"/^[0-9]{3,6}$/\", \"errorText\": \"Whole MiB, at least 256.\"}}}"
    done

    cat <<EOF
{
  "step_title": "Volume servers",
  "items": [
    {"desc": "Each checked DSM volume runs its own SeaweedFS volume server from the directory below, so its disk is a separate data node in the same rack. A server that stays keeps its ports and data; a new one takes unused ports. Unchecking a server stops it without deleting its directory. Leave every box unchecked to keep the current servers (on a fresh install, one server in @seaweedfs on the lowest-numbered volume)."},
    {"desc": "Memory is the server's Go budget; its upload and download limits follow from it. The package's memory limit becomes the sum of the budgets plus 1024 MiB of page cache per server. About 2 MB per volume held is typical (746 volumes measured 1.4 GB, peaking at 2.3 GB). After the wizard, run register-service.sh as an administrator to create new directories and apply the memory limit."},
    {"type": "multiselect", "subitems": [$checks
    ]},
    {"type": "textfield", "subitems": [$dirs
    ]},
    {"type": "textfield", "subitems": [$memory
    ]}
  ]
}
EOF
}
