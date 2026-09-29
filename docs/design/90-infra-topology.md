# OSiris 인프라 토폴로지 (Infra Topology) — 물리 파드 바인딩과 인프라 팀

> 작성일: 2026-09-11 (초안, 검토·확정 대기 — T1)
> 작성 모델: Opus (시스템·보안 아키텍처 설계)
> 입력: `10-core-architecture.md`(§1.7 금지 목록 G1~G10, §권한·격리), `70-kubernetes-deployment.md`(K1~K13, operator 패턴), `deploy/osiris/templates/rbac-operator.yaml`(기존 operator RBAC 실물), 호스트 실측(단일 노드 8vCPU/48GB, docker 컨테이너 79개).
> 범위: (1) 이 클러스터의 정체성 결정, (2) 인프라 팀의 권한 경계, (3) 호스트 docker→파드 이관 모델, (4) 콘솔 Infra 메뉴 IA, (5) 불변식.
> 비범위: 개별 서비스(sample-app·<svc-a>·<svc-b>…)의 구체 manifest, 노드 증설 조달, osiris-api의 k8s 클라이언트 코드 상세(구현 워커=T2 몫).
>
> **표기 규칙** (기존 문서 계승)
> - 표기 없는 서술 = 이 문서가 내리는 **확정 제안**(단, 이 문서 전체가 T1 검토·확정 전 초안).
> - `[결정 필요]` = 사용자/T1이 확정해야 하는 분기. §9에 모음.
> - `[실측]` = 이 초안 작성 중 클러스터/호스트에서 직접 확인한 값.
> - `IN1~INn` = 이 문서가 세우는 인프라 불변식(§8).
>
> **이 문서가 T1에게 묻는 것**: §2의 격리 긴장 진단이 맞는가, §4의 권한 경계(에이전트는 제안·operator가 적용·승인 게이트)가 충분한가, §9의 세 결정.

---

## 0. 결정 요약 (IN1~IN12)

| # | 결정 | 한 줄 근거 |
|---|---|---|
| **IN1** | 인프라 관리는 **operator 패턴**을 따른다 — 에이전트는 kubectl/클러스터 권한을 **직접 갖지 않는다** | G8("에이전트=호스트 root 금지")의 인프라판. 권한은 좁고 감사되는 operator 하나에만 |
| **IN2** | AI-org 런타임(`osiris-system`/`osiris-agents`)과 **인프라 워크로드는 네임스페이스로 분리** | 격리 서사를 흐리지 않는다. 인프라가 늘어도 에이전트 도달면은 안 넓어짐 |
| **IN3** | 에이전트("인프라 팀")는 **제안(manifest·이관계획)만** 하고, **승인 게이트**를 거쳐, `infra-operator`가 적용한다 | orchestrator 수렴 루프 + 승인 게이트의 기존 패턴 재사용 |
| **IN4** | 콘솔·osiris-api의 인프라 접근 1단계는 **읽기 전용** | 토폴로지 시각화가 위험 없이 즉시 유용. 쓰기는 승인 게이트 뒤 |
| **IN5** | 호스트→파드 이관은 **기능이 아니라 서비스별 운영 태스크** — 하나씩, 작업 지도 노드로 추적 | 79개 일괄 이관은 자원·안전 모두 무리 |
| **IN6** | 단일 노드(8vCPU/48GB)에 79개를 다 올리지 않는다 — **이관 대상은 선별**하고 노드 예산을 명시 | [실측] 현재 노드로는 물리적으로 불가 |
| **IN7** | `infra-operator`는 **인프라 네임스페이스에만** 스코프된다 — `osiris-system`/`osiris-agents`·노드·클러스터롤은 건드리지 않는다 | 최소권한. AI-org operator(osiris-orchestrator)와 권한이 겹치지 않음 |
| **IN8** | **파괴적 연산**(delete·scale-to-0·이미지 교체)은 예외 없이 PersonOnly 승인 게이트 | H-시리즈·§8.2 강제/협조 구분 계승 |
| **IN9** | 인프라 파드도 **docker.sock을 마운트하지 않는다** — 컨테이너 안에서 다른 컨테이너를 제어하지 않는다(k8s API 경유만) | G8 불변. "파드가 파드를 만든다"는 operator만 |
| **IN10** | 토폴로지의 **진실은 k8s API**(live)다 — 그래프(Neo4j)는 이관 태스크·소유권·의도를 담고, live 상태를 복제하지 않는다 | G7("Neo4j 단일 관문")·D1과 정합. 상태는 k8s가, 의도는 그래프가 |
| **IN11** | 인프라 팀은 **조직 온톨로지의 팀/직무**로 표현될 수 있으나, 그 권한은 온톨로지가 아니라 **operator RBAC**이 집행한다 | 권한을 프론트/그래프에 하드코딩 금지(G10) — 집행은 클러스터 |
| **IN12** | 이 문서는 `70`을 **대체하지 않고 확장**한다 — 인프라 워크로드는 K-결정(NetworkPolicy·A3 egress)을 상속한다. **단 PodSecurity는 현재 `osiris-agents`에도 미집행이므로**(T1 실측: enforce 라벨 없음), "상속"이 아니라 **선행 과제**로 명시한다(§8 blocker 5) | 없는 보증을 상속한다고 적지 않는다 |
| **IN13** | 신규 `infra-*` NS는 **ResourceQuota + `osiris-system`보다 낮은 PriorityClass**를 갖는다 | [T1-W3] 네트워크 격리 ≠ 자원 격리. 공유 노드에서 infra가 osiris-system을 축출하지 못하게. `resourcequota-agents.yaml`·`priorityclass.yaml` 패턴 재사용 |
| **IN14** | 신규 `infra-*` NS는 **생성과 동시에 default-deny NetworkPolicy**를 받는다 | [T1-W-누락] k8s NS는 정책이 없으면 full-allow다. "NetworkPolicy 상속"은 자동이 아니다(`10-core §448`) |
| **IN15** | infra 실행 권위는 **`osiris-orchestrator`를 확장**한다 — 기존 operator SA에 `infra-*` Role을 더 바인딩한다(새 독립 권위 금지). AD6의 단일 실행 권위·재평가 규율을 그대로 상속 | [확정 2026-09-11] [T1-W2] 두 번째 실행 권위를 만들면 TOCTOU가 두 갈래가 됨. 사용자 확정: (a) orchestrator 확장 |
| **IN16** | infra **워크로드**(sample-app 등)는 **프레임워크 서비스**이지 조직 노드가 아니다 — G4 정합, omniroute·어댑터와 동층. 인프라 **팀**(사람/역할, 그래프)과 구분하며 유일한 링크는 "팀↔워크로드 소유" | [T1-W-누락] IN11의 팀(온톨로지)과 워크로드(프레임워크)를 느슨히 뒤섞지 않는다(`10-core §215-218`) |

