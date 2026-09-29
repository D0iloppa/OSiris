# OSiris Helm chart (Phase C-1 골격)

이 chart는 `docs/design/70-kubernetes-deployment.md`(K1~K13)를 구현한다.
그 문서가 결정을 소유하며, 이 chart는 그 결정을 렌더링만 한다 — 새 결정을
여기서 발명하지 않는다.

## 이번 phase(C-1)의 범위

`osiris-system`(core/api/orchestrator/omniroute/egress/neo4j/nats)과
`osiris-agents` 두 네임스페이스, 그리고 §2 배치도의 모든 워크로드를
템플릿으로 만든다. **다음은 이번 phase에 없다** — 다음 phase에서 채워진다:

- NetworkPolicy · Ingress (C-2)
- Agent CRD · RBAC · ServiceAccount · TokenReview 권한 (C-3)
- bootstrap-master / genesis 워크로드 (C-4)

## 설치 전 준비

1. 자격증명 값을 채운다(G5 — 평문을 이 저장소에 커밋하지 않는다):
   ```
   cp deploy/osiris/values.yaml deploy/osiris/values-secret.yaml   # 커밋 금지
   $EDITOR deploy/osiris/values-secret.yaml   # secrets.* 채우기
   helm install osiris deploy/osiris -f deploy/osiris/values-secret.yaml
   ```
   또는 `secrets.existingSecretName`으로 이미 클러스터에 있는 Secret을
   가리킨다(예: 외부 시크릿 매니저가 채운 것).

2. 기본값(단일 노드/미결정 토폴로지)은 이대로 설치하면 된다 — K4의
   nodeAffinity는 `global.pinCoreToMainNode: false`(기본값)일 때 렌더되지
   않는다. **멀티노드로 전환할 때만** 아래 절 참고.

## 멀티노드 전환 (K4 — 코어 노드 고정)

★ [2026-07-28 실배포 실측 — 결함 1] 이 chart는 처음부터 `osiris.dev/role=main`
라벨을 요구하는 required nodeAffinity를 항상 렌더했다. 그런데 그 라벨은
**chart가 붙일 수 없다** — 노드는 클러스터 스코프 객체이고 chart의 렌더
대상이 아니다. 그 결과 실측: 단일 노드 k3d 클러스터에 라벨 없이 기본
설치를 하면 neo4j-0/nats-0/osiris-core-0가 전부

```
FailedScheduling: 0/1 nodes are available: 1 node(s) didn't match Pod's
node affinity/selector.
```

로 `Pending`에 멈춘다. 그래서 이제 이 affinity는 `global.pinCoreToMainNode`
(기본 `false`)로 켜고 끈다 — 기본 설치는 affinity 없이 성공한다.

**멀티노드 클러스터로 갈 때는 반드시 이 순서를 지킨다:**

1. 실제 메인(코어를 고정할) 노드에 먼저 라벨을 붙인다:
   ```
   kubectl label node <메인-노드> osiris.dev/role=main
   ```
2. 그 다음에만 `global.pinCoreToMainNode=true`로 upgrade한다:
   ```
   helm upgrade osiris deploy/osiris --set global.pinCoreToMainNode=true \
     -f deploy/osiris/values-secret.yaml
   ```
   순서를 바꾸면(라벨보다 먼저 켜면) 결함 1과 똑같은 `FailedScheduling`이
   재발한다.

**K4의 대가(감추지 않는다)**: `pinCoreToMainNode=true`는 required
nodeAffinity이므로, 그 라벨이 붙은 노드(메인 노드)가 죽으면 osiris-core/
neo4j/nats **세 StatefulSet 모두 재스케줄되지 않는다** — 다른 노드가
아무리 여유 있어도 스케줄러가 그 노드를 고르지 않는다. 이것은 "코어의
상태 소유권(Neo4j가 유일한 진실)을 노드 분산이 아니라 노드 고정으로
지킨다"는 이 설계의 명시적 트레이드오프다(§0 K4) — 메인 노드를 잃으면
코어 3종과 함께 사실상 컨트롤 플레인 기능이 죽는다는 뜻이고, 그 사고
복구는 "다른 노드에 같은 라벨을 다시 붙이고 파드가 재스케줄되길
기다리는 것"이다(자동 페일오버 없음).

