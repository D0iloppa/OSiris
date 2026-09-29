# OSiris 재기준선 (Rebaseline)

> 작성일: 2026-08-12
> 입력: `README.md`, `docker-compose.yml`, `docs/design/10-core-architecture.md`,
> `20-ontology-schema.md`, `30-onboarding-provisioning.md`, `31-onboarding-boundary.md`,
> `70-kubernetes-deployment.md`, `IMPLEMENTATION-NOTES.md`(전체, 특히 §75·§108·§109),
> 저장소 전수(코드 실측: LOC, 테스트 파일, Dockerfile, nginx conf, 스크립트).
> 범위: **재기준선 지도 한 장** — 무엇이 되어 있고, 무엇이 미비하고, 무엇을 변경하거나
> 폐기하는가. 새 설계(액션 레이어·역량 스택)는 이 문서의 범위가 아니다 — §1 참고.
> 비범위: 신규 기능의 상세 설계, 코드 변경.
>
> **표기 규칙**: 표기 없는 서술 = 코드/문서 실측으로 확인된 사실. `[미검증]` = 확인하지
> 못한 가정. `[갭]` = 이 감사에서 발견한, 현재 아무 문서/코드에도 답이 없는 공백.
> **인용은 파일:줄번호** — 이 문서는 살아 있는 코드를 인용하므로 §번호 체계가 없다.
> 코드가 바뀌면 인용도 썩는다는 것을 감수한다(설계 문서 간 인용은 `70` 서문의 원칙대로
> §번호를 쓴다).

---

## 1. 이 문서의 자리

OSiris는 지금 "처음부터 다시 세운다"는 태도로 재기준선을 잡는 중이다. 이 문서는 그 재기준선의
**지도**다 — 다른 세션이 이 저장소를 처음 열었을 때, 설계 문서 11개(`00`~`70`)와
`IMPLEMENTATION-NOTES.md`(5,910줄)를 처음부터 다 읽지 않고도 "지금 여기서 시작하면 된다"고
판단할 수 있는 한 장짜리 진입점이 되는 것이 목적이다.

이 문서가 다루지 않는 것 둘은 형제 워커의 몫이다:

- **`80-action-layer.md`** — 액션 레이어(승인 게이트·외부 영향 액션 실행)의 새 설계.
- **`81-capability-stacks.md`** — 역량 스택(에이전트가 프로젝트별로 갖추는 도구·권한 묶음)의
  새 설계.

이 문서는 그 둘이 "어떤 기반 위에" 놓이는지 — 현재 무엇이 진짜로 동작하고, 무엇이 설계만
있고 코드가 없고, 무엇을 폐기해야 그 기반이 깨끗해지는지 — 를 확정한다. §2~§5가 인벤토리·
괴리·검증 표면·폐기 목록이고, §6~§7이 이미 결정된 두 개의 구체적 전환(OmniRoute npm 이행,
k8s 정본화)이며, §8~§9가 "지금 서 있는 자리"와 "잃으면 비싼 것"이다.

---

## 2. 현행 인벤토리

LOC는 `find <dir> -name '*.go' | xargs wc -l`(Go) / `find console/src -name '*.ts*' | xargs wc -l`
(TS/TSX) 실측이며, 공백·주석 포함 원시 라인 수다.

| 컴포넌트 | 스택 | 크기 | 상태 | 근거(file:line) | 소유 설계 문서 |
|---|---|---|---|---|---|
| `osiris-gw` | nginx | 설정 파일 | **구현됨** — `[2026-07-28 K2 전환]`으로 **폐기 예정**(K3, §7) | `gw/conf.d/default.conf` | `10` §2.3, `70` §2 K3 |
| `neo4j`(컨테이너명 `osiris-neo4j`) | Neo4j | 스키마만 | **구현됨(제약/인덱스만)** — 카탈로그 시드는 `osiris-core`가 별도 소유 | `neo4j/` 초기화 스크립트, `core/internal/graphdb/seed.go`(493줄) | `20` 전체 |
| `nats`(컨테이너명 `osiris-nats`) | NATS JetStream | 설정 파일 | **구현됨** — 인증/ACL 부분 도입 | `nats/generated/authorization.conf`(gitignore, orchestrator가 매 reconcile에 재생성), `nats/generated/authorization.conf.example`(템플릿) | `10` §7 D14 |
| `osiris-egress` | squid | 설정 파일 | **구현됨, 실측 검증됨** | `bin/verify-isolation.sh`(342줄)가 이 컴포넌트를 직접 검증 | `10` §2.6, §8.3 |
| `osiris-omniroute` | Next.js(벤더 이미지 재빌드) | Dockerfile 1개 | **구현됨** — 소스 재빌드 방식이 npm 릴리스 방식으로 전환 결정됨(§6) | `omniroute-build/Dockerfile` | `10` §9, `31` |
| `osiris-core` | Go | 5,043줄 | **부분 구현** | `find core -name '*.go' \| xargs wc -l` | `10` §1.7 G7/D17, `20` |
| `osiris-orchestrator` | Go | 7,408줄 | **부분 구현, 가장 성숙** — k8s 드라이버 + 테스트 보유 | `orchestrator/internal/driver/kube/driver.go` + `driver_test.go`(333줄) | `10` §5, `70` §2 K9 |
| `osiris-api` | Go | 8,391줄 — **최대 컴포넌트** | **부분 구현** | `find api -name '*.go' \| xargs wc -l` | `10` §3, `30`, `31`, `60` |
| `console` | React/TS | 2,360줄 | **부분 구현, 목(mock) 우세** — 온보딩 화면 1개만 실동, 그 외 7개는 `<Placeholder>` | `console/src/pages/{ChatPage,OrgPage,OpsPage,ApprovalsPage,WorkPage,MyTasksPage,OntologyPage}.tsx` 7개 파일 | `40`, `41` |
| `agent`(supervisor) | Go | 3,644줄 | **부분 구현, 실제 `claude` 프로세스 기동** — 테스트 0개 | `agent/internal/supervisor/task.go:207`(`exec.Command(s.claudeBin, args...)`) | `10` §5 |
| `deploy/osiris/`(Helm chart) | Helm | 매니페스트 20개 | **구현됨, compose 경로에서 한 번도 실행되지 않음** | `templates/` 19개 파일(`_helpers.tpl` 포함, 그중 매니페스트 18개) + `crds/agent.yaml` 1개 | `70` 전체 |

