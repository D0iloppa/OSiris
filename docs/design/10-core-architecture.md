# OSiris 코어 아키텍처 (Core Architecture)

> 작성일: 2026-07-26
> 입력: `00-research-prior-art.md`(기술 선행조사), `40-console-ia.md`(관리 콘솔 IA), 호스트 환경 실측(상위 리버스 프록시 / dev-net / omniroute), 배포 2단계 확정.
> 범위: 배포 단계 전략, 외부 진입 경로, 시스템 컴포넌트, 에이전트 런타임, 상태 수렴, 메시지 버스, 권한·자격증명·격리, 장애 모델, 콘솔 계약 충족 매핑, 미해결 쟁점 결단.
> 비범위: omniroute 내부(어댑터 경계만), 온보딩 파이프라인 상세(스키마·프로비저닝 인터페이스만), 온톨로지 스키마 상세(`20-ontology-schema.md`), 프론트엔드 구현.
>
> **표기 규칙**
> - 표기 없는 서술 = 이 문서가 내린 **확정 결정**.
> - `[미검증]` = 공식 출처로 확인하지 못한 가정. 구현 착수 전 확인 필요.
> - `[조사 뒤집음]` = `00-research-prior-art.md`의 권고를 뒤집은 항목. 근거를 함께 적었다.
> - **[1단계]** = docker-compose 배포에 해당. **[2단계]** = k3s 배포에 해당. 표기 없으면 두 단계 공통.
>
> **`[2026-07-28 K2 전환]`** 위 이원 표기 규칙 자체가 더 이상 참이 아니다. 사용자 확정으로 배포가 **k8s 단일 대상으로 전면 재작성**되었고(`70-kubernetes-deployment.md` K1~K13), compose 경로([1단계])는 폐기된다. 이 문서 전체에 흩어진 `[1단계]`/`[2단계]` 표기는 삭제하지 않고 그대로 두지만(정정 이력 보존 원칙), **읽을 때 `[1단계]` 서술은 "폐기된 과거 경로", `[2단계]` 서술은 "현재 유일한 배포 대상이 될 예정이었던 것 — 지금은 그조차 `70-kubernetes-deployment.md`의 K1~K13이 갱신·대체함"으로 읽어야 한다.** 개별 절 중 `[2단계]` 서술이 K-결정과 실제로 어긋나는 지점(§2.6, §6.2, §8.5, §4.1)에는 별도 정정을 달아 두었다. 그 외 `[2단계]` 서술 중 정정 표시가 없는 것은 K1~K13과 충돌이 확인되지 않은 것이며, 최종 확인은 `70-kubernetes-deployment.md`가 갖는다.

---

## 0. 결정 요약

| # | 결정 | 한 줄 근거 |
|---|---|---|
| D1 | **온톨로지(Neo4j) = 조직 의도(intent)의 유일한 진실. 물성(materialization)은 그로부터 파생. 런타임 관측(observation)은 워크로드 드라이버가 진실.** | 같은 사실을 두 곳에 두지 않기 위해 3분할로 소유권을 자름 |
| D2 | 배포는 **1단계 docker-compose → 2단계 k3s**. 두 단계를 가로지르는 **불변 자산**(온톨로지·배정·버스·supervisor·콘솔·게이트웨이 이음새)을 먼저 고정한다 | 오늘 돌아야 하는 것과 나중에 확장할 것을 분리 |
| — | **`[2026-07-28 K2 전환]`** D2의 "1단계→2단계 이행" 구조 자체가 폐기된다. K2(`70-kubernetes-deployment.md`)가 확정한 것은 **k8s 단일 배포 대상으로의 전면 재작성**이며, docker-compose 경로는 만들지 않는다. D2가 지켰던 목표("불변 자산을 먼저 고정") 자체는 유효하게 남지만, 그 불변 자산이 지켜야 할 "이행 경계"가 이제 없다 — 처음부터 k8s가 유일한 대상이다 | 아래 §2.6/§4.1/§6.2/§8.5의 개별 정정 참고 |
| D3 | 워크로드 생성/관측/소멸은 **`WorkloadDriver` 인터페이스 뒤에서만** 일어난다. 1단계 `DockerDriver`, 2단계 `KubeDriver` | 이 인터페이스가 1→2단계 전환의 유일한 재작성 지점 |
| D4 | **에이전트는 compose 파일에 단 한 줄도 없다.** 조직은 런타임에 변하므로 Docker Engine API로 직접 생성/회수 | 선언적 정적 파일과 런타임 조직의 충돌을 정면으로 해결 |
| D5 | supervisor는 **사이드카가 아니라 컨테이너의 PID 1**, Go 단일 정적 바이너리 | SIGTERM이 supervisor에 먼저 도달해야 graceful checkpoint가 성립 |
| D6 | **liveness는 supervisor 자신의 select 루프 tick만 본다.** `claude` 실행 여부·실행 시간은 절대 보지 않는다 | 40분짜리 턴 도중 재시작 참사를 구조적으로 불가능하게 만듦 |
| D7 | 인박스는 **폴링이 아니라 JetStream pull consumer의 long-poll**. 유휴 경로에 `claude` 프로세스가 없다 | 유휴 토큰 비용 0 |
| D8 | 외부 진입은 **`인터넷 → 상위 리버스 프록시 → osiris-gw → osiris-api`**. `osiris-gw`가 **안정적 이음새(stable seam)** — 배포 단계가 바뀌어도 상위 리버스 프록시 설정은 불변 | 상위 게이트웨이는 OSiris 소유가 아니므로 건드리지 않는 것이 자산 |
| D9 | **TLS는 상위 리버스 프록시에서 종단.** 내부(dev-net 포함)는 평문 HTTP | 인증서 갱신 경로가 이미 하나로 통일되어 있고, 그것을 깨는 비용 > 동일 호스트 브리지 재암호화의 이득 |
| D10 | **`dev-net`에 발을 걸치는 OSiris 컴포넌트는 정확히 2개**: `osiris-gw`(인바운드 전용), `osiris-egress`(아웃바운드 전용). **에이전트는 어떤 경로로도 `dev-net`에 도달할 수 없다**(단 하나의 예외: egress 프록시 허용목록의 `omniroute`) | 에이전트는 신뢰 경계 안에서 임의 코드를 실행하는 주체 |
| D11 | **컨트롤 플레인 밖(호스트 systemd)에 `osiris-watchdog`**. `cluster.heartbeat` 부재 신호만으로는 불충분. 대역외 알림은 **상위 리버스 프록시를 경유하지 않는다** | "죽어서 못 알리는" 경우를 안쪽의 무엇으로도 알릴 수 없음 |
| D12 | **배정은 LLM이 있는 프로세스에 존재하지 않는다** — `osiris-core`(결정적) / `agent-master`(LLM)를 배포 단위로 분리 | compute-over-inference를 구조로 강제 |
| D13 | 권한은 **하나의 `PermissionProfile` 선언에서 4개 층위**(RBAC / 네트워크+egress 허용목록 / Claude Code allowedTools+게이트 / NATS ACL)로 파생 | 선언이 하나여야 감사와 회전이 성립 |
| D14 | 크로스팀 Master 경유는 **NATS 계정 권한으로 기술적으로 강제** (규약 아님) `[조사 뒤집음]` | 프롬프트 인젝션으로 깨지는 규약은 거버넌스가 아님 |
| D15 | **[2단계]** CRD는 `Team`/`Agent`/`PermissionProfile` 3종뿐. `Assignment` CRD는 만들지 않는다 | etcd는 태스크 큐가 아님 |
| D16 | **[2단계]** 에이전트 워크로드는 StatefulSet이 아니라 **Agent CR이 직접 소유하는 bare Pod + PVC** `[조사 뒤집음]` | 우리가 이미 컨트롤러를 갖고 있으므로 StatefulSet은 통제 불가한 중간층만 추가 |
| D17 | Neo4j 쓰기는 **전부 `osiris-core`의 graph-write API 경유.** LLM은 임의 Cypher 실행 권한 없음 | revision bump·스키마 검증·이벤트 발행을 한 지점에서 보장 |
| D18 | **워크스페이스는 named volume만.** 어떤 코드도 호스트 경로를 알지 못한다 | bind mount 경로가 코드에 박히면 2단계 PVC 이관이 막힌다 |
| D19 | 콘솔 SSE 재개는 **JetStream 시퀀스 기반**(게이트웨이 메모리 비의존) | 게이트웨이 인스턴스가 바뀌어도 이벤트가 이어짐 |
| D20 | SSE는 **두 홉 모두에서** 버퍼링 비활성·긴 read timeout이 필요. 15초 keepalive 코멘트 라인 상시 발행 | 한 홉만 놓쳐도 실시간이 죽음 |

---

## 1. 배포 단계 전략

### 1.1 두 단계

| | **1단계 (지금 구현)** | **2단계 (나중)** |
|---|---|---|
| 실행 기반 | docker-compose (단일 호스트) | k3s 호스트 네이티브(systemd), `--cluster-init` |
| 목적 | 오늘 실제로 돌아가는 AI 조직 | 멀티노드 확장, 선언적 상태 수렴, 세밀한 격리 |
| 노드 수 | 1대 고정 | 1대 → N대 |
| 전환 트리거 | — | 아래 §1.6 |

**현 시점 가정 — `[사용자 확정 대기]`**: **모든 배포는 1단계에서 시작하고, 2단계는 §4.2의 마이그레이션으로만 도달한다.** 즉 "1단계 없이 2단계로 바로 시작하는 그린필드 절차"는 **정의하지 않으며 지원 대상이 아니다.** 이것은 설계 판단이 아니라 제품 판단이므로 이 문서가 확정하지 않는다 — 사용자 확정 전까지의 작업 가정으로만 기록한다. 그린필드 2단계가 지원 대상으로 확정되면 §4.2에 별도 절차(k3s 선설치 + `server/manifests` 직접 투입 + 카탈로그 시드)를 추가해야 하며, 그때 §1.6의 전환 비용표 중 "스토리지 마이그레이션 절차"만 불필요해진다.

### 1.2 두 단계를 가로지르는 **불변 자산**

이것들은 배포 방식과 무관하게 동일하며, 1단계에서 만든 그대로 2단계로 넘어간다. **설계의 중심은 여기에 있다.**

1. **온톨로지 스키마 전체** (`20-ontology-schema.md`) — Neo4j 라벨·관계·제약·인덱스
2. **결정적 배정 알고리즘**과 배정 근거 스냅샷 스키마
3. **NATS 서브젝트 구조·스트림 정의·ACL 모델**
4. **supervisor 바이너리와 그 프로토콜** — 인박스 소비, 하트비트, 체크포인트/재개, 헬스 판정
5. **`osiris-core` / `osiris-api` 전체** — 컨테이너 런타임에 무관
6. **권한 grant-catalog와 파생 규칙** (RBAC 층만 1단계에서 no-op)
7. **콘솔 전체**와 REST/SSE 계약
8. **외부 진입 경로**: DNS, `example.com` 인증서, 상위 리버스 프록시 conf, `osiris-gw` 존재 자체
9. **컨테이너 내부 경로 규약**: `/workspace`, `/var/run/osiris/creds`, `/var/run/osiris/config`
10. **에이전트가 아는 호스트명 3개**: `nats`, `osiris-core`, `osiris-egress` (모두 환경변수 주입)

### 1.3 1단계 ↔ 2단계 대응표

| 축 | **1단계 (compose)** | **2단계 (k3s)** | 전환 시 |
|---|---|---|---|
| 에이전트 워크로드 단위 | Docker 컨테이너 1개 (Docker Engine API로 생성, compose 파일에 없음) | Pod 1개 (`Agent` CR이 ownerReference로 소유) | `WorkloadDriver` 구현만 교체 |
| 워크로드 명세 | `AgentWorkloadSpec` (그래프에서 매번 파생, 메모리에만 존재) | 동일 spec을 `Agent` CR spec에 영속화 | spec 타입 공유. CR은 파생 결과의 캐시 |
| 상태 수렴 | `osiris-orchestrator`의 reconcile 루프 (label 기반 desired vs actual, 15초 주기 + 이벤트 트리거) | `osiris-operator` (controller-runtime) | 루프 **구조가 동일**하도록 작성. 드라이버만 다름 |
| 격리 — L3 | 분리된 도커 네트워크 (`internal: true`). **네트워크 단위 격리, 팀 간은 미분리** | Namespace + NetworkPolicy (팀 간 전면 차단, 개별 egress 규칙) | 격리가 **강해짐**. 1단계에서 실질 강제는 NATS ACL + egress 허용목록 |
| 격리 — 프로세스 | `security_opt: no-new-privileges`, `cap_drop: ALL`, `read_only: true`, non-root. orchestrator가 생성 시 항상 부여 | 동일 + PodSecurity admission이 **강제** | 보증자가 "우리 코드"에서 "클러스터"로 이동 |
| 스토리지 | named volume `osiris-ws-<employeeId>` (Docker API 생성). 복제 없음 | PVC (Longhorn, replica=min(3,노드수)) | 볼륨→PVC 데이터 복사 절차 필요(§1.6) |
| 리소스 제한 | `HostConfig.Memory` / `NanoCPUs` (Docker API 직접 지정) | `resources.requests/limits` | 같은 `RoleResourceProfile`에서 파생 |
| 헬스 — liveness | Docker `HEALTHCHECK`가 상태만 표시. **재시작 판정은 orchestrator가 수행**(3회 연속 unhealthy → drain 후 재생성) | `livenessProbe`가 kubelet에서 직접 수행 | **판정 기준(§5.4)은 완전히 동일.** 집행자만 다름 |
| 헬스 — readiness | orchestrator가 `/readyz` 폴링 → 런타임 캐시·배정 필터 갱신 | `readinessProbe` + CR status condition | 동일 |
| graceful shutdown | `ContainerStop(timeout=660s)` → SIGTERM → 660초 후 SIGKILL | `terminationGracePeriodSeconds: 660` | supervisor 코드 무변경 |
| 에이전트 동적 생성 | **Docker Engine API 직접 호출** (§1.4) | CR 생성 → operator가 Pod 생성 | 드라이버 교체 |
| 시크릿 | 호스트의 `0700` 디렉토리를 read-only bind → `/var/run/osiris/creds` (orchestrator가 파일 생성·회전) | k8s Secret projected volume → 같은 경로 | **컨테이너 내 경로가 같으므로 supervisor 무변경** |
| 서비스 디스커버리 | 도커 내장 DNS (`nats`, `osiris-core`, `osiris-egress`) | k8s Service DNS (같은 이름의 Service를 만든다) | 이름을 동일하게 유지하면 무변경 |
| 스케줄링 | 없음(1대). orchestrator가 좌석 환산으로 사전 거부 | kube-scheduler | 좌석 환산 로직은 재사용(용량 계산 소스만 교체) |
| RBAC | 없음. 에이전트는 애초에 컨테이너 오케스트레이터 API를 쓰지 않으므로 실손실 작음 | ServiceAccount + Role/RoleBinding (대부분 비어 있음) | `k8s.read.ownteam` grant가 1단계에서 no-op |
| 컨트롤 플레인 | dockerd + osiris-orchestrator | k3s(apiserver+etcd) + osiris-operator | §10 장애 모델 갱신 |
| 노드 확장 | **불가** | join 토큰 기반 | §1.5 |
| 감사 | Docker 이벤트 + orchestrator 감사 로그 | 위 + k8s audit + etcd 이력 | 강화됨 |

### 1.4 [1단계] 에이전트 동적 생성 — 가장 어려운 지점의 결단

**문제**: compose는 선언적 정적 파일인데, 조직은 런타임에 바뀐다(온보딩이 새 직원을 만든다).

**검토한 세 안**

| 안 | 내용 | 판정 |
|---|---|---|
| (a) compose 파일 재생성 + `docker compose up -d` | 온보딩 때마다 `docker-compose.agents.yml`을 다시 쓰고 재적용 | **기각.** ① 프로젝트 전체를 재수렴시켜 무관한 서비스 재시작 위험, ② 파일 쓰기 경쟁, ③ 컨테이너 안에서 compose CLI를 돌려야 함(소켓 마운트 + 바이너리 + 프로젝트 디렉토리 공유), ④ 진행 중인 에이전트 턴을 끊을 수 있음, ⑤ **조직 정보가 파일에 새어나가 그래프가 유일한 진실이 아니게 됨**(D17 위반) |
| (b) **Docker Engine API 직접 호출** | orchestrator가 `/var/run/docker.sock`으로 컨테이너 단위 create/start/stop/remove | **채택** |
| (c) 미리 정의된 슬롯 풀 | `agent-slot-01..20`을 미리 띄워두고 배정 시 역할을 주입 | **기각.** 유휴 자원 낭비, 조직 규모 상한을 고정, 에이전트별 볼륨/자격증명이 슬롯에 붙어 정체성이 흐려짐 |

**(b)의 설계**

```
compose 파일에 있는 것 :  osiris-gw, osiris-api, osiris-core, osiris-orchestrator,
                          neo4j, nats, osiris-egress          ← 코어 7개. 끝.
compose 파일에 없는 것 :  에이전트 전부 (agent-master 포함).
```

`osiris-orchestrator`의 수렴 루프:

```go
// 15초 주기 + org.revision 이벤트 수신 시 즉시
func reconcile(ctx) {
    desired := graph.DeriveAgentWorkloads()          // Neo4j → []AgentWorkloadSpec
    actual  := driver.Observe(ctx)                   // label osiris.dev/employee-id 로 필터
    for _, d := range desired {
        a, ok := actual[d.EmployeeID]
        switch {
        case !ok:                           driver.Apply(ctx, d)         // 신규 채용
        case a.SpecHash != d.SpecHash:      driver.Recreate(ctx, d)      // 이미지·리소스 변경 → drain 후 교체
        case a.Unhealthy >= 3:              driver.Recreate(ctx, d)      // liveness 집행
        case a.ConfigHash != d.ConfigHash:  driver.ReloadConfig(ctx, d)  // 프롬프트·권한·역량 변경 → 무중단
        case a.Stopped && d.Running:        driver.Start(ctx, d.EmployeeID)
        }
    }
    for id := range actual {                                            // 퇴사/정지
        if _, ok := desired[id]; !ok { driver.Drain(ctx, id, 660); driver.Delete(ctx, id) }
    }
}
```

`Apply`가 하는 일(멱등):
1. named volume `osiris-ws-<id>` 없으면 생성
2. 자격증명 디렉토리 `<host>/creds/<id>/` 생성·권한 0700·파일 기록(게이트웨이 토큰, git 토큰, NATS creds)
3. 렌더된 설정 `<host>/config/<id>/{CLAUDE.md,settings.json,mcp.json}` 기록 — 전부 그래프에서 파생된 문자열 치환 결과이며, `CLAUDE.md`의 원문 템플릿은 `:Role.promptTemplateMd`다(`20-ontology-schema.md` §3.2)
4. NATS 사용자 생성/갱신(팀 계정 하위, §7.3의 ACL)
5. `ContainerCreate`:
   - 이미지: `ghcr.io/osiris/agent-runtime:<pinned>`
   - 네트워크: `osiris-agents` **하나만**
   - 마운트: volume→`/workspace`, creds→`/var/run/osiris/creds`(ro), config→`/var/run/osiris/config`(ro)
   - `Labels`: `osiris.dev/employee-id`, `osiris.dev/team`, `osiris.dev/spec-hash`, `osiris.dev/managed-by=orchestrator`
   - `HostConfig`: `Memory`, `NanoCPUs`, `RestartPolicy: unless-stopped`, `ReadonlyRootfs: true`, `CapDrop: [ALL]`, `SecurityOpt: [no-new-privileges]`, `Tmpfs: {/tmp: rw,size=512m}`
   - `Healthcheck`: `CMD curl -fsS localhost:8081/livez`, interval 10s, retries 3
6. `ContainerStart`

**Docker 소켓 = 사실상 호스트 root.** 따라서:
- 소켓은 **`osiris-orchestrator`에만** 마운트한다. 에이전트 컨테이너에는 절대 주지 않는다.
- orchestrator는 **LLM을 호출하지 않는다**(이미지에 `claude` 없음, 게이트웨이 자격증명 없음). D12와 같은 이유이며 여기서는 보안 요구이기도 하다.
- orchestrator는 `osiris-agents` 네트워크에 붙지 않는다(에이전트가 orchestrator에 도달 불가).
- `[미검증]` 소켓 프록시(예: `docker-socket-proxy`)로 API 표면을 축소하는 것을 권고하나 1단계에서는 미도입(Simplicity First). 도입 트리거: OSiris 외 다른 워크로드가 같은 호스트에서 돌기 시작할 때.

### 1.5 [1단계] 정직하게 포기하는 것

| 포기하는 것 | 1단계의 실제 결과 | 2단계에서의 회복 |
|---|---|---|
| **멀티노드** | 조직 규모 상한이 호스트 1대 사양에 묶인다. 8GiB/4vCPU면 상시 에이전트 4~6명이 현실적 상한 | 워커 노드 join |
| **자동 재스케줄링** | 호스트가 죽으면 전부 멈춘다. 컨테이너 크래시만 `restart: unless-stopped`로 복구 | 노드 장애 시 다른 노드로 Pod 재생성 |
| **선언적 상태 수렴의 견고함** | desired state가 orchestrator 메모리에만 있다. orchestrator가 죽으면 수렴이 멈춘다 (**완화**: 기동 시 그래프에서 전량 재계산하므로 재기동만으로 회복. desired가 유실되는 것이 아니라 "계산이 멈추는" 것) | etcd에 CR spec이 남아 컨트롤러 재기동과 무관하게 desired가 보존 |
| **세밀한 L3 격리** | 같은 `osiris-agents` 네트워크 안에서 에이전트끼리 L3 도달 가능. **실질 강제는 NATS ACL과 egress 허용목록이며, L3 자체는 열려 있다** | NetworkPolicy로 팀 간·에이전트 간 전면 차단 |
| **세밀한 RBAC** | 없음. 실손실은 작다(에이전트는 오케스트레이터 API를 쓰지 않음) | ServiceAccount 단위 Role |
| **보안 프로파일의 강제** | `cap_drop`/`read_only`를 **orchestrator 코드가** 항상 부여한다. 코드가 유일한 보증자이며 admission 같은 독립 검증자가 없다 | PodSecurity admission이 클러스터 차원에서 강제 |
| **롤링 업데이트 / PDB** | 없음. orchestrator가 순차 교체를 직접 수행 | Deployment 롤링, PDB |
| **스케줄러·오버커밋 관리** | 없음. orchestrator가 좌석 환산으로 **사전 거부**만 한다("여유 좌석 0명") | kube-scheduler |
| **스토리지 복제** | 없음(단일 호스트라 의미도 없음). 야간 tar 백업만 | Longhorn 복제 |
| **etcd 수준의 변경 감사** | Docker 이벤트 + orchestrator 감사 로그로 대체 | k8s audit log |

이 표에서 **"완화"라고 적지 않은 항목은 실제로 포기하는 것**이다. 1단계는 "compose로도 다 된다"가 아니라 **"1대에서 조직을 실제로 돌려보되, 멀티노드·자동복구·강제 격리는 2단계로 미룬다"**는 선택이다.

### 1.6 1→2단계 전환 비용

**그대로 살아남는 것 (재작성 0)**

온톨로지 스키마 · 배정 알고리즘 · 배정 근거 스냅샷 · NATS 서브젝트/스트림/ACL · supervisor 바이너리 전체 · `osiris-core` 전체 · `osiris-api` 전체 · 콘솔 전체 · grant-catalog · `osiris-gw` 컨테이너와 그 존재 · **상위 리버스 프록시 conf** · **DNS** · **`example.com` 인증서와 갱신 경로** · 컨테이너 이미지(에이전트 런타임) · 컨테이너 내부 경로 규약 · 에이전트가 아는 호스트명 3개

**다시 써야 하는 것**

| 항목 | 규모 | 비고 |
|---|---|---|
| `WorkloadDriver`의 Kube 구현 | 중 | 인터페이스는 동일. Docker 구현이 참조 구현이 됨 |
| CRD 타입 정의 3종 + zz_generated | 소 | `AgentWorkloadSpec`을 CR spec으로 옮기는 기계적 작업 |
| RBAC / NetworkPolicy 렌더러 | 중 | 1단계에서 no-op이던 grant-catalog의 두 층이 실제 코드를 갖게 됨 |
| 부트스트랩 스크립트 | 중 | `osiris-up`(compose) → k3s 설치 + `server/manifests` 투입 |
| 스토리지 마이그레이션 절차 | 소 | 볼륨별 `tar` → PVC 복원. 에이전트를 순차 정지하며 수행 |
| `osiris-gw`의 업스트림 지시 | **1줄** | `proxy_pass http://osiris-api:8443` → `http://<호스트IP>:8443` |
| watchdog 체크 항목 | 소 | `docker ps`/컨테이너 헬스 → k3s `/readyz` |

**상위 리버스 프록시 쪽 변경: 0.** 이것이 §2의 이음새 설계의 목적이다.

### 1.7 1단계에서 **하면 안 되는 것** (2단계로 가는 길을 막는 결정)

각 항목은 실제로 저지르기 쉬운 실수이며, 지금 금지해 둔다.

| # | 금지 | 저지르면 생기는 일 | 대신 |
|---|---|---|---|
| G1 | 워크스페이스를 **호스트 경로 bind mount** | 호스트 경로 가정이 supervisor/도구 코드에 스며들어 PVC로 못 옮김 | **named volume만.** 컨테이너 내 경로는 항상 `/workspace` |
| G2 | 에이전트가 **서로를 컨테이너 이름으로 직접 호출** | 2단계에서 Pod IP/Service로 옮길 때 전면 재작성 + 팀 격리 붕괴 | 에이전트 간 통신은 **NATS 전용**. 아는 호스트명은 3개뿐 |
| G3 | Docker API 타입이 **`WorkloadDriver` 밖으로 유출** | 2단계에서 호출부 전부 재작성 | 드라이버 인터페이스 뒤에만 둔다. `github.com/docker/docker` import는 `driver/docker` 패키지에서만 허용(CI 린트로 강제) |
| — | **`[2026-07-28 K2 전환]`** "2단계에서 호출부 전부 재작성"이라는 위험 서술의 전제(두 드라이버가 순차로 공존)가 사라진다. K2가 확정한 대로 드라이버는 처음부터 `driver/kube` 하나뿐이고 `DockerDriver`는 구현되지 않는다. 다만 G3의 **약속 자체는 다른 방식으로 이행된다**: `WorkloadDriver` 추상은 그대로 유지하고 호출부(§1.4 수렴 루프 등)는 드라이버 구현 교체와 무관하게 유지된다 — 지금은 지킬 두 번째 드라이버가 없을 뿐이다. `github.com/docker/docker` import 제한 린트는 **검사 대상 패키지 자체가 없어지므로 삭제(또는 no-op)** 한다 | 상세는 `70-kubernetes-deployment.md` K2/K7 |
| G4 | compose 파일에 **에이전트/조직 정보** 기재 | 그래프가 유일한 진실이 아니게 됨 → 두 곳이 어긋남 | 코어 7개 서비스만 |
| G5 | 시크릿을 compose `environment:`에 **평문 env로** | 2단계 Secret 이관 시 주입 방식이 달라져 supervisor 수정 필요. env는 `/proc/<pid>/environ`으로 오래 노출 | **파일 주입.** 경로 `/var/run/osiris/creds/*`를 두 단계 동일 유지 |
| G6 | `network_mode: host` 사용 | 격리 소멸 + 2단계 NetworkPolicy 모델과 단절 | 항상 명시적 브리지 네트워크 |
| G7 | Neo4j에 **여러 컴포넌트가 직접 쓰기** | 리비전·검증·이벤트가 새고 온톨로지가 오염 | graph-write API 단일 관문 (D17) |
| G8 | 에이전트 컨테이너에 **docker.sock 마운트** | 에이전트가 호스트 root와 동등해짐 | orchestrator에만 |
| G9 | `restart: always`에 의존한 "복구" | unhealthy-but-alive를 못 잡고, 체크포인트 없는 무한 재시작 루프 발생 | orchestrator의 수렴 루프가 liveness를 집행(§1.3) |
| G10 | 좌석/용량을 **프론트에 하드코딩** | 프로파일 변경이 반영 안 됨(콘솔 요구 위반) | `RoleResourceProfile`에서 백엔드 계산 |

**[2026-07-27 신설] 통합 어댑터가 이 목록을 약화시키지 않는지 점검 — 오히려 강화한다.** `50-integration-adapters.md`가 도입한 격리 어댑터(Plane 미러, 텔레그램 알림)를 이 목록과 대조했다:
- **G5·A4(§11.1)와의 정합**: A4는 "`osiris-core` 이미지에 `claude` 바이너리와 게이트웨이 자격증명을 넣지 않는다"였다. 어댑터 분리는 같은 논리를 **외부 알림 채널 자격증명**(텔레그램 봇 토큰 등)에도 적용한 것이다 — `osiris-core`는 텔레그램 봇 토큰을 전혀 갖지 않고, 그 자격증명은 격리된 `osiris-adapter-telegram` 컨테이너에만 파일 주입된다(G5 그대로 준수, §9.5 표에 반영). 즉 이 분리는 G5/A4를 **위반하지 않을 뿐 아니라, "외부로 나가는 자격증명은 그것을 실제로 쓰는 컴포넌트 하나에만 있어야 한다"는 원칙을 코어 밖으로 한 겹 더 넓혀 강화한다.**
- **G7과의 정합**: 어댑터는 Neo4j에 직접 붙지 않는다(`osiris-core:8091` 경유만, D1 두 번째 writer 금지가 어댑터에도 적용). G7 위반 없음.
- **G8과의 정합**: 어댑터 컨테이너에도 docker.sock을 마운트하지 않는다(§8.3 신설 문단). G8 위반 없음.
- **G4와의 정합**: 어댑터는 조직 구성원이 아니라 정적으로 compose에 선언된 프레임워크 서비스이며(`50-integration-adapters.md` E8), 빈 자격증명으로 기동해 온보딩이 채운다 — `osiris-omniroute`와 같은 층위. compose에 조직 정보를 넣는 것이 아니므로 G4와 무관하다.

