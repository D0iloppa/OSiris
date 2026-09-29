#!/usr/bin/env bash
# Neo4j 제약/인덱스(DDL)만 적용한다. 카탈로그 시드(의도 데이터)는 여기서 하지 않는다
# — graph-write API가 만들어지기 전까지는 원칙적으로 시드할 방법이 없다(G7).
# osiris-up 이 Neo4j healthy 이후에 이 스크립트를 호출한다. 여러 번 실행해도 안전
# (전부 IF NOT EXISTS).
set -euo pipefail

CONTAINER="${OSIRIS_NEO4J_CONTAINER:-osiris-neo4j}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="$ROOT/.env"

if [ -f "$ENV_FILE" ]; then
  # shellcheck disable=SC1090
  source "$ENV_FILE"
fi

NEO4J_AUTH="${NEO4J_AUTH:-neo4j/CHANGE_ME_use_a_strong_password_min8}"
NEO4J_USER="${NEO4J_AUTH%%/*}"
NEO4J_PASS="${NEO4J_AUTH#*/}"

echo "[apply-schema] waiting for ${CONTAINER} to report healthy..."
tries=0
until [ "$(docker inspect -f '{{.State.Health.Status}}' "$CONTAINER" 2>/dev/null || echo starting)" = "healthy" ]; do
  tries=$((tries + 1))
  if [ "$tries" -gt 60 ]; then
    echo "[apply-schema] ERROR: ${CONTAINER} did not become healthy in time" >&2
    exit 1
  fi
  sleep 2
done

echo "[apply-schema] applying constraints/indexes from neo4j/init/constraints.cypher"
docker exec -i "$CONTAINER" cypher-shell -u "$NEO4J_USER" -p "$NEO4J_PASS" --format plain \
  < "$ROOT/neo4j/init/constraints.cypher"

echo "[apply-schema] done."
echo "[apply-schema] NOTE: 카탈로그 시드(Capability/Permission/PermissionProfile/AssignmentPolicy/OrgRevision=0)는"
echo "                     이 스크립트의 범위가 아니다 — osiris-core의 graph-write API 경유로만 써야 한다(D17)."
echo "                     자세한 내용은 docs/design/IMPLEMENTATION-NOTES.md 참조."