## Service 토폴로지 — 클라이언트용 ClusterIP + governing headless (결함 2)

★ [2026-07-28 실배포 실측] neo4j/nats/osiris-core/osiris-omniroute 네
StatefulSet은 이제 Service를 **두 개씩** 갖는다:

- `neo4j` / `nats` / `osiris-core` / `osiris-omniroute` — 일반 ClusterIP.
  클라이언트(osiris-core/osiris-api/osiris-orchestrator/osiris-omniroute
  자신)가 이 이름으로 붙는다. 이전에는 이 이름들이 headless
  (`clusterIP: None`)였는데, headless Service의 DNS는 **ready 엔드포인트가
  있을 때만** 이름을 반환한다 — 실측:
  ```
  $ nslookup neo4j.osiris-system.svc.cluster.local
  ** server can't find neo4j.osiris-system.svc.cluster.local: NXDOMAIN
  ```
  그 결과 neo4j-0이 아직 Ready가 아닐 때 osiris-core/osiris-orchestrator가
  "이름은 항상 풀리고 TCP만 거부된다"는 전제로 짠 재시도 루프에서 대신
  NXDOMAIN(`server misbehaving`)을 받아 `fatal: context canceled`로
  죽었다(둘 다 실측 로그). 그래서 이 네 이름은 이제 일반 ClusterIP다 —
  파드가 아직 안 떠도 이름은 항상 풀리고 TCP만 거부된다(재시도 루프가
  기대하는 정상 동작).
- `neo4j-headless` / `nats-headless` / `osiris-core-headless` /
  `osiris-omniroute-headless` — headless(`clusterIP: None`).
  StatefulSet.spec.serviceName 전용(안정적 파드별 DNS를 위해 k8s가
  요구하는 governing Service) — 클라이언트는 이 이름으로 절대 붙지 않는다.

`10-core-architecture.md` §1.2 불변자산 10번(에이전트가 아는 호스트명은
`nats`/`osiris-core`/`osiris-egress` 정확히 셋)은 이 분리로 지켜진다 —
그 세 이름은 여전히 클라이언트용 ClusterIP Service의 이름이다(`osiris-
egress`는 애초에 Deployment라 headless 문제 자체가 없었다).

**기존 설치를 업그레이드할 때(수동 조치 필요)**: Service의 `clusterIP`와
StatefulSet의 `serviceName`은 모두 k8s의 **immutable 필드**다. headless→
ClusterIP로 바뀌는 이번 변경을 `helm upgrade`만으로 적용하면
`Forbidden: spec is immutable` 류 오류가 난다. 그러면 아래 순서로
해당 리소스만 지우고 다시 upgrade한다(PVC는 그대로 남는다 — Service/
StatefulSet 삭제는 PVC를 건드리지 않는다):
```
kubectl -n osiris-system delete svc neo4j nats osiris-core osiris-omniroute
kubectl -n osiris-system delete statefulset neo4j nats osiris-core osiris-omniroute
helm upgrade osiris deploy/osiris -f deploy/osiris/values-secret.yaml
```

## 검증

★ [2026-07-28 Phase C-4a] `helm` CLI로 실제 `helm lint`/`helm template`을
돌렸고(0 chart(s) failed), 실제 k3d(k3s v1.30.4) 클러스터에
`helm install`/`helm upgrade`로 설치해 파드가 실제로 뜨는지까지 확인했다
(이전 phase의 "helm이 없어 yaml.safe_load_all로만 확인했다"는 기록은
이제 유효하지 않다 — 실제 실행 결과로 대체):

```
helm lint deploy/osiris
helm template osiris deploy/osiris \
  --set secrets.neo4jPassword=x --set secrets.coreApiToken=x \
  --set secrets.natsCorePassword=x --set secrets.natsOrchestratorPassword=x \
  --set secrets.natsApiPassword=x
```

