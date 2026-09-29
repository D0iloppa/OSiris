# OSiris Kubernetes 배포 (K8s Deployment)

> **범위**: [2단계](배포 전제가 k3s/k8s로 이동)의 산출물 형태 — Helm chart 하나, 네임스페이스·파드 배치, 네트워크 경계의 k8s 표현, genesis/master key 자격증명의 k8s 재배선, Agent CRD와 operator, 그리고 1단계(compose) 설계 자산 중 이관되는 것과 폐기되는 것.
>
> **입력**: `10-core-architecture.md` §1.7(G1~G10)·§2.6·§2.8·§6.2·§6.3·§8.4·§8.5, `30-onboarding-provisioning.md` §0(O1~O16)·§3.4·§5.2·§7.2, `60-identity-authn.md` §0(H1~H32)·§4.3·§4.5·§4.6·§19.8·§19.9, `IMPLEMENTATION-NOTES.md` §108.
> **전제**: 위 세 문서의 결정은 확정 사항이며 이 문서가 재논의하지 않는다. 이 문서는 **그것들을 k8s 위에서 어떻게 실현하는가**만 다룬다. G/D/A/I/W/C/H/O 번호 체계는 그대로 계승하고, 이 문서가 새로 확정한 결정에만 **K1~K13**을 새로 붙인다.
>
> **표기 규칙**: 표기 없는 서술 = 이 문서의 확정 결정. `[미검증]` = 확인 필요. 나머지 표기는 상위 문서와 동일.
>
> **인용은 줄번호가 아니라 §번호로 한다.** 이 문서의 초판은 `10-core-architecture.md`를
> 줄번호로 인용했는데, 같은 시각에 그 파일에 정정 문단이 삽입되면서 세 인용이 각각 4·10·16줄씩
> 밀렸다(감독 실측). 줄번호는 인용 대상이 살아 있는 문서일 때 **반드시 썩는다** — §번호는 썩지
> 않는다.

---

## 0. 결정 요약 (K1~K13)

| # | 결정 | 한 줄 근거 | 대가 |
|---|---|---|---|
| K1 | 산출물은 **설치 패키지 하나(Helm chart)**. 이미지 N개·파드 N개·설치 명령 1개. `osiris-core`와 `osiris-omniroute`는 **각자 파드**로 남는다 | omniroute가 프로바이더 API 키의 유일한 소유자다(`10-core-architecture.md` §9, `30` O13) — 한 이미지에 합치면 그 경계가 소멸한다 | 이미지 빌드·배포 단위가 compose 대비 늘어난다(Helm chart 관리 비용) |
| K2 | **k8s가 배포 전제가 된다.** compose 경로를 폐기하고, "compose 파일 하나로 어디서든 설치 완결"이라는 이식성 문장도 폐기한다 | `30-onboarding-provisioning.md` §7.2가 이미 2단계를 "1단계에서의 마이그레이션"으로 설계해 두었으나, 이번 결정은 그 마이그레이션을 **전제**가 아니라 **유일 경로**로 못박는다 | "어디서든"의 의미가 *도커만 있으면* → *k3s를 올릴 수 있으면*으로 바뀐다. 도커만 있는 호스트·소형 VPS·로컬 노트북 배포가 지원 대상에서 빠진다 |
| K3 | 진입점은 **Ingress Controller 단독**. 콘솔 SPA는 자기 파드(nginx)가 서빙. **`osiris-gw` 폐지** | `10-core-architecture.md` §2.3이 `osiris-gw`에 부여한 "안정적 이음새" 역할은 k8s에서 Ingress Controller가 이어받는다 — 별도 nginx 파드를 하나 더 두는 것은 중복이다 | `IMPLEMENTATION-NOTES.md` §108.1이 **실측으로 검증한** SSE 설정(`proxy_buffering off` · `proxy_cache off` · `proxy_read_timeout 3600s` · `proxy_http_version 1.1`, §108.1 "설정도 대조했다")을 Ingress 컨트롤러별 annotation으로 다시 표현해야 하고, annotation 문법이 컨트롤러마다 다르다(§8 미결) |
| K4 | `osiris-system` 네임스페이스에 `osiris-api`·`osiris-core`·`osiris-orchestrator`·`osiris-omniroute`·`osiris-egress` + Neo4j/NATS를 StatefulSet+PVC로 둔다. 코어는 `nodeAffinity: osiris.dev/role=main`으로 최초 노드에 고정 | 코어의 상태 소유권(Neo4j가 유일한 진실)은 노드 분산이 아니라 노드 고정으로 지켜야 데이터 정합이 단순해진다 | **명시적으로 수용한 대가: 메인 노드를 잃으면 컨트롤 플레인이 죽는다.** 조인 노드가 늘어도 가용성이 아니라 작업부하 분산만 얻는다 |
| K5 | `osiris-agents` 네임스페이스에 **직원 1명 = Pod 1개 + PVC·SA·Secret·NetworkPolicy**. 스케줄러가 자유 배치하므로 조인 노드로 확산된다 | `30-onboarding-provisioning.md` §7.2 세 번째 행이 이미 이 형태를 확정해 두었다 — *"`Agent` CR 생성 → operator가 Pod+PVC+SA+Secret+NetworkPolicy 렌더"* | 팀 단위 리소스 예산(`RoleResourceProfile`)이 노드 단위 자원 경쟁과 상호작용하므로, k8s ResourceQuota/LimitRange 설계가 별도로 필요해진다(이 문서 비범위) |
| K6 | 에이전트 샌드박스는 **현재 수준을 유지**한다: `readOnlyRootFilesystem` / `cap_drop: ALL` / `no-new-privileges` / 허용목록 egress. **새 샌드박스 런타임(Sysbox·Kata·gVisor)을 도입하지 않는다** | `10-core-architecture.md` §8.3이 이미 이 조합을 실측·확정했고, `[2단계] seccompProfile: RuntimeDefault`가 추가되는 것 외 변경이 없다. 에이전트가 필요한 자유는 "패키지 설치·git·빌드·허용목록 네트워킹" 수준이므로 일반 Pod의 `securityContext`로 충분하다 | 커널 공유형 격리(runc 기반)의 알려진 한계(커널 exploit 표면)를 그대로 안고 간다 — 이것이 "충분하다"는 판단의 명시적 반대급부다 |
| K7 | genesis 자격증명을 **OSiris가 만들지 않는다.** `bootstrap-master` Pod에 전용 ServiceAccount를 주고, projected SA token을 자동 발급받아, `osiris-core`가 **K8s TokenReview API로 검증**한다 | §4 참고. `10` §8.5의 신뢰 근거("core가 orchestrator의 주장을 믿는다")가 k8s에서는 K8s 자신이 Pod 신원을 보증하므로 불필요해진다 | controller-runtime 코드가 K8s API 서버에 TokenReview 호출 권한을 요구하게 된다(§7.1이 이 권한의 성질을 명문화) |
| K8 | 사람의 최초 로그인 자격증명(master key)은 `osiris-system`의 **K8s Secret**으로 둔다. 조작자는 `kubectl get secret`으로 읽는다 | `60-identity-authn.md` §9.1의 신뢰 논거(*"`docker`를 쓸 수 있는 사람 = 이미 전권자"*)가 한 단어만 바뀌어 대응된다: **`kubectl`을 쓸 수 있는 사람 = 이미 전권자** (§4.4) | 파일 삭제(H23)가 Secret 삭제로 바뀌는 것 외 실질적 대가는 없다 — H10 술어와 판정/자료 두 차원의 독립(§4.5)은 무변경이므로 이 대응이 새 취약점을 만들지 않는다 |
| K9 | 에이전트 생성은 **Agent CRD + operator**(현재의 `osiris-orchestrator`가 operator가 된다) | `10-core-architecture.md` §6.1이 CRD의 값을 *"desired state를 etcd에 캐시함으로써 컨트롤러 재기동과 무관하게 desired가 보존된다. 이것이 CRD가 실제로 벌어주는 값이며, 그 외의 이유로 CRD를 도입하는 것이 아니다"*로 한정해 두었고, 1단계의 약점("desired가 orchestrator 메모리에만 존재")이 이것으로 해소된다 | controller-runtime·CRD 버저닝·RBAC 설계가 새로 들어온다 — 이는 순수 추가 비용이며 K9의 근거가 되는 문장 자체가 "그 외의 이유로 CRD를 도입하는 것이 아니다"라고 스스로 한정한다 |
| K10 | Agent CR은 `osiris-agents`에 **namespaced**로 둔다 | K8s는 네임스페이스를 넘는 `ownerReference`를 허용하지 않는다(namespaced 객체의 owner는 같은 네임스페이스이거나 cluster-scoped여야 한다). CR을 `osiris-system`에 두면 `osiris-agents`의 Pod를 소유할 수 없어 가비지 컬렉션이 동작하지 않는다 | 없음 — 이유가 순수 기술 제약이므로 반대안이 애초에 성립하지 않는다. `kubectl delete agent <id>` 하나로 Pod·PVC·SA·Secret·NetworkPolicy가 함께 사라지는 이점을 얻는다 |
| K11 | CNI는 **k3s 기본으로 두고 실측 검증**한다 | NetworkPolicy는 기본이 **허용**이고 CNI가 지원하지 않으면 무시된다. Docker의 `internal: true`가 브리지에 외부 게이트웨이/라우트가 없는 **토폴로지적 차단**인 것과 성질이 다르다 | `[2026-07-28 Phase B]` **골격 확인**: k3s는 Flannel(NetworkPolicy 미지원)을 쓰지만 **kube-router의 netpol 컨트롤러를 내장해 기본 활성**하며 ingress/egress를 강제한다. **그러나 egress 정확성은 `[미검증]` 유지** — §3.4 |
| K12 | **노드 조인 신뢰 모델은 결정 보류.** 단일 노드로 시작하고 조인 지점만 구조적으로 열어둔다(코어 pinning + 에이전트 네임스페이스 분리) | 지금 결정하기에는 조인 인증 방식(토큰/인증서/외부 IdP 연동)의 선택지가 아직 좁혀지지 않았다 | 조인 트래픽에 대한 인가 모델이 문서화되지 않은 채 남는다(§8 미결) |
| K13 | **전면 재작성.** 이관/폐기 목록은 §6 | 배포 방식이 바뀌어도 온톨로지·인가·감사 계층은 불변 자산이어야 한다는 것이 상위 세 문서의 반복된 원칙(D1·D17·G7 등)이다 | 폐기 대상(§6.2)의 재작성 비용은 실비용이며 축소하지 않는다 |

