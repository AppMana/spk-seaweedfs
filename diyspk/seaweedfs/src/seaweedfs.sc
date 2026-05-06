[seaweedfs-volume-http]
title="SeaweedFS Volume HTTP"
desc="SeaweedFS volume server data plane (read/write)"
port_forward="no"
dst.ports="8080/tcp"

[seaweedfs-volume-grpc]
title="SeaweedFS Volume gRPC"
desc="SeaweedFS volume server gRPC (master heartbeat + replication)"
port_forward="no"
dst.ports="18080/tcp"