---

## 2. 외부 진입 경로 및 게이트웨이 이음새

### 2.0 프레임워크 계약 (일반) — §2.1~§2.8은 예시 배포다 (I5 2차 수정, 사용자 지시)

**이 문서 최초 판(§2.1 이하)은 이 특정 개발 호스트의 실측을 그대로 계약처럼 적어 버렸다 — `<upstream-proxy>`/`dev-net`/`osiris.example.com`가 반드시 있어야 하는 것처럼.** 이는 설계와 실제 배포 예시를 섞은 것이다. 사용자가 바로잡았다: *"OSiris는 특정 사이트 구성이 아니라 어디서든 사용 가능한 프레임워크다."*

**일반 계약(이것이 진짜 설계다)**:
- `osiris-gw`는 **포트에서 수신한다.** 기본 설치에서는 `127.0.0.1:${OSIRIS_GW_HOST_PORT}`에 직접 게시되어, 설치 직후 로컬호스트에서 콘솔(`/`)·플랫폼 상태(`/pre-status`)·omniroute 관리 UI(`/omni/`)에 바로 닿는다.
- **임의의 상위 게이트웨이가 그 앞에 놓일 수 있다 — 놓이지 않아도 된다.** 상위 게이트웨이가 있는 배포는 `docker-compose.dev-net.yml` 같은 오버레이로 `osiris-gw`를 그 상위가 아는 공유 네트워크에 추가로 붙인다. `osiris-gw` 서비스 정의 자체(설정·라우팅 로직)는 상위 유무와 무관하게 동일하다 — A8이 이미 이걸 말하고 있었다("osiris-gw를 안정적 이음새로 영구 유지"), 이번 수정은 그 이음새가 흡수하는 대상을 "이 호스트의 특정 상위 리버스 프록시"에서 "있을 수도 없을 수도 있는 임의의 상위"로 일반화한 것뿐이다.
- **공개 도메인, 상위 게이트웨이 유무, TLS 종단 위치는 전부 온보딩이 결정한다.** 부트스트랩(`osiris-up`)은 이 중 어느 것도 미리 정하지 않는다 — `.env`의 `PUBLIC_ORIGIN` 기본값은 `http://localhost:${OSIRIS_GW_HOST_PORT}`이고, `DEV_NET_NAME`은 기본적으로 비어 있다(§11.1 A8/A9는 유효하되, "어떤 상위"인지는 배포마다 다르다는 뜻으로 다시 읽을 것). 이 항목들이 정확히 무엇이고 온보딩이 어떻게 다뤄야 하는지는 `docs/design/IMPLEMENTATION-NOTES.md` I5 §56("온보딩이 결정할 항목")에 정리했다.
- **TLS는 상위 게이트웨이가 있다면 거기서 종단한다(D9). 없다면(기본 설치) 종단 자체가 없다** — 평문 HTTP로 로컬호스트에 노출되는 것은 사설 도구(Grafana/Portainer류)들의 일반적인 관례와 같은 신뢰 경계(로컬 머신)를 전제한다. 인터넷에 그대로 노출하려면 사용자가 상위 게이트웨이나 별도 리버스 프록시를 명시적으로 앞에 둬야 한다 — OSiris가 자동으로 그렇게 하지 않는다.

**부록 — 이 호스트의 예시 배포**: 아래 §2.1~§2.8은 이 개발 호스트가 실제로 쓰는 하나의 구체적 배포(상위 = `<upstream-proxy>`, 도메인 = `osiris.example.com`, dev-net 오버레이 적용)를 실측 그대로 기록한 것이다. **이 값들은 설정이지 설계가 아니다** — 다른 배포는 다른 상위·다른 도메인·오버레이 없음으로 있을 수 있고, 그래도 `osiris-gw`가 제공하는 이음새 계약(§2.3)은 동일하다. 아래 절들을 읽을 때 "이 호스트에서는"을 암묵적으로 붙여서 읽을 것.

### 2.1 확인된 호스트 환경 (실측, 이 호스트의 예시 배포)

- `<upstream-proxy>` = nginx 컨테이너. 호스트 `:80`/`:443` 독점. 외부 도커 네트워크 `dev-net`에 물려 있음. **OSiris 소유가 아님.**
- `dev-net` 가입이 상위 리버스 프록시 라우팅의 전제 조건.
- TLS: `example.com` 단일 SAN 인증서가 약 19개 서브도메인을 커버. zerossl-bot(certbot 계열) webroot 발급 → `nginx/live/example.com/` 복사 → reload. **갱신 경로가 하나로 통일.**
- 서브도메인 추가 = `nginx/conf.d/<도메인>.conf` 하나 + `dev-net` 컨테이너 이름으로 프록시. `resolver 127.0.0.11` + `set $var` 지연 해석(업스트림이 죽어 있어도 nginx가 기동).
- `osiris.example.com` DNS는 이미 호스트를 가리킴.
- **`omniroute`는 이미 `dev-net` 위에서 도는 실제 컨테이너** (`:20128`, `omniroute.example.com`). 네트워크상 도달 가능한 이웃으로 전제.

### 2.2 홉 구조 — 무엇이 어디서 끝나는가

```
 ┌──────────┐  HTTPS/443            ┌───────────────────────────────┐
 │ 브라우저  │ ────────────────────▶ │ <upstream-proxy> (dev-net)    │  ← OSiris 소유 아님
 └──────────┘  osiris.example.com   │  · TLS 종단 (example.com SAN)  │
                                     │  · HSTS, HTTP→HTTPS 리다이렉트 │
                                     │  · X-Forwarded-For 최초 기입   │
                                     │  · SSE 버퍼링 off              │
                                     └──────────────┬────────────────┘
                                        평문 HTTP    │ dev-net
                                                     ▼
                                     ┌───────────────────────────────┐
                                     │ osiris-gw (nginx)   ★ 이음새   │  ← OSiris 소유
                                     │  네트워크: dev-net + osiris-edge│
                                     │  · 유일한 dev-net 인바운드 접점 │
                                     │  · XFF/X-Real-IP 재작성(신뢰1홉)│
                                     │  · SSE 버퍼링 off, 3600s        │
                                     │  · 정적 자산(콘솔 SPA) 서빙     │
                                     │  · /api/* 만 업스트림으로       │
                                     └──────────────┬────────────────┘
                                        평문 HTTP    │ osiris-edge (internal: true)
                                                     ▼
                                     ┌───────────────────────────────┐
                                     │ osiris-api                    │
                                     │  REST + SSE. 인증·인가 여기서   │
                                     └───────────────────────────────┘
```

각 홉에서 **끝나는 것 / 통과하는 것**

| 홉 | 여기서 끝나는 것 | 여기를 통과하는 것 |
|---|---|---|
| 상위 리버스 프록시 | TLS, HTTP/2, HSTS, 공개 도메인 라우팅 | 인증(세션 쿠키), 애플리케이션 경로, SSE 스트림, XFF |
| osiris-gw | 정적 자산 서빙, 경로 분기(`/api/*` vs SPA), 프록시 신뢰 체인 정규화 | 인증, SSE 스트림, 정규화된 클라이언트 IP |
| osiris-api | 인증·인가·세션, 비즈니스 계약(REST/SSE) | (내부로는 NATS/Neo4j 접근만) |

### 2.3 `osiris-gw` = 안정적 이음새 (stable seam)

**핵심 원리**: 상위 `<upstream-proxy>` 입장에서 업스트림이 **단 하나(`osiris-gw`)로 고정**되면, 그 뒤가 compose 서비스든 k3s hostPort든 상위 설정을 건드리지 않고 갈아끼울 수 있다.

`<upstream-proxy>`의 `conf.d/osiris.example.com.conf` (한 번 설치하고 **영구 불변**):

```nginx
server {
    listen 443 ssl http2;
    server_name osiris.example.com;
    ssl_certificate     /etc/nginx/live/example.com/fullchain.pem;   # 기존 단일 SAN 인증서 그대로
    ssl_certificate_key /etc/nginx/live/example.com/privkey.pem;

    resolver 127.0.0.11 valid=10s ipv6=off;      # 업스트림이 죽어 있어도 nginx가 기동되도록
    set $osiris_up "osiris-gw:80";               # ← 지연 해석 관용구 (기존 패턴 준수)

    location / {
        proxy_pass http://$osiris_up;
        proxy_http_version 1.1;
        proxy_set_header Host              $host;
        proxy_set_header X-Real-IP         $remote_addr;
        proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Connection        "";
        # --- SSE 필수 (D20) ---
        proxy_buffering            off;
        proxy_cache                off;
        proxy_read_timeout         3600s;
        proxy_send_timeout         3600s;
        chunked_transfer_encoding  on;
        gzip                       off;
    }
}
```

`osiris-gw`의 conf에서 **단계 전환 시 바뀌는 것은 한 줄**:

```nginx
# 1단계 (compose):  같은 osiris-edge 네트워크의 서비스명
set $api_up "osiris-api:8443";

# 2단계 (k3s):      호스트의 hostPort DaemonSet
set $api_up "172.17.0.1:8443";      # 또는 host-gateway
```

**이음새가 지켜주는 것**
- DNS 레코드: 불변
- `example.com` 인증서와 zerossl-bot 갱신 경로: 불변 (**OSiris는 인증서를 발급하지도, 갱신하지도 않는다**)
- `<upstream-proxy>`의 conf: 불변
- `dev-net` 상의 컨테이너 이름 `osiris-gw`: 불변 (2단계에서도 이 컨테이너는 남는다)
- 콘솔이 아는 오리진 `https://osiris.example.com`: 불변

**대가**: 홉이 하나 늘고 `osiris-gw`가 단일 실패점이 된다. nginx 하나의 안정성과 이 불변성의 가치를 견주어 **수용**한다. (osiris-gw는 상태가 없으므로 재시작이 즉시이고, `restart: unless-stopped`로 충분하다.)

### 2.4 TLS 종단 위치 — 결정과 근거

**결정: TLS는 `<upstream-proxy>`에서 종단한다. `상위 리버스 프록시 → osiris-gw → osiris-api` 구간은 평문 HTTP.**

근거:
1. **인증서 갱신 경로가 이미 하나로 통일되어 있고, 그 단일성이 운영 자산이다.** 내부 재암호화를 도입하면 내부 CA 발급·배포·갱신 경로가 하나 더 생기고, 그 갱신 실패는 곧 콘솔 전면 장애가 된다. 실패 모드를 하나 늘려 얻는 것이 뒤 항목의 위협 모델에 비해 작다.
2. **위협 모델**: 평문 구간은 전부 동일 호스트의 도커 브리지다. 이 트래픽을 스니핑할 수 있는 주체는 (a) 호스트 root, (b) `dev-net`에 있는 다른 컨테이너다. (a)라면 TLS는 어차피 무의미하다(키도 그 호스트에 있다). (b)가 실질 위협인데 — 
3. **`dev-net`은 이미 신뢰 구간으로 운용되고 있다.** 19개 서브도메인 전부가 상위 리버스 프록시→각 업스트림으로 평문으로 흐르는, 표준 리버스 프록시 구성이다. OSiris만 예외적으로 재암호화하는 것은 일관성 없는 국소 강화이며, 다른 18개가 평문인 이상 실질 보안 이득이 적다.
4. 브라우저↔엣지 구간은 보호된다: 세션 쿠키 `Secure; HttpOnly; SameSite=Strict`, 상위 리버스 프록시의 HSTS. (여기서 `Secure`가 붙는 것은 **이 배포 예시가 TLS로 종단하기 때문**이다 — 쿠키 속성 자체는 요청별로 결정되며 상위 게이트웨이 없는 루프백 설치에서는 붙지 않는다. §8.4 표의 정정 참고.)

**완화 조치** (평문 구간에 인증 토큰이 흐르는 것을 수용하는 대가):
- 세션 TTL 12시간, 로그아웃 시 서버측 즉시 무효화, 재발급 시 rotate.
- 인증 자격증명은 쿠키로만 전달. 쿼리스트링·URL 경로에 절대 싣지 않는다(프록시 액세스 로그에 남으므로).
- `osiris-api`는 `Host` 헤더가 `osiris.example.com`가 아니면 거부(다른 dev-net 컨테이너가 IP로 직접 치는 것을 차단).

**재검토 트리거**: `dev-net`에 제3자/비신뢰 컨테이너가 올라가는 순간 → `osiris-gw`까지 내부 CA 기반 TLS 재암호화를 도입한다. 이때도 **공개 인증서는 건드리지 않는다**(§2.5).

### 2.5 공개 엣지 인증서 vs 내부 PKI — 분리해서 다룬다

| | **공개 엣지 인증서** | **내부 PKI** |
|---|---|---|
| 대상 | 브라우저 ↔ 상위 리버스 프록시 | 컴포넌트 간, 노드 간 |
| 현재 | `example.com` 단일 SAN, zerossl-bot webroot, 단일 갱신 경로 | **미도입** |
| 소유 | 상위 리버스 프록시 (OSiris 밖) | OSiris |
| OSiris의 책임 | **없음.** 발급·갱신·배포 어디에도 관여하지 않는다 | 도입 시 전적으로 OSiris |
| 현재 미도입 근거 | — | 1단계는 전부 `internal: true` 도커 네트워크 안이고, 에이전트는 이미 개별 자격증명(NATS nkey/JWT, 게이트웨이 토큰)으로 인증된다. mTLS는 인증을 중복시킬 뿐 새 보증을 거의 주지 않는다 |
| **도입 트리거** | — | **[2단계] 멀티노드로 확장해 노드 간 트래픽이 물리 네트워크를 타는 순간** → NATS TLS + Neo4j bolt TLS를 **필수화**. 이때 CA는 k3s 내장 CA를 재사용하거나 cert-manager 자체 서명 CA를 쓴다(공개 인증서와 무관) |

두 문제를 섞지 않는 것이 중요하다: 공개 인증서는 "브라우저가 우리를 믿는가"의 문제이고, 내부 PKI는 "컴포넌트끼리 서로를 믿는가"의 문제다. 전자는 이미 풀려 있고 우리 소유가 아니며, 후자는 아직 필요하지 않다.

### 2.6 `dev-net` 가입과 격리의 충돌 해소 — DMZ 패턴

**보안 요구 (명시)**: **에이전트 Pod/컨테이너는 어떤 경로로도 `dev-net`에 도달할 수 없어야 한다.** 에이전트는 신뢰 경계 안쪽에서 LLM이 생성한 임의 코드를 실행하는 주체이며, `dev-net`에는 OSiris와 무관한 다른 프로젝트의 컨테이너들이 있다.

```
                        dev-net (외부, 공유)
   ┌──────────┬───────────────┬──────────────┬─────────────────┐
   │ upstream │   omniroute   │  타 프로젝트  │   타 프로젝트    │
   └────┬─────┴───────▲───────┴──────────────┴─────────────────┘
        │             │
        │ 인바운드     │ 아웃바운드(단 하나의 핀홀)
        ▼             │
   ┌─────────┐   ┌────┴──────────┐
   │osiris-gw│   │ osiris-egress │      ← dev-net에 발을 걸친 OSiris 컴포넌트는 이 둘뿐
   └────┬────┘   └────▲──────────┘
        │             │
  ┌─────▼──────┐ ┌────┴───────────────────────────────────────┐
  │osiris-edge │ │ osiris-agents        (internal: true)      │
  │(internal)  │ │  에이전트 컨테이너 N개, nats, egress        │
  └─────┬──────┘ └────▲───────────────────────────────────────┘
        │             │
  ┌─────▼─────────────┴───────────────────────────────────────┐
  │ osiris-data          (internal: true)                     │
  │  osiris-api, osiris-core, osiris-orchestrator, neo4j, nats│
  └───────────────────────────────────────────────────────────┘
```

**네트워크 배치표**

| 컴포넌트 | dev-net | osiris-edge | osiris-data | osiris-agents | 근거 |
|---|:---:|:---:|:---:|:---:|---|
| `osiris-gw` | ✅ | ✅ | | | 인바운드 전용 이중 홈 |
| `osiris-api` | | ✅ | ✅ | | 엣지와 데이터를 잇는 유일한 지점 |
| `osiris-core` | | | ✅ | ✅ | 에이전트 대면 포트(gate/graph-write)는 **별도 포트 + 에이전트 토큰 인증** |
| `osiris-orchestrator` | | | ✅ | | **에이전트가 도달 불가.** docker.sock 보유자이므로 |
| `neo4j` | | | ✅ | | **에이전트가 도달 불가.** 라우팅 자체가 없음 |
| `nats` | | | ✅ | ✅ | 불가피한 이중 홈. 실질 강제는 NATS ACL |
| `osiris-egress` | ✅ | | | ✅ | 아웃바운드 전용 이중 홈. I5: `osiris-llm`에도 이중 홈(§9.2) — dev-net 멤버 수는 그대로 2(D10 불변) |
| 에이전트 컨테이너 | | | | ✅ | **단 하나의 네트워크** |
| `osiris-omniroute` (I5) | | | | | **`osiris-llm` 하나뿐**(신설). 에이전트는 `osiris-agents`에 없어 직접 도달 불가 — `osiris-egress`가 CONNECT로만 중계(§8.3 문구 불변). 자신의 아웃바운드도 `osiris-egress`를 프록시로 사용(A3 예외 없음, §9.2) |
| `osiris-omniroute-lo` (I5) | | | | | `osiris-llm` + 전용 `osiris-omniroute-lo` 망(non-internal, 이 다리 하나만 멤버) 이중 홈. socat 하나만 도는 순수 TCP 중계 — 애플리케이션 로직이 없어 이 컨테이너가 뚫려도 할 수 있는 일은 `osiris-omniroute:20128` 중계뿐 |
| `osiris-adapter-plane`/`osiris-adapter-telegram` (신설, `50-integration-adapters.md`) | | | | | **이 표의 네 열 어디에도 속하지 않는다** — 신설 `osiris-adapters`(internal) 망 하나뿐. 상세 배치표(egress 이중 홈 포함)는 `50-integration-adapters.md` §2.2가 소유(이 표는 그 문서를 가리키기만 하고 복제하지 않는다) — D10 불변(dev-net 멤버는 여전히 `osiris-gw`/`osiris-egress` 둘뿐) |

**코디네이터 권고에서 한 걸음 확장한 부분과 그 이유**: 권고는 "`osiris-gw`만 이중 홈"이었다. 여기에 `osiris-egress`를 **아웃바운드 전용 이중 홈으로 추가**한다. 이유는 §10의 장애 요구 때문이다 — *"상위 리버스 프록시가 죽어도 내부 조직(에이전트)은 계속 돌아야 한다."* omniroute를 공개 이름(`omniroute.example.com`)으로 접근하면 그 트래픽이 상위 리버스 프록시를 헤어핀으로 통과하게 되어, **상위 리버스 프록시 장애가 곧 전 에이전트 정지**가 된다. 이는 명시된 요구를 위반한다. 따라서 egress 프록시가 `dev-net` 위에서 `omniroute:20128`에 **직접** 도달하게 한다.

**핀홀을 좁히는 구체 조치**
- egress 프록시(HTTP CONNECT)는 **소스 서브넷 ACL**로 `osiris-agents` 대역(`172.31.240.0/24`, compose에 고정)에서 온 요청만 받는다. `dev-net` 쪽에서 들어오는 요청은 전부 거부 → 남이 우리 프록시를 못 쓴다.
- egress 프록시는 `dev-net` 인터페이스에 **어떤 포트도 publish하지 않는다**(연결을 걸기만 하고 받지 않는다).
- 허용목록(도메인:포트)만 CONNECT 성립. `dev-net` 대상은 **`omniroute:20128` 단 하나**. 나머지 dev-net 호스트는 허용목록에 없으므로 프록시를 통한 피벗이 불가능하다.
- 모든 CONNECT 시도(허용/거부)를 감사 로그로 남기고 `ops.egress` 서브젝트로 발행 → 콘솔에서 이상 접근 관찰 가능.

**[2단계]에서 이 경계의 표현**

```yaml
# 팀 Namespace 기본 정책: egress 전면 deny
kind: NetworkPolicy
spec:
  podSelector: {}
  policyTypes: [Egress, Ingress]
  egress:
    - to: [{ namespaceSelector: {kubernetes.io/metadata.name: kube-system},
             podSelector: {k8s-app: kube-dns} }]
      ports: [{protocol: UDP, port: 53}]
    - to: [{ namespaceSelector: {kubernetes.io/metadata.name: osiris-system},
             podSelector: {app: nats} }]
      ports: [{protocol: TCP, port: 4222}]
    - to: [{ namespaceSelector: {kubernetes.io/metadata.name: osiris-system},
             podSelector: {app: osiris-egress} }]
      ports: [{protocol: TCP, port: 3128}]
    - to: [{ namespaceSelector: {kubernetes.io/metadata.name: osiris-system},
             podSelector: {app: osiris-core} }]
      ports: [{protocol: TCP, port: 8090}]        # 에이전트 대면 포트만
  ingress:
    - from: [{ namespaceSelector: {kubernetes.io/metadata.name: osiris-system},
               podSelector: {app: osiris-core} }]
```

즉 **1단계의 "분리된 도커 네트워크"가 2단계에서 "기본 deny + 4개 예외의 NetworkPolicy"가 된다.** 경계의 *의미*는 같고 *해상도*만 올라간다. `dev-net`은 2단계에서도 `osiris-gw` 컨테이너와 `osiris-egress`만 붙는다(둘 다 k8s 밖의 docker 컨테이너로 남기거나, egress만 `hostNetwork` 없이 별도 docker 컨테이너로 유지 — 어느 쪽이든 에이전트 Pod는 CNI 네트워크 안에 갇힌다).

> **`[2026-07-28 K2 전환]` 두 가지가 더 이상 참이 아니다.**
> 1. **예외 개수.** 위 문단은 "4개 예외"라고 세지만, kube-dns는 실무에서 UDP/53뿐 아니라 TCP/53(대형 응답 폴백)도 필요해 **예외가 4개가 아니라 5개**가 된다 — kube-dns 규칙 자체가 필수이면서 하나의 `to:` 블록으로는 부족하다. K2/K13 전면 재작성에서 이 표는 다시 실사해야 한다.
> 2. **"경계의 의미는 같다"는 비교가 성립하지 않는다.** 도커 `internal: true` 네트워크는 **기본이 거부**(bridge 밖으로 라우팅 경로 자체가 없음)인 반면, k8s `NetworkPolicy`는 **기본이 허용**이며 어떤 정책도 없으면 전체 허용이고, 정책이 있어도 **CNI가 NetworkPolicy를 지원하지 않으면 조용히 무시된다**(flannel 등). 즉 위 yaml은 "선언되어 있고 CNI가 이를 실제로 강제하는 경우"에만 도커 internal망과 동등하며, 그 전제 자체가 CNI 선택에 달려 있다 — `70-kubernetes-deployment.md` §3.1/§3.2가 정하는 CNI 선택(및 그것이 NetworkPolicy를 강제하는지)을 확인하기 전까지 이 절의 "동등하다"는 서술을 그대로 신뢰해서는 안 된다. kube-dns 예외가 5개째로 필수 추가된다는 점은 §3.2/§7.3이 명문화했다.

### 2.7 SSE 프록시 체인 요구사항 (D20)

SSE는 **두 홉 모두**에서 아래를 만족해야 한다. **한 홉만 놓쳐도 실시간이 죽는다** (이벤트가 4KB 프록시 버퍼에 갇혀 화면이 멈춘 것처럼 보인다).

| 지시 | 값 | 없으면 |
|---|---|---|
| `proxy_buffering` | `off` | 이벤트가 버퍼에 고여 수십 초 지연 또는 무한 대기 |
| `proxy_cache` | `off` | 스트림이 캐시되어 두 번째 클라이언트가 멈춤 |
| `proxy_read_timeout` / `proxy_send_timeout` | `3600s` | 기본 60초에 스트림이 끊김(재연결 폭풍) |
| `proxy_http_version` | `1.1` + `Connection ""` | HTTP/1.0으로 다운그레이드되며 chunked 실패 |
| `gzip` | `off` (또는 `text/event-stream` 제외) | gzip 버퍼링으로 이벤트가 지연 |
| `X-Accel-Buffering` 응답 헤더 | `osiris-api`가 `no`로 내려줌 | 프록시 conf 실수에 대한 2차 방어 |

**애플리케이션 측 보강**
- `osiris-api`는 SSE 연결 직후 **즉시** `event: hello`를 보낸다. 콘솔이 500ms 안에 받지 못하면 브라우저 개발자 콘솔에 "프록시 버퍼링 의심" 경고를 남긴다 — conf 실수를 배포 직후에 잡기 위한 자가진단.
- 스트림에 **15초마다 `: keepalive` 코멘트 라인**을 상시 발행한다. 중간 장비의 idle timeout과 `cluster.heartbeat`(10초) 부재 판정을 구분 가능하게 한다.
- 재연결은 `Last-Event-ID`(JetStream 시퀀스, D19)로 이어받는다.

### 2.8 클라이언트 실제 IP — 2홉 신뢰 체인

```
브라우저(203.0.113.9)
  → <upstream-proxy> : X-Forwarded-For: 203.0.113.9            X-Real-IP: 203.0.113.9
  → osiris-gw        : X-Forwarded-For: 203.0.113.9, <upstream-proxy ip>
                ★ 여기서 osiris-gw가 재작성 →                  X-Real-IP: 203.0.113.9  (<upstream-proxy>가 준 값을 신뢰)
  → osiris-api       : X-Real-IP 만 읽는다
```

**규칙: 각 홉은 바로 앞 홉 하나만 신뢰한다.**
- `osiris-gw`: `X-Real-IP`를 상위 리버스 프록시가 준 값 그대로 통과시킨다(`proxy_set_header X-Real-IP $http_x_real_ip;`). 상위 리버스 프록시 외의 출처에서 온 요청은 애초에 도달할 수 없다(dev-net 안이고, `Host` 검사도 걸린다).
- `osiris-api`: **`X-Forwarded-For`를 파싱하지 않는다.** `osiris-gw`가 정규화한 `X-Real-IP` 하나만 읽는다. XFF 체인 파싱은 홉 수 오설정에 취약하므로 아예 쓰지 않는다.
- 신뢰 프록시 지정: `osiris-api`는 `TRUSTED_PROXY_CIDR`(= `osiris-edge` 대역) 밖에서 온 TCP 연결의 `X-Real-IP`를 **무시**하고 peer IP를 쓴다.

**용도 제한**: 클라이언트 IP는 **감사 로그와 레이트리밋에만** 쓴다. 인가 결정(누가 무엇을 할 수 있는가)에는 쓰지 않는다 — 스푸핑 리스크를 인가 경로에서 원천 제거한다.

### 2.9 단일 공개 도메인 원칙 — 프레임워크당 도메인 엔드포인트 하나 (I5, 사용자 확정)

**결정: OSiris는 공개 엔드포인트를 `osiris.example.com` 단 하나만 갖는다. 프레임워크 내부 구성요소(콘솔, `osiris-api`, 그리고 I5가 신설한 `osiris-omniroute` 대시보드 등)는 전부 그 도메인 하위 경로로 라우팅된다.** 신규 서브도메인 할당(`console.osiris.example.com`류)은 **선택지에서 제외한다** — 인증서 SAN 재발급 회피는 부수적 이유이고, 진짜 이유는 "프레임워크 레벨은 하위 경로로 라우팅되어야지, 도메인 엔드포인트가 여러 개인 것은 설계상 말이 안 된다"는 원칙 자체다(사용자 확정, 근거는 IMPLEMENTATION-NOTES.md I5).

**경로 네임스페이스 배분** (`osiris-gw`가 이 분기를 수행 — `gw/conf.d/default.conf`. **I5 3차 수정으로 아래 표가 다시 바뀌었다** — §9.7/§9.8, IMPLEMENTATION-NOTES I5 §57~§63 참고):

| 경로 | 목적지 | 상태 |
|---|---|---|
| `= /` (정확히 이 경로만) | 콘솔 슬롯 | `osiris-api`/콘솔 미구현이라 손으로 관리하는 정적 랜딩(`gw/html/index.html`)이 대신 서빙 중 |
| `/_api/` | **OSiris API** — 원래 `/api/`였다가 이전됨(이유는 아래) | `osiris-api` 미구현. 컴포넌트가 뜨면 이 위치의 프록시 대상이 될 것(내부 구현은 `40-console-ia.md`가 정의한 그대로 `/api/*`, 게이트웨이가 `/_api/*`→`/api/*`로 벗겨서 전달) |
| `/_api/events` (SSE) | OSiris SSE(D19/D20) | 위와 동일 — 별도 location 없이 `/_api/` prefix가 커버(값 불변) |
| `/pre-status` | 플랫폼 상태 진단 정적 페이지 | `gw/render-status.sh`가 굽는다 |
| `/favicon.*`, `/osiris-mark.svg` | OSiris 브랜드 정적 파일 | 콘솔 슬롯/진단 페이지가 참조 |
| `/omni/` | `osiris-omniroute` 대시보드(전체 페이지/에셋/클라이언트 라우팅) | **서브패스가 실제로 성립한다** — §9.7의 소스 재빌드(`omniroute-build/Dockerfile`)로 `OMNIROUTE_BASE_PATH=/omni`를 빌드 타임에 구웠다(벤더의 사전빌드 이미지는 런타임 env로는 안 됨을 먼저 실측 확인, §9.7) |
| **그 외 전부** (`/api/*`, `/v1/*`, `/a2a`, `/dashboard/*`, `/login`, `/status`, ... 위 예약 경로 밖 전부) | **omniroute** — `/omni` 접두사를 붙여 전달 | **I5 3차 수정**: omniroute의 클라이언트(브라우저) 코드가 Next.js `basePath`를 안 타는 리터럴 절대경로를 광범위하게 하드코딩하고 있다(`/app/src` 420개 파일이 `/api/`만 리터럴로 참조, 그 외 `/v1/models`·`/a2a`·`<a href="/dashboard/...">` 등 — 실측, 추측 아님). 처음엔 발견한 것들(`/api/`, `/v1/`, `/a2a`)만 개별 location으로 리라이트했지만, 순수 내비게이션 링크(`/dashboard/*`, `/status`, `/login`, `/docs`, `/terms`, ...)까지 나오는 걸 보고 **화이트리스트 대신 블랙리스트로 재설계**했다 — OSiris가 명시적으로 예약한 위 다섯 부류만 빼고 나머지는 기본적으로 omniroute로 간다. omniroute 소스는 한 줄도 고치지 않았다 — 경로 소유권만 뒤집었다 |

