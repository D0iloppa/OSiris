# OSiris 외부 시스템 연동 어댑터 아키텍처 (Integration Adapters)

> 작성일: 2026-07-27
> 입력: `10-core-architecture.md` §6.1(D1)·§6.2(`WorkloadDriver`)·§7(메시지 버스, 특히 §7.5)·§8(권한·자격증명·격리)·§9(LLM 게이트웨이 어댑터 경계)·§1.7(G1~G10)·§11.1(A3/A5), `20-ontology-schema.md` §3.4~§3.6·§4·§6, `31-onboarding-boundary.md` §1.3(판정 절차), `40-console-ia.md`(Notifications #9, Approvals #4), `IMPLEMENTATION-NOTES.md`(I5 전체, 특히 §12/§17/§41~§69), Plane API 공개 문서(`developers.plane.so`, 웹 조사), Telegram Bot API 공개 문서(웹 조사).
> 범위: Plane(읽기 전용 미러) · 텔레그램(알림 채널) 두 연동의 어댑터 경계, 공유 배관, 격리·자격증명 모델, 온톨로지 델타 제안. 코드는 작성하지 않는다.
> 비범위: 플러그인 레지스트리·매니페스트 스펙·마켓스토어·동적 로딩(§6에서 이유와 함께 명시적으로 배제), Plane/텔레그램 이외 연동(Slack·Notion 등은 §6의 일반화 경로에만 언급), 온톨로지 스키마 파일 자체 수정(`20-ontology-schema.md`는 다른 워커 소유 — 이 문서는 델타를 **제안**만 한다), 콘솔 화면 상세(`40-console-ia.md`/`41-console-design-system.md` 소유).
>
> **표기 규칙**: 표기 없는 서술 = 이 문서의 확정 설계 결정. `[미검증]` = Plane/텔레그램 API의 구체 사실 중 조사로 확인하지 못한 가정. `[판단]` = 이 문서가 내린 재량적 결단(선택지가 있었고 근거를 들어 하나를 골랐음을 명시).

---

## 0. 결정 요약

| # | 결정 | 한 줄 근거 |
|---|---|---|
| E1 | 확장점은 **미러(projection)**와 **알림(notification)** 둘로 나눈다. 하나의 `IntegrationAdapter` 인터페이스로 덮지 않는다 | Plane(외부에 투영)과 텔레그램(외부로 알림 전달)은 방향과 계약이 다르다. 억지로 합치면 둘 다 쓰지 않는 공통분모만 남는다 |
| E2 | 어댑터는 **에이전트와 동일한 격리 등급**을 받는다 — 별도 컨테이너, 전용 내부망(`osiris-adapters`, 신설), egress 프록시+허용목록, 스코프 토큰, 파일 주입 자격증명 | 향후 마켓스토어를 염두에 두면 어댑터는 신뢰할 수 없는 코드일 수 있다. `osiris-core` 안에서 실행하는 설계는 금지 |
| E3 | 어댑터는 **NATS·Neo4j에 직접 붙지 않는다.** `osiris-core`가 신설하는 **어댑터 대면 REST+SSE 포트(`:8091`)** 하나만 통한다 | D19/D20이 이미 검증한 SSE+seq 재개 패턴을 재사용. 어댑터용 NATS ACL 프로파일이라는 새 개념을 만들지 않아도 됨(Simplicity First) |
| E4 | Plane 미러는 **외부 ID를 Neo4j에 저장하지 않는다.** Plane 쪽에 커스텀 프로퍼티/설명 마커로 `osiris:workItemId`를 심어 라벨 기반으로 재발견한다(`WorkloadDriver`의 Docker 라벨 패턴과 동일 계열) | 외부 ID는 "조직의 의도"가 아니라 투영 메커니즘의 부산물이다. Neo4j에 넣으면 D1을 애매하게 만들고 향후 어댑터마다 스키마 필드가 늘어난다 |
| E5 | Plane 드리프트: **사람이 Plane에서 직접 고치면 다음 재투영 때 조용히 덮어쓴다(재투영 = 진실 재확정). 단, 되돌려쓰기 직전에 그 사실을 감사 로그+콘솔 알림으로 노출한다.** Plane 쪽 쓰기 권한은 온보딩이 "읽기 전용 API 토큰"을 발급하도록 권고하되 강제하지 않는다 | Plane은 진실이 아니므로 그 쪽 수정이 이겨서는 안 된다(D1). 그러나 침묵한 채 덮으면 사람이 "왜 내가 고친 게 없어졌지"를 겪는다 — 감사 가능성으로 보완 |
| E6 | 텔레그램 인바운드: **허용하되 좁게** — 이미 그 사람에게 발신된 특정 레코드(승인/배정)에 대한 구조화 버튼 탭(콜백 데이터)만 받는다. 자유 텍스트는 파싱·실행하지 않는다. 구현 우선순위는 발신이 1순위, 인바운드는 2단계 | "지시 채널로 만들지 마라"는 확정과 배치되지 않는다 — 지시(자유형 WBS 조작)가 아니라 이미 라우팅된 레코드에 대한 이진 확인이며, 기존 승인/완료 API를 재사용할 뿐 새 쓰기 경로가 아니다 |
| E7 | 텔레그램 수신자 매핑은 **기존 스키마(`:Person.notifyChannels`)를 그대로 쓴다.** 온톨로지 델타 불필요 — `telegram:<chat_id>` 형식으로 채운다 | `20-ontology-schema.md` §3.1이 이미 `notifyChannels: string[]`에 `"telegram:..."` 예시를 명시하고 "에스컬레이션 사다리가 순서대로 사용"이라고 적어 두었다. 이 문서가 재발명할 필요가 없다 |
| E8 | 어댑터는 **정적으로 compose에 선언된 프레임워크 서비스**다(에이전트처럼 `WorkloadDriver`가 동적으로 만들지 않는다). `osiris-omniroute`와 같은 층위 — 빈 자격증명으로 기동, 온보딩이 채운다 | 지금 N=2인 확장점에 동적 워크로드 생성 기계장치를 만드는 것은 투기적 일반화. G4의 정신(조직 정보를 compose에 넣지 않는다)과도 무관 — 어댑터는 조직 구성원이 아니다 |
| E9 | 두 어댑터를 **별도 컨테이너 이미지**로 만든다(공용 "어댑터 런타임" 패턴만 공유) | 하나가 뚫려도 다른 하나의 자격증명(텔레그램 봇 토큰 vs Plane 토큰)이 노출되지 않는다 |

---

## 1. 두 확장점 — 배관은 공유하되 계약은 다르다

### 1.1 왜 하나로 묶지 않는가

| | 미러(Plane) | 알림(텔레그램) |
|---|---|---|
| 방향 | 온톨로지 → 외부(투영) | 온톨로지 이벤트 → 사람(전달) |
| 트리거 | WorkItem 변경(온톨로지 쪽 사건) | 승인 대기/배정/에스컬레이션(운영 쪽 사건) |
| 멱등성의 의미 | "같은 WorkItem을 두 번 투영해도 Plane에 중복 이슈가 생기지 않는다" | "같은 알림을 두 번 보내도 사람이 두 번 확인 안 해도 된다"(사실상 무의미 — 전달은 멱등이 아니라 **성공/실패**가 중요) |
| 상태를 갖는가 | 있음(ID 매핑, 마지막 동기화 시각) | 거의 없음(발신 후 결과만 로그) |
| 실패의 의미 | 미러가 낡음(조직은 안 멈춤) | 사람이 못 알아챔(에스컬레이션 사다리의 다음 단이 보완) |
| 인바운드 | 없음(확정) | 좁게 허용(§4.4) |

공통분모로 both를 만족하는 인터페이스를 하나 만들면 `Sync(Entity) error` 한 메서드만 남고, 그 안에서 결국 두 갈래로 분기하게 된다 — 이름만 하나인 두 개의 설계다. **인터페이스는 둘, 배관(lifecycle·자격증명·격리·헬스)만 하나로 통일한다.**

### 1.2 공유 배관

두 어댑터가 예외 없이 지키는 공통 계약:

| 항목 | 내용 |
|---|---|
| **생명주기** | `Installed`(이미지 존재, 컨테이너 기동, 자격증명 없음) → `Configured`(온보딩이 자격증명 주입, 외부 API 도달 확인) → `Active`(정상 동작) → `Suspended`(콘솔에서 끄거나 자격증명 회수). `:AIAgent.desiredState`(`Running`/`Suspended`)와 같은 모양이지만 어댑터는 **`osiris-core`가 아니라 어댑터 자신의 config-reload 루프**가 상태를 갖는다(에이전트처럼 `WorkloadDriver`가 관리하지 않음 — E8) |
| **자격증명 경로** | `/var/run/osiris/creds/<adapter>_token`(0400), G5(파일 주입, env 금지) 그대로 준수. 회전은 온보딩 화면에서 재발급 → orchestrator가 아니라 **어댑터 자신의 config-reload**가 mtime 변화를 감지해 무중단 반영(supervisor의 `watchCreds` 패턴 재사용, §5.2) |
| **격리 등급** | 에이전트와 동일 — 별도 컨테이너, `osiris-adapters`(신설, `internal: true`) 네트워크 하나만, `cap_drop: ALL`/`read_only`/`non-root`/`no-new-privileges`, docker.sock 절대 미마운트 |
| **core API 접근** | `osiris-core:8091`(신설 어댑터 대면 포트) 하나만. Neo4j·NATS 직접 연결 없음(§1.3) |
| **egress 허용목록** | `osiris-egress`가 어댑터별 목적지만 허용: Plane 어댑터는 배포 시 설정된 Plane 인스턴스 도메인(자체 호스팅이면 사이트별 값이므로 온보딩이 채움), 텔레그램 어댑터는 `api.telegram.org`. 그 외 전부 거부 |
| **상태·헬스 노출** | 각 어댑터가 `/livez`(자체 프로세스 생존)·`/healthz`(외부 API 도달성, 무인증, <1s) 노출. `osiris-core`가 이를 폴링해 `GET /integrations/status`로 콘솔에 집계 제공(신규 REST, 콘솔 워커 인계 — §8) |
| **실패 처리** | Plane 실패 = 미러가 낡음(조직은 안 멈춤, §10.2 저하 모드와 같은 철학). 텔레그램 실패 = 그 알림 한 건이 전달 안 됨(§7.5 에스컬레이션 사다리의 다음 단이 안전망 — 어댑터가 자체 재시도 루프를 만들 필요 없음) |

**왜 NATS/Neo4j를 아예 안 주는가 [판단]**: 에이전트는 이미 NATS 사용자·ACL 프로파일이라는 기계장치가 있으니 어댑터에도 같은 걸 하나 더 만들 수도 있었다. 그러나 `osiris-api`가 이미 콘솔을 위해 "그래프/버스를 대신 조회·구독해 REST+SSE로 정규화" 하는 정확히 같은 패턴을 갖고 있다(`40-console-ia.md` E절). 어댑터를 "또 하나의 콘솔 클라이언트"로 취급하면 새 ACL 프로파일 종류를 만들지 않고 기존에 검증된 D19(JetStream seq 기반 재개)·D20(SSE 2홉 요구사항) 설계를 그대로 재사용할 수 있다. 대가는 `osiris-core`가 세 번째 네트워크(`osiris-adapters`)에 다리를 걸치는 것뿐이며, 이는 §2.6의 네트워크 배치표에 이미 있는 "여러 대면 포트를 여러 네트워크에" 패턴(현재도 `osiris-core`는 `osiris-data`+`osiris-agents` 이중 홈이다 — `IMPLEMENTATION-NOTES.md` §12)의 연장이다.

### 1.3 인터페이스 계약 (언어중립, 시그니처 수준)

`WorkloadDriver`(§6.2)가 선례다 — 인터페이스는 순수 값만 주고받고, 구현별 wire 타입은 절대 경계를 넘지 않는다.

```
// 공유 타입 — 두 어댑터가 공통으로 쓰는 core 대면 읽기 모델
type WorkItemSnapshot struct {
    ID, ParentID, Kind, Title    string   // kind: project|deliverable|task|checklistItem
    Status                       string   // 9단계 (20-ontology-schema.md §3.4)
    ProgressPct                  int
    AssigneeID, AssigneeName     string
    AssigneeKind                 string   // Person|AIAgent
    DueAt                        *time.Time
    OrgRevision                  int      // 이 스냅샷이 파생된 시점
}

type NotifiableEvent struct {
    ID, Kind         string   // approval.created | assignment.created | escalation.tier2 | ...
    Severity         string   // info|warning|critical
    RecipientPartyID string
    Summary          string   // 렌더링 전 템플릿 채움 결과(사람이 읽을 문장)
    DeepLink         string   // 콘솔 딥링크(승인 큐/워크아이템 상세)
    EscalationTier   int      // §7.5의 어느 단인지 (0=즉시, 1=15분, 2=4시간, 3=24시간)
}

// ── 확장점 1: 미러(projection) ──────────────────────────────
type ProjectionAdapter interface {
    Configure(ctx, cfg AdapterConfig) error
    // 목록 전체를 훑어 idempotent upsert. 최초 전체투영과 증분 재동기화 둘 다 이 메서드 하나로 처리
    // (증분은 core가 변경분만 담아 호출하고, 최초는 core가 전량을 배치로 나눠 반복 호출 — 어댑터는 차이를 몰라도 됨)
    Sync(ctx, items []WorkItemSnapshot) (SyncResult, error)
    // 외부 시스템에서 라벨/마커로 이미 존재하는 항목을 재발견 (재기동 후 캐시 없이도 동작해야 함, §3.2)
    Reconcile(ctx, knownIDs []string) (DriftReport, error)
    Health(ctx) (Status, error)
}

type SyncResult struct{ Created, Updated, Unchanged, Failed []string }
type DriftReport struct{
    ExternallyModified []string   // 외부에서 값이 바뀐 항목(§3.5 드리프트)
    MissingExternally   []string  // 외부에서 삭제/누락된 항목
}

// ── 확장점 2: 알림(notification) ────────────────────────────
type NotificationAdapter interface {
    Configure(ctx, cfg AdapterConfig) error
    Deliver(ctx, ev NotifiableEvent, recipientRef string) (DeliveryResult, error)  // 1회 시도, 재시도 루프 없음(§4.3)
    // §4.4 — 인바운드(선택 구현, phase 2). core가 이 메서드로 어댑터에게 "이 사람 대상 대기 중인 확인 가능 액션"을 알려주고,
    // 어댑터는 그 액션 목록으로만 콜백 버튼을 구성한다(자유 입력 금지, 텍스트 파싱 없음)
    RegisterAckTargets(ctx, recipientRef string, actions []AckAction) error
    Health(ctx) (Status, error)
}
type AckAction struct{ ActionID, Kind, Label string }  // Kind: approve|reject|mark_done
type DeliveryResult struct{ Delivered bool; Error string; DeliveredAt time.Time }
```

**공통 규칙**: 어느 메서드도 Neo4j 타입·Cypher·NATS 서브젝트 문자열을 인자로 받지 않는다. `osiris-core`가 그래프/버스를 읽어 위 순수 값으로 변환한 뒤에만 어댑터에 넘긴다 — LLM 게이트웨이 어댑터(§9)가 "LLM 호출 자체는 어댑터를 통하지 않는다, 어댑터는 자격증명 수명주기만 담당한다"고 분리한 것과 같은 원리를 반대 방향(외부→내부가 아니라 내부→외부)에 적용한 것이다.

### 1.4 CI 린트 — 경계를 강제할 것인가

**판단: 강제한다.** `WorkloadDriver`의 G3(`github.com/docker/docker` 타입 유출 금지, `bin/lint-driver-boundary.sh`)와 `31-onboarding-boundary.md` §5의 `bin/lint-onboarding-boundary.sh`/`bin/lint-health-invariant.sh`가 이미 "설계 문서의 규칙을 grep 가능한 불변식으로 좁혀 CI가 검사"하는 선례를 두 번 남겼다. 세 번째로 반복해 이 패턴이 이 저장소의 표준임을 굳힌다.

`bin/lint-adapter-boundary.sh`(신설, 명세만 — 구현은 다음 워커):

1. **D1 구조적 검사**: `adapters/**` 패키지 전체에서 Neo4j 드라이버 import(`neo4j-go-driver` 등) 또는 Cypher 리터럴 패턴(` MATCH `, ` MERGE `, ` CREATE ` 대소문자 무관 문자열 리터럴)을 grep. 매치가 있으면 실패 — 어댑터 코드가 그래프를 직접 알 방법이 원천적으로 없어야 한다.
2. **네트워크 배치 검사**: `docker-compose*.yml`에서 `osiris-adapter-*` 서비스 정의를 찾아 `networks:` 목록이 `osiris-adapters`(및 자기 자신의 egress 경유만) 이외의 값을 포함하면 실패. 특히 `osiris-data`/`dev-net`/`osiris-agents` 어느 것도 포함해서는 안 된다.
3. **자격증명 태그 검사**: `31-onboarding-boundary.md` §5.1의 `.env.example` 태그 스키마를 어댑터 변수(`OSIRIS_PLANE_API_TOKEN`, `OSIRIS_TELEGRAM_BOT_TOKEN`)에도 그대로 적용 — `human-facing-secret` 태그가 붙은 변수의 기본값이 비어 있지 않으면 실패(§2.3의 사건 1·3과 같은 재발 방지).
4. **쓰기 표면 검사**: `osiris-core`의 `:8091`(어댑터 대면) 라우터 등록 코드에서, Plane 어댑터 토큰 스코프로 인증되는 핸들러 중 mutating 메서드(POST/PATCH/DELETE, `/approvals/*/decision`·`/assignments/*/complete-report` 제외)가 하나라도 등록되어 있으면 실패 — "미러는 읽기 전용"이 라우팅 테이블 수준에서 강제되는지 확인한다.

CI 통과 조건으로 병합을 막는 지점까지는 다음 워커(구현)의 몫이나, 검사 내용 자체는 이 문서가 정의한다.

---

## 2. 불변식

### 2.1 D1 — 두 번째 writer가 되지 않는다

E3(전용 REST+SSE 포트)와 §1.4의 린트 3번이 이를 구조적으로 강제한다. 추가로:

- Plane 어댑터의 core 대면 토큰은 **읽기 스코프만** 발급된다(§8.1의 grant-catalog에 `adapter.read`라는 새 grant id를 추가 제안 — 네트워크/RBAC 없음, Claude Code 층 해당 없음, 대신 "어댑터 대면 API 라우터의 인증 미들웨어가 이 스코프로는 mutating 핸들러를 아예 매칭시키지 않는다"는 코드 규칙).
- 텔레그램 어댑터의 core 대면 토큰은 `adapter.read` + `adapter.ack`(신규 grant — 오직 `POST /approvals/{id}/decision`과 `POST /wbs/{taskId}/complete-report` 두 개의 **이미 존재하는** 콘솔 API와 동일한 핸들러만 호출 가능. 새 그래프 조작 경로가 아니라 기존 단일 관문의 새로운 호출자일 뿐이다).
- 두 토큰 모두 임의 Cypher는 물론, 그래프-쓰기 API의 다른 어떤 엔드포인트도 부를 수 없다(A5, D17 그대로 유지).

### 2.2 격리 — 에이전트와 동등, 그러나 이유는 다르다

에이전트는 "LLM이 생성한 임의 코드를 실행하는 신뢰 경계 안쪽의 프로세스"라서 격리한다(§8.3). 어댑터는 LLM을 실행하지 않지만 **"우리가 통제하지 않는 제3자 코드"**(향후 마켓스토어를 가정하면 특히)라는 점에서 같은 위협 모델을 공유한다. 그래서 등급은 같지만 근거 문구는 구분해 기록해 둔다 — 나중에 "왜 어댑터까지 이렇게 격리하나"를 묻는 사람에게 "에이전트라서가 아니라 신뢰할 수 없는 실행 코드라서"라고 답할 수 있어야 한다.

네트워크 배치표에 추가되는 행 (§2.6 표 형식 계승):

| 컴포넌트 | dev-net | osiris-edge | osiris-data | osiris-agents | osiris-adapters(신설) | 근거 |
|---|:---:|:---:|:---:|:---:|:---:|---|
| `osiris-core` | | | ✅ | ✅ | ✅ | 세 번째 대면 포트(`:8091`, 어댑터 전용) 추가 — 기존 이중 홈(`IMPLEMENTATION-NOTES.md` §12)의 연장 |
| `osiris-egress` | ✅ | | | ✅ | ✅ | 어댑터의 아웃바운드(Plane/텔레그램 API)도 A3 예외 없이 프록시 경유 |
| `osiris-adapter-plane`(신설) | | | | | ✅ | **단 하나의 네트워크.** neo4j·nats·orchestrator·dev-net 도달 불가 |
| `osiris-adapter-telegram`(신설) | | | | | ✅ | 위와 동일 |

D10("`dev-net`에 발을 걸치는 OSiris 컴포넌트는 정확히 2개")은 불변이다 — `osiris-adapters`는 내부망이고 dev-net 멤버가 아니다.

### 2.3 자격증명 — `31-onboarding-boundary.md` §1.3 절차 적용

**Plane API 토큰**

| 질문 | 답 |
|---|---|
| Q1 (기동 필수?) | 아니오 — 빈 값으로 기동, 미구성 상태로 healthy |
| Q2 (사람이 화면에서 입력?) | 예 — 대표가 Plane 자신의 설정 화면에서 발급받아 OSiris 온보딩/연동 화면에 붙여넣는다 |
| Q2a (조직에 관한 것?) | 예 — "우리가 Plane의 어느 워크스페이스를 미러링하는가"는 조직 의도 |
| **Q2b (교환 경로 존재?)** | **그렇다 — 다만 사건 4(omniroute 관리자 비밀번호)와는 다른 모양으로 통과한다.** Plane의 개인/워크스페이스 API 토큰은 [미검증, 그러나 Plane API 문서 구조상 타당] 애초부터 계정 로그인 비밀번호가 아니라 **이미 스코프 제한된 API 전용 자격증명**이다(워크스페이스 멤버 단위 발급, 리보크 가능). 즉 "원본(비밀번호)을 교환해서 스코프 제한된 산출물을 얻는" 절차가 필요 없다 — **사람이 화면에서 건네는 값 자체가 이미 Q2b가 요구하는 최종 형태**다. Q2b는 "교환 경로 없음 → 원본을 그대로 저장하되 왜 그런지 명시" 분기가 아니라 "교환이 이미 완료된 값을 받는다"는 분기로 통과한다 |
| 결론 | **온보딩이 결정.** 저장은 파일 주입(`/var/run/osiris/creds/plane_api_token`, 0400) — `.env`에 절대 두지 않는다(사건 1과 동일 원칙) |

**텔레그램 봇 토큰**

| 질문 | 답 |
|---|---|
| Q1 | 아니오 |
| Q2 | 예 — 대표가 텔레그램 `@BotFather`에서 발급받아 붙여넣는다 |
| Q2a | 예 — "이 조직의 알림을 어느 봇이 보내는가" |
| **Q2b** | **그렇다, Plane과 같은 모양으로.** 텔레그램 봇 토큰은 설계상 **딱 그 봇 하나만** 인증하는 단일 목적 자격증명이며, 사람의 텔레그램 계정 자체를 대표하지 않는다(사람 계정 비밀번호와 봇 토큰은 텔레그램 플랫폼이 애초에 분리해 둔 두 가지 다른 개념). "사람이 화면에서 건네는 값이 이미 스코프 제한된 기계 자격증명"이라는 같은 이유로 통과 |
| 결론 | 온보딩이 결정. 파일 주입(`/var/run/osiris/creds/telegram_bot_token`, 0400) |

**개인 텔레그램 chat_id (`:Person.notifyChannels`의 `telegram:<value>`)**

이건 위 두 값과 성질이 다르다 — Q2 관문에서 "이게 자격증명인가?"부터 다시 봐야 한다. chat_id 자체는 **베어러 시크릿이 아니다**(chat_id를 안다고 그 사람을 사칭해 메시지를 보낼 수 있는 것은 봇 토큰을 쥔 쪽뿐이다 — chat_id는 이메일 주소에 가깝다). 그래서 §1.3의 자격증명 하위분기(Q2)를 타지 않고 **Q3(그래프의 일부)**로 바로 간다 — `:Person.notifyChannels`가 이미 그 자리다. E7에서 이미 결론냈듯 이 값은 새 판정이 필요 없다. 다만 그 값을 **어떻게 얻는가**(등록 플로우)는 이 문서가 결정해야 한다: 개인이 봇에게 1회 `/start` → 봇이 1회용 확인 코드를 회신 → 개인이 콘솔의 "내 알림 채널" 화면(신규, My Tasks/프로필 인접)에 그 코드를 입력 → `osiris-api`가 코드-chat_id 대응을 어댑터로부터 확인받아 **일반 프로필 수정 경로(graph-write API)로 `notifyChannels`를 갱신**한다. 이 등록 플로우는 텔레그램 어댑터가 "코드 X가 chat_id Y로 확인됨"이라는 사실을 전달만 할 뿐, Neo4j에 쓰는 것은 여전히 `osiris-core`다 — D1이 개인 설정 갱신에도 예외 없이 적용된다.

---

## 3. Plane 미러 어댑터

### 3.1 매핑표

Plane 조사 결과 [미검증 다수, 아래 표기]: Plane의 네이티브 계층은 워크스페이스 > 프로젝트 > (Epic/Task 2단계 이슈 계층, "Hierarchy" 기능 활성 시 Epic이 Task를 담고 Task가 Epic을 담지 못함)이며, 이슈에 라벨·상태(state)·담당자·커스텀 프로퍼티(work-item-type properties)가 붙는다. OSiris WBS는 4단계(project/deliverable/task/checklistItem)다 — 계층 단수가 다르므로 그대로 1:1이 안 된다.

| OSiris `:WorkItem.kind` | Plane 대상 | 비고 |
|---|---|---|
| `project`(루트) | **Plane Project**(컨테이너) | 온톨로지의 캠페인/프로젝트 1개 = Plane Project 1개. 그 이하부터가 이슈 |
| `deliverable` | **Plane Epic** | Hierarchy 기능이 활성화된 워크스페이스 전제 `[미검증 — 워크스페이스 설정에 따라 Epic 기능 자체가 꺼져 있을 수 있음]`. 꺼져 있으면 일반 이슈로 폴백(제목에 계층 접두어 `[deliverable]` 부여) |
| `task` | **Plane Task(Issue)**, `parent` 필드로 위 Epic에 연결 | Plane의 기본 이슈 단위와 자연 대응 |
| `checklistItem` | **Task 이슈 내부의 서브 항목** — 우선순위: (a) Plane이 이슈 단위 체크리스트/서브태스크 위젯을 지원하면 그것 `[미검증]`, (b) 아니면 이슈 설명(description) 안의 마크다운 체크리스트(`- [ ] 제목`) 라인으로 표현 | `:CompletionCriterion.kind`가 `checklist`인 리프와 개념적으로 가장 가까움(`20-ontology-schema.md` §3.4) |
| `status`(9단계) | Plane의 `state`(카테고리: backlog/unstarted/started/completed/cancelled 등, 워크스페이스별 커스텀 가능) | 번역표는 아래 |
| `progressPct`(롤업) | Epic/Task 표시명 또는 설명 상단에 `[62%]` 접두어로 노출, 혹은 Plane의 진행률 위젯이 있으면 그것 `[미검증]` | Plane 자체 롤업 로직에 맡기지 않는다 — **OSiris의 rollup-engine 계산값을 그대로 밀어넣는다**(진실은 하나, Plane은 그 값을 표시만) |

**status 번역표** [미검증 — Plane state 카테고리명은 워크스페이스별 커스텀 가능하므로 온보딩 시 실제 워크스페이스의 state 목록을 조회해 매핑을 확인해야 함, 아래는 기본 매핑 제안]:

| OSiris `status` | Plane state 카테고리(제안) |
|---|---|
| `planned` | Backlog |
| `assigned` | Unstarted |
| `in_progress` | Started |
| `awaiting_approval` / `awaiting_human` / `awaiting_verification` | Started(세부 구분은 이슈 설명/코멘트에 문장으로 부기 — Plane 기본 state 카테고리가 5종뿐이라 9단계를 전부 별도 state로 만들면 워크스페이스 커스터마이징을 강제하게 됨. 과설계 회피) |
| `done` | Completed |
| `blocked` | Started + 라벨 `blocked` 부여(state는 그대로, 라벨로 신호) |
| `cancelled` | Cancelled |

### 3.2 외부 ID 매핑 — 저장 위치 결정 (E4의 근거)

**결정: Neo4j에 저장하지 않는다.** 대신 Plane 쪽에 OSiris WorkItem ID를 심어 넣고(우선순위: 이슈 커스텀 프로퍼티 `osiris_work_item_id` [미검증 — work-item-type properties API가 인스턴스별 값 설정까지 지원하는지 미확인], 실패 시 대체 경로로 이슈 설명 본문에 HTML 주석 마커 `<!-- osiris:work_item_id=wi-1234 -->` 삽입 — 이 대체 경로는 커스텀 프로퍼티 기능 유무와 무관하게 항상 동작하므로 **주 경로가 아니라 항상 병행하는 이중 보장**으로 둔다), 매 재동기화 시 Plane API로 그 마커를 가진 이슈를 조회해 대응 관계를 **재발견**한다.

**D1 관점의 판단 근거**: 외부 ID는 "조직의 의도"(누가 무엇을 하는가)가 아니라 "이 투영 메커니즘이 우연히 그 값을 그렇게 매겼다"는 사실이다 — `20-ontology-schema.md`의 §6.1 삼분할(의도/물성/관측)로 보면, 외부 ID는 그 어느 층에도 깔끔히 속하지 않는 **투영 부산물**이다. 억지로 Neo4j에 넣으면:
1. WorkItem 스키마에 어댑터별 필드(`planeIssueRef`, 나중엔 `jiraIssueRef`, `notionPageRef`...)가 하나씩 늘어난다 — §6의 일반화 경로가 요구하는 "N=2에서 일반화 압력을 만들지 않는다"는 원칙에 정면으로 위배.
2. 이미 이 저장소에 선례가 있다 — `WorkloadDriver`의 `WorkloadRef`(컨테이너 이름)도 Neo4j에 저장하지 않고 Docker 라벨(`osiris.dev/employee-id`)로 매번 `Observe()`가 재발견한다(§6.2). 라벨 기반 재발견은 이미 검증된 이 코드베이스의 관용구다.

**대가**: 매 재동기화마다 "라벨로 찾기" 질의 1회가 추가된다. 조직 규모(수십~수백 WorkItem)에서는 무시할 만하다. 만약 Plane 쪽 API가 라벨/커스텀 프로퍼티 검색을 지원하지 않는 것으로 확인되면[미검증이 부정으로 판명되는 경우], **차선책으로 어댑터 자신의 로컬 경량 KV(SQLite 등, 컨테이너 볼륨에)에 매핑을 캐시**한다 — 이 경우에도 Neo4j에는 넣지 않는다(어댑터의 사적 상태일 뿐, 유실되어도 재전체투영으로 복구 가능해야 함이 전제).

### 3.3 투영 트리거 — 혼합

| 트리거 | 무엇을 하는가 |
|---|---|
| **주기적 reconcile**(기본 5분, 온보딩에서 조정 가능) | 담당 팀/프로젝트 범위의 WorkItem 스냅샷 전체를 `osiris-core:8091`에서 읽어 `Sync()` 호출. 자기치유(self-healing) 성격 — 어댑터가 재기동해도, core가 잠깐 죽었다 살아나도 결국 수렴 |
| **빠른 경로(event nudge)** | `osiris-core`가 `task.*.event`를 내부에서 구독하고 있다가, 미러 대상 범위의 WorkItem이 바뀌면 어댑터 대면 SSE 스트림에 "이 ID만 지금 다시 동기화해" 이벤트를 얹는다(재사용: D19의 JetStream seq 기반 SSE, 콘솔이 이미 쓰는 것과 같은 인프라). 어댑터는 이 이벤트를 받으면 해당 ID만 즉시 `Sync()` — 사람이 승인·완료 직후 몇 분씩 기다리지 않게 하는 지연 단축용이며, **신뢰하지 않는다**(놓쳐도 다음 주기 reconcile이 잡는다) |

**최초 전체투영 vs 증분의 차이**: 인터페이스 시그니처는 하나(`Sync([]WorkItemSnapshot)`)이므로 어댑터 입장에서 둘은 구분되지 않는다 — 차이는 호출자(`osiris-core`)가 "이번엔 범위 전체를 배치로 나눠 여러 번 부르는가, 변경분 몇 건만 부르는가"뿐이다. 이는 §9.6이 이미 채택한 "선언과 실제가 다를 때만 갱신"(단순 스킵이 아니라 값 대조 후 갱신) 원칙을 그대로 물려받는다 — 매 `Sync` 호출은 이미 존재하는 항목이면 라벨로 찾아 필드가 실제로 다를 때만 Plane API PATCH를 날린다(불필요한 API 호출·Plane 쪽 "수정됨" 타임스탬프 노이즈 방지).

### 3.4 사람·AI를 Plane 사용자에 매핑

Plane 이슈의 "담당자(assignee)" 필드는 Plane 워크스페이스의 실제 멤버 계정을 요구한다. AI 에이전트는 Plane 계정을 갖지 않는다(계정을 만들어 줄 이유가 없다 — Plane은 사람이 조회하는 용도일 뿐, AI가 Plane에 로그인해서 뭔가 할 일이 없다).

**결정**: Plane의 담당자 필드에는 **사람 담당자만** 실제로 매핑한다(그 사람이 Plane 워크스페이스 멤버로 이미 존재해야 함 — 온보딩이 이 대응을 1회 확인). **AI 에이전트가 담당인 WorkItem은 Plane 이슈 담당자를 비워두고, 대신 제목 접두어 또는 라벨(`agent:마케팅-AI`)로 표시**한다. 이유: 억지로 "AI용 Plane 계정"을 만들면 (a) Plane 라이선스/좌석을 소비할 수 있고, (b) 그 가짜 계정이 실수로 Plane 안에서 뭔가 조작할 수 있는 표면을 만든다 — 미러가 읽기 전용이어야 한다는 원칙과도 어긋난다. 라벨은 담당자 필드보다 표현력이 떨어지지만 "누가 보는 용도"(사람이 조회)에는 충분하다.

### 3.5 드리프트 처리 — 결단 (E5)

사람은 반드시 Plane에서 직접 이슈를 고치려 시도한다(제목 오타 수정, 상태를 실수로 옮김, 코멘트 추가 등). 세 가지 대응을 검토했다:

| 안 | 내용 | 판정 |
|---|---|---|
| (a) Plane 쪽 권한을 아예 읽기 전용으로 건다 | 워크스페이스 멤버 권한을 뷰어로 고정 | **보조 수단으로 채택, 단독으로는 불충분.** 온보딩이 "가능하면 이 계정 권한을 뷰어로 설정하라"고 권고하지만, Plane 워크스페이스 관리자 권한이 온보딩에 없을 수 있고(외부 시스템 관리 경계 밖), 대표가 스스로 다른 사람에게 편집 권한을 줄 수도 있다 — **강제 불가능한 권고**일 뿐이다 |
| (b) 사람의 수정을 감지해 온톨로지에 반영(Plane → Neo4j 역흐름) | Plane이 곧 진실이 되는 경로를 만든다 | **기각.** Plane은 읽기 전용 미러라는 사용자 확정을 정면으로 뒤집는다. 이 경로가 열리면 두 번째 writer가 실질적으로 생긴다(D1 위반) |
| (c) **재투영으로 조용히 덮어쓰되, 덮어쓰기 직전에 드리프트를 감지해 감사·알림으로 노출** | reconcile이 Plane 쪽 현재 값과 마지막으로 자신이 쓴 값을 비교(3.3의 "값이 실제로 다를 때만 PATCH" 로직이 이미 이 비교를 하고 있다) → 자신이 마지막으로 쓴 값과 다르면서, **동시에 Neo4j 쪽 값과도 다르면** 그건 "사람이 Plane에서 직접 고침"이다 → 다음 sync에서 Neo4j 값으로 되돌리되, 그 되돌림을 `ops.adapter.plane.drift_overwritten` 이벤트로 발행 | **채택** |

(c)의 세부: `DriftReport`(§1.3 인터페이스)가 "외부에서 값이 바뀐 항목"을 알려주면, `osiris-core`가 이를 `notification.created`(콘솔 알림센터, #9)로 변환해 "Plane에서 '3. 예산안 수립'의 제목이 변경되었으나 온톨로지 값으로 되돌렸습니다 — 실제로 바꾸려면 Admin Chat 또는 Work 보드에서 수정하세요"를 대표에게 보여준다. **왜 그냥 덮지 않고 알리는가**: 사람이 Plane에서 고친 것이 "OSiris가 몰라야 할 사소한 오타"가 아니라 "실제로 반영되어야 할 실질적 수정"일 수도 있기 때문이다 — 침묵한 채 지우면 그 의도가 그냥 사라진다. 알림은 "고쳐야 할 게 있으면 정식 경로(Admin Chat/Work)로 다시 해달라"는 안내로 기능한다. 상태(state) 변경만은 예외적으로 조용히 되돌린다(알림 없이) — 상태는 대체로 사람이 실수로 드래그하는 경우가 잦고, 그 자체가 "지금 안이니 확인해달라"는 신호로 알림을 남기면 노이즈가 크다 — 대신 §7.5의 정상 에스컬레이션 경로가 실제 상태 변화 요구는 이미 커버한다.

**Plane 쪽 코멘트는 미러 대상이 아니다**: 사람이 Plane 이슈에 남긴 코멘트는 읽지도 반영하지도 않는다(그건 Plane 안에서만 사는 부가 대화이지 온톨로지 상태가 아니다). 이걸 명시하지 않으면 "코멘트도 드리프트인가?"라는 질문이 나중에 또 나온다 — 아니다, 코멘트는 애초에 동기화 대상 필드 목록(제목/상태/담당자/진행률/마감일)에 없다.

---

## 4. 텔레그램 알림 어댑터

**이건 새 기능이 아니라 이미 설계된 빈 구멍을 메운다.** §7.5의 에스컬레이션 사다리(즉시→15분→4시간→24시간)와 사람 배정 알림은 지금 콘솔 밖으로 나가는 채널이 **0개**다. 이 절은 그 사다리의 15분·4시간 단계, 그리고 §10.3의 대역외 알림 요구와 겹치지 않는 부분을 실제로 구현 가능하게 만든다.

### 4.1 알림 대상 이벤트

| 이벤트 | §7.5/§10.3과의 관계 | 텔레그램으로 가는가 |
|---|---|---|
| 승인 대기 생성(`approval.created`) | §7.5 즉시 단계는 **콘솔**(알림센터+뱃지)만. 텔레그램은 15분 무응답 시에만 | 조건부(에스컬레이션 15분 단계에서만) |
| 사람 배정(`org.event.member_assigned`류) | 배정 즉시는 콘솔. 사람이 콘솔을 안 보고 있으면 §7.5와 같은 사다리를 태움 | 조건부 |
| 에스컬레이션 15분/4시간/24시간 단계 | §7.5가 이미 "외부 채널(이메일/메신저) 1회"라고 명시한 바로 그 자리 | **예 — 이 어댑터가 그 "메신저" 슬롯을 채운다** |
| 컨트롤플레인 장애(`cluster.control_plane.status_changed`) | §10.3의 watchdog 대역외 알림은 **상위 리버스 프록시도 OSiris 스택도 경유하지 않는** 별도 경로(`/etc/osiris/watchdog.env`)로 이미 확정되어 있다 | **아니오 — 겹치지 않는다.** watchdog의 대역외 알림은 이 어댑터(osiris-core 산하, 스택 안)를 거치지 않는다. 컨트롤플레인이 죽으면 이 어댑터도 함께 죽을 수 있으므로 같은 경로로 취급하면 안 된다. 텔레그램 어댑터는 "조직 운영 중 알림"만 다루고, "플랫폼 생사 알림"은 여전히 watchdog의 몫이다(역할 분리 유지) |

### 4.2 수신자 매핑 — 결단은 이미 §2.3에서 완료

`:Person.notifyChannels`의 `telegram:<chat_id>` 항목을 그대로 쓴다(E7). 등록 플로우는 §2.3에서 정의한 그대로(봇 `/start` → 확인코드 → 콘솔 "내 알림 채널" 화면 → graph-write API 경유 갱신). 배열 순서가 곧 에스컬레이션 단계의 채널 우선순위다(스키마 주석이 이미 그렇게 정의).

**AI 에이전트는 이 매핑의 대상이 아니다** — `:AIAgent`에는 `notifyChannels`가 없다(스키마에 없음, 있을 이유도 없다: 에이전트에게 알림을 보내는 것은 그 에이전트의 인박스(`team.<team>.inbox.<employeeId>`)에 태스크를 넣는 것과 동치이며, 이는 이미 존재하는 경로다).

### 4.3 메시지 렌더링·전달 보장

- **렌더링**: `NotifiableEvent.Summary`는 `osiris-core`가 이미 완성된 문장으로 채워 보낸다(§3.3의 compute-over-inference 표: "대표용 완료보고 요약문"은 LLM 몫이지만, 알림 문장 자체는 **고정 템플릿 채움**이지 LLM 호출이 아니다 — 알림마다 LLM을 부르면 토큰 낭비이고 지연도 생긴다). 어댑터는 그 문장을 텔레그램 메시지 포맷(Markdown/HTML 엔티티 이스케이프)으로만 감싼다.
- **전달 보장 수준**: §7.5가 이미 "사람 대상은 재전달이 아니라 에스컬레이션 사다리"로 결단했다. 이 어댑터는 그 결단과 정합해야 한다 — **`Deliver()` 1회 시도(짧은 네트워크 재시도 2~3회까지는 허용, 하지만 이는 같은 에스컬레이션 단 안에서의 전송 안정화일 뿐 사다리를 대신하는 재전달 루프가 아니다). 실패하면 그대로 실패로 기록하고 다음 에스컬레이션 단(4시간)이 넘겨받는다.** 텔레그램 자체의 전송 실패(예: 사람이 봇을 차단, 네트워크 문제)를 어댑터가 무한 재시도하면 §7.5가 명시적으로 피하려던 "재전달 모델"을 옆문으로 재도입하는 셈이 된다.
- **감사**: 모든 발신 시도(성공/실패)를 `ops.adapter.telegram.delivery`로 기록(`ops.egress` 감사와 같은 패턴, JetStream 30일) — 고빈도는 아니지만 그래프에 넣지 않는다(P6 정신 계승, 사람 알림 이력은 조직 의도가 아니라 운영 로그).

### 4.4 인바운드 허용 여부 — 결단 (E6)

**허용한다, 단 좁게.** 근거를 세 갈래로 정리한다.

1. **"지시 채널로 만들지 마라"는 요구를 어기지 않는가**: 사용자의 확정은 "작업 지시는 내부 챗봇·UI 에서만"이다. "지시"란 WBS를 분해하거나, 태스크를 만들거나, 배정을 바꾸는 **개방형 자연어 명령**을 뜻한다고 읽는 것이 자연스럽다. 이 문서가 허용하려는 것은 그것과 다르다 — **이미 그 사람을 대상으로 발신된, 이미 존재하는 레코드(특정 `:Approval` 또는 특정 `:Assignment`)에 대한 정해진 두 개의 버튼(승인/반려, 완료확인)**뿐이다. 새로운 의도를 만들지 않고, 이미 만들어진 의도를 확정할 뿐이다. `callback_data`(구조화된 고정 값)만 받고 자유 텍스트는 파싱·실행하지 않는다 — 사람이 텔레그램에 아무리 긴 자연어를 보내도 어댑터는 그것을 core에 전달하지 않는다(로그만 남기거나, "지시는 콘솔에서 해주세요"라는 안내 회신만 보낸다).
2. **새로운 인바운드 공개 표면을 만드는가**: 아니오 — 텔레그램 봇 API는 **long-polling**(`getUpdates`)으로도 받을 수 있으므로, 어댑터가 `api.telegram.org`에 아웃바운드로 계속 폴링하는 방식을 쓰면 **새 공개 포트를 열 필요가 없다**(webhook 방식이었다면 `osiris-gw`에 새 인바운드 경로를 열어야 했을 것이고, 이는 §2.9의 고정된 경로 예약 목록과 충돌했을 것이다). 이 판단으로 인바운드를 "새 공격 표면"이 아니라 "기존 egress 파이프라인의 반대쪽 절반"으로 만들 수 있다.
3. **D1을 어기는가**: 아니오 — 버튼 탭이 호출하는 것은 **콘솔이 이미 호출하는 바로 그 두 API**(`POST /approvals/{id}/decision`, `POST /wbs/{taskId}/complete-report`)다. 새 쓰기 경로가 아니라 기존 단일 관문의 새 호출자일 뿐이며, 그 관문 안의 권한 검사(승인자 역할, 담당자 일치)는 호출자가 콘솔이든 텔레그램 어댑터든 동일하게 적용된다 — 텔레그램이라고 검사를 우회하지 않는다.

**우선순위**: 발신(§4.1~§4.3)이 1순위다. §7.5의 사다리에 채널이 하나도 없는 지금의 빈 구멍을 메우는 것이 이 연동의 존재 이유다. 인바운드 버튼 확인은 **2단계로 명시적으로 미룬다** — 없어도 사다리는 완결된다(사람이 콘솔에서 처리하면 그만이다), 있으면 편의가 늘 뿐이다. 이 우선순위 표기 자체가 "과잉설계 금지"(10명 미만 내부 도구) 제약에 대한 이 문서의 응답이다.

**완료보고 사진 첨부는 이번 범위에 넣지 않는다**: My Tasks 화면(#8)의 완료보고는 메모+사진 첨부를 받는다. 텔레그램 메시지도 사진을 받을 수 있어 자연스러운 확장처럼 보이지만, 파일 다운로드(`getFile`)·저장 경로·크기 제한까지 다루면 어댑터가 단순 "확인 버튼 중계자"에서 "파일 업로드 게이트웨이"로 성격이 바뀐다 — 이번 설계 범위 밖으로 명시적으로 남기고, 2단계 인바운드가 자리잡은 뒤 별도 판단 대상으로 인계한다.

---

## 5. 온톨로지 변경 제안 (제안만 — 스키마 파일 미수정)

`20-ontology-schema.md`는 수정하지 않는다. 아래는 감독/온톨로지 담당 워커가 검토할 **제안 목록**이다.

| # | 제안 | 필요성 판단 |
|---|---|---|
| P1 | `:IntegrationAdapter` 노드(신규 라벨) — `id`, `kind`(`projection`/`notification`), `name`(`plane`/`telegram`), `enabled`(bool), `configJson`(비밀 아닌 설정만: Plane 워크스페이스 슬러그, 대상 팀 범위 등), `createdAtRevision` | **필요.** "이 조직이 Plane을 미러링하기로 했다/텔레그램으로 알림받기로 했다"는 조직 의도이지 런타임 관측이 아니다(D1) — 자격증명은 이 노드에 절대 넣지 않는다(파일 주입 경로 그대로). 런타임 캐시 노드(`:AdapterRuntime`류)는 **제안하지 않는다** — 헬스는 §1.2가 이미 REST(`GET /integrations/status`)로 노출하기로 했고, 어댑터가 2개뿐인 지금 별도 그래프 캐시 노드는 과설계다 |
| P2 | `(:WorkItem)-[:REQUESTED_BY]->(:Party)` 관계(신규) — "누가 이 티켓을 요청/발의했는가"(Plane의 "reporter"에 대응) | **필요 검토 권고, 이 문서는 판단 유보.** 감독이 지적한 인접 공백 "작성자·완료자 명시 필드 부재"의 절반(작성자)에 해당. `:Evidence.submittedBy`는 물리 업무의 증빙 제출자일 뿐 WorkItem 자체의 발의자가 아니다. Plane 미러가 "reporter" 필드를 채우려면 이 값이 있어야 하나, 없어도 미러는 동작한다(그 필드를 비워두면 그만) — **미러의 완료 조건이 아니라 품질 향상 항목**으로 분류한다 |
| P3 | "완료자" — **추가 델타 불필요, 기존 `[:PERFORMED_BY]{endedAt}` 관계로 충분**하다고 판단 | `20-ontology-schema.md` §4가 이미 `(:WorkItem)-[:PERFORMED_BY]->(:Party) {startedAt, endedAt}`(prov:wasAssociatedWith)를 정의해 두었다. "누가 언제 끝냈는가"는 이 관계의 최댓값 `endedAt`으로 구할 수 있다 — 새 필드를 또 만들 필요가 없다. **단, 이 관계가 실제로 완료 시점에 채워지는지는 구현 상태 확인이 필요**(이 문서의 범위 밖, `graph-write API` 구현 워커에게 인계) |
| P4 | 상태 전이 이력을 그래프에 넣을 것 | **불필요하다고 판단.** 감독이 지적한 두 번째 인접 공백이지만, Plane 미러 관점에서 "언제 누가 끝냈나"는 JetStream `task.*.event`(`TASKS` 스트림, 90일 보존, §7.2)로 이미 충분히 커버된다 — 미러 어댑터가 이 스트림을 구독해 Plane 이슈의 코멘트/활동 로그로 재투영하면 되며(§3.3의 이벤트 nudge 경로가 이미 이 스트림을 본다), 그래프 스키마 변경이 필요 없다. P6 원칙(고빈도 시계열은 그래프에 넣지 않는다)과도 일치한다. 90일을 넘는 이력이 Plane에 필요하다는 요구가 생기면, 그건 `10-core-architecture.md` §12.1이 이미 인정한 "트랜스크립트 30일 한계"와 같은 종류의 정직한 한계이지 이 어댑터가 해결할 문제가 아니다 |
| P5 | 컨텍스트 저널(에이전트 인수인계용) | **무관.** 이 미러/알림 어댑터 어느 쪽도 에이전트 간 작업 인수인계를 다루지 않는다. 감독의 지적대로 이 설계와 관계없다 |
| P6 | `:Person.notifyChannels`의 `telegram:` 항목 — 값 포맷을 스키마 주석에 `telegram:<chat_id 숫자>`로 구체화 | **경미한 명확화 제안.** 현재 스키마는 `"telegram:..."`로만 예시되어 있어 무엇이 들어가는지(bot username? chat_id? phone?) 불명확하다. chat_id(정수)로 못박는 주석 한 줄이면 충분 — 새 필드나 구조 변경 아님 |

---

## 6. 만들지 않는 것 — 그리고 일반화 경로

**설계하지 않는다(명시적 비범위, 반복 확인)**: 플러그인 레지스트리, 매니페스트 스펙, 마켓스토어 UI/API, 어댑터 동적 로딩·설치. 지금 N=2에서 이걸 만들면 추상의 값을 매길 근거(3개 이상의 실제 사례에서 나온 공통점)가 없다.

**3번째·4번째 어댑터가 왔을 때 일반화할 것 (지금은 아무것도 만들지 않는다)**:

1. **정적 compose 선언 → 동적 워크로드로 전환하는 기준**: 어댑터 수가 늘어 "설치할 때마다 compose를 손으로 편집"하는 비용이 무시할 수 없어지면(대략 5개 이상, 또는 사용자가 직접 어댑터를 껐다 켰다 자주 해야 하는 운영 패턴이 나타나면), `WorkloadDriver`/`AgentWorkloadSpec`(§6.2)를 본뜬 `AdapterWorkloadSpec` + `AdapterDriver`를 만들고 orchestrator의 수렴 루프에 두 번째 desired-state 소스(`:IntegrationAdapter` 노드 → 어댑터 워크로드)로 얹는다. 이때 P1에서 제안한 `:IntegrationAdapter` 노드가 정확히 이 desired state의 근원이 된다 — 미리 만들어 둔 이유가 바로 이것이다(지금 당장 쓰지 않아도, 노드 자체는 "그 조직이 이 연동을 켜기로 했다"는 의도를 이미 담고 있으므로 나중에 재사용 가능).
2. **인터페이스 통합 여부**: `ProjectionAdapter`와 `NotificationAdapter`가 계속 둘로 남을지, 셋째 어댑터가 "투영도 하고 알림도 하는" 혼합형으로 나타나면 그때 공통 상위 인터페이스(`Configure`/`Health`만 있는 최소 `Adapter`)를 추출한다 — 지금 미리 추출하지 않는 이유는 §1.1에 이미 적었다.
3. **자격증명 판정 절차의 재사용**: 새 어댑터의 토큰이 나올 때마다 `31-onboarding-boundary.md` §1.3의 7문항과 이 문서 §2.3이 보여준 두 사례(Q2b가 "이미 스코프된 값"으로 통과하는 패턴)를 그대로 적용하면 된다 — 이 절차 자체는 이미 일반화되어 있다.
4. **격리 등급의 재사용**: §2.2의 네트워크 배치표에 행을 하나 추가하는 것으로 끝난다(`osiris-adapters` 네트워크는 이미 여러 어댑터를 수용하도록 설계되어 있다 — 특정 어댑터 전용이 아니다).
5. **CI 린트의 재사용**: §1.4의 `bin/lint-adapter-boundary.sh`는 이미 "`adapters/**` 전체"를 대상으로 하므로 새 어댑터 패키지가 그 아래 추가되기만 하면 자동으로 커버된다 — 린트 자체를 고칠 필요가 없다.

Slack·Notion 등은 이 경로를 그대로 타는 후보로 남겨 두되, 지금은 설계하지 않는다.

---

## 7. 상위 문서에서 발견한 것 — 감독에게 기록만

이 문서는 `10-core-architecture.md`/`20-ontology-schema.md`/`31-onboarding-boundary.md`/`40-console-ia.md`를 수정하지 않는다. 아래는 이번 설계 과정에서 발견한, 감독이 판단해야 할 사항이다.

| # | 위치 | 내용 |
|---|---|---|
| Q1 | `10-core-architecture.md` §3.2 컴포넌트 표, `osiris-core` 행의 "책임" 열 | `"approval-service / notify-adapter"`가 `osiris-core`의 내부 책임으로 기재되어 있다. 이 문서의 격리 요구(§2.2, 어댑터는 신뢰 경계 밖 별도 컨테이너)와 글자 그대로 겹쳐 읽으면 "텔레그램에 실제로 HTTP를 쏘는 코드가 `osiris-core` 안에 있다"는 오독이 가능하다. **이 문서의 해석(그리고 권고)**: `osiris-core`의 `notify-adapter`는 **누구에게 언제 어떤 심각도로 알릴지 결정하고 `NotifiableEvent`를 만들어 어댑터 대면 포트(`:8091`)에 얹는 결정 로직**까지만이고, 실제 텔레그램 Bot API 호출은 이 문서가 정의한 격리된 `osiris-adapter-telegram` 컨테이너가 한다 — LLM 게이트웨이 어댑터(§9)가 "토큰 수명주기는 core, 실제 LLM 호출은 에이전트 컨테이너"로 나눈 것과 같은 분리다. `10-core-architecture.md` §3.2의 문구가 이 분리를 명시하지 않아 오독 여지가 있으므로, 감독이 그 표에 "(실제 외부 채널 호출은 `50-integration-adapters.md`의 격리된 어댑터가 수행)" 각주를 추가하는 것을 권고한다 |
| Q2 | `20-ontology-schema.md` §3.4 `:WorkItem`, §3.4 `:Evidence` | 감독이 이미 지적한 "작성자 필드 부재"는 §5 P2로 제안했다. 스키마 파일 자체는 건드리지 않았다 |
| Q3 | `31-onboarding-boundary.md` §2.3(축 C) | 이 문서가 만드는 두 어댑터 자격증명(Plane 토큰, 텔레그램 봇 토큰)은 프로바이더 키·omniroute 관리자 비밀번호와 같은 축(외부 스택 연계)에 속하지만 아직 그 문서의 표에 행이 없다 — 감독이 그 문서를 갱신할 때 이 문서의 §2.3 표를 그대로 옮겨 쓸 수 있게 정리해 두었다(이 문서가 `31`을 직접 고치지는 않는다) |
| Q4 | `10-core-architecture.md` §2.9 경로 예약 표 | 텔레그램 어댑터는 long-polling만 쓰므로 새 인바운드 경로가 필요 없다(§4.4). 그러나 향후 어댑터 중 webhook 방식만 지원하는 것(예: 일부 SaaS는 long-polling을 아예 지원하지 않음)이 추가되면, §2.9의 고정 경로 예약 목록(`/`, `/_api/`, `/omni/`, `/pre-status`)에 어댑터 웹훅용 새 prefix(예: `/_hooks/<adapter>`)를 추가해야 하는 순간이 온다 — 지금은 필요 없지만 감독이 그 트리거를 인지하고 있어야 한다 |

---

## 8. 다음 워커를 위한 인터페이스 (짧게)

- **`osiris-core` 구현 워커**: `:8091` 어댑터 대면 REST+SSE 포트, `adapter.read`/`adapter.ack` 두 grant, `GET /integrations/status` 신규 엔드포인트가 이 문서가 요구하는 전부다. 그래프-쓰기는 기존 `/approvals/{id}/decision`·`/wbs/{taskId}/complete-report`를 어댑터 토큰도 호출할 수 있게 인증 미들웨어만 확장하면 되고, 새 비즈니스 로직은 없다.
- **온보딩 워커**: §2.3의 두 자격증명(Plane/텔레그램)과 §4.2의 개인 chat_id 등록 플로우를 온보딩 대화 또는 "연동" 화면에 추가할 것. `omniroute`의 `registerProviderCredential` 패턴(값을 파일/DB에만 쓰고 로그·응답에 남기지 않음)을 그대로 재사용.
- **콘솔 워커**: `GET /integrations/status`(어댑터 헬스), "내 알림 채널" 개인 설정 화면(텔레그램 chat_id 등록), 알림센터(#9)에 에스컬레이션 단계별 배지 표시 정도가 이 문서가 요구하는 UI 추가분이다. 새 사이드바 항목은 필요 없다(온보딩/설정 화면 하위로 충분).
- **CI/인프라 워커**: §1.4의 `bin/lint-adapter-boundary.sh`, `osiris-adapters` 네트워크와 두 어댑터 컨테이너의 compose 서비스 정의(빈 자격증명 기본값, `osiris-omniroute`와 동일한 "설치 시엔 아무것도 하지 않는다" 원칙).