**정정**: 브리핑은 Helm chart를 "22 templates including a CRD"로 적었다. 실측 결과
`deploy/osiris/templates/`에는 파일 19개(헬퍼 1개 + 매니페스트 18개)가 있고 `crds/`에
`agent.yaml` 1개가 별도로 있다 — 매니페스트 성격의 파일 총 20개, 그 외 `Chart.yaml`·
`values.yaml`·`README.md`를 더하면 디렉토리 전체 파일 23개다. 22라는 숫자는 어느 셈법으로도
나오지 않아 이 문서가 실측치로 교체한다.

콘솔 LOC도 정정한다: 브리핑의 "~2,723 LOC"는 `.ts`/`.tsx` 실측(2,360줄)과 다르다 — 아마
`.css`/`.json` 등을 포함한 셈법 차이로 보이나 확인하지 못했다. 이 문서는 소스 코드(`.ts`/`.tsx`)
실측치 2,360을 쓴다.

---

## 3. 설계-코드 괴리

| # | 괴리 | 근거 | 판정 |
|---|---|---|---|
| G-1 | `README.md`의 서비스 상태표가 `osiris-api`/`osiris-core`/`osiris-orchestrator`를 "**미구현**, compose 하단에 주석으로만 존재"라고 적고 있다 | `README.md`(70줄), 실제로는 세 컴포넌트 합 20,842줄이 존재하고 `docker-compose.yml`에 정식 서비스 정의로 올라 있다(주석이 아니다, `docker-compose.yml:265` 등) | **문서가 틀렸다.** README를 갱신해야 한다 — 이 문서의 범위 밖이지만 다음 워커에게 명시적으로 인계 |
| G-2 | 브리핑은 `console/src/api/onboarding.ts`의 `USE_MOCK_API`가 **기본 `true`**라고 적었다 | `console/src/api/config.ts:18` — `export const USE_MOCK_API: boolean = (import.meta.env.VITE_API_MODE ?? 'real') === 'mock'`. 주석(`config.ts:2-4`)이 명시: "**2026-07-27부터 기본값은 real**". `console/Dockerfile:47`도 `ARG VITE_API_MODE=real` | **브리핑이 틀렸다 — 이 문서가 정정한다.** 기본값은 **real**이다. 다만 실동은 온보딩 0단계 한 화면뿐이고, 나머지 7개 페이지가 `<Placeholder>`라는 점(§2)은 브리핑대로 사실이다 — "목 우세"라는 결론 자체는 유지되지만 원인이 `USE_MOCK_API` 기본값이 아니라 **미구현 페이지 자체**다 |
| G-3 | `POST /onboarding/commit`(`30` §5.2의 15단계 트랜잭션)이 코드 어디에도 없다 | `api/internal/onboarding/handlers.go`에 `commit` 핸들러 없음(grep 무결과). `core/internal/genesis/genesis_test.go`의 `TestCommitIsNotInTheAllowlist`가 이를 **의도적 부재**로 잠근다(F14 해소, `IMPLEMENTATION-NOTES.md:4722-4729`) | **의도적 갭.** `10` §8.5의 스코프 표에서 `commit`을 명시적으로 제거했다(F14) — "아직 안 만듦"이 아니라 "허용목록에 없어서 호출 자체가 거부됨"이 현재 설계 의도다. `70` §8 미결 목록도 이를 "배포 방식과 무관하게 남는 최대 미구현 항목"으로 이미 인계해 두었다 |
| G-4 | `GET /onboarding/status`의 `phase`가 하드코딩 `"NotStarted"` | `api/internal/onboarding/handlers.go:418`, 주석(`:406-409`)이 이유를 명시: "phase and the conversational (1단계) parts of `org` are (see IMPLEMENTATION-NOTES.md)" — 즉 `commit`이 없으므로 `NotStarted` 이후 상태로 전이할 방법이 아예 없다 | G-3의 직접 결과. 별개 버그가 아니라 같은 갭의 증상 |
| G-5 | `20-ontology-schema.md` §8.2의 `assign(task, asOf)`(작업 배정 엔진)가 코드에 없다 | `core/`·`orchestrator/`·`api/`에서 "assign"으로 찾히는 함수는 전부 **조직 역할 배정**(`AssignOrgRole`, `core/internal/api/server.go:440`, `core/internal/graphdb/writeops.go:1010`, `api/internal/corewrite/corewrite.go:148`)이다. `20` §8.2가 정의한 **WBS 작업 ↔ 사람/에이전트 매칭 알고리즘**(`candidateSet`, `relaxed := assign(...)` 재귀 등, `20-ontology-schema.md:625-766`)에 대응하는 코드는 없다 | **정정**: 브리핑은 이를 "`10 §8`"로 인용했으나 `10-core-architecture.md` §8은 "권한·자격증명·격리"이지 배정 엔진이 아니다. 정확한 근거는 **`20-ontology-schema.md` §8.2~§8.3**이다. 실체(코드 부재)는 브리핑대로 맞다 — 인용만 이 문서가 정정한다 |
| G-6 | 온보딩 0단계는 genesis 창(`10` §8.5)이 아니라 별도의 정적 관리 설정 토큰(`state/creds/admin/setup_token`)으로 게이트된다 | `IMPLEMENTATION-NOTES.md` §75(2102-2126줄) — genesis 토큰의 핵심 성질(W1: "조직이 생기는 순간 영구히 닫힌다")이 0단계가 요구하는 재실행성(프로바이더 키 회전 등)과 정면 충돌하기 때문에 의도적으로 분리했다 | **의도적 설계 결정**이며 코드 주석(`api/internal/adminauth/token.go` 패키지 doc)에도 명시되어 있다 — 갭이 아니라 §8.5의 정정 사례. `70` §7.2가 이 메커니즘의 k8s 재배선을 다루지 않으므로, k8s 이행 시 이 토큰의 저장 위치(현재 호스트 파일)를 Secret으로 옮기는 작업이 `70`에 빠져 있다는 점은 **[갭]**으로 새로 기록한다 |
| G-7 | 온보딩 0단계 완주 도중 `docker compose up -d osiris-orchestrator` 수동 재기동이 필요하다 | `30-onboarding-provisioning.md:244` — 게이트웨이 토큰 발급 응답에 `needsOrchestratorRestart:true`와 `restartCommand`가 그대로 실려 있고, "1단계 알파에서는 이 재기동이 수동"이라고 명시 | k8s 이행(§7)이 이 수동 개입을 자동화할지 여부가 `70`에 명시되지 않았다 — **[갭]** |
| G-8 | 콘솔 로그인(조직 생성 이후 사람이 세션을 얻는 구체적 메커니즘)이 설계 어디에도 없다 | `IMPLEMENTATION-NOTES.md` §76(2128-2146줄) — `10` §8.4는 쿠키의 **산출물** 형태만 규정하고 발급 절차가 없음을 코드 검색까지 마친 뒤 확인. `20`의 `:Person` 스키마에도 credential 필드 없음 | **다음 워커가 먼저 설계해야 하는 항목으로 이미 명시적으로 인계됨**(§76 자신이 그렇게 적어 두었다). 이 문서는 그 인계를 재확인만 한다 |
| G-9 `[2026-08-13 신설]` | **에이전트 개인 기억(자매 프로젝트 Blueurban AaaM D35의 마인드맵 방식)과 평가(evaluation) 축이 OSiris에 대응하는 자리가 전혀 없다** | 감독이 대조 확인. 두 축 모두 흡수 가치는 있으나 OSiris 스키마·아키텍처에 대응하는 개념 자체가 없어 이번 개정(2026-08-13)에서는 문서화하지 않았다 | **미결 검토 항목 — 별도 세션으로 넘긴다.** 새 설계가 필요하며, 이 문서는 그 필요성만 한 줄로 기록한다 |