---

## 1. 이 문서의 자리

`70-kubernetes-deployment.md`는 **AI 조직 런타임을 k8s에 올리는** 문서다 — osiris-system(플랫폼)과 osiris-agents(에이전트)라는 두 네임스페이스, 그리고 orchestrator가 `osiris-agent-operator` RBAC으로 에이전트 파드를 만드는 operator 패턴을 확립했다(K9·K10, `rbac-operator.yaml`).

이 문서(`90`)는 **그 위에 한 층을 더 얹는다**: k8s 클러스터를 "AI 조직만 도는 곳"에서 "이 로컬의 인프라 전반이 사는 곳"으로 넓히되, **격리 자세를 잃지 않고**, 그 확장 자체를 OSiris가 관리 대상으로 삼는다.

핵심 통찰: **operator 패턴은 이미 답을 갖고 있다.** orchestrator가 에이전트 파드에게 하는 일(제안→수렴 루프→적용, 권한은 operator에만)을, 인프라 파드에 대해 `infra-operator`가 그대로 반복한다. 새 보안 모델을 발명하는 게 아니라 **검증된 패턴을 한 번 더 인스턴스화**하는 것이다.

**왜 지금인가**: 사용자가 k8s를 깐 이유가 이것이다 — "인프라 토폴로지를 실제 물리 파드와 연결". 단일 호스트의 docker 컨테이너 79개는 서로 이름으로 호출하고(G2 위반 형태), docker.sock 없이는 관측·복구가 수동이며, 토폴로지가 사람 머릿속에만 있다. k8s로 옮기면 토폴로지가 **선언**이 되고 operator가 **수렴**시킨다.

---

## 2. 진단 — 격리 긴장 (이 문서의 핵심)

OSiris의 보안 서사 전체가 **"에이전트는 상자 안에 갇혀 있다"**이다:

- **G8**: 에이전트 컨테이너에 docker.sock 금지 — "에이전트가 호스트 root와 동등해짐". docker.sock은 orchestrator에만.
- **G6**: `network_mode: host` 금지 = 격리 소멸.
- **K-격리**: osiris-agents는 NetworkPolicy 전면 차단, PodSecurity admission 강제.

"인프라 팀이 실제 파드를 관리한다"는 요구는 **정의상 클러스터 조작 권한**(k8s API, 이미지, 경우에 따라 노드)을 부른다. 이건 위 규칙들이 에이전트에게 **구조적으로 금지한** 바로 그 권한이다.

**틀린 해법(하지 말 것)**: 인프라 팀 에이전트에게 kubectl 자격증명이나 클러스터롤을 직접 준다. 이 순간 격리 서사가 붕괴한다 — 침해된 에이전트 하나가 클러스터 전체를 쥔다.

**옳은 해법(IN1·IN3)**: 권한을 에이전트가 아니라 **좁고 감사되는 operator**에 둔다. 에이전트는 "이 서비스를 이렇게 이관하자"는 **제안**(선언적 manifest 또는 이관 계획)을 만들고, 그 제안이 **승인 게이트**를 통과하면 `infra-operator`가 적용한다. 에이전트는 클러스터 API를 **한 번도 직접 부르지 않는다**.

이건 orchestrator가 에이전트 파드에 대해 이미 하는 일과 **동형(isomorphic)**이다:

```
[AI-org]   에이전트 CR 제안 → 수렴 루프 → osiris-orchestrator가 osiris-agents에 파드 생성
[인프라]   이관/변경 제안   → 승인 게이트 → infra-operator가 인프라 NS에 워크로드 적용
```

## 3. 정체성 결정 — 한 클러스터, 분리된 네임스페이스

### 3.1 [실측] 규모 현실

```
클러스터:   k3d-osiris-dev-server-0  단일 노드  8 vCPU / 48 GB
이관 후보:   호스트 docker 컨테이너 79개
  sample-app 13 · <svc-a> 8 · <svc-c> 4 · <svc-d> 3 · <svc-b> 3 · <svc-e> 2 · <svc-f> 2 · <svc-g> · …
```

**[T1-W4 정정]** "자원상 불가"는 과장이다 — 현재 노드 부하는 1% CPU / 6% mem에 불과하다(T1 실측). 진짜 제약은 개수가 아니라 **무거운 상태형**이다: oracle-xe/free, SAP HANA, valhalla routing, postgres 8개+, mariadb 등이 8코어를 포화시킨다. 경량 다수(정적 프록시·무상태 앱)는 수용 가능하다. 결론(IN6 선별)은 유지되나 근거는 "개수"가 아니라 "상태형 중량"이다:
1. **이관은 선별적**이다 — 무엇을 올릴지가 결정 항목(§9-③). 무거운 상태형과 외부 노출 없는 개발용은 **호스트 잔류를 기본값**으로 둔다.
2. 무거운 상태형까지 진지하게 옮기려면 **노드 증설**(워커 노드 추가)이 전제다. k3d는 `k3d node create`로 워커를 붙일 수 있다 — 별도 조달 결정.

### 3.2 별도 클러스터가 아니라 별도 네임스페이스 (IN2)

세 안을 저울질했다:

| 안 | 격리 | 운영 복잡도 | 판정 |
|---|---|---|---|
| A. 기존 osiris NS에 섞어 넣기 | 나쁨 — 에이전트 도달면이 넓어짐 | 낮음 | 기각 |
| B. **같은 클러스터, 별도 인프라 NS** (`infra-*`) | 좋음 — NetworkPolicy로 osiris-agents ↔ infra 전면 차단 | 중 | **채택** |
| C. 완전 별도 클러스터 | 최상 | 높음 — 두 컨트롤플레인, 두 관측 | 과함(현 규모) |

**B 채택 근거**: k8s NetworkPolicy는 네임스페이스 경계에서 이미 전면 차단을 집행한다(K11 실측). 인프라를 `infra-<service>` 네임스페이스 군에 두면, 에이전트(osiris-agents)는 그것들에 L3로 닿지 못한다 — 인프라가 79개로 늘어도 **에이전트가 만질 수 있는 것은 늘지 않는다**. AI-org 런타임은 순수하게 유지된다.

경계 규칙(IN12·IN13·IN14): 인프라 NS도 `70`의 K-결정을 **명시적으로** 적용받는다 — 자동 상속이 아니다(T1이 짚은 함정):
- **IN14 default-deny NetworkPolicy** — NS 생성과 **동시에** 건다. k8s NS는 정책이 없으면 full-allow이고, CNI가 미지원이면 조용히 무시된다(`10-core §448`). "상속"은 자동이 아니므로 NS 프로비저닝의 일부로 박는다.
- **IN13 자원 격리** — ResourceQuota + `osiris-system`보다 낮은 PriorityClass. **네트워크 격리만으로는 부족하다**(T1-W3): 공유 노드에서 request 없는/과한 infra 워크로드가 osiris-system을 스케줄 압박·축출하면 "AI-org 순수 유지"가 자원층에서 깨진다. `resourcequota-agents.yaml`·`priorityclass.yaml` 패턴을 재사용한다.
- **A3 단일 egress** — 외부로 나가는 인프라 트래픽도 osiris-egress 허용목록 또는 인프라 전용 egress를 경유.
- **PodSecurity** — IN12 참고: 현재 `osiris-agents`에도 미집행이므로 상속할 원본이 라이브가 아니다. 선행 과제(§8 blocker 5).

## 4. 권한 경계 — infra-operator

### 4.1 세 주체

```
1. 콘솔/osiris-api        읽기 전용 (IN4)  — 토폴로지·헬스·자원 조회. 1단계 전부.
2. infra-operator (SA)    쓰기 (스코프됨)  — 인프라 NS에만 apply/scale/restart/delete.
                                            승인된 제안만 실행.
3. 인프라 팀 (에이전트)    권한 0           — k8s API 직접 호출 안 함. 제안만 생성.
```

### 4.2 `infra-operator` RBAC (제안, IN7)

기존 `osiris-agent-operator`(osiris-agents 네임스페이스 한정)와 **같은 모양, 다른 스코프**:

```yaml
# 인프라 NS "군"에 대한 Role (네임스페이스마다, 또는 라벨 셀렉터 기반 운영)
# ★ ClusterRole이 아니라 네임스페이스 Role의 합 — osiris-system/osiris-agents,
#   노드, RBAC 자체에는 절대 닿지 않는다(IN7).
rules:
  - apiGroups: [""]
    resources: ["pods","services","persistentvolumeclaims","configmaps","secrets"]
    verbs: ["get","list","watch","create","update","patch","delete"]
  - apiGroups: ["apps"]
    resources: ["deployments","statefulsets","daemonsets"]
    verbs: ["get","list","watch","create","update","patch","delete"]
  - apiGroups: ["networking.k8s.io"]
    resources: ["ingresses","networkpolicies"]
    verbs: ["get","list","watch","create","update","patch","delete"]
```

