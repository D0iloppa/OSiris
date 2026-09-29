# OSiris 기술 선행조사 (Prior-Art Research)

> 조사일: 2026-07-26
> 범위: Q1(프레임워크 비교) ~ Q6(온톨로지 표준) + Q2-b(임베디드 컨트롤 플레인 번들링, 배포 모델 전제 정정 반영). 코드 작성 없음, 조사 결과만 정리.
> 표기 규칙: 출처 URL이 붙은 문장은 **확인된 사실**. `[추론]` 표기는 조사자의 추정/판단이며 공식 출처로 직접 확인되지 않음.

---

## 한 페이지 요약

| # | 질문 | 권고 (한 줄) |
|---|------|------|
| Q1 | 선행사례 비교 | 에이전트 프레임워크(CrewAI/AutoGen/LangGraph/OpenAI Agents SDK)는 **전부 탈락** — "LLM 프로세스 내부의 서브에이전트" 모델이라 OSiris의 "독립 k8s Pod = 1 에이전트" 전제와 충돌. **Kubernetes Operator 패턴(reconciliation loop)을 조직 상태 관리의 골격으로, Temporal을 무거운 one-shot 작업의 durable execution 엔진으로 부분 차용**하고, 나머지는 자체 구축. |
| Q2 | k8s 장수 CLI 에이전트 운용 | **StatefulSet(에이전트별 stable identity + PVC) 기반으로 시작하고, 향후 k8s SIG-Apps의 Agent Sandbox(`kubernetes-sigs/agent-sandbox`) CRD로 이관을 염두에 둔다.** Deployment/Job 단독은 부적합. |
| Q2-b | 임베디드 컨트롤 플레인 번들링 | **k3s를 호스트 systemd 서비스로 직접 설치(비컨테이너)하고, `embedded etcd`로 시작해 처음부터 멀티서버 승격 경로를 열어둔다.** kind/k3d 식 "Docker 안에 컨트롤 플레인" 구성은 **프로덕션 비권고** — DinD/특권 컨테이너는 개발·CI 전용으로 명확히 탈락시킨다. PVC는 local-path-provisioner 대신 **Longhorn(또는 클라우드 CSI)**을 처음부터 채택해 노드 종속성 문제를 원천 차단한다. |
| Q3 | Claude Code 헤드리스 제약 | `claude -p`(headless)는 자체 게이트웨이 경유(`ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`), `--resume`/`--continue`를 통한 세션 재개, `--allowedTools`/`--permission-mode`를 공식 지원한다. **단, "상주 supervisor 루프"는 CLI 자체 기능이 아니라 외부에서 폴링·재기동 로직을 감싸야 한다** — 이 부분은 미확인/자체 구현 필요. |
| Q4 | 그래프 DB | **Neo4j Community Edition(단일 인스턴스, GPLv3, 무료)으로 시작**하고, 조직 규모가 커져 HA가 꼭 필요해지면 Enterprise 라이선스 구매를 검토. Apache AGE는 비교 열위(생태계/문서 미성숙)로 백업안으로만 유지. |
| Q5 | 메시지 버스 | **NATS JetStream 채택.** Kafka는 이 규모(에이전트 수십~수백)에 과잉설계, Redis Streams는 멀티노드 HA/영속성이 약함, Postgres LISTEN/NOTIFY는 온톨로지 DB와 결합 유혹이 있으나 payload 8000바이트 제한과 확장성 리스크로 탈락. |
| Q6 | 조직 온톨로지 표준 | **W3C ORG Ontology + FOAF(`foaf:Agent`)를 뼈대로 재사용하고, 업무/실행 이력 추적에는 PROV-O를 결합.** schema.org Organization은 SEO용 경량 스키마라 내부 온톨로지로는 부적합, SKOS는 capability 분류체계(taxonomy)에 보조적으로만 사용. "AI 에이전트=조직 구성원" 직접 선례는 W3C 표준에는 없으나 `foaf:Agent`가 사람/소프트웨어를 이미 동일 상위클래스로 다루므로 확장 여지가 있다. |

---

## Q1. 선행사례 비교

### 비교표

| 프레임워크 | 실행 모델 | 장수 프로세스 지원 | k8s 네이티브 운용 | 사람 노드 혼합 | 결정적 배정 지원 | 판정 |
|---|---|---|---|---|---|---|
| CrewAI | 단일 프로세스 내 역할(Role) 기반 크루, LLM 콜 오케스트레이션 | 없음(요청-응답 실행) | 없음 | 없음 | 없음(LLM이 라우팅) | **탈락** |
| AutoGen(AG2) | 단일 프로세스 내 멀티에이전트 대화 | 없음, MS가 유지보수 모드로 전환 | 없음 | 부분적(human-in-the-loop 콜백) | 없음 | **탈락** |
| LangGraph | 그래프 기반 상태머신, 프로세스 내 실행 | 체크포인터로 상태 저장 가능하나 "프로세스"가 아니라 "그래프 노드" 단위 | 없음(자체는 라이브러리) | 부분적(interrupt/human-in-the-loop) | 없음(그래프 엣지는 코드로 정의하지만 조직 온톨로지 capability 매칭과는 다른 개념) | **탈락(단, 그래프 상태머신이라는 사고방식은 워크플로 설계 시 참고 가능)** |
| OpenAI Swarm/Agents SDK | 핸드오프(handoff) 체인, 단일 프로세스 | 없음 | 없음 | 없음 | 없음 | **탈락** |
| Claude Agent SDK | 훅+서브에이전트, 단일 상위 프로세스가 하위 위임 | 세션 영속(`--resume`)은 있으나 "서브에이전트"가 독립 프로세스/Pod는 아님 | 없음(SDK 자체는 컨테이너 하나 안에서 실행) | 없음 | 없음 | **CLI 실행기(executor)로만 채택 — 오케스트레이션 모델은 미채택** |
| Temporal | 워크플로 엔진, durable execution, 이벤트 소싱 기반 재생(replay) | **있음** — 수개월~수년 지속되는 워크플로, 크래시 후 정확히 재개 | 있음(자체가 k8s에 배포되는 서버 클러스터) | 활동(activity)으로 사람 승인 대기를 모델링 가능 | 결정적 코드 실행 요구(비결정적 호출 금지) — 배정 로직을 Temporal Activity로 구현 시 적합 | **부분 채택 후보 — 무거운 one-shot 작업의 재시도/타임아웃/승인대기에 적합** |
| Argo Workflows | k8s 네이티브 컨테이너 워크플로(DAG), CRD 기반 | 없음(작업 완료 후 종료, CI/CD·배치에 최적화) | **있음(k8s 컨트롤러 자체)** | 없음 | 없음(DAG는 정적 정의) | **탈락(단, k8s 컨트롤러/CRD 패턴 자체는 Q1의 "채택할 부분"에 참고)** |
| Dagster | 데이터 자산(asset) 중심 오케스트레이션 | 없음(배치/파이프라인 지향) | 있음(k8s executor 있음) | 없음 | 없음 | **탈락(도메인이 데이터 파이프라인이지 조직 에이전트 아님)** |
| Kubernetes Operator 패턴 | CRD + reconciliation loop(desired vs actual state) | **개념적으로 적합** — "에이전트가 살아있어야 한다"는 desired state를 지속적으로 reconcile | **네이티브** | 해당 없음(패턴 자체는 사람/에이전트 구분 없음) | 해당 없음(Operator는 배정이 아니라 상태 수렴을 다룸) | **핵심 채택 — Master/조직 상태 관리자를 Operator로 구현** |

### 권고

핵심 질문 — "이 중 무엇을 채택/차용하고 무엇은 안 맞는가" — 에 대한 답:

1. **CrewAI, AutoGen, LangGraph, OpenAI Swarm/Agents SDK, Claude Agent SDK는 오케스트레이션 프레임워크로서 전부 탈락시킨다.** 이들은 공통적으로 "하나의 LLM 호스트 프로세스가 여러 역할/서브에이전트를 오케스트레이션"하는 모델이다. OSiris의 4번 전제("에이전트=독립 1급 프로세스, 서브에이전트 아님")와 근본적으로 상충한다. Claude Agent SDK는 유일하게 "CLI 실행기(런타임)"로서는 그대로 쓴다 — 이는 Q1이 아니라 Q3의 영역이다.
2. **Kubernetes Operator 패턴을 조직 상태 관리(Master의 핵심 로직)의 골격으로 채택한다.** "에이전트 1명 = Pod 1개"라는 설계는 이미 Operator적 사고(CR로 원하는 조직도를 선언하고, 컨트롤러가 실제 Pod/PVC/ServiceAccount를 reconcile)와 정확히 들어맞는다. Argo Workflows도 k8s 네이티브 컨트롤러라는 점은 참고할 가치가 있지만, Argo는 "완료되면 끝나는 워크플로"이므로 상시 조직 상태 관리자로는 맞지 않는다.
3. **Temporal을 "무거운 one-shot 작업"의 durable execution 엔진 후보로 부분 채택한다.** 사람 승인 대기, 며칠씩 걸리는 작업, 재시도가 필요한 작업(전제 5의 "무거운 실작업은 one-shot 프로세스")에 Temporal의 워크플로/액티비티 모델이 정확히 맞는다. 다만 Temporal 자체가 별도 클러스터(DB 포함)를 요구하므로, 초기 단계에는 "k8s Job + 자체 재시도 로직"으로 단순화하고, 조직 규모가 커지면 Temporal 도입을 재검토한다 — 이는 과잉설계 방지 원칙에 따른 것.
4. **Dagster는 도메인 불일치로 완전 탈락.** 데이터 자산 파이프라인 도구이며 조직 내 사람/에이전트 태스크 배정과는 별 관계가 없다.

### 트레이드오프/리스크

- Operator 패턴 채택 시, "에이전트 1개 = CR 1개"의 reconciliation 로직을 직접 작성해야 하는 개발 비용이 발생한다. 기존 프레임워크가 이 부분을 대신 해주지 않는다.
- Temporal 도입은 별도 컴포넌트(Temporal Server + PostgreSQL/Cassandra 백엔드)를 운영해야 하는 부담이 있다 — 초기엔 미도입하고 필요 시 추가하는 것을 권고.
- LangGraph의 "그래프 상태머신"적 사고는 WBS 분해 결과를 표현하는 데 유용할 수 있어, 프레임워크 자체는 안 쓰더라도 설계 아이디어로 참고할 가치가 있다.

### 출처