**콘솔 계약 변경 — 인계 (사용자가 반영시킴)**: OSiris API의 공개 경로가 `/api/`에서 `/_api/`로 바뀌었다. **`40-console-ia.md`/`41-console-design-system.md`는 이 워커가 고치지 않는다**(다른 워커 소유) — 그 문서들이 여전히 `/api/`를 전제하고 있다면, `osiris-api`의 **내부 구현**은 그대로 `/api/*`로 두고(그 문서들을 고칠 필요 없음), 콘솔의 **클라이언트(브라우저)** 쪽 base URL만 `/_api/`로 바꿔야 한다 — `osiris-gw`가 `/_api/*` → `/api/*`로 벗겨서 osiris-api에 전달한다.

**다음 워커(콘솔)에게 — 중요한 제약**: 콘솔이 실제 SPA가 되어 `/`(정확히 그 경로) 밖의 최상위 경로(예: `/dashboard`, `/login`)를 쓰게 되면 위 "그 외 전부 → omniroute" 블랙리스트와 충돌한다. 그때는 콘솔이 쓸 최상위 경로들을 그 catch-all보다 먼저 오는 명시적 location으로 예약하거나, 콘솔 자신도 서브패스(`/console/`류)를 받는 방향으로 재검토할 것.

**omniroute 라우팅에 실제 WS 업그레이드/SSE 경로가 있다(추측 아님, 소스 확인)**: `/app/server-ws.mjs`가 `http.Server`의 `upgrade` 이벤트를 감싸고, `/app/responses-ws-proxy.mjs`의 `RESPONSES_WS_PUBLIC_PATHS = {"/responses","/v1/responses","/api/v1/responses"}`가 WS 업그레이드 대상이다. `text/event-stream` SSE는 `/app/src/app/api/v1/messages/route.ts`(에이전트가 실제 쓰는 그 엔드포인트) 등에 있다. 그래서 위 catch-all과 `/omni/` 블록 둘 다 `proxy_buffering off`+SSE 타임아웃(D20과 동일 값)뿐 아니라 `Upgrade`/`Connection` 헤더(nginx의 표준 `map $http_upgrade $connection_upgrade` 관용구)까지 갖춘다.

**에이전트 추론 경로와는 완전히 무관하다**: `OSIRIS_GATEWAY_BASE_URL`을 쓰는 에이전트는 `osiris-egress`의 CONNECT 터널로 `osiris-omniroute`에 직접 닿고 `osiris-gw`를 전혀 거치지 않는다(§8.3). 위 라우팅 전부는 브라우저가 `osiris-gw`를 거쳐 omniroute 대시보드를 볼 때만 적용된다.

**다음 워커에게**: 새 UI/API 컴포넌트를 추가할 때 새 서브도메인을 만들지 말 것. 위 표에 행을 추가하고(예약 경로로) `osiris-gw`의 `location` 블록으로 라우팅할 것 — omniroute 쪽 catch-all보다 먼저 오게 해야 한다.

**[2026-07-27 신설] 통합 어댑터(Plane·텔레그램, `50-integration-adapters.md`)는 이 표에 행이 필요 없다 — 소스로 확인**: 텔레그램은 `getUpdates` **long-polling**으로 확정되어(50 §4.4) 어댑터가 `api.telegram.org`에 아웃바운드로 계속 폴링할 뿐, `osiris-gw`에 새 인바운드 경로(webhook)를 열 필요가 없다. Plane 미러도 어댑터가 core의 `:8091`(§8.3)과 Plane API를 왕복할 뿐 브라우저가 거치는 공개 경로가 아니다. **재검토 트리거**: 향후 long-polling을 지원하지 않고 webhook만 지원하는 어댑터가 추가되면, 그때 위 표에 `/_hooks/<adapter>` 같은 새 예약 경로를 추가해야 한다(현재는 필요 없다는 사실만 기록, `50-integration-adapters.md` §7 Q4 인계).

---

## 3. 시스템 컴포넌트

### 3.1 전체 다이어그램 [1단계 기준]

```
   인터넷 ─────▶ <upstream-proxy> (dev-net, TLS 종단, OSiris 소유 아님)
                     │ 평문 HTTP
═════════════════════╪══════════════════════════════════════════════════════════════
  호스트 계층 (컨테이너 밖)
   ┌─────────────────┼───────────────────────────────────────────────────────────┐
   │ systemd: osiris-watchdog   (컨테이너 아님. dockerd·컨테이너 헬스·엔드포인트   │
   │   감시, 대역외 알림 — <upstream-proxy>를 경유하지 않는 경로)                 │
   │ dockerd                                                                      │
   └─────────────────┼───────────────────────────────────────────────────────────┘
═════════════════════╪══════════════════════════════════════════════════════════════
                     ▼
  ┌──────────────────────────────┐        dev-net
  │ osiris-gw   (nginx)  ★이음새 │◀───────────────────────┐
  │  dev-net + osiris-edge       │                        │
  │  SPA 서빙 · /api/* 프록시     │                        │
  └──────────────┬───────────────┘                        │
                 │ osiris-edge (internal)                 │
  ┌──────────────▼───────────────────────────────────┐    │
  │ osiris-api    REST + SSE + 인증                   │    │
  │   · SSE 팬아웃(JetStream seq = Last-Event-ID)     │    │
  │   · 좌석 환산 · cluster.heartbeat 발행             │    │
  └──────────────┬───────────────────────────────────┘    │
                 │ osiris-data (internal)                 │
  ┌──────────────▼──────────┐   ┌──────────────────────┐  │
  │ Neo4j CE                │◀─▶│ NATS JetStream       │  │
  │ 조직 의도의 진실         │   │ INBOX/TASKS/         │  │
  │ (쓰기는 core 경유만)     │   │ TRANSCRIPT/COST      │  │
  └──────────────▲──────────┘   └────▲─────────────▲───┘  │
                 │                    │             │      │
  ┌──────────────┴────────────────────┴──────┐      │      │
  │ osiris-core   ※ LLM 호출 없음             │      │      │
  │  ├ graph-write API (유일한 Neo4j 쓰기 경로)│      │      │
  │  ├ assignment-engine (결정적 배정+근거)    │      │      │
  │  ├ task-dispatcher / rollup-engine        │      │      │
  │  ├ approval-service / notify-adapter      │      │      │
  │  └ :8090 에이전트 대면 포트(토큰 인증)      │──────┼──┐   │
  └───────────────────────────────────────────┘      │  │   │
  ┌───────────────────────────────────────────┐      │  │   │
  │ osiris-orchestrator  ※ docker.sock 보유    │      │  │   │
  │  ├ WorkloadDriver = DockerDriver           │      │  │   │
  │  ├ 수렴 루프(desired vs actual)             │      │  │   │
  │  └ liveness 집행 · 자격증명 회전 · NATS 사용자│      │  │   │
  └───────────────────┬───────────────────────┘      │  │   │
                      │ Docker Engine API             │  │   │
                      ▼                               │  │   │
  ┌──────────────────────────────────────────────────┼──┼───┼──┐
  │ osiris-agents (internal: true, 172.31.240.0/24)  │  │   │  │
  │  ┌ agent-master ─┐ ┌ 팀장 ─┐ ┌ 팀원 ─┐            │  │   │  │
  │  │ PID1 supervisor│ │ 동일  │ │ 동일  │────────────┘  │   │  │
  │  │  └ claude -p   │ │       │ │       │───────────────┘   │  │
  │  │ vol /workspace │ │       │ │       │                   │  │
  │  └────────────────┘ └───────┘ └───────┘                   │  │
  │                          │ 모든 외부 통신                   │  │
  │  ┌───────────────────────▼──────────────────────────────┐  │  │
  │  │ osiris-egress (HTTP CONNECT, 소스ACL + 도메인 허용목록)│──┼──┘
  │  └──────────────────────────────────────────────────────┘  │
  └────────────────────────────────────────────────────────────┘
                              │ dev-net (아웃바운드 전용 핀홀)
                              ▼
                   omniroute:20128        인터넷(github/npm/pypi/SMTP)
```

**[2단계] 차이**: `osiris-gw`/`osiris-egress`는 docker 컨테이너로 그대로 남고, 그 안쪽이 k3s로 바뀐다. `osiris-orchestrator` → `osiris-operator`(CRD 기반), 도커 네트워크 → Namespace+NetworkPolicy, named volume → PVC(Longhorn), `osiris-api` → 서버 노드 DaemonSet + hostPort 8443.

**[2026-07-27 정정] `notify-adapter`는 위 다이어그램의 `osiris-core` 박스 안에 있지만, 그 이름이 오독을 유발한다.** `50-integration-adapters.md`가 확정한 대로 텔레그램 등 실제 외부 채널 API 호출은 `osiris-core` 안에서 일어나지 않는다 — 아래 §3.2와 §8.3에서 경계를 명시한다.

### 3.2 컴포넌트별 책임과 경계

| 컴포넌트 | 책임 | 명시적으로 하지 않는 것 |
|---|---|---|
| `osiris-gw` (nginx) | 이음새. SPA 서빙, `/api/*` 프록시, 프록시 신뢰 정규화, SSE 통과 | 인증·인가 없음. 상태 없음 |
| `osiris-api` | 콘솔 REST/SSE 계약, 인증·세션, 좌석 환산, `cluster.heartbeat` | 비즈니스 결정 없음. 쓰기는 core로 위임 |
| `osiris-core` | 결정적 로직 전부. Neo4j 쓰기 단일 관문. **`notify-adapter`는 누구에게/언제/어느 심각도로 알릴지 결정하고 `NotifiableEvent`를 만들어 어댑터 대면 포트(`:8091`)에 얹는 것까지만**(§8.3) | **LLM을 호출하지 않는다** — 이미지에 `claude` 바이너리·게이트웨이 자격증명 없음(D12). **외부 채널(텔레그램 Bot API 등)에 실제로 HTTP를 쏘지 않는다** — 그건 격리된 어댑터 컨테이너의 일이다(`50-integration-adapters.md` E2/E3, [2026-07-27 정정]) |
| `osiris-orchestrator` **[1단계]** | desired↔actual 수렴, 컨테이너 생명주기, liveness 집행, 자격증명/NATS 사용자 발급 | 배정·계획 없음. LLM 없음. 에이전트 네트워크에 붙지 않음 |
| `osiris-operator` **[2단계]** | 위와 동일 책임을 CR reconcile로 | 플랫폼 컴포넌트(Neo4j/NATS/Longhorn) spec을 수정하지 않음(읽기만) |
| `osiris-egress` | 외부 egress 도메인 허용목록 강제, 전 외부통신 감사 | TLS 종단(MITM) 안 함 — CONNECT만 중계 |
| `osiris-watchdog` | 컨트롤 플레인 **밖**에서 생사 관측, 대역외 알림, 제한적 재기동 | 조직 로직 없음. 컨테이너 아님 |
| `osiris-supervisor` | 에이전트 컨테이너의 PID 1. 인박스 소비, `claude` 실행, 헬스, 체크포인트 | 어떤 태스크를 받을지 스스로 정하지 않음. **Neo4j에 접속하지 않는다** |
| `agent-master` | 대표와의 대화, WBS 분해, 재계획, 완료보고 요약 | **배정하지 않는다.** 후보 계산·선택은 core |
| Neo4j | 조직·역량·WBS·배정근거·권한 선언의 저장 | 런타임 상태의 진실이 아님(캐시만). 직접 쓰기를 받지 않음 |
| NATS JetStream | 인박스, 이벤트, 트랜스크립트, 비용 | 조직 상태 저장소가 아님. 하트비트/로그는 영속화하지 않음 |

### 3.3 LLM 추론 지점 vs 결정적 코드 지점 (compute-over-inference)

| 기능 | 처리 | 비고 |
|---|---|---|
| 대표 자연어 지시 해석, WBS 분해 | **LLM** (agent-master) | 비정형 입력 구조화가 본질 |
| 태스크 → 요구 capability 추출 | **LLM** | 단, 선택지는 `:Capability` 카탈로그로 **제한**. 자유 문자열 금지 |
| 후보 필터링·점수·선택 | **코드** (assignment-engine) | 런타임 토큰 0 |
| tie-break | **코드** (고정 체인, 최종 tiebreak는 `partyId` 사전순) | 난수 금지. 같은 입력 → 항상 같은 결과 |
| 배정 근거 문장 | **코드** (템플릿) | 콘솔 ⓘ는 규칙명 + 후보 비교표. 산문 불필요 |
| WBS 진척 롤업 | **코드** | 산술 |
| 좌석 환산 | **코드** | 산술 |
| 에이전트 실작업 | **LLM** (`claude -p`) | 본연의 작업 |
| 작업 결과 상태 보고 | **코드** (`stream-json` 파싱) | "무엇을 했는지 요약해줘" 추가 턴 금지 |
| 인박스 확인 / 하트비트 | **코드** (NATS) | 유휴 토큰 0 |
| 승인 필요 여부 판정 | **코드** (grant-catalog의 `gate` 필드) | LLM이 "이건 승인 필요 없을 듯"이라 판단하는 일이 없어야 함 |
| 대표용 완료보고 요약문 | **LLM** | 사람 가독성이 목적 |

### 3.4 WBS 발행의 2단 구조 — LLM은 계획을 심고, 발행·배정은 결정적 코드다 `[2026-08-13 신설]`

> **근거**: 같은 문제(AI를 조직 구성원으로 운용)를 상용 그룹웨어에서 2주간 실전 배치해 얻은 외부 프로젝트의 실측 교훈 — `Blueurban-GroupWare/DESIGN-ORCHESTRATION.md`(이하 "Blueurban AaaM") D16·D19. **OSiris 코드베이스의 실측이 아니라 자매 프로젝트의 실측이며, 그 구분을 유지한 채 인용한다.**

위 §3.2·§3.3이 이미 "`agent-master`는 WBS 분해까지만 하고 배정은 하지 않는다"(§3.2 표)와 "WBS 분해는 LLM, 후보 필터링·점수·선택은 코드"(§3.3 표)를 정해 두었다. 이번 개정은 그 경계선을 **분해와 배정 사이의 세 번째 지점 — "발행(그래프 영속화)"**까지 명시적으로 확장한다. 지금까지 이 문서는 "누가 배정하는가"만 결정했고 "누가, 언제 `:WorkItem`을 그래프에 쓰는가"는 암묵적으로 `agent-master`의 분해 산출물에 맡겨져 있었다. **그 암묵을 명문화하고, 발행 단계를 결정적 코드로 못박는다.**

**Blueurban이 기각한 설계 (D19)**: 매일 09:00 배정 에이전트가 그날 업무를 발행하는 방식. 기각 근거 둘 — ① **반결정성**: 같은 계획을 어제와 다르게 쪼갠다(LLM 재호출마다 분해 결과가 흔들린다). ② **중복 발행 방지 수단 부재**: 같은 노드가 두 번 발행되는 것을 막을 구조가 없었다.

**Blueurban의 재배치 (D16)**: 09:00 배치의 역할을 "배정기"에서 **"감독관"**으로 바꿨다 — 발행 자체는 이벤트 구동으로 하고, 정기 배치는 정체·누락 점검과 브리핑만 한다.

**멱등성의 얻는 방식**: Blueurban은 노드당 티켓 1개를 **구조적으로** 강제해서 얻었다(`task:nodeIndex` 키로 중복 발행을 원천 차단). "평가가 몇 번 돌아도, 이벤트와 정기 점검이 겹쳐도 중복 발행되지 않는다."

**부트스트랩 함정 — OSiris도 반복할 수 있는 유형이므로 명문화한다**: Blueurban은 라이브 검증에서야 "이벤트 구동 훅만으로는 최초 발행 트리거가 없어 영구히 0건 발행된다"는 것을 발견했다. 이벤트가 상태 변화에 반응하는 구조라면, **누구도 최초의 상태 변화를 일으키지 않는 한 아무 이벤트도 없다.** 해법은 **주기적 재계산(reconcile) 루프**다. OSiris가 이벤트 구동 발행(§7의 `org.revision`/`task.*.event` 소비)만 설계하고 이 재계산 루프를 빠뜨리면 같은 함정에 빠진다 — **이 문서는 그 루프가 필요하다는 것만 명문화하고, 구체 배선(어느 컴포넌트가 도는가)은 갭으로 남긴다.**

**두 단계**:

1. **LLM 단계 (`agent-master`, 무변경)** — 대표의 자연어 지시를 해석해 WBS를 그래프 노드 후보로 제안한다. §3.2·§3.3의 기존 서술 그대로다. **사람 확정을 거친다** — 콘솔에서 대표가 제안된 트리를 승인해야 다음 단계로 넘어간다(온보딩 커밋이 이미 쓰는 "제안 → 사람 확정 → 트랜잭션 커밋" 패턴, `30-onboarding-provisioning.md` §5.2와 같은 모양).
2. **결정적 코드 단계 (osiris-core, 신설 개념·미구현)** — 심긴 계획을 순수 계산으로 평가해 `:WorkItem`을 발행하고 `20 §8`의 `assign()`으로 배정한다. **LLM 호출이 없다.** 이것은 `20 §8.2`가 이미 `assign()` 자신에 대해 선언한 원칙 — *"이 알고리즘은 LLM을 호출하지 않는다. 런타임 토큰 비용 0"* — **의 연속·확장**이다. 원칙을 한 줄로 다시 쓰면: **"그래프는 결정적 코드가 굴리고, LLM은 판단이 필요한 곳에만 쓴다."**

**발행 조건 — OSiris 실제 스키마 위에서 (Blueurban의 6조건을 참고하되 발명하지 않는다)**:

Blueurban의 6조건(노드타입=task ∧ status=pending ∧ 모든 선행 done ∧ blocks 없음 ∧ 미발행 ∧ 담당자 지정)을 `20-ontology-schema.md`의 실제 `:WorkItem` 속성(§3.4)·관계(§4)·`status` enum과 대조한 결과:

| Blueurban 조건 | OSiris 번역 | 근거/상태 |
|---|---|---|
| 노드타입 = task | `:WorkItem.kind ∈ {deliverable, checklistItem}` — `20 §6.1`이 "실무적으로는 deliverable 또는 checklistItem에 붙는다"고 이미 명시. `project`는 롤업 컨테이너일 뿐 발행 대상이 아니다 | 직접 대응 |
| status = pending | `:WorkItem.status == 'planned'` — `20 §3.4`가 정의한 enum의 첫 상태. `pending`이라는 값은 OSiris에 없고 `planned`가 그 자리다 | 직접 대응(어휘만 치환) |
| 모든 선행 done | `(:WorkItem)-[:DEPENDS_ON {kind:'fs'}]->(:WorkItem)`의 모든 도착 노드가 `status == 'done'` | **[갭]** — 관계 자체는 있다(`20 §4`). 그러나 `20 §8.2`의 `assign()` 의사코드(§8.2~§8.4)는 `DEPENDS_ON`을 **한 번도 참조하지 않는다** — 배정은 "이 태스크가 지금 배정 가능한가"만 보고, "선행이 끝났는가"는 아무 코드도 검사하지 않는다. 발행 단계가 신설되면 이 검사를 **처음으로** 어딘가에 넣어야 한다는 뜻이고, 지금은 그 자리가 없다 |
| blocks 없음 | 대응 관계 없음 | **[갭]** — `20 §4`의 관계 전체 목록에 `BLOCKS`(또는 역방향 차단) 관계가 없다. `DEPENDS_ON`은 순방향 선행관계만 표현하며 역방향 차단을 표현하지 않는다. `:WorkItem.status == 'blocked'`는 상태값이지 사전 조건 입력이 아니다(그 상태 자체가 `assign()`의 출력, `20 §8.6` FB3/FB4). **이 조건은 BLOCKS 관계를 요구하나 현재 스키마에 없다 — 스키마를 새로 발명하지 않는다** |
| 미발행 | `:WorkItem.status == 'planned'`이 이미 이 역할을 겸한다 — `assigned` 이후로는 재발행 대상이 아니다. Blueurban의 `task:nodeIndex`처럼 별도 멱등성 키가 있는 것은 아니고, **`status` 필드 자체가 그 전이("발행 전/후")를 표현**한다. 다만 이것이 멱등성을 구조적으로 보장하려면 "발행 후보 조회 + 상태 전이"가 **단일 트랜잭션**이어야 한다 — `20 §8.7`이 이미 "단일 트랜잭션으로 `:AssignmentDecision`+`:Assignment`+`:WorkItem.status` 변경을 커밋한다"고 못박아 두었으므로, 이 트랜잭션 경계 안에서 발행 스캔을 수행하면 Blueurban이 겪은 중복 발행이 구조적으로 재발하지 않는다. **[갭]** 이 "발행 후보를 스캔하는 함수" 자체는 아직 없다 — `assign()`(`core/internal/graphdb/assign_engine.go`)은 이미 결정된 단일 `Task`를 받아 배정만 하지, "어느 `:WorkItem`이 지금 발행 가능한가"를 그래프에서 스캔하는 코드는 존재하지 않는다 |
| 담당자 지정 | 해당 없음 — **역전된 관계** | OSiris에서는 담당자 지정이 발행의 **선행조건이 아니라 결과**다(`assign()`의 출력이 배정이다). Blueurban의 이 조건은 "미리 사람이 담당자를 정해 둔다"는 그쪽 조직의 운영 방식을 반영한 것이고, OSiris는 애초에 자동 배정 설계이므로 **이 조건은 이식하지 않는다** — 아키텍처 차이로 기록하고 갭으로 다루지 않는다 |

**발행 단계가 `assign()`을 호출할 때 걸리는 기존 제약 두 가지(이미 구현됨, 코드 확인)**:

- `core/internal/graphdb/assign_engine.go`의 `AssignTx(ctx, tx neo4j.ManagedTransaction, task assign.Task, asOf time.Time) (Result, error)`가 실제 시그니처다 — 호출자의 이미 열린 트랜잭션을 받는다(`30 §5.2` 리허설이 같은 이유로 요구하는 패턴). 발행 단계를 설계·구현할 워커는 이 시그니처에 맞춰야 하며, 별도 트랜잭션을 새로 열지 않는다.
- **F5**: `core/internal/assign/evaluate.go`의 F5(`scopeAllows`)는 `task.SecurityLabel != "public"`이면 **무조건** `SCOPE_VIOLATION`을 추가한다(코드 주석: "a real securityLabel value" 도입 전까지의 임시 전면 차단). 즉 **`securityLabel`이 `public`이 아닌 `:WorkItem`은 현재 배정 자체가 불가능하다.**
- **F2 / `external_effect`**: 같은 파일의 `GatePermissionFor`가 매핑 출처를 확정할 수 없어 **항상 `("", false)`를 반환**하고, 그 결과 `actionClass: external_effect`인 모든 태스크는 `NOT_PERMITTED:unresolved-gate`로 **무조건 배정 불가**다(fail-closed, `80-action-layer.md` G-A11과 연결된 미해결 사안).

발행 단계를 새로 설계·구현하는 워커는 이 두 제약을 **전제로** 설계해야 한다 — "발행은 됐는데 배정만 항상 실패한다"가 새 버그가 아니라 기존에 이미 그렇게 되어 있는 상태라는 뜻이다.

---

## 4. 부트스트랩

### 4.1 [1단계] `docker compose up` 한 번

> **`[2026-07-28 K2 전환]`** 아래 `osiris-up` 스크립트와 그것이 수행하는 절차(compose 네트워크 생성 등)는 K2(전면 재작성)로 **폐기된다.** compose 경로 자체를 만들지 않으므로 이 스크립트는 존재하지 않는다. 설치 절차는 `helm install`(또는 그에 준하는 k8s 매니페스트 적용)로 대체된다 — 구체 순서·차트 구조는 `70-kubernetes-deployment.md`가 소유한다. 아래 절차는 이력으로 남기되, preflight가 확인하던 항목(디스크·RAM 하한, 사전 리소스 존재 확인) 자체의 **목적**은 k8s 설치 전 점검으로 형태를 바꿔 이어진다.

```
$ git clone <osiris> && cd osiris
$ cp .env.example .env && $EDITOR .env      # 도메인, dev-net 이름, 알림 채널, 리소스 상한
$ ./osiris-up

 0. preflight
    docker ≥ 24 / compose v2 · dev-net 존재 확인 · 디스크 60GiB · RAM 8GiB
    (실패 시 즉시 중단 — 부분 설치 금지)
 1. 내부 네트워크 생성
    docker network create --internal --subnet 172.31.238.0/24 osiris-edge
    docker network create --internal --subnet 172.31.239.0/24 osiris-data
    docker network create --internal --subnet 172.31.240.0/24 osiris-agents
 2. 호스트 상태 디렉토리 생성 (0700, orchestrator 전용)
    ./state/{creds,config,backup}
 3. docker compose up -d          ← 코어 7개만(+ `osiris-omniroute`도 빈 DB로 기동, §9.0/§9.4). 에이전트 0개.
    `bootstrap-master`는 아직 만들지 않는다(`30-onboarding-provisioning.md` §1.0 — 아래 참고).
 4. Neo4j Ready 대기 → 스키마 마이그레이션(제약·인덱스)
    → 카탈로그 시드(Capability taxonomy, Permission catalog, 기본 PermissionProfile 3종,
      AssignmentPolicy v1, OrgRevision=0)
 5. osiris-gw conf 렌더 + dev-net 가입 확인
 6. ▶ 한 번만 필요한 수동 단계 (OSiris 밖의 자산이므로 자동화하지 않는다)
    <upstream-proxy> 에 아래 conf 를 설치하고 reload 하세요:
      <§2.3의 osiris.example.com.conf 내용을 그대로 출력>
      $ docker exec <upstream-proxy> nginx -t && docker exec <upstream-proxy> nginx -s reload
 7. osiris-watchdog systemd 유닛 설치 (대역외 알림 자격증명 대화형 입력)
 8. 관리자 1회용 로그인 토큰 출력 → https://osiris.example.com
    (이 시점에 `bootstrap-master`는 **아직 없다** — 아래 참고. **genesis 토큰도 아직 발급되지
     않는다**[2026-07-27 3차 정정] — 발급 자체가 `bootstrap-master` 컨테이너 생성 시점으로
     옮겨졌다(§8.5). 이 실행이 끝난 시점에 호스트에는 genesis 관련 시크릿 파일이 전혀
     존재하지 않는다)

  목표 소요: 5분 이내(이미지 프리풀 포함)  [미검증 — 실측 필요]
```

부트스트랩 직후 조직은 **비어 있다**. 팀·직무·에이전트는 온보딩(`30-onboarding-provisioning.md`)이 **`osiris-core`의 graph-write API를 통해 `20-ontology-schema.md` §9(시드 순서·멱등 규칙·검증 불변식)대로 그래프에 쓰는 것**으로 생성되며, 커밋으로 `:OrgRevision`이 올라간 그 순간 orchestrator의 수렴 루프가 컨테이너를 만든다. 온보딩은 컨테이너/Pod를 직접 만들지 않는다(§13).

**[2026-07-27 갱신, `30-onboarding-provisioning.md` §1.0 반영 — 이전 판은 낡았다]** 이전 판은 이 구간에서 곧바로 `bootstrap-master`가 대화를 맡는다고 적었다. 그 뒤 `bootstrap-master`도 LLM이 필요하고 그 LLM 게이트웨이(`osiris-omniroute`)가 바로 온보딩이 구성해야 할 대상이라는 **순환 의존**이 드러났다(§9.6). 이 순환은 대화형 온보딩보다 **앞서는 온보딩 0단계**로 해소된다:

```
osiris-up 완료(위 0~8단계) → 조직 비어 있음, osiris-omniroute도 빈 DB로 대기
     ↓
온보딩 0단계 (LLM 전혀 없음, 결정적 코드/폼 — `30-onboarding-provisioning.md` §1.0)
  프로바이더 등록 → osiris/* 별칭 확정 → 게이트웨이 토큰 발급 → 게이트웨이 헬스 검증
     ↓ (전부 통과해야만 다음으로)
osiris-orchestrator가 (osiris-api의 API 요청으로 트리거되어) bootstrap-master 컨테이너 생성
     ↓
콘솔 Onboarding 화면 → bootstrap-master와 대화 시작 (이하 §1.1~§1.5, `30-onboarding-provisioning.md`)
```