---

## 4. 검증 표면

**Go 테스트 파일 6개, 합계 1,636줄**:

| 파일 | 줄 수 | 대상 |
|---|---|---|
| `core/internal/genesis/genesis_test.go` | 223 | genesis 스코프 허용목록(K7 이후 TokenReview 기반) |
| `core/internal/onboarding/draft_test.go` | 206 | 온보딩 초안 |
| `orchestrator/internal/operator/operator_test.go` | 381 | Agent CR operator |
| `orchestrator/internal/genesisboot/genesisboot_test.go` | 367 | genesis 부트스트랩 순서(§7에서 폐기 예정) |
| `orchestrator/internal/driver/kube/driver_test.go` | 333 | k8s 워크로드 드라이버 |
| `api/internal/onboarding/handlers_arraycontract_test.go` | 126 | 온보딩 핸들러 계약 |

**정직한 0-커버리지 목록** — 테스트 파일이 전혀 없는 표면:

- `agent/` 전체(3,644줄, 실제 `claude` 프로세스를 기동하는 컴포넌트)
- `console/` 전체(2,360줄)
- `orchestrator/internal/driver/docker/`(compose용 워크로드 드라이버 — `driver/kube`에는 테스트가 있는데 `driver/docker`에는 없다, `orchestrator/internal/driver/docker/driver.go`)
- `api/internal/authn`·`api/internal/authz`·`api/internal/machineauth`·`api/internal/session` — 신원/인가 표면 전체. `find api/internal/{authn,authz,machineauth,session} -name '*_test.go'`가 빈 결과