- [Temporal: Agentic AI Workflows](https://temporal.io/blog/build-resilient-agentic-ai-with-temporal)
- [Temporal Durable Execution for AI](https://temporal.io/blog/durable-execution-meets-ai-why-temporal-is-the-perfect-foundation-for-ai)
- [Temporal vs Argo Workflows — Pipekit](https://pipekit.io/blog/temporal-vs-argo-workflows)
- [Astronomer: Managed Airflow vs Dagster vs Prefect vs Temporal](https://llms.astronomer.io/managed-airflow-vs-dagster-vs-temporal)
- [Composio: Claude Agents SDK vs OpenAI Agents SDK vs Google ADK](https://composio.dev/content/claude-agents-sdk-vs-openai-agents-sdk-vs-google-adk)
- [2026 AI Agent Framework Showdown — QubitTool](https://qubittool.com/blog/ai-agent-framework-comparison-2026)
- [CrewAI vs LangGraph vs AutoGen — Pickaxe](https://pickaxe.co/post/crewai-vs-langgraph-vs-autogen)

---

## Q2. k8s에서 장수 대화형 CLI 에이전트 운용 패턴

### 비교표

| 옵션 | 상태 유지 | 안정적 identity/네트워크 | PVC per-agent | 적합성 |
|---|---|---|---|---|
| Deployment | 없음(무상태 가정) | 없음(재시작 시 identity 리셋) | 어려움(볼륨 공유 전제) | **부적합** |
| Job/CronJob | 일시적, 완료 후 종료 | 없음 | 가능하나 목적과 안 맞음 | **부적합**(무거운 one-shot 실작업에는 Job이 적합 — 전제 5) |
| StatefulSet(size=1 ×N) | **있음** | **있음**(headless Service + 순번 hostname) | **있음**(volumeClaimTemplates) | **가능, 현재로선 실용적 선택** — 단 "운영상 악몽(operational nightmare)"이라는 지적이 있음 |
| (미래) Agent Sandbox CRD (k8s SIG-Apps) | 있음 + suspend/resume(scale-to-zero) | 있음(안정적 hostname) | 자동화 | **방향성은 맞으나 2026년 시점 초기 단계 프로젝트** |

### 권고

**지금 당장은 "에이전트별 StatefulSet(size=1) + PVC(volumeClaimTemplates) + headless Service" 조합으로 구현**하되, `kubernetes-sigs/agent-sandbox` 프로젝트(2026-03 Kubernetes 블로그에 소개됨)의 발전을 추적하여 성숙하면 이관한다. 이는 k8s 공식 블로그가 정확히 우리가 겪을 문제("StatefulSet of size 1 + headless Service + PVC per agent 조합은 이론적으로 가능하나 규모가 커지면 운영상 악몽이 된다")를 지적하고 있기 때문에, 초기 규모(팀 4개, 에이전트 수십)에서는 실용적이지만 확장 시 재검토가 필요한 결정임을 명시한다.

**probe 매핑**: liveness probe는 "supervisor 루프(인박스 폴링)가 죽지 않았는가"를 확인하는 별도의 헬스체크 엔드포인트(sidecar 또는 supervisor 자체의 경량 HTTP 상태 서버)로 구현해야 한다 — Claude Code CLI 자체에는 헬스체크용 HTTP 엔드포인트가 없으므로(Q3 참고) **supervisor 프로세스가 이를 감싸야 한다 [추론, 미확인]**. readiness probe는 "인박스 폴링 준비 완료(예: MCP 서버 연결, 자격증명 로드 완료)" 시점을 기준으로 한다 — 이 역시 자체 구현 필요.

**graceful shutdown**: `preStop` 훅 + 충분한 `terminationGracePeriodSeconds`로 "작업 중 종료 방지"를 구현한다. Claude Code CLI는 SIGTERM 수신 시 진행 중인 turn을 중단하고 SessionEnd 훅을 실행한 뒤 종료 코드 143으로 종료하는 것이 공식 문서로 확인된다. 따라서 preStop 훅에서 "현재 태스크 완료 대기 또는 체크포인트 저장 신호"를 supervisor에 보내고, grace period를 작업 특성(무거운 one-shot 작업은 수분~수십분 단위)에 맞게 넉넉히 설정해야 한다.

### 근거

- k8s 공식 블로그가 명시적으로 "Deployment는 무상태용, Job은 일시적용이라 장수 에이전트에 부적합하며, StatefulSet+headless Service+PVC 조합은 이론적으로 가능하나 대규모 운영 시 악몽"이라고 밝힘.
- Agent Sandbox는 "AI 에이전트는 몇 시간씩 유휴 상태로 있다가 짧게 활동하는 stateful singleton 워크로드"라는 정확히 OSiris의 사람/에이전트 supervisor 루프 모델과 일치하는 문제의식에서 출발.
- Claude Code의 SIGTERM 처리(in-progress turn abort, SessionEnd hook, exit code 143)는 공식 문서로 확인됨.

### 트레이드오프/리스크

- StatefulSet 방식은 수백 개 에이전트로 확장 시 관리 복잡도가 급증한다(k8s 공식 문서가 인정하는 리스크).
- Agent Sandbox CRD는 2026년 시점 SIG Apps에서 개발 중인 신생 프로젝트로, 프로덕션 성숙도·실제 채택 사례가 아직 부족하다 — 지금 이걸 전면 채택하는 것은 리스크가 크다. StatefulSet으로 시작 후 마이그레이션 경로를 열어두는 것을 권고.
- 콜드스타트(신규 Pod 기동 시 약 1초 오버헤드로 보고됨)가 "유휴 후 재개" 흐름의 연속성을 깨뜨릴 수 있음 — supervisor 루프를 상주시키는 하이브리드 생명주기(전제 5)가 바로 이 문제에 대한 대응이라는 점에서 설계가 방향은 맞다.
- liveness/readiness probe와 preStop 훅 설계는 Claude Code CLI 자체가 제공하는 기능이 아니라 **전부 자체 구현이 필요** — 이 부분은 미확인/설계 과제로 남긴다.

### 출처

- [Kubernetes Blog: Running Agents on Kubernetes with Agent Sandbox (2026-03-20)](https://kubernetes.io/blog/2026/03/20/running-agents-on-kubernetes-with-agent-sandbox/)
- [kubernetes-sigs/agent-sandbox (GitHub)](https://github.com/kubernetes-sigs/agent-sandbox)
- [CNCF: Decoding the pod termination lifecycle in Kubernetes](https://www.cncf.io/blog/2024/12/19/decoding-the-pod-termination-lifecycle-in-kubernetes-a-comprehensive-guide/)
- [Claude Code Docs: Run Claude Code programmatically (SIGTERM/SessionEnd 동작)](https://code.claude.com/docs/en/headless)
- [devopscube: Kubernetes Pod Graceful Shutdown with SIGTERM & preStop Hooks](https://devopscube.com/kubernetes-pod-graceful-shutdown/)

---

## Q2-b. 임베디드 컨트롤 플레인 번들링 패턴

> **전제 정정 반영**: OSiris는 기존 k8s 클러스터에 올라타는 앱이 아니라, **k8s 컨트롤 플레인 자체를 품고 배포되는 자족적(self-contained) 런타임**이다. 한 대의 호스트에서 부트스트랩하면 컨트롤 플레인이 함께 올라오고, 이후 워커 노드를 join시켜 그대로 멀티노드로 확장되어야 한다.

### 1. 배포판 선택 비교표

| 항목 | k3s | k0s | MicroK8s | kind / k3d |
|---|---|---|---|---|
| 형태 | 단일 바이너리(<100MB) | 단일 바이너리 | snap 패키지 | 컨테이너 안에 컨트롤 플레인(DinD 방식) |
| 리소스 풋프린트 | 매우 낮음 — agent 노드 512MB RAM, server(컨트롤플레인) 노드 2GB RAM/2 vCPU 권장으로 확인됨 | k3s와 유사하게 낮음, 임베디드 etcd 사용 | 최소 약 540MB, 단 snap 패키징과 애드온 번들로 실사용 리소스는 더 높아지는 경향이 확인됨 | 호스트 Docker 엔진 위에 컨테이너를 얹는 구조라 오버헤드 추가 |
| 임베디드 datastore | 기본 SQLite(단일서버), `--cluster-init` 플래그로 **embedded etcd로 승격 가능** — 이것이 k3s가 공식 지원하는 유일한 데이터스토어 마이그레이션 경로임이 확인됨 | 기본적으로 embedded etcd 사용(k3s와 달리 처음부터 etcd 지향) | 자체 dqlite 기반 HA 애드온 존재 | 해당 없음(단발성 클러스터, 보통 재생성 전제) |
| 노드 join 방식 | `curl \| sh` 설치 스크립트 + `K3S_URL`/`K3S_TOKEN` 환경변수 또는 `--server`/`--token` 플래그로 **토큰 기반 조인**이 공식 확인됨. 서버 노드의 `/var/lib/rancher/k3s/server/node-token` 파일에서 토큰 확인 | 유사한 토큰 기반 조인 제공[추론, 세부 미검증] | snap 기반 `microk8s add-node`/`microk8s join` | 해당 없음(멀티노드는 동일 호스트 내 컨테이너로 시뮬레이션할 뿐 물리적 노드 조인 개념이 아님) |
| upstream 근접도 | 일부 컴포넌트 교체(경량화 목적) | **k0s는 k3s와 달리 컴포넌트 스와핑을 피하고 upstream Kubernetes 동작에 더 가깝다는 점이 확인됨** | 표준에 가까움 + Canonical 애드온 | upstream 그대로(단, Docker 안에서 시뮬레이션) |
| 에어갭 설치 | 지원(에어갭 번들 이미지 제공) | 지원 | 지원 | 해당 없음(개발용) |
| 라이선스 | Apache 2.0, Rancher/SUSE 후원 오픈소스 | Apache 2.0 | Apache 2.0(Canonical) | Apache 2.0(SIG 프로젝트) |
| 프로덕션 적합성 | **프로덕션 사용 사례 다수, 에지·홈랩부터 상용까지** | k0s는 "단일노드 환경에 이상적이며 기본적으로 고가용성을 지원해 데이터 집약적 워크로드에 적합"하다고 평가됨 | 로컬/에지 개발 중심, 프로덕션 사례는 상대적으로 적음 | **개발/CI 전용으로 명시적으로 탈락** (아래 2번 참고) |

### 권고 (배포판)

**k3s를 채택한다.** 이유:
- 단일 바이너리로 호스트에 직접 설치되고, 초기엔 SQLite 단일 서버로 최소 자원(512MB~2GB)에서 기동하되, `--cluster-init` 플래그 하나로 embedded etcd로 승격해 멀티서버 HA로 갈 수 있는 경로가 **공식적으로 지원되는 유일한 마이그레이션**이라는 점이 확인되어 "1노드 → N노드 무중단 확장" 요구(Q2-b 4번)에 가장 직접적으로 부합한다.
- Rancher/SUSE의 Harvester(베어메탈 HCI 어플라이언스, ISO/iPXE로 설치되며 내부적으로 k3s를 관리 클러스터 기반으로 사용)라는 "제품이 자기 자신의 k8s 클러스터를 품고 배포되는" 실제 선례가 확인되어, OSiris가 지향하는 자족적 런타임 모델과 아키텍처적으로 유사하다.
- k0s도 유력한 대안이지만(upstream에 더 가깝고 처음부터 etcd 지향), k3s 대비 커뮤니티/문서/에지-홈랩 실사용 사례의 폭에서 앞선다는 신호가 다수 검색결과에서 일관되게 나타났다.
- MicroK8s는 snap 패키징 의존성이 배포 대상 호스트 환경(에어갭, 특정 리눅스 배포판)에 제약을 줄 수 있어 "자족적 런타임"이라는 목표에는 k3s만큼 깔끔하지 않다.

### 2. "Docker 컨테이너 안에서 컨트롤 플레인을 돌리는 것"의 실제 타당성 — 직답

**결론: 호스트에 직접(systemd) 설치를 권고하고, 컨테이너 안에 컨트롤 플레인을 넣는 kind/k3d 방식은 프로덕션에서 채택하지 않는다.**

근거:
- kind는 컨트롤 플레인을 Docker 컨테이너 안에서 돌리며, 이 방식은 **시스템 수준 기능(systemd, cgroup)이 완전하지 않은 환경에서 실패할 수 있다**는 것이 공식 kind 문서의 "Known Issues"에서 확인된다. 특히 cgroup 네임스페이스 기능이 없는 구버전 커널에서는 아예 동작하지 않을 수 있다.
- k3d(k3s를 Docker 컨테이너로 감싼 것)는 k3s가 systemd에 상대적으로 덜 의존해 kind보다는 제약이 적지만, **"k3s 컨테이너를 특권(privileged) 모드로 돌려야 하는데, 특권 모드는 노드 레벨 리소스 제한(cgroup limit)을 무력화한다"는 결정적 한계가 확인**되었다 — 이는 OSiris가 요구하는 "에이전트별 resource limit"(전제 4)과 정면으로 충돌한다: 컨트롤 플레인 자체가 특권 컨테이너로 도는 순간, 그 컨테이너가 관리하는 워크로드의 리소스 제한 보장이 흔들릴 수 있다는 뜻이다.
- 특권 컨테이너는 "Docker가 컨테이너에 호스트 네이티브 프로세스에 준하는 전체 권한을 부여하므로 비특권 운용이 더 안전하다"는 것이 보안 문헌에서도 일반적으로 확인되는 사실이다.
- 결정적으로, kind/k3d는 사실상 모든 공식·커뮤니티 문서에서 **로컬 개발·CI 테스트 목적**으로 설명되며, "물리적 격리환경을 하나 만들고 그 안에 k8s를 넣는다"는 사용자의 원래 구상은 **컨트롤 플레인 자체의 관점에서는 안티패턴**이다. (반대로, 컨트롤 플레인이 관리하는 "에이전트 워크로드"는 이미 k8s 자체가 격리 단위이므로 별도 Docker 격리가 불필요하다는 것이 전제 4의 취지이기도 하다 — 즉 "격리는 k8s 레벨에서, 컨트롤 플레인은 호스트에 네이티브로"가 일관된 결론이다.)

### 3. 부트스트랩 UX — 단일 커맨드로 컨트롤 플레인 + 코어 서비스 동시 기동

- k3s는 **`/var/lib/rancher/k3s/server/manifests`에 넣은 YAML을 시작 시점과 파일 변경 시점에 `kubectl apply`처럼 자동 배포**하는 기능을 공식 제공한다. 이는 OSiris 부트스트랩 스크립트가 (a) k3s 설치 스크립트를 실행하고 (b) 그래프 DB(Neo4j Helm), 메시지 버스(NATS Helm), Master 컨트롤러, WebUI의 manifest/Helm 차트를 이 디렉토리에 미리 심어두는 것만으로 "한 번의 설치 커맨드 = 컨트롤 플레인 + OSiris 코어 서비스 전체 기동"을 실현할 수 있음을 의미한다. Helm 차트도 이 메커니즘으로 자동 배포 가능하다는 것이 확인된다(HelmChart 커스텀 리소스 자동 인식).
- **선행 사례**: Harvester(SUSE)는 "부팅 가능한 어플라이언스 이미지"로 배포되며 베어메탈에 ISO/iPXE로 설치하고 자체 k3s 기반 관리 클러스터를 내장한다 — "제품이 곧 클러스터를 자체 포함해 배포된다"는 정확히 동일한 패턴의 실제 상용 선례다. Talos Linux는 "베어메탈에서 1분 이내 쿠버네티스로"를 표방하는 불변(immutable) 리눅스로, API로만 관리되는 선언적 구성이 특징이다 — 다만 이번 조사에서 Talos/Coder/Windmill을 "하나로 묶은 번들 제품"의 직접 사례는 발견하지 못했다(각자 독립 프로젝트).
- Windmill은 자체적으로 "소규모는 Docker Compose 단일 인스턴스, 대규모/프로덕션은 자체 Helm 차트로 k8s 배포"라는 이원 전략을 취하는 것으로 확인되어, **OSiris도 "단일 호스트 데모/소규모 = k3s 단일 서버", "프로덕션 확장 = 멀티서버 embedded etcd"라는 동일한 이원 전략을 하나의 배포판(k3s) 안에서 지원**할 수 있다는 점이 유리하다(Windmill처럼 아예 다른 두 스택을 유지할 필요가 없음).

### 4. 1노드 → N노드 확장 경로 — 실제로 무엇이 문제가 되는가

이 부분이 가장 중요한 결론이다. 두 가지 독립적인 문제가 확인되었다:

**(1) Datastore 승격 문제**: k3s는 기본적으로 SQLite로 단일 서버 컨트롤 플레인을 구성하며, **"SQLite는 다중 서버 클러스터에서 쓸 수 없다"**는 것이 공식적으로 확인된다. 멀티서버(HA)로 가려면 서버 노드의 systemd 유닛에 `--cluster-init` 플래그를 추가하고 재시작해 SQLite를 embedded etcd로 전환해야 하며, 이것이 k3s가 공식 지원하는 유일한 자동 마이그레이션 경로임이 확인된다(그 외 외부 데이터스토어 간 이전은 제3자 백업/복원 도구를 써서 직접 해야 함). **따라서 OSiris 부트스트랩 설계는 "처음부터 `--cluster-init`로 기동"하는 편이, 나중에 SQLite에서 전환하는 것보다 리스크가 적다** — 마이그레이션 자체는 지원되지만 서비스 재시작을 수반하므로, 이왕이면 처음부터 승격 가능한 모드로 시작해두는 편이 무중단 확장에 유리하다는 것이 실무 문서에서 권장된다.

**(2) PVC의 노드 종속성 문제 — 가장 결정적인 리스크**: k3s가 기본 내장하는 **local-path-provisioner는 PV를 노드 로컬 디렉토리로 생성**하며, 이렇게 만들어진 PVC는 **그 노드에 영구히 고정**된다 — 복제도, 스냅샷도, 노드 간 마이그레이션도, 쿼터도 없다는 것이 공식·커뮤니티 문서 모두에서 일관되게 확인된다. 이는 전제 4(에이전트 1명 = Pod 1개 = 고유 PVC)와 정면으로 충돌할 수 있는 지점이다: **에이전트 Pod가 local-path PVC를 쓰는 상태로 단일 노드에서 시작하면, 이후 멀티노드로 확장했을 때 그 에이전트는 원래 노드에 영원히 묶이거나(스케줄링 제약), 노드 장애 시 그 에이전트의 작업공간이 통째로 유실된다.** 해결책으로 확인된 것은 **Longhorn**(k3s 생태계에서 사실상 표준으로 취급되는 복제 분산 블록스토리지, Rancher/SUSE가 만든 프로젝트로 k3s와 같은 계열) 또는 NFS/Rook-Ceph 같은 공유·복제 스토리지로 처음부터 전환하는 것이다.

### 권고 (확장 경로)

**부트스트랩 시점부터 (a) `--cluster-init` 플래그로 embedded etcd를 켜고, (b) local-path-provisioner를 기본 StorageClass에서 제외하고 Longhorn(또는 클라우드 환경이면 해당 CSI 드라이버)을 기본 StorageClass로 지정한다.** 단일 호스트로 시작하더라도 이 두 가지를 처음부터 설정해두면, 워커 노드를 토큰 기반으로 join시키는 것만으로 무중단에 가깝게 멀티노드로 확장할 수 있다 — 반대로 SQLite+local-path로 시작하면 나중에 두 가지 모두 "살아있는 클러스터에서 전환"이라는 훨씬 리스키한 작업이 된다.

### 트레이드오프/리스크

- `--cluster-init`로 처음부터 etcd를 쓰면 SQLite 대비 리소스 사용량이 약간 늘고(2GB 권장 사양은 이미 이를 반영한 수치로 보임), 단일 서버 상태에서도 etcd 운영 지식(백업/복구)이 필요해진다 — "단순한 시작"이라는 이점이 다소 줄어든다.
- Longhorn을 처음부터 도입하면 복제 오버헤드(디스크 I/O, 네트워크)가 local-path 대비 커지고, Longhorn 자체가 관리해야 할 추가 컴포넌트가 된다 — 단일 호스트 데모 환경에서는 다소 무거운 선택일 수 있다. 다만 "나중에 못 바꾼다"는 리스크보다는 낫다는 것이 이번 조사의 결론이다.
- kind/k3d를 프로덕션에서 탈락시킨 것은 명확하지만, **개발자 로컬 환경·CI 파이프라인에서 OSiris 자체를 테스트할 때는 kind/k3d가 여전히 유용한 도구**라는 점은 구분해야 한다 — "프로덕션 배포 모델"과 "개발용 테스트 도구"는 서로 다른 결정이다.
- Talos Linux(불변 리눅스 + API 관리)는 "베어메탈에 자기완결적으로 클러스터를 심는다"는 철학이 OSiris와 잘 맞을 수 있으나, 이번 조사에서는 k3s만큼 깊이 검증하지 못했다 — **향후 "호스트 OS 자체를 무엇으로 할 것인가"를 별도로 조사할 가치가 있다(미해결 쟁점에 추가)**.

### 출처

- [K3s Docs: Architecture](https://docs.k3s.io/architecture)
- [K3s Docs: Cluster Datastore](https://docs.k3s.io/datastore)
- [K3s Docs: High Availability Embedded etcd](https://docs.k3s.io/datastore/ha-embedded)
- [GitHub Discussion: Migrating sqlite to etcd (k3s-io/k3s #9432, #7936)](https://github.com/k3s-io/k3s/discussions/9432)
- [K3s Docs: Volumes and Storage](https://docs.k3s.io/add-ons/storage)
- [robododd: Make Your k3s Local Storage Persistent with Longhorn](https://robododd.com/make-your-k3s-local-storage-persistent-with-longhorn/)
- [kind: Known Issues (systemd/cgroup 제약)](https://kind.sigs.k8s.io/docs/user/known-issues/)
- [k3s-io/k3s Issue #2125 (k3d 특권모드와 cgroup limit 무력화)](https://github.com/k3s-io/k3s/issues/2125)
- [oneuptime: K3s Auto-Deploying Manifests](https://oneuptime.com/blog/post/2026-02-09-k3s-auto-deploy-disconnected-edge/view)
- [K3s Docs: Packaged Components(auto-deploy manifests 메커니즘)](https://docs.k3s.io/installation/packaged-components)
- [SUSE Harvester 공식 문서](https://docs.harvesterhci.io/v1.8/)
- [Siderolabs: Talos Linux](https://www.siderolabs.com/talos-linux)
- [Windmill Docs: Self-host](https://www.windmill.dev/docs/advanced/self_host)
- [Portainer: Comparing Resource Consumption in K0s vs K3s vs Microk8s](https://www.portainer.io/blog/k0s-vs-k3s)
- [Siderolabs: Which Kubernetes is the smallest?](https://www.siderolabs.com/blog/which-kubernetes-is-the-smallest)
- [k3s-io/k3s ADR: agent-join-token](https://github.com/k3s-io/k3s/blob/main/docs/adrs/agent-join-token.md)

---

## Q3. Claude Code CLI 헤드리스 운용의 실제 제약 (공식 문서 확인)

모든 항목은 `code.claude.com/docs` 공식 문서에서 직접 확인했다. 확인 불가 항목은 명시적으로 "미확인"으로 표기한다.

| 항목 | 상태 | 근거 |
|---|---|---|
| `-p`/`--print` 헤드리스 모드 | **확인** — 비대화형 실행, `--output-format text\|json\|stream-json` 지원 | [code.claude.com/docs/en/headless](https://code.claude.com/docs/en/headless) |
| `--bare` 모드 | **확인** — 훅/스킬/플러그인/MCP/CLAUDE.md 자동탐색을 생략, CI·스크립트에 권장, 향후 `-p`의 기본값이 될 예정 | 상동 |
| `ANTHROPIC_BASE_URL` (자체 게이트웨이 경유) | **확인** — API 엔드포인트를 override하여 proxy/게이트웨이로 라우팅 가능. 단, `api.anthropic.com`이 아닌 호스트를 가리키면 MCP tool search와 Remote Control이 기본 비활성화됨(v2.1.196+) | [code.claude.com/docs/en/env-vars](https://code.claude.com/docs/en/env-vars) |
| `ANTHROPIC_AUTH_TOKEN` | **확인** — Authorization 헤더에 `Bearer <값>`으로 커스텀 인증 토큰 전달 가능 (omniroute 같은 자체 게이트웨이 인증에 사용 가능) | 상동 |
| `ANTHROPIC_API_KEY` | **확인** — `-p`(비대화형) 모드에서는 구독 자격증명보다 **항상 우선 적용**됨 | 상동 |
| `--resume` / `--continue` (세션 재개) | **확인** — `--continue`는 현재 디렉토리의 최근 세션 재개, `--resume <session-id>`는 특정 세션 재개. **`claude -p`로 만든 세션은 세션 피커에는 안 뜨지만 session ID로는 재개 가능** (`claude -p --resume <id>`) | [code.claude.com/docs/en/sessions](https://code.claude.com/docs/en/sessions) |
| 세션 재개 시 미복원 항목 | **확인** — `--mcp-config`, `--settings`, `--plugin-dir`, `--fallback-model`, `--add-dir`로 넣은 설정은 재개 시 다시 넘겨줘야 함(자동 복원 안 됨). `plan`/`bypassPermissions` 권한모드는 재개되지 않음 | 상동 |
| 세션 저장 위치/보존기간 | **확인** — 기본 `~/.claude/projects/<project>/<session-id>.jsonl`, `CLAUDE_CONFIG_DIR`로 저장 위치 이전 가능, `cleanupPeriodDays`(settings.json)로 기본 30일 보존기간 조정 가능 | 상동 |
| `CLAUDE_CONFIG_DIR` | **확인**(단, env-vars 페이지 표에는 직접 나열되지 않고 sessions 페이지에서 "저장 위치를 `~/.claude` 밖으로 옮기려면 `CLAUDE_CONFIG_DIR` 환경변수를 설정하라"고 명시) — 컨테이너별로 설정 디렉토리를 분리하는 용도로 공식 지원 | [code.claude.com/docs/en/sessions](https://code.claude.com/docs/en/sessions) |
| `--permission-mode` / `--allowedTools` | **확인** — `--allowedTools "Bash,Read,Edit"`로 개별 도구 사전승인. `--permission-mode dontAsk`(잠금 CI용), `acceptEdits`(파일쓰기+일반 fs 명령 자동승인) 등 permission mode 존재. 우선순위는 deny > allow > ask > defaultMode | [code.claude.com/docs/en/headless](https://code.claude.com/docs/en/headless), [code.claude.com/docs/en/agent-sdk/permissions](https://code.claude.com/docs/en/agent-sdk/permissions) |
| MCP 서버 연결 방식 | **확인** — stdio(로컬 프로세스), HTTP/streamable-http(원격, 권장), SSE(**deprecated**, HTTP로 대체 권장). 프로토콜은 JSON-RPC 2.0 | [code.claude.com/docs/en/mcp](https://code.claude.com/docs/en/mcp) |
| 사용량/비용 훅 | **확인** — `--output-format json` 응답에 `total_cost_usd` 및 모델별 비용 breakdown 포함, 스크립트가 매 호출 비용을 파싱 가능 | [code.claude.com/docs/en/headless](https://code.claude.com/docs/en/headless) |
| 백그라운드 태스크 종료 처리 | **확인** — `claude -p` 실행 중 백그라운드 Bash 태스크는 결과 반환 후 5초 유예 뒤 종료. 백그라운드 서브에이전트는 최대 10분 대기(`CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS`로 조정 가능) | 상동 |
| SIGTERM 처리 | **확인** — SIGTERM 수신 시 진행 중 turn 중단, Bash 프로세스 트리 종료, `SessionEnd` 훅 실행 후 exit code 143 | 상동 |
| stdin 크기 제한 | **확인** — v2.1.128부터 파이프 stdin은 10MB로 제한됨 | 상동 |
| "상주 supervisor 루프"(인박스 폴링) 자체 지원 여부 | **미확인** — Claude Code CLI 자체에는 "인박스를 지속 폴링하는 상주 모드"라는 기능이 없다. `-p` 호출은 1회성 실행이며, 상주 루프는 OSiris가 외부 supervisor 프로세스(예: 별도 경량 데몬)로 감싸서 필요할 때마다 `claude -p --resume <id>`를 호출하는 구조로 직접 구현해야 한다. |
| liveness/헬스체크용 HTTP 엔드포인트 | **미확인** — 공식 문서에서 발견하지 못함. supervisor 쪽에서 자체로 헬스체크 서버를 둬야 함. |

### 권고

**Claude Code CLI(`claude -p --bare`)를 "실행 엔진"으로, 그 위에 얇은 자체 supervisor 프로세스를 씌우는 2계층 구조를 권고한다.** `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`으로 omniroute 게이트웨이 경유가 공식적으로 가능하므로 전제 6은 그대로 실현 가능하다. 세션 재개(`--resume`)와 비용 훅(`total_cost_usd`)이 공식 지원되므로 supervisor는 이를 활용해 세션 ID를 온톨로지 DB에 기록하고 재기동 시 재사용하면 된다.

### 트레이드오프/리스크

- `--bare` 모드는 MCP/CLAUDE.md/훅 자동탐색을 생략하므로, OSiris가 필요로 하는 MCP 서버·CLAUDE.md 컨텍스트를 매 호출 시 명시적 플래그(`--mcp-config`, `--settings` 등)로 다시 주입해야 한다 — 이는 supervisor의 책임이 커진다는 뜻.
- 세션 재개 시 여러 설정(`--mcp-config` 등)이 자동 복원되지 않으므로, supervisor가 "이 에이전트의 표준 설정"을 매번 재구성해서 넘겨야 한다 — 상태를 어디에 얼마나 들고 있을지(온톨로지 DB? 로컬 파일?)는 설계 결정 필요.
- "상주 루프"와 "헬스체크"는 공식 기능이 아니므로 전량 자체 구현 리스크로 남는다.

### 출처

- [Run Claude Code programmatically (Headless mode)](https://code.claude.com/docs/en/headless)
- [Manage sessions](https://code.claude.com/docs/en/sessions)
- [Environment variables](https://code.claude.com/docs/en/env-vars)
- [Connect Claude Code to tools via MCP](https://code.claude.com/docs/en/mcp)
- [Configure permissions (Agent SDK)](https://code.claude.com/docs/en/agent-sdk/permissions)

---

## Q4. 그래프 DB 선택

### 비교표

| 항목 | Neo4j Community | Neo4j Enterprise | PostgreSQL + Apache AGE | Memgraph | ArangoDB | Dgraph |
|---|---|---|---|---|---|---|
| 라이선스 | GPLv3, 무료 | 상용(Neo4j commercial license), 유료 | Apache 2.0(AGE) + PostgreSQL 라이선스, 무료 | 커뮤니티판 무료(BSL 계열), Enterprise 유료[추론-재확인 필요] | ArangoDB Community License(2023-10부터, 상용사용 제한 + 단일클러스터 100GB 제한) | 오픈소스(Apache 2.0 계열) |
| 클러스터링/HA | **미지원**(공식 커뮤니티에서 "Community Edition은 clustering 공식 미지원"이라고 확인) | 지원(Causal Clustering, 최소 3 pod: leader 1 + follower 2) | PostgreSQL 자체의 HA(스트리밍 복제 등) 재사용 가능, AGE 확장 자체의 클러스터 인식은 별도 확인 필요 | 자체 HA 기능 존재[추론] | 자체 클러스터 지원, k8s 통합 존재 | 분산 아키텍처로 수평 확장·HA 기본 지원 |
| k8s 배포 난이도 | 단일 인스턴스는 간단(Helm), Causal Cluster는 EE 필요 | Helm 차트로 멀티노드 Causal Cluster 배포 지원(최소 k8s 1.6+, PV 프로비저너 필요). **공식 k8s Operator는 없음**("자체관리 인스턴스용 Operator 계획 없음"이 커뮤니티에서 확인됨) | 표준 PostgreSQL 배포(Helm/Operator 생태계가 이미 풍부: CloudNativePG 등) + AGE 확장 설치만 추가 | Helm 차트 존재 | Helm 차트/k8s 통합 존재 | k8s 네이티브 지향 설계 |
| 쿼리 표현력(다단 경로탐색·권한상속) | Cypher — **매우 강력**, 다단 경로 탐색·가변 길이 패턴이 표준 | 동일 + 쿼리 트레이싱 등 운영 기능 추가 | openCypher 지원(AGE가 Cypher를 PostgreSQL 위에 구현) — 표현력은 Neo4j에 근접하나 성숙도·최적화는 낮음 | Cypher 지원, 인메모리라 매우 빠름 | AQL(자체 쿼리 언어), 그래프+문서+KV 멀티모델 | GraphQL 유사 문법, JSON/protobuf 응답 |
| 생태계/문서/커뮤니티 | **가장 성숙**(사실상 그래프 DB 표준) | 동일 | 활발히 개발 중이나 Neo4j 대비 미성숙, PostgreSQL 11~18 지원 확인됨(2026 시점 기준 최신 버전 지원 확인) | 중간 | 중간(멀티모델이라 범용성은 있음) | 상대적으로 니치 |
| 비용(초기 규모: 에이전트 수십, 단일 클러스터) | 무료 | 유료(HA 필요 시에만) | 무료 | 무료(커뮤니티판) | 100GB 제한 내 무료 | 무료 |

### 권고

**Neo4j Community Edition을 단일 인스턴스로 채택**하고, 조직 규모가 커지거나 무중단이 필수가 되는 시점에 Neo4j Enterprise(Causal Cluster)로 업그레이드하는 경로를 열어둔다.

### 근거

- OSiris 초기 규모(팀 4개, 에이전트 수십~수백)에서는 단일 인스턴스로 충분하며, Neo4j Community Edition은 "ACID 트랜잭션, Cypher, 프로그래밍 API를 완전 지원하는 fully functional edition"으로 공식 확인된다.
- 온톨로지의 핵심 요구("다단 경로 탐색, 권한 상속")는 Cypher의 가변 길이 경로 패턴(`(a)-[:REPORTS_TO*1..5]->(b)` 류)으로 자연스럽게 표현되며, 이 부분에서 Neo4j/Cypher의 성숙도가 Apache AGE보다 높다.
- 라이선스: Community Edition은 라이선스 비용이 없고(GPLv3), Enterprise는 클러스터링이 필요해질 때만 상용 라이선스 비용을 지불하면 되는 **점진적 확장 경로**가 명확하다.
- Apache AGE는 "PostgreSQL 생태계에 올라탄다"는 인프라 단순화 장점이 있으나(전제 7의 "제대로 된 그래프 DB"라는 요구에 비추어), 공식 문서·커뮤니티 조사에서 "프로덕션 준비도에 대한 구체적 평가"를 찾지 못했고 성숙도가 낮다는 신호(버전 0.x대, incubating 상태였던 이력)가 있다.

### 트레이드오프/리스크

- **탈락 사유 정리**:
  - Apache AGE — 생태계/문서 미성숙, HA 스토리는 PostgreSQL의 것을 그대로 재사용해야 하지만 AGE 확장 자체의 클러스터 인식 동작은 검증이 더 필요함(미확인). "인프라 단순화(팀이 이미 Postgres를 쓴다면 추가 컴포넌트 불필요)"라는 장점은 있으므로 완전 폐기보다는 백업안으로 유지 권고.
  - Memgraph — 인메모리 기반이라 온톨로지처럼 "쓰기보다 읽기 위주지만 영속성이 중요한" 데이터에는 메모리 비용·영속화 전략이 추가 고려사항. 이번 조사에서 라이선스 세부사항을 확실히 검증하지 못함(미확인, 재조사 필요).
  - ArangoDB — 2023년 10월부터 Community License로 전환되며 상용 사용 제한과 100GB 단일클러스터 제한이 생겼다는 점이 확인되어, 향후 성장 시 제약이 될 수 있음.
  - Dgraph — 니치 생태계, 온톨로지 표준 쿼리 표현력(Cypher 계열)과 다른 문법 체계라 SPARQL/Cypher 기반 표준 온톨로지(Q6)와의 정합성이 떨어짐.
- Neo4j Community Edition 자체의 리스크: **공식 클러스터링 미지원**이므로 단일 인스턴스 장애 시 다운타임이 발생한다 — "멀티노드/클라우드" 전제(전제 4)와는 온톨로지 DB 자체가 아니라 에이전트 워크로드 배치 얘기이므로 직접 충돌하지는 않으나, 온톨로지 DB의 가용성이 조직 전체의 SPOF(단일장애점)가 될 수 있다는 점은 설계 단계에서 명시적으로 리스크로 인지해야 한다.
- Neo4j 공식 커뮤니티 확인상 "자체관리형 Neo4j용 k8s Operator는 없다"— Helm 차트로 배포해야 하므로, Operator 기반 운영(Q1/Q2에서 채택한 사고방식)과 온톨로지 DB 배포 방식 사이에는 다른 접근이 필요하다는 점도 유의.

### 출처

- [Neo4j Operations Manual: Introduction (Community vs Enterprise)](https://neo4j.com/docs/operations-manual/current/introduction/)
- [Neo4j Community: Clustering for Community Edition](https://community.neo4j.com/t/clustering-and-load-balancing-for-community-edition/59710)
- [Neo4j FAQ: Enterprise Edition Open Core Licensing](https://neo4j.com/open-core-and-neo4j/)
- [Neo4j-Helm User Guide (Causal Cluster on k8s)](https://alemairebe.github.io/neo4j-helm/user-guide/USER-GUIDE.html)
- [Apache AGE 공식 사이트](https://age.apache.org/)
- [Apache AGE GitHub](https://github.com/apache/age)
- [db-engines: ArangoDB vs Dgraph vs Memgraph](https://db-engines.com/en/system/ArangoDB;Dgraph;Memgraph)
- [Memgraph: ArangoDB vs Memgraph](https://memgraph.com/blog/arangodb-vs-memgraph)

---

## Q5. 메시지 버스

### 비교표

| 항목 | NATS JetStream | Redis Streams | Kafka | Postgres LISTEN/NOTIFY |
|---|---|---|---|---|
| 목적 적합성(에이전트 인박스+팀채널+승인이벤트) | **적합** — 큐 그룹(queue group)으로 인박스별 1:1 소비, 영속 스트림으로 재생 가능 | 적합 — Consumer Group·ACK·PEL(pending entry list)·DLQ 패턴 기본 지원 | 과함 — 초당 수백만 메시지 처리를 위한 설계로, 에이전트 수십~수백 규모에는 오버킬 | 부적합 우려 — payload 8000바이트 제한, 트랜잭션 커밋 시 글로벌 배타 잠금 필요 |
| 운영 복잡도(k8s) | 낮음(경량 서버, 공식 Helm 차트, StatefulSet 기반 클러스터) | 낮음(이미 Redis를 캐시용으로 쓰는 경우 추가 인프라 불필요) | 높음(브로커+주키퍼/KRaft, 파티션 관리 등) | 매우 낮음(이미 있는 Postgres 재사용, 단 온톨로지 DB가 Neo4j라 이 항목엔 해당 안 됨) |
| 영속성/재생(replay) | 있음(JetStream이 Kafka급 영속성 제공한다고 평가됨) | 있음(append-only log) | 있음(가장 강력, 장기보관·리플레이 특화) | 없음(NOTIFY는 구독 시점 이후만 수신, payload도 별도 조회 필요) |
| 리소스 사용량 | 매우 가벼움(base 10~50MB, 부하 시 100~500MB로 보고됨) | 중간(Redis 자체가 인메모리 기반) | 무거움 | 별도 인프라 없음(단 확장성 리스크) |
| 확장성(에이전트 수십~수백 규모) | 충분 | 충분(10K msg/s 미만이면 충분하다는 평가) | 과잉설계 | 커넥션 제한과 공유 잠금으로 인해 노드 확장에 근본적 제약(다른 인스턴스 복제로 우회 불가) |

### 권고

**NATS JetStream을 메시지 버스로 채택한다.**

### 근거

- 용도(에이전트별 인박스, 팀 채널, 승인 이벤트)는 정확히 NATS의 큐 그룹(load-balanced 소비) + 서브젝트 계층(subject hierarchy, 예: `team.alpha.agent.xxx.inbox`, `team.alpha.channel`) 모델과 맞아떨어진다.
- 규모(수십~수백 에이전트)에서 Kafka는 명백한 과잉설계다 — 초당 수백만 메시지용 아키텍처를 관리 부담만 지고 쓰는 셈이다.
- k8s 환경에서 NATS 클러스터는 Redis보다도 가볍고, Kafka보다 압도적으로 운영이 단순하다(전제 4의 "멀티노드/클라우드"에도 잘 맞는 공식 Helm 차트 존재).
- Redis Streams는 유력한 대안이었으나, "이미 Redis를 캐시 등으로 운영 중"이라는 전제가 OSiris에는 없고(현재 스택 미정), 굳이 새로 들여올 거라면 메시징 전용으로 설계된 NATS JetStream이 더 목적에 부합한다.

### 트레이드오프/리스크

- Postgres LISTEN/NOTIFY는 온톨로지 DB(Neo4j 채택)와 별개 스토어이므로 애초에 "이미 있는 DB 재사용" 이점이 없다 — 완전 탈락.
- NATS는 Kafka 대비 장기 보관·초대용량 리플레이가 필요해지면(예: 전체 조직 이력 몇 년치를 이벤트소싱으로 재생) 한계에 부딪힐 수 있다. 다만 현재 요구사항(승인 이벤트, 인박스)에는 오버스펙이 아니다.
- JetStream 클러스터 자체도 프로덕션에서는 최소 3노드 Raft 클러스터가 권장되므로, "완전 무료 공짜"는 아니고 최소한의 운영 리소스는 필요하다.

### 출처

- [NATS Docs: Compare NATS](https://docs.nats.io/nats-concepts/overview/compare-nats)
- [NATS vs Redis vs Kafka: Message Broker Comparison 2026](https://www.index.dev/skill-vs-skill/nats-vs-redis-vs-kafka)
- [DBOS: Postgres LISTEN/NOTIFY Actually Scales(및 한계)](https://www.dbos.dev/blog/postgres-listen-notify-scalability)
- [Stacksync: PostgreSQL LISTEN/NOTIFY의 8000바이트 한계](https://www.stacksync.com/blog/beyond-listen-notify-postgres-request-reply-real-time-sync)
- [NATS JetStream 공식 문서](https://docs.nats.io/nats-concepts/jetstream)

---

## Q6. 조직 온톨로지 선행 표준

### 비교표

| 표준 | 커버 범위 | 재사용 가능성 | AI 에이전트=조직원 모델링 선례 |
|---|---|---|---|
| W3C ORG Ontology(vocab-org) | Organization, FormalOrganization, OrganizationalUnit, Membership, Role, Post, Site, ChangeEvent, OrganizationalCollaboration 등 9개 클래스 + 25개+ 프로퍼티, 조직계층·멤버십·보고체계·이력 | **높음** — "org:memberOf" 관계의 주체가 `foaf:Agent`로 정의되어 있어 사람/조직/소프트웨어 모두 대상이 됨. `org:Role`, `org:Membership`, `org:headOf`(팀장 표현에 바로 활용 가능) | **직접적 선례는 없음**(설계 당시 사람 조직을 염두에 둠) — 그러나 구조 자체가 `foaf:Agent`를 주체로 잡아 확장 가능하게 설계됨 |
| schema.org Organization | Organization, Person, `employee`/`member` 프로퍼티 등 — 검색엔진 리치스니펫용 경량 스키마 | **낮음** — 계층적 보고체계, 세밀한 역할·권한 상속을 표현하는 어휘가 빈약, SEO 목적의 얕은 스키마 | 없음 |
| PROV-O | Entity/Activity/Agent 3대 핵심 클래스, `wasGeneratedBy`, `wasAssociatedWith`, `used` 등 — "누가 무엇을 언제 근거로 했는가" 추적 | **높음** — 업무(WBS 태스크)=Activity, 산출물=Entity, 사람/에이전트=Agent로 그대로 매핑 가능. **최근 연구(PROV-AGENT, arXiv 2508.02866)가 LLM 에이전트의 프롬프트/응답/도구호출까지 PROV-O 확장으로 모델링한 선례 존재** | 있음(PROV-AGENT 확장 논문 수준의 선례) |
| FOAF | Agent(최상위)-Person/Organization/Group(하위클래스), 소셜 관계 | **중간** — `foaf:Agent`가 "사람, 조직, **소프트웨어**"를 모두 포괄하는 최상위 클래스로 공식 정의되어 있음("Agent 클래스는 IM 챗봇처럼 사람이 아닌 소프트웨어에도 쓰인다"는 설명이 스펙에 명시) | **가장 근접한 선례** — FOAF 스펙 자체가 "소프트웨어 봇"을 Agent의 예시로 든다 |
| SKOS | 개념체계(concept scheme), 시소러스형 계층·관련어 표현 | **보조적** — capability 분류체계(예: "코드생성 > 백엔드 > Python"과 같은 역량 taxonomy)에 적합하나 조직도 자체를 표현하는 용도는 아님 | 해당 없음 |

### 권고

**W3C ORG Ontology를 조직도의 뼈대로, FOAF의 `foaf:Agent`를 사람/AI 에이전트 공통 상위 클래스로, PROV-O를 업무 실행 이력(누가 언제 무엇을 근거로 어떤 산출물을 만들었는가) 추적에 결합하여 재사용한다.** SKOS는 capability 분류체계(온톨로지 기반 결정적 배정, 전제 9)에 보조적으로 사용한다. schema.org Organization은 채택하지 않는다.

### 근거

- ORG Ontology는 공식 스펙에서 "가장 단순한 표현은 어떤 개체(foaf:Agent로 표현됨)가 조직에 org:memberOf 관계를 갖는 것"이라고 명시한다 — 즉 표준 자체가 처음부터 "조직 구성원 = Agent"라는 일반화된 개념을 사용하고 있어, 사람과 소프트웨어 에이전트를 같은 `org:Membership` 구조에 넣는 데 표준의 근본 취지를 거스르지 않는다.
- FOAF 스펙은 "Agent 클래스는 Person이라 하기엔 지나치게 특정적인 경우를 위해 존재하며, 예컨대 IM 챗봇 같은 소프트웨어에도 IM 챗ID 같은 속성이 쓰인다"고 공식적으로 밝히고 있어, **사람과 소프트웨어를 같은 상위 클래스로 놓는 것이 FOAF 설계자의 의도에 부합**한다는 근거가 된다.
- PROV-O는 W3C 표준으로 Entity/Activity/Agent라는 3분류가 WBS(업무=Activity)-산출물(Entity)-담당자(Agent, 사람/AI 불문)와 정확히 대응되며, 최근 학계 연구(PROV-AGENT)가 이를 LLM 에이전트 실행 추적으로 확장한 선례가 있어 "바퀴를 새로 만들 필요가 없다"는 근거가 명확하다.
- schema.org Organization은 검색엔진 최적화(SEO)를 위한 얕은 마크업 스키마로, 세밀한 권한 상속·보고체계·역할 표현에 필요한 어휘가 부족하다는 것이 조사에서 확인되었다(별도 표준인 ORG Ontology가 바로 이 공백을 메우기 위해 W3C가 표준화한 것).

### 트레이드오프/리스크

- **정직하게 명시**: "AI 에이전트와 사람을 같은 조직 구성원으로 모델링"하는 **W3C 공식 표준 차원의 직접 선례는 없다.** ORG Ontology와 FOAF 모두 설계 당시 목표는 사람(및 법인)조직이었고, AI 에이전트를 명시적으로 다룬 조항은 없다. 다만 두 표준의 최상위 클래스(`org:memberOf`의 주체, `foaf:Agent`)가 구조적으로 소프트웨어를 배제하지 않고 있어 "확장"은 가능하나 "선례를 그대로 가져다 쓴다"는 것과는 다르다 — 이 갭은 OSiris가 자체적으로 메워야 한다.
- 커뮤니티 프로젝트로 `agentic-patterns/agentic-ai-onto`(GitHub)라는 "Agentic AI Ontology"가 존재하는 것을 확인했으나, 이는 **W3C 표준이 아니라 개별 커뮤니티 프로젝트**이며 성숙도·채택도가 검증되지 않았다 — 참고 자료 수준으로만 취급하고 표준으로 의존하지 않을 것을 권고 `[추론 아님, 존재만 확인, 신뢰도 미검증]`.
- ORG+FOAF+PROV-O를 조합하면 표준 3개를 동시에 매핑해야 하는 설계 부담이 생긴다 — RDF/OWL 기반 트리플이 아니라 Neo4j의 property graph로 이를 매핑할 때 "온톨로지 클래스 개념을 그래프 라벨/관계로 어떻게 투영할지"는 별도 설계 결정이 필요하다(표준들은 원래 RDF/OWL 세계의 것이고, OSiris는 Neo4j를 채택했으므로 직접적인 SPARQL 호환은 포기하고 "개념적 참고"로만 삼는 것이 현실적이다).

### 출처

- [W3C: The Organization Ontology](https://www.w3.org/TR/vocab-org/)
- [Epimorphics: An organization ontology](https://epimorphics.com/public/vocabulary/org.html)
- [W3C: PROV-O: The PROV Ontology](https://www.w3.org/TR/prov-o/)
- [arXiv: PROV-AGENT: Unified Provenance for Tracking AI Agent Interactions](https://arxiv.org/pdf/2508.02866)
- [FOAF Vocabulary Specification 0.99](https://xmlns.com/foaf/spec/)
- [agentic-patterns/agentic-ai-onto (GitHub, 커뮤니티 프로젝트 — 참고용)](https://github.com/agentic-patterns/agentic-ai-onto)

---

## 설계 단계에서 반드시 결정해야 하는 미해결 쟁점

1. **supervisor 루프의 정확한 구현 방식** — Claude Code CLI 자체는 상주 폴링 기능이 없다(Q3 미확인 항목). 이 supervisor를 어떤 언어/런타임으로 작성할지, `claude -p --resume`을 얼마나 자주 호출할지(폴링 주기), 폴링 자체가 토큰을 소모하지 않도록 어떻게 설계할지가 미결이다.
2. **liveness/readiness probe의 구체적 구현** — "에이전트가 살아있다"를 무엇으로 판정할지(supervisor 프로세스 생존? 마지막 인박스 폴링 성공 시각? 세션 파일의 mtime?)가 확정되지 않았다.
3. **Neo4j 단일 인스턴스의 SPOF 리스크 수용 여부** — 전제 4(멀티노드/클라우드)와 Neo4j Community Edition(클러스터링 미지원)이 온톨로지 DB 계층에서는 긴장 관계에 있다. Enterprise 라이선스 비용을 언제 지불할지 임계점(에이전트 수, 가용성 SLA)을 정해야 한다.
4. **Neo4j Helm 배포와 "k8s Operator 패턴 채택"(Q1) 사이의 정합성** — Neo4j 자체관리형에는 공식 Operator가 없다는 점이 확인되었으므로, 온톨로지 DB는 Operator 패턴 바깥에서(Helm으로) 관리하고, 에이전트 Pod들만 자체 Operator로 관리하는 이원 체계가 될 텐데 이 경계를 문서화해야 한다.
5. **Temporal 도입 시점 기준** — "무거운 one-shot 작업"이 정확히 어느 조건(실행시간? 재시도 필요성? 사람 승인 대기 여부?)을 만족하면 단순 k8s Job에서 Temporal 워크플로로 승격시킬지 임계값이 없다.
6. **RDF/OWL 표준(ORG, FOAF, PROV-O)을 Neo4j property graph로 투영하는 매핑 규칙** — 클래스를 노드 라벨로, 프로퍼티를 관계로 옮기는 구체적 스키마(예: `org:Role`을 노드로 할지 관계 속성으로 할지)가 미정이다.
7. **NATS JetStream의 승인 이벤트(approval event) 전달 보장 수준** — 사람 노드에게 알림을 보낼 때 "최소 1회 전달"로 충분한지, 사람이 오프라인일 때의 재전달/타임아웃 정책이 없다.
8. **크로스팀 통신이 반드시 Master를 경유해야 한다는 설계(전제 8)와 NATS 서브젝트 구조의 매핑** — Master 경유를 "NATS 레벨의 라우팅 강제"로 할지 "애플리케이션 레벨의 규약"으로만 둘지(즉 기술적으로는 팀 간 직접 구독이 가능하지만 안 하기로 약속하는 방식)가 미정이며, 후자라면 거버넌스/감사 리스크가 남는다.
9. **omniroute 게이트웨이와 `ANTHROPIC_BASE_URL` 전환 시 MCP tool search/Remote Control 비활성화의 영향 범위** — 공식 문서에 따르면 비공식 호스트로 `ANTHROPIC_BASE_URL`을 돌리면 일부 기능이 꺼진다고 되어 있으나, OSiris가 실제로 그 기능들에 의존하는지 여부를 확인해야 한다.
10. **호스트 OS/베이스 이미지 선정** — k3s를 호스트에 직접(systemd) 설치하기로 했는데, 그 호스트 OS를 범용 리눅스(Ubuntu 등)로 할지 Talos Linux 같은 불변(immutable)·API 관리형 OS로 할지가 미결이다. Talos는 이번 조사에서 k3s만큼 깊이 검증하지 못했다(Q2-b 트레이드오프 참고).
11. **`--cluster-init`(embedded etcd)를 언제부터 켤 것인가** — 데모/단일 사용자 환경까지 포함해 "항상 처음부터 etcd로 시작"할지, 아니면 "정말로 2번째 노드를 join하는 시점에만 전환"할지 정책이 없다. 전자는 리소스 낭비 가능성(2GB 권장 사양), 후자는 실제 마이그레이션 실행이라는 운영 리스크를 감수해야 한다.
12. **기본 StorageClass를 Longhorn으로 강제할 때의 최소 하드웨어 요구사항** — Longhorn은 복제를 위해 노드당 추가 디스크 I/O·네트워크 대역폭을 요구한다. "1대의 저사양 호스트에서도 데모가 돌아가야 한다"는 요구와 상충할 수 있어, 단일 노드 데모 모드에서는 Longhorn의 복제 수(replica count)를 1로 낮추는 등의 절충안을 명시적으로 결정해야 한다.
13. **워커 노드 join의 보안 모델** — k3s의 토큰 기반 join(`K3S_TOKEN`)은 편리하지만 토큰 유출 시 임의의 호스트가 클러스터에 합류할 수 있다. 사람 직원의 개인 기기를 워커 노드로 join시키는 시나리오(전제 3의 "사람도 1급 노드")가 있다면, 이 토큰 배포·회전 정책을 별도로 설계해야 한다.