---

## 1. 왜 k8s인가 — 목적과 수단을 혼동하지 않는다

**목적은 격리다. k8s는 그 격리를 구현하는 수단 중 하나이고, 스케일링은 목적이 아니라 "닫아 두지 않는 문"이다.**

`10-core-architecture.md` §1.7은 이미 1단계 compose 설계 전체를 "2단계로 가는 길을 막지 않는" 제약(G1~G10) 아래 정의했다. 즉 k8s로의 이행은 이 저장소의 처음부터 예정된 경로였다 — 이번 결정은 새 방향을 여는 것이 아니라, "예정된 2단계"를 **유일한 배포 전제**로 승격시키는 것이다(K2).

**격리가 여전히 1급 목표다**: K6이 명시하듯 이 문서는 에이전트 샌드박스 수준을 올리지 않는다. k8s가 주는 것은 "더 강한 격리"가 아니라 **선언적 상태 관리(K9)·가비지 컬렉션(K10)·네임스페이스 단위 정책 표현력(§3)**이다. 격리 자체의 강도는 K6이 못박은 대로 compose 시절과 동일하다.

**스케일링은 얻는 것이 아니라 열어 두는 것이다**: K4가 코어를 노드에 고정하므로, 조인 노드를 늘려도 컨트롤 플레인 가용성은 늘지 않는다(K4의 명시적 대가). 얻는 것은 에이전트 워크로드의 분산뿐이다(K5). 이 문서는 "k8s로 가면 고가용성을 얻는다"는 과장된 주장을 하지 않는다.

---

## 2. 네임스페이스·파드 배치

```
osiris-system (namespace)
├── osiris-api            (Deployment, replicas>=1, DaemonSet 가능 — 60 H2 A2)
├── osiris-core           (StatefulSet, replicas=1, nodeAffinity: osiris.dev/role=main)
├── osiris-orchestrator   (Deployment/StatefulSet — Agent operator 겸임, K9)
├── osiris-omniroute      (StatefulSet, 자체 PVC — LLM 게이트웨이, 별도 이미지·파드)
├── osiris-egress         (Deployment — squid 허용목록 프록시, A3 집행)
├── neo4j                 (StatefulSet + PVC, nodeAffinity: osiris.dev/role=main)
└── nats                  (StatefulSet + PVC, nodeAffinity: osiris.dev/role=main)

osiris-agents (namespace)
└── (직원 1명 = Pod 1개 + PVC + SA + Secret + NetworkPolicy) × N
    — Agent CR이 각 세트를 소유(ownerReference), 스케줄러 자유 배치

kube-system
└── (CNI, kube-dns — §3의 egress 예외 대상)
```