명시적 **비권한**(IN7): nodes, clusterroles/clusterrolebindings, namespaces(생성), `osiris-*` 네임스페이스의 어떤 것도. 노드 조회(토폴로지 표시용 read)는 osiris-api의 별도 read-only ClusterRole로 분리한다 — 쓰기 operator와 읽기 뷰의 권한을 섞지 않는다.

**어디에 귀속되는가 (IN15, [T1-W2] — [결정 필요] §9-②)**: `infra-operator`는 새로운 **독립 실행 권위**가 되어서는 안 된다. `80 §5.2.5(AD6)`가 "core가 자기 자격증명으로 집행하고 **집행 시점에 재평가**"를 유일한 실행 권위로 못박았고, infra-operator가 그 밖에 뜨면 TOCTOU(승인~적용 사이 상태 변화) 처리가 두 갈래가 되어 안전 공백이 생긴다. 두 귀속안:
- **(a) osiris-orchestrator 확장** — 기존 operator SA에 `infra-*` Role을 더 바인딩. 수렴 루프·리스 등 이미 있는 실행 기계를 재사용. 단 orchestrator의 책임 범위가 넓어짐.
- **(b) core 실행평면 편입** — infra apply를 core의 액션 실행 경로에 태워 AD6 재평가를 그대로 상속. 개념적으로 가장 정합하나 core에 k8s apply 능력을 새로 얹어야 함.

**[확정 2026-09-11] (a) osiris-orchestrator 확장.** 기존 `osiris-orchestrator` SA에 `infra-*` 네임스페이스 Role을 더 바인딩한다 — 수렴 루프·리스 등 이미 있는 실행 기계를 재사용하고, 실행 권위가 하나로 유지된다(IN15). infra apply도 orchestrator가 AD6 재평가 규율 아래 집행한다. core에 k8s apply를 새로 얹지 않는다.

### 4.3 osiris-api 읽기 RBAC (1단계 전부) — [T1-W1 반영]

콘솔은 `/_api/*`만 부른다(§84 계약). 따라서 osiris-api에 **읽기 전용** k8s 접근을 준다. 그러나 T1 검토가 짚은 두 문제를 먼저 닫아야 한다:

**(1) `rbac-api.yaml` 불변식과의 충돌 — 명시적 개정.** `rbac-api.yaml`은 "osiris-api의 k8s API 권한은 **0**이다 — RoleBinding을 안 만드는 것 자체가 구현"이라 명문화하고 `verify-isolation-k8s.sh [E2→RBAC]`가 실측한다. read-only는 `create pods`를 안 주므로 E2(생성 불가)는 통과하지만, "권한 0" 불변식 자체는 깨진다. **이 문서는 그 불변식을 조용히 추가하지 않고 명시적으로 개정한다**: "osiris-api는 **인프라 토폴로지 표시에 필요한 최소 범위의 읽기 전용** k8s 접근을 가지며, 그 외 모든 k8s 쓰기 권한은 여전히 0이다(E2 유지)." `rbac-api.yaml`과 `verify-isolation-k8s.sh`의 주석·검사를 이 개정에 맞춰 갱신하는 것이 T2 선결 과제다.

**(2) 전 클러스터 read = 정보유출 비대칭 — 범위 축소.** cluster-wide read는 `osiris-system`·`osiris-agents`의 워크로드 스펙(이미지·env·참조 secret **이름**)까지 읽는다. IN7은 *write*만 osiris-*를 배제했지 *read*는 전 클러스터였다 — osiris-api 침해 시 유출 표면이 된다. **secrets를 ClusterRole에서 제외**하는 것은 물론이고, 워크로드 read를 **`infra-*` 네임스페이스로 한정**하고, 클러스터 스코프는 토폴로지 골격에 꼭 필요한 것(nodes·namespaces 메타, metrics)만 남긴다:

```yaml
# (a) 클러스터 스코프 — 토폴로지 골격 최소한 (워크로드 스펙 아님)
kind: ClusterRole
rules:
  - apiGroups: [""]
    resources: ["nodes","namespaces"]         # 노드/NS 목록·상태. secrets·pods 제외
    verbs: ["get","list","watch"]
  - apiGroups: ["metrics.k8s.io"]
    resources: ["nodes","pods"]                # 자원 사용 표시(metrics-server, 실측 작동)
    verbs: ["get","list"]
# (b) 워크로드 read — infra-* 네임스페이스로 한정(Role, NS마다 바인딩)
kind: Role   # in each infra-* namespace
rules:
  - apiGroups: ["","apps","networking.k8s.io"]
    resources: ["pods","services","deployments","statefulsets","persistentvolumeclaims","ingresses","events"]
    verbs: ["get","list","watch"]             # secrets 없음
```

