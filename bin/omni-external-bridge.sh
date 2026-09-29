#!/usr/bin/env bash
# bin/omni-external-bridge.sh — omniroute.mode=external(k8s)용 호스트 브리지.
#
# k3d 클러스터 파드는 다른 docker 망(예: edge)의 컨테이너 이름을 풀지 못한다.
# 그래서 k3d 망에 **고정 IP** socat 컨테이너를 하나 두고, 그 컨테이너를 외부
# omniroute가 있는 망에도 붙여 이름으로 중계한다(외부 omniroute가 재생성돼 IP가
# 바뀌어도 무관). 차트는 values `omniroute.external.ip`에 이 고정 IP를 넣는다.
# 고정 대상 TCP 중계라 범용 프록시가 되지 못한다.
#
#   bin/omni-external-bridge.sh            # 생성(이미 있으면 그대로)
#   bin/omni-external-bridge.sh --remove   # 제거
#
# env(필수): BRIDGE_IP=<k3d 망 안의 비어 있는 고정 IP> TARGET_NET=<외부 omniroute가 붙은 docker 망>
# env(선택): BRIDGE_NET=k3d-osiris-dev TARGET_ADDR=omniroute:20128
set -euo pipefail
NAME=osiris-omni-bridge
BRIDGE_NET="${BRIDGE_NET:-k3d-osiris-dev}"; BRIDGE_IP="${BRIDGE_IP:-}"
TARGET_NET="${TARGET_NET:-}"; TARGET_ADDR="${TARGET_ADDR:-omniroute:20128}"

if [ "${1:-}" = "--remove" ]; then docker rm -f "$NAME"; exit 0; fi
[ -n "$BRIDGE_IP" ] && [ -n "$TARGET_NET" ] || { echo "BRIDGE_IP·TARGET_NET 을 지정하세요 (헤더 주석)"; exit 1; }
if docker inspect "$NAME" >/dev/null 2>&1; then echo "$NAME already exists"; exit 0; fi
docker run -d --name "$NAME" --restart unless-stopped \
  --network "$BRIDGE_NET" --ip "$BRIDGE_IP" --label osiris.role=omniroute-external-bridge \
  alpine/socat:1.8.0.1 "TCP-LISTEN:20128,fork,reuseaddr" "TCP:${TARGET_ADDR}"
docker network connect "$TARGET_NET" "$NAME"
echo "$NAME: ${BRIDGE_IP}:20128 -> ${TARGET_ADDR} (via ${TARGET_NET})"