실측 결과(k3d `osiris-dev`, 단일 노드, `pinCoreToMainNode=false` 기본값):
`nats-0`/`neo4j-0`/`osiris-api`/`osiris-console`/`osiris-core-0`/
`osiris-egress`/`osiris-omniroute-0` 전부 `1/1 Running`. `osiris-orchestrator`
만 계속 `0/1`인데, 이유는 아래 "알려진 미해결 항목" 참고 — Agent
operator 미구현 때문이 아니라 **NetworkPolicy가 의도한 대로 orchestrator의
neo4j 직접 접속을 막고 있기 때문**임을 실측으로 확인했다(Go 코드 수정은
이 phase 범위 밖이라 그대로 둔다).

## 알려진 미해결 항목 — osiris-orchestrator가 Ready가 안 되는 이유(실측)

★ [2026-07-28 Phase C-4a 실측] `osiris-orchestrator` 파드는 계속
재시작한다. 로그:
```
osiris-orchestrator: connecting to neo4j at bolt://neo4j:7687 (read-only)
osiris-orchestrator: neo4j not ready yet (verify neo4j connectivity:
  ConnectivityError: dial tcp 10.43.140.144:7687: connect: connection
  refused), retrying in 2s
... (반복) ...
osiris-orchestrator: fatal: context canceled
```
neo4j는 이미 `1/1 Running`으로 안정적이고(`osiris-core-0`가 같은
Service로 정상 연결됨), 그런데도 orchestrator만 매번 거부당한다. 직접
검증했다(`app.kubernetes.io/component=orchestrator` 라벨을 단 디버그
파드로 `nc -zv neo4j 7687` — 즉시 거부, 같은 파드에 `component=core`
라벨을 붙이면 즉시 성공). 원인은 `templates/networkpolicy-system.yaml`의
`osiris-neo4j-allow`가 neo4j:7687 ingress를 `core`/`api` 두 컴포넌트로만
좁혀 뒀기 때문이다(A5/G7/D17 "그래프 쓰기 단일 관문은 core 하나"라는
설계 그대로) — 그 파일이 이미 TODO로 예고한 그대로: "orchestrator-
deployment.yaml이 여전히 NEO4J_URI/NEO4J_PASSWORD env를 주고 있는데,
이건 driver/docker(K9 이전) 시절 유산이고, 실제로 이 env를 써서 neo4j에
직접 접속을 시도하면 이 NetworkPolicy 아래서 연결이 거부된다." 이번
실측이 그 예고를 실증했다. **이것은 이 phase가 고칠 결함이 아니다** —
NetworkPolicy를 늦추면 A5/G7/D17이 깨지고, orchestrator 바이너리에서
그 env/직접접속 코드를 빼는 것은 Go 코드 수정(K9 driver/kube 교체, C-3
범위)이다. `osiris-core`/`osiris-api`는 이 제약 없이 정상적으로
`1/1 Running`이다.

이 실측 중에 (이 phase의 결함 목록과 무관하게) k3s의 NetworkPolicy
enforcement 자체가 실제로 동작한다는 것도 확인됐다 — 이것이 아래
"결함 6 확인 결과"의 근거를 뒷받침한다: pod-to-pod 트래픽은 정책이
막으면 실제로 막히고(위 실측), kubelet의 probe 트래픽은 같은 정책
아래서도 막히지 않는다(둘의 차이가 곧 결함 6의 답이다).

## 결정 대응표 (70 문서 § → 파일)