**바뀐 것은 시점과 호출 주체뿐, 정체성은 무변경**: `bootstrap-master`는 여전히 그래프 노드가 아니고, 여전히 `osiris.dev/genesis=true` 라벨을 가진 임시 컨테이너이며, 여전히 §8.5의 고정 스코프로만 인가된다 — 다만 그것을 **생성하는 주체가 `osiris-up`(1회성 스크립트)에서 `osiris-orchestrator`로 바뀌고**(이유: `osiris-api`는 docker.sock을 갖지 않는다, D4/G8), **생성 시점이 "osiris-up 완료 직후"에서 "온보딩 0단계 통과 직후"로 늦춰진다.** 늦추는 이유: 0단계가 사람의 폼 입력을 거쳐야 하는 이상 완료 시점을 예측할 수 없고, 그 전에 LLM을 부를 수 없는 `bootstrap-master`를 미리 띄워 두는 것은 자원 낭비이자 §8.5의 genesis 창을 불필요하게 넓히는 일이기 때문이다(창이 열려 있는 시간이 길수록 침해 표면이 커진다는 §8.5 자신의 철학과 정합). **[2026-07-27 3차 정정] genesis 토큰의 발급 시점도 이제 이 정정과 함께 옮겨진다.** 직전 판(2차 정정)은 발급 시점(`osiris-up` 실행 중)을 그대로 두고 그 토큰을 쓰는 컨테이너의 생성 시점만 늦추는 TOFU(최초 사용 시 바인딩)를 도입했으나, 그 결과 발급~최초 바인딩 사이에 사람의 0단계 폼 입력이라는 길이를 예측할 수 없는 구간이 새로 끼어들고 그 구간에 토큰이 유출되면 유출자가 접속을 선점할 수 있다는 잔여 위험을 인정할 수밖에 없었다. 이번 정정(사용자 확정)은 잔여 위험 자체를 없애기 위해 **발급 시점을 `bootstrap-master` 컨테이너 생성 시점으로 옮긴다** — 상세 근거와 창이 실제로 0이 되는 순서는 아래 §8.5 참고. `bootstrap-master`의 정체성·권한 스코프·폐기 경로는 `30-onboarding-provisioning.md` §1.3/§1.5, 인가 모델은 §8.5에 있다.

`docker-compose.yml` 골격 (요지만):

```yaml
networks:
  dev-net:       { external: true }
  osiris-edge:   { internal: true }
  osiris-data:   { internal: true }
  osiris-agents: { internal: true, ipam: { config: [{ subnet: 172.31.240.0/24 }] } }

volumes: { neo4j-data: {}, nats-data: {} }

services:
  osiris-gw:
    image: nginx:1.27-alpine
    networks: [dev-net, osiris-edge]          # ★ 인바운드 전용 이중 홈
    volumes: [ ./gw/conf.d:/etc/nginx/conf.d:ro, ./console/dist:/usr/share/nginx/html:ro ]
    restart: unless-stopped

  osiris-api:
    image: ghcr.io/osiris/api:${TAG}
    networks: [osiris-edge, osiris-data]
    environment: [ TRUSTED_PROXY_CIDR=172.31.238.0/24, PUBLIC_ORIGIN=https://osiris.example.com ]
    restart: unless-stopped

  osiris-core:
    image: ghcr.io/osiris/core:${TAG}          # claude 바이너리 없음(D12)
    networks: [osiris-data, osiris-agents]
    restart: unless-stopped

  osiris-orchestrator:
    image: ghcr.io/osiris/orchestrator:${TAG}
    networks: [osiris-data]                    # ★ 에이전트 네트워크에 붙지 않음
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock       # ★ 여기에만
      - ./state:/state
    restart: unless-stopped

  neo4j:
    image: neo4j:5-community
    networks: [osiris-data]                    # ★ 에이전트는 도달 불가
    volumes: [ neo4j-data:/data ]
    restart: unless-stopped

  nats:
    image: nats:2-alpine
    command: ["-js", "-sd", "/data", "-c", "/etc/nats/nats.conf"]
    networks: [osiris-data, osiris-agents]
    volumes: [ nats-data:/data, ./nats:/etc/nats:ro ]
    restart: unless-stopped

  osiris-egress:
    image: ghcr.io/osiris/egress:${TAG}
    networks: [osiris-agents, dev-net]         # ★ 아웃바운드 전용 이중 홈
    environment: [ ALLOW_SRC_CIDR=172.31.240.0/24 ]   # dev-net 쪽 요청 전면 거부
    restart: unless-stopped
    # 어떤 포트도 publish 하지 않는다
```

### 4.2 [2단계] k3s 부트스트랩

```
$ osiris-migrate-to-k3s          # 1단계 데이터를 보존하며 전환
 1. 전 에이전트 순차 drain (진행 중 턴 완료 대기, 최대 660초/명)
 2. Neo4j dump, NATS 스트림 백업, 워크스페이스 볼륨 tar → ./state/backup
 3. k3s server 설치 (systemd)
    INSTALL_K3S_EXEC="server --cluster-init
       --disable=traefik,local-storage,servicelb
       --node-label osiris.dev/role=server"
    (traefik 비활성: 우리는 osiris-gw를 이미 갖고 있다. ingress 계층은 장애 도메인만 늘린다)
 4. /var/lib/rancher/k3s/server/manifests/ 자동배포
    01-longhorn.helmchart(replicas=min(3,노드수), defaultClass) / 02-nats / 03-neo4j
    04-osiris-crds / 05-operator / 06-core / 07-api(DaemonSet hostPort 8443) / 08-egress
 5. 백업 복원 (Neo4j dump → PVC, 볼륨 tar → PVC)
 6. osiris-gw conf 한 줄 변경 → nginx -s reload      ★ <upstream-proxy> 는 건드리지 않는다
 7. watchdog 체크 항목 갱신(docker → k3s)
```

노드 join(§2단계):

```
콘솔 [+ 노드 추가] → POST /cluster/nodes/join-token
  osiris-api가 단명(10분)·1회용 OSiris 토큰 발급 (Neo4j에 해시만 저장)
새 서버: osiris-join --token <ott> --server https://<server>:8443
  ① 토큰 검증·즉시 소각·감사 기록 → 실제 K3S_TOKEN을 1회 전달
     ※ 실제 k3s 클러스터 토큰은 이 경로 외로 서버 밖에 나가지 않는다
  ② k3s agent join, taint osiris.dev/unverified=true:NoSchedule 자동 부착
  ③ watchdog을 새 노드에도 설치, peer 목록 갱신
  ④ 콘솔에서 대표가 승인 → taint 제거 → 스케줄 가능
서버(컨트롤 플레인) 노드는 1대 또는 3대만 허용. 2대 구성은 API가 거부(etcd 정족수).
```

---

## 5. 에이전트 런타임

> 이 절은 **두 단계 공통**이다. supervisor는 자신이 Pod 안인지 Docker 컨테이너 안인지 알지 못하며, 알 필요도 없다. 차이는 표로만 표시한다.

### 5.1 컨테이너 구성

| | [1단계] Docker | [2단계] k8s |
|---|---|---|
| 생성 주체 | orchestrator의 `ContainerCreate` | operator가 `Agent` CR로부터 Pod 렌더 |
| PID 1 | `osiris-supervisor` | `osiris-supervisor` |
| 워크스페이스 | named volume → `/workspace` | PVC → `/workspace` |
| 자격증명 | 호스트 0700 디렉토리 ro-bind → `/var/run/osiris/creds` | Secret projected → 같은 경로 |
| 설정 | 위와 동일 → `/var/run/osiris/config` | Secret projected → 같은 경로 |
| 리소스 | `Memory` / `NanoCPUs` | `resources.requests/limits` |
| 종료 유예 | `ContainerStop(timeout=660)` | `terminationGracePeriodSeconds: 660` |
| liveness | Docker HEALTHCHECK + **orchestrator가 재시작 집행** | kubelet `livenessProbe` |
| readiness | orchestrator가 `/readyz` 폴링 | `readinessProbe` |
| 네트워크 | `osiris-agents` 하나 | 팀 Namespace + NetworkPolicy |
| 보안 | `cap_drop ALL`, `read_only`, `no-new-privileges`, non-root (orchestrator가 부여) | 동일 + PodSecurity admission이 강제 |

**단일 컨테이너 · supervisor가 PID 1 (D5)** 인 이유:
1. SIGTERM이 supervisor에 **먼저** 도달해야 한다. 사이드카면 런타임이 두 컨테이너에 동시에 SIGTERM을 보내고 `claude`가 체크포인트 로직과 무관하게 죽는다 — §5.5가 성립하지 않는다.
2. `claude`가 자식이므로 프로세스 트리 관리(좀비 수거, 프로세스 그룹 시그널)가 표준 POSIX로 끝난다.
3. 워크스페이스를 공유 볼륨·IPC로 나눌 필요가 없다.

**언어: Go, 단일 정적 바이너리.** (a) 인터프리터·의존성 없이 바이너리 하나, (b) NATS 구독·자식 wait·헬스서버·하트비트의 동시성이 goroutine + `select`로 표현되며 **그 `select` 루프가 곧 liveness 신호**가 된다(§5.4), (c) orchestrator/operator와 타입 공유. Python/Node는 이미지 비대화와 기동 지연으로 탈락.

### 5.2 메인 루프

```go
func main() {
    st    := loadState("/workspace/.osiris/state.json")   // sessionId, checkpoint
    cfg   := renderConfig("/var/run/osiris/config")
    creds := watchCreds("/var/run/osiris/creds")          // mtime 감시 → 재시작 없이 회전 반영

    go healthServer(":8081")     // /livez /readyz — atomic 만 읽는다
    go heartbeatLoop(10 * time.Second)
    go outboxFlusher()           // NATS 두절 시 스풀 재전송

    if st.Checkpoint.Status == "interrupted" { resumeInterrupted(st) }   // §5.5

    sub  := js.PullSubscribe(inboxSubject(), durable(employeeID))
    tick := time.NewTicker(5 * time.Second)

    for {
        select {
        case <-tick.C:
            atomic.StoreInt64(&lastLoopTick, nowMs())   // ★ liveness의 유일한 근거
            reconcileGateway()                          // 60초 캐시 갱신
        case <-drainSignal:
            handleDrain(); return
        default:
        }
        if draining.Load() { continue }

        // ★ 폴링이 아니라 long-poll. 대기 중 CPU·토큰 소모 0.
        msgs, err := sub.Fetch(1, nats.MaxWait(30*time.Second))
        atomic.StoreInt64(&lastLoopTick, nowMs())
        if err == nats.ErrTimeout { continue }            // 정상 — 메시지가 없었을 뿐
        if err != nil { markNATSDegraded(err); backoff(); continue }

        m := msgs[0]
        go m.InProgress(every(25 * time.Second))          // ack_wait(30m) 연장
        runTask(m, st, cfg, creds)                        // 여기서 처음 claude 프로세스가 생긴다
        m.Ack()
    }
}
```

`runTask`가 조립하는 커맨드:

```
claude -p --bare \
  --resume <sessionId>                                  # 최초 실행이면 생략
  --settings   /var/run/osiris/config/settings.json     # --bare는 자동탐색 안 함 → 항상 명시
  --mcp-config /var/run/osiris/config/mcp.json          # --resume이 복원 안 함 → 항상 재주입
  --allowedTools "<PermissionProfile 파생 목록>"
  --permission-mode dontAsk
  --add-dir /workspace/repo
  --output-format stream-json
  <<< "<인박스 메시지에서 조립한 태스크 프롬프트>"
```

`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, `GIT_ASKPASS`는 **자식 프로세스 env에만** 주입한다. supervisor 자신의 환경에는 두지 않는다(장수 프로세스의 env는 `/proc/<pid>/environ`으로 오래 노출된다).

`stream-json` 출력의 처리:
- 도구 호출/결과 → `ops.log.<id>` (Core NATS, 비영속)
- 최종 result 라인의 `total_cost_usd`·토큰 수 → `ops.cost.<id>` (JetStream COST)
- 전체 라인 → `TRANSCRIPT` 스트림(30일) + `/workspace/.osiris/transcripts/<taskId>.jsonl`
- 완료/실패 판정 → `task.<taskId>.event`

**"결과를 요약해서 보고해줘"라는 추가 LLM 턴은 존재하지 않는다.**

### 5.3 생명주기 상태기계

```
   Pending ──▶ Provisioning ──▶ Booting ──▶ Ready ⇄ Working
     │        (볼륨/자격증명/     (컨테이너   (readyz    (claude 자식 실행 중)
     │         설정/NATS사용자     기동, 설정   =200)        │
     │         생성)              로드,           ▲          │ SIGTERM
     │                            게이트웨이       └──────────┤
     │                            probe)                     ▼
     │                                                   Draining
     │                                          (체크포인트 기록 → waitSeconds
     │                                           만료 시 자식에 SIGTERM)
     │                                                       ▼
     │                                                    Stopped
     ├──▶ Suspended   조직 변경으로 비활성. 컨테이너 삭제·볼륨 보존.
     │                 resume 시 Provisioning 복귀 (sessionId 보존 → --resume 가능)
     └──▶ Failed      연속 3회 크래시 또는 게이트웨이 probe 10분 연속 실패
                       지수 백오프(1m→2m→4m→…→30m 상한) 후 재시도
                       + org.node.status_changed(오류) + notification.created
```

**상태의 진실 위치**: `phase`는 **워크로드 드라이버의 관측값이 권위**다. [1단계] orchestrator의 런타임 캐시, [2단계] `Agent` CR `status.phase`. Neo4j `:AgentRuntime.phase`는 콘솔 조회 편의를 위한 **캐시**이며 `observedAt` 없이 존재해서는 안 된다(§6.1). `Working` 여부는 supervisor 하트비트가 권위다.

### 5.4 헬스체크 판정 기준 (핵심)

#### `/livez` — "supervisor가 wedge되지 않았는가"만 본다

```
200 iff  (now - lastLoopTick) < 30s
     AND natsConnState != CLOSED          (RECONNECTING은 살아있는 것으로 본다)
     AND childReaper goroutine 생존        (자체 tick으로 확인)
     AND 헬스서버가 3초 내 응답             (probe timeout이 곧 이 검사)
```

**`claude` 프로세스의 존재 여부, 실행 시간, 응답 여부는 livez의 입력이 아니다.** 40분짜리 턴이 도는 동안에도 supervisor의 `select` 루프는 5초 티커로 계속 돌기 때문에 `lastLoopTick`이 갱신된다. 즉 **"작업 중이라서 probe가 실패한다"는 상황이 구조적으로 발생할 수 없다.**

구현 시 반드시 지킬 규칙 (어기면 위 설계가 무너진다):
- **헬스 핸들러는 atomic 변수만 읽는다.** 자식 프로세스 실행 경로가 잡는 어떤 mutex도 잡지 않는다. (흔한 실패: mutex로 보호된 상태 구조체를 헬스가 읽다가, 긴 exec 경로가 락을 쥐고 있어 헬스 타임아웃 → 재시작. **금지한다.**)
- CPU limit은 `>= 1000m`(1 vCPU)을 강제한다(orchestrator가 검증). 헬스 goroutine이 굶지 않도록.
- 헬스서버는 자식 stdout 파이프에 절대 블록되지 않는다: 별도 goroutine + 버퍼드 채널, **채널 만재 시 로그 라인을 버린다**(로그 유실 < 재시작).

집행: `failureThreshold 3 × period 10s` = **30초 연속 wedge**여야 재시작. [1단계]에서는 orchestrator가 Docker 헬스 상태 3연속 unhealthy를 같은 기준으로 집행한다. 재시작 시 진행 중이던 턴은 체크포인트에서 재개된다.

#### `/readyz` — "새 작업을 안전하게 맡길 수 있는가"

```
200 iff  자격증명 파일 로드 성공 (필수 키 존재, 만료 전)
     AND 렌더된 설정 유효 (파싱 성공, configHash 계산됨)
     AND 부팅 시 `claude --version` 1회 성공
     AND JetStream durable consumer 바인드 성공
     AND /workspace 쓰기 가능 (60초 주기 touch)
     AND gatewayHealthy (omniroute /healthz 60초 주기, 연속 2회 실패까지 관용)
```

**`readyz`는 유휴/작업중을 구분하지 않는다.** 작업 중인 에이전트도 `ready=true`다. 이유: readiness=false는 [2단계]에서 "Service 엔드포인트 제외"를 의미하는데 에이전트는 Service 뒤에 있지 않다(큐에서 당겨오는 워커). 여기에 "바쁨"을 실으면 런타임 의미론이 왜곡되고 PDB·롤링 판정이 깨진다. 바쁨/한가함은 하트비트의 `state`와 `activeTaskPoints`로 표현하고 배정 엔진이 그것을 본다.

`readyz=false`의 실제 효과:
1. 런타임 상태에 `Ready=False` + reason 기록
2. **assignment-engine의 하드 필터 `UNAVAILABLE`에 걸려 후보에서 자동 제외** — 게이트웨이가 죽으면 그 순간부터 새 배정이 그 에이전트에게 가지 않는다. LLM 판단 없이 결정적으로.
3. 콘솔 Org 노드가 "오류" 링으로 전환(`org.node.status_changed`)

**어떤 probe도 LLM을 호출하지 않는다.** 불변식으로 못 박는다: `/livez`·`/readyz` 처리 경로에서 `claude` 프로세스를 생성하는 코드는 존재하지 않는다. 게이트웨이 확인도 `/healthz`(무인증, 모델 호출 없음)만 친다.

### 5.5 SIGTERM · graceful shutdown · 재개

```
[t=0]  런타임 → SIGTERM → PID 1 (supervisor)
       draining = true                     (인박스 Fetch 즉시 중단)
       publish agent.state = draining      (콘솔 즉시 인지)
       자식 없음 → outbox 플러시 → consumer unbind → exit 0   (~2초)

[t=0]  자식(claude) 실행 중:
       1) 체크포인트 fsync → /workspace/.osiris/state.json
          { taskId, sessionId, turnStartedAt, replySubject, natsMsgSeq,
            workspaceGitSha, status: "interrupted", attempt: n }
       2) publish task.<id>.event = interrupted   (core가 태스크를 in-flight로 유지)
       3) 시그널을 보내지 않고 drain.waitSeconds(기본 600s) 동안 자연 완료 대기
          → 안에 끝나면 정상 완료 경로 → 체크포인트 삭제 → exit 0

[t=600] 만료:
       4) 자식에 SIGTERM
          (Claude Code 공식 동작: 진행 중 턴 중단 → Bash 프로세스 트리 종료
           → SessionEnd 훅 → exit 143)
       5) 최대 20초 대기 → 미종료 시 프로세스 그룹에 SIGKILL
          [미검증] SIGTERM→exit 143 소요 시간 상한이 공식 문서에 없음. 20초는 가정.
       6) checkpoint.status = "interrupted" 확정 → outbox 플러시 → exit 0

[t=660] 종료 유예 만료 → 런타임이 SIGKILL
        설계상 여기에 도달하지 않는다. 도달하면 버그로 간주하고 알림 발행.
```

**preStop 훅(2단계)을 두지 않는다.** preStop의 통상 용도는 엔드포인트 디레지스트레이션인데 에이전트는 Service 뒤에 없다. PID 1의 SIGTERM 처리로 충분하며, 훅을 하나 더 두면 실패 모드만 늘어난다.

**재개**

```
supervisor 부팅 → checkpoint.status == "interrupted" 발견
 1. 워크스페이스 무결성 확인 (git status / dirty 파일 목록)
 2. claude -p --resume <sessionId> 로 재개, 조립 프롬프트:
      "이전 턴이 인프라 사유(드레인/재배포)로 중단되었습니다. 중단 시각 <t>.
       워크스페이스의 현재 상태를 먼저 확인하고, 이미 완료된 부분을 반복하지 말고
       이어서 진행하세요. 미커밋 변경: <파일 목록>"
 3. attempt += 1.  attempt > 3 이면 재개하지 않고 태스크 blocked + 팀장 에스컬레이션
```

**at-least-once의 안전성 논거**: 재개는 중복 실행 가능성을 내포한다. 이것이 안전한 이유는 **외부 영향 액션(커밋·발송·지출)이 전부 승인 게이트 뒤에 있기 때문**이다(§8.2). 게이트는 `approvalId`로 멱등하며 이미 승인·실행된 액션은 재실행되지 않는다. 중복 실행의 최악 결과는 "토큰을 조금 더 쓰고 같은 결론"이지 "두 번 커밋 / 두 번 송금"이 아니다. **이 성질이 Temporal을 지금 도입하지 않아도 되는 근거의 절반**이다(나머지 절반은 JetStream WorkQueue의 재전달).

**NATS 두절 시**: 결과 발행 실패는 `/workspace/.osiris/outbox/<seq>.json`에 스풀하고 재연결 시 순서대로 재전송. 각 메시지는 `Nats-Msg-Id`(= `taskId:attempt:eventType`)를 가지므로 JetStream 중복 제거 창(2분)과 core의 멱등 처리로 흡수된다.

### 5.6 세션·설정 상태의 보관 위치

| 상태 | 권위 위치 | 사본 | 이유 |
|---|---|---|---|
| `sessionId` | **워크스페이스 볼륨** `/workspace/.osiris/state.json` | 런타임 status, Neo4j `:AgentRuntime` | 세션 JSONL 실체가 같은 볼륨(`CLAUDE_CONFIG_DIR=/workspace/.claude`)에 있어 ID와 데이터가 분리 불가능하게 함께 산다. 볼륨을 잃으면 ID도 무의미하므로 다른 곳에 두면 좀비 ID만 남는다 |
| 진행 중 체크포인트 | **워크스페이스 볼륨** (fsync) | 없음 | 재개 주체가 그 볼륨을 마운트한 supervisor 자신 |
| `settings.json`/`mcp.json`/`allowedTools` | **파생 원본은 Neo4j** → 드라이버가 파일로 렌더 | `configHash`를 하트비트에 포함 | `--bare`가 자동탐색을 안 하고 `--resume`이 복원하지 않으므로 매 호출 명시 주입 |
| 자격증명 | 호스트 0700 디렉토리 [1단계] / k8s Secret [2단계] | 없음 | 회전 주체가 orchestrator/operator |
| 트랜스크립트 | 볼륨(전체) + JetStream `TRANSCRIPT`(30일) | — | 콘솔이 컨테이너에 직접 붙지 않아도 과거 조회 가능해야 함 |
| 비용 | JetStream `COST`(400일) | Neo4j에 넣지 않음 | 고빈도 시계열은 그래프에 넣지 않는다(§6.3) |

설정 갱신 경로: 조직 변경 → Neo4j → OrgRevision bump → 드라이버가 spec 재계산 → `specHash` 변화 → 설정 파일 재렌더 → `agent.<id>.config.reload` 발행 → supervisor가 재렌더 후 **다음 태스크부터** 적용(진행 중 턴은 건드리지 않는다). 이미지·리소스 변경만 컨테이너 교체를 유발하며, 이때도 §5.5의 drain 경로를 탄다.

### 5.7 무거운 one-shot 작업

기준: **예상 실행시간 > 20분** 또는 **격리된 큰 리소스 필요**(빌드, 대용량 처리) → 상주 컨테이너에서 돌리지 않고 분리한다.

- [1단계] orchestrator가 `osiris-job-<taskId>` 컨테이너를 생성(동일 이미지·동일 자격증명·동일 네트워크, 권한 확대 없음), 종료 시 회수. [2단계] `Job`(Agent CR 소유).
- supervisor는 `--one-shot` 모드로 뜬다: 인박스를 바인드하지 않고 인자로 받은 태스크만 수행 후 종료.
- 워크스페이스는 에이전트 볼륨을 동시 마운트하지 않고 **전용 임시 볼륨**을 받으며, 결과 아티팩트만 NATS/저장소로 넘긴다(동시 마운트 문제 회피의 최단 경로).
- 재시도는 2회 + supervisor 체크포인트. **Temporal은 도입하지 않는다**(§11 쟁점 5).

---

## 6. 상태 수렴과 진실 소유권

### 6.1 진실 소유권 — 가장 중요한 경계 (D1)

```
 ┌──────────────────────────────────────────────────────────────────────────┐
 │ 의도 (INTENT) — "조직은 이래야 한다"                                       │
 │ 진실: Neo4j                                                              │
 │ 내용: 사람·에이전트·팀·직무·역량·권한선언·WBS·배정·배정근거·정책            │
 │ 쓰기: osiris-core의 graph-write API 만 (콘솔·Master·온보딩 전부 경유) D17  │
 └────────────────────────────┬─────────────────────────────────────────────┘
                              │ 단방향 파생. OrgRevision N → AgentWorkloadSpec
                              ▼
 ┌──────────────────────────────────────────────────────────────────────────┐
 │ 물성 (MATERIALIZATION) — "그러려면 무엇이 존재해야 하는가"                  │
 │ [1단계] 저장소 없음. orchestrator가 매 루프마다 그래프에서 재계산(메모리)   │
 │ [2단계] CRD의 spec 에 영속화. 단 spec은 파생물이므로 사람이 편집하면 되돌려짐│
 │ 쓰기: graph-projector 만                                                  │
 └────────────────────────────┬─────────────────────────────────────────────┘
                              │ reconcile (WorkloadDriver)
                              ▼
 ┌──────────────────────────────────────────────────────────────────────────┐
 │ 관측 (OBSERVATION) — "실제로 지금 어떤가"                                  │
 │ [1단계] Docker 컨테이너 상태 + orchestrator 런타임 캐시 + NATS 하트비트     │
 │ [2단계] CRD의 status + NATS                                               │
 │ 쓰기: 드라이버(상태) / supervisor(하트비트) 만                              │
 └────────────────────────────┬─────────────────────────────────────────────┘
                              │ 비권위 미러링 (10초 배치)
                              ▼
        Neo4j `:AgentRuntime`, `:ClusterNode`  ← 캐시. 절대 진실이 아님.
        모든 속성에 `observedAt` 필수. stale(>60s)이면 콘솔이 회색 처리.
```

**세 줄 규칙 (구현자가 외워야 할 것)**
1. *"이 사실이 사람의 결정으로 바뀌는가?"* → **Neo4j** (누가 어느 팀인가, 무슨 역량인가, 무엇을 승인해야 하는가)
2. *"이 사실이 런타임이 만들어야 할 물건인가?"* → **파생된 spec** (그리고 그것은 1번에서 계산된다)
3. *"이 사실이 세계를 관측해서 얻어지는가?"* → **드라이버의 관측값**. Neo4j에 있다면 그건 캐시이고 `observedAt` 없이 존재해서는 안 된다.

같은 사실이 두 곳에 있는 **유일한 허용 케이스는 관측값의 캐시**이며, 이때 Neo4j 쪽은 **읽기 전용·유효기간 있음**이다. 예: 콘솔 조직도는 Neo4j 한 번의 쿼리로 이름·직무·상태를 함께 얻는다(캐시 덕분). 그러나 "이 에이전트를 재시작할까?"는 드라이버의 관측값을 다시 읽고 판단한다.

**[1단계]에 물성 저장소가 없다는 사실의 함의** — 정직하게: desired state가 orchestrator 메모리에만 존재한다. 그래서 orchestrator가 죽으면 **수렴이 멈춘다**(desired가 유실되는 것이 아니라 계산이 멈추는 것). 기동 시 그래프에서 전량 재계산하므로 재기동만으로 회복한다. [2단계]에서 CRD spec이 이 파생 결과를 etcd에 캐시함으로써 컨트롤러 재기동과 무관하게 desired가 보존된다. **이것이 CRD가 실제로 벌어주는 값이며, 그 외의 이유로 CRD를 도입하는 것이 아니다.**

**드리프트 교정** [2단계]: projector는 60초마다(그리고 `org.changed` 수신 시 즉시) 그래프에서 목표 spec을 재계산해 CR과 비교하고 다르면 덮어쓴다. `kubectl edit`으로 손댄 spec은 최대 60초 후 사라지며, operator가 `OverwrittenManualEdit` 이벤트를 남긴다.

### 6.2 `WorkloadDriver` — 단계를 가로지르는 이음새 (D3)

```go
type AgentWorkloadSpec struct {
    EmployeeID   string          // = Neo4j :AIAgent.id
    TeamID       string
    RoleID       string
    Image        string
    ModelAlias   string          // "osiris/worker" — 프로바이더 모델 ID 아님
    Resources    ResourceProfile // RoleResourceProfile 에서 파생
    Permissions  ProfileRef      // PermissionProfile 이름 + version
    Concurrency  int             // maxTaskPoints
    DrainSeconds int             // 기본 600
    Desired      DesiredState    // Running | Suspended

    // 렌더된 설정 — 컨테이너를 교체하지 않고 파일만 다시 쓰면 되는 것들
    RenderedPrompt   string      // :Role.promptTemplateMd 를 이 개인에 맞춰 치환한 결과 (CLAUDE.md)
    RenderedSettings string      // settings.json (permissions.allow/deny, hooks)
    RenderedMCP      string      // mcp.json

    // ★ 두 해시는 의도적으로 분리되어 있다. 합치면 프롬프트 한 줄 수정이 전 에이전트 재생성이 된다.
    SpecHash   string  // Image, Resources, 네트워크, DrainSeconds 의 해시  → 변하면 Recreate
    ConfigHash string  // Rendered* 3종 + ModelAlias 의 해시              → 변하면 ReloadConfig
}