osiris-api는 이 토큰으로 k8s API를 읽어 `/_api/infra/*`로 콘솔에 넘긴다. **쓰기 엔드포인트는 1단계에 만들지 않는다**(IN4). 이 두 수정을 닫으면 A단계(읽기 뷰)의 실제 위험은 **"쓰기 위험 0, 정보유출 표면은 infra-* 워크로드 스펙으로 한정"**이다 — secrets 값은 어느 경로로도 노출되지 않는다.

### 4.4 파괴적 연산 게이트 (IN8)

delete·scale-to-0·이미지 교체·PVC 삭제는 예외 없이 승인을 거친다. 승인 인박스(콘솔 Approvals)에 인프라 액션 타입(`infra.apply`·`infra.delete` 등)을 추가한다 — `80-action-layer.md`의 `:ActionType` 프레임을 그대로 쓴다(그래프 네이티브 액션 + 세 안전장치).

**[T1-W-누락] 게이트로 다루지 주체로 다루지 않는다.** `80 §5.1.3`은 "위험한 실행은 **게이트**로 다루지 주체 종류로 다루지 않는다"고 명시한다. 따라서 인프라 액션 코드는 HumanOnly가 아니라 `gateClass: approval.admin`으로 다룬다 — 이는 "사람만 부를 수 있다"가 아니라 "누가 부르든 admin 승인 게이트를 통과해야 apply된다"는 뜻이다. 이렇게 하면 인프라 팀 에이전트가 제안(액션 생성)까지는 하되 apply는 승인 없이는 불가능하다는 IN3가 게이트 층에서 자연히 집행된다. 즉 **인프라 변경도 조직의 다른 액션과 같은 승인 표면을 공유**한다.

## 5. 인프라 팀 (조직 모델)

### 5.1 팀은 온톨로지에, 권한은 클러스터에 (IN11)

"인프라 팀을 구성한다"는 것은 조직 온톨로지에 팀/직무 노드를 만드는 것으로 표현할 수 있다(20-ontology-schema). 그러나 **그 팀이 파드를 만들 수 있는 것은 온톨로지 때문이 아니라 infra-operator RBAC 때문이다**. 온톨로지는 "누가 이 이관을 제안·승인할 자격이 있는가"(조직적 권한)를 담고, 클러스터 RBAC은 "무엇이 실제로 apply되는가"(기술적 집행)를 담는다. 둘을 혼동하면 G10(권한을 프론트/그래프에 하드코딩)을 위반한다.

**[T1-W-누락, IN16] 팀 ≠ 워크로드.** 여기서 두 가지를 명확히 갈라야 한다:
- **인프라 팀** = 사람/역할, 조직 온톨로지의 노드(누가 제안·승인하는가).
- **인프라 워크로드**(sample-app·<svc-b> 등) = **프레임워크 서비스**이지 조직 노드가 아니다 — G4("compose/그래프에 조직 정보만") 정합, omniroute·통합 어댑터와 **같은 층위**(`10-core §215-218`: "정적으로 선언된 프레임워크 서비스"). 이관되는 컨테이너는 조직 구성원이 아니다.

둘 사이의 **유일한 링크**는 "이 팀이 이 워크로드를 **소유**한다"는 소유 관계뿐이다. 워크로드를 조직 노드로 취급하면(예: sample-app_db를 팀원처럼) 그래프가 오염되고 G4가 무너진다.

### 5.2 이관 태스크 = 작업 지도 노드 (IN5)

여기서 앞서 미뤄둔 **작업 지도**와 수렴한다. 각 서비스 이관은 작업 노드다:

```
프로젝트: "호스트 인프라 이관"
  └ 노드: "sample-app 스택 이관"  [상태: 대기]
      ├ 노드: "sample-app_db → StatefulSet + PVC 데이터 이관"
      ├ 노드: "sample-app_bff → Deployment"
      └ 노드: "sample-app_nginx → Ingress"
```

인프라 팀 에이전트가 이 노드들에 manifest 초안·이관 절차를 **제안으로** 채우고, 사람이 승인하면 infra-operator가 실행하며, 노드 상태가 수렴한다. 이것이 §4의 "제안→승인→적용"과 §5의 "팀"과 작업 지도를 하나로 묶는다.

## 6. 마이그레이션 모델 — 한 컨테이너를 옮기는 해부

호스트 docker 컨테이너 하나 → 파드는 **6요소**를 갖는다. 전부 자동화되지 않으며, 사람 판단이 필요한 지점을 명시한다:

| 요소 | 호스트(docker) | 파드(k8s) | 사람 판단 지점 |
|---|---|---|---|
| 워크로드 | container + `restart:` | Deployment(무상태) / StatefulSet(상태) | 상태성 여부 |
| 저장소 | named volume / bind mount | PVC + **데이터 이관**(volume 복사) | 데이터 손실 위험 — 컷오버 창 |
| 네트워킹 | 컨테이너 이름 DNS / 포트 매핑 | Service(내부) + Ingress(외부) | 외부 노출 도메인 |
| 시크릿 | env / 파일 | Secret(파일 주입, G5) | 평문 env 금지 |
| 헬스 | docker healthcheck | liveness/readiness probe | 프로브 경로 |
| 컷오버 | — | 트래픽 전환 + 구 컨테이너 폐기 | 롤백 계획 |