이 마지막 항목은 `bin/lint-identity-authn.sh`(864줄)로 **휴리스틱하게만** 대체 커버된다 — 정적
텍스트 검사(HI15·HI16·HI17·H10·AUD·GEN 등 6군, `70-kubernetes-deployment.md:227`)이지 단위
테스트가 아니다. 즉 인가 로직의 런타임 동작을 실제로 실행해 검증하는 테스트는 **0개**다.

**`bin/verify-isolation.sh`(342줄)가 실제로 확인하는 것**: `osiris-agents` 네트워크에만 붙은
임시 컨테이너가 (a) Neo4j·dev-net에 도달하지 못하는지, (b) `nats`·`osiris-egress`(허용목록
안)에는 도달하는지, (c) `dev-net`에서 `osiris-egress`를 오픈 프록시로 오용하려는 시도가
거부되는지 — 즉 **네트워크 토폴로지 격리**만 검증하며, 인가 로직·데이터 정합·온보딩 플로우는
범위 밖이다. `70` §6.2가 이미 이 스크립트를 "k8s용으로 재작성 필요, NetworkPolicy·PSP/PSA
검사로 대체"로 폐기 예정 처리했고, 그 후속으로 **`bin/verify-isolation-k8s.sh`(340줄)가 이미
존재한다**(§7.2에서 다시 다룸).

CI·Makefile은 저장소에 없다(`find . -iname 'Makefile' -o -iname '.github'` 무결과, 별도 실측).

---

## 5. 폐기 목록

| 항목 | 근거(file:line) | 사유 |
|---|---|---|
| `docker-compose.yml` | `70-kubernetes-deployment.md:234`(K2) | k8s 단일 배포 대상으로 전면 재작성 결정. §7에서 상세 |
| `osiris-up` | `70-kubernetes-deployment.md:235`(K9), 실측: 파일이 여전히 저장소에 존재 | 컨테이너 생성 주체가 orchestrator/operator로 이관 |
| `orchestrator/internal/driver/docker/`(driver.go) | `70-kubernetes-deployment.md:236` | `driver/kube`(controller-runtime, 테스트 333줄 보유)로 대체 |
| `orchestrator/internal/genesisboot/`(genesisboot.go 366줄 + genesisboot_test.go 367줄) | `70-kubernetes-deployment.md:237` | genesis 발급 시퀀스가 K7(TokenReview 기반)로 재배선되어 순서 보장 테스트 10개의 대상 자체가 사라짐 |
| `bin/verify-isolation.sh`의 15검사(compose 네트워크 전용) | `70-kubernetes-deployment.md:238` | NetworkPolicy·PSP/PSA 검사로 대체 — 후속인 `bin/verify-isolation-k8s.sh`(340줄)가 이미 작성되어 있음(§7.2) |
| `api/internal/masterkey`의 파일 IO 절반 | `70-kubernetes-deployment.md:239`(K8), 실측: `api/internal/masterkey/masterkey.go` 존재 | K8s Secret API가 대체하는 부분만큼 불필요 |
| `omniroute-build/Dockerfile`(소스 재빌드 방식) | §6에서 상세 | npm 릴리스 이미지로 대체 |
| `gw/conf.d/default.conf`의 블랙리스트-접두사 트릭 | §6에서 상세 | 대시보드 비공개화로 서브패스 자체가 불필요해짐 |

**평가만 하고 결론을 유보하는 항목**(브리핑이 "평가하라"고 지정한 것들 — 삭제를 권고하지 않는다):

- **`DESIGN-admin.md`(30KB, 저장소 루트)** — `[2026-08-12 판정 번복]` 이 문서의 초판은 "다른 프로젝트(원본 앱 관리자 콘솔)의 설계 스펙이며 `admin-frontend/`·`frontend/`·`landing/`·`admin-legacy` 등 존재하지 않는 디렉토리를 전제한다"는 근거로 **삭제를 권고했다. 그 근거는 사실이지만 결론이 틀렸다.** 실측 결과 이 파일은 **살아 있는 참조 대상**이다:
  - `41-console-design-system.md`가 약 30곳에서 인용한다. 같은 문서 4행이 *"디자인 표준: `<repo-root>/DESIGN-admin.md`(원본 앱 관리자 콘솔용, 375줄)의 톤 운용 체계를 계승한다. 원본은 사용자 소유이며 이 문서는 그 파일을 **읽기만** 했고 수정하지 않았다"*로 관계를 명시하고, §9에 차이점 표를, 참고문헌에 *"375줄, 읽기 전용 — 톤 운용 체계·컴포넌트 추출 전략의 원본"*을 둔다.
  - 콘솔 소스 3개가 주석에서 참조한다 — `console/src/layout/PageHeader.tsx:7`, `console/src/layout/Topbar.tsx:14`, `console/src/theme/tokens.ts:34`.

  즉 타 프로젝트 문서인 것은 맞으나 OSiris가 그 사실을 알면서 **의도적으로 계승한 조상 문서**이며, 삭제하면 `41`의 인용 30건과 콘솔 토큰 값의 근거가 고아가 된다. 절대경로로 인용되므로 이동도 인용을 깨뜨린다. **판정: 현 위치 보존. 폐기 대상 아님.**