| 결정/절 | 구현 파일 |
|---|---|
| K1(설치 패키지 하나, omniroute 별도 파드) | `Chart.yaml`, `templates/omniroute-statefulset.yaml` |
| K2(k8s가 배포 전제) | chart 존재 자체 |
| K3(Ingress 단독 진입, osiris-gw 폐지) | `templates/console-deployment.yaml`, `templates/api-deployment.yaml`(Ingress는 C-2) |
| K4(코어 노드 고정 + 대가, `global.pinCoreToMainNode` 스위치로 기본 off) | `templates/core-statefulset.yaml`, `templates/neo4j-statefulset.yaml`, `templates/nats-statefulset.yaml`, `templates/priorityclass.yaml`, `templates/_helpers.tpl`(`osiris.mainNodeAffinity`), `values.yaml`(`global.pinCoreToMainNode`) |
| K5(osiris-agents 자유 배치) | `templates/namespace.yaml` |
| §1.2 불변자산 10번(고정 호스트명 3개) | `templates/_helpers.tpl` |
| §2 배치도 전체 | `templates/*-statefulset.yaml`, `templates/*-deployment.yaml` |
| §3.2 A3(egress 허용목록) | `templates/egress-deployment.yaml` |
| §7.2(NATS 인증 ConfigMap 재배선) | `templates/nats-statefulset.yaml`(정적 부트스트랩만, 동적 재기록은 C-3) |
| K7/K8(자격증명 재배선, G5) | `templates/secrets.yaml`, 각 워크로드의 `secretKeyRef` |
| K9/K10(Agent CRD/operator) | 이번 phase 범위 아님 — `templates/orchestrator-deployment.yaml`에 TODO로 표시 |

## `TODO(감독 판단 필요)` 전체 목록

1. `templates/egress-deployment.yaml` — compose의 소스 IP ACL
   (`osiris-agents`/`osiris-omniroute` 서브넷 분리)을 k8s Pod CIDR로
   옮길 수 없어 생략함 — 그 분리의 실제 집행을 NetworkPolicy(C-2)에
   위임할지 확인 필요.
2. `templates/nats-statefulset.yaml` — §7.2가 요구하는 "operator가
   authorization.conf ConfigMap을 patch하고 nats를 reload"의 구체적
   방식(사이드카 reloader vs NATS 자체 hot-reload, RBAC 범위)이 아직
   정해지지 않음 — C-3에서 결정 필요.
3. `templates/api-deployment.yaml` — master key 저장소가 K8(§4.4, K8s
   Secret)과 현재 Go 코드(파일시스템, `masterkey.Boot`)가 불일치함 — 이번
   phase는 PVC로 잠정 유지, Secret API로의 전환은 `masterkey` 패키지
   재작성(§6.2 폐기 대상)이 선행되어야 함.
4. `[2026-07-28 Phase C-2b 해소 — 읽기 경로만]` `templates/api-deployment.yaml`
   — `ENV_FILE_PATH`(.env 되쓰기)의 emptyDir(재시작 시 소실)을 osiris-secrets
   Secret의 `REPO_ENV_FILE` 키(읽기 전용 마운트)로 교체함. **쓰기 경로는
   여전히 미정** — 온보딩 0단계가 토큰을 발급받아 저장하는 코드
   (omniroute/operations.mjs의 applyGatewayTokenToEnv/applyManageKeyToEnv)는
   여전히 ".env 파일에 write"하고, 이 마운트가 읽기 전용이므로 k8s에서는
   그 write가 실패한다 — Secret을 patch하는 코드로 재작성하는 것은 C-3
   몫(api-deployment.yaml의 TODO(C-3) 참고).
5. `[2026-07-28 Phase C-2b 해소]` ~~`values.yaml`/`templates/console-deployment.yaml`
   — 콘솔 전용 nginx 이미지(Dockerfile)가 레포에 없음~~ — `console/Dockerfile`
   신설(멀티스테이지: node 빌드 → nginxinc/nginx-unprivileged에 산출물만
   COPY, 라우팅 conf는 굽지 않음). `console-deployment.yaml`의 포트를
   8080(nginx-unprivileged 기본)으로 맞춤.
6. `templates/orchestrator-deployment.yaml` — `DOCKER_SOCKET_PATH`/
   `HOST_PROJECT_DIR`는 K9 이전(driver/docker) 메커니즘이라 k8s에서
   실질적 의미가 없음 — driver/kube(controller-runtime) 교체는 C-3.