**원칙(IN5)**: 하나씩. 각 이관은 (1) 파드를 **병렬로** 띄우고 헬스 확인 → (2) 데이터 동기화 → (3) 트래픽 컷오버 → (4) 구 컨테이너 폐기. 구 컨테이너는 검증 전까지 살려 둔다(롤백 경로).

**우선순위 제안**(무상태·자기완결부터, 상태·의존 많은 것은 나중):
1. 무상태 정적/프록시(<svc-e>, <svc-f>-static) — 위험 최저, 패턴 확립
2. 자기완결 앱(<svc-b> 스택 — 자체 postgres/redis 포함)
3. 상태 DB(sample-app_db 등) — 데이터 이관 위험 최고, 마지막

## 7. 콘솔 IA — Infra 메뉴 (canvas + 작업패널 + MCP)

사이드바에 **Infra** 항목 신설(screens.ts의 "운영" 그룹, Ops 옆). 화면 레이아웃은 **작업 지도와 같은 2단**(감독 지시, 2026-09-11):

```
┌ 좌: 토폴로지 그래프 canvas ──────────┬ 우: 작업 패널 ──────────┐
│  노드→네임스페이스→워크로드→파드     │  대화형(에이전트 + infra  │
│  (온톨로지 그래프 컴포넌트 재사용)   │  MCP 도구)로 infra를 구성  │
│  live k8s 상태 + 자원/헬스           │  제안→승인→적용            │
└──────────────────────────────────┴───────────────────────────┘
```

**좌: 토폴로지 canvas** — 작업 지도/온톨로지가 쓰는 그래프 컴포넌트를 재사용한다(감독: "이 토폴로지는 온톨로지 구성에서도 확인 가능"). 즉 인프라 토폴로지는 **온톨로지 그래프의 한 뷰**이며, 별도 시각화를 발명하지 않는다. live k8s 상태(파드 phase·재시작·자원)를 노드에 얹는다.

**우: 작업 패널(대화형)** — 감독 핵심 요구: "**대화형으로 infra를 구성**". 이 패널에서 에이전트가 사용자와 대화하며 인프라를 관리한다. 그 능력은 **infra MCP 도구**(§7.1)로 노출된다.

### 7.1 infra MCP 도구 — 인프라 관리의 도구 표면

기존 MCP 서버들(`mcp__<tool>__*` 형태)과 같은 방식으로, **인프라 관리 MCP 서버**(`mcp__infra__*` 가칭)를 만든다. 작업 패널의 에이전트가 이 도구로 인프라를 읽고·바꾼다.

**★ 불변식과의 정합(중대)**: MCP 도구가 있다고 해서 IN1·IN3·IN8이 약화되지 않는다 — 도구를 **두 종류로 가른다**:
- **읽기 도구**(`infra_topology`·`infra_pods`·`infra_nodes`·`infra_describe` 등) — osiris-api의 read 표면(§4.3)을 그대로 감싼다. 자유 호출.
- **쓰기 도구**(`infra_propose_migration`·`infra_apply`·`infra_scale`·`infra_delete` 등) — **직접 apply하지 않는다.** `80-action-layer.md`의 `:ActionType` **제안(action)을 생성**할 뿐이고, 그 제안이 `gateClass: approval.admin` 승인 게이트를 통과해야 orchestrator(IN15)가 실제로 apply한다. 즉 "대화형으로 구성"은 **대화형으로 제안을 쌓고, 사람이 승인하면 적용**되는 것이다 — 에이전트가 MCP를 통해 클러스터를 직접 바꾸는 경로는 존재하지 않는다(IN3).

이렇게 하면 감독의 "MCP로 infra 관리"와 문서의 격리 불변식이 **정확히 한 지점**에서 만난다: MCP 쓰기 도구 = 제안 생성기, 승인 게이트 = 유일한 apply 관문.

### 7.2 단계

- **A단계 (읽기 canvas)** — 여기부터. 좌 토폴로지 canvas(live 읽기) + 우 작업 패널 **셸**(레이아웃·자리표시자). MCP 도구와 대화형 에이전트 연결은 다음 단계 — A단계는 위험 0의 읽기 뷰 + 패널 골격.
- **B단계 (infra MCP 읽기 도구)** — `mcp__infra__*` 읽기 도구로 패널 에이전트가 토폴로지를 질의. **인증(확정)**: MCP는 k8s를 직접 읽지 않고 **osiris-api `/_api/infra/*`를 기계주체(machine principal) Bearer로 호출**한다(§7.1 "osiris-api read 표면을 감싼다" 준수 — 단일 read 표면·프로젝션 유지). 이를 위해 5개 infra 라우트를 `PersonOnly` → `RequirePerm(authz.PermOrgReadOps)`로 바꾼다. `PermOrgReadOps`는 이미 "operational read: agent health, **cluster**, event stream... 기계주체 공유"로 정의되어(`/api/events`와 동일 게이트) 인프라 토폴로지 읽기에 정확히 맞고, 사람 세션(콘솔)과 기계주체(MCP)가 **둘 다** 도달한다. MCP 서버는 `POST /api/machine-principals`로 발급된 `osm_…` 토큰(org.read.ops)을 env로 받아 쓴다. **A단계에서 이 라우트를 PersonOnly로 둔 것은 "기계 확장은 별도 결정"이라는 유보였고, B단계가 그 결정을 내린다.**
- **C단계 (infra MCP 쓰기 도구 = 제안 생성)** — 쓰기 도구가 `:ActionType` 제안을 만들고 승인 인박스로. 승인 시 orchestrator apply.
- **D단계 (이관 워크플로)** — 작업 지도와 연동, 서비스별 이관 노드가 제안-승인-적용을 태운다.

