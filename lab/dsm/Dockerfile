ARG VM_BASE=labcontainers/vm-ubuntu:csi-816233a
FROM ${VM_BASE}
# Reuse the matched runtime's vrnetlab and declared-interface implementation.
COPY launch-dsm.py /launch-dsm.py
ENTRYPOINT ["uv", "run", "/launch-dsm.py"]