**K4의 대가를 다시 명시한다**: `osiris.dev/role=main` 노드가 죽으면 `osiris-core`·Neo4j·NATS가 함께 죽고, 새 노드에 재스케줄되지 않는다(고정이므로). 이것은 사고가 아니라 **명시적으로 수용한 설계**다 — 코어 상태의 정합을 노드 분산보다 우선한다.

---

## 3. 네트워크

### 3.1 NetworkPolicy 매핑

`10-core-architecture.md` §2.6이 compose의 `internal: true` 네트워크 분리(`osiris-edge`/`osiris-data`/`osiris-agents`)로 그린 DMZ 패턴은, §2.6 말미의 예고대로 **"기본 deny + 4개 예외의 NetworkPolicy"**(`10` §2.6)로 표현된다. 원문 그대로 인용한다:

> *"즉 **1단계의 "분리된 도커 네트워크"가 2단계에서 "기본 deny + 4개 예외의 NetworkPolicy"가 된다.** 경계의 *의미*는 같고 *해상도*만 올라간다."*(`10-core-architecture.md` §2.6)

`10` §2.6의 4개 예외는: kube-dns(53/UDP) · NATS(4222) · osiris-egress(3128) · osiris-core 에이전트 대면 포트(8090). 이 문서는 이 넷을 그대로 계승한다.

### 3.2 A3(단일 이그레스) 집행 방식 — 예외가 4개에서 5개가 된다

`10` §2.6이 나열한 4개 예외 중 하나가 이미 **kube-dns**다. 이것은 compose 시절에는 없던 항목이다 — Docker 브리지 네트워크는 임베디드 DNS(`127.0.0.11`)를 자동으로 갖지만, k8s Pod는 DNS 해석 자체가 `kube-system`의 별도 서비스이므로 **egress default-deny 정책에 예외를 명시적으로 뚫지 않으면 이름 해석 자체가 안 된다.** `10` §2.6이 이미 이 예외를 넷 중 하나로 포함해 예고했으므로, 이 문서가 새로 발명한 예외가 아니라 **필수로 추가된** 예외임을 여기서 명문화한다.

집행 방식: `osiris-agents` 네임스페이스 기본 정책은 **egress default-deny**(`podSelector: {}`, `policyTypes: [Egress]`) + `HTTPS_PROXY` 환경변수로 `osiris-egress:3128` 강제. `10-core-architecture.md` §2.6이 이미 제시한 YAML 골격(§2.6 말미)을 그대로 계승한다.

### 3.3 K3 — Ingress Controller 단독 진입

- `osiris-gw`(compose 시절 nginx, 정적 자산 서빙 + `/api/*` 분기)를 폐지하고 그 두 역할을 분리한다: 정적 자산 서빙은 **콘솔 SPA 자신의 파드(nginx)**가, 경로 분기·TLS 종단·SSE 패스스루는 **Ingress Controller**가 맡는다.
- SSE 요구사항(`10-core-architecture.md` §2.7, D20)은 Ingress annotation으로 재표현해야 한다. `IMPLEMENTATION-NOTES.md` §108.1이 실측한 nginx location 설정 — *"`location /omni/`와 `location /_api/`가 둘 다 `proxy_buffering off` · `proxy_cache off` · `proxy_read_timeout 3600s` · `proxy_http_version 1.1`"*(§108.1) — 을 컨트롤러별 annotation으로 옮겨야 하며, 이 문법은 컨트롤러마다 다르다(§8 미결).

---

### 3.4 K11 실측 — k3s의 NetworkPolicy 집행 `[2026-07-28 Phase B]`

**확인된 것**: k3s의 기본 CNI는 **Flannel(VXLAN)이고 Flannel 자체는 NetworkPolicy를 지원하지 않는다.** 대신 k3s가 **kube-router의 netpol 컨트롤러 라이브러리를 내장**해 별도로 함께 띄우며, 그것이 ingress/egress 정책을 강제한다:

> *"K3s includes an embedded network policy controller… The underlying implementation is kube-router's netpol controller library (no other kube-router functionality is present)."*
> — [k3s docs: Networking Services](https://docs.k3s.io/networking/networking-services)

- **기본 활성**: 비활성화 플래그가 `--disable-network-policy`("Disable k3s **default** network policy controller", [k3s docs: server CLI](https://docs.k3s.io/cli/server))이므로 기본이 활성이다.
- **egress 방향도 지원된다** — A3(단일 이그레스)가 egress 정책에 의존하므로 이것이 확인되어야 했다.
- **주의(운영)**: *"Network policy iptables rules are not removed if the K3s configuration is changed to disable the network policy controller"* — 컨트롤러를 껐다고 규칙이 사라지지 않는다. 수동 정리가 필요하다.

**여전히 `[미검증]`으로 남기는 것 — 그리고 왜 남기는가**:

1. **k3s가 vendoring한 kube-router 버전에 알려진 ingress/egress 상호작용 결함이 있었다.** [k3s#2390](https://github.com/k3s-io/k3s/issues/2390)은 *"a pod 'A' selected by an egress policy can contact a pod 'B' which is selected by an ingress policy that should forbid this … k3s is using a forked / vendored version from prior to 0.4.0"*라고 적는다(upstream kube-router 1.0.0에서 수정). **현재 k3s가 여전히 그 구버전을 vendoring하는지는 확인하지 못했다.**
2. **`default-deny egress + 5예외`가 실제 트래픽에서 의도대로 동작하는지는 문서 조사로 닫을 수 없다.** 이 프로젝트는 격리를 **실측으로** 확인해 왔다 — `bin/verify-isolation.sh`가 15개 검사로 *라우트 부재*를 측정했고(`IMPLEMENTATION-NOTES.md` §104.5 완료조건 8), 그 스크립트는 k8s용으로 재작성 대상이다(§6.2).

**따라서 K11의 `[미검증]`은 "CNI가 NetworkPolicy를 지원하는가"에서 "우리 정책이 실제로 막는가"로 좁혀진 상태로 남는다.**

**닫는 수단은 만들어졌다 `[2026-07-28 Phase C-2]`**: `bin/verify-isolation-k8s.sh`가 `kubectl exec`로 도달 가능성을 찍는다. 기존 `bin/verify-isolation.sh`와의 대응은 §6.2에 적는다.

> **검사 개수를 정확히 적는다(감독 실측)**: 기존 `verify-isolation.sh`는 **현재 환경에서 15개 판정을 실행**하며, 실제 에이전트 워크로드를 요구하는 계열(D1~D8)은 *"SKIP: 실행 중인 실제 에이전트 컨테이너가 없습니다"*로 **명시적으로 건너뛴다** — 조용히 통과하지 않는 것이 그 스크립트의 규율이다. 즉 "15개"는 *정의된 검사 수*가 아니라 *에이전트 없이 실행 가능한 판정 수*다. k8s 후속판도 같은 규율을 따라야 하며, 에이전트 계열은 C-3(Agent CRD)이 라벨 규약을 확정한 뒤에야 실행 가능하다.

**남은 조건**: 이 스크립트가 **실제 k3s 클러스터에서 15개 판정 전부 PASS**하고, C-3 이후 에이전트 계열까지 PASS해야 K11이 닫힌다. 그때까지 `[미검증]` 유지.

## 4. 자격증명

### 4.1 K7 — genesis 자격증명의 재배선

**compose(1단계)의 신뢰 근거**: `10-core-architecture.md` §8.5이 명시한 그대로다.

> *"이 인증이 의미를 갖는 이유는 **오직 `osiris-orchestrator`만 `docker.sock`을 가져 컨테이너를 실제로 만들 수 있고**(G8) 따라서 "방금 만든 컨테이너의 진짜 ID"를 주장할 수 있는 유일한 발신자이기 때문이다"*(`10-core-architecture.md` §8.5)

즉 1단계는 **core가 orchestrator의 주장을 믿는** 구조다 — orchestrator가 "이 컨테이너 ID는 내가 방금 만들었다"고 말하면 core는 그 말을 믿고 토큰을 그 ID에 바인딩한다(§8.5의 W4). 이것은 §6.4류의 신뢰 경계이지, 제3자가 독립적으로 검증하는 구조가 아니다.

**k8s(2단계)에서 이 주장이 필요 없어지는 이유**: K8s 자신이 Pod 신원을 보증한다. `bootstrap-master` Pod에 전용 ServiceAccount를 부여하면, kubelet이 그 Pod에 **projected ServiceAccount token**을 자동으로 마운트하고, 그 토큰은 K8s API 서버가 서명한 값이다. `osiris-core`는 orchestrator의 "이것이 방금 만든 컨테이너다"라는 **주장을 믿을 필요 없이**, `tokenreviews.authentication.k8s.io`에 그 토큰을 제출해 **K8s 자신에게 직접 검증받는다.** 신뢰의 원천이 "orchestrator만 docker.sock을 가진다"는 배타적 능력 논증에서 "K8s가 자신이 스케줄한 Pod의 신원을 보증한다"는 플랫폼 자체의 성질로 옮겨간다.

**흐름**: core가 `currentOrgRevision == 0` 확인(W1) → orchestrator가 SA+RoleBinding 생성 → Pod 생성 → Pod가 core를 호출하며 자신의 projected token을 제시 → core가 TokenReview로 검증 → 커밋 후 SA 삭제(W3).

### 4.2 W1~W5 대조표

| # | compose(1단계) | k8s(2단계) | 대응 |
|---|---|---|---|
| W1 | `currentOrgRevision >= 1`이면 genesis 요청 무조건 403 | **무변경.** 판정 주체·조건 동일 | 변경 없음 |
| W2 | 하드 TTL 24h(발급=컨테이너 생성 시각 기준) | **`expirationSeconds`**(projected SA token의 만료 필드)로 표현. K8s 네이티브 메커니즘으로 같은 성질을 얻는다 | TTL 개념은 유지, 집행 수단이 core의 자체 계산에서 K8s 토큰 필드로 이동 |
| W3 | 커밋 후 명시 revoke + `bootstrap-master` Drain→Delete | **커밋 후 SA 삭제.** SA가 사라지면 UID 불일치로 토큰이 인증 실패가 된다 — 공식 문서 원문으로 확인됨(§4.3) | **성립. 단 "즉시"가 아니라 "최대 60초"**(finalizer pending-deletion 시) — 그리고 그 60초는 §7.4가 이미 값을 치른 봉투 안이다(§4.3.1) |
| W4 | 발급 시 컨테이너 ID에 바인딩(3차 정정으로 창이 0) | **K8s가 Pod 신원을 보증**하므로 바인딩 자체를 core가 계산할 필요가 없다 — TokenReview 응답이 Pod 신원(namespace/serviceaccount/pod name)을 직접 알려준다 | compose의 "발급=바인딩" 성질이 k8s에서는 "K8s가 신원 자체를 보증"으로 대체되어 더 단순해진다 |
| W5 | 재온보딩이 창을 다시 열지 않는다 | **무변경.** W1과 같은 술어를 재사용하므로 재온보딩 시나리오에서 성질이 바뀌지 않는다 | 변경 없음 |

### 4.3 ~~`[미검증]`~~ **해소** — ServiceAccount 삭제와 토큰 무효화 `[2026-07-28 Phase B 실측]`

**결론: SA를 삭제하면 그 SA로 발급된 bound token은 TokenReview에서 인증 실패가 되고, 같은 이름으로 SA를 재생성해도 이전 토큰은 부활하지 않는다.** 공식 문서 원문:

> *"If the referenced object or the ServiceAccount is pending deletion (for example, due to finalizers), then for any instant that is 60 seconds (or more) after the `.metadata.deletionTimestamp` date, authentication with that token would fail. **If the referenced object no longer exists (or its `metadata.uid` does not match), the request will not be authenticated.**"*
> — [Kubernetes: Managing Service Accounts — Bound service account tokens](https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/)

세 가지가 확정된다:

| 시나리오 | 판정 |
|---|---|
| SA가 완전히 사라짐 | **즉시 인증 실패** (`no longer exists`) |
| 같은 이름으로 SA 재생성 | **즉시 인증 실패** — 새 UID가 발급되므로 `metadata.uid does not match`. **"폐기했는데 부활"이 성립하지 않는다** |
| SA가 pending deletion (finalizer 때문에 남아 있음) | `deletionTimestamp` **+60초**부터 실패 |

bound token은 클레임에 ServiceAccount UID와 (설정 시) Pod UID를 함께 담는다(KEP-1205). UID가 바인딩의 실체이므로 이름 재사용으로는 되돌릴 수 없다 — 이것이 §4.1이 K7을 채택한 이유(K8s가 신원의 보증인)의 구체적 형태다.

#### 4.3.1 ★ W3 문구를 정정한다 — "즉시"가 아니라 "최대 60초"

세 번째 행 때문에 **W3의 서술을 고쳐야 한다.** `10-core-architecture.md` §8.5의 W3는 *"커밋 후 명시적 revoke"*를 즉시성으로 서술하지만, k8s 형태에서는 finalizer가 붙은 경우 **최대 60초의 유예 창**이 생긴다. 이 문서는 그것을 숨기지 않고 다음과 같이 규정한다:

> **W3(k8s 형태)**: 온보딩 커밋 후 `bootstrap-master`의 ServiceAccount를 삭제한다. 자격증명은 **삭제 요청 시점부터 최대 60초 안에** 무효가 된다.

**그리고 이 60초는 새 비용이 아니다.** `60-identity-authn.md` §7.4가 이미 이 시스템의 리보크 지연 상한을 **최대 60초**로 못박고 명시적 대가로 인정했다:

> *"**대가 — 명시**: **강제 로그아웃(리보크)이 최대 60초 지연된다.** 즉시성을 요구하는 침해 대응 경로는 이 지연을 알고 있어야 한다."*(`60-identity-authn.md` §7.4)

즉 SA 삭제의 유예 창은 **설계가 이미 값을 치른 봉투 안쪽에 들어간다.** 두 지연이 겹치는 것이 아니라 같은 상한을 공유한다 — `IMPLEMENTATION-NOTES.md` §104.3이 SSE 워치독 주기(30초)를 정할 때 *"장수명 연결이 그 위에 두 번째·더 큰 지연을 얹으면 새 대가를 만드는 것"*이라며 60초 봉투 안쪽을 요구한 것과 정확히 같은 판단 기준이다.

#### 4.3.2 `[확인 실패]`로 남기는 세부

결정에 영향을 주지 않지만 정직하게 남긴다:

- **`BoundServiceAccountTokenVolume`의 정확한 GA 버전** — KEP-1205의 성숙도 표는 Beta 1.21 / GA 1.22로 읽혔으나 널리 인용되는 "1.21 GA"와 어긋난다. **이 결정은 여기에 의존하지 않는다** — k3s가 번들하는 어떤 최신 버전도 두 값 모두를 한참 넘겼다.
- **legacy(비-bound) SA 토큰의 정확한 무효화 의미론** — 이 설계는 legacy 토큰을 쓰지 않으므로 대조만 필요한 항목이며, 원문 인용을 확보하지 못했다.

### 4.4 K8 — master key의 k8s 재배선

`60-identity-authn.md` §9.1이 break-glass의 **허용 대상**을 규정하며 세운 논거를 원문 그대로 인용한다:

> *"**허용 대상**: 호스트에 물리적/관리적 접근이 있는 사람 = `docker`를 쓸 수 있는 사람 = **이미 전권자**(docker.sock은 호스트 root와 동등, G8의 전제)."*(`60-identity-authn.md` §9.1)

k8s에서는 이 논거가 **한 단어만 바뀌어** 성립한다 — *`docker`를 쓸 수 있는 사람* → **`kubectl`을 쓸 수 있는 사람**. 그래서 master key를 `osiris-system` 네임스페이스의 K8s Secret으로 옮기는 것이 안전한 이유는 다음과 같다: `kubectl get secret`으로 이 값을 읽을 수 있는 주체는 이미 클러스터 전권자이므로, **이 값을 읽는 데 필요한 권한이 이 값이 여는 권한과 같거나 그보다 크다.** 두 배포 형태에서 논거가 동형인 것은 우연이 아니라, 원 논거가 저장 매체가 아니라 **접근 권한의 포함관계**에 의존하도록 쓰여 있었기 때문이다.

> **`[2026-07-28 감독 정정]`** 이 절의 초판은 §9.1을 *"0400 파일을 읽을 수 있는 사람은
> 이미 host-root-equivalent"*로 인용부호에 넣어 인용했으나 **그것은 §9.1의 원문이 아니다**
> (취지는 같으나 문장이 다르다). 위 인용문이 실제 원문이며, 이 레포의 인용 정확성 규율에
> 따라 교체했다. 파생 표현을 원문처럼 인용하면 다음 사람이 그 문장을 근거로 삼는다.

- **H23(파일 삭제)** → **Secret 삭제**. 판정 차원(H10, §4.3의 두 조건)과 자료 차원(파일/Secret 존재)의 독립(`60-identity-authn.md` §4.5)은 **무변경**이다 — 두 차원이 독립이라는 것이 원 설계의 안전 성질이었고, 저장 매체를 파일에서 Secret으로 바꾸는 것은 자료 차원의 구현만 바꿀 뿐 그 독립성 자체를 건드리지 않는다.
- H10 술어("두 조건이 모두 참일 때 영구 거부")는 그래프 상태(`currentOrgRevision`, `org.admin` 보유자)만으로 평가되므로 저장 매체 변경과 무관하게 그대로 유지된다.

---

## 5. Agent CRD와 operator

### 5.1 K9 — CRD를 도입하는 유일한 이유

`10-core-architecture.md` §6.1을 그대로 인용한다:

> *"[1단계]에 물성 저장소가 없다는 사실의 함의 — 정직하게: desired state가 orchestrator 메모리에만 존재한다. 그래서 orchestrator가 죽으면 수렴이 멈춘다 … [2단계]에서 CRD spec이 이 파생 결과를 etcd에 캐시함으로써 컨트롤러 재기동과 무관하게 desired가 보존된다. **이것이 CRD가 실제로 벌어주는 값이며, 그 외의 이유로 CRD를 도입하는 것이 아니다.**"*(`10-core-architecture.md` §6.1)

이 문서는 이 한정을 그대로 계승한다 — CRD는 "쿠버네티스답다"거나 "선언적이라 우아하다"는 이유로 도입되는 것이 **아니다.** 유일한 근거는 desired state의 영속화다. `osiris-orchestrator`가 그대로 operator 역할을 겸임한다(~~controller-runtime 기반~~ **[2026-07-28 D-1 구현 시 이탈]** → `client-go`의 dynamic informer + workqueue. 근거: `driver/kube`가 이미 `dynamic.Interface`로 같은 CRD를 다루므로 controller-runtime을 더하면 **같은 CRD에 두 개의 서로 다른 클라이언트 추상화**가 생긴다. 네임스페이스 하나·리소스 하나를 보는 작은 수렴 루프라 Manager/leader-election의 이점이 필요 없다. 실측으로 동작 확인 — IMPLEMENTATION-NOTES.md §115.4) — 새 컴포넌트가 아니라 기존 컴포넌트의 역할 확장이다(`10-core-architecture.md` §6.3이 이미 3종 CRD를 `Team`/`Agent`/미상 나머지로 스케치해 두었다).

**대가**: ~~controller-runtime 도입~~(**[2026-07-28]** 위 이탈로 이 비용은 발생하지 않았다 — 새 의존성 없이 이미 있는 `client-go`로 구현됐다), CRD 스키마 버저닝(`apiVersion: osiris.dev/v1alpha1`), operator에 필요한 RBAC(ClusterRole/RoleBinding) 설계가 새로 생긴다. 이는 순수 추가 비용이며, K9의 근거 문장 자체가 "그 외의 이유로 CRD를 도입하는 것이 아니다"라고 스스로 그 편익의 한계를 그어 두었다.

### 5.2 K10 — Agent CR을 `osiris-agents`에 두는 이유는 기술 제약이다

K8s의 `ownerReference`는 **네임스페이스를 넘을 수 없다** — namespaced 객체의 owner는 같은 네임스페이스이거나 cluster-scoped 객체여야 한다. `Agent` CR을 `osiris-system`(코어가 사는 네임스페이스)에 두면, `osiris-agents`에 있는 Pod/PVC/SA/Secret/NetworkPolicy를 그 CR이 **소유할 수 없다** — 다른 네임스페이스의 객체를 owner로 지정하는 것 자체가 API 서버에서 거부된다. 그 결과 가비지 컬렉션이 동작하지 않아 `kubectl delete agent <id>`가 CR만 지우고 나머지 리소스는 고아로 남는다.

따라서 CR을 Pod와 **같은 네임스페이스**(`osiris-agents`)에 둔다. 이는 설계 취향이 아니라 K8s 오브젝트 모델의 제약이 강제하는 유일한 선택이다. 이 배치로 **정리를 K8s 가비지 컬렉터에 위임**할 수 있다 — `kubectl delete agent <id>` 하나로 Pod·PVC·SA·Secret·NetworkPolicy가 함께 사라진다.

---

## 6. 이관·폐기

### 6.1 이관 (변경 없이 그대로 계승)

| 자산 | 근거 |
|---|---|
| G/D/A/I/W/C/H/O 번호 체계 전부 | 배포 방식과 무관한 설계 결정 언어 |
| 온톨로지(I1~I17)·시드·writeops | `20-ontology-schema.md`가 소유, 배포와 무관 |
| RBAC(DG1~DG5, `authz.CheckDelegation`)·H10 술어·`orgstate` | `60-identity-authn.md` §20~§21, 그래프 상태 기반 판정은 저장 매체 무관 |
| 소스 텍스트 린트 6군(HI15·HI16·HI17·H10·AUD·GEN) | 코드를 검사하므로 배포 방식과 무관 |
| core 테스트 23개 | 코어 로직 자체는 무변경 |

### 6.2 폐기

| 자산 | 사유 |
|---|---|
| `docker-compose.yml` | K2 — compose 경로 자체가 폐기 |
| `osiris-up` | 컨테이너 생성 주체가 orchestrator/operator로 이관(K9) |
| `driver/docker` 패키지 | `driver/kube`(controller-runtime)로 대체(`10-core-architecture.md` §6.2가 이미 이 이음새를 예정) |
| `genesisboot`(순서 보장 테스트 10개 포함) | genesis 발급 시퀀스가 K7로 재배선되어 순서 보장 대상 자체가 바뀐다 |
| `verify-isolation.sh` 15검사 | k8s용으로 재작성 필요 — NetworkPolicy·PSP/PSA 검사로 대체 |
| `masterkey` 패키지의 파일 IO 절반 | K8이 Secret API로 대체하는 부분만큼 파일 IO 코드가 불필요해진다 |

---

## 7. 새 항목 — 어느 기존 문서에도 없는 것

### 7.1 osiris-core의 TokenReview 권한 — 워크로드 생성 권한이 아니다

K7로 인해 `osiris-core`는 `tokenreviews.authentication.k8s.io`에 대한 `create` 권한을 갖는다. **이것은 인증 검증 권한이지 워크로드 생성 권한이 아니다.** `10-core-architecture.md` §1.7의 G8("core는 워크로드를 만들지 못한다", docker.sock은 orchestrator에만)의 k8s 대응은 **core가 Pod/Deployment를 만들거나 수정할 수 없다**는 뜻이며, TokenReview 호출은 "제출된 토큰이 유효한 K8s 신원인가"를 K8s API 서버에 **묻는** 것일 뿐 그 자체로 아무 리소스도 만들거나 바꾸지 않는다. 이 구분을 명문화하는 이유는, 이후 누군가 RBAC 감사에서 "core가 k8s 권한을 얻었다"를 "core가 워크로드를 만들 수 있게 됐다"로 오독할 가능성이 실재하기 때문이다 — TokenReview 권한과 워크로드 생성 권한은 K8s RBAC 상에서도 완전히 별개의 리소스(`tokenreviews` vs `pods`/`deployments`)이며 하나가 다른 하나를 함의하지 않는다.

### 7.2 NATS 인증 재배선

현재(1단계) `osiris-orchestrator`가 `nats/generated/authorization.conf`를 생성하고 NATS 서버에 SIGHUP을 보내 리로드한다(`orchestrator/internal/natsauth`). k8s에서는 이 설정 파일이 **ConfigMap**(또는 자격증명 성격이 있는 부분은 **Secret**)이 되고, 리로드는 파일 변경 감지(sidecar reloader 또는 NATS 자체의 config reload 지원) 방식으로 바뀐다. `60-identity-authn.md`의 HI9 린트(`$KV.AUTHN_*` 버킷은 `osiris-api`만 접근)의 검사 대상도 이 재배선에 맞춰 ConfigMap/Secret 접근 RBAC로 확장되어야 한다 — 린트 자체의 판정 로직(코드 정적 검사)은 6.1(이관)이 계승하지만, 검사가 들여다볼 매니페스트 형식이 바뀐다.

### 7.3 A3 집행의 새 예외 — kube-dns

§3.2에서 이미 서술했듯, `10` §2.6이 말한 "기본 deny + 4개 예외"의 그 4개 예외 목록 자체에 이미 kube-dns가 포함되어 있다("즉 1단계의 '분리된 도커 네트워크'가 2단계에서 '기본 deny + 4개 예외의 NetworkPolicy'가 된다" — `10-core-architecture.md` §2.6). 이 문서는 그 예외가 **왜 필수인지**(k8s Pod의 DNS 해석은 compose의 임베디드 DNS와 달리 별도 서비스로 분리되어 있어 명시적 egress 허용 없이는 이름 해석 자체가 실패한다)를 명문화한다.

---

## 8. 미결

| 항목 | 내용 |
|---|---|
| K12 — 노드 조인 신뢰 모델 | 결정 보류. 단일 노드 시작 + 코어 pinning + 에이전트 네임스페이스 분리로 조인 지점만 구조적으로 열어 둔다 |
| K11 — 우리 정책이 실제로 막는가 | `[미검증]` **좁혀짐**(§3.4). "CNI가 지원하는가"는 확인됨(kube-router netpol 내장, 기본 활성, egress 포함). 남은 것은 (a) vendored kube-router 버전의 결함 잔존 여부 (b) `default-deny egress + 5예외`의 실기 동작 — `verify-isolation.sh` k8s 후속판으로 닫는다(Phase C 완료조건) |
| Ingress 컨트롤러 종류 | K3의 SSE annotation(§3.3)이 여기 종속된다 — 컨트롤러가 확정되지 않으면 annotation 문법을 최종화할 수 없다 |
| `POST /onboarding/commit` 미구현 | `30-onboarding-provisioning.md` §5.2가 정의한 15단계 단일 트랜잭션(§9.2 단계 1-12 MERGE 쓰기 + I1~I12 검증 + 리허설 + `OrgRevision`+1)의 실제 구현. `IMPLEMENTATION-NOTES.md` §109.9/§4977이 이미 "미구현"으로 기록해 두었다 — 배포 방식(compose/k8s)과 무관하게 남는 최대 미구현 항목이며, 이 문서가 새로 만든 갭이 아니다 |
| 컨설턴트 시스템 프롬프트의 소유자·위치 | `30-onboarding-provisioning.md` §3.4.6이 "그 파일의 소유자와 위치가 아직 어디에도 없다"고 이미 기록한 갭. 컨설턴트는 `:Role`이 없으므로(그래프 밖) 이미지에 구운 정적 자산이어야 한다는 방향만 정해져 있고, k8s ConfigMap/이미지 중 어느 쪽으로 담을지는 이 문서도 결정하지 않는다 |
| ServiceAccount 삭제 → 토큰 무효화 의미론 | §4.3 참고. Phase B 실측 대상 |

---

## 9. 전체 토폴로지 (ASCII)

```
                                  인터넷
                                    │
                                    ▼
                         ┌─────────────────────┐
                         │  Ingress Controller   │  ← K3: osiris-gw 폐지, 진입점 단독화
                         │  (TLS 종단, SSE 패스스루) │     annotation은 §8 미결
                         └──────────┬────────────┘
                                    │
                ┌───────────────────┼────────────────────┐
                ▼                                        ▼
      ┌──────────────────┐                    ┌──────────────────────┐
      │ console-spa (nginx)│                   │  osiris-api (Deploy)  │
      │  정적 자산 서빙만    │                   │  인증·인가·세션        │
      └──────────────────┘                    └──────────┬────────────┘
                                                            │
   osiris-system (namespace) ─────────────────────────────┼──────────────────────
                                                            ▼
   ┌───────────────────────────────────────────────────────────────────────────┐
   │  nodeAffinity: osiris.dev/role=main  (K4 — 고정, 대가: SPOF)                │
   │                                                                             │
   │   ┌──────────────┐   TokenReview    ┌───────────────────┐                  │
   │   │ osiris-core   │◀────────────────│ bootstrap-master   │ (임시 Pod, K7)   │
   │   │ (StatefulSet) │  (K8s API 서버로  │  전용 SA + projected│                  │
   │   │               │   검증 위임)      │  token              │                  │
   │   └──────┬────────┘                 └───────────────────┘                  │
   │          │ graph-write API 단일 관문                                        │
   │          ▼                                                                  │
   │   ┌──────────────┐        ┌──────────────┐       ┌────────────────────┐    │
   │   │    neo4j      │        │     nats      │       │ osiris-orchestrator │   │
   │   │ (StatefulSet+ │        │ (StatefulSet+ │       │  = Agent operator   │   │
   │   │     PVC)      │        │     PVC)      │       │  (K9, controller-   │   │
   │   └──────────────┘        └──────────────┘       │   runtime)          │   │
   │                                                    └──────────┬──────────┘   │
   │   ┌──────────────┐        ┌──────────────┐                   │              │
   │   │osiris-omniroute│       │ osiris-egress │◀──────────────────┘  reconcile   │
   │   │ (StatefulSet,  │       │ (허용목록 프록시│                                  │
   │   │  자체 이미지·파드)│      │  A3 집행)      │                                  │
   │   └──────────────┘        └───────┬───────┘                                  │
   └───────────────────────────────────┼──────────────────────────────────────────┘
                                        │ HTTPS_PROXY (egress default-deny + 5예외)
                                        │  kube-dns(53) · nats(4222) · egress(3128)
                                        │  · core:8090 · [omniroute 아웃바운드도 경유]
   osiris-agents (namespace) ──────────┼──────────────────────────────────────────
                                        ▼
   ┌───────────────────────────────────────────────────────────────────────────┐
   │  스케줄러 자유 배치 — 조인 노드로 확산 가능 (K5)                              │
   │                                                                             │
   │   Agent CR(직원1) ──owns──▶ Pod + PVC + SA + Secret + NetworkPolicy         │
   │   Agent CR(직원2) ──owns──▶ Pod + PVC + SA + Secret + NetworkPolicy   ...   │
   │   (K10: CR이 Pod와 같은 네임스페이스 — ownerReference가 네임스페이스를        │
   │    못 넘으므로. `kubectl delete agent <id>` 하나로 전부 GC)                  │
   │                                                                             │
   │   각 Pod: readOnlyRootFilesystem / cap_drop:ALL / no-new-privileges /       │
   │           seccompProfile:RuntimeDefault (K6 — 새 샌드박스 런타임 없음)        │
   │           도달 가능: nats, osiris-core:8090, osiris-egress:3128 뿐            │
   └───────────────────────────────────────────────────────────────────────────┘

   조인 노드 (미결, K12) ─ ─ ─ ─ ─▶ osiris-agents Pod만 확산, osiris-system은 고정
```

---

## 10. 실제 배포가 이 문서에 되먹인 것 (2026-07-28, Phase C-4 / D)

이 절은 **차트를 진짜로 install해서** 확인된 것만 적는다. 상세 경위와 결함 목록은
`IMPLEMENTATION-NOTES.md` §114–§115에 있고, 여기에는 **이 문서의 서술을 바꾸는 것**만 남긴다.

### 10.1 K11이 실측으로 닫혔다 (NetworkPolicy 집행)

§3.4가 *"k3s 기본 CNI가 NetworkPolicy를 실제로 집행하는지 실측 필요"*로 남겨 둔 항목:

**대조 실험으로 종료.** 디버그 Pod를 `orchestrator` 라벨로 띄우면 neo4j 접속이 거부되고,
같은 Pod를 `core` 라벨로 바꾸면 성공했다 → **집행된다.** 따라서 "kubelet 프로브가 차단되지
않는다"는 관찰도 정책이 무력한 탓의 허상이 아니라 진짜 발견이다.

operator 도입(§5) 후 **에이전트 Pod가 실제로 뜨므로** 격리도 직접 측정됐다:

```
osiris-agents 안의 Pod 에서 →
  osiris-omniroute:20128  차단   ← §9.0의 "에이전트는 omniroute에 직접 도달 불가"
  neo4j:7687              차단
  osiris-core:8090        도달(404)  ← §2.6
  osiris-egress:3128      허용
  nats:4222               허용
```

에이전트별 NetworkPolicy는 **불필요**하다 — 네임스페이스 전역 정책으로 충분함이 확인됐다.

### 10.2 §3의 네트워크 서술에 추가되는 두 가지

**(a) headless Service만으로는 기동이 교착된다.** headless Service의 DNS는 **ready인
엔드포인트만** 반환하므로, 아직 준비 안 된 대상의 이름은 **NXDOMAIN**이 된다. `core`/
`orchestrator`의 재시도 루프는 *"이름은 풀리고 TCP만 거부된다"*를 전제로 쓰여 있어 여기서
멈춘다. → `<name>-headless`(governing) + `<name>`(ClusterIP, 클라이언트용)으로 분리한다.
불변 호스트명 3개는 보존된다.

**(b) apiserver egress는 포트를 두 개 열어야 한다.** orchestrator가 operator를 겸임하면서
apiserver(`10.43.0.1:443`)에 도달해야 하는데, `ipBlock: 0.0.0.0/0, port: 443`만으로는
막혔다. 이 클러스터의 kube-router netpol이 egress `ports:`를 **kube-proxy DNAT 이후의 실제
포트**로 매칭하고, k3s의 apiserver는 control-plane 노드에 hostNetwork로 떠 있어 그 포트가
`6443`이기 때문이다(Endpoints가 `<node-ip>:6443`임을 확인). **443과 6443을 모두 허용한다.**
어느 CNI가 어느 지점에서 매칭하는지는 클러스터마다 다를 수 있으므로 둘 다 남긴다 —
`egress-deployment.yaml`의 기존 TODO와 같은 성격의 **인정된 한계**다.

### 10.3 K3의 진입점 전환이 실제로 일어났다

`osiris-gw`(compose)가 은퇴하고 **Ingress 컨트롤러가 그 역할(단일 진입점 + `/omni/` 프록시)을
물려받았다.** 그 결과 omniroute NetworkPolicy에 **ingress 컨트롤러 네임스페이스를 허용**해야
한다(없으면 502). `osiris-agents`는 계속 제외 — §9.0이 살아 있어야 한다.

외부 도메인(`https://osiris.example.com/`)은 **k3d 로드밸런서 컨테이너를 기존 오버레이 네트워크에
이중 홈으로 붙여** 연결한다. 이는 compose 시절 `osiris-gw`만 그 네트워크에 물리던 것과 **같은
패턴**이며 격리 구조를 새로 뚫지 않는다. TLS는 바깥 프록시에서 종단하는 1홉 추가 구조를 유지한다.
**★ 이 연결은 k3d가 관리하지 않으므로 클러스터를 재생성하면 다시 붙여야 한다.**

### 10.4 SSE 통과가 실측으로 확인됐다 — §108의 미검증 항목 종료

`configuration-snippet`이 ingress-nginx 1.9+에서 기본 비활성이라 admission webhook이 install
자체를 거부했다. 따라서 §108이 실측한 SSE 지시자는 **전용 애노테이션 3개**로 나른다:
`proxy-buffering: off`, `proxy-read-timeout: 3600`, `proxy-send-timeout: 3600`.

**2홉(외부 프록시 → ingress-nginx) 전부 통과 실측:**

```
HTTP/2 200 · content-type: text/event-stream · cache-control: no-cache
라이브 푸시 지연:  유발 → 프레임 도착 = 25ms, 32ms
```

버퍼링이 걸려 있으면 프레임은 버퍼가 찰 때까지 갇혀 몰려 도착한다. 이 값은 그럴 수 없다.

측정 방법의 함정도 기록한다: 이벤트를 **발행하지 않는** 요청을 유발로 쓰면 스트림이 조용하고,
그 조용함을 통과로 오독할 수 있다. 유발 동작이 실제로 발행하는지 먼저 확인해야 한다.

### 10.5 이 문서 범위에서 새로 세워지는 항목

- **ConfigMap/Secret 값 변경은 파드를 롤아웃시키지 않는다** — neo4j·omniroute·nats에서 **세 번**
  반복됐다. 단발 사고가 아니라 이 차트의 구조적 성질이므로 checksum 애노테이션 패턴이 필요하다.
- **compose `command:` ≠ k8s `command:`** — compose는 CMD(ENTRYPOINT 인자), k8s는 ENTRYPOINT
  자체를 덮어쓴다. 번역 시 대체로 `args:`가 맞다. 틀리면 **로그 한 줄 없이** 죽는다.
- **compose `start_period` ↔ k8s `startupProbe`** — compose healthcheck는 죽이지 않지만
  k8s liveness는 죽인다. 느리게 뜨는 것에는 startupProbe가 필요하다.
- **§7.2의 미결이 이제 활성 에러다**: NATS ACL 동적 재계산 경로가 k8s에 없어 orchestrator가
  15초마다 `write nats authorization.conf: no such file or directory`를 낸다. §7.2가 남긴
  두 선택(누가 ConfigMap을 patch하는가 / reload를 어떻게 트리거하는가)이 여기서 결판난다.
  **권고: operator가 ConfigMap을 patch하고 reload는 파일 변경 감지에 맡긴다**(orchestrator가
  이미 `natsauth.go`를 소유하므로 compose 배선을 그대로 옮기는 쪽. 사이드카 reloader는
  움직이는 부품을 늘린다). `rbac-operator.yaml`에 ConfigMap patch 권한 추가가 필요하다.
- **K8이 아직 구현이 아니다**: §4가 master key를 K8s Secret이라 서술하지만 실제로는 **PVC
  파일**에 산다(`masterkey.Boot`가 파일 IO). 게이트웨이 토큰·manage key도 같다. 서술과 구현이
  어긋나 있음을 여기 명시한다 — 함께 Secret으로 옮기는 것이 별도 phase다.
- **genesisboot(W1–W5)가 kube 모드에서 미구현 스텁이다.** K7이 TokenReview로 신원 보증을
  대체했으므로, §8.5의 순서 보증(create→issue→write→start)을 k8s에서 무엇이 대신하는지
  §4.2의 W1–W5 대응표를 재확인해야 한다.