osiris-api 표면: `GET /_api/infra/topology`·`/_api/infra/pods`·`/_api/infra/nodes`(읽기, A단계) → 쓰기 액션 엔드포인트와 MCP 쓰기 도구는 전부 승인 게이트 뒤(B·C단계).

## 8. 불변식과 T2 착수 전 blocker

§0의 표(IN1~IN16)가 불변식이다. 구현(T2)·리뷰(T1)의 체크리스트로 쓴다. 특히 **깨지면 격리가 무너지는 것**: IN1(권한은 operator에만)·IN3(에이전트는 제안만)·IN7(operator 스코프 한정)·IN9(docker.sock 없음). 이 넷은 G8의 인프라판이며, 코드 리뷰에서 fault-inject로 확인할 것.

**T2 착수 전 반드시 닫을 blocker (T1 검토):**
1. **§4.3 재작성** — `rbac-api.yaml`의 "osiris-api 권한 0" 불변식을 명시적으로 개정하고, read 범위를 §4.3대로 축소(전 클러스터 워크로드 read = 정보유출 비대칭). `verify-isolation-k8s.sh [E2]`의 주석·검사도 갱신. **A단계(읽기 뷰) 착수의 직접 선결.** — [반영됨, §9-① 확정 시 종료]
2. ~~**IN15 확정**~~ — ✅ 종료: osiris-orchestrator 확장으로 확정.
3. ~~**IN13**~~ — ✅ **닫음 (2026-09-11)**: `priorityclass.yaml`에 `osiris-infra`(value -100, 최하위) 추가, `infra-namespaces.yaml`이 infra-* NS마다 ResourceQuota를 건다(values `resourcequota.infra`). 인프라 워크로드가 `priorityClassName: osiris-infra`를 박는 것은 orchestrator 책임(per-NS default priority 불가).
4. ~~**IN14**~~ — ✅ **닫음 (2026-09-11)**: `infra-namespaces.yaml`이 infra-* NS **생성과 동시에** default-deny NetworkPolicy(Ingress 전면거부·Egress는 DNS만)를 건다. 자동 상속 아님 — 프로비저닝에 박음.
5. ~~**PodSecurity**~~ — ✅ **닫음(baseline enforce) (2026-09-11)**: `osiris-agents`와 infra-* NS에 `enforce=baseline` + `warn/audit=restricted` 라벨. baseline을 고른 이유는 restricted enforce가 (agents) orchestrator 파드 스펙 미검증·(infra) 서드파티 이미지를 거부할 위험 때문 — enforce=restricted 승격은 orchestrator 시큐리티 컨텍스트 실측 후 후속 과제.

**A단계 "위험 0" 정밀 판정(T1)**: 정확히는 "쓰기 위험 0, 정보유출 표면은 신설". blocker 1을 닫아 read를 infra-*로 좁히면 A단계는 안전하게 착수 가능.

## 9. 결정 — **확정됨 (2026-09-11)**

T1 검토 반영 후 사용자가 확정. 이 절의 결정은 이제 IN 불변식과 동급으로 T2 구현의 근거다.

- **① 클러스터 정체성** — ✅ **확정: B안**(같은 클러스터·별도 NS), IN13(자원 격리) 전제. 무거운 상태형은 호스트 잔류 기본값.
- **② 권한 경계 + operator 귀속** — ✅ **확정: 네임스페이스 Role의 합**(IN7), operator 귀속은 **(a) osiris-orchestrator 확장**(IN15).
- **③ 이관 대상 선별** — ✅ **확정: 무상태 우선순위**(§6). 무거운 상태형 DB·외부 노출 없는 개발용은 명시적 잔류 후보.
- **④ 인프라 팀 표현** — ✅ **확정: 온톨로지 팀/직무**(IN11), 워크로드와 구분(IN16). 권한 집행은 RBAC.
- **⑤ metrics** — ✅ **확정: 기존 metrics-server 채택**(실측 작동).

**상태**: T1 (b)판정 → 수정 반영 → 사용자 확정 완료. T2는 §8 blocker(§4.3 read 범위·IN13·IN14·PodSecurity)를 A단계 구현과 함께 닫으며 착수한다.

## 10. 전체 토폴로지 (제안, ASCII)