type WorkloadDriver interface {
    Apply(ctx, AgentWorkloadSpec) (WorkloadRef, error)   // 멱등. 볼륨/자격증명/설정/네트워크 포함
    Recreate(ctx, AgentWorkloadSpec) error               // drain → delete → apply
    ReloadConfig(ctx, AgentWorkloadSpec) error           // 설정 파일만 재기록 + config.reload 발행. 무중단
    Drain(ctx, employeeID string, waitSeconds int) error
    Delete(ctx, employeeID string) error
    Observe(ctx) (map[string]WorkloadStatus, error)      // employeeID → {phase, health, restarts, specHash, configHash}
}
```

**두 해시의 경계 (§5.6의 "설정 변경은 다음 태스크부터, 이미지·리소스만 교체" 규칙의 기계적 표현)**

| 무엇이 바뀌면 | 해시 | 동작 | 진행 중 턴 |
|---|---|---|---|
| 이미지, CPU/메모리, 드레인 시간, 네트워크 | `SpecHash` | `Recreate` (drain 660초 경유) | 완료 대기 후 교체 |
| **`:Role.promptTemplateMd`**, 개인 역량 레벨, `PermissionProfile`, MCP 구성, 모델 별칭 | `ConfigHash` | `ReloadConfig` | **건드리지 않는다.** 다음 태스크부터 적용 |

`[2단계]`에서는 `Agent` CR의 `spec.specHash` / `spec.configHash`와 `status.observedSpecHash` / `status.observedConfigHash`가 같은 역할을 한다.

- [1단계] `driver/docker` — Docker Engine API. **`github.com/docker/docker` import는 이 패키지에서만 허용**(CI 린트로 강제, G3).
- [2단계] `driver/kube` — controller-runtime. `AgentWorkloadSpec`을 `Agent` CR spec으로 직렬화.
- 수렴 루프(§1.4)는 두 단계에서 **같은 코드**다.

> **`[2026-07-28 K2 전환]`** 위 두 줄 중 `driver/docker`(1단계)는 만들어지지 않는다. K2 전면 재작성으로 남는 드라이버는 `driver/kube` 하나뿐이다. "수렴 루프는 두 단계에서 같은 코드"라는 마지막 문장은 형태만 바뀌어 유효하다 — 이제 "같은 코드가 유일한 드라이버 위에서 돈다"로 읽는다. G3의 CI 린트(`github.com/docker/docker` import 제한)는 그 대상 패키지가 없어지므로 폐기한다.

**[2026-07-28 K2 전환]** §6.3의 CRD 설계(`Team`/`Agent`/`PermissionProfile`) 자체는 K2 이후에도 유효하다 — 애초에 이 설계는 처음부터 유일 배포 대상이 되는 쪽에 속해 있었다. 아래 "**[1단계]에서의 대응물**"(도커 네트워크 배치·컨테이너 라벨·렌더된 settings.json)만 만들어지지 않는 과거 경로로 읽는다.

### 6.3 [2단계] CRD 설계

`apiVersion: osiris.dev/v1alpha1`, **3종만**.

**`Team`** (클러스터 스코프) → Namespace, 팀 기본 NetworkPolicy, NATS account, 팀 채널 권한.
```yaml
spec: { graphId, displayName, parentTeam, leadEmployeeId, namespace,
        defaultPermissionProfile, channelSubject, budget: {monthlyUsd} }
status: { namespaceReady, natsAccount, agentCount, conditions[] }
```

**`Agent`** (팀 Namespace) → Pod, PVC, SA, Role/RoleBinding, NetworkPolicy, Secret 2종. spec은 `AgentWorkloadSpec`과 1:1.
```yaml
spec: { graphId, displayName, roleId, teamRef, runtimeClass, image,
        permissionProfileRef, capabilities[], modelAlias,
        concurrency: {maxTaskPoints}, drain: {waitSeconds},
        desiredState: Running|Suspended, specHash, configHash }
status: { phase, podName, nodeName, sessionId, currentTaskId, lastHeartbeatAt,
          restartCount24h, observedSpecHash, observedConfigHash,
          conditions: [Ready, GatewayReachable, CredentialsValid] }
```

**`PermissionProfile`** (클러스터 스코프) → RBAC + NetworkPolicy + settings.json + NATS ACL의 단일 원천(§8.1).
```yaml
spec:
  graphId: pp-builder
  version: 7
  grants:
    - { id: fs.workspace.write }
    - { id: net.egress.pkg }
    - { id: net.egress.github }
    - { id: git.commit, gate: approval, approver: admin }
    - { id: msg.team.publish }
  denies: []
status: { renderedFor: 5, catalogVersion: "2026.07" }
```

**만들지 않는 CRD와 이유**

| 후보 | 기각 이유 |
|---|---|
| `Assignment` / `Task` | 하루 수백 건 회전. etcd는 태스크 큐가 아니며 watch 폭풍과 리소스버전 압박을 만든다. 배정은 Neo4j(진실) + NATS(전달)로 충분하고 k8s가 reconcile할 물건이 없다 |
| `Employee`(사람 포함) | **사람에게는 reconcile할 런타임 리소스가 없다.** 사람의 "런타임"은 콘솔 계정과 알림 어댑터이며 둘 다 공유 컴포넌트다. 사람을 1급 조직 노드로 다루는 것은 **온톨로지 계층에서 완전히 보장**되며(`:Party`), 런타임 계층에 사람 오브젝트를 만드는 것은 은유의 과잉이다 |
| `Approval` | 상태 전이가 빠르고 payload가 크다(diff). Neo4j + JetStream이 적합 |
| `WorkItem` | 위와 동일 |

**[1단계]에서의 대응물**: CRD 없음. `Team` → 도커 네트워크 배치 + NATS account, `Agent` → 컨테이너 라벨(`osiris.dev/employee-id`, `spec-hash`), `PermissionProfile` → 렌더된 settings.json + NATS ACL + egress 허용목록. **어느 것도 조직 정보를 저장하지 않는다** — 전부 그래프에서 파생된 라벨/파일일 뿐이다(G4).

### 6.4 Neo4j에 두지 않는 것

- 고빈도 시계열: 하트비트, 로그 라인, 비용 레코드 → NATS/JetStream
- 승인 payload 본문(diff, 첨부) → JetStream + 볼륨. 그래프에는 `payloadRef`만
- 런타임 오브젝트의 사본 → 드라이버 관측값(캐시만 허용, `observedAt` 필수)

---

## 7. 메시지 버스

### 7.1 서브젝트 트리 (단계 무관 — 불변 자산)

```
org.event.<type>                   # member_added, role_changed, revision_bumped …
org.revision                       # OrgRevision bump (수렴 루프 트리거)

team.<team>.channel                # 팀 브로드캐스트 (전제 10의 "팀 채널")
team.<team>.inbox.<employeeId>     # 개인 인박스 (사람·AI 공통). WorkQueue
master.inbox                       # 크로스팀은 반드시 여기로 (전제 10)
master.reply.<correlationId>

task.<taskId>.event                # created/assigned/started/progress/interrupted/done/failed
approval.request
approval.decision.<approvalId>
human.notify.<employeeId>          # core의 notify-adapter(결정 로직)가 소비 → NotifiableEvent로 변환해
                                    # 어댑터 대면 :8091에 얹음. 실제 외부 채널 팬아웃은 격리된
                                    # osiris-adapter-telegram 등이 수행([2026-07-27 정정], §3.2/§8.3)

heartbeat.agent.<employeeId>       # 10초. Core NATS(비영속)
heartbeat.cluster                  # 10초. osiris-api 발행. Core NATS
ops.log.<employeeId>               # 로그 라인. Core NATS(비영속, 고volume)
ops.cost.<employeeId>              # 턴별 비용. JetStream
ops.transcript.<employeeId>        # stream-json 라인. JetStream 30일
ops.egress                         # egress 프록시 감사(허용/거부). JetStream 30일

cluster.node.<node>.event          # [2단계] join/ready/pressure/offline/drain
agent.<employeeId>.config.reload   # 설정 갱신 통지
console.event                      # osiris-api가 콘솔용으로 정규화해 재발행
```

### 7.2 스트림

| 스트림 | 서브젝트 | 보존 | 정책 | 비고 |
|---|---|---|---|---|
| `INBOX` | `team.*.inbox.*`, `master.inbox` | WorkQueue | `ack_wait=30m`, `max_deliver=3`, DLQ `dlq.inbox` | 에이전트별 durable pull consumer |
| `TASKS` | `task.*.event`, `approval.*`, `org.event.*`, `cluster.*` | Limits 90일 | `Nats-Msg-Id` 중복제거 2분 | **콘솔 SSE 재개(`Last-Event-ID`)의 원천** |
| `TRANSCRIPT` | `ops.transcript.*` | Limits 30일, file | — | 콘솔 과거 조회 |
| `COST` | `ops.cost.*` | Limits 400일 | — | 월별 정산·예산 |
| `EGRESS` | `ops.egress` | Limits 30일 | — | 외부통신 감사 |
| (스트림 없음) | `heartbeat.*`, `ops.log.*` | — | Core NATS | 10초 하트비트를 영구 저장할 이유가 없다 |

### 7.3 크로스팀 라우팅 강제 (D14) `[조사 뒤집음]`

조사(쟁점 8)는 "NATS 레벨 강제 vs 애플리케이션 규약"을 열어뒀다. **NATS 레벨 강제를 택한다.** 에이전트는 LLM으로 구동되며 프롬프트 인젝션·오작동으로 규약을 어길 수 있다. OSiris의 가치 제안이 "감사 가능한 AI 조직"인 이상, 어길 수 있는 규약은 거버넌스가 아니다. 강제 비용은 낮다 — 드라이버가 이미 팀 계정·에이전트 사용자를 만들고 있으므로 권한 목록 몇 줄이 추가될 뿐이다.

에이전트 `dev-a`(alpha팀)의 NATS 사용자 권한:
```
publish:   team.alpha.channel
           team.alpha.inbox.*                  # 같은 팀 동료에게 직접
           master.inbox                        # 크로스팀은 여기로만
           master.reply.>
           heartbeat.agent.dev-a
           ops.log.dev-a, ops.cost.dev-a, ops.transcript.dev-a
           task.*.event
           approval.request
subscribe: team.alpha.inbox.dev-a
           team.alpha.channel
           approval.decision.>
           agent.dev-a.config.reload
deny 그 외 전부.  team.marketing.* 로의 publish는 NATS 서버가 거부한다.
```
Master만 모든 `team.*.inbox.*`에 publish 권한을 갖는다. 이로써 "크로스팀 = Master 경유"가 **기술적 사실**이 된다. [1단계]에서 L3 격리가 약한 것(§1.5)을 실질적으로 메우는 것이 바로 이 층이다.

### 7.4 콘솔 SSE 재개 (D19)

콘솔의 `Last-Event-ID`는 **JetStream `TASKS` 스트림의 시퀀스 번호**를 그대로 쓴다. `osiris-api`는 재연결 요청을 받으면 그 seq 이후부터 ephemeral consumer로 다시 읽어 흘려보낸다. 게이트웨이 인메모리 버퍼를 쓰지 않는 이유: [2단계]에서 콘솔이 다른 노드의 `osiris-api`로 페일오버해도 이벤트가 이어져야 하기 때문이며, [1단계]에서도 `osiris-api` 재시작 후 유실이 없다.

### 7.5 승인·알림 전달 보장 (쟁점 7)

JetStream at-least-once + 명시 ack + `Nats-Msg-Id` 멱등. **사람 대상은 재전달이 아니라 에스컬레이션 사다리**로 푼다(사람의 오프라인은 정상 상태이므로):

```
즉시        콘솔 알림센터 + My Tasks 뱃지
+15분 무응답 외부 채널(이메일/메신저) 1회
+4시간 무응답 팀장 에이전트 + 대표에게 알림
+24시간     태스크 status = blocked, 재배정 후보 재계산(assignment-engine 재실행),
            org.node.status_changed(사람 노드: awaiting-human → 오류 등급)
```
`awaiting-human`은 **1급 상태**이며 에이전트 오류와 동일 우선순위 경로를 탄다(콘솔 요구).

---

## 8. 권한 · 자격증명 · 격리

### 8.1 하나의 선언 → 네 층위 (D13)

브리핑은 세 층위(온톨로지 capability / RBAC·NetworkPolicy / Claude Code `--allowedTools`)를 요구했다. 여기에 **NATS ACL**을 넷째로 추가한다(§7.3의 이유).

```
Neo4j:  (:Role)-[:HAS_PROFILE]->(:PermissionProfile)-[:GRANTS]->(:Permission {code})
             │  graph-projector
             ▼
spec :  grants = [ {id, gate, approver, limit} ]        ([2단계] PermissionProfile CR)
             │  grant-catalog (버전 고정 Go 맵, 사용자 편집 불가)
             ▼
 ┌────────────┬────────────────────┬──────────────────────┬─────────────┐
 │ RBAC       │ 네트워크            │ Claude Code           │ NATS ACL    │
 │ [1단계]    │ [1단계] egress      │ settings.json         │ user perms  │
 │  no-op     │  허용목록           │ (permissions.allow/   │             │
 │ [2단계]    │ [2단계] +NetworkPol │  deny, hooks)         │             │
 │  Role/Bind │                     │ + --allowedTools 인자 │             │
 └────────────┴────────────────────┴──────────────────────┴─────────────┘
```

grant-catalog 발췌 (operator/orchestrator 공용, 코드로 고정):

| grant id | RBAC | 네트워크 | Claude Code | 승인 게이트 |
|---|---|---|---|---|
| `fs.workspace.write` | — | — | `Read,Write,Edit,Glob,Grep` | 없음 |
| `net.egress.pkg` | — | 허용목록 `registry.npmjs.org`, `pypi.org`, `proxy.golang.org` | `Bash(npm install:*)`, `Bash(pip install:*)` | 없음 |
| `net.egress.github` | — | 허용목록 `github.com`, `api.github.com` | `Bash(git fetch:*)`, `Bash(git clone:*)` | 없음 |
| `llm.gateway` | — | 허용목록 `omniroute:20128` | — | 없음 (모든 에이전트 기본) |
| `git.commit` | — | — | `Bash(git add:*)`, `Bash(git commit:*)` + 게이트 | `commit` |
| `git.push` | — | (github 허용 전제) | `Bash(git push:*)` + 게이트 | `commit` |
| `msg.external.send` | — | 허용목록 SMTP 릴레이 | MCP `mailer.send` + 게이트 | `message` |
| `spend.request` | — | — | MCP `finance.request_spend` + 게이트 | `spend` (+`limit`) |
| `k8s.read.ownteam` | [2단계] get/list pods,events in own ns / [1단계] **no-op** | — | — | 없음 (팀장만) |
| `exec.physical` | — | — | — | — (사람 전용 **역량**. 실행 권한이 아니라 배정 필터용) |
| `org.admin` | — | — | — | — (사람 전용 **콘솔 권한**. 위임의 최상위) |
| `org.manage.people` | — | — | — | — (사람 전용 콘솔 권한) |
| `org.manage.roles` | — | — | — | — (사람 전용 콘솔 권한) |
| `org.read.ops` | — | — | — | — (사람·기계 주체 공용 읽기) |
| `org.read.onboarding` | — | — | — | — (사람·기계 주체 공용 읽기) |

**[2026-07-27 신설] 조직·콘솔 권한 코드 5개 — 네 층위 투영이 전부 no-op인 이유** (F9 해소, `60-identity-authn.md` §20.4). 위 다섯 행은 `osiris-api`가 **콘솔 요청을 인가할 때만** 쓰는 코드다. 에이전트 런타임(RBAC/네트워크/Claude Code/NATS ACL)에는 대응물이 없으므로 네 열이 전부 `—`이며, 이는 누락이 아니라 `exec.physical`과 같은 **의도된 형태**다 — 그 행이 이미 "실행 권한이 아니라 배정 필터용"이라는 성격으로 같은 자리에 있다. 반대로 이 코드들을 네 층위 중 어디엔가 투영하려는 시도가 나오면 그것이 설계 오류다: 사람의 콘솔 권한이 에이전트 컨테이너의 능력으로 새는 경로가 되기 때문이다.

**I9 준수 요구**: 이 5개를 `:Permission` 카탈로그에 추가할 때 `:Permission.catalogVersion`과 **이 grant-catalog(코드로 고정된 Go 맵)를 같은 버전으로 함께 올려야 한다.** I9가 두 곳의 버전 일치를 불변조건으로 요구하므로, 한쪽만 올리면 부팅 시 불일치로 걸린다. 시드 절차는 `20-ontology-schema.md` §9.2 단계 3.

### 8.2 강제(enforcement) vs 협조(cooperation) — 정직한 구분

이 구분을 흐리면 보안 모델 전체가 착시가 된다.

| 층위 | 성질 | 프롬프트 인젝션에 견디는가 |
|---|---|---|
| egress 프록시 허용목록 / [2단계] NetworkPolicy | **강제** | **견딘다.** 프록시/커널이 막는다 |
| 도커 네트워크 분리 (`internal: true`) | **강제** | 견딘다. 라우팅 자체가 없다 |
| NATS ACL | **강제** | 견딘다. 서버가 거부 |
| `cap_drop`, `read_only`, non-root, resource limit | **강제** | 견딘다 |
| [2단계] RBAC / SA 토큰 미마운트 | **강제** | 견딘다 |
| Claude Code `--allowedTools` / `deny` | **협조** | **견디지 못한다** |
| 게이트 훅(PreToolUse) | **협조** | 견디지 못한다 |

**원칙: "막히지 않으면 회사가 손해를 보는가?"에 예라면 반드시 강제 층에서 막는다.** `--allowedTools`는 빠른 피드백과 UX를 위한 것이지 마지막 방어선이 아니다.

이 원칙의 구체적 귀결 — **승인된 외부 영향 액션은 에이전트가 실행하지 않는다.**

```
claude 가 `git commit` 시도
 → PreToolUse 훅(렌더된 settings.json) → supervisor 로컬 :8081/gate
 → supervisor: approval.request 발행 (payload: diff, taskId, employeeId, approvalType)
 → 훅은 블록(최대 24h. 그동안 하트비트는 계속 → liveness 영향 없음)
 → 대표가 콘솔에서 승인 → approval.decision.<id>
 → ★ 실제 커밋/푸시는 osiris-core 가 자기 자격증명으로 수행한다.
   에이전트에게는 "승인되어 반영됨"만 통보된다.
 → 거부면 사유가 claude 에게 전달되어 다른 방법을 찾거나 태스크를 반려
```
에이전트의 git 토큰은 **읽기 전용 스코프**만 갖는다. 쓰기 권한은 core에만 있다. 이것이 훅(협조)에 의존하지 않는 진짜 강제다.

`[미검증]` `--bare` 모드에서 `--settings`로 명시 전달한 파일의 `hooks` 섹션이 로드되는지 공식 문서로 확인되지 않았다. 로드되지 않으면 대안: (a) `--bare`를 빼고 `--settings`만 지정, (b) **MCP 툴 래핑** — git 조작을 `osiris-git` MCP 서버로만 노출하고 Bash에서 `git`을 deny. (b)가 더 견고하므로 훅이 안 되면 (b)로 간다. **어느 쪽이든 위의 "core가 대신 실행" 강제는 그대로다.**

### 8.3 격리 요약

에이전트 1명 = 컨테이너 1개 = 고유 볼륨 + 고유 자격증명 + 고유 NATS 사용자 + resource limit. 추가로:
- `runAsNonRoot`, `readOnlyRootFilesystem`(쓰기는 `/workspace`와 tmpfs `/tmp`만), `no-new-privileges`, `cap_drop: ALL`, [2단계] `seccompProfile: RuntimeDefault`
- 네트워크: `osiris-agents` 하나. 도달 가능한 것은 `nats`, `osiris-core:8090`, `osiris-egress:3128` 셋뿐. **Neo4j·orchestrator·dev-net에는 라우팅이 없다**(§2.6)
- 게이트웨이 접근도 egress 프록시 경유. 에이전트가 `ANTHROPIC_BASE_URL`을 스스로 바꿔치기해도 허용목록에 없으면 나가지 못한다
- docker.sock 절대 미마운트(G8)

**[2026-07-27 신설] 통합 어댑터(Plane 미러·텔레그램 알림 등, `50-integration-adapters.md`)는 에이전트가 아니지만 동일 등급으로 격리된다 — 근거는 다르다.** 에이전트는 "LLM이 생성한 임의 코드를 실행하는 신뢰 경계 안쪽 프로세스"라서 격리하고, 어댑터는 LLM을 실행하지 않지만 "우리가 통제하지 않는 제3자 코드"(향후 마켓스토어를 염두에 둔 신뢰 모델)라서 같은 등급을 받는다(`50-integration-adapters.md` §2.2). 구체적으로:
- 네트워크는 신설 `osiris-adapters`(`internal: true`) **하나뿐** — `osiris-agents`·`osiris-data`·`dev-net` 어디에도 붙지 않는다. **Neo4j·NATS에 직접 도달 불가**(에이전트와 동일 강제, D1을 어댑터에도 적용).
- `osiris-core`가 `osiris-adapters`에도 다리를 걸쳐(세 번째 대면 포트 `:8091`) 어댑터와만 REST+SSE로 통신한다 — `osiris-core:8090`(에이전트 대면)과는 별개 포트·별개 인증 스코프(`adapter.read`/`adapter.ack`).
- 어댑터의 아웃바운드(텔레그램 `api.telegram.org`, Plane 인스턴스 도메인)도 **`osiris-egress`를 예외 없이 경유**한다 — A3(전 외부통신 프록시 강제)를 어댑터에도 그대로 적용. `osiris-egress`가 `osiris-adapters`에도 이중 홈된다(§2.6 네트워크 배치표에 이 표의 위임 대상은 `50-integration-adapters.md` §2.2 — 그 문서가 `osiris-adapters` 행을 소유한다, 아래 §2.6 말미 참고).
- D10("`dev-net`에 발을 걸치는 OSiris 컴포넌트는 정확히 2개")은 불변이다 — 어댑터는 `dev-net` 멤버가 아니다.
- docker.sock 절대 미마운트(G8과 동일 원칙).

### 8.4 자격증명

| 자격증명 | 발급 | 저장·주입 | TTL / 회전 |
|---|---|---|---|
| 게이트웨이 토큰 | core가 omniroute 어댑터 `IssueToken()`으로 에이전트별 발급 | 파일 `/var/run/osiris/creds/gateway_token`(0400) → supervisor가 **자식 env에만** 주입 | 24h. 드라이버가 6h마다 갱신. **exec 시점에 파일을 읽으므로 재시작 없이 반영** |
| git 자격증명 | 에이전트별·리포별 fine-grained PAT. **읽기 전용 스코프**. 공용 조직 토큰 금지 | 파일 → `GIT_ASKPASS`로 자식에 주입 | 90일. 만료 30일 전 알림 |
| NATS 자격증명 | 드라이버가 팀 계정 하위 사용자 nkey/JWT 생성 | 파일 | 30일. 회전 시 supervisor 재연결(무중단) |
| core 대면 토큰 | 드라이버 발급. `osiris-core:8090` 호출용 | 파일 | 24h |
| 사람 콘솔 세션 | osiris-api | `HttpOnly; SameSite=Strict` 쿠키 + **`Secure`는 요청별 동적**(`r.TLS != nil \|\| X-Forwarded-Proto: https`일 때만 부여) — **[2026-07-27 정정]** 원문은 `Secure`를 무조건으로 적었으나, 그러면 상위 게이트웨이 없이 `http://127.0.0.1:<port>`로 접속하는 **기본 설치가 조용히 로그인 불가**가 된다(프레임워크의 이식성 요구와 정면충돌). 실측 확인: 루프백 응답엔 `Secure` 없음, `https` 경유 응답엔 있음 — `IMPLEMENTATION-NOTES.md` §87, 상세 근거는 `60-identity-authn.md` | 12h, 로그아웃 시 서버측 무효화 |
| [2단계] 노드 join 토큰 | osiris-api. 해시만 저장 | 1회용 | 10분 |
| watchdog 알림 자격증명 | 부트스트랩 대화형 입력 | 호스트 `/etc/osiris/watchdog.env` (0600, root) | 수동 |

**침해 대응**: 그래프에서 해당 에이전트를 `Suspended`로 바꾸면 컨테이너 삭제 + NATS 사용자 revoke + 게이트웨이 토큰 revoke가 한 번에 일어난다. 콘솔 Org의 `[⋯] → 격리`가 이 경로를 호출한다.

### 8.5 부트스트랩 시점의 인가 — 조직 밖 존재(genesis principal)

§8.1의 권한 파생과 `20-ontology-schema.md` §7.1의 `effective(party)`는 **`:Party`를 전제**한다. 그런데 최초 온보딩 시점에는 조직이 비어 있어 `:Party`가 존재하지 않는다. 대화를 맡는 임시 제네시스 에이전트 `bootstrap-master`(`30-onboarding-provisioning.md` §1.3)는 그래프 노드가 아니므로 이 계산의 대상이 될 수 없다.

**결정: `bootstrap-master`는 PermissionProfile에서 파생되지 않는 유일한 주체이며, `osiris-core`에 하드코딩된 고정 스코프로 인가된다.** 이것은 D13("선언 하나에서 네 층위 파생")의 **의도적이고 유일한 예외**다. 예외를 두는 대신, 아래 다섯 겹의 폐쇄 조건으로 **영구 백도어가 되는 것을 구조적으로 막는다.**

**genesis 토큰**

| 항목 | 값 |
|---|---|
| 발급 주체 | **`osiris-core`.** [2026-07-27 3차 정정] `osiris-up`은 더 이상 이 토큰을 발급하지 않는다 — 아래 참고. `osiris-orchestrator`가 `bootstrap-master` 컨테이너를 생성하는 시퀀스 도중(컨테이너 ID를 확보한 직후, 아직 기동하기 전) core에 발급을 요청하고, core가 그 요청에 응답해 발급한다 |
| 발급 조건 | **`currentOrgRevision == 0`이고, 요청에 실제 컨테이너 ID가 함께 와야 한다.** 판정은 `osiris-core` 자신이 요청 처리 시점에 Neo4j를 직접 읽어 수행한다(D17/G7 — core가 유일한 그래프 관문이므로 이 판정의 소유자이기도 하다). 이미 온보딩된 시스템(`currentOrgRevision >= 1`)에서는 이 요청 자체가 거부되어 새 토큰이 나오지 않는다. Neo4j를 읽을 수 없으면 거부한다(기존 fail-closed 원칙 무변경, 아래 참고) |
| 저장·주입 | 호스트 `state/creds/<containerID>/genesis_token`(0400) → 컨테이너 `/var/run/osiris/creds/`. **고정된 상주 경로(구 `state/creds/genesis/token`)는 사라진다** — 이 파일은 해당 `bootstrap-master` 컨테이너와 함께 생성되고(컨테이너 생성 직후, 기동 전에 기록) 그 컨테이너가 drain+delete될 때 함께 삭제된다. 일반 에이전트의 `<host>/creds/<id>/` 패턴(§1.4 `Apply()` 2단계)과 동일한 파일 주입 메커니즘(G5 준수) — 새 메커니즘이 아니라 그 패턴을 컨테이너 ID 키로 재사용한 것 ~~**`[2026-07-28 K2 전환]`**~~ **위 "저장·주입" 행 전체가 k8s에는 성립하지 않는다.** 호스트 경로 파일 주입, `ContainerCreate`가 반환하는 컨테이너 ID, "생성 직후·기동 전에 호스트 파일을 써 넣는다"는 절차 모두 Docker Engine API 고유의 성질이며 k8s에는 대응 API가 없다(실측 확인됨 — Pod 생성과 기동 사이에 이런 훅을 걸 표준 지점이 없다). **K7이 이를 대체한다: `bootstrap-master` 전용 ServiceAccount + projected service-account token(audience 한정) + `osiris-core`의 TokenReview 기반 검증.** "발급=바인딩" 이라는 이 절의 목표(아래 W4)는 K7에서는 "이 SA로 서명된 토큰만 core가 신뢰하고, 그 SA는 이 Pod에만 마운트된다"는 형태로 이행된다. 상세 메커니즘은 `70-kubernetes-deployment.md` §4.1(K7) 참고 |
| 허용 스코프 | **`[2026-07-28 4차 정정 — F14]`** `POST /onboarding/draft`, `GET /onboarding/status`, 카탈로그 **읽기 전용** 조회. **그 외 전부 403** — ~~`POST /onboarding/commit`~~(아래 4차 정정), graph-write의 일반 경로, 태스크 배정, 승인, 인박스 어느 것도 부를 수 없다. **이 주체는 조직 그래프를 한 바이트도 바꿀 수 없다.** **`[2026-07-28 K2 전환]`** 이 행 자체(허용 스코프 목록과 그 강제 논리)는 발급·저장 메커니즘이 Docker에서 k8s로 바뀌는 것과 무관하게 유효하다 — core가 무엇으로 신원을 확인하든(genesis 토큰이든 K7의 TokenReview 결과든) 허용목록 검사는 core 안에서 동일하게 일어난다 |
| NATS 권한 | 없음. `bootstrap-master`는 NATS 사용자를 발급받지 않는다(인박스도 하트비트도 없다). 따라서 `master.inbox`에 대한 권한이 조직 없이 부여되는 모순이 생기지 않는다 |
| 네트워크 | `osiris-agents` 하나. `osiris-core:8090`와 `osiris-egress`만 도달(§2.6의 기존 경계 재사용, 새 구멍 없음) |

**[2026-07-27 3차 정정] genesis 토큰의 발급 시점을 컨테이너 생성 시점으로 옮긴다 — 이 절의 이력.** 이 지점은 지금까지 두 번 고쳐졌다:

- **1차(원 설계)**: 발급(`osiris-up` 실행 중)과 `bootstrap-master` 생성이 같은 실행 안에서 사실상 동시였으므로 "발급 시 컨테이너 ID에 바인딩"이 문자 그대로 성립했다.
- **2차(직전 워커, TOFU)**: `30-onboarding-provisioning.md` §1.0(온보딩 0단계) 삽입으로 `bootstrap-master` 생성이 사람의 폼 입력 이후로 늦춰지자, 직전 워커는 **발급 시점은 그대로 두고**(`osiris-up` 실행 중) W4만 "발급 시 바인딩"에서 "최초 사용 시 바인딩"(TOFU)으로 고쳤다. 이 수정은 그 자체로는 정합적이었으나, 발급~최초 바인딩 사이에 **사람의 0단계 폼 입력이라는, 길이를 예측할 수 없는 구간**이 새로 끼어들고 그 구간에 토큰이 유출되면 유출자가 먼저 접속해 바인딩을 선점할 수 있다는 잔여 위험을 인정할 수밖에 없었다.
- **3차(이번 정정, 사용자 확정)**: 잔여 위험 자체를 없애기 위해 **발급 시점을 `bootstrap-master` 컨테이너 생성 시점으로 옮긴다.** 사람의 폼 입력(0단계)은 이제 발급보다 **앞서** 완전히 끝나 있어야 하는 전제조건이고(`30-onboarding-provisioning.md` §1.0.7), 발급과 생성이 다시 같은 시퀀스 안에서 일어나므로 W4가 원래 의도대로(발급=바인딩) 문자 그대로 성립한다 — TOFU가 더 이상 필요 없다. 이 변경이 지금 비용 없이 가능한 이유: genesis 토큰은 아직 구현되지 않았고 `CORE_API_TOKEN`이 그 자리를 임시로 메우고 있을 뿐이다(`IMPLEMENTATION-NOTES.md` §9 부근). 부수 효과: 호스트에 0400으로 상주하던 고정 경로(`state/creds/genesis/token`)가 없어진다 — 위 표 참고.

> **`[2026-07-28 K2 전환]`** 아래 7단계 시퀀스 전체는 **k8s에는 대응 API가 없어 그대로 성립하지 않는다**(실측 확인됨). `ContainerCreate`/`ContainerStart` 분리, 컨테이너 ID의 사전 확보, 호스트 파일을 생성-후-기동-전에 써 넣는 절차는 Docker Engine API에 고유하다. `70-kubernetes-deployment.md` §4.1(K7)이 이를 대체한다: 전용 ServiceAccount + projected token(TTL·audience 한정) + core의 TokenReview 검증. 아래 시퀀스는 **삭제하지 않고 이력으로 남긴다** — Docker 기반 설계가 "창을 0으로 만드는" 문제를 어떻게 풀었는지의 기록이며, K7이 같은 목표(발급과 신원이 분리되지 않는 것)를 다른 API 위에서 어떻게 달성하는지는 아래 W1~W5 대응표 참고.

**창(window)이 실제로 0이 되는 순서 — 이 정정이 새로 설계하는 유일한 부분.** 컨테이너 ID는 그 컨테이너를 만들기 전에는 존재하지 않으므로, "발급이 곧 바인딩"이 성립하려면 발급 시점에 이미 core가 그 ID를 알고 있어야 한다. 아래 순서로 이를 만족시킨다 — orchestrator의 기존 파일 주입 경로(G5·§1.4 `Apply()`)를 그대로 재사용하며, 새 메커니즘을 만들지 않는다:

1. `osiris-api`가 온보딩 0단계(§1.0.3의 0-1~0-5) 전부 통과를 감지 → `osiris-orchestrator`에 `bootstrap-master` 생성을 요청한다(`30-onboarding-provisioning.md` §1.0.7).
2. `osiris-orchestrator`가 호스트에 이 컨테이너 전용 자격증명 디렉토리(0700)를 준비하고 **`ContainerCreate`만 호출한다 — `ContainerStart`는 아직 호출하지 않는다.** 이미지·라벨(`osiris.dev/genesis=true`)·네트워크(`osiris-agents` 하나)는 §1.3과 동일. Docker Engine API에서 `ContainerCreate`는 프로세스를 기동하지 않고 컨테이너 ID만 확정한다는 점이 핵심이다 — 이것은 새로 만든 성질이 아니라, 기존 §1.4 `Apply()`가 이미 5단계(`ContainerCreate`)와 6단계(`ContainerStart`)를 분리된 두 호출로 다루고 있는 이 저장소의 기존 패턴 그대로다.
3. `ContainerCreate`가 반환한 실제 컨테이너 ID를 orchestrator가 획득한다. **이 시점까지 genesis 토큰은 아예 존재하지 않는다** — 발급 요청 자체가 아직 나가지 않았다.
4. orchestrator가 `osiris-core:8090`에 그 `containerID`를 실어 발급을 요청한다(`osiris-orchestrator`↔`osiris-core`, 기존 `CORE_API_TOKEN`으로 인증 — 아래 "무엇이 인증하는가" 참고).
5. `osiris-core`가 요청 처리 안에서 `currentOrgRevision == 0`을 확인하고, 참이면 **그 자리에서 토큰을 발급하며 동시에 그 `containerID`에 바인딩한다** — 발급과 바인딩이 하나의 원자적 호출이므로 "발급됐지만 아직 안 묶인" 상태가 애초에 존재하지 않는다. 거짓이거나 판정 불가면 거부한다.
6. orchestrator가 응답받은 토큰을 **컨테이너가 아직 시작되지 않은 상태에서** 호스트의 자격증명 디렉토리에 파일로 기록한다(`genesis_token`, 0400) — 이 디렉토리는 이미 2단계의 `ContainerCreate` 마운트 스펙에 `/var/run/osiris/creds`(ro)로 선언되어 있으므로, 컨테이너가 기동하면 그 내용이 곧바로 보인다(§1.4 `Apply()`의 파일 주입과 인과관계가 동일하고, 순서만 "생성 전에 기록"에서 "생성 직후·기동 전에 기록"으로 한 단계 재배치된 것뿐이다).
7. `ContainerStart`. 이 순간 전까지 이 토큰을 읽거나 쓸 수 있는 주체는 `osiris-orchestrator`(호스트 프로세스, 이미 신뢰 경계 안)와 `osiris-core`(발급자)뿐이었고, 네트워크로 도달 가능한 제3의 프로세스도 없었다(컨테이너가 기동하지 않았으므로 `osiris-agents`에 아직 아무것도 붙어 있지 않다).

**왜 "좁다"가 아니라 "0"인가.** 2차 정정이 인정했던 잔여 위험의 본질은 "유출된 토큰을 누가 먼저 써서 바인딩을 선점하는가"라는 **경쟁(race)**이었다 — 바인딩이 "누가 먼저 연결하는가"로 결정됐기 때문이다. 이번 설계는 바인딩을 그 경쟁에서 완전히 빼낸다: **바인딩은 core가 4~5단계에서 orchestrator의 인증된 요청 하나만 보고 일방적으로 확정**하며, 그 확정은 제3자가 "먼저 접속"하는 것으로 바꿀 수 있는 값이 아니다(연결 순서가 아니라 core의 단일 발급 결정이 바인딩의 유일한 원천이다). 설령 4~6단계 사이 어딘가에서 이 값이 관찰된다는 극단적 가정을 하더라도, 관찰자가 할 수 있는 것은 "이미 확정된 `containerID`를 사칭하는 것"뿐이며 이는 진짜 `bootstrap-master` 컨테이너 자체를 이미 장악해야 가능한 별개의 침해다(구판 W4에서도 마찬가지로 다섯 겹 폐쇄조건의 범위 밖이었던 위협). 즉 "유출 후 선점 경쟁"이라는 범주의 위험 자체가 이 설계에서는 성립할 수 없다 — 좁힌 것이 아니라 없앤 것이다.

**core가 발급 요청을 받을 때 무엇이 그 요청을 인증하는가.** 기존 `CORE_API_TOKEN`(`osiris-orchestrator`가 `osiris-core`의 graph-write API를 부를 때 이미 쓰는 정적 플랫폼 토큰, §9.5)으로 충분하다 — 이 요청도 "orchestrator가 core에게 보내는 플랫폼 내부 호출"이라는 점에서 기존 graph-write 호출과 성격이 같고, 새 신뢰 경계를 만들지 않는다. 이 인증이 의미를 갖는 이유는 **오직 `osiris-orchestrator`만 `docker.sock`을 가져 컨테이너를 실제로 만들 수 있고**(G8) 따라서 "방금 만든 컨테이너의 진짜 ID"를 주장할 수 있는 유일한 발신자이기 때문이다 — `osiris-api`를 포함한 어떤 다른 컴포넌트도 이 요청을 대신 보낼 능력(진짜 ID를 알 방법)이 없다.

**`currentOrgRevision == 0` 판정을 누가 하는가.** `osiris-core` 자신이다 — D17/G7("Neo4j 쓰기·읽기는 core의 graph-write API 단일 관문")과 정합하며, 별도 판정자를 두면 판정 로직이 두 곳에 존재하게 되어 그 자체가 새 불일치 원천이 된다. **판정 불가(Neo4j 다운) 시 거부한다는 §8.5의 기존 fail-closed 원칙은 무변경이다.**

이 순서는 §1.4 `Apply()`의 기존 create→start 분리, G5(파일 주입), G8(docker.sock은 orchestrator에만)과 전부 정합함을 확인했다 — 새 예외나 새 네트워크 경로를 만들지 않는다. **`[2026-07-28 K2 전환]`** 이 정합성 확인은 Docker 전제(`docker.sock`, `ContainerCreate`/`Start`) 위에서만 성립한다. K7로 옮기면 `docker.sock`·G8은 관여하지 않고, 대신 "이 SA·이 projected token은 이 Pod 밖으로 나가지 않는다"는 k8s의 볼륨 프로젝션 보장이 같은 역할을 한다.

이 정정이 아래 W1~W5를 깨는지 다시 검증했다(**Docker 전제 기준**. K2 전환 이후 각 항목이 K7에서 어떻게 다시 성립하는지는 바로 아래 표 다음에 정리한다):

- **W1(구조적 폐쇄)**: 안 깨진다. "조직이 비어 있는가"만으로 조건이 서술되어 있어 발급 시점 이동과 무관하게 그대로 성립한다 — 오히려 이번 정정으로 더 단순해졌다: 발급 자체가 없는 구간에는 요청할 genesis 토큰도 없다.
- **W2(24시간 TTL)**: 안 깨진다 — 그리고 **직전 워커가 남긴 경계조건("0단계가 24시간을 넘기면 만료될 수 있다")이 사라진다.** TTL 기산점이 이제 컨테이너 생성(=발급) 시점이므로, 사람의 0단계 폼 입력 구간은 이미 발급보다 **앞서** 완전히 끝나 있다 — TTL 24시간은 이제 `bootstrap-master`가 실제로 살아 있는 동안(1단계 온보딩 대화, §1.4/§1.5)에만 관련된다. 이론적으로 그 대화 자체가 24시간을 넘기면 토큰이 만료될 수 있으나, 이는 같은 컨테이너 ID에 대한 재발급으로 해결되는 정상 운영 상황이지 다섯 겹 폐쇄조건이 다루는 유출/경쟁 시나리오가 아니다.
- **W3(정상 경로 revoke)**: 안 깨진다. revoke는 `bootstrap-master`가 실제로 존재하고 온보딩이 커밋된 **이후**의 일이므로 발급 시점이 언제였는지와 무관하다.
- **W4(컨테이너 바인딩)**: **이번 정정으로 복구된다.** "발급 시 바인딩"이 다시 문자 그대로 성립한다(위 순서 1~7). 표를 원래 문구로 되돌리되, 한 번 TOFU로 깨졌다가 복구된 이력을 남긴다(같은 함정의 재발을 막기 위해) — 아래 표 참고.
- **W5(재온보딩 비재개방)**: 안 깨진다. 이 조건은 W1이 닫힌 *이후*의 이야기이므로 발급 시점 이동과 무관하다.

**결론: genesis principal의 다섯 겹 폐쇄 조건은 이번 정정으로 깨지지 않는다 — 오히려 W4가 2차 정정에서 안고 있던 잔여 위험이 구조적으로 제거된다.**

> **`[2026-07-28 K2 전환]` W1~W5는 폐기되지 않는다 — K7에서 각각 대응된다.** 위 다섯 겹은 Docker `ContainerCreate`/`Start` 분리를 전제로 서술되어 있으나, 그 **목표**(조직이 생기면 즉시 무력화/시간 상한/정상 폐기/발급-신원 불가분/재개방 금지)는 배포 API와 무관하게 유효하고 K7이 k8s 위에서 다시 충족시킨다:
> - **W1**(구조적 폐쇄) → core의 TokenReview 검증 경로에서 `currentOrgRevision` 재평가 그대로 유지. Docker/k8s 무관.
> - **W2**(TTL) → projected service-account token의 `expirationSeconds`로 표현(k8s 표준 필드).
> - **W3**(정상 revoke) → 온보딩 커밋 후 `bootstrap-master` Pod/CR 삭제(§1.5, K10의 ownerReference GC와 함께 — `30-onboarding-provisioning.md` §1.5 정정 참고).
> - **W4**(발급=신원 불가분) → "발급 시 컨테이너 ID 바인딩" 대신 "이 SA를 마운트한 Pod만 이 토큰을 낼 수 있다"는 k8s 볼륨 프로젝션 보장이 대신한다. 별도의 발급 API 호출·바인딩 원자성 문제 자체가 사라진다(SA가 곧 신원이므로).
> - **W5**(재온보딩 비재개방) → 무변경. `agent-master`가 정상 `:Party`로 수행.
>
> 상세 메커니즘·구현 계약은 `70-kubernetes-deployment.md` §4.1~§4.2(K7, W1~W5 대조표)가 소유한다. 이 문서(§8.5)는 위 대응관계만 인계하고, 세부 불일치가 발견되면 그쪽이 우선한다.

#### 8.5.1 `[2026-07-28 4차 정정 — F14, 사용자 확정]` `POST /onboarding/commit`을 스코프에서 제거한다

위 표의 허용 스코프에서 **`POST /onboarding/commit`이 빠졌다.** 이것이 이 절의 네 번째 정정이며, 앞선 세 번(발급 시점·바인딩 방식)과 달리 **스코프 자체를 좁히는** 유일한 정정이다.

**무엇이 문제였는가.** `30-onboarding-provisioning.md` O2는 처음부터 *"LLM은 구조화된 중간표현(`OrgDefinitionDraft`)을 만드는 것까지만"*이라고 정했다. 그런데 위 표는 `commit`을 허용하고 있었으므로, **LLM이 구동하는 컨테이너가 자기 초안을 스스로 확정하는 쓰기를 호출할 수 있었다.** O2가 정한 경계가 권한 차원에서는 강제되지 않는 상태였다 — 관습이었고, 스코프는 그것을 **표현 가능하게** 두고 있었다. `20-ontology-schema.md` §7.1이 인가를 "선언에서 파생"으로 만든 이유와 같은 문제다: 지켜야 하는 규칙은 표현 불가능하게 만들어야 지켜진다.

**그러면 누가 커밋하는가 — 승인한 사람의 세션이다.** 전체 흐름과 두 구간(1회차/2회차)의 호출자는 `30-onboarding-provisioning.md` §3.4가 소유한다. 요약하면:

| 구간 | 커밋 호출자 | 인가 |
|---|---|---|
| 1회차(조직 없음) | **master key 세션**(`kind:"master-key"`, `partyId` 없음) | `60-identity-authn.md` §4.1이 이미 정의한 그 세션. 귀속은 §4.1 ⑤의 자기귀속(`byPartyId` = 그 커밋이 만드는 초기 관리자) |
| 2회차 이후 | **`org.admin` 사람 세션** | `effective(party)` + DG1~DG5(§21.1). §21.4-3의 요구가 자동 충족된다 |

**이 정정이 §8.5에 주는 효과 — 예외가 작아진다.** 위 §8.5의 결정문은 이 고정 스코프를 *"D13의 의도적이고 유일한 예외"*로 인정하며 그 대가로 다섯 겹 폐쇄를 요구했다. 예외의 크기가 곧 위험의 크기이고, 이번 정정으로 예외에 남는 것은 **읽기와 초안 검증뿐**이다. 초안은 그래프에 살지 않는다(`30-onboarding-provisioning.md` §8: 초안의 상태 저장 위치는 채팅 트랜스크립트다). 즉 **genesis principal은 이제 조직 그래프에 대한 쓰기 능력을 전혀 갖지 않는다.**

**W1~W5 재검증 — 전부 무영향.** 스코프를 **좁히는** 방향이므로 다섯 겹 중 어느 것도 약해질 수 없다. 그리고 **W3이 지키려던 성질이 강화된다**: W3는 *"커밋 후 revoke"*로 "커밋 이후에는 아무것도 못 쓴다"를 보장했는데, 이제 그 성질이 **커밋 전에도 참**이다. W1(조직 있으면 발급 거부)·W2(24h TTL)·W4(발급 시 컨테이너 바인딩)·W5(재온보딩 비재개방)는 문자 그대로 성립한다.

**구현 계약(이 정정의 실질).** core의 genesis 스코프 검사는 **허용목록(allowlist)**이어야 하며 `commit`은 그 목록에 없다. 금지 목록이 아니라 허용 목록인 이유는 새 엔드포인트가 추가될 때 **기본이 거부**여야 하기 때문이다. 이 성질은 린트로 강제한다(`bin/lint-identity-authn.sh`의 `GEN` 검사: 고정 스코프 목록에 `commit`이 없고, 목록이 허용목록 형태다).

**창(window)이 닫히는 조건 — 다섯 겹**

| # | 조건 | 성질 |
|---|---|---|
| W1 | **요청 시점마다 재평가**: `currentOrgRevision >= 1` **이고** `:AIAgent{isMaster:true}`의 `:AgentRuntime.ready == true`이면 genesis 스코프 요청을 **무조건 403**. 토큰의 유효성과 무관하다 | **구조적 폐쇄 — 가장 중요.** 토큰이 유출되어도 조직이 존재하는 순간 무력해진다 |
| W2 | 하드 TTL 24시간(발급 시각 = 컨테이너 생성 시각 기준). 만료 시 자동 무효. **[2026-07-27 3차 정정]** 발급이 컨테이너 생성 시점으로 옮겨지며, 2차 정정이 명시했던 "0단계가 24시간을 넘기면 `bootstrap-master` 생성 전에 만료될 수 있다"는 경계조건은 **사라진다** — 0단계는 이제 발급보다 앞서 끝나 있어야 하는 전제조건이기 때문이다. 남는 것은 1단계 온보딩 대화 자체가 24시간을 넘기는 드문 경우뿐이며, 같은 컨테이너 ID에 대한 재발급(정상 재시도)으로 복구된다 | 시간 상한 |
| W3 | 온보딩 커밋 완료 루틴이 진짜 `agent-master`의 Ready를 확인한 직후 **명시적 revoke** + 컨테이너 `Drain→Delete` (`30-onboarding-provisioning.md` §1.5) | 정상 경로의 즉시 폐기 |
| W4 | **[2026-07-27 3차 정정, 이력 보존]** 토큰은 **발급 시** 그 컨테이너 ID에 바인딩된다 — 원래 문구로 복귀. **이력**: 2차 정정에서 온보딩 0단계 삽입으로 발급과 컨테이너 생성 시점이 분리되며 "최초 사용 시 바인딩"(TOFU)으로 한 차례 바뀌었고, 그 결과 발급~최초 바인딩 구간의 유출 시 선점 경쟁이라는 잔여 위험을 인정했었다. 3차 정정이 발급 시점 자체를 컨테이너 생성 시점으로 옮겨(위 "창이 실제로 0이 되는 순서") 그 구간을 없앴고, 그 결과 원래 문구가 다시 문자 그대로 성립한다 | 유출 시 재사용 완전 차단. **잔여 위험 없음** — 바인딩이 "누가 먼저 접속하는가"의 경쟁이 아니라 core의 단일 발급 결정으로 확정되므로, 선점 경쟁이라는 범주의 위험 자체가 성립하지 않는다(위 "왜 0인가" 참고) |
| W5 | **재온보딩은 창을 다시 열지 않는다.** 조직 변경을 위한 2회차 이후 온보딩은 진짜 `agent-master`(정상 `:Party`, 정상 PermissionProfile)가 수행한다 | 상시 재실행 요구와 특권 경로의 분리 |

W1이 핵심이다: **특권이 "토큰을 가졌는가"가 아니라 "조직이 비어 있는가"에 조건부**이므로, 조직이 한 번 생기면 그 특권은 되살릴 방법이 없다. W2~W4는 W1이 성립하기 전(온보딩 진행 중)의 창을 좁히는 보조 장치다.

**감사**: genesis 스코프 호출은 전부 `security.genesis.used` 이벤트로 `TASKS` 스트림에 발행되고, 온보딩 커밋 시 `:OrgRevision.summary`에 "genesis 경로로 생성됨"이 기록된다. 이후 어떤 조직 변경이 최초 부트스트랩에서 왔는지 영구히 구분된다.

**W1 판정이 불가능한 경우**(Neo4j 다운으로 `currentOrgRevision`을 읽을 수 없음): **거부한다.** fail-closed가 원칙이며, 부트스트랩은 재시도하면 되는 작업이다.

---

## 9. LLM 게이트웨이 어댑터 경계

omniroute 내부는 설계하지 않는다. OSiris가 **요구하는 계약**만 정의한다.

**I5 (사용자 확정) — 전용 인스턴스 신설.** §1.1의 배포 모델("프레임워크가 자기 스택을 자족적으로 갖는다")과 일관되게, `dev-net` 위의 **공용** omniroute(다른 프로젝트와 공유, 노출 별칭이 `auto/best-*`뿐이고 `osiris/*` 자격증명이 없어 `/v1/messages`가 404)를 계속 가리키는 대신, **OSiris 스택 내부에 전용 `osiris-omniroute` 컨테이너를 둔다.** 공용 인스턴스(`<host-infra>/omniroute`, 컨테이너명 `omniroute`, 볼륨 `omniroute_omniroute_data`)는 완전히 무관하게 그대로 운용된다 — 고치지도, 흡수하지도 않는다. 아래 §9.2는 이제 이 **전용 인스턴스**에 대한 재실측 결과다(구/신 인스턴스에서 동일하게 재현됨).

### 9.0 배치 — 어디에 있고 어떻게 도달하는가 (I5)

`osiris-omniroute`는 신설 내부 네트워크 `osiris-llm`(`internal: true`) **하나에만** 붙는다 — `osiris-agents`의 멤버가 아니므로 에이전트가 직접 도달할 경로는 없다(§8.3 "도달 가능한 것은 `nats`·`osiris-core:8090`·`osiris-egress:3128` 셋뿐"이 한 글자도 안 바뀐다). `osiris-egress`가 `osiris-llm`에도 이중 홈되어 에이전트의 CONNECT를 중계한다(`dev-net` 멤버 수는 `osiris-gw`/`osiris-egress` 둘로 불변 — D10 그대로).

`osiris-omniroute` 자신의 실제 LLM 프로바이더행 아웃바운드도 **`osiris-egress`를 HTTPS_PROXY/HTTP_PROXY로 강제**한다 — A3(전 외부통신 프록시 강제)에 예외를 두지 않는 결정. 근거는 실측이다: 이 이미지(`open-sse/utils/proxyFetch.ts`, `globalThis.fetch`를 패치)는 DB에 프로바이더별 프록시가 설정되지 않은 기본 상태(신설 인스턴스는 항상 이 상태)에서 `HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY`/`NO_PROXY` 표준 env를 그대로 읽어 적용한다(`resolveEnvProxyUrl`, `source: "env"`). 컨테이너를 실제로 부팅한 로그에서 이 경로가 실제로 타는 것도 확인했다: 백그라운드 Arena ELO 리더보드 동기화가 `[ProxyFetch] Proxy request failed (env, fail-closed): fetch failed`를 남겼다 — 목적지가 우리 테스트 허용목록에 없어 프록시가 거부한 것이지, 프록시를 우회해서 실패한 게 아니다. **잃는 것**: omniroute 자신의 아웃바운드가 `osiris-egress`의 도메인 허용목록(`egress/squid.conf`의 `llm_provider_domains`)에 걸리므로, "160+ providers"를 표방하는 이 앱이 지원하는 프로바이더 중 허용목록에 없는 것을 사용자가 새로 추가하면 그 도메인을 허용목록에 한 줄 추가해야 한다(무중단 `docker compose restart osiris-egress`) — 이것이 (a) 대신 (b)(omniroute에 자체 아웃바운드 망을 주는 예외)를 택하지 않은 이유다: 프록시 준수가 실측으로 확인된 이상, A3의 단일 감사 지점(egress 허용/거부 로그)을 유지하는 비용(허용목록 한 줄 추가)이 예외를 두는 비용(omniroute의 아웃바운드가 `osiris-egress`의 감사·강제에서 완전히 빠지고, 코드의 어떤 경로가 env를 안 보고 있었는지 우리가 검증할 수 없는 위험)보다 작다.

**A10 갱신**: dev-net 아웃바운드 핀홀(`osiris-egress`의 dev-net 이중 홈)의 존재 이유가 예전에는 "공용 omniroute(dev-net) 도달"이었다. I5로 게이트웨이 지향점이 내부 `osiris-omniroute`(osiris-llm)로 바뀌었으므로 **그 이유는 소멸했다** — 그러나 핀홀은 여전히 필요하다: `net.egress.github`/`net.egress.pkg` grant(§8.1)가 요구하는 실제 인터넷 목적지(github.com, registry.npmjs.org, pypi.org 등, 개발 에이전트의 외부 접근)가 `dev-net`의 NAT를 통해서만 나가기 때문이다. 핀홀을 없앨 수 있는지 검토했으나 — `dev-net`은 `external: true`(호스트가 만든 일반 브리지, NAT로 인터넷 라우팅)이고 `osiris-agents`/`osiris-llm`은 `internal: true`(라우팅 없음)이므로, `osiris-egress`가 dev-net을 완전히 떠나면 pkg/github 등 실제 외부 도메인으로 나갈 방법 자체가 없어진다. **결론: 핀홀은 유지하되 근거만 교체한다** — "omniroute 도달"이 아니라 "개발 에이전트의 실제 외부 인터넷 접근(github/npm/pypi 등)"이 지금의 근거다.

### 9.1 인터페이스 (osiris-core 내 단일 어댑터)

```go
type AgentIdentity struct {
    EmployeeID string   // "dev-a"
    TeamID     string   // "alpha"
    ModelAlias string   // "osiris/worker" — 프로바이더 모델 ID가 아님
}
type Token struct {
    ID string; Value string   // 평문. 로그·이벤트에 절대 싣지 않음
    BaseURL string            // ANTHROPIC_BASE_URL 에 들어갈 값
    ExpiresAt time.Time
}
type LLMGateway interface {
    IssueToken(ctx, AgentIdentity, ttl time.Duration) (Token, error)
    RevokeToken(ctx, tokenID string) error
    Usage(ctx, AgentIdentity, TimeRange) (UsageRecord, error)  // 정산 대조용(실시간 아님)
    Health(ctx) (Status, error)                                 // 무인증, <100ms
}
```

**LLM 호출 자체는 어댑터를 통하지 않는다** — `claude` CLI가 `ANTHROPIC_BASE_URL`로 직접 친다. 어댑터는 자격증명 수명주기와 정산 대조만 담당한다. 이 분리 덕분에 어댑터가 다운되어도 이미 발급된 토큰으로 에이전트는 계속 일한다.

### 9.2 omniroute에 요구하는 계약

1. `<BaseURL>/v1/messages`가 **Anthropic Messages API 와이어 호환**이며 **SSE 스트리밍 지원**. `[미검증]` — 미지원 시 Admin Chat의 `chat.token` 타이핑 효과가 불가능해지고 콘솔은 턴 단위 렌더로 저하된다.
2. `Authorization: Bearer <token>` 수용, 토큰별 신원·쿼터 분리.
3. 토큰 발급/폐기 관리 API (TTL 지정 가능). **I5 실측으로 충족 확인**: `POST /api/keys`(관리자 인증)가 즉시 사용 가능한 Bearer 토큰을 발급한다 — §9.4 참고.
4. `GET /healthz` 무인증, 100ms 이내. **재실측(I5, 전용 신설 인스턴스에서 같은 다이제스트로 재현)**: `/healthz` → 404, `/api/health` → 401 — 구/신 인스턴스 공통으로 이 계약을 만족하지 않는다. `/v1/models`는 무인증 200이며 모델 호출을 유발하지 않으므로(실측) 계속 이 우회를 쓴다(`OSIRIS_GATEWAY_HEALTH_PATH=/v1/models`) — 설계 위반이 아니라 게이트웨이 측 계약 미충족의 설정상 우회다.
5. **논리 모델 별칭 해석**: OSiris는 `osiris/planner`, `osiris/worker`, `osiris/cheap` 세 별칭만 보낸다. 실제 프로바이더·모델 선택은 게이트웨이 책임 → 프로바이더 교체가 OSiris 설정 변경 없이 가능. **I5 실측으로 등록 방법 확정**: omniroute의 "combo"(`POST /api/combos`, `name`에 `/`를 허용하는 정규식 `^[a-zA-Z0-9_/.\-\[\] ]+$`)가 이 별칭 메커니즘이다 — 공용 인스턴스에서 관측된 `auto/best-coding` 등 내장 별칭도 `owned_by: "combo"`로 노출됨을 확인했다. `osiris/worker`라는 이름으로 combo를 만들면 그대로 `/v1/models`에 노출된다(실측: 생성 직후 `GET /v1/models`에 `{"id":"osiris/worker","owned_by":"combo",...}` 확인). §9.4 참고.
6. 요청 헤더 `X-OSiris-Employee-Id`, `X-OSiris-Task-Id`를 로그에 보존(비용 귀속 대조).

### 9.4 구성은 코드가 한다 — 그러나 부트스트랩이 아니라 **온보딩**이 한다 (I5, 사용자 확정 · 2차 수정)