- **`docker-compose.dev-net.yml`**: 자체 문서화가 상세하고(`docker-compose.dev-net.yml:1-24`) 이유가 명확한 **선택적 오버레이**다. compose 경로 자체가 K2로 폐기되므로 이 파일도 compose와 함께 자연 소멸하는 것이 맞다 — 별도의 독자적 폐기 사유는 없다. **판정: compose 폐기에 동봉, 별도 조치 불필요**
- **`nats/generated/authorization.conf` vs `.example` 쌍**: `.gitignore:8`(`nats/generated/authorization.conf`)로 라이브 파일만 제외하고 `.example`은 커밋되는 구조가 의도대로 동작 중이다. `70` §7.2가 k8s에서 이 쌍이 ConfigMap/Secret으로 재배선됨을 이미 기록해 두었다 — **판정: 문제 없음, k8s 이행 시 자연 대체**
- **`console/dist/`**: 디렉토리가 실제로 존재하고(932KB, 파일 5개) 빌드 산출물이 맞지만, `.gitignore:12`(`console/dist/`)가 이미 이를 무시 대상으로 지정해 두었다 — 즉 "체크인되어 있다"는 브리핑의 전제가 **이 저장소 상태(git 없음)에서는 확인 불가**다. `.gitignore` 규칙상 정책은 이미 올바르다. **판정: 정책 문제 없음, 실제 git 이력에서 추적 여부만 다음 워커가 `git ls-files`로 재확인**
- **`state/`**: `.gitignore:2-3`으로 이미 제외되어 있고, 실측 내용물도 빈 디렉토리 골격(`backup/`·`config/`·`creds/` 각각 비어 있음, `.gitkeep` 1개)뿐이다 — 런타임 산출물이되 이 시점에는 실질적으로 비어 있다. **판정: 정책·상태 모두 정상, 폐기 대상 아님**

---

## 6. 전환 1: OmniRoute npm 이행

### 6.1 Before/After

| | **현재 (소스 재빌드)** | **전환 후 (npm 릴리스)** |
|---|---|---|
| 빌드 방식 | 벤더 다이제스트 고정 이미지에서 전체 소스 트리를 꺼내 `next build`를 **다시 실행**(`omniroute-build/Dockerfile:1-27`) | OSiris 소유 이미지가 npm 릴리스 패키지를 그대로 소비 |
| `basePath` | `/omni`를 빌드 타임에 굽는다(`omniroute-build/Dockerfile:63-65`, `ENV OMNIROUTE_BASE_PATH=/omni`) — Next.js `basePath`는 컴파일 타임 상수라 런타임 env로 안 됨(실측, `omniroute-build/Dockerfile:4-8`) | **루트 모드**(basePath 없음) |
| 대시보드 노출 | `osiris-gw`가 `/omni/`로 공개 서빙(`gw/conf.d/default.conf:18-234`, 블랙리스트-접두사 트릭) | **ClusterIP + `kubectl port-forward`** — 공개 안 함, API만 in-cluster 노출 |
| 공급망 신뢰 | 벤더 소스 + 다이제스트 고정 이미지 | npm 태그(버전) 고정 — `postinstall`이 **이미지 빌드 시점**에 실행됨 |

### 6.2 폐기되는 것 3가지

1. **`omniroute-build/Dockerfile`** — 소스 재빌드 자체가 불필요해진다.
2. **basePath 패칭/베더링 전체** — `ENV OMNIROUTE_BASE_PATH=/omni`(`omniroute-build/Dockerfile:65`)와
   그 값이 요구했던 모든 빌드 단계.