```
 k3d-osiris-dev  (단일 노드 → 증설 시 워커 추가)
 ├─ ingress-nginx        (진입점, K3)
 ├─ osiris-system        [AI-org 플랫폼]  gw·api·core·orchestrator·neo4j·nats·omniroute·egress
 ├─ osiris-agents        [AI-org 워크로드]  ← NetworkPolicy 전면 차단, docker.sock 없음
 │      ▲ osiris-orchestrator (osiris-agent-operator RBAC)
 │
 ├─ infra-sample-app     [인프라 NS 군]  db(STS+PVC)·bff·nginx·engine·worker…
 ├─ infra-<svc-b>        redis·postgres·app
 ├─ infra-<svc-e> · infra-<svc-f> · …
 │      ▲ infra-operator (infra-* 네임스페이스 한정 RBAC, IN7)
 │        │  ← 승인된 제안만 적용 (IN3·IN8)
 │
 └─ osiris-api (read-only ClusterRole) ──/_api/infra/*──▶ 콘솔 Infra 메뉴 (A단계 뷰)

 격리: osiris-agents ──✕──▶ infra-*   (NetworkPolicy, 에이전트는 인프라에 못 닿음, IN2)
 권한: 에이전트 ──제안──▶ 승인 게이트 ──▶ infra-operator ──apply──▶ infra-*  (IN1·IN3)
```

---

> **다음 단계**: 이 초안을 T1이 검토·확정(§2 진단, §4 경계, §9 결정) → 확정본을 근거로 T2가 A단계(읽기 전용 토폴로지 뷰: osiris-api read RBAC + `/_api/infra/*` + 콘솔 Infra 메뉴)부터 구현.

---

## 11. C-1 — 제안→승인 척추 (2026-09-11 확정, 사용자: C-1 먼저·C-2는 이관 시)

조사로 드러난 사실: `:ActionType` 카탈로그·검증은 구현돼 있으나 **승인 라운드트립(80-action-layer P4)은 미배선**이다 — `:Approval` 노드 스키마만 있고 writeops는 "out of scope", `agent/internal/supervisor/gate.go`가 `approval.decision.<id>`를 구독만 하고 발행자가 0건. 그래서 C-1은 "MCP 도구 추가"가 아니라 **승인 척추를 인프라 범위로 처음 세우는 일**이다. C-2(실제 apply·operator·infra-* NS)는 첫 이관 서비스를 정할 때 D단계와 함께.

### 신규 불변식

| # | 결정 | 근거 |
|---|---|---|
| **IN17** | 인프라 액션은 `:ActionType`(infra.apply/scale/delete, `gateClass: approval.admin`)이고, 제안은 기존 `:Approval` 노드의 인스턴스다 | 80-action-layer 모델 재사용. 새 승인 메커니즘을 만들지 않는다(IN8의 "단일 승인 표면") |
| **IN18** | **HI15 예외 하나**: 기계주체는 `POST /api/infra/proposals`(제안 생성)만 할 수 있고 **결정(승인/반려)은 절대 못 한다**. 제안은 **inert** — 사람이 승인하기 전엔 아무 능력도 부여하지 않으므로, 기계의 제안 생성은 권한 경계가 아니다. 능력 경계는 **사람의 승인 결정**에 있다 | IN3("에이전트는 제안만") 집행. HI15("기계=GET만")를 최소 예외로 완화하되, 그 예외가 여는 것은 "inert 노드 생성"뿐 |
| **IN19** | `approval.decision.<id>`(승인/반려)의 **유일한 발행자는 세션 인증된 콘솔/REST 경로**다. supervisor·에이전트·기계주체는 이 결정을 어떤 형태로도 발행하지 못한다. decide 엔드포인트는 PersonOnly + RequirePerm(게이트의 admin 권한) | 80 §813 그대로 — "에이전트 터미널이 승인을 실어 나르는" 지름길 금지 |

### C-1 범위 (실행 제외 = C-2)

1. **osiris-core**: infra `:ActionType` 3종 시드(CreateActionType 호출 — 지금까지 아무도 안 부른 그 경로) + `:Approval` writeops 신설(제안 생성/대기목록 조회/결정). Neo4j 쓰기는 G7대로 core 단일 관문.
2. **osiris-api**: `POST /_api/infra/proposals`(IN18 — 기계주체 제안 허용) · `GET /_api/infra/proposals?status=pending`(목록) · `POST /_api/infra/proposals/{id}/decision`(IN19 — **PersonOnly + admin**, 세션 인증 유일 발행자). 제안 페이로드(kind·target·manifest)는 저장만 하고 **실행하지 않는다**.
3. **mcp/infra**: 쓰기 도구 `infra_propose_apply`/`_scale`/`_delete` — **제안 생성만**(직접 apply 없음). 읽기 도구(B단계)와 한 서버.
4. **콘솔 Approvals**: 자리표시자 → 실구현. 대기 중 인프라 제안 목록 + 승인/반려. 승인해도 **아직 apply되지 않고** status만 `approved`로(실행기는 C-2).

### C-2 (이관 시, 이 문서 §4~6과 합류)
승인된 제안을 실제 apply하는 orchestrator 실행기 + infra-operator RBAC(IN15) + 첫 infra-* NS(IN13/IN14). apply할 대상(이관 서비스)이 생길 때 의미가 서므로 그때.