**최초 구현은 틀렸다.** `bin/configure-omniroute.sh`를 `osiris-up`(부트스트랩)에 편입시켜, 스택을 띄우자마자 `omniroute/config.json`의 선언대로 프로바이더/별칭/API 키를 자동 적용하게 했었다. **사용자가 이를 정정했다**: "온보딩에서 결정할 것을 미리 하지 마라. OSiris는 하나의 프레임워크다 — 내부가 MSA 형태로 쪼개져 있을 뿐, 부트스트랩은 빈 스택을 띄우는 것까지다." 이 정정은 §1.1 배포 모델과도 더 정합적이다 — 조직 구성(프로바이더 선택, 모델 매핑 확정)은 **온보딩이 결정할 조직 의도**이지, 부트스트랩이 대신 정해 둘 배포 세부가 아니다.

**바로잡은 경계**:
- **`osiris-up`(부트스트랩)은 `osiris-omniroute`에 아무것도 적용하지 않는다.** 빈 DB로 기동하고 healthy해야 정상이다(healthcheck는 무인증 `/v1/models`라 빈 상태에서도 200 — §9.2 계약 4번의 우회가 이 성질과 우연히 잘 맞는다).
- **구성은 `omniroute/operations.mjs`가 제공하는 오퍼레이션**(`registerProviderCredential`/`upsertAlias`/`upsertAliases`/`issueOrGetGatewayToken`/`applyGatewayTokenToEnv`/`getConfigurationStatus`)**으로 온보딩 파이프라인이 호출한다.** 이 모듈은 UI도, 대화 흐름도 아니다 — 파라미터를 받아 omniroute 관리 API를 수렴적으로(§ensureAlias 계열 — 두 번 불러도 실제 상태가 선언과 일치) 호출하는 순수 오퍼레이션 집합이다. 온보딩 UI/대화 흐름 자체는 이 문서의 범위 밖이며 온보딩 워커가 만든다(§9.6에 인계 사항).
- **`omniroute/config.json`은 이제 "적용되는 선언"이 아니라 "온보딩이 제시할 기본값(default)"이다.** 별칭 이름 세 개(`osiris/planner|worker|cheap`)는 §9.2 계약으로 고정이지만, 각 별칭이 가리키는 provider/model은 온보딩이 대표에게 확인받아 정하는 것이 맞다 — 이 파일은 그 확인 단계의 시작값을 담을 뿐 자동 적용되지 않는다(`getDefaultAliasMapping()`으로 읽기만 함).

**관리 API 표면 — 실측으로 확인(그대로 유효, 호출 시점만 바뀜)**:
- 인증: `POST /api/auth/login {password}` → `Set-Cookie: auth_token=<JWT>`(30일). 관리자 비밀번호는 `INITIAL_PASSWORD` env(최초 부팅 시 bcrypt 해시로 DB에 영속화)로 부트스트랩한다. `requireManagementAuth`는 이 쿠키 **또는** `manage` 스코프를 가진 API 키의 `Authorization: Bearer`를 동등하게 받는다.
- 프로바이더: `GET/POST /api/providers`, `PUT /api/providers/{id}` — `{provider, apiKey, name}`.
- 별칭(§9.2 계약 5번): `GET/POST /api/combos`, `PUT /api/combos/{id}` — `{name, models:[{provider, model}]}`.
- API 키(에이전트가 실제로 쓰는 것): `GET/POST /api/keys`, `PATCH /api/keys/{id}` — `{name, scopes}`. 평문 값은 생성 응답에만 실리고, 이후 재조회는 `GET /api/keys/{id}/reveal`(env `ALLOW_API_KEY_REVEAL=true` 필요)로만 가능.
- 모든 오퍼레이션이 이름 기준 존재 여부를 먼저 조회하고, 있으면 **선언과 실제가 다를 때만** 갱신한다(단순 "있으면 스킵"이 아니다 — 이게 원래 결함이었다, §9.6 참고).

### 9.5 프로바이더 자격증명 경계 — omniroute가 소유, OSiris는 게이트웨이 토큰만 (I5, 사용자 확정 · 2차 수정)

**최초 구현의 두 번째 결함**: `omniroute/config.json`의 `apiKeyEnv`가 `.env`의 `ANTHROPIC_API_KEY`를 가리키게 하고, OSiris 쪽 스크립트가 그 값을 읽어 omniroute로 밀어넣었다. **사용자가 지적**: "그 API 키 관리하는 역할을 하려고 omniroute를 도입하는 건데 어불성설임." omniroute를 도입한 이유 자체가 프로바이더 자격증명을 한 곳에서 관리하기 위함인데, OSiris(`.env`, 백업, 배포 파이프라인)가 그 키를 또 들고 있으면 시크릿 보관 지점이 하나 늘어난 것뿐이다.

**바로잡은 경계**: **omniroute가 프로바이더 자격증명(Anthropic 등)의 유일한 소유자다. OSiris는 자기 게이트웨이 토큰(`OSIRIS_GATEWAY_TOKEN` — omniroute가 OSiris에게 발급한 추론 전용 API 키)만 갖는다.** A4가 "core 이미지에 게이트웨이 자격증명을 넣지 않는다"고 강제한 것과 같은 원칙을 프로바이더 키에도 적용한 것 — 자격증명은 그것을 실제로 쓰는 컴포넌트 하나에만 있어야 한다.

- **OSiris(이 저장소, `.env` 포함)가 절대 보유하지 않는 것**: 프로바이더 API 키. `.env`/`.env.example`/`config.json`/스크립트/문서 어디에도 두지 않는다.
- **OSiris가 보유해도 되는 것**: `OSIRIS_GATEWAY_TOKEN`. 이것은 OSiris 자신의 자격증명이므로 `.env`에 있는 것이 맞다.
- 프로바이더 키 투입 경로: 온보딩 콘솔이 대표에게서 키를 받아 `registerProviderCredential({name, provider, apiKey})`로 omniroute 관리 API에 바로 전달한다. 그 함수는 값을 파일에도, 로그에도 쓰지 않는다(코드 검색으로 확인 가능 — `writeFileSync`/`appendFileSync` 호출이 그 함수 안에 없다).

**실측 근거(재확인)**: `ANTHROPIC_API_KEY`가 없어도 별칭(combo)과 에이전트 API 키는 정상 생성/갱신되고 `/v1/models`에 별칭 3개가 노출된다 — combo의 `models` 항목은 프로바이더 커넥션을 참조하는 FK가 아니라 `{provider, model}` 문자열 쌍일 뿐이기 때문이다. 다만 참조된 프로바이더가 실제로 등록되어 있는지는 `upsertAlias`가 매번 검증하고, 없으면 그 별칭 하나를 실패로 기록한다(생성/갱신을 시도하지 않음) — "키 없이 갈 수 있는 데까지"와 "선언이 가리키는 전제조건은 검증한다"를 동시에 만족시킨다. **실제 추론(진짜 프로바이더 키로 `/v1/messages` 왕복)은 여전히 `[미검증]`이다** — 사용자의 진짜 프로바이더 키를 이 세션이 알아낼 방법도, 그래야 할 이유도 없다.

**같은 경계가 omniroute 관리자 비밀번호에도 적용된다(I5 3차 수정)**: 최초 구현이 `OSIRIS_OMNIROUTE_ADMIN_PASSWORD`를 설치 시점에 생성해 `.env`에 저장하고 `docker-compose.yml`에서 `:?`(필수)로 강제했다 — 이건 "온보딩에서 결정할 것을 미리 하지 마라"는 같은 원칙을 사람 대면 자격증명에도 적용하지 않은 것이다. omniroute 자신에게 이미 첫 접속 온보딩 흐름이 있다(실측: `managementPassword.ts`가 비밀번호 미설정 시 로그인을 403+`needsSetup:true`로 거부하고, `login/page.tsx`가 `/api/settings/require-login`의 `hasPassword:false`를 보고 자체 `/dashboard/onboarding` 화면으로 안내 — 이 화면이 `POST /api/settings/require-login {requireLogin, password}`로 최초 비밀번호를 사람에게서 직접 받는다). **그래서 이 값은 omniroute 자신에게 완전히 맡긴다** — `INITIAL_PASSWORD: ${OSIRIS_OMNIROUTE_ADMIN_PASSWORD:-}`(옵셔널, 기본 빈 문자열)로 바꿨고, 새 설치는 이 값을 절대 미리 만들지 않는다. `docker compose up -d`는 이 값 없이도 전 서비스 healthy해야 한다(이식성 검증의 일부, §2.0).

**OSiris가 만들어 `.env`에 저장하는 나머지 시크릿의 판정표** — "내부 서비스간 자격증명(사람이 절대 안 보고, 안 쳐도 됨)"과 "사람 대면 자격증명(온보딩이 다뤄야 함)"을 구분한다:

| 변수 | 소비자 | 판정 | 근거 |
|---|---|---|---|
| `NEO4J_AUTH`/`NEO4J_PASSWORD` | `osiris-core`만 bolt 접속에 사용. 사람이 이 값을 입력/열람할 일이 없음(Neo4j Browser도 노출 안 함, §1.2 결정) | **정당** — 설치 시 자동 생성 유지 | 순수 플랫폼 내부 인증. I2가 이미 `.env`에 둔 선례(G5는 "에이전트 자격증명"에 대한 요구이지 플랫폼 컴포넌트가 아니다) |
| `CORE_API_TOKEN` | `osiris-orchestrator`(및 미래의 `osiris-api`)가 `osiris-core`의 graph-write API를 호출할 때 사용. 사람이 입력하지 않음 | **정당** — 유지 | 같은 이유. §8.5 genesis 모델의 임시 단순화(I2 §9)이며 정적 플랫폼 토큰이라는 성격 자체는 바뀌지 않았다 |
| `NATS_CORE_PASSWORD`/`NATS_ORCHESTRATOR_PASSWORD` | `osiris-core`/`osiris-orchestrator`가 NATS에 붙을 때 사용. 사람이 입력하지 않음 | **정당** — 유지 | 같은 이유. I3 §17이 이미 이 경계를 명시("에이전트별 NATS 자격증명은 파일 주입(G5), 플랫폼 컴포넌트 것은 env") |
| `OSIRIS_OMNIROUTE_ADMIN_PASSWORD` | **사람**이 브라우저로 `/omni/`에 로그인할 때 직접 입력하는 값 | **부당했다 — 수정함** | 사람이 실제로 보고 입력하는 계정 자격증명이라 프로바이더 키(§9.5)와 같은 부류다. 온보딩(또는 omniroute 자신의 첫 접속 화면)이 다뤄야 하는데 설치 스크립트가 대신 만들어 버렸다 — 이번에 옵셔널로 되돌렸다 |
| `OSIRIS_GATEWAY_TOKEN` | 에이전트 자식 프로세스(`ANTHROPIC_AUTH_TOKEN`). 사람이 입력하지 않음(omniroute가 발급) | **정당(현재 단계)** | 발급 주체가 omniroute이고 값 자체는 기계가 소비한다. §8.4가 요구하는 "에이전트별 파일 주입"은 아직 구현되지 않아 정적 env로 전 에이전트 공유 중인 기존 격차(I4 §30) — 이건 별개 문제이지 이번 판정의 대상이 아니다 |
| **[2026-07-27 신설]** `plane_api_token`(파일 `/var/run/osiris/creds/plane_api_token`, `.env`에는 절대 두지 않음) | **사람**이 Plane 자신의 워크스페이스 설정 화면에서 발급받아 온보딩 연동 화면에 붙여넣음. 소비자는 `osiris-adapter-plane` 컨테이너뿐 — `osiris-core`는 이 값을 보지 않는다 | **정당 — 사람 대면이지만 저장 자체는 옳다** | `31-onboarding-boundary.md` §1.3의 Q2b(교환 경로 존재 여부)를 적용한 결과다: Plane의 워크스페이스 API 토큰은 애초부터 계정 비밀번호가 아니라 이미 스코프 제한된 API 전용 자격증명이므로 "교환"이 필요 없고, 사람이 건네는 값 자체가 이미 Q2b가 요구하는 최종 형태다(`50-integration-adapters.md` §2.3). G5(파일 주입, `.env` 금지)를 어댑터 자격증명에도 그대로 적용 — `.env`에 두면 사건 1(프로바이더 키)의 재발이 된다 |
| **[2026-07-27 신설]** `telegram_bot_token`(파일 `/var/run/osiris/creds/telegram_bot_token`) | **사람**이 텔레그램 `@BotFather`에서 발급받아 온보딩 연동 화면에 붙여넣음. 소비자는 `osiris-adapter-telegram` 컨테이너뿐 | **정당 — 사람 대면이지만 저장 자체는 옳다** | 같은 Q2b 판정, 다른 근거: 봇 토큰은 설계상 딱 그 봇 하나만 인증하는 단일 목적 기계 자격증명이며 사람의 텔레그램 계정 자체를 대표하지 않는다(계정 비밀번호와 봇 토큰은 텔레그램이 애초에 분리해 둔 별개 개념) — "사람이 건네는 값이 이미 스코프 제한된 기계 자격증명"이라는 이유로 원본을 그대로 저장해도 사건 4(교환 없이 원본 저장)의 재발이 아니다. 파일 주입만, `.env` 금지(위와 동일 원칙) |

**분류 기준을 한 줄로**: *"이 값을 사람이 화면에서 직접 보거나 입력하는가?"* — 그렇다면 온보딩 소관(설치 스크립트가 미리 만들지 않는다). 아니라면(순수 기계 간 인증) 설치 시 자동 생성 + `.env`가 정당하다.

### 9.6 다음 워커(온보딩)를 위한 인계 — 오퍼레이션 계약과 순환 문제

**오퍼레이션 계약**은 `omniroute/operations.mjs`가 정의한다(전부 async, 구조화된 결과를 돌려주고 치명적 오류에서만 throw):
- `registerProviderCredential({name, provider, apiKey})` — 프로바이더 등록/갱신(blind overwrite). 온보딩이 사람에게서 받은 키를 그대로 전달.
- `upsertAlias({name, providerName, provider, model})` / `upsertAliases(entries)` — `osiris/*` 별칭 수렴. 참조된 프로바이더가 없으면 `{ok:false, providerMissing:true}`.
- `issueOrGetGatewayToken({name, scopes})` — OSiris용 추론 전용 API 키 발급/재조회. `applyGatewayTokenToEnv(token)`은 그 값을 `.env`에 반영하는 별도 헬퍼(자동으로 불리지 않음 — 온보딩이 "지금 반영해도 될까"를 스스로 결정).
- `getConfigurationStatus()` — 지금 무엇이 있고 무엇이 비었는지 읽기 전용 보고. `30-onboarding-provisioning.md`의 `GET /onboarding/status`와 맞물리는 지점.
- osiris-core(Go)가 이 오퍼레이션들을 직접 import할 수 없으므로, `node omniroute/operations.mjs <op>`로 서브프로세스 호출 + stdin/stdout JSON 한 줄이라는 교차 언어 계약도 함께 제공한다(파일 헤더 참고).
- `omniroute/config.json`의 `getDefaultAliasMapping()`이 "온보딩이 제시할 기본값"을 준다 — 대표가 확인/수정한 뒤 `upsertAlias`로 적용하는 흐름을 온보딩이 구현할 것.

**감독이 발견한 순서 문제 — bootstrap-master의 순환 의존**: `30-onboarding-provisioning.md`는 온보딩 대화를 조직 밖 임시 제네시스 에이전트(`bootstrap-master`)가 맡는다고 결단했다. 그런데 그 에이전트도 LLM을 쓰려면 게이트웨이(`osiris-omniroute`)가 이미 구성되어 있어야 한다 — "게이트웨이를 구성하는 대화"를 "게이트웨이가 필요한 에이전트"가 진행하는 순환이 생긴다. **해소 방향(이 워커의 판단이 아니라 인계 — W3 설계가 이미 깔아둔 원칙과 같은 모양)**: `30-onboarding-provisioning.md`의 W3가 "LLM은 `OrgDefinitionDraft`까지만 만들고 나머지는 결정적 코드"라고 이미 정했다. 같은 논리로 **온보딩 0단계(스택 배선: 프로바이더 키 투입 → 별칭 확정 → 게이트웨이 토큰 발급)는 LLM이 필요 없는 결정적 단계**여야 한다 — 온보딩 콘솔(폼 UI)이 사람에게서 직접 키를 받아 위 오퍼레이션들을 호출하고, `bootstrap-master`의 대화형 온보딩(조직 구조 드래프트 등)은 게이트웨이가 살아난 **다음**부터 시작되어야 순환이 끊긴다. 이 문서(설계)는 이 순환의 존재와 해소 방향만 기록한다 — `30-onboarding-provisioning.md`를 실제로 고치는 것은 온보딩 워커의 몫이다.

**[2026-07-27 해소 확인]** `30-onboarding-provisioning.md` §1.0(O11~O14)이 이 인계를 정확히 이 해소 방향대로 반영했다 — 온보딩 0단계가 신설되어 `bootstrap-master` 생성을 그 뒤로 늦췄다. `10-core-architecture.md` 쪽의 대응 정정은 §4.1과 §8.5에 반영했다(생성 시점/주체 변경). **[2026-07-27 3차 정정으로 갱신]** 처음에는 genesis 토큰의 발급 시점은 그대로 두고 소비(바인딩) 시점만 TOFU로 늦췄으나, 그 잔여 위험을 없애기 위해 **발급 시점 자체를 `bootstrap-master` 컨테이너 생성 시점으로 옮기는 쪽으로 다시 정정했다** — §8.5에 창이 0이 되는 구체적 순서(`ContainerCreate` → ID 확보 → core 발급·바인딩 → 파일 주입 → `ContainerStart`)와 W1~W5 재검증을 반영했다. W4는 "발급 시 바인딩"이라는 원래 문구로 복귀했다.

### 9.7 서브패스 마운트 — 실측 결과 불가 (I5)

프레임워크당 도메인 하나 원칙(§2.9)에 따라 `osiris.example.com/omni/`로 대시보드를 마운트하는 방안을 최우선으로 검토했다. omniroute의 `next.config.mjs`는 `basePath: process.env.OMNIROUTE_BASE_PATH || ""`로 **서브패스 배포를 명시적으로 지원한다**(코드 주석: "Opt-in subpath deployment behind a reverse proxy"). 그러나 **Next.js의 `basePath`는 `next build` 시점에 번들에 굳는 값이라, 이미 빌드되어 배포된 이미지에 런타임 `OMNIROUTE_BASE_PATH` env를 주는 것으로는 바뀌지 않는다** — 실측: 이 다이제스트에 `OMNIROUTE_BASE_PATH=/omni`를 주고 기동해도 `GET /omni/` → 404, `GET /` → 200(마치 env가 없었던 것처럼 루트에서 그대로 서빙)이었다. `sub_filter` 등으로 에셋 경로를 강제로 재작성하는 우회는 깨지기 쉽고(업그레이드마다 재검증 필요) 채택하지 않는다 — 특히 basePath 없이 `/omni/`만 벗겨 프록시하면 `/api/*`가 osiris-api용 네임스페이스(§2.9)와 충돌한다.

**결론: 루프백 전용으로 남긴다.** `osiris-omniroute`는 `osiris-llm`(internal) 하나에만 붙어 자신은 host 포트를 열지 않는다(실측: Docker는 `internal: true` 네트워크에만 붙은 컨테이너의 host 포트 publish를 아예 세팅하지 않는다 — `HostConfig.PortBindings`는 기록되지만 `NetworkSettings.Ports`가 항상 `{}`로 비어 실제 바인드가 안 됨, 별도 격리 네트워크로 재현 확인). 대신 애플리케이션 로직이 전혀 없는 socat 중계 컨테이너(`osiris-omniroute-lo`, §2.6 DMZ 다리 패턴 재사용)만 별도 non-internal 망 + `osiris-llm`에 이중 홈해 `127.0.0.1:${OSIRIS_OMNIROUTE_LOOPBACK_PORT:-20129}`를 연다 — 기존 20128과 겹치지 않고, 외부 인터페이스에서는 도달 불가, `osiris-omniroute` 자신에게 A3를 우회할 두 번째 인터페이스를 주지 않는다. **이 포트는 구성 자동화에 쓰이지 않는다**(§9.4가 그 경로) — 사람이 어쩌다 대시보드/`call_logs`를 들여다볼 때만 쓰는 순수 편의 통로다. 벤더 이미지를 직접 소스에서 빌드(`OMNIROUTE_BASE_PATH=/omni` 고정)하는 방향으로 가면 §2.9의 `/omni/` 행을 채울 수 있다 — 다음 워커 인계 사항.

---

## 10. 장애 모델

### 10.1 무엇이 죽으면 무엇이 멈추는가 [1단계 기준]

| 죽는 것 | 소유 | 즉시 멈추는 것 | 계속 도는 것 | 감지 | 복구 |
|---|---|---|---|---|---|
| **상위 리버스 프록시** | **외부** | 콘솔 접근(전면). 사람의 승인·지시 불가 | **에이전트 조직 전체가 계속 돈다** — LLM 호출은 egress→omniroute 직결이라 상위 리버스 프록시를 타지 않는다(§2.6). 배정·실행·승인 대기·비용 집계 모두 정상 | watchdog(호스트에서 `:443` 확인) | OSiris 소유가 아님. **대역외 알림으로 대표에게 통지**. OSiris는 아무것도 하지 않는다 |
| **osiris-gw** | OSiris | 콘솔 접근 | 위와 동일 | watchdog + 상위 리버스 프록시 502 | `restart: unless-stopped`. 상태가 없어 즉시 회복 |
| **osiris-api** | OSiris | 콘솔 전면 불통 | 에이전트 작업, 배정, 승인 큐 적재(결정만 못 함) | watchdog | 재시작. SSE는 `Last-Event-ID`로 이어짐(유실 없음) |
| **Neo4j** | OSiris | 신규 배정, 조직도/온톨로지 조회, 온보딩, WBS 생성 | **이미 배정된 태스크 수행, 승인 게이트, 로그·비용 수집** | core readiness + watchdog bolt TCP | 재시작 → 실패 시 최근 dump 복원(RPO 1시간) |
| **NATS** | OSiris | **가장 넓게**: 인박스 전달, 하트비트, 콘솔 SSE, 승인 라우팅 | 진행 중인 `claude` 턴(결과는 supervisor outbox에 스풀) | 전 컴포넌트 즉시 | 재시작(JetStream file store가 볼륨에 있어 상태 보존) → supervisor들이 outbox 재전송 |
| **osiris-core** | OSiris | 신규 배정, 롤업, 알림 에스컬레이션, 그래프 쓰기, **승인된 액션의 실제 실행** | 에이전트 작업, 콘솔 읽기 | api 헬스, watchdog | 재시작. [2단계] replicas 2 + Lease |
| **osiris-orchestrator** | OSiris | 신규 에이전트 생성, 설정 반영, 자격증명 회전, **liveness 집행** | 기존 에이전트 전부 | watchdog | 재시작. 기동 시 그래프에서 전량 재계산해 수렴 재개 |
| **osiris-egress** | OSiris | **모든 LLM 호출과 외부 통신** | 배정 계산(코드), 콘솔, 승인 대기 | supervisor gateway probe → 전 에이전트 `readyz=false` | 재시작. 태스크는 큐에 남아 유실 없음 |
| **omniroute** | 외부 | 모든 LLM 작업 | 배정 계산, 콘솔, 승인 | supervisor probe(60초) | 외부 의존. 전 에이전트가 후보에서 빠지고 대표에게 알림 |
| **dockerd** | OSiris(호스트) | 전부 | 없음 | watchdog | watchdog가 `systemctl restart docker`(30분당 1회) → 실패 시 대역외 알림 |
| **호스트** | — | **전부** | 없음 | **같은 호스트의 watchdog도 죽으므로 자체 감지 불가** | §10.3 |
| [2단계] 워커 노드 1대 | OSiris | 그 노드의 에이전트 | 나머지 | NotReady(40초) | Longhorn 복제본이 있으면 다른 노드에 재생성 + 체크포인트 재개 |

### 10.2 저하 모드 — 설계된 성질

- **Neo4j 다운 = 조직이 얼어붙지만 멈추지는 않는다.** 이미 배정된 일은 끝까지 간다. 우연이 아니라 설계다 — 배정 결과가 이미 NATS 인박스 메시지로 전달되어 있고, **supervisor는 Neo4j에 접속하지 않는다**(불변식으로 유지할 것).
- **NATS 다운 = 새 일은 안 오지만 하던 일은 끝난다.** 결과는 outbox에 쌓였다가 복구 시 재전송.
- **상위 리버스 프록시 다운 = 사람은 못 보지만 조직은 일한다.** §2.6에서 egress를 dev-net 직결로 둔 이유가 정확히 이것이다. 만약 omniroute를 `omniroute.example.com`(공개 이름)로 접근했다면 상위 리버스 프록시 장애가 곧 전 조직 정지가 됐을 것이다.
- **게이트웨이 다운 = 조직 전체 유급 대기.** 태스크 유실 없음.

### 10.3 컨트롤 플레인 장애의 역설 — W4 제안 평가와 대안 (D11)

**W4 제안**: `cluster.heartbeat`(10초) 부재를 콘솔이 신호로 삼는다.

**평가: 필요하지만 불충분하다.** 채택하되 단독으로는 쓰지 않는다.
1. **보는 사람이 있어야만 작동한다.** 새벽 3시에는 아무도 모른다.
2. **원인을 구분하지 못한다.** SSE 두절은 "클러스터 다운", "게이트웨이 재시작", "대표의 와이파이 끊김", "**상위 리버스 프록시 장애**"를 구분하지 못한다. 오탐 배너가 반복되면 진짜 경보가 무시된다.
3. **알릴 수단이 함께 죽는다.** 사람 알림 경로(core의 notify 결정 로직 → 격리된 알림 어댑터 → 텔레그램/SMTP 등, [2026-07-27 정정])가 같은 스택 안에 있고, 게다가 콘솔 도달 경로는 **OSiris가 소유하지도 않은 상위 리버스 프록시**를 지난다.

**대안: `osiris-watchdog` — 컨테이너 밖, 호스트 systemd 유닛**

```
Go 단일 바이너리 systemd 유닛. 컨테이너가 아니며 dockerd/k3s에 의존하지 않는다.

10초 주기 로컬 체크:
  ① systemctl is-active docker        ([2단계] k3s)
  ② 컨테이너 헬스     docker inspect 로 코어 7개 + 에이전트 전체
     ([2단계] https://127.0.0.1:6443/readyz)
  ③ NATS   127.0.0.1:<port>/healthz
  ④ Neo4j  bolt TCP connect
  ⑤ osiris-api  /healthz
  ⑥ osiris-gw   /  (200 기대)
  ⑦ <upstream-proxy> https://127.0.0.1:443  (SNI osiris.example.com)   ← 외부 의존성도 관측
  결과를 로컬 파일 저널에 기록 (스택 의존 0)

피어 가십 [2단계]: :9099/peer 로 5초마다 상호 확인
  → 한 노드가 통째로 죽어도 생존 피어가 안다
  → 고립된 watchdog은 "내가 격리됐다"와 "전체가 죽었다"를 구분할 수 있다

대역외 알림 (★ <upstream-proxy> 도, OSiris 스택도 경유하지 않는다):
  3회 연속 실패 시 /etc/osiris/watchdog.env 의 채널로 직접 발송
  (SMTP 릴레이 / Telegram Bot / SMS 웹훅 중 부트스트랩 시 최소 1개 필수)
  ※ 이 경로가 <upstream-proxy>를 타면 안 되는 이유: <upstream-proxy> 장애를 <upstream-proxy>로 알릴 수 없다.

제한적 자가치유:
  dockerd 비활성 → systemctl restart docker.  30분당 최대 1회. 플래핑 방지.
  그 외 컴포넌트는 재시작하지 않는다(오케스트레이터가 할 일이므로).

콘솔 폴백 엔드포인트:
  GET https://<host>:9099/watchdog/health
  → { node, checks: {docker, containers[], nats, neo4j, api, gw, upstream_gw}, peers[], since }
  ※ 이 포트는 <upstream-proxy>를 거치지 않고 호스트에서 직접 노출된다(콘솔이 별도 오리진으로 조회).
```

**콘솔 판정 규칙** — 이 조합이 오탐을 제거한다:

| `cluster.heartbeat` | watchdog 직접 조회 | 콘솔이 표시하는 것 |
|---|---|---|
| 수신 중 | — | 정상 (배너 접힘) |
| 20초 이상 부재 | 응답 있음, 모든 체크 정상 | "콘솔 연결 문제" — 재연결 중 (**경보 아님**) |
| 20초 이상 부재 | 응답 있음, `upstream_gw` 만 실패 | "외부 게이트웨이 장애 — **조직은 계속 일하고 있습니다**" (별도 등급) |
| 20초 이상 부재 | 응답 있음, 내부 체크 실패 | **최우선 경보 배너** + 무엇이 죽었는지 구체적으로 |
| 20초 이상 부재 | 모든 조회 실패 | "네트워크 확인 필요 또는 전면 장애" + 대역외 알림이 이미 발송되었을 것임을 안내 |

이로써 콘솔 배너는 **추측이 아니라 관측**이 되고, "조직은 도는데 화면만 안 보이는" 상황을 구분해서 말할 수 있다.

**정직하게 남는 한계**: 단일 호스트에서 그 호스트 전체(전원·커널 패닉)가 죽으면 watchdog도 함께 죽어 대역외 알림조차 나가지 않는다. 소프트웨어로 해결 불가. 완화: (a) 부트스트랩이 "[2단계] 노드 2대 이상 권고"를 명시, (b) 외부 uptime 모니터가 `https://<host>:9099/watchdog/health`를 5분 주기로 조회하도록 등록하는 절차를 설치 가이드에 포함(OSiris 밖이므로 안내만).

