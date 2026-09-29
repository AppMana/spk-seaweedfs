#!/usr/bin/env bash
# Rebuild the DSM-only emulator and prove RED/GREEN before producing its image.
set -euo pipefail
umask 077
[[ $# == 2 ]] || { echo 'usage: build-qemu.sh EXISTING_OUTPUT_PARENT IMAGE_TAG' >&2; exit 2; }
parent=$(realpath -e -- "$1")
[[ -d "$parent" ]]
image=$2
script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
run=$(mktemp -d "$parent/dsm-qemu-build.XXXXXXXX")
revision=bb779689554ca5e69e0ac0bb56f90c41c64a80bb
git clone --depth 1 --branch v10.0.8 https://github.com/qemu/qemu.git "$run/source"
test "$(git -C "$run/source" rev-parse HEAD)" = "$revision"
git -C "$run/source" apply --check "$script_dir/qemu-ncq-regression.patch"
git -C "$run/source" apply "$script_dir/qemu-ncq-regression.patch"
cd "$run/source"
./configure --without-default-features --target-list=x86_64-softmmu \
  --enable-kvm --enable-tcg --enable-tools --enable-pixman \
  --disable-docs --disable-werror --disable-debug-info > "$run/configure.log" 2>&1
ninja -C build -j "${QEMU_BUILD_JOBS:-8}" qemu-system-x86_64 qemu-img tests/qtest/ahci-test > "$run/build.log" 2>&1
export QTEST_QEMU_BINARY="$run/source/build/qemu-system-x86_64"
export QTEST_QEMU_IMG="$run/source/build/qemu-img"
testbin="$run/source/build/tests/qtest/ahci-test"
timeout 30 "$testbin" -p /x86_64/ahci/io/ncq/simple > "$run/baseline-control.log" 2>&1
if timeout 30 "$testbin" -p /x86_64/ahci/io/ncq/after-smart-error > "$run/red.log" 2>&1; then
  echo 'expected upstream regression failure, got success' >&2
  exit 1
fi
# Reject missing dependencies, empty selection, timeout and unrelated failures.
grep -q 'not ok /x86_64/ahci/io/ncq/after-smart-error' "$run/red.log"
grep -q 'test_ncq_after_smart_error: assertion failed' "$run/red.log"
git apply --check "$script_dir/qemu-ncq-fix.patch"
git apply "$script_dir/qemu-ncq-fix.patch"
ninja -C build -j "${QEMU_BUILD_JOBS:-8}" qemu-system-x86_64 > "$run/rebuild.log" 2>&1
timeout 300 "$testbin" > "$run/ahci-full.log" 2>&1
test "$(grep -c '^ok ' "$run/ahci-full.log")" = 76
! grep -Eq '^not ok |# SKIP' "$run/ahci-full.log"
sha256sum "$QTEST_QEMU_BINARY" "$script_dir/qemu-ncq-regression.patch" "$script_dir/qemu-ncq-fix.patch" > "$run/inputs.sha256"
docker build --network none --pull=false -f "$script_dir/Dockerfile" -t "$image-wrapper" "$script_dir"
docker build --network none --pull=false --build-arg "DSM_BASE=$image-wrapper" \
  --build-context "qemu-build=$run/source/build" --build-context "qemu-source=$run/source" \
  -f "$script_dir/Dockerfile.qemu" -t "$image" "$script_dir"
docker run --rm --network none --entrypoint /usr/local/bin/qemu-system-x86_64 "$image" --version
docker image inspect --format '{{.Id}}' "$image" > "$run/image-id"
printf 'DSM_QEMU_BUILD_VERIFIED:%s\n' "$run"