3. **`gw/conf.d/default.conf`의 블랙리스트-접두사 트릭** — 실측(`gw/conf.d/default.conf:20-22`,
   "브라우저로 `/omni/`에서 로그인하면 `.../api/auth/login`으로 POST된다 — `/omni/api/auth/login`이
   아니라")로 확인된 대로, omniroute 자신의 하드코딩된 절대 경로 참조를 흡수하기 위해 화이트리스트가
   아니라 **블랙리스트**(`/`만 빼고 나머지 전부 omniroute로) 방식을 채택했던 구조다. 대시보드가
   공개되지 않으면 이 흡수 자체가 필요 없다.

### 6.3 근거

서브패스가 존재했던 유일한 이유는 대시보드를 `/omni/`에 **공개**하기 위해서였다. 지금 채택되는
보안 태세는 "프로바이더 API 키를 쥔 콘솔은 공개 노출되어서는 안 된다"이므로(`gw/conf.d/default.conf:117-119`가
이미 "omniroute 경로 전체를 공개하는 것과 같다"고 스스로 경고해 두었다), 대시보드를 비공개로
돌리는 순간 서브패스의 존재 이유가 소멸한다.

### 6.4 잔여 비용을 정직하게 기록한다

공급망 신뢰의 성질이 바뀐다 — "벤더 소스 + 다이제스트 고정 이미지"(재현 가능, 리뷰 가능한
빌드 로그)에서 "npm 태그 고정"(패키지 레지스트리 신뢰, 업스트림 `postinstall` 스크립트가
이미지 빌드 시점에 실행됨)으로 이동한다. 이는 **명시적으로 받아들이는 대가**이지, 이 문서가
숨기는 리스크가 아니다.

### 6.5 보안 설정 체크리스트 — 자매 배포 대비 현재 상태

`docker-compose.yml:265-316`의 `osiris-omniroute` 서비스 정의를 실측한 결과:

| 설정 | 자매 배포 권고값 | 현재 `docker-compose.yml` | 평가 |
|---|---|---|---|
| `REQUIRE_API_KEY` | `true` | **설정 없음**(env 블록에 항목 자체가 없음) | **[갭]** — 명시적으로 켜져 있지 않다. 기본값이 무엇인지는 `[미검증]` |
| `ALLOW_API_KEY_REVEAL` | `false` | **`"true"`로 명시 설정**(`docker-compose.yml:293`) | **의도적 예외, 브리핑과 정면 배치.** 코드 주석(`:287-292`)이 이유를 밝힌다 — 온보딩이 이미 만든 게이트웨이 키를 idempotent 재적용을 위해 평문 재조회해야 하고, 이 경로는 `/omni/`(omniroute 자체 로그인 뒤) 또는 `osiris-llm`/`osiris-edge` 내부망에서만 닿으므로 `dev-net`/`osiris-agents`로는 노출면이 넓어지지 않는다는 논거. **이 문서는 브리핑의 권고값을 그대로 채택하지 않는다** — 대시보드가 비공개(§6.1)로 바뀌면 이 예외의 근거(내부망 한정)가 한층 더 강해지므로, npm 이행 후에도 `true` 유지가 타당해 보인다. 다만 이 판단은 온보딩 워커의 실제 재조회 필요성을 재확인해야 확정된다 — **[미검증]로 다음 워커에 인계** |
| `MACHINE_ID_SALT` | 배포별 고유값, 기본 `endpoint-proxy-salt` 공유 금지 | **설정 없음** — env 블록에 항목이 없다 | **[갭]** — 기본값을 그대로 쓰고 있을 가능성이 있다. 확인 및 배포별 값 채번 필요 |
| API/WS 바인딩 | `0.0.0.0` 아님 | `docker-compose.yml:265-316`에 `ports:` 매핑 자체가 없음 — 호스트에 노출되지 않고 `osiris-llm`/`osiris-edge` 내부 네트워크에서만 도달 가능 | **사실상 충족** — 명시적 바인딩 설정이 아니라 compose 네트워크 격리로 달성되고 있다. npm 이행/k8s 전환 후에도 ClusterIP로 이 성질이 유지되어야 함(§6.1) |

**백업 단위 — [갭] 확인**: OmniRoute는 `STORAGE_ENCRYPTION_KEY`를 자신의 데이터 디렉토리
안에서 자동 생성한다(브리핑 서술, 이 문서에서 소스 직접 대조는 못함 — `[미검증]`). 이것이
사실이라면 `osiris-omniroute-data` 볼륨(`docker-compose.yml:92,303`) 전체를 백업하지 않으면
복구가 불가능하다. 실측 결과 **이 저장소에는 어떤 백업 메커니즘도 없다** —
`docker-compose.yml`·`bin/*.sh` 전체에서 "backup" 문자열 검색이 무결과이고, `state/backup/`
디렉토리는 존재하지만 완전히 비어 있다(§5). **[갭]로 명시 기록**: `osiris-omniroute-data`
(및 k8s 전환 후 그 PVC 대응물)에 대한 백업 절차가 설계·코드 어디에도 없다.

### 6.6 F15 — omniroute의 egress 강제가 관례라는 사실

`IMPLEMENTATION-NOTES.md` §108.3(4665-4689줄)을 대조해 확인했다:

- `areLocalProviderUrlsAllowed()`가 **기본 ON**이다("OmniRoute is local-first").
- `getProviderValidationGuard()`는 클라우드 메타데이터 엔드포인트만 막는 `block-metadata`를
  반환하고 그 외 내부 호스트는 전부 허용한다.
- 결과: manage 키 보유자가 provider-node를 내부 URL + `proxyEnabled:false`로 등록하면
  `osiris-egress`(squid 허용목록)를 **통째로 우회**해 컨트롤 플레인 내부로 임의 HTTP 요청을
  보낼 수 있다. 실측(§108.3)으로 이 조작이 실제 가능함을 확인했다 — `proxyEnabled:true`인
  커넥션이 내부 호스트로 가려다 502가 난 것이 반증 증거다.

가장 값싼 조치 후보로 이미 식별되어 있는 것: **`OMNIROUTE_ALLOW_LOCAL_PROVIDER_URLS=false`**를
compose(또는 전환 후 Helm values)에 명시해 기본값을 뒤집는다 — OSiris는 로컬 OpenAI 호환
프로바이더를 쓰지 않으므로 부작용이 없다. `IMPLEMENTATION-NOTES.md:4685-4689`가 이 조치를
포함해 3개 후보((a) env 뒤집기 (b) 관리 표면에서 provider-node 생성 자체를 막기 (c) 네트워크
층에서 강제)를 나열했지만 **아직 고르지 않은 상태로 열려 있다**(`4848`: "F15는 손대지 않았다").
이 문서도 고르지 않는다 — 재기준선 이후 첫 착수 후보로 인계한다.

---

## 7. 전환 2: k8s 정본화

`70-kubernetes-deployment.md`의 K1~K13은 이미 확정된 결정이다. 이 절은 그 결정 중 무엇이
**이미 코드로 반영되었는지**와 무엇이 **아직 compose 시절 그대로 남아 있는지**를 실측으로
가른다.

| K결정 | 요지 | 상태 | 근거 |
|---|---|---|---|
| K1 | Helm chart 1개, 이미지 N개 | **완료** | `deploy/osiris/`(§2) 존재, 23개 파일 |
| K2 | compose 경로 폐기, k8s 유일 전제 | **미완료 — compose가 여전히 1급 경로** | `docker-compose.yml`·`osiris-up`이 여전히 저장소에 있고 `README.md`의 "빠른 시작"이 여전히 이를 가리킨다 |
| K3 | `osiris-gw` 폐지, Ingress 단독 진입 | **미완료** | `gw/conf.d/default.conf`가 여전히 활성 설정(§6에서 다룬 블랙리스트 트릭 포함) |
| K4 | 코어 컴포넌트 `nodeAffinity` 고정 | **Helm 템플릿 존재, 실기 미검증** | `deploy/osiris/templates/core-statefulset.yaml` 등 — compose 경로에서 한 번도 실행되지 않았다는 것이 브리핑 전제이자 이 문서가 확인한 사실 |
| K5 | 직원 1명 = Pod + PVC + SA + Secret + NetworkPolicy | **operator 코드 존재** | `orchestrator/internal/operator/operator_test.go`(381줄) — 실기동 검증은 `[미검증]` |
| K6 | 샌드박스 수준 유지, 신규 런타임 도입 안 함 | **정책 결정, 코드 영향 없음** | 변경 대상 없음 |
| K7 | genesis 자격증명을 TokenReview로 검증(자체 발급 안 함) | **완료** | `core/internal/genesis/genesis_test.go`가 K7 전환을 정확히 반영(TestGuardVerifyAcceptsExpectedServiceAccount 등, `genesis_test.go:1-40`) — Store/Issue/Verify 구식 테스트는 삭제됨 |
| K8 | master key를 K8s Secret으로 | **미완료** | `api/internal/masterkey/masterkey.go`가 여전히 파일 IO 기반(§5 폐기 목록) |
| K9 | Agent CRD + operator(orchestrator가 겸임) | **부분 완료** | `deploy/osiris/crds/agent.yaml` + `orchestrator/internal/operator/`(테스트 보유) |
| K10 | Agent CR을 `osiris-agents`에 namespaced로 | **[미검증]** — CRD 스코프를 이 문서에서 직접 대조하지 못함 | `deploy/osiris/crds/agent.yaml` |
| K11 | CNI netpol 실측 | **§8 미결로 이미 명시**(`70-kubernetes-deployment.md:264`) — kube-router netpol은 확인됐으나 default-deny 실기 동작은 `bin/verify-isolation-k8s.sh` 몫 | `70` §8 |
| K12 | 노드 조인 신뢰 모델 | **결정 보류(명시적)** | `70-kubernetes-deployment.md:263` |
| K13 | 전면 재작성, 이관/폐기 목록은 §6 | **부분 진행** — 아래 폐기 항목 참고 | §5 |

**§6.2(70) 폐기 목록의 실측 완료 여부**:

| 폐기 대상 | 완료 여부 |
|---|---|
| `docker-compose.yml` | 아직 존재, 미폐기 |
| `osiris-up` | 아직 존재, 미폐기 |
| `driver/docker` 패키지 | 아직 존재(`orchestrator/internal/driver/docker/driver.go`), 미폐기 — 테스트 0개(§4) |
| `genesisboot`(순서 보장 테스트 10개 포함) | 아직 존재(`orchestrator/internal/genesisboot/`, 366+367줄), 미폐기 |
| `verify-isolation.sh` 15검사 | **원본은 아직 존재**하지만, **후속판 `bin/verify-isolation-k8s.sh`(340줄)가 이미 작성되어 있다** — 둘 다 저장소에 공존 중, 원본 폐기(삭제)는 아직 안 됨 |
| `masterkey` 패키지의 파일 IO 절반 | 아직 그대로, 미폐기 |

**요약**: k8s 정본화는 **설계 문서 층위에서는 완결**(K1~K13 확정)되어 있고, **코드 층위에서는
genesis(K7)와 k8s 드라이버(부분, K5/K9) 정도만 실제로 갈아탔다.** compose 경로(`docker-compose.yml`,
`osiris-up`, `osiris-gw`, `driver/docker`, `genesisboot`, `masterkey` 파일 IO)는 폐기가
**결정**되었을 뿐 **실행**되지 않은 상태로 공존하고 있다. 이것이 재기준선이 상속하는 가장 큰
단일 부채다.

---

## 8. 목표선까지의 거리

설치→온보딩 경로를 순서대로 추적한다.

1. `./osiris-up` → `docker compose up -d` — 인프라 계층(`osiris-gw`·`neo4j`·`nats`·
   `osiris-egress`·`osiris-omniroute`·`osiris-core`·`osiris-orchestrator`·`osiris-api`)이 뜬다.
   **여기까지는 실제로 동작한다**(§2 인벤토리, 각 컴포넌트가 "구현됨" 또는 "부분 구현").
2. 브라우저가 콘솔 온보딩 0단계 화면을 연다 — `console`의 온보딩 관련 코드는 목이 아니라 실제
   `osiris-api`를 호출한다(§3 G-2 정정: `USE_MOCK_API` 기본 `real`).
3. 0단계 자체(OmniRoute admin 비밀번호, 프로바이더 키 등록, 모델 별칭, 게이트웨이 토큰 발급,
   검증)는 **`api/internal/onboarding/handlers.go`에 실제로 구현되어 있다**.
4. 게이트웨이 토큰 발급 단계에서 흐름이 **한 번 끊긴다**: 응답이 `needsOrchestratorRestart:true`와
   함께 `docker compose up -d osiris-orchestrator`라는 수동 명령을 되돌려 준다(`30-onboarding-provisioning.md:244`,
   G-7). 콘솔은 이 명령을 복사 가능한 텍스트로 보여줄 뿐, 자동 실행하지 않는다.
5. 사람이 그 명령을 수동으로 실행하고 0단계 완주까지 도달하면, 이론적으로 다음은
   `POST /onboarding/commit`이어야 한다(`30` §5.2의 15단계 트랜잭션: MERGE 쓰기 + I1~I12 검증 +
   리허설 + `OrgRevision`+1).
6. **여기서 경로가 끝난다.** `commit`은 코드에 존재하지 않고, `core/internal/genesis/genesis_test.go`의
   `TestCommitIsNotInTheAllowlist`가 이를 구조적으로 잠가 둔다(G-3). `GET /onboarding/status`도
   `phase: "NotStarted"`에서 절대 벗어나지 않는다(G-4) — 상태 기계 자체가 다음 상태로 전이할
   길이 없다.

**결론**: 설치는 "인프라가 구성되고, 프로바이더가 등록됨"까지 도달한다. **"조직이 존재하고
에이전트가 그 안에서 동작함"에는 도달하지 못한다.** 이 간극을 메우는 두 가지가 §80(액션 레이어)·
§81(역량 스택)의 전제 조건은 아니지만, 그 두 설계가 실제로 시험될 수 있으려면 최소한 조직 하나가
`commit`을 통해 존재해야 한다 — 즉 **`POST /onboarding/commit` 구현이 이 저장소 전체에서 가장
값이 큰 단일 미구현 항목**이라는 `70` §8의 평가를 이 문서도 그대로 확인한다.

---

## 9. 재구현 시 잃으면 비싼 것

재기준선이 "처음부터 다시 세운다"는 태도를 취하더라도, 아래는 **다시 만들려면 같은 실측·
시행착오를 반복해야 하는** 밀도 높은 지식이다. 코드를 버리더라도 이것들은 버리지 않아야 한다.

- **신원·인가 계층의 설계 밀도**: `60-identity-authn.md`(1,717줄)가 H1~H32로 담아낸 판정
  기준, 특히 H10 술어와 `orgstate` 기반 판정의 독립성(§7 K8 항목 참고). 테스트가 0개인
  표면(§4)이라서 코드만 남기면 **왜 이렇게 만들었는지**가 사라진다 — 문서가 유일한 근거다.
- **`IMPLEMENTATION-NOTES.md`의 실측 발견들**: 특히 §108의 SSE 버퍼링 실측(20초에 걸쳐 분산
  도착한 하트비트로 버퍼링 부재를 증명한 방법론 자체), §108.3의 omniroute 사설 URL 허용
  발견(F15), 그리고 Next.js `basePath`가 런타임 env로 반영되지 않는다는 실측(`omniroute-build/Dockerfile:4-8`)
  — 이런 것들은 코드를 다시 읽어서는 재발견하기 어렵고, 실제로 컨테이너를 띄워 관측해야
  나오는 지식이다.
- **네트워크 격리 모델과 그 검증 방법론**: `10` §2.6의 "분리된 도커 네트워크 → 기본 deny +
  4개 예외" 대응 관계, 그리고 `bin/verify-isolation.sh`(342줄)가 구현한 "임시 컨테이너를
  네트워크에 붙여 도달 가능성을 실측"하는 검증 패턴 자체 — 이 패턴이 k8s용
  `bin/verify-isolation-k8s.sh`(340줄)로 이미 이식되어 있다는 사실도 포함해서.
- **supervisor의 drain/resume 상태 기계**: `agent/internal/supervisor/task.go`가 실제
  `claude` CLI 프로세스를 기동·관리하는 코드(`task.go:207`)이며, `10` §5의 D5~D7(PID 1,
  liveness가 `claude` 실행 여부를 보지 않음, JetStream pull consumer의 long-poll)이 이
  코드의 설계 근거다. 테스트가 0개이므로(§4) 이 상태 기계의 정확한 동작은 **코드 자체가
  유일한 사양**이다 — 재구현 시 이 파일을 먼저 정독하지 않으면 D6(liveness가 40분짜리 턴
  도중 재시작하지 않는다는 보장)을 조용히 깨뜨리기 쉽다.
- **F14 정정의 추론 과정**: `IMPLEMENTATION-NOTES.md` §109.1(4718-4729줄)이 기록한 "허용목록이어야
  하며 금지 목록이면 안 되는 이유"(새 엔드포인트 추가 시 기본이 거부여야 한다는 원칙) — 이
  원칙은 `commit`뿐 아니라 앞으로 추가될 모든 genesis 스코프 엔드포인트에 적용되어야 하는
  일반 원칙이다. 코드(`AllowedRoutes`)만 보면 "왜 화이트리스트인가"가 보이지 않는다.

이 다섯 가지는 공통적으로 **"실측하지 않으면 틀리기 쉬운 함정을 이미 한 번 밟고 기록해 둔
것"**이라는 성질을 갖는다. 재구현이 이 함정들을 다시 밟는 것은 시간 낭비다.