---

## 11. 미해결 쟁점 13개 — 결정표

| # | 쟁점 | 결정 | 근거 | 재검토 트리거 |
|---|---|---|---|---|
| 1 | supervisor 구현 방식·폴링 주기·토큰 비용 | **Go 단일 정적 바이너리, 컨테이너 PID 1.** 폴링이 아니라 JetStream pull consumer long-poll(`Fetch(1, MaxWait=30s)`). 유휴 시 `claude` 프로세스 부재 → **토큰 0**. 단계 무관 동일 | SIGTERM 선점 필요(§5.1), 유휴 비용 0 요구 | 에이전트 60명 초과(NATS 연결 수) / Claude Code 외 CLI 지원 필요 시 |
| 2 | liveness/readiness 판정 기준 | **livez = supervisor `select` 루프 tick(<30s) + NATS 연결 + reaper 생존. `claude` 실행 여부를 보지 않는다.** readyz = 자격증명·설정·게이트웨이·consumer·볼륨. **바쁨을 readiness에 싣지 않는다.** [1단계] 집행자는 orchestrator, [2단계] kubelet — **판정 기준은 동일** | 장시간 턴 중 재시작 참사 방지(§5.4) | 오탐 재시작이 1건이라도 발생하면 즉시 |
| 3 | Neo4j 단일 인스턴스 SPOF 수용 여부 | **수용.** 1시간 dump + 일 1회 오프호스트 복사. 저하 모드(신규 배정만 중단, 진행 중 작업 계속) 명시. **[1단계]에서는 애초에 전 컴포넌트가 단일 호스트이므로 Neo4j만의 문제가 아니다** | 초기 규모에서 EE 비용 대비 이득 없음. 저하 모드가 견딜 만함 | AI 에이전트 50명 초과 **또는** 분기당 Neo4j 원인 장애 2건 초과 **또는** 가용성 SLA 계약 → Enterprise + Causal Cluster |
| 4 | Neo4j Helm 배포와 Operator 패턴의 정합성 | **이원 체계 명문화.** 플랫폼 계층(Neo4j/NATS/[2단계]Longhorn/k3s)은 **부트스트랩이 배치**([1단계] compose, [2단계] k3s auto-deploy HelmChart). 조직 계층(Team/Agent/PermissionProfile)만 orchestrator/operator가 수렴. **오케스트레이터는 플랫폼 컴포넌트를 읽기만 하고 절대 수정하지 않는다** | 두 계층의 변경 주기·소유자가 다름 | Neo4j 공식 self-managed operator 출시 시 |
| 5 | Temporal 도입 시점 | **미도입.** 역할 대체: JetStream WorkQueue(내구성) + 볼륨 체크포인트(상태) + 승인 게이트(사람 대기) + 재시도 2회 | 별도 클러스터+DB 운영 비용. §5.5의 at-least-once 안전성 논거로 충분 | 아래 4개 중 **2개** 충족 시: (a) 단일 태스크 중앙값 실행시간 > 30분, (b) 24시간 초과 사람 대기 워크플로가 정례화, (c) 다단계 보상 트랜잭션 필요, (d) 주간 수동 개입 > 5건 |
| 6 | RDF/OWL → property graph 매핑 규칙 | **확정.** 클래스→라벨, 상위클래스→**공동 라벨로 머티리얼라이즈**, object property→관계, datatype property→노드 속성, **reified 클래스(`org:Membership`, `org:Role`, `prov:Activity`)→노드로 승격**, IRI는 `iri` 속성 보존. SPARQL 호환 포기 | 규칙표와 근거는 `20-ontology-schema.md` §2. **배포 단계와 무관한 불변 자산** | RDF export/연동 요구 발생 시 |
| 7 | 승인 이벤트 전달 보장 수준 | **JetStream at-least-once + 명시 ack + `Nats-Msg-Id` 멱등.** 사람 대상은 **재전달이 아니라 에스컬레이션 사다리**(즉시 → 15분 → 4시간 → 24시간 blocked+재배정, §7.5) | 사람의 오프라인은 정상 상태이므로 재전달이 답이 아님 | 승인 병목이 주 3건 초과 시 임계값 단축 |
| 8 | 크로스팀 Master 경유의 강제 수준 | **NATS 계정/사용자 권한으로 기술적 강제** `[조사 뒤집음]`. 타 팀 서브젝트 publish를 서버가 거부 | 프롬프트 인젝션으로 깨지는 규약은 거버넌스가 아님. 강제 비용이 낮음. **[1단계]에서 L3 격리가 약한 것을 실질적으로 메우는 층이기도 하다** | 팀 20개 초과로 ACL 관리 부담 발생 시 |
| 9 | `ANTHROPIC_BASE_URL` 전환 시 MCP tool search / Remote Control 비활성화 영향 | **수용.** OSiris는 둘 다 의존하지 않는다 — MCP 서버는 에이전트당 고정 5개 이하로 명시 주입하므로 search 불필요, 원격 제어는 자체 콘솔 API로 수행 | 의존 없음이 확인됨 | 에이전트당 MCP 서버 10개 초과 시(컨텍스트 비대화) → 서버 수 축소 우선 |
| 10 | 호스트 OS / 베이스 | **[1단계]** Docker Engine이 도는 systemd 리눅스(현 호스트 그대로 — 이미 상위 리버스 프록시가 돌고 있으므로 신규 결정 불필요). **[2단계]** Ubuntu 24.04 LTS(cgroup v2 + systemd). **Talos 기각** `[조사 뒤집음 — 조사는 "조사할 가치 있음"이었으나 배제로 확정]` | Talos는 호스트 systemd가 없어 **§10.3의 아웃오브클러스터 watchdog을 실행할 곳이 없다.** watchdog을 클러스터 안에 넣으면 설계 목적이 소멸. 게다가 상위 리버스 프록시가 이미 이 호스트에 있어 OS 교체는 현실적이지 않다 | 하드웨어 어플라이언스 제품화(별도 물리 워치독 전제) 시 |
| 11 | `--cluster-init`(embedded etcd) 시점 | **[1단계] 해당 없음.** **[2단계] 최초 부팅부터 항상 켠다**(단일 노드 포함) | 살아있는 클러스터에서의 SQLite→etcd 전환이 더 위험. etcd 추가 RAM ~300MB는 8GiB 최소사양에 이미 포함 | 4GiB 이하 호스트 요구 시 → `--mode lite`(SQLite, **join 불가**) 별도 추가 |
| 12 | Longhorn 최소 하드웨어 / 단일 노드 절충 | **[1단계] 해당 없음** (named volume, 복제 없음, 야간 tar 백업). **[2단계] `numberOfReplicas = min(3, 정상 노드 수)`**, `dataLocality: best-effort`, 최소 4 vCPU / 8 GiB / 60 GiB SSD. 노드 수 변화 시 자동 조정 | 단일 노드에서 replica 3은 무의미한 3중 쓰기. 노드가 늘면 자동으로 안전해짐 | HDD 환경 또는 8GiB 미만 요구. `[미검증]` Longhorn 라이브 replica 수 변경의 무중단성 |
| 13 | 워커 노드 join의 보안 모델 | **전제 재해석 먼저**: "사람도 1급 노드"는 **조직 온톨로지의 노드**이지 k8s 노드가 아니다(§6.3). 따라서 **개인 기기의 워커 join은 범위 밖이며 금지.** 워커 노드는 운영자가 관리하는 서버뿐. 그 위에서 [2단계]: ① 단명(10분)·1회용 OSiris join 토큰을 osiris-api가 발급, ② **실제 `K3S_TOKEN`은 서버 밖으로 나가지 않는다**(검증 후 1회 전달), ③ 신규 노드는 `unverified` taint로 격리, ④ 콘솔에서 대표가 승인해야 taint 제거, ⑤ 모든 join 시도를 감사 기록. **[1단계]는 단일 호스트라 해당 없음** | 사람에게는 reconcile할 물리 리소스가 없으므로 k8s 노드가 될 이유가 없다. k3s의 장수 클러스터 토큰을 그대로 배포하는 것은 회전 불가 리스크 | 원격 GPU 워커·재택 노드 요구가 실제로 발생하면 mTLS + 별도 노드풀 + 데이터 분류 정책과 함께 재설계. `[미검증]` `k3s token create --ttl` 존재 여부 — 존재해도 위 래퍼 방식이 감사 이점으로 유지 |

### 11.1 이 문서가 추가로 내린 결정 (조사·브리핑에 없던 항목)

| # | 결정 | 근거 | 재검토 트리거 |
|---|---|---|---|
| A1 | **[2단계]** 에이전트를 StatefulSet이 아니라 **Agent CR 소유의 bare Pod + PVC**로 `[조사 뒤집음]` | 우리는 이미 컨트롤러를 갖고 있다. StatefulSet의 유일한 가치(안정 identity, volumeClaimTemplates)를 컨트롤러가 직접 제공하며, 대신 (a) 통제 불가한 중간층 제거, (b) 660초 grace의 체크포인트 인지 재시작, (c) suspend-to-zero, (d) in-place 설정 리로드가 자연스러워진다. k8s 블로그가 경고한 "N개 StatefulSet의 운영상 악몽"을 애초에 만들지 않는다. 게다가 **[1단계]의 `DockerDriver`가 이미 "컨테이너 하나를 직접 소유"하는 모델**이라 드라이버 교체가 1:1로 대응된다. **감수하는 비용**: 노드 장애 시 Pod 재생성을 우리가 구현(≈100줄) — 어차피 체크포인트 인지 재시작 때문에 커스텀 로직이 불가피 | agent-sandbox CRD가 프로덕션 성숙 시 이관(Agent CR이 상위집합이라 경로가 열려 있음) |
| A2 | **[2단계]** `osiris-api`를 서버 노드 DaemonSet + hostPort 8443 | 외부 LB가 없다. ingress 계층을 추가하면 장애 도메인만 늘어난다. SSE 재개가 JetStream seq 기반(D19)이라 인스턴스 간 페일오버가 성립한다 | 노드 20대 초과 또는 외부 LB 도입 시 |
| A3 | **모든 외부 egress를 `osiris-egress` HTTP CONNECT 프록시 경유**로 강제. **I5: `osiris-omniroute` 자신의 아웃바운드도 예외 없이 포함**(§9.0) | ① [2단계] k3s 기본 NetworkPolicy(kube-router)는 **FQDN 기반 egress를 지원하지 않아** "github.com만 허용"을 CIDR로 표현하는 것이 실무적으로 불가능, ② [1단계] 도커 네트워크는 그보다 더 거칠다, ③ 프록시가 도메인 허용목록을 강제하면서 동시에 전 외부통신 감사로그를 남긴다, ④ **`dev-net` 핀홀을 한 지점으로 모을 수 있다**(§2.6). I5: omniroute에 자체 아웃바운드 망을 주는 예외안도 검토했으나, omniroute가 `HTTPS_PROXY`/`HTTP_PROXY`를 실제로 준수함을 실측(소스 코드 + 실행 로그)으로 확인해 예외를 두지 않았다 | Cilium 등 FQDN 지원 CNI 도입 시(그래도 감사 목적으로 유지 권고) |
| A4 | **배정 로직은 LLM이 있는 프로세스에 존재하지 않는다** — `osiris-core` 이미지에 `claude` 바이너리와 게이트웨이 자격증명을 넣지 않는다 | compute-over-inference를 문서상 원칙이 아니라 **배포 구조로 강제**. 나중에 누가 "여기서 LLM 한 번만 부르면 편한데"라고 생각해도 물리적으로 불가능 | 없음(영구 불변식) |
| A5 | **Neo4j 쓰기는 core의 graph-write API 단일 관문.** LLM은 임의 Cypher 실행 권한 없음(MCP로 노출되는 것은 검증된 고수준 오퍼레이션만) | OrgRevision bump·스키마 검증·이벤트 발행·감사를 한 지점에서 보장. LLM이 그래프를 자유롭게 수정하면 온톨로지가 곧 오염된다 | 없음 |
| A6 | **[2단계]** 서버 노드는 1대 또는 3대만 허용. 2대 구성은 API가 거부 | etcd 정족수상 2대는 1대보다 가용성이 나쁘다 | 없음 |
| A7 | **preStop 훅 없음.** PID 1이 SIGTERM을 직접 처리 | 에이전트는 Service 뒤에 없어 디레지스트레이션이 불필요. 훅은 실패 모드만 추가 | 없음 |
| A8 | **`osiris-gw`를 안정적 이음새로 영구 유지** — [2단계]에서도 이 컨테이너는 남는다 | 상위 리버스 프록시 conf·DNS·인증서를 영구 불변으로 만드는 대가로 홉 하나와 무상태 SPOF 하나를 받아들인다 | 없음 |
| A9 | **TLS는 상위 리버스 프록시에서만 종단.** 내부 재암호화 없음 | §2.4. 이미 통일된 인증서 갱신 경로를 깨는 비용 > 동일 호스트 브리지 재암호화의 이득 | `dev-net`에 비신뢰 제3자 컨테이너가 올라가는 순간 |
| A10 | **`osiris-egress`를 `dev-net`에 아웃바운드 전용으로 이중 홈** (코디네이터 권고에서 한 걸음 확장). **I5: 근거 갱신** — 원래 이유("공용 omniroute 도달")는 게이트웨이 지향점이 전용 `osiris-omniroute`(osiris-llm)로 옮겨가며 소멸했지만, 핀홀 자체는 `net.egress.github`/`net.egress.pkg`(개발 에이전트의 실제 외부 접근)가 여전히 `dev-net`의 NAT를 통해서만 인터넷에 나갈 수 있어 유지한다(§9.0) | "상위 리버스 프록시가 죽어도 조직은 계속 돌아야 한다"는 요구를 만족하려면 (구)공용 omniroute 도달 경로가 상위 리버스 프록시를 지나면 안 된다(§2.6). I5 이후: github/npm/pypi 등 실제 인터넷 목적지가 이 핀홀의 유일한 존재 이유 | (구)근거였던 공용 omniroute 의존은 이미 소멸(I5). `net.egress.*` grant가 없어지거나 dev-net 아닌 경로로 인터넷에 나갈 방법이 생기면 재검토 |
| A11 | **에이전트 동적 생성은 Docker Engine API 직접 호출** (compose 파일 재생성/슬롯 풀 기각) | §1.4. 조직 정보가 파일로 새어나가면 그래프가 유일한 진실이 아니게 된다 | 없음([2단계]에서 CR로 자연 이행) |
| A12 | **`SpecHash`와 `ConfigHash`를 분리한다.** 페르소나 프롬프트(`:Role.promptTemplateMd`)·권한·역량 변경은 `ConfigHash`에만 들어가 **무중단 `ReloadConfig`**를 유발하고, 컨테이너 재생성(`Recreate`)은 이미지·리소스·드레인시간 변경에만 국한된다 | 두 해시를 합치면 **역할 설명문 한 줄 수정이 그 직무의 전 에이전트를 드레인·재생성**시킨다(660초 × N). 텍스트 편집의 폭발 반경으로 과도하다. §5.6이 이미 정한 "설정은 다음 태스크부터, 이미지·리소스만 교체" 규칙의 기계적 표현이기도 하다 | 프롬프트 변경이 진행 중 턴에 즉시 반영되어야 하는 요구가 생기면(현재는 다음 태스크부터) 재검토 |
| A13 | **genesis principal(`bootstrap-master`)은 PermissionProfile 파생의 유일한 예외**이며, 특권은 토큰 소지가 아니라 **"조직이 비어 있는가"에 요청 시점마다 조건부**다(§8.5 W1) | 최초 온보딩 시점에는 `:Party`가 없어 §7.1의 유효 권한 계산 대상이 존재하지 않는다. 예외를 두되 **구조적으로 닫히게** 만들어 영구 백도어화를 막는다 — 토큰이 유출되어도 조직이 생기는 순간 무력해진다 | 조직이 존재하는 상태에서 genesis 스코프가 필요해지는 시나리오가 나오면(예: 재해 복구 시 빈 그래프 재구축) 별도 복구 모드로 설계. 현재 그 경로는 없다 |

---

## 12. 콘솔(40-console-ia.md) 요구 계약 충족 매핑

### 12.1 REST

| 콘솔 요구 API | 충족 | 제공 원천 | 비고 |
|---|---|---|---|
| `GET /orgs/tree` | ✅ | Neo4j (`:Party`/`:Team`/`:Membership` + `:AgentRuntime` 캐시) | 사람·AI 혼합. `20-ontology-schema.md` Q3 |
| `GET /orgs/nodes/{id}` | ✅ | 위 + 드라이버 관측값 | 사람 노드는 런타임 status 없이 `:Person` 속성만 |
| `POST /orgs/nodes/{id}/restart` | ⚠️ 부분 | AI: `Recreate`(drain 후 재생성). **사람: 400** | 사람 노드에 "재시작"이 없음 → 콘솔이 버튼 비노출 |
| `GET /wbs/{projectId}/tree` | ✅ | Neo4j `:WorkItem` + rollup-engine | |
| `PATCH /wbs/{taskId}` | ✅ | api → core graph-write | |
| `POST /wbs/{taskId}/reassign` | ✅ | core: `:AssignmentOverride` 생성. 응답에 규칙값+override값 동시 포함 | 규칙 계산 결과를 **덮어쓰지 않음** |
| `POST /wbs/{taskId}/complete-report` | ✅ | core. 첨부는 볼륨 저장 후 `:Evidence` 노드 | |
| `POST /wbs/{taskId}/verify` | ✅ | core. 롤업 재계산 트리거 | |
| `GET /assignments/{taskId}/rationale` | ✅ **1급 요구로 반영** | `:AssignmentDecision` + `[:CONSIDERED]`(탈락자·탈락사유 포함) + `:AssignmentOverride` | **배정 시점 스냅샷.** 역량명·레벨·부하를 엣지에 **비정규화 복사**해 조직이 바뀌어도 재현됨 |
| `GET /approvals/queue` | ✅ | Neo4j `:Approval` (pending) | |
| `GET /approvals/{id}` | ✅ | `:Approval` + JetStream payload | `approvalType` ∈ {commit, message, spend}, 타입별 고정 스키마 |
| `POST /approvals/{id}/decision` | ✅ | core → `approval.decision.<id>` | 승인 후 **실제 실행은 core가 자기 자격증명으로**(§8.2) |
| `GET /ops/agents/health` | ✅ | 드라이버 관측값 집계 | |
| `GET /ops/agents/{id}/transcript?range=` | ⚠️ 부분 | JetStream `TRANSCRIPT`(30일). 초과분은 에이전트 볼륨 | **30일 초과는 볼륨이 살아있을 때만.** Suspended 후 볼륨 회수 시 소실. 콘솔은 "보관 기간 경과" 응답 처리 필요 |
| `GET /ops/cost/summary?groupBy=team&range=` | ✅ | JetStream `COST` 집계(api 5분 캐시) | |
| `GET /ontology/nodes?type=&q=` | ✅ | Neo4j | |
| `GET /ontology/nodes/{id}/edges` | ✅ | Neo4j | |
| `PATCH /ontology/nodes/{id}` | ⚠️ 제한 | core graph-write의 **화이트리스트 필드만** | 임의 속성 편집 불가(스키마 무결성). 편집 가능 필드 목록을 API가 함께 반환 |
| `POST /chat/messages` | ✅ | api → `master.inbox` | |
| `POST /chat/{id}/cancel` | ✅ | api → agent-master supervisor의 abort → 자식에 SIGTERM | |
| `GET /onboarding/status` | ✅ | Neo4j 무결성 검사 쿼리 집합(`20-ontology-schema.md` §9.3) | |
| `GET /notifications` | ✅ | Neo4j `:Notification` | |
| `GET /cluster/control-plane/health` | ✅ **강화** | api 자체 + **watchdog 집계**(상위 리버스 프록시 상태 포함) | 단일 소스가 아니라 in-band + out-of-band 결합(§10.3) |
| `GET /cluster/nodes` | ⚠️ 단계차 | **[1단계] 노드 1개(호스트)만 반환** — 컨테이너 배치·리소스 사용률 포함. **[2단계]** k8s Node API | 콘솔의 노드 카드 UI는 그대로 쓰되 1단계에서는 카드가 1장 |
| `GET /cluster/nodes/{id}/capacity` | ✅ | **좌석 환산은 백엔드 계산.** `:RoleResourceProfile` ÷ allocatable | 프로파일 변경 시 자동 반영(하드코딩 없음, G10) |
| `POST /cluster/nodes/join-token` | ⚠️ 단계차 | **[1단계] 501 Not Implemented** (단일 호스트). [2단계] 10분·1회용 토큰 | 콘솔은 1단계에서 `[+ 노드 추가]` 버튼을 "2단계에서 사용 가능" 툴팁과 함께 비활성 |
| `POST /cluster/nodes/{id}/drain` | ⚠️ 단계차 | **[1단계] 501.** [2단계] eviction + Agent drain | 위와 동일 |
| `DELETE /cluster/nodes/{id}` | ⚠️ 단계차 | **[1단계] 501.** [2단계] drain 완료 후 허용 | 위와 동일 |
| **(추가 제공)** `POST /ops/agents/{id}/abort` | ➕ | 진행 중 턴만 중단(컨테이너 유지) | 재시작보다 가벼운 개입 수단 |
| **(추가 제공)** `POST /orgs/nodes/{id}/quarantine` | ➕ | 즉시 격리(컨테이너 삭제 + 토큰 revoke) | 침해 대응 |
| **(추가 제공)** `GET /ops/egress/audit` | ➕ | egress 프록시 감사(허용/거부 이력) | 외부통신 가시성 |

### 12.2 이벤트

| 콘솔 요구 이벤트 | 충족 | 원천 |
|---|---|---|
| `chat.token` | ⚠️ 조건부 | agent-master supervisor가 `stream-json` 파싱 후 발행. **omniroute SSE 패스스루가 전제** `[미검증]`. 미지원 시 턴 단위 렌더로 저하 |
| `chat.artifact.updated` | ✅ | `task.*.event` + `org.event.*` → api 정규화 |
| `wbs.task.status_changed` | ✅ | `task.<id>.event` |
| `wbs.task.rollup_changed` | ✅ | rollup-engine → `task.<parentId>.event` |
| `org.node.status_changed` | ✅ | 드라이버 관측 + `heartbeat.agent.*` + 사람의 `awaiting-human` |
| `approval.created` / `approval.resolved` | ✅ | `approval.request` / `approval.decision.*` |
| `ops.log.line` | ✅ | `ops.log.<id>` (Core NATS). 선택된 에이전트만 구독 |
| `notification.created` | ✅ | notify-adapter |
| `onboarding.step_changed` | ✅ **확장** | `org.event.*` 수신 시 api가 무결성 검사 재실행 후 발행. **payload에 `phase` 필드(`Drafting`/`Reviewing`/`Committing`/`Committed`/`Provisioning`/`Steady`)를 싣는다** — `30-onboarding-provisioning.md` §2 상태기계와 1:1. 이벤트 **이름은 바꾸지 않는다**(콘솔 구독 계약 유지) |
| `cluster.control_plane.status_changed` | ✅ | api + watchdog 집계. **최우선 등급** |
| `cluster.node.status_changed` | ⚠️ 단계차 | [1단계] 호스트 1개의 리소스 압박만. [2단계] 전 노드 |
| `cluster.node.join_progress` | ⚠️ 단계차 | [1단계] 미발행. [2단계] join 각 단계 |
| **(추가 제공)** `cluster.heartbeat` | ➕ | api가 10초 발행(W4 요구 반영). **단독 신호로 쓰지 말 것** — 판정 규칙은 §10.3 |
| **(추가 제공)** `cluster.external_gateway.status_changed` | ➕ | watchdog의 상위 리버스 프록시 관측. "조직은 도는데 화면만 안 보임"을 구분해 표시하기 위함 |

### 12.3 비-API 계약

| 콘솔 요구 | 충족 | 실현 |
|---|---|---|
| 배정 근거를 **배정 시점에 영속화** | ✅ | `:AssignmentDecision` 불변 노드 + `[:CONSIDERED]` 엣지. 역량명/레벨/부하 비정규화 복사 |
| override를 별도 레이어로 저장 | ✅ | `:AssignmentOverride`가 결정을 가리킴. 결정은 불변 |
| 승인 타입별 표준 payload 스키마 | ✅ | grant-catalog가 `approvalType`별 고정 스키마 정의 |
| 사람의 "응답대기"를 1급 상태로 | ✅ | `:Assignment.status = awaiting-human`. 에이전트 오류와 **동일 우선순위** + 에스컬레이션 사다리 |
| SSE 재연결 시 `Last-Event-ID` 유실 복구 | ✅ | **JetStream `TASKS` 시퀀스를 event id로 사용.** 게이트웨이 메모리 비의존 |
| 좌석 환산은 백엔드 계산 | ✅ | `:RoleResourceProfile` 기반. 프로파일 변경 시 자동 반영 |
| 게이트웨이-컨트롤플레인 동시 장애 인지 | ✅ **강화** | `cluster.heartbeat` 채택 + **스택 밖 watchdog** + 상위 리버스 프록시까지 관측 + 콘솔 판정 규칙표(§10.3) |
| **(신규 요구)** SSE 프록시 2홉 요구사항 | ✅ | §2.7. 자가진단(`event: hello` 500ms) + 15초 keepalive |
| **(신규 요구)** 클라이언트 실제 IP | ✅ | §2.8. 홉마다 1개만 신뢰, `X-Real-IP`만 사용, 인가에는 미사용 |

### 12.4 콘솔 문서와의 불일치 — 콘솔 측 조정이 필요한 항목

> `40-console-ia.md`는 이 문서의 소유가 아니므로 여기서는 **기록과 권고만** 한다. 반영 여부는 콘솔 워커/감독의 결정이다.

| # | 불일치 | 실제 백엔드 동작 | 콘솔이 해야 할 조정 |
|---|---|---|---|
| M1 | **Onboarding 스테퍼가 4단계에서 끝난다** (`①팀 → ②역할/직무 → ③capability → ④승인정책`). 그러나 실제 완료 지점은 그 뒤의 **리허설 → 커밋 → 프로비저닝**이다(`30-onboarding-provisioning.md` §2, `20-ontology-schema.md` §9.2). ④가 채워져도 조직은 **아직 존재하지 않으며 에이전트도 뜨지 않은 상태**다 | `GET /onboarding/status`는 4개 항목의 정의 충족도와 **별개로** `phase`(6개 상태)를 함께 반환한다. `:OrgRevision`이 올라가기 전까지 조직은 그래프에 없다 | **5번째 스텝 "⑤ 검증·적용"을 추가**하고, 그 안에서 `Committing`/`Provisioning`/`Steady`의 진행을 보여줄 것. ④ 완료 시점에 "다 됐다"고 읽히는 시각적 종결(체크 아이콘 전체 점등)을 주지 말 것 |
| M2 | 스테퍼가 선형(한 방향)으로 보이나, 리허설 실패 시 `Reviewing`으로 **되돌아간다** | 커밋 트랜잭션이 통째로 롤백되므로 부분 상태가 남지 않는다 | ⑤에서 실패하면 스테퍼가 ②~④ 중 해당 지점으로 되돌아가는 표현이 필요. 실패 항목은 `AssignmentRationalePopover`(C절)를 그대로 재사용해 표시 |
| M3 | 사이드바 "조직 변경 감지 시에만 뱃지" 조건이 미정의 | `baseOrgRevision < currentOrgRevision` 이거나 미해결 역량 갭(I6 / 배정 폴백 FB5)이 존재할 때 | 별도 API 불필요 — `GET /onboarding/status`의 반환값으로 판정 |

**충족하지 못하는 항목 정리**
1. `restart`의 사람 노드 케이스 — 개념적으로 불가. 버튼 비노출로 해결.
2. 트랜스크립트 30일 초과 — 보관 비용 대비 가치가 낮아 의도적 제한. 필요 시 오브젝트 스토리지 도입(현재 미도입).
3. `PATCH /ontology/nodes/{id}` 자유 편집 — 스키마 무결성 우선. 화이트리스트로 대체.
4. `chat.token` 스트리밍 — omniroute 의존. `[미검증]` 확인 후 저하 경로 확정.
5. **클러스터/노드 관리 3종(join-token/drain/delete)** — [1단계] 단일 호스트라 501. [2단계]에서 전부 충족. 콘솔은 단계 플래그(`GET /cluster/capabilities`)를 받아 UI를 비활성화한다. ➕ 이 capabilities 엔드포인트를 추가 제공한다.

---

## 13. 다음 워커를 위한 인터페이스

- **온보딩 워커**: 시드 순서·멱등 규칙·검증 불변식은 `20-ontology-schema.md` §9. **그래프에 쓰기만 하면 된다** — 컨테이너/Pod 생성은 수렴 루프가 자동 수행하므로 온보딩이 런타임을 직접 만지지 않는다. 온보딩이 호출하는 것은 core의 graph-write API 하나뿐이다.
- **콘솔 워커**: §12가 계약. ⚠️ 5건 + ➕ 5건만 UI 조정이 필요하다. 특히 `GET /cluster/capabilities`로 1/2단계를 구분해 노드 관리 UI를 켜고 끈다.
- **백엔드 구현 워커**: §5.4의 헬스 구현 금지사항(mutex 금지, 채널 만재 시 로그 폐기, CPU limit ≥ 1000m), §8.2의 강제/협조 구분, §1.7의 G1~G10 금지 목록을 코드 리뷰 체크리스트로 쓸 것. `WorkloadDriver` 경계는 CI 린트로 강제할 것(G3).
- **인프라 워커**: §2.3의 상위 리버스 프록시 conf는 **한 번 설치하고 다시 건드리지 않는다.** 단계 전환은 `osiris-gw` conf 한 줄이다.
