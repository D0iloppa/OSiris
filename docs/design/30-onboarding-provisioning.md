# OSiris 온보딩·프로비저닝 파이프라인 (Onboarding & Provisioning Pipeline)

> 작성일: 2026-07-26, §1.0(온보딩 0단계) 추가: 2026-07-27, genesis 토큰 발급 시점을 컨테이너 생성 시점으로 이동(3차 정정): 2026-07-27
> 입력: `20-ontology-schema.md` §5(역량 모델)·§7(권한 모델)·§8(배정 알고리즘)·§9(온보딩 시드 인터페이스, I1~I12)·§11(제약), `10-core-architecture.md` §1.4(에이전트 동적 생성)·§5(에이전트 런타임)·§6(진실 소유권)·§8(권한·자격증명)·§13(다음 워커를 위한 인터페이스), `40-console-ia.md` Onboarding 화면·`GET /onboarding/status`. §1.0 추가분 입력: `omniroute/operations.mjs`(6개 오퍼레이션 계약), `IMPLEMENTATION-NOTES.md` §9.6/§53(순환 문제의 존재와 해소 방향 인계), `31-onboarding-boundary.md`(판정 기준, 계승·재논의하지 않음).
> 범위: 조직 온톨로지 시드, 에이전트 자동 프로비저닝, 검증 리허설, 상시 재실행(diff·진행 중 업무 처리·롤백), **그리고 대화형 온보딩에 앞서는 온보딩 0단계(결정적 스택 배선, §1.0)**. **컨테이너/Pod 생성 로직 자체(수렴 루프)는 비범위** — `10-core-architecture.md` §1.4·§6이 이미 확정. 이 문서는 그 앞단(온톨로지를 무엇으로 채우는가, 그리고 그보다도 앞선 게이트웨이 배선)과 뒷단(그 결과가 무엇을 트리거하는가)만 다룬다.
> 전제: 상위 세 문서의 결정은 확정 사항이며 재논의하지 않는다. 온보딩은 **graph-write API만 호출**하고 Cypher를 직접 실행하지 않으며 컨테이너를 직접 만들지 않는다(`20-ontology-schema.md` §9.1, `10-core-architecture.md` §13). §1.0의 판정 기준(무엇이 설치 시점/온보딩/프레임워크 기본값인가)은 `31-onboarding-boundary.md`가 확정한 것을 그대로 적용하며 이 문서가 재정의하지 않는다.
>
> **표기 규칙**: 표기 없는 서술 = 이 문서의 확정 결정. `[미검증]` = 확인 필요. `[갭]` = 이 문서가 발견했으나 이 문서 자신이 채우지 않고 명시적으로 표시만 한 공백(§1.0.6/§1.0.9처럼 이 문서 범위 내의 것과 §9의 상위 문서 것을 구분). `[상위 문서 갭]` = 상위 문서에서 발견했으나 이 문서가 임의로 고치지 않고 명시적으로 표시만 한 지점(§9 참조).

---

## 0. 결정 요약

| # | 결정 | 한 줄 근거 |
|---|---|---|
| O1 | **부트스트랩 역설은 "조직 밖의 제네시스 에이전트"로 푼다.** `bootstrap-master`는 그래프 노드가 아니라 `osiris-up`이 1회 직접 생성하는 임시 컨테이너다 | Master는 조직이 있어야 존재하는데, 조직 정의 대화 자체가 Master의 일. 대화 상대와 조직 멤버를 분리해야 순환이 끊긴다 |
| O2 | 대화는 **LLM이 구조화된 중간표현(`OrgDefinitionDraft`)을 만드는 것까지만**. 그래프 쓰기·불변식 검사·리허설은 전부 결정적 코드(graph-write API) | compute-over-inference. Admin Chat의 WBS 카드와 동일한 패턴(대화 위에 편집 가능한 구조화 카드) |
| O3 | **LLM이 만드는 것은 역량/권한 "선택"이지 "발명"이 아니다.** `:Capability`/`:Permission` 카탈로그는 이미 부트스트랩이 시드했으므로(§9.2 단계 2-6) 온보딩 대화는 그 카탈로그의 코드만 참조한다 | `20-ontology-schema.md` §9.2: 카탈로그는 "시스템 소유. 사용자가 임의 추가 불가" |
| O4 | **CLAUDE.md/persona 프롬프트는 `:Role` 생성 시점에 LLM이 1회 저술**하고 그래프에 저장(`:Role.promptTemplateMd`, 신규 필드 — §9.3에서 갭으로 표시), 이후 **매 프로비저닝/재렌더링은 순수 문자열 치환(코드)** | 프롬프트 저술은 자연어 생성이라 LLM의 일이 맞지만, 그것을 "매번" 하면 토큰 낭비이자 비결정적이 된다. 1회 저술 + 결정적 렌더링으로 나눈다 |
| O5 | `settings.json`/`allowedTools`/`mcp.json`은 **온보딩이 손대지 않는다.** 전부 `PermissionProfile`→grant-catalog 파생이며 이미 `10-core-architecture.md` §5.6·§8.1이 확정 | 온보딩의 몫은 "이 직무에 어떤 `PermissionProfile`을 붙일지" 선택뿐 |
| O6 | 리허설(§9.2 단계 15)의 "배정 후 롤백"은 **§8의 실제 `assign()` 코드를 그대로 재사용**하되, **커밋 전 트랜잭션 내부에서 실행 후 그 부분만 되돌린다** — append-only 불변식(C5)을 어기지 않기 위한 재해석. 컨테이너 실제 기동은 리허설 범위 밖(프로비저닝 단계에서 자연히 검증됨) | §9 모순 절 참조. C5(`AssignmentDecision` 삭제 금지)와 문자 그대로의 "배정 후 롤백"이 충돌하므로, "커밋되지 않은 트랜잭션은 삭제가 아니다"로 해소 |
| O7 | 재실행 시 diff 단위는 **`AgentWorkloadSpec.SpecHash`(이미 존재하는 개념)**. 변경 종류별로 soft reload / recreate(drain) / drain+delete 셋 중 하나로 매핑 | `10-core-architecture.md` §5.6·§6.2가 이미 정의한 기제를 재사용. 새 개념을 만들지 않는다 |
| O8 | 소속·역량 변경으로 **진행 중 배정이 무효화되면, 해당 `:Assignment`를 `revoked`로 만들고 `:WorkItem`을 재배정 대상으로 되돌린다.** 이것은 새 하드 불변식이 아니라 **온보딩 전용 경고(I13)**로 커밋 전에 관리자에게 보여준다 | 스키마는 삭제하지 않고 `validTo`/`revoked`로 표현하는 원칙(P4)을 그대로 따름. 업무를 100% 막으면 재실행형 온보딩이 실무적으로 못 쓰이게 된다 |
| O9 | 온보딩 커밋은 **단일 Neo4j 트랜잭션**(시드 쓰기 + 불변식 검사 + 리허설, 전부 커밋 전) + **성공 시에만 `OrgRevision`+1과 함께 원자적으로 커밋**. 실패 시 네이티브 ROLLBACK — 애플리케이션 사가(saga) 불필요 | 15단계 전부가 "부분 커밋 금지"라는 같은 성질을 가지므로 트랜잭션 경계를 하나로 묶는 것이 가장 단순 |
| O10 | 온보딩의 "완료"는 **두 신호로 분리**: (a) 정의+검증 통과(revision 커밋) (b) 프로비저닝 완료(신규 에이전트 전원 Ready). 콘솔 스테퍼가 4단계에서 끝나는 것은 갭으로 표시(§9) | `10-core-architecture.md` D1의 의도/물성/관측 3분할과 정합. "그래프가 맞다"와 "컨테이너가 떴다"는 다른 사실이다 |
| O11 | **온보딩 0단계(결정적 스택 배선)를 대화형 온보딩(이하 "1단계 온보딩") 앞에 신설한다** — LLM 없이 결정적 코드/폼으로 프로바이더 등록 → `osiris/*` 별칭 확정 → 게이트웨이 토큰 발급 → 게이트웨이 헬스 검증까지 마친 뒤에만 대화형 온보딩이 시작된다(§1.0) | `bootstrap-master`도 LLM이 필요하고 그 LLM 게이트웨이가 바로 온보딩으로 구성되는 순환이 있다(`IMPLEMENTATION-NOTES.md` §53/§9.6 인계). 이 순환을 끊는 논리는 W3가 이미 세운 것과 같다 — LLM은 구조화 산출물까지만, 나머지는 결정적 코드 |
| O12 | **`bootstrap-master` 컨테이너의 생성 시점이 "osiris-up 완료 직후"에서 "0단계 통과 직후"로 늦춰지고, 호출 주체가 `osiris-up` 스크립트에서 `osiris-orchestrator`(osiris-api가 트리거)로 옮긴다.** 생성 로직(이미지·라벨·네트워크, §1.3)은 무변경 — 재사용이지 새 메커니즘이 아니다. **[2026-07-27 3차 정정] genesis 토큰의 발급도 이 생성 시퀀스 안으로 함께 옮긴다** — `osiris-up`은 더 이상 이 토큰을 발급하지 않는다 | 0단계는 사람이 화면에서 값을 입력해야 끝나므로 완료 시점을 예측할 수 없다. LLM을 부를 수 없는 상태의 `bootstrap-master`를 미리 띄워 두는 것은 자원 낭비이자 §8.5 genesis 창을 불필요하게 넓힌다. **genesis 토큰 발급을 `osiris-up`에 그대로 둔 최초 정정(2차)은 발급~컨테이너 생성 사이에 사람의 폼 입력 구간이 새로 끼어들게 방치해 TOFU 도입을 강제했고 잔여 위험(유출 시 선점 경쟁)을 남겼다 — 발급 자체를 컨테이너 생성 시점으로 옮기면 그 구간이 통째로 사라져 잔여 위험도 함께 사라진다.** 창이 실제로 0이 되는 순서와 근거는 `10-core-architecture.md` §8.5 |
| O13 | **[2026-07-27 정정, 감독 지적]** omniroute 관리자 **비밀번호 자체는 어디에도 저장하지 않는다.** 0단계 최초 1회, 사람이 입력한 비밀번호를 **`manage` 스코프 omniroute API 키로 교환**하고, 그 뒤로는 **키만** `.env`(`OSIRIS_OMNIROUTE_MANAGE_KEY`)에 저장한다 — 비밀번호는 메모리에서만 쓰이고 즉시 폐기된다(§1.0.4) | 비밀번호를 저장하는 최초 결정은 §1.1~§1.2 자신이 세운 기준(자격증명 축 하위기준: "사람이 화면에서 직접 보거나 입력하는가?") **을 스스로 어긴 것**이었다 — 정확히 사건 3과 같은 값이다. omniroute 관리 API가 `manage` 스코프 API 키 인증을 이미 지원함을 소스+실측으로 확인해(`31-onboarding-boundary.md` §1.4 사건 4) 교환 패턴으로 정정했다 |
| **O15** | **`[2026-07-28 사용자 지시]` 1단계 온보딩의 대화 주체를 「온보딩 컨설턴트」로 명명하고, 그 성질을 못박는다: 컨설턴트는 자문만 한다 — `POST /onboarding/commit`을 **genesis 스코프에서 제거**하고, 커밋은 승인한 사람의 세션이 호출한다** (§3.4) | 지금까지 `bootstrap-master`의 스코프는 `{draft, commit}`이었다. 즉 **LLM이 구동하는 주체가 조직을 확정하는 쓰기를 스스로 호출할 수 있었다.** O2가 *"LLM은 `OrgDefinitionDraft`까지만"*이라고 정해 둔 경계가 **권한 차원에서는 강제되지 않고 있었다** — 이 결정이 그 간극을 닫는다. 부수 효과로 §1.3이 인정한 D13의 "의도적이고 유일한 예외"가 **더 작아지고**, `60-identity-authn.md` §6.3-2(*"온보딩 커밋의 행위자는 그 대화를 승인한 사람"*)가 서술이 아니라 **호출자 그 자체**가 된다 |
| **O16** | **`[2026-07-28]` omniroute(LLM 게이트웨이)는 온보딩의 1순위 선행조건이며, 0단계의 완료조건에 **스트리밍(SSE) 패스스루 검증**을 추가한다** (§1.0.3의 0-6, §1.0.3.1). **`[실측 완료 2026-07-28]` omniroute·osiris-gw 모두 SSE를 버퍼링하지 않음이 확인되었다(§1.0.3.2) — 우회 설계 3안은 전부 불필요** | O11이 이미 "0단계 통과 후에만 대화"를 정했지만, 컨설턴트를 온보딩의 **주된 수단**으로 명명하는 순간 게이트웨이는 편의가 아니라 **경로 그 자체**가 된다. 그리고 `40-console-ia.md` §D가 컨설턴트 대화를 **SSE 토큰 스트림**으로 규정하므로, "헬스 200"만 검증하고 스트리밍은 검증하지 않으면 *"게이트웨이는 살아 있는데 대화가 한 글자도 안 나오는"* 상태로 1단계에 진입한다. 이 항목은 오랫동안 `[미검증]`으로 남아 있던 추정을 **검증 대상으로 승격**한다 |
| O14 | 0단계 상태는 **Neo4j에 쓰지 않는다.** `getConfigurationStatus()`(omniroute 실시간 조회)와 `.env`의 값 존재 여부만으로 `GET /onboarding/status`를 매 호출 계산한다(§1.0.5) | omniroute DB가 이미 이 진실의 유일한 소유자다(D1 "관측값은 캐시하지 않고 다시 읽는다"와 같은 논리). §8.5의 genesis 창은 열려 있어 그래프에 쓸 **수는** 있지만, 쓸 대상 자체가 없다 |

---

## 1. 부트스트랩 역설과 그 해결

### 1.0 온보딩 0단계 — 결정적 스택 배선 (순환 문제 해소, 신규)

> 이 절은 `10-core-architecture.md`가 §9.6(I5 인계)에서 발견해 "해소 방향만 기록하고 실제로 고치는 것은 온보딩 워커의 몫"이라고 넘긴 순환 문제를 실제로 해소한다. 판정 기준의 상세는 `31-onboarding-boundary.md`를 참조하고(계승, 여기서 재논의하지 않음), 이 절은 그 기준을 이 파이프라인의 구체적 단계 설계에 적용한 결과만 적는다.

#### 1.0.1 순환 문제

§1.1~§1.5(아래)는 온보딩 대화를 조직 밖의 임시 제네시스 에이전트 `bootstrap-master`가 맡는다고 결단했다. 그런데 **그 에이전트도 LLM이 필요하고, 그 LLM 게이트웨이(`osiris-omniroute`)가 바로 온보딩이 구성해야 하는 대상이다.** "게이트웨이를 구성하는 대화"를 "게이트웨이가 있어야 존재할 수 있는 에이전트"가 진행하는 순환이 생긴다.

#### 1.0.2 해소 원칙

이 문서 자신이 O2에서 이미 세운 원칙과 같은 모양이다 — **LLM은 구조화된 산출물(`OrgDefinitionDraft`)까지만 만들고 나머지는 결정적 코드**. 같은 논리를 게이트웨이 배선에도 적용한다:

> **온보딩 0단계(스택 배선)는 LLM이 전혀 필요 없는 결정적 단계다.** 사람이 폼에 값을 입력하고, 코드(온보딩 콘솔의 백엔드, 즉 미래의 `osiris-api`)가 `omniroute/operations.mjs`의 6개 오퍼레이션을 호출해 수렴시킨다. 이 단계에 `claude` 프로세스는 등장하지 않는다. **0단계가 전부 통과한 뒤에만** `bootstrap-master`가 생성되고, 그 다음부터가 기존에 "온보딩"이라고 부르던 대화형 단계다(이하 이 문서에서 "1단계 온보딩"이라 부른다 — 배포 1단계/2단계의 "1단계"와는 다른 축의 번호이므로 혼동 주의).

#### 1.0.3 단계 순서와 성공 판정

**[2026-07-27 갱신]** 0-1을 자격증명 **교환**(최초 1회, 재실행 시 스킵) + **셋업 확인**(매번)으로 나눈다 — §1.0.4의 정정을 반영한 결과다.

| # | 단계 | 호출 오퍼레이션 | 성공 판정 |
|---|---|---|---|
| 0-1a | (최초 설치, 또는 매니지 키 만료/폐기 시) 관리자 자격증명 교환 | 사람이 입력한 비밀번호로 1회 로그인 → `manage` 스코프 API 키 발급(§1.0.4의 신규 계약, `issueOrGetGatewayToken`류 오퍼레이션을 `scopes:['manage']`로 호출) → **비밀번호는 즉시 폐기, 키만 저장** | 발급된 키로 관리 엔드포인트(예 `GET /api/keys`) 1회 왕복 200 확인(실측 완료, §1.0.4) 후 `.env`의 `OSIRIS_OMNIROUTE_MANAGE_KEY` 반영 |
| 0-1b | (재실행, 매니지 키가 이미 있음) omniroute 자체 셋업/도달성 확인 | `getConfigurationStatus()` — 인증은 매니지 키(Bearer 헤더)로, 비밀번호는 관여하지 않음 | `reachable:true`. 키가 만료/폐기되어 인증 실패면 0-1a로 재분기(재교환) |
| 0-2 | 프로바이더 자격증명 등록 | `registerProviderCredential({name, provider, apiKey})` | `ok:true`(`created` 또는 `updated`) |
| 0-3 | `osiris/*` 별칭 적용 | `upsertAliases(entries)`, 초기값은 `getDefaultAliasMapping()` | 선언한 별칭 전부 `ok:true`(`created`/`converged`/`inSync`), `providerMissing`이 하나도 없음 |
| 0-4 | 게이트웨이 토큰 발급 | `issueOrGetGatewayToken({name, scopes})` + `applyGatewayTokenToEnv(token)` | 토큰 발급 성공 + `.env` 반영 확인. **이 시점에 `osiris-orchestrator` 재기동이 필요**(아래 참고) |
| 0-5 | 게이트웨이 헬스 검증 | 실제 LLM 호출 없이: (a) `OSIRIS_GATEWAY_HEALTH_PATH` 200 (b) `getConfigurationStatus()`의 `aliases` 3개 전부 `inSync` (c) orchestrator가 0-4에서 발급된 토큰으로 재기동되었음을 orchestrator 상태 엔드포인트로 확인 | (a)(b)(c) 전부 참 |
| **0-6** | **`[2026-07-28 O16]` 스트리밍(SSE) 패스스루 검증** | `osiris/worker` 별칭으로 `stream: true` + **`max_tokens: 1`** 채팅 완성 1회. 확인 대상: (a) 응답이 `text/event-stream`으로 오는가 (b) **`data:` 프레임이 2개 이상 도착하는가**(1개면 버퍼링되어 한 번에 온 것이다) (c) 첫 프레임까지의 지연이 전체 응답 시간과 유의미하게 다른가 | (a)(b)(c) 전부 참 |

**"게이트웨이 헬스만으로 충분한가?" — 아니다, 증명 가능하게 불충분하다.** `GET /v1/models`(0-5의 (a))는 omniroute가 **빈 DB일 때도 200을 반환한다**(실측, `IMPLEMENTATION-NOTES.md` §41: "healthcheck는 무인증 `/v1/models`라 빈 상태에서도 200"). 즉 프로바이더도 별칭도 하나도 없는 상태에서 헬스체크만 보면 통과로 오인된다. 그래서 0-5는 헬스만이 아니라 (b)(c)를 함께 요구한다.

**0-2~0-5가 실제 프로바이더 키의 유효성까지 보증하지는 않는다 — 의도적 한계.** omniroute는 등록 시점에 키를 실제로 검증하지 않는다(실측, §40: 가짜 키로도 201 생성 성공). 진짜 유효성은 실제 추론이 처음 실패할 때(Provisioning 이후, 실제 에이전트의 첫 태스크에서) 드러난다 — 이는 §5.4(컨테이너 스모크 테스트 배제)와 같은 트레이드오프를 0단계에도 그대로 적용한 것이다: 이 단계에서 진짜 LLM 호출(비용 발생)을 하지 않는다.

#### 1.0.3.1 0-6이 위 원칙의 **좁은 예외**인 이유, 그리고 그 부수 효과 `[2026-07-28 O16]`

바로 위 문단은 *"이 단계에서 진짜 LLM 호출(비용 발생)을 하지 않는다"*고 정했고, **0-6은 그것을 어긴다.** 자기 면제가 아니라 근거를 밝히고 좁게 뚫는다.

**왜 스트리밍만 예외인가 — 실패의 성질이 다르다.** §1.0.3이 미루기로 한 것(프로바이더 키 유효성)은 **나중에 드러나고 복구 가능한** 실패다: 첫 태스크가 실패하고, 관리자가 키를 고치고, 재시도한다. 스트리밍 패스스루가 안 되면 **다음 단계 자체가 불가능하다** — 컨설턴트와의 대화가 1단계 온보딩의 경로 그 자체이고(O15/§3.4), `40-console-ia.md` §D가 그 대화를 SSE 토큰 스트림으로 규정한다. 즉 이것은 "나중에 알아도 되는 사실"이 아니라 **전환 조건**이다. 전환 조건을 검증하지 않으면 0단계는 *"게이트웨이는 200을 주는데 대화가 한 글자도 안 나오는"* 상태로 1단계에 진입하고, 그때 관리자가 보는 것은 원인을 짐작할 수 없는 빈 화면이다.

**비용은 `max_tokens: 1`로 묶는다.** 검증 대상은 응답의 **내용이 아니라 전달 방식**이므로 토큰이 1개면 충분하다 — 프레임이 2개 이상 도착하는지가 판정이고, 그것은 프롬프트 길이나 응답 길이와 무관하다.

**부수 효과 — §1.0.3이 못 닫는다고 인정한 갭이 부분적으로 닫힌다.** 0-6은 **실제 추론 호출**이므로, 프로바이더 키가 무효라면 **여기서 인증 오류로 드러난다.** 위 문단이 *"진짜 유효성은 실제 추론이 처음 실패할 때 드러난다"*고 미뤄 둔 그 시점이 **0단계 안으로 당겨진다.** 다만 과대주장하지 않는다: 이것이 보증하는 것은 *"이 별칭으로 최소 1회 추론이 성공한다"*이며, 모델별 접근 권한·레이트리밋·잔액 소진 같은 조건은 여전히 나중에 드러날 수 있다. **0-6은 "키가 완전히 유효하다"의 증명이 아니라 "키가 완전히 무효는 아니다"의 증명이다.**

#### 1.0.3.2 ~~`[미검증]`~~ **해소 — omniroute는 SSE를 버퍼링하지 않는다** `[2026-07-28 실측]`

2026-07-26 이래 감독 결정 대기 1번으로 열려 있던 *"omniroute가 SSE 스트리밍 패스스루를
지원하는가"*를 **실측으로 닫는다.** 세 갈래 대안((a) 스트리밍 포기 / (b) omniroute
우회 / (c) omniroute에 패스스루 추가)은 **전부 불필요하다.**

**측정 1 — 소스 근거.** 벤더 이미지에 전체 소스가 들어 있어(`omniroute-build/Dockerfile`이
그 사실 위에 서 있다) 직접 읽었다. `open-sse/handlers/chatCore/streamingPipeline.ts`의
`assembleStreamingPipeline()`은 응답을 이렇게 조립한다:

```
providerResponse → transformStream → [PII] → [progress] → SSE heartbeat → [modelEcho] → client
                 (전부 pipeThrough — 증분 TransformStream)
```

버퍼링 구현일 수 없는 세 가지 증거: (1) 전 단계가 `pipeThrough`이고 어디에도 전체 본문을
모으는 지점이 없다, (2) **`createSseHeartbeatTransform`이 존재한다** — 하트비트는 *열려
있는* 스트림을 살려두기 위한 것이므로 버퍼링 파이프라인에는 의미가 없다,
(3) `pipeWithDisconnect`가 클라이언트 중단을 업스트림으로 전파한다. 서브시스템 패키지명
자체가 `open-sse`다.

**측정 2 — 실측(결정적).** omniroute 자신의 네이티브 SSE 엔드포인트를 `osiris-gw` 경유로
구독해 프레임 도착 시각을 측정했다(프로바이더와 무관한 경로를 고른 이유는 §1.0.3.3):

| 경과 | 프레임 |
|---|---|
| 15 ms | `data: {"type":"leaderboard",…}` |
| 5,016 ms | `data: …` |
| 10,019 ms | `data: …` |
| 15,016 ms | `: heartbeat 1785200661605` |
| 15,019 ms | `data: …` |
| 20,019 ms | `data: …` |

`HTTP/1.1 200`, `Content-Type: text/event-stream`, **첫 15ms → 마지막 20,019ms에 걸쳐
분산 도착.** 버퍼링이면 전부 종료 시점에 한꺼번에 왔어야 한다. 그리고 **하트비트 주석
프레임이 실제로 관측되었다** — 측정 1에서 소스로 찾은 그 `createSseHeartbeatTransform`이
런타임에 돌고 있다는 직접 증거다.

**이 측정이 함께 증명하는 것 — `osiris-gw`도 버퍼링하지 않는다.** 위 경로는
`클라이언트 → osiris-gw(nginx) → osiris-omniroute`이므로 nginx가 SSE를 모았다면 같은
결과가 나올 수 없다. `gw/conf.d/default.conf`의 `location /omni/`와 `location /_api/`가
둘 다 `proxy_buffering off` · `proxy_cache off` · `proxy_read_timeout 3600s` ·
`proxy_http_version 1.1`을 갖는 것도 확인했다 — 설정과 실측이 일치한다. `/_api/` 쪽은
`IMPLEMENTATION-NOTES.md` §104가 이미 `/api/events`로 별도 실측했다.

#### 1.0.3.3 그래서 0-6은 **무엇을 더** 검증하는가 (측정의 한계를 정직하게)

위 측정은 **omniroute와 gw가 SSE를 릴레이한다**를 증명하지만, **프로바이더 →
omniroute → 클라이언트의 토큰 단위 릴레이**를 증명하지는 **않는다**. 측정에 쓴
엔드포인트는 omniroute가 스스로 만드는 스트림이지 업스트림에서 받아 넘긴 스트림이
아니다. 그 구간을 합성 프로바이더로 재현하려 했으나 omniroute의 모델 동기화 테이블에
합성 노드의 모델이 올라가지 않아(후보 0개 → `ALL_ACCOUNTS_INACTIVE`) 막혔고, **실제
프로바이더 키가 없으면 이 구간은 측정할 수 없다.**

**그것이 정확히 0-6이 하는 일이다.** 0-6은 실제 키로 `stream:true`를 한 번 호출하므로
이 마지막 구간을 **온보딩 0단계에서 자동으로 검증**한다. 즉 남은 위험은 "설계 결정"이
아니라 "설치 시 자동 확인되는 항목"으로 이동했다.

**남은 위험이 낮은 이유 하나 더**: `open-sse/handlers/chatCore/jsonBodyToSse.ts`가
존재한다 — 업스트림이 스트리밍하지 않는 경우 omniroute가 JSON 본문을 SSE로 변환해
내보낸다. 즉 **클라이언트가 받는 것은 어떤 경우에도 `text/event-stream`**이며, 최악의
경우 잃는 것은 토큰 단위 체감뿐이고 프로토콜이 깨지지는 않는다.

#### 1.0.4 omniroute 관리자 자격증명의 순서 문제 — 결단 (2026-07-27 정정)

> **이 절은 감독의 지적으로 뒤집혔다.** 최초 판(아래 각주에 보존)은 "비밀번호를 `.env`에 저장한다"였다 — 이는 §1.1~§1.2 자신이 세운 기준(자격증명 축: "사람이 화면에서 직접 보거나 입력하는가?")을 스스로 어긴 것이었고, 정확히 사건 3(omniroute 관리자 비밀번호를 설치 시점에 구운 것)과 같은 값을 다시 `.env`에 들여놓는 것이었다. 감독이 omniroute 소스(`requireManagementAuth.ts`)를 실측해 제3의 길을 확인시켜 주었다 — **비밀번호를 저장하지 않고, `manage` 스코프 API 키로 1회 교환한 뒤 그 키만 저장한다.** 상세 경위와 이 사례 자체의 판정 절차상 의의는 `31-onboarding-boundary.md` §1.4 사건 4에 있다 — 여기서는 이 파이프라인에 적용한 결과만 적는다.

**순서(무변경)**: `osiris-orchestrator`(및 미래의 `osiris-core`)가 omniroute 관리 API를 호출하려면 관리자 인증이 필요한데, 그 최초 비밀번호는 **omniroute 자신의 온보딩**(`/omni/`의 첫 접속 시 `/dashboard/onboarding`으로 안내, `IMPLEMENTATION-NOTES.md` §67)에서 사람이 직접 정한다. **omniroute의 자체 셋업이 OSiris 0단계보다 논리적으로 앞선다** — 이 사실은 바뀌지 않는다.

**실측으로 확인한 제3의 길**: omniroute의 관리 API(`requireManagementAuth`)는 대시보드 세션 쿠키(비밀번호 로그인의 결과)뿐 아니라 **`manage` 스코프를 가진 API 키의 `Authorization: Bearer`도 완전히 동등하게 받는다.** 실제로 확인했다(아래 §1.0.4-실측):
- `manage` 스코프 키로 `GET /api/providers`·`GET /api/combos`·`GET /api/keys`·`PUT /api/providers/{id}`·`PUT /api/combos/{id}`·`POST/PATCH/DELETE /api/keys`·`GET /api/keys/{id}/reveal` **전부 200.** 즉 `omniroute/operations.mjs`의 6개 오퍼레이션이 실제로 호출하는 엔드포인트 **전부**가 매니지 키만으로 동작한다 — 쿠키/비밀번호가 전혀 필요 없다.
- **정직하게 밝혀야 할 한계**: 매니지 키는 "권한이 낮은" 키가 아니다 — 다른 키(예 `osiris-agents` 추론 키)의 평문을 `reveal`로 읽을 수 있고, 새 매니지 키를 스스로 발급할 수도 있다(실측 확인). 즉 omniroute 내부에서 매니지 키는 비밀번호와 **동등한 관리 권한**을 갖는다 — 블라스트 레이디어스를 줄이는 것이 아니다. 이 패턴이 주는 이득은 **권한 축소가 아니라 자격증명 축소**다: (1) 사람의 실제 비밀번호가 OSiris 쪽 어디에도 남지 않는다, (2) 매니지 키는 비밀번호와 **독립적으로 회전/폐기**할 수 있다(비밀번호를 바꾸지 않고도 키만 지우고 새로 발급하면 된다 — 실측: 새 매니지 키가 기존 매니지 키의 도움 없이도 비밀번호로 다시 발급 가능하고, 기존 키는 매니지 키 자신의 권한으로 `DELETE`할 수 있다), (3) OSiris가 저장하는 자격증명이 "이 값을 사람이 화면에서 직접 보거나 입력하는가?"라는 기준에서 **명백히 아니오** 쪽으로 넘어간다 — 매니지 키는 사람이 보는 값이 아니라 기계가 반복 사용하는 발급물이다(게이트웨이 토큰과 동일한 성질).

**결단: 비밀번호는 저장하지 않는다. 최초 1회 메모리에서만 써서 `manage` 스코프 키로 교환하고, 그 키(`OSIRIS_OMNIROUTE_MANAGE_KEY`)만 `.env`에 저장한다.**

`31-onboarding-boundary.md` §1.2의 판정 절차를 이 결단에 적용해 재확인한다: 비밀번호 자체는 Q2(사람이 화면에서 직접 보거나 입력하는가?) → **예**이므로 저장 대상이 아니다(사건 3과 동일 판정). 그런데 교환으로 얻은 **매니지 키**는 다시 판정 절차를 처음부터 통과시킨다 — Q2(사람이 직접 보거나 입력하는가?) → **아니오**(기계가 발급받아 기계가 쓴다) → 순수 기계 간 인증 → **설치 시점급으로 저장 정당**(§2.2 축B의 `OSIRIS_GATEWAY_TOKEN`과 동일 범주). 이것이 이 문서의 결정 절차에 새 질문이 필요했던 이유다 — `31-onboarding-boundary.md` §1.3에 Q2b를 추가했다: *"저장해야 하는 게 사람의 원본 자격증명이라면, 그 자격증명을 스코프 제한된 기계 자격증명으로 교환할 수 있는 경로가 있는가? 있다면 원본은 절대 저장하지 않고 교환된 산출물만 저장한다."*

**흐름(갱신)**: (a) 신규 설치 — `.env`에 `OSIRIS_OMNIROUTE_MANAGE_KEY`가 없다. 0단계 콘솔 화면이 "먼저 `/omni/`를 열어 초기 비밀번호를 설정하세요"라는 딥링크를 보여주고, 사람이 그 값을 정한 뒤 **같은 값을 0단계 화면의 입력칸에 1회 입력**한다 — `osiris-api`가 그 값을 **메모리에서만** 써서 매니지 키를 발급받고, 발급된 **키**를 `.env`에 기록한 뒤 **비밀번호는 그 요청의 스코프를 벗어나면 즉시 폐기**한다(파일에도, 로그에도 남지 않음). (b) 기존 설치 재실행 — `.env`에 이미 `OSIRIS_OMNIROUTE_MANAGE_KEY`가 있으면 그 키로 조용히 인증하고(Bearer 헤더, 로그인 호출 자체가 없음) 성공하면 사람에게 아무것도 묻지 않는다. 키가 만료/폐기되어 실패하면 (a)로 재분기(재교환 — 이번에도 새 비밀번호 입력이 필요하다면 사람에게 요청).

**`omniroute/operations.mjs` 계약 변경 명세 (구현은 다음 워커, 이 워커는 코드를 고치지 않는다)**:
1. `resolveOpts()`가 `opts.manageKey || process.env.OSIRIS_OMNIROUTE_MANAGE_KEY`를 추가로 읽는다.
2. `manageKey`가 있으면 `login()`을 **호출하지 않고** 매 `api()` 호출에 `Authorization: Bearer <manageKey>` 헤더를 직접 싣는다(쿠키 대신) — 실측으로 완전히 동등하게 동작함을 확인했다(위 §1.0.4-실측).
3. `adminPassword`(기존 `login()` 경로)는 **오직 최초 교환에만** 남는다 — 매니지 키가 아직 없을 때 1회 사용.
4. 최초 교환은 **기존 `issueOrGetGatewayToken({name, scopes:['manage']}, {adminPassword})`를 그대로 재사용해도 실측상 동일하게 동작한다**(별도 함수를 만들 필요가 최소한으로는 없다) — 다만 감사 가독성을 위해 `exchangeAdminPasswordForManageKey({password, name})`라는 별도 이름을 두는 편을 권고한다("비밀번호를 소비하는 유일한 지점"이 코드에서 grep 한 번으로 보이도록). 이 이름 결정은 다음 워커의 재량이며 이 문서가 강제하지 않는다.
5. `applyGatewayTokenToEnv`와 대칭인 `applyManageKeyToEnv(key, {envFile})`(또는 기존 함수를 변수명 인자화)가 필요 — `.env`의 `OSIRIS_OMNIROUTE_MANAGE_KEY` 라인을 갱신.
6. `.env.example`에서 `OSIRIS_OMNIROUTE_ADMIN_PASSWORD`를 제거하고 `OSIRIS_OMNIROUTE_MANAGE_KEY=`(빈 값 기본)로 교체 — `31-onboarding-boundary.md` §3(전수 감사)에도 반영.

**§1.0.4-실측 (2026-07-27, 감독 지시로 수행)**: 현재 이 저장소가 기동 중인 `osiris-omniroute`(`.env`의 기존 `OSIRIS_OMNIROUTE_ADMIN_PASSWORD` 사용, **회전하지 않음**)를 대상으로, 그 비밀번호로 로그인해 `scopes:["manage"]` API 키를 발급하고, 그 키의 `Authorization: Bearer` 헤더만으로 아래 전부가 200으로 성공함을 확인했다. 테스트로 만든 키는 전부 삭제해 정리했다(cleanup 확인: 최종 `GET /api/keys`에 `osiris-agents` 하나만 남음, 원본 비밀번호는 무변경).

| 엔드포인트 | 메서드 | 매니지 키만으로 결과 |
|---|---|---|
| `/api/combos` | GET | 200 |
| `/api/providers` | GET | 200 |
| `/api/keys` | GET | 200 |
| `/api/providers/{id}` | PUT | 200 |
| `/api/combos/{id}` | PUT | 200 |
| `/api/keys/{id}` | PATCH | 200 |
| `/api/keys/{id}/reveal` | GET | 200(다른 키의 평문도 읽힘 — 위 "정직하게 밝혀야 할 한계" 참고) |
| `/api/keys` | POST(새 매니지 키 생성) | 201 |
| `/api/keys/{id}` | DELETE | 200 |

`operations.mjs`의 6개 오퍼레이션이 실제로 왕복하는 엔드포인트 집합과 이 표를 대조하면 **100% 일치** — 교환 패턴으로 전환해도 기능적 회귀가 없다.

<details><summary>최초 판(뒤집힌 결정, 기록 보존 — append-only 관례)</summary>

이전 결단은 다음과 같았다: "저장한다(`.env`의 `OSIRIS_OMNIROUTE_ADMIN_PASSWORD`), 매 호출 재입력을 요구하지 않는다." 근거로 세 가지 후보(매 호출 재입력/세션 유지/`.env` 저장)를 검토해 `.env` 저장을 택했으나, 세 후보 모두 "비밀번호 원본을 어떻게 다루는가"만 물었을 뿐 "비밀번호를 교환할 수 있는가"를 묻지 않았다 — 이 누락이 §1.3(결정 절차)에 Q2b를 추가하게 된 이유다.
</details>



#### 1.0.5 0단계 상태를 어디에 저장하는가

**결단: Neo4j에 쓰지 않는다.** 이 시점(조직 없음, `:OrgRevision{n:0}`만 존재)에는 §8.5의 genesis 창이 열려 있어 그래프에 쓰는 것이 **기술적으로는 가능**하다 — 그러나 쓸 대상이 없다:
- 프로바이더/별칭/토큰의 진실은 이미 omniroute 자신의 DB에 있다(`getConfigurationStatus()`가 그 진실을 실시간으로 조회). 이걸 그래프에 또 복사하면 D1이 이미 규정한 "관측값은 캐시일 뿐, `observedAt` 없이 존재해서는 안 된다"는 원칙을 새 스키마 없이 우회하게 된다.
- `.env`의 `OSIRIS_GATEWAY_TOKEN`/`OSIRIS_OMNIROUTE_ADMIN_PASSWORD` 존재 여부는 파일을 읽으면 그만이다.

**`GET /onboarding/status`의 0단계 부분은 매 호출 즉석 계산**: `getConfigurationStatus()` 호출 + `.env`의 관련 변수 존재 여부 확인을 조합해 응답을 만든다(§1.0.8에서 정확한 응답 형상 정의). 새 노드 라벨도, 새 파일 저장소도 만들지 않는다 — Simplicity First.

#### 1.0.6 재실행 의미론

0단계 전체가 재실행 가능해야 한다(프로바이더 키 회전, 도메인 변경과는 별개로 모델 매핑 변경 등). 각 오퍼레이션의 성질별로:

> **[2026-07-27 신설] 조직이 생긴 뒤에는 누가 이것을 호출하는가** (F12 해소). 이 절은 "재실행 가능해야 한다"만 말하고 **호출 주체를 적지 않았다.** `60-identity-authn.md`가 master key를 초기 관리자의 최초 로그인 자격증명으로 재정의하면서 그 값이 **비밀번호 설정과 동시에 영구 폐기**되므로(H23/H10), 이 절을 문자 그대로 읽으면 조직 생성 후에는 0단계를 부를 주체가 사라진 것처럼 보인다 — **그렇지 않다.**
>
> **조직 생성 후의 호출 주체는 `org.admin` 권한으로 로그인한 사람이다.** 0단계 오퍼레이션은 콘솔의 Onboarding 화면(`40-console-ia.md` #6)에 그대로 남아 있고, 그 화면이 `org.admin`에 걸려 있다(`60-identity-authn.md` §20.4). 즉 재실행 경로는 없어진 것이 아니라 **부트스트랩 자격증명에서 정규 조직 권한으로 넘어간 것**이다.
>
> **검증 전용 기계 주체는 이것을 호출할 수 없다** — 0단계 오퍼레이션은 전부 쓰기이고 기계 주체에는 `org.read.*` 접두만 부여 가능하다(`60` §19). 조직 생성 *전* 구간의 재실행은 여전히 master key 세션이 수행한다.



| 오퍼레이션 | 멱등성 | 이미 돌아가는 에이전트에 미치는 영향 |
|---|---|---|
| 관리자 자격증명 교환(§1.0.4, 매니지 키 회전) | 매니지 키는 언제든 폐기 후 재발급 가능(실측 확인, §1.0.4-실측) — **비밀번호를 바꾸지 않고도** 키만 회전할 수 있다(매니지 키 자신이 새 키 발급·기존 키 삭제 권한을 갖는다) | **영향 없음** — 이 자격증명은 omniroute 관리 API 호출에만 쓰이고 에이전트의 LLM 호출 경로(게이트웨이 토큰)와는 별개다 |
| `registerProviderCredential` (키 회전) | 멱등(blind overwrite) | **영향 없음** — omniroute 내부에서 프로바이더 연결이 바뀔 뿐, 에이전트는 여전히 `osiris/worker` 같은 별칭만 알고 있다. 다음 LLM 호출부터 조용히 새 키로 라우팅된다 |
| `upsertAlias`/`upsertAliases` (모델 매핑 변경) | 멱등(수렴, §50의 결함 수정 이후) | **영향 없음** — 별칭 이름이 그래프의 `:AIAgent.modelAlias`에 저장되고 바뀌지 않으므로 `ConfigHash`/`SpecHash` 어느 쪽도 변하지 않는다. 진행 중 턴은 물론 다음 태스크도 영향 없이 새 모델로 라우팅된다. 이것이 별칭이라는 간접 계층을 둔 이유(§9.2 계약 5) 그 자체다 |
| `issueOrGetGatewayToken`+`applyGatewayTokenToEnv` (토큰 재발급/회전) | 스코프가 같으면 재조회(멱등). **진짜 회전(기존 키 폐기 후 재발급)은 이 6개 오퍼레이션에 없다 — `[갭]`, 아래 참고** | `.env` 갱신만으로는 전파되지 않는다: `osiris-orchestrator`는 이 값을 **자신의 컨테이너 환경변수**로 읽으므로 `docker compose up -d osiris-orchestrator` 재기동이 필요하다(기존에 이미 문서화된 사실, `.env.example` 주석). 재기동 자체는 이미 돌던 에이전트 **컨테이너**를 건드리지 않는다 — orchestrator 재기동 후 다음 reconcile tick이 각 에이전트의 자격증명 파일을 새 토큰으로 재기록하고, supervisor는 파일 mtime 감시로 **재시작 없이** 반영한다(§5.2/§5.6, 기존 메커니즘 재사용) |
| 도메인/`PUBLIC_ORIGIN` 변경(축 A, 0단계 범위 밖이지만 같은 재실행 절에서 언급할 가치가 있음) | 수동(`.env` 편집 + `osiris-gw` conf 재적용) | 기존 콘솔 세션 쿠키의 도메인이 무효화되어 재로그인이 필요할 수 있다 — 경미한 파괴적 영향으로 명시 |

**`[갭]` — 진짜 토큰 회전 오퍼레이션이 없다**: 유출 의심 시 "기존 키를 폐기하고 새 키를 발급"하는 것은 현재 6개 오퍼레이션 어디에도 없다(`issueOrGetGatewayToken`은 있으면 재조회할 뿐 폐기하지 않는다). 회전이 필요하면 사람이 omniroute UI에서 직접 키를 삭제한 뒤 0-4를 재실행해야 한다 — 다음 워커 인계 사항으로 표시한다.

#### 1.0.7 0단계 → 1단계 전환 조건과 `bootstrap-master` 생성 시점

**전환 조건**: §1.0.3의 0-1~**0-6** 전부 통과 `[2026-07-28 O16: 0-6 추가]`. 그중 어느 하나라도 실패면 전환하지 않는다(§1.0.3에서 이미 논증했듯 헬스만으로는 불충분).

**`bootstrap-master` 생성 시점 정정**: 기존 §1.3~§1.4(아래)는 "`osiris-up`이 코어 7개 기동과 동시에 `bootstrap-master`도 1회 직접 생성"이라고 되어 있었다. **이 문서는 이 시점을 늦춘다.** `osiris-up`은 여전히 코어 7개(이제 `osiris-omniroute`도 포함, 빈 DB로 healthy)만 기동하고 **`bootstrap-master`는 만들지 않는다.** 0단계가 사람의 폼 입력을 거쳐야 하는 이상 완료 시점을 예측할 수 없고, 그 전에 LLM을 부를 수 없는 `bootstrap-master`를 띄워 두는 것은 자원 낭비이자 §8.5 genesis 창을 불필요하게 넓힌다(창이 열려 있는 시간이 길수록 침해 표면이 커진다 — §8.5 자신의 "다섯 겹 폐쇄" 철학과 정합).

**생성 주체도 바뀐다**: `osiris-up`(1회성 스크립트) → **`osiris-orchestrator`가 API로 트리거되어 생성**(호출자는 온보딩 콘솔의 백엔드, 즉 0단계 완료를 감지한 `osiris-api`). 이유: `osiris-api`는 docker.sock을 갖지 않는다(D4/G8이 이미 금지) — 컨테이너를 실제로 만들 수 있는 것은 docker.sock을 가진 `osiris-orchestrator`뿐이다. 이 변경은 **생성 로직(이미지·라벨·네트워크·genesis 라벨 예외, §1.3)을 그대로 재사용**한다 — 새 컨테이너 생성 메커니즘을 만드는 것이 아니라, 이미 있는 메커니즘을 호출하는 방식(스크립트 → API 요청)만 바뀐다.

**[2026-07-27 3차 정정] genesis 토큰의 발급 시점도 이 정정과 함께 옮겨진다 — 더 이상 무변경이 아니다.** 이전 판(이 절)은 "발급은 여전히 `osiris-up`이 1회 수행하고, 늦춰지는 것은 그 토큰을 쓰는 컨테이너의 생성뿐"이라고 적었다. 그 결과 발급(`osiris-up` 실행 시점)과 소비(`bootstrap-master` 생성 시점, 사람의 0단계 폼 입력 이후) 사이에 예측 불가능한 구간이 생겨, `10-core-architecture.md` §8.5가 이를 TOFU(최초 사용 시 바인딩)로 메꾸면서도 "유출 시 선점 경쟁"이라는 잔여 위험을 인정해야 했다. 이번 정정은 그 구간 자체를 없앤다 — **genesis 토큰은 이제 `osiris-up`이 발급하지 않고, `bootstrap-master` 컨테이너를 만드는 바로 이 시퀀스 안에서 발급된다.** 구체적으로: orchestrator가 `ContainerCreate`(아직 기동 전)로 실제 컨테이너 ID를 확보 → 그 ID를 실어 `osiris-core`에 발급을 요청(기존 `CORE_API_TOKEN`으로 인증) → core가 `currentOrgRevision == 0`을 자신이 직접 확인한 뒤 그 ID에 바인딩해 발급 → orchestrator가 파일로 주입 → 그제서야 `ContainerStart`. 창(발급~바인딩 사이의 간격)이 구조적으로 0이 되는 이유와 다섯 겹 폐쇄조건(W1~W5) 재검증은 `10-core-architecture.md` §8.5에 있다 — 이 문서는 그 결과(발급 시점이 여기로 옮겨졌다는 사실)만 반영한다.

> **`[2026-07-28 K2 전환]`** 바로 위 `ContainerCreate`→ID 확보→발급→파일 주입→`ContainerStart` 시퀀스는 Docker Engine API 전제 위에서만 성립하며, k8s에는 이 순서를 그대로 옮길 API가 없다(실측 확인됨). `70-kubernetes-deployment.md` §4.1(K7)이 대체한다 — bootstrap-master Pod가 전용 ServiceAccount의 projected token을 마운트하고, `osiris-core`가 TokenReview로 검증하는 방식이며, "발급이 곧 바인딩"이라는 이 절의 목표는 "이 SA가 이 Pod에만 마운트된다"는 k8s 보장으로 이행된다. `osiris-orchestrator`/`osiris-api` 역할 분리(docker.sock 보유 여부에 근거한 위 "생성 주체" 문단)도 k8s에서는 `osiris-operator`가 Pod 생성 권한을 갖는 구조로 대응되며, 상세는 §4.1이 소유한다.

이 정정은 §1.3~§1.5(아래)의 `bootstrap-master`의 **정체성**(그래프 노드 아님, genesis 라벨, 권한 스코프, 폐기 경로)을 전혀 바꾸지 않는다 — 오직 **"언제, 무엇이 생성을 호출하는가"**만 바꾼다.

#### 1.0.8 `osiris-api` 엔드포인트 계약

콘솔의 0단계 화면(대화형 마법사 앞의 순수 폼 — §1.0.2가 이미 "LLM 없음"을 못박았으므로 이 화면은 Admin Chat의 카드 패턴을 재사용하지 않고 일반적인 설정 폼이다)이 호출할 계약. 6개 오퍼레이션을 얇게 감싸며, 시크릿을 받는 엔드포인트는 **저장하지 않고 전달만**(프로바이더 키) 하거나 **저장하되 재노출하지 않음**(관리자 비밀번호·게이트웨이 토큰, §1.0.4/O13)을 원칙으로 한다.

| 엔드포인트 | 감싸는 오퍼레이션 | 시크릿 처리 원칙 |
|---|---|---|
| `GET /onboarding/step0/status` | `getConfigurationStatus()` + `.env` 존재 여부 확인(§1.0.5) | 시크릿 값 자체는 응답에 절대 포함하지 않음 — `exists`/`ok` 같은 불리언만 |
| `POST /onboarding/step0/omniroute-admin` `{password}` | **[2026-07-27 정정]** `password`로 1회 로그인해 `manage` 스코프 키를 발급받는 교환(§1.0.4의 `exchangeAdminPasswordForManageKey`류 오퍼레이션)만 수행 — 이후 발급된 **키**만 `.env`(`OSIRIS_OMNIROUTE_MANAGE_KEY`)에 반영 | **`password`는 저장하지 않고 교환에만 사용** — 요청 처리 완료 즉시 메모리에서 폐기, 파일/로그 어디에도 쓰지 않는다. **교환으로 얻은 키는 저장**(§1.0.4 — 이 키는 사람이 보는 값이 아니라 기계 발급물이므로 축B 판정이 다르다). 응답에는 `password`도 `key`도 에코하지 않는다 — `exchanged:true`만 |
| `POST /onboarding/step0/providers` `{name, provider, apiKey}` | `registerProviderCredential(...)` | **저장하지 않고 전달만** — `apiKey`는 이 호출 스택 어디에도 파일/로그로 쓰이지 않는다(`operations.mjs`가 이미 이 계약을 지킨다, 이 엔드포인트는 그 계약을 유지만 하면 된다). 응답에도 `apiKey`를 에코하지 않는다 |
| `GET /onboarding/step0/alias-defaults` | `getDefaultAliasMapping()` | 시크릿 없음 |
| `POST /onboarding/step0/aliases` `{entries}` | `upsertAliases(entries)` | 시크릿 없음(모델 매핑만) |
| `POST /onboarding/step0/gateway-token` | `issueOrGetGatewayToken(...)` + `applyGatewayTokenToEnv(token)` | **저장** — 토큰 자체는 `.env`에만, 응답에는 `issued:true`만(토큰 값을 콘솔에 노출할 필요가 없다 — 사람이 볼 값이 아니므로, `31-onboarding-boundary.md` §2.2 축B 판정과 정합). 응답에 `needsOrchestratorRestart:true`와 `restartCommand:"docker compose up -d osiris-orchestrator"`를 포함(§1.0.6 — 1단계 알파에서는 이 재기동이 수동이며, 콘솔은 Ops 화면의 "노드 join" 모달과 같은 패턴으로 복사 가능한 커맨드를 보여준다) |
| `POST /onboarding/step0/verify-gateway` | §1.0.3의 0-5 판정 로직(실제 LLM 호출 없음) | 시크릿 없음 |

`GET /onboarding/status`(기존, 대화형 단계의 진행 상황을 보여주던 엔드포인트)는 위 `step0/status`를 **최상위 필드로 흡수**해 하나의 응답으로 합친다:

```jsonc
{
  "step0": {
    "omniroute": { "reachable": true, "manageKeyExchanged": true },  // "authenticated"가 아니라
                                       // "manageKeyExchanged" — 인증은 매니지 키(§1.0.4)로 매번
                                       // 이루어지고, 비밀번호는 이 필드가 처음 true가 되는
                                       // 교환 순간에만 등장했다가 사라진다
    "providers": [ { "name": "osiris-anthropic", "provider": "anthropic" } ],
    "aliases": [ { "name": "osiris/worker", "inSync": true, "providerRegistered": true } ],
    "gatewayToken": { "issued": true, "propagatedToOrchestrator": true },
    "streamingVerified": true,        // [2026-07-28 O16] 0-6. 컨설턴트 대화가 실제로
                                       // 가능한지의 유일한 증거 — 헬스 200과 별개다
    "ready": true                     // 0-1~0-6 전부 통과 여부 — 이 값이 §1.0.7의 전환 조건
  },
  "phase": "NotStarted",              // §2의 상태기계 값. step0.ready == false 인 동안은
                                       // 콘솔이 대화형 화면 자체를 비활성화한다
  "org": { "teams": 0, "roles": 0, "capabilityMappingRatio": null, "approvalPolicyDefined": false }
}
```

#### 1.0.9 실패·중단 처리

0단계는 §5.2(아래)의 단일 Neo4j 트랜잭션과 **성질이 다르다** — omniroute는 트랜잭션을 지원하지 않는 외부 시스템이고, 6개 오퍼레이션은 각각 독립적으로 멱등하다(§1.0.6). 따라서:

- **부분 실패가 곧 정상 상태다.** 0-2에서 실패해도(예: apiKey가 명백히 잘못된 형식) 0-1의 결과(omniroute 인증)는 그대로 유효하다. "롤백"할 것이 없다 — 실패한 단계만 사람이 값을 고쳐 재시도하면 되고, 이는 §1.0.6의 "재실행"과 **완전히 같은 코드 경로**다(정상 재실행과 오류 복구가 분기하지 않는다는 것이 이 설계의 단순함이다).
- **재개 지점**: 대화형 온보딩의 `Drafting` 상태처럼 별도 초안/트랜스크립트 저장소가 없다 — 콘솔이 0단계 화면을 다시 열면 `GET /onboarding/step0/status`가 각 하위 단계의 통과 여부를 즉시 알려주고, 화면은 처음 실패한 단계로 포커스를 옮긴다(`40-console-ia.md` Onboarding 화면의 "우측 체크리스트 패널" 패턴 재사용 — 새 UI 패턴을 만들지 않는다).
- **구체적 실패 유형과 표면화**:
  - *omniroute 미셋업*(관리자 비밀번호 없음) → 0-1에서 즉시 감지, `/omni/`로의 딥링크 안내(§1.0.4).
  - *게이트웨이 불통*(컨테이너 다운/네트워크 문제) → 0-1에서 `reachable:false`로 즉시 감지, 재시도 버튼만 필요(파괴할 상태가 없는 순수 프로브).
  - *잘못된 프로바이더 키*(형식은 맞지만 실제로 무효) → **0단계는 이걸 못 잡는다(§1.0.3의 의도적 한계).** 실제 추론이 처음 시도될 때(Provisioning 이후) 실패로 드러나며, 그 시점의 표면화는 Ops 화면의 에이전트 오류 상태·`org.node.status_changed`가 담당한다(이 문서의 §5~§8이 이미 다루는 경로를 재사용, 0단계가 새 오류 채널을 만들지 않는다).

### 1.1 문제

온보딩 콘솔(40-console-ia.md #6)은 "대화형 마법사"이고, 조직 전역에서 자연어 대화를 받는 존재는 `agent-master`(`isMaster:true`인 `:AIAgent`, `10-core-architecture.md` §3.2)다. 그런데 `:AIAgent{isMaster:true}` 노드는 `20-ontology-schema.md` §9.2의 **10번째 단계**에서 만들어진다 — 그 앞에 카탈로그(2-6)·팀(7)·직무(8)가 이미 있어야 한다. 즉 **최초 온보딩 대화 시점에는 Master가 그래프에도, 런타임에도 존재하지 않는다.**

### 1.2 검토한 안

| 안 | 내용 | 판정 |
|---|---|---|
| (a) 최소 시드로 Master를 먼저 만들고 나머지는 Master가 채움 | 조직 루트+카탈로그만 부트스트랩이 심고, `:AIAgent{isMaster}` 하나를 최소 스펙(팀 없음, 역량 없음)으로 즉시 만들어 대화를 넘김 | **기각.** 팀도 없는데 "Master"가 존재한다는 것은 I2(모든 AIAgent는 유효한 Membership이 정확히 1개)를 온보딩 도중 내내 위반한 상태로 두는 것 — 불변식이 "항상 참"이어야 하는 성질(schema §9.1 "각 단계는 하나의 트랜잭션")과 어긋난다. 또한 이 미완성 Master가 `master.inbox`에 대한 NATS 권한을 가지면 크로스팀 강제(D14)의 전제(팀이 존재)가 성립하지 않는 상태에서 권한이 부여되는 모순이 생긴다 |
| (b) 온보딩은 LLM 없는 순수 폼(form)이고 대화는 편의층일 뿐 | 팀/직무/역량을 드롭다운·표 입력으로만 받고, 자연어 파싱은 하지 않음 | **기각.** `40-console-ia.md` #6이 "대화형 마법사"를 명시적으로 요구했고, Admin Chat과 동일 패턴 재사용이 콘솔 설계 원칙(#6 "신규 화면 패턴을 만들지 않음")이다. 폼 전용으로 가면 그 요구와 원칙을 둘 다 어기게 되어 재논의가 필요한 상위 결정 변경이 된다 — 이 문서 권한 밖 |
| **(c) 조직 밖의 제네시스 에이전트(bootstrap-master)** — **채택** | Master와 동일한 에이전트 런타임 이미지·`claude -p` 호출 패턴을 쓰되, **그래프 노드가 아니며 조직에도 속하지 않는** 임시 컨테이너. `osiris-up`이 1회 직접 생성(§4.1의 8개 코어 서비스와 별개, 컴포즈 파일에도 없음). 온보딩 완료 시 폐기 | 채택. 이유는 §1.3 |

### 1.3 채택안: `bootstrap-master`

**정체성**: `osiris.dev/genesis=true` 라벨을 가진, `ghcr.io/osiris/agent-runtime` 이미지의 컨테이너 1개. `:AIAgent` 노드가 아니므로 `osiris-orchestrator`의 수렴 루프(§1.4의 desired/actual 비교)가 **그래프에서 파생하지 않는다** — 즉 orchestrator 관점에서는 "존재를 모르는" 컨테이너다. 이것이 중요하다: 수렴 루프의 고아 정리 로직(`for id := range actual: if not in desired: Drain+Delete`, `10-core-architecture.md` §1.4)이 라벨 매칭에서 `osiris.dev/genesis=true`를 명시적으로 제외하도록 orchestrator 코드에 예외 한 줄이 필요하다 — 그렇지 않으면 다음 15초 tick에 곧바로(그래프가 비어 있으므로 desired가 공집합) 고아로 오인되어 드레인된다. **이 예외는 orchestrator의 "그래프가 유일한 진실"이라는 원칙(D17/G4)의 유일한 의도적 이탈이며, genesis 라벨이 있는 리소스에 한해서만 적용된다.**

> **`[2026-07-28 K2 전환]`** 위 문단은 Docker 컨테이너·`osiris-orchestrator` 수렴 루프를 전제로 서술되어 있다. K2 전면 재작성 이후 **"임시 컨테이너"는 "임시 Pod"가 된다.** `osiris.dev/genesis=true` 라벨과 "그래프에서 파생하지 않는 리소스"라는 정체성은 그대로 유지되지만, 다음 두 가지가 바뀐다: (1) 고아 정리 예외 로직의 위치가 `osiris-orchestrator`가 아니라 `osiris-operator`(reconcile 루프)로 옮겨간다 — 대응 예외는 여전히 필요하다. (2) `70-kubernetes-deployment.md` §5.2(K10)에 따라 `bootstrap-master` Pod는 **Agent CR과 같은 네임스페이스에 두어 k8s 가비지 컬렉터(ownerReference)에 폐기를 위임**한다 — 아래 §1.5 정정 참고. "그래프가 유일한 진실"이라는 원칙과의 관계는 무변경이다.

> **`[2026-07-28 O15/§3.4]` 이 절의 스코프 서술은 좁혀졌다.** 아래 문단은 스코프를
> `{draft, commit}`으로 적고 있으나, **`POST /onboarding/commit`은 제거되었다** —
> 커밋은 승인한 사람의 세션이 호출한다. 근거와 두 구간(1회차/2회차)의 호출자는
> [§3.4](#34-온보딩-컨설턴트--자문-권한의-경계-2026-07-28-사용자-지시-o15)에 있고,
> 그 결과 아래 문단이 인정하는 D13 예외는 **읽기와 초안 검증만** 남는다.
> **`[2026-07-28 F14 해소]`** `10-core-architecture.md` §8.5의 스코프 표도 갱신되었다 —
> 그 문서의 **§8.5.1**(4차 정정)이 `commit` 제거와 W1~W5 재검증을 담고 있다. 두 문서가
> 이제 일치한다.

**권한 스코프 (§9의 갭으로 표시)**: `bootstrap-master`가 부르는 것은 오직 온보딩 전용 graph-write API 엔드포인트(`POST /onboarding/draft`, ~~`POST /onboarding/commit`~~ **[§3.4로 제거]** 등, §3 참조)뿐이다. 그런데 `10-core-architecture.md` §7.1의 유효 권한 계산(`effective(party)`)은 **`:Party`를 전제로** 정의되어 있고, `bootstrap-master`는 `:Party`가 아니다(그래프에 없음). 따라서 이 토큰의 인가는 PermissionProfile 파생이 아니라 **osiris-core 코드에 하드코딩된 고정 스코프**(온보딩 draft 엔드포인트와 읽기만 — `[2026-07-28 §3.4]` commit 제외, 그 외 전부 거부)로 처리한다. 이것은 권한 모델의 "선언 하나에서 4개 층위 파생"(D13) 원칙에 대한 **의도적이고 유일한 예외**이며, TTL은 온보딩 진행 중에만 유효하고 §1.5의 폐기 시 즉시 revoke된다.

**네트워크**: 일반 에이전트와 동일하게 `osiris-agents` 네트워크 하나(§2.6 기존 DMZ 패턴 재사용, 새 구멍을 뚫지 않는다). `osiris-core:8090`(graph-write, 온보딩 스코프 토큰)과 `osiris-egress`(LLM 게이트웨이)만 도달.

**모델**: `osiris/planner` 별칭(Master와 동일 — WBS 분해와 마찬가지로 조직 구조 분해가 본질적으로 같은 종류의 추론).

### 1.4 흐름 속 위치

**§1.0의 정정을 반영한 최신 순서** — `bootstrap-master`의 생성이 코어 기동과 동시가 아니라 0단계 통과 이후로 늦춰지고, 호출 주체가 `osiris-up`에서 `osiris-orchestrator`로 바뀐다(§1.0.7). 아래 §1.1~§1.5는 `bootstrap-master`의 **정체성**(그래프 노드 아님, genesis 라벨, 권한 스코프, 폐기 경로)을 다루며 이 정정으로 바뀌지 않는다 — 바뀌는 것은 생성 시점·주체뿐이다.

```
osiris-up 실행
 └ §4.1의 코어 7개 기동 + 카탈로그 시드(2-6단계, 코드) — osiris-omniroute도 이 시점에
   기동되지만 빈 DB로 healthy할 뿐, 아무것도 구성되지 않는다(§1.0.2)
 └ 관리자 1회용 로그인 토큰 출력. **genesis 토큰은 이 시점에 아직 발급되지 않는다**
   [2026-07-27 3차 정정] — 발급 자체가 `bootstrap-master` 컨테이너 생성 시점으로
   옮겨졌다(`10-core-architecture.md` §8.5). 이 실행이 끝나도 호스트에 genesis
   관련 시크릿 파일은 존재하지 않는다
     ↓
콘솔 Onboarding 화면 진입 → ★ 온보딩 0단계 (§1.0, LLM 없음, 결정적 코드/폼)
 └ 0-1 omniroute 셋업 확인 → 0-2 프로바이더 등록 → 0-3 별칭 확정 → 0-4 게이트웨이
   토큰 발급(+orchestrator 재기동) → 0-5 게이트웨이 헬스 검증 → ★ 0-6 스트리밍(SSE)
   패스스루 검증 — 전부 통과해야 다음으로 [2026-07-28 O16]
     ↓ (§1.0.7의 전환 조건 충족)
 └ ★ bootstrap-master 컨테이너 생성 — 이제 osiris-orchestrator가 osiris-api의 API
     요청으로 트리거(§1.0.7). 생성 로직 자체(이미지·라벨·네트워크)는 무변경.
     **genesis 토큰은 바로 이 생성 시퀀스 안에서 처음 발급된다**: orchestrator가
     `ContainerCreate`(미기동)로 컨테이너 ID를 확보 → 그 ID로 core에 발급 요청 →
     core가 `currentOrgRevision==0` 확인 후 그 ID에 바인딩해 발급 → orchestrator가
     파일 주입 → `ContainerStart`(`10-core-architecture.md` §8.5, 창이 0이 되는 순서)

     > **`[2026-07-28 K2 전환]`** 위 시퀀스(`ContainerCreate` → ID 확보 → 발급 →
     > 파일 주입 → `ContainerStart`)는 k8s에는 대응 API가 없어 그대로 성립하지
     > 않는다. K7이 대체한다: bootstrap-master Pod 생성 시 전용 ServiceAccount가
     > projected token을 볼륨으로 마운트받고, `osiris-core`가 그 토큰을
     > TokenReview API로 검증한다 — "생성 후 기동 전에 호스트 파일을 써 넣는다"는
     > 절차 자체가 없어지고, 신원은 SA 마운트가 대신 보장한다. 상세는
     > `10-core-architecture.md` §8.5(K2 전환 정정)와 `70-kubernetes-deployment.md`
     > K7 참고.
     ↓
콘솔 Onboarding 화면 → ★ 온보딩 컨설턴트와 대화 (이하 "1단계 온보딩", §2, §3, §3.4)
 └ 런타임 실체는 위에서 만든 bootstrap-master 컨테이너 그대로. 컨설턴트는 **제안만**
   한다 — OrgDefinitionDraft를 만들고 매 턴 draft 엔드포인트로 조기 검증받되,
   **commit은 부를 수 없다**(§3.4, genesis 스코프에서 제거됨)
     ↓
★ 사람이 초안을 승인 → **승인한 세션이** 커밋을 호출 (§3.4.3)
 └ 1회차: master key 세션(`kind:"master-key"`, partyId 없음 — 60 §4.1의 그 세션)
   2회차 이후: org.admin 사람 세션(DG1~DG5 평가 — 60 §21.4-3)
     ↓
온보딩 커밋 (§7.1의 10단계 시퀀스) → :OrgRevision +1
 └ 이 시점 처음으로 orchestrator 수렴 루프가 desired 집합에서 실제 agent-master(isMaster:true)를
   발견 → Apply() → 진짜 agent-master 컨테이너 기동
     ↓
진짜 agent-master가 Ready 되면 → bootstrap-master 명시적 drain+delete (§1.5)
```

### 1.5 폐기 경로

`bootstrap-master`는 orchestrator의 그래프 기반 정리 대상이 **아니므로**(§1.3), 폐기는 온보딩 커밋 완료 루틴이 명시적으로 수행한다: 진짜 `agent-master`의 `:AgentRuntime.ready == true`를 확인(폴링 또는 `org.node.status_changed` 구독, 타임아웃 5분 — 실패 시 §9.2 이후의 "프로비저닝" 단계 실패로 취급하고 `bootstrap-master`는 유지해 관리자가 계속 대화하며 문제를 진단할 수 있게 한다) 후, 동일한 Docker Engine API로 `Drain→Delete`. `bootstrap-master`는 태스크 큐를 갖지 않으므로(§1.3) drain은 사실상 즉시 끝난다.

**왜 암묵적 경쟁(race)에 기대지 않는가**: 그래프 기반 고아 정리에 맡기면(제외 라벨을 지우는 방식) "언제 지워지는지"가 15초 tick 타이밍에 의존해 재현 불가능해진다. 온보딩 완료라는 명확한 이벤트에 결합된 명시적 호출이 훨씬 단순하고 감사 가능하다.

> **`[2026-07-28 K2 전환]`** 위 "동일한 Docker Engine API로 `Drain→Delete`"는 k8s에는 대응하지 않는다. `70-kubernetes-deployment.md` §5.2(K10)이 대체한다: `bootstrap-master`는 진짜 `agent-master`용 `Agent` CR과 **같은 네임스페이스에 두어**, 온보딩 커밋 완료 루틴이 `bootstrap-master` Pod(또는 그 CR)를 명시적으로 삭제하면 나머지 정리는 **k8s 가비지 컬렉터(ownerReference)에 위임**한다 — "명시적 호출"이라는 이 절의 핵심 성질(암묵적 경쟁에 기대지 않음)은 유지된다: 삭제 트리거 자체는 여전히 온보딩 완료 이벤트에 결합된 명시적 호출이고, 그 트리거 *이후*의 하위 리소스 정리만 GC가 대신한다. `Drain`에 대응하는 "진행 중 태스크 없음" 전제는 §1.3("태스크 큐를 갖지 않으므로")과 동일하게 유지되어, k8s에서도 별도 drain 유예 없이 즉시 삭제해도 안전하다.

---

## 2. 온보딩 상태기계

**§1.0 삽입에 따른 갱신**: 대화형 상태기계(`Drafting` 이하)는 무변경이다. 그 앞에 0단계 상태 `Wiring`을 하나 추가한다 — `bootstrap-master`가 아직 존재하지 않으므로 이 상태에서는 대화가 원천적으로 불가능하고, 콘솔은 §1.0.8의 폼 화면만 보여준다.

```
Wiring  (§1.0. GET /onboarding/status의 step0.ready == false. LLM 없음, 폼 UI)
   │ 0-1~0-6 전부 통과 (§1.0.3/§1.0.7)
   ▼
NotStarted
   │ 관리자가 Onboarding 화면 진입 (bootstrap-master 생성 완료됨, §1.0.7)
   ▼
Drafting ──────────────────────────────────────────┐
   │ (bootstrap-master 또는 기존 agent-master와 대화,          │ "다시 짜줘"
   │  OrgDefinitionDraft 인라인 카드 편집 — §3.2)              │ (카드 폐기,
   ▼                                                          │  새 카드 생성)
Reviewing  (카드 하단 액션바: 승인 / 일부 승인 / 다시 짜줘) ◀──┘
   │ "승인"
   ▼
Committing  (§7.1의 단일 트랜잭션 진행 중 — 수 초)
   │
   ├─ 실패(오류) ──▶ Reviewing 복귀, 오류 목록을 카드에 인라인 표시 (§5.5)
   │
   ▼ 성공 (경고는 있을 수 있음, 오류는 없음)
Committed  (:OrgRevision +1. GET /onboarding/status = "정의 완료")
   │
   ▼
Provisioning  (수렴 루프가 컨테이너 기동 중. GET /onboarding/status = "프로비저닝 중 N/M")
   │
   ▼
Steady  (신규 에이전트 전원 Ready 또는 타임아웃으로 개별 Failed 처리)
   │
   ▼ (조직 변경 필요 시 — 사람이 아무 때나 트리거)
Drafting 재진입  ← 상시 재실행형(요구 4). 이전 Steady 상태의 그래프가 새 대화의 출발점
```

**중단/재개**: `Drafting`/`Reviewing` 상태의 미완성 초안은 **Neo4j에 전혀 쓰지 않는다**(§3.2). 초안은 Admin Chat의 `artifact` 메시지와 동일하게 **채팅 트랜스크립트(JetStream `TRANSCRIPT`, 이미 존재하는 인프라)에만 존재**한다. 따라서:
- 브라우저 새로고침/세션 종료 → 재진입 시 마지막 `artifact` 메시지를 트랜스크립트에서 복원하면 그만이다. 온보딩 전용 저장소를 새로 만들지 않는다(Simplicity First, Admin Chat과 동일 메커니즘 재사용).
- `Committing` 도중 중단(네트워크 끊김 등)은 Neo4j 트랜잭션이 커밋되지 않은 채로 남을 뿐이며, Neo4j 트랜잭션 자체가 타임아웃/커넥션 종료 시 자동 롤백된다 — 애플리케이션이 별도로 복구할 상태가 없다.
- `Provisioning` 중단(콘솔을 닫아도)은 이미 그래프에 커밋되어 있으므로 orchestrator가 그래프를 다시 읽고 계속 수렴한다 — 콘솔이 떠 있을 필요가 없다.

---

## 3. 대화 → 조직 정의안 → 그래프

### 3.1 LLM/코드 경계 (compute-over-inference 적용)

`10-core-architecture.md` §3.3의 표를 온보딩에 그대로 확장한다.

| 기능 | 처리 | 비고 |
|---|---|---|
| 자연어 지시 → 팀/직무 구조 초안 추출 | **LLM** | 비정형 입력의 구조화가 본질 |
| 직무 → 요구 역량 매핑 제안 | **LLM**, 단 선택지는 `:Capability` 카탈로그 코드로 **제한** | 자유 문자열 절대 금지(O3) |
| 새 `:Role`의 persona/CLAUDE.md 원문 저술 | **LLM**, **`:Role` 생성 시 1회만** | O4. 이후 재사용은 렌더링(코드) |
| 카탈로그 코드 존재 여부 확인 | **코드** (graph-write API가 draft 수신 시 검증) | I4와 동일 검사를 커밋 전에 조기 실행 |
| `OrgDefinitionDraft` → 실제 MERGE 쓰기 | **코드** | §9.2의 15단계 전부 |
| I1~I12 불변식 검사 | **코드** | LLM이 "이 정도면 될 것 같다" 판단하는 일이 없어야 함 |
| 리허설 더미 배정 | **코드** (§8의 실제 `assign()` 재사용) | 새 로직을 만들지 않는다 — 배정 코드가 두 벌이 되면 어긋남의 원천 |
| 재실행 시 diff 요약문("이번에 바뀐 것: …") | **LLM** (사람 가독성용) | 판정 자체는 코드(§6.1), 문장만 LLM |
| 진행 중 업무 영향 경고 문구 | **코드 템플릿** | 콘솔 ⓘ 팝오버와 동일 원칙 — 산문 불필요, 규칙명+대상 목록 |

### 3.2 중간표현: `OrgDefinitionDraft`

대화가 그래프에 직접 닿지 않도록 하나의 중간 표현을 둔다 — Admin Chat의 WBS `artifact` 카드와 정확히 같은 지위다. LLM은 이 JSON만 생성/수정하고, 승인 전까지 Neo4j는 건드리지 않는다.

```jsonc
{
  "draftId": "draft-2026-07-26-01",
  "baseOrgRevision": 7,          // 이 초안이 기준으로 삼은 조직 상태. 재실행 diff의 기준점(§6.1)
  "teams": [
    { "op": "upsert", "id": "team-mkt", "name": "마케팅팀", "slug": "marketing" }
  ],
  "roles": [
    { "op": "upsert", "id": "role-perf-mkt", "title": "퍼포먼스 마케터",
      "teamId": "team-mkt", "level": 2,
      "requiresCapabilities": [
        { "code": "cap.mkt.media_buying", "minLevel": 3 },
        { "code": "cap.mkt.reporting",   "minLevel": 3 }
      ],
      "basePermissionProfile": "builder",
      "needsPromptAuthoring": true          // 신규 Role → §4의 LLM 1회 저술 트리거
    }
  ],
  "parties": [
    { "op": "upsert", "kind": "AIAgent", "id": "agent-mkt-2",
      "name": "마케팅-AI-2", "runtimeClass": "standard",
      "modelAlias": "osiris/worker", "isMaster": false }
  ],
  "memberships": [
    { "op": "upsert", "partyId": "agent-mkt-2", "teamId": "team-mkt",
      "roleId": "role-perf-mkt", "isHead": false }
  ],
  "warningsAcknowledged": []       // 관리자가 "경고 확인하고 계속"을 누른 경고 코드 목록
}
```

`op`는 항상 `upsert`(§9.1 멱등 원칙과 정합)이거나 `close`(사람 퇴사/팀 폐지 — `validTo`/`closedAtRevision` 설정, §6.4)뿐이다. **`delete`는 이 표현에 존재하지 않는다** — 스키마가 삭제를 허용하지 않기 때문(P4).

### 3.3 강제 지점

`graph-write API`의 draft 수신 엔드포인트(커밋 이전, 대화 중간에도 매 턴 호출 가능)가 다음을 **코드로** 검증하고 위반 시 LLM에게 되돌려 재생성을 요구한다(대표에게 보이지 않는 내부 재시도 1회, 그래도 실패하면 대표에게 "이 역량은 카탈로그에 없습니다" 오류로 노출):
- `requiresCapabilities[].code`가 `:Capability.code` 카탈로그에 존재(I4의 조기 검사)
- `basePermissionProfile`이 §9.2 5단계의 3종 중 하나이거나 기존에 복제된 프로파일
- `runtimeClass`가 §9.2 6단계의 `:RoleResourceProfile` 중 하나
- `modelAlias`가 I10이 요구하는 3개 중 하나

이렇게 하면 커밋 시점의 I1~I12 검사는 "이미 조기 검증된 것들의 최종 확인"이 되어 리허설 실패율이 낮아진다(대표가 커밋 버튼을 누른 뒤에 처음 오류를 보는 상황을 줄인다).

### 3.4 온보딩 컨설턴트 — 자문 권한의 경계 `[2026-07-28 사용자 지시, O15]`

**H33.** 1단계 온보딩의 대화 주체를 **「온보딩 컨설턴트」**(이하 컨설턴트)라 부른다. 런타임 실체는 §1.3의 `bootstrap-master` 컨테이너 그대로이며 **새 컴포넌트가 아니다** — 이 절은 그 컨테이너가 온보딩 대화에서 **무엇으로 행동하는가**를 확정한다.

#### 3.4.1 이름이 바꾸는 것 — 그리고 바꾸지 않는 것

| | 지금까지 서술 | 컨설턴트로서 |
|---|---|---|
| 정체 | "온보딩 대화를 맡는 제네시스 에이전트" | **조직을 진단하고 구조를 제안하는 자문역** |
| 산출물 | `OrgDefinitionDraft` | 같음 — **제안서**다 |
| 권한 | `{draft, commit}` + 카탈로그 읽기 | **`{draft}` + 카탈로그 읽기.** `commit` 제거 |
| 확정 주체 | 모호했다(에이전트가 커밋 API를 부를 수 있었다) | **승인한 사람의 세션** |

**바뀌지 않는 것**: 컨테이너 정체성(그래프 노드 아님, `osiris.dev/genesis=true` 라벨, 임시), 생성 시점·주체(§1.0.7), 폐기 경로(§1.5), `OrgDefinitionDraft`의 형태(§3.2), 강제 지점(§3.3), LLM/코드 경계(§3.1). **이 절은 스코프 한 줄을 좁히고, 그 결과를 서술한다.**

#### 3.4.2 왜 `commit`을 빼는 것이 옳은가 — 세 가지

1. **O2가 정한 경계를 권한으로 강제한다.** O2는 *"LLM이 구조화된 중간표현을 만드는 것까지만"*이라고 정했는데, 그 경계는 지금까지 **관습으로만** 지켜졌다 — 스코프가 `commit`을 포함하고 있었으므로, 컨설턴트가 자기 초안을 스스로 확정하는 것을 **막는 것이 아무것도 없었다.** `20-ontology-schema.md` §7.1이 인가를 "선언에서 파생"으로 만든 이유와 같다: 지켜야 하는 규칙은 그것을 **표현 불가능하게** 만들어야 지켜진다.

2. **`60-identity-authn.md` §21.4-3과 정확히 같은 방향이다.** 그 항목은 *"LLM이 제안했다는 사실이 위임 규칙을 면제하지 않는다"*고 못박고, 온보딩 대화 경로도 DG1~DG5를 지나야 한다고 요구한다. 커밋 호출자가 사람의 세션이면 그 요구가 **자동으로** 충족된다 — 위임 판정은 세션의 `effective(party)`로 평가되고, 컨설턴트에게는 평가할 `:Party`가 애초에 없다.

3. **D13의 예외가 작아진다.** §1.3은 genesis 토큰의 인가가 `PermissionProfile` 파생이 아니라 **core에 하드코딩된 고정 스코프**라는 점을 *"D13에 대한 의도적이고 유일한 예외"*로 인정했다. 그 예외의 크기가 곧 위험의 크기다. `commit`을 빼면 예외에 남는 것은 **읽기와 초안 검증뿐**이며, 초안은 그래프에 살지 않는다(§8: 초안의 상태 저장 위치는 채팅 트랜스크립트다). 즉 **컨설턴트는 조직 그래프를 한 바이트도 바꿀 수 없다.**

#### 3.4.3 그러면 누가 커밋하는가 — 두 구간

```
[1회차 — 조직이 아직 없다]
  컨설턴트 ── OrgDefinitionDraft(제안) ──▶ 콘솔 카드
                                            │ 사람이 검토·편집·경고 확인
                                            ▼
                            master key 세션 ── POST commit ──▶ osiris-core
                                              (kind:"master-key", partyId 없음)
                                              → §9.2의 15단계 + OrgRevision{n:1}
                                              → byPartyId = 그 커밋이 만드는 초기 관리자

[2회차 이후 — 조직이 있다]
  컨설턴트(또는 진짜 agent-master) ── 제안 ──▶ 콘솔 카드
                                            ▼
                       org.admin 사람 세션 ── POST commit ──▶ osiris-core
                                              → DG1~DG5 평가(§21.4-3), OrgRevision +1
```

**1회차의 `:Party` 없는 커밋은 새 예외가 아니다.** `60-identity-authn.md` §4.1이 이미 그 구간을 위해 `kind:"master-key"` 세션을 정의했고(*"§3.1의 the only session without a :Party"*), §4.1 ⑤가 *"byPartyId = 그 초기 관리자 `:Person.id`"*라는 자기귀속을 규정했다. 이 절은 **그 세션에 커밋을 맡길 뿐, 새 주체를 만들지 않는다.** 구현상으로도 이미 존재한다 — `POST /api/onboarding/initial-admin`이 master-key 세션 전용이며 세계 상태 술어로 닫히는 형태다(`IMPLEMENTATION-NOTES.md` §101, `api/internal/people/me.go`).

#### 3.4.4 컨설턴트가 **할 수 있어야** 하는 것 (자문이 실제로 가능하려면)

"쓰기 없음"이 "아무것도 못 읽음"이 되면 자문이 성립하지 않는다. 컨설턴트는 다음이 필요하다:

| 필요 | 왜 | 이미 있는가 |
|---|---|---|
| `:Capability`/`:Permission`/`:PermissionProfile`/`:RoleResourceProfile` 카탈로그 읽기 | O3: 선택이지 발명이 아니므로 선택지를 알아야 한다 | **있다** — §8.5의 스코프에 "카탈로그 읽기 전용" |
| `POST /onboarding/draft` (검증 왕복) | §3.3의 조기 검증이 매 턴 일어나야 재생성 루프가 대표에게 안 보인다 | **있다** |
| `GET /onboarding/status` | 자기 진행 상황·기존 조직 요약 | **있다** |
| 2회차: 기존 조직 구조 읽기 | "지금 이렇게 되어 있으니 이렇게 바꾸자"가 자문의 본질 | **부분** — §9의 갭으로 남긴다(아래 3.4.6) |

#### 3.4.5 컨설턴트가 **절대 못 하는** 것 (구조적으로)

- `POST /onboarding/commit` — 스코프에서 제거(이 절)
- graph-write 일반 경로, 태스크 배정, 승인, 인박스 — §8.5가 이미 전부 403
- `:Person` 생성·역할 부여·등록 티켓 발급 — `60-identity-authn.md` §20.6의 경로는 전부 사람 세션 + DG 판정을 요구한다
- 자기 스코프 확장 — genesis 토큰은 컨테이너 ID에 바인딩되고(W4) TTL 24h(W2)이며 커밋 후 revoke된다(W3)

**W1~W5 대조: 전부 무영향.** 이 변경은 스코프를 **좁히는** 방향이므로 다섯 겹 폐쇄조건 중 어느 것도 약해지지 않는다 — W1(조직 있으면 403), W2(하드 TTL), W3(커밋 후 revoke + drain/delete), W4(발급 시 컨테이너 바인딩), W5(재온보딩이 창을 다시 열지 않음)는 문자 그대로 성립한다. 오히려 W3이 지키려던 성질("커밋 후에는 아무것도 못 쓴다")이 **커밋 전에도** 참이 된다.

#### 3.4.6 남는 갭 (§9로 이관)

- **2회차 이후 컨설턴트의 조직 읽기 스코프가 정의되지 않았다.** §8.5의 고정 스코프는 "카탈로그 읽기 전용"까지만 열거하며, 기존 팀/직무/사람 구조 읽기는 포함하지 않는다. 그런데 재온보딩 자문은 현재 구조를 알아야 성립한다. 두 방향이 있고 **이 문서가 고르지 않는다**: (a) 고정 스코프에 조직 읽기를 추가한다 — 예외가 다시 커진다, (b) 재온보딩은 컨설턴트가 아니라 **진짜 `agent-master`**가 맡는다 — 그쪽은 `:Party`이므로 `effective(party)`가 정상 적용되고 예외가 전혀 필요 없다. **(b)가 §1.4의 기존 흐름과 이미 정합한다**(온보딩 커밋 후 `bootstrap-master`는 폐기되고 진짜 Master가 뜬다) — 즉 컨설턴트는 **1회차 전용**일 수 있다. 감독 판단 필요.
- **컨설턴트의 페르소나/시스템 프롬프트를 누가 저술하는가.** §4.1의 소유권 표는 `:Role.promptTemplateMd`를 LLM이 1회 저술한다고 정하지만, 컨설턴트는 `:Role`이 없다(그래프 밖). 따라서 그 프롬프트는 **이미지에 구운 정적 자산**이어야 하며(온보딩이 결정할 것을 설치 시점에 굽지 않는다는 원칙의 예외가 아니다 — 컨설턴트는 조직의 산물이 아니라 조직을 만들기 위한 도구다), 그 파일의 소유자와 위치가 아직 어디에도 없다.

---

## 4. 에이전트 프로비저닝: 프롬프트·설정의 소유권

`10-core-architecture.md` §13이 이미 "온보딩은 그래프에 쓰기만 하면 되고 컨테이너 생성은 수렴 루프가 한다"고 결단했다. 이 절은 그 "그래프에 쓰는 것"의 **내용물**을 확정한다 — 누가, 언제, 무엇을 쓰는가.

### 4.1 소유권 표

| 산출물 | 저작 시점 | 저작 주체 | 저장 위치 | 렌더링 시점 | 렌더링 주체 |
|---|---|---|---|---|---|
| CLAUDE.md / persona 시스템 프롬프트 본문 | **`:Role` 최초 생성 시 1회** | **LLM** (bootstrap-master 또는 agent-master) | `:Role.promptTemplateMd` — **§9 갭, 스키마에 신규 필드 필요** | Apply/Recreate/config-reload마다 | **코드** (WorkloadDriver, 문자열 치환만) |
| `settings.json`(`permissions.allow/deny`, hooks) | 절대 저작 안 함 | — | 이미 §9.2 5단계의 `:PermissionProfile` | Apply/Recreate/config-reload마다 | **코드** (grant-catalog, `10-core-architecture.md` §8.1) |
| `--allowedTools` 인자 | 위와 동일 | — | 위와 동일 | 위와 동일 | **코드** |
| `mcp.json` | 위와 동일 | — | 위와 동일(grant → MCP 서버 매핑, §8.1 표) | 위와 동일 | **코드** |
| 태스크별 실행 프롬프트(`claude -p <<<`) | **인박스 메시지 도착마다** | — | — (저장되지 않음) | 매 태스크 | **코드** (`task-dispatcher`, `10-core-architecture.md` §5.2) |

**핵심 원칙**: LLM이 글을 쓰는 지점은 정확히 하나 — 새 `:Role`이 생길 때의 persona 원문. 그 외 모든 렌더링(개인차 반영 포함)은 결정적 치환이다.

### 4.2 Role → 개인차(AIAgent) 반영

`:Role.promptTemplateMd`는 플레이스홀더를 포함한 템플릿이다. 예:

```markdown
너는 OSiris 조직의 {{role.title}}이다. 소속: {{team.name}}.
이 역할의 표준 책임: {{role.responsibilitiesSummary}}
너의 보유 역량: {{capabilities | join}}  (레벨 3 미만은 "보조 역량"으로 취급)
너의 유효 권한: {{effectivePermissions | join}}
이 목록 밖의 행동(외부영향 액션)은 항상 승인 게이트를 통과해야 한다.
{{#if isMaster}}너는 전 조직의 유일한 Master다. 크로스팀 지시는 반드시 너를 거친다.{{/if}}
```

WorkloadDriver의 `Apply()`(`10-core-architecture.md` §1.4 단계 3, "렌더된 설정 … 기록")가 이 템플릿을 다음으로 채운다:
- `{{role.*}}` — 해당 `:AIAgent`의 유효 `:Membership`이 가리키는 `:Role`
- `{{capabilities}}` — `(:Party)-[:HAS_CAPABILITY]->` 조회 결과(개인차, §20 스키마 Q10과 동일 질의 재사용)
- `{{effectivePermissions}}` — §7.1의 `effective(party)` 계산 결과(이미 정의된 코드)
- `{{isMaster}}` — `:AIAgent.isMaster`

같은 `:Role`을 가진 두 `:AIAgent`(예: 마케팅-AI, 마케팅-AI-2)는 **템플릿은 같지만 렌더 결과가 다르다**(개인 역량 레벨·추가 권한이 다를 수 있으므로). 이것이 "직무는 재사용, 개인은 채움"의 구체적 구현이다.

### 4.3 재렌더링 트리거 (기존 §5.6 재확인 + diff 매핑)

`10-core-architecture.md` §5.6은 이미 규칙을 정해뒀다: 설정 변경은 **다음 태스크부터 적용**(config-reload, 컨테이너 무중단)이고, **이미지·리소스 변경만 컨테이너 교체**(recreate, drain 경유)를 유발한다. CLAUDE.md/settings.json/mcp.json은 전부 "설정"이므로 — `:Role`의 역량 요구가 바뀌거나 개인의 `HAS_CAPABILITY` 레벨이 바뀌거나 `PermissionProfile`이 바뀌어도 **컨테이너를 새로 만들지 않는다.** §6에서 이 규칙을 재실행 diff 표에 통합한다.

---

## 5. 검증 리허설

### 5.1 오류(진행 불가) vs 경고(진행 가능)

`20-ontology-schema.md` §9.3의 I1~I12를 그대로 집행하며, 등급은 스키마가 이미 정한 대로다.

| 등급 | 불변식 | 온보딩이 보여주는 것 |
|---|---|---|
| **오류** | I1, I2, I3, I4, I5, I8, I9, I10, I12 | 카드 상단에 빨간 배지 목록. 커밋 버튼 비활성. 각 항목에 "고치러 가기" 딥링크(예: I1 위반 → 해당 팀의 팀장 지정 UI로 스크롤) |
| **경고** | I6, I7, I11 | 카드 상단에 주황 배지 목록 + "경고 확인하고 계속" 체크박스. 체크해야 커밋 버튼 활성 |
| **온보딩 전용 경고 (신규, O8)** | **I13**: 이 변경으로 `revoked` 처리될 진행 중 `:Assignment` 목록 | §6.3에서 상세 |

경고를 체크하지 않고 진행하는 것은 **허용하지 않는다** — "허용할지"에 대한 답은 "확인 후에만 허용"이다(무시하고 몰래 진행 불가, 명시적 동의 필요). 이 체크 상태는 `OrgDefinitionDraft.warningsAcknowledged`에 기록되어 커밋 API 호출 시 함께 전송되고, `:OrgRevision.summary`에 "경고 N건 확인 후 진행"으로 남아 감사 가능하다.

### 5.2 트랜잭션 경계

```
graph-write API: POST /onboarding/commit(draft)
 BEGIN TRANSACTION (Neo4j 세션 1개)
   1. §9.2 단계 1-12에 해당하는 MERGE 쓰기 (draft의 upsert/close 전부)
   2. I1~I12 조회 (읽기 전용)
      IF 오류 존재: ROLLBACK, RETURN { errors: [...] }, STOP.  ← 아무것도 남지 않는다
      IF 경고 존재 AND !warningsAcknowledged 충족: ROLLBACK, RETURN { warnings: [...] }, STOP.
   3. 더미 WBS 리허설 (§5.3) — 같은 트랜잭션 안에서 실제 assign() 실행
      IF outcome != ASSIGNED for 어떤 역할: ROLLBACK, RETURN { rehearsalFailures: [...] }, STOP.
      ELSE: 더미로 만든 WorkItem/Assignment/AssignmentDecision을
            **같은 미커밋 트랜잭션 안에서** 삭제(O6 — 커밋된 적 없으므로 C5 위반 아님)
   4. :OrgRevision.n += 1, [:PREV] 연결, summary 기록
   COMMIT   ← 유일한 durable 순간. 부분 상태 없음.
 5. org.revision 이벤트 발행 (커밋 성공 후, NATS)
```

**§9.2 원문과의 차이 표시**: 스키마 문서의 표는 13(검증)→14(커밋)→15(리허설) 순서로 적혀 있어, 리허설이 커밋 **이후**에 실행되는 것처럼 읽힌다. 이 문서는 리허설을 커밋 **이전**, 같은 트랜잭션 안으로 당겨왔다 — 이유와 긴장은 §9.2(공백·모순)에서 명시적으로 짚는다. **이것은 이 문서가 상위 결정을 임의로 바꾼 것이 아니라, 순서가 야기하는 append-only 위반 가능성을 해소하기 위한 제안이며 스키마 소유자의 확인이 필요하다(§10 열린 질문 3).**

### 5.3 더미 WBS 배정 리허설

**대상**: 구성원이 1명 이상 있는 모든 `:Role` 각각에 대해 더미 `:WorkItem` 1개(`id: "wbs-rehearsal-<draftId>-<roleId>"`, `requiredCapabilities` = 해당 `:Role`의 `REQUIRES_CAPABILITY` 그대로 복사, `owningTeamId` = 해당 팀, `executionMode`는 `cap.exec.physical` 포함 여부로 자동 결정).

**실행**: `20-ontology-schema.md` §8.2의 `assign(task, asOf)`를 **그대로** 호출한다(별도 시뮬레이션 함수를 만들지 않는다 — 배정 로직이 두 벌이 되면 실제 배정과 리허설이 어긋날 수 있는 원천이 생긴다). `outcome == ASSIGNED`가 아니면(`DEGRADED`도 실패로 취급 — 리허설은 "완전한" 배정만 성공으로 본다) 해당 역할은 리허설 실패다.

**실패 시 화면에 보여줄 것**: §8.7의 `persistDecision`이 만드는 `[:CONSIDERED]` 근거를 그대로 사용해 — 콘솔의 `AssignmentRationalePopover`(40-console-ia.md C절)와 **동일한 컴포넌트**로 "이 역할을 맡을 수 있는 사람이 아무도 없습니다 + 후보별 탈락 사유"를 보여준다. 새 UI를 만들지 않는다.

### 5.4 컨테이너 스모크 테스트는 포함하지 않는다 — 명시적 결정

요구사항이 제기한 질문("배정 경로만 검사하면 컨테이너가 실제로 뜨는가는 미검증")에 대한 답: **포함하지 않는다.**

이유:
1. 커밋 전(revision 미확정) 시점에 실제 컨테이너를 띄우는 것은 "그래프가 유일한 진실"(D1) 경계를 흐린다 — 임시로 뭔가를 만들었다가 다시 지우는 특별 경로가 필요해지고, 이는 정확히 §1.4가 기각한 "슬롯 풀" 안과 같은 종류의 복잡도다.
2. 커밋 직후 몇 초 안에 수렴 루프가 어차피 실제 컨테이너를 만든다(§7.1) — 리허설이 흉내 낸 것을 또 만드는 것은 중복이다.
3. LLM 호출 토큰 비용 없이(리허설은 §8 배정 코드만 실행, `osiris-core`에는 애초에 `claude` 바이너리가 없다 — D12) 끝낼 수 있는 검사를 컨테이너 기동까지 늘리면 굳이 비용/시간을 태우는 것이다.

**대신**: "컨테이너가 실제로 뜨는가"는 **커밋 이후의 별도 신호(Provisioning 단계, §2)**로 다룬다. 이는 이미 존재하는 관측 채널(`/ops/agents/health`, `org.node.status_changed`, `readyz`)을 재사용하며, 온보딩은 여기에 새 메커니즘을 추가하지 않고 **소비만** 한다(§8).

### 5.5 실패를 무엇으로 제시하는가 (실제로 고칠 수 있으려면)

각 오류/경고 항목은 다음 3가지를 항상 함께 갖는다(콘솔 카드에 인라인 표시):
1. **어떤 불변식**(코드, 예 `I5`)과 **사람이 읽을 한 줄**("cap.exec.physical은 사람만 보유할 수 있습니다")
2. **어떤 노드/엣지가 원인인지**(예: `개발자A(AIAgent)`가 `cap.exec.physical`을 보유 — 카드에서 해당 역량 배지 클릭 시 제거 액션 제공)
3. **고치는 액션**을 카드 안에서 바로 실행 가능(추가 화면 전환 없음 — Admin Chat의 "담당자 배지 클릭 → 즉시 재배정"과 동일한 인터랙션 재사용)

---

## 6. 재실행·변경 감지·롤백

### 6.1 diff 단위

`AgentWorkloadSpec.SpecHash`(`10-core-architecture.md` §6.2에 이미 정의)를 그대로 diff 단위로 쓴다. 온보딩 재실행이 만드는 새 `OrgDefinitionDraft`가 커밋되면, 수렴 루프가 다음 tick(또는 `org.revision` 이벤트 즉시)에 `DeriveAgentWorkloads()`를 다시 돌려 새 `SpecHash`를 계산하고 기존 것과 비교한다 — **이 비교 자체는 온보딩의 책임이 아니라 이미 존재하는 orchestrator 로직**이다. 온보딩이 새로 규정해야 하는 것은 "그래프의 무엇이 바뀌면 SpecHash의 어느 구성요소가 바뀌는가"뿐이다.

### 6.2 변경 종류 → 파급 매핑

| 그래프 변경 | `AgentWorkloadSpec` 영향 | 파급 | 근거 |
|---|---|---|---|
| `:Role.requiresCapabilities` 변경 | 없음(개인 역량이 진실, Role은 정책일 뿐) | 없음. 다음 배정부터 새 기준 적용 | 배정은 그래프를 매 호출 시 조회(§8.3) — 캐시 없음 |
| `:Party.HAS_CAPABILITY` 추가/변경 | CLAUDE.md 렌더 입력 변경 | **soft reload**(다음 태스크부터 새 persona) | §5.6 "설정 변경은 다음 태스크부터" |
| `:PermissionProfile.GRANTS` 변경(버전 증가) | `settings.json`/`allowedTools`/`mcp.json` 변경 | **soft reload** | 위와 동일. `renderedFor` 리비전 필드로 추적(§6.3 CRD 예시) |
| `:Membership`의 팀 변경(이적) | NATS 계정(팀 소속) + PermissionProfile(base) 변경 | **recreate**(drain 후 재생성) | NATS 사용자는 팀 계정 하위로 생성되므로(§7.3) 팀이 바뀌면 신원 자체가 바뀐다 — Apply() 단계 4(NATS 사용자 생성/갱신)가 재실행되어야 함 |
| `:AIAgent.runtimeClass`/`image`/`modelAlias` 변경 | 리소스/이미지 | **recreate** | §5.6 "이미지·리소스 변경만 컨테이너 교체" |
| `:AIAgent.desiredState = Suspended` | Desired | **drain, 컨테이너 삭제, 볼륨 보존**(§5.3 상태기계) | resume 시 sessionId로 재개 가능 |
| `:Membership.validTo` 설정(퇴사/이동 완료) 또는 `:AIAgent.closedAtRevision` | desired 집합에서 제외 | **drain(660s) + delete**(볼륨 회수 정책은 §6.4) | 기존 고아 정리 경로(§1.4) |
| `:Team` 신설/`:Role` 신설, 구성원 아직 없음 | 신규 desired 없음(구성원 있어야 워크로드가 생김) | 없음 | `:AIAgent`가 없으면 워크로드도 없다 |

### 6.3 진행 중 업무 처리 — I13 (온보딩 전용 경고)

**질의**: 커밋하려는 diff가 어떤 `:Party`의 `Membership`/`HAS_CAPABILITY`/`closedAtRevision`을 바꾸는지 계산한 뒤, 그 `:Party`들에 대해 다음을 조회한다.

```cypher
MATCH (a:Assignment)-[:TO]->(p:Party)
WHERE p.id IN $affectedPartyIds
  AND a.status IN ['active','awaiting_human']
MATCH (a)-[:OF_TASK]->(w:WorkItem)
RETURN p.id, p.name, a.id AS assignmentId, w.id AS taskId, w.title
```

결과가 비어 있지 않으면 **I13 경고**로 표시: "이 변경은 N건의 진행 중 업무에 영향을 줍니다: [목록]. 계속하면 이 업무들은 재배정 대상으로 돌아갑니다." 관리자가 확인 체크 후 커밋하면, 같은 커밋 트랜잭션 안에서:

1. 영향받는 `:Assignment.status = 'revoked'`(삭제 아님, P4)
2. 해당 `:WorkItem.status`를 `planned`로 되돌림(단, `in_progress`에서 이미 만들어진 `:Artifact`/부분 진행은 그대로 둔다 — 다음 배정자가 이어받을 근거로 남는다)
3. **컨테이너 레벨**: 팀 이적·역량 변경만으로는 컨테이너가 즉시 죽지 않는다(§6.2, soft reload 또는 recreate는 수렴 루프가 별도 스케줄로 수행하며 drain 유예 660초를 그대로 존중한다). 따라서 진행 중이던 턴은 자연 완료되거나 §5.5(core-architecture)의 체크포인트 경로로 중단된다.
4. **정말로 조직을 떠나는 경우**(`closedAtRevision` 설정, 사람이든 AI든)만 예외: 이 경우 재개할 사람이 없으므로(`--resume`할 `:AIAgent`가 이미 그래프에서 사라짐), 체크포인트가 있어도 **재개하지 않고** 위 1-2단계로 확정 처리한다. **잃는 것을 정직하게 인정한다**: 그 시점까지의 LLM 진행 컨텍스트(체크포인트)는 폐기된다 — 이는 "이미 승인 게이트 뒤에 있는 외부영향 액션은 중복 실행되지 않는다"(§5.5 at-least-once 논거)와 같은 급의 트레이드오프이며, 부분 산출물(`:Artifact`)은 남으므로 완전한 손실은 아니다.

### 6.4 사람이 떠나거나 팀이 사라질 때

- 사람 퇴사: `:Person.closedAtRevision` 설정 + 모든 유효 `:Membership.validTo` 설정. §6.3의 I13 경고가 그 사람의 진행 중 업무를 잡아낸다. 물리 업무(`cap.exec.physical`)만 수행 가능한 업무였다면 재배정 시 I6(`CAPABILITY_GAP`)이 다시 뜰 수 있다 — 이는 정상이며, "이 역량을 가진 사람이 이제 없다"는 신호가 정확히 온보딩이 다음에 채용을 트리거해야 함을 의미한다(§8.6 FB5와 동일한 신호 경로).
- 팀 폐지: `:Team`에는 `validTo` 필드가 스키마에 없다(§3.1의 `:Party` 공통 속성 `closedAtRevision`만 있음) — **팀도 `:Party`이므로 `closedAtRevision`으로 처리**한다. 팀이 사라지면 그 팀 소속 모든 유효 `:Membership`도 함께 닫아야 하는데, 이는 **원자적으로(같은 트랜잭션)** 이루어져야 한다 — 그렇지 않으면 "팀은 없는데 유효 멤버십은 있는" 순간이 생겨 I1(정확히 1개의 팀장 멤버십)이 무의미해진다. graph-write API의 "팀 폐지" 연산은 이 연쇄 닫기를 하나의 원자적 단위로 구현한다(단일 MERGE/SET 문이 아니라 여러 문이지만 §5.2의 같은 트랜잭션 안).

### 6.5 멱등성

`OrgDefinitionDraft`를 아무 변경 없이 그대로 두 번 커밋하면(재실행이지만 실제로는 아무것도 안 바뀐 경우) — MERGE 자체는 멱등이지만(§9.1), **`:OrgRevision`을 매번 +1 하면 안 된다**(무의미한 리비전 증가가 반복되면 수렴 루프가 매번 재계산을 트리거하고, 콘솔 히스토리가 "아무 일도 없었는데 리비전만 오른" 잡음으로 채워진다). 따라서:

> **graph-write API는 커밋 직전, "이 draft를 적용한 결과가 현재 그래프 상태와 완전히 동일한가"를 확인하고, 동일하면 리비전을 올리지 않고 `{ noop: true }`를 반환한다.**

이는 C8("모든 의도 변경 트랜잭션은 OrgRevision을 정확히 1 증가")과 모순되지 않는다 — 실질 변경이 없는 호출은애초에 "변경 트랜잭션"이 아니라고 해석한다(§9의 열린 질문으로도 표시 — 스키마 소유자 확인 필요).

### 6.6 부분 실패 롤백

§5.2에서 이미 명시한 대로, 15단계 전체(카탈로그 upsert 제외 — 이건 부트스트랩 소유, 온보딩 재실행 범위 밖) + I1~I12 + 리허설이 **하나의 Neo4j 트랜잭션**이므로, 어느 단계에서든 실패하면 **네이티브 트랜잭션 ROLLBACK**으로 전체가 원상복귀한다. 별도의 사가(saga)형 보상 트랜잭션이 필요 없다 — 이것이 애초에 트랜잭션 경계를 좁게(개별 MERGE마다) 쪼개지 않고 하나로 묶은 이유다.

---

## 7. 1단계 시퀀스 / 2단계 차이

### 7.1 온보딩 커밋 직후 — 1단계(compose) 순서

```
①  관리자가 Onboarding 카드에서 [승인 및 적용] 클릭
②  콘솔 → osiris-api → osiris-core: POST /onboarding/commit(draft)
③  osiris-core: Neo4j 트랜잭션 시작
      §9.2 단계 1-12 MERGE 쓰기(delta만, 재실행이면) → I1~I12 검사 → 리허설(§5.2-5.3)
④  전부 통과 → :OrgRevision.n += 1 → COMMIT (durable 순간)
⑤  osiris-core: "org.revision" 이벤트를 NATS에 발행
⑥  osiris-orchestrator: 이벤트 즉시 수신(+ 어차피 15초 주기로도 도는 중)
      desired := graph.DeriveAgentWorkloads()   ← 신규/변경된 :AIAgent 전부 포함
      actual  := driver.Observe(ctx)            ← genesis 라벨 제외(§1.3)
      신규 AIAgent마다 driver.Apply():
        볼륨 생성 → 자격증명 파일 기록(게이트웨이 토큰 IssueToken 호출 포함)
        → settings.json/mcp.json/CLAUDE.md 렌더(§4) → NATS 사용자 생성(팀 ACL, §7.3)
        → ContainerCreate(라벨: employee-id/team/spec-hash) → ContainerStart
      변경만 있는 기존 AIAgent → config-reload 발행 또는 recreate(§6.2 표에 따라)
      제거 대상 AIAgent → Drain(660s) → Delete
⑦  각 신규 컨테이너의 supervisor가 부팅 → /readyz 200 → orchestrator 런타임 캐시 갱신
      → Neo4j :AgentRuntime 캐시 갱신(observedAt 포함, P2) → org.node.status_changed 발행
⑧  콘솔 Org 화면에 신규 노드가 "가동"으로 전환되는 것이 실시간(SSE)으로 보임
⑨  (최초 온보딩에 한함) 진짜 agent-master가 Ready 확인되면
      → bootstrap-master 명시적 Drain+Delete(§1.5)
⑩  GET /onboarding/status: 정의 단계는 "완료"(revision N), 프로비저닝 단계는
      "N/M Ready"로 별도 진행률 노출(§8)
```

### 7.2 2단계(k3s)에서 달라지는 것

> **`[2026-07-28 K2 전환]`** 이 절 제목과 아래 표는 원래 "1단계(compose)에서 2단계(k3s)로 갔을 때 무엇이 달라지는가"를 다뤘다. K2/K13(`70-kubernetes-deployment.md`, 사용자 확정)이 배포를 **k8s 단일 대상으로 전면 재작성**하기로 하면서, 이 비교의 전제인 "1단계가 먼저 있고 2단계가 그로부터의 마이그레이션"이라는 순서 자체가 사라졌다. 아래 표는 이제 **"1단계 vs 2단계"가 아니라 "폐기된 compose 배포 경로 vs 현재 유일한 배포 경로(k8s)"**로 읽어야 한다 — 오른쪽 열의 서술이 여전히 유효한 현재 상태이고, 왼쪽 열은 만들어지지 않는 과거 경로다. 표는 삭제하지 않고 이력으로 남긴다(정정 이력 보존 원칙).

| 단계 | 1단계 | 2단계 |
|---|---|---|
| ③④ (Neo4j 트랜잭션 + 리허설) | 동일 | **완전히 동일** — 배포 단계와 무관한 불변 자산(20-ontology-schema.md 머리말) |
| ⑤⑥ 이벤트 → 수렴 | `osiris-orchestrator` reconcile | `osiris-operator`가 같은 `DeriveAgentWorkloads()` 코드로 반응(`10-core-architecture.md` §6.2) |
| Apply() | Docker Engine API 직접 | `Agent` CR 생성 → operator가 Pod+PVC+SA+Secret+NetworkPolicy 렌더 |
| bootstrap-master 존재 여부 | **필요**(§1) | **불필요** — 2단계는 항상 1단계에서의 **마이그레이션**(`osiris-migrate-to-k3s`, §4.2)이며 Neo4j dump를 그대로 복원하므로, 2단계가 처음 뜨는 순간 조직은 이미 비어 있지 않다. 즉 부트스트랩 역설은 **1단계 최초 설치 시 정확히 한 번만** 풀면 되는 문제다 **[2026-07-28 K2 전환]** ~~불필요~~ → **필요.** 이 판정 전체가 뒤집힌다. K2/K13 전면 재작성에는 "1단계에서의 마이그레이션"이 없다 — compose 경로가 아예 존재하지 않으므로 Neo4j dump를 복원해 줄 이전 단계 자체가 없다. 따라서 **k8s 최초 기동이 곧 빈 조직이고, bootstrap-master는 k8s에서도 다시 필요하다.** 이 최초 기동 시 자격증명(구 genesis 토큰에 대응하는 것)을 K7이 어떻게 세우는지는 `70-kubernetes-deployment.md` §4.1~§4.2(K7) 참고 — 전용 ServiceAccount + projected token + core의 TokenReview 검증으로, `bootstrap-master`의 정체성·폐기 경로는 아래 §1.3/§1.5 정정과 함께 유지된다 |
| 볼륨/자격증명 | 호스트 bind + orchestrator 파일 기록 | PVC + k8s Secret projected volume(같은 컨테이너 내 경로, supervisor 무변경) |
| 재실행 diff 판정 | 동일 SpecHash 로직 | 동일 SpecHash 로직, CR spec에 영속화되어 컨트롤러 재기동에도 desired 보존(§6.1) |

**[상위 문서 갭]**: 2단계를 **처음부터**(1단계를 거치지 않고 k3s로 그린필드 시작) 하는 경로는 `10-core-architecture.md` 어디에도 없다(§4.2는 항상 "이미 도는 1단계에서 전환"을 전제). 이 문서도 그 경로를 정의하지 않는다 — §9·§10에서 열린 질문으로 남긴다. **`[2026-07-28 K2 전환]`** 이 갭은 이제 갭이 아니라 **표준 경로**다 — K2/K13 이후 모든 설치가 "k8s로 그린필드 시작"이며, 그 최초 기동에서 bootstrap-master가 필요하다는 것이 바로 위 행의 정정이다.

---

## 8. 콘솔(`GET /onboarding/status` 등) 계약 충족

| 콘솔 요구 | 이 문서의 충족 방식 |
|---|---|
| `GET /onboarding/status`: 팀 수·직무 수·capability 매핑 비율·승인정책 정의 여부 | §3.2 `OrgDefinitionDraft`의 각 배열 길이 + I1~I12 검사 결과 집계(20-ontology-schema.md §9.3 "이 검사 결과를 단계별로 집계해서 반환") |
| 스테퍼 ①팀 ②역할/직무 ③capability ④승인정책 | 이 4단계는 §3.2 draft의 필드에 각각 대응(teams/roles/capability 매핑 완성도/policy). **그러나 이 문서의 파이프라인은 4단계 이후에 리허설(§5)·커밋(§7)·프로비저닝(§8) 3단계가 더 있다** — 콘솔 스테퍼가 "④에서 끝"으로 보이는 것은 실제 완료 지점과 어긋난다. **[상위 문서 갭]**: 5번째 스텝("검증·적용") 추가를 권고하되 40-console-ia.md를 이 문서가 직접 수정하지 않는다 |
| `onboarding.step_changed` 이벤트 | 이 문서의 §2 상태기계 전이(Drafting→Reviewing→Committing→Committed→Provisioning→Steady) 각각에서 발행. 콘솔은 기존 이벤트 이름을 그대로 쓰되 payload에 `phase` 필드로 6개 상태 중 하나를 실어야 함(현재 문서는 필드 존재를 가정하지 않으므로 백엔드 구현 시 확정 필요) |
| Onboarding 화면의 "대화+인라인 카드" 패턴 재사용 | §3.2 `OrgDefinitionDraft`가 Admin Chat의 `artifact` 메시지와 동일한 지위(§2의 상태 저장 위치도 동일: 채팅 트랜스크립트) |
| 조직 변경 시마다 재실행(요구 4) | §6 전체가 이 요구의 구현. 사이드바 "조직 변경 감지 시에만 뱃지"는 `GET /onboarding/status`가 `baseOrgRevision < currentOrgRevision`이거나 미해결 I6(역량 갭, FB5 트리거)가 있을 때 뱃지 신호를 계산하면 됨 — 새 메커니즘 불필요, 기존 `:OrgRevision` 비교로 충분 |

---

## 9. 상위 문서에서 발견한 공백·모순

| # | 위치 | 내용 |
|---|---|---|
| G1 | `10-core-architecture.md` §4.1, "부트스트랩 직후 조직은 비어 있다" 문단 | "팀·직무·에이전트는 온보딩이 **§11의 프로비저닝 인터페이스**로 생성"이라고 되어 있으나, 그 문서의 §11은 "미해결 쟁점 13개"이며 프로비저닝 인터페이스가 아니다. 실제로 의도한 참조는 `20-ontology-schema.md` §9로 보인다 — **댕글링 크로스레퍼런스**. 이 문서는 §9(스키마 문서)를 인터페이스로 사용했다 |
| G2 | `20-ontology-schema.md` §9.2 단계 13→14→15 순서 vs §11 제약 C5 | ~~표는 "13 검증(실패 시 리비전 안 올림) → 14 커밋(리비전+1) → 15 리허설(배정 후 롤백)" 순서다~~ **[해소됨 — 후속 확인]** `20-ontology-schema.md` §9.2가 그 사이 순서를 이 문서의 재해석과 정확히 같은 방향(리허설을 커밋 이전, 같은 트랜잭션 안)으로 이미 정정했고, 그 문서 자신이 "이 정정은 `30-onboarding-provisioning.md` §5.2의 재해석과 일치하며, 그 문서의 열린 질문 1과 §9 G2를 해소한다. 온보딩 문서 쪽 변경은 필요 없다"고 명시했다. `31-onboarding-boundary.md` §6 B1에서 이 감사로 확인함 — 이 문서는 그 명시적 확인을 반영해 §10 열린 질문 1을 닫는다 |
| G3 | `20-ontology-schema.md` 전체 | `:Role`에 프롬프트/persona 콘텐츠를 담을 필드가 없다. `10-core-architecture.md` §5.6은 "설정 파일은 그래프에서 파생"이라고만 하고 CLAUDE.md/시스템 프롬프트의 **원문이 어디서 오는지는 정의하지 않는다.** 이 문서는 `:Role.promptTemplateMd` 신규 필드를 제안했다(§4) — 스키마 문서에 반영이 필요하다 |
| G4 | `10-core-architecture.md` §7.1 유효 권한 계산 | `effective(party)`가 `:Party`를 전제로 정의되어 있어, 조직 밖 존재(이 문서가 도입한 `bootstrap-master`)의 인가를 표현할 방법이 없다. 이 문서는 하드코딩 예외로 처리했다(§1.3) — 권한 모델이 "선언 하나에서 4개 층위 파생"(D13)을 표방하는데 예외가 생기는 것은 정직하게 기록해 둔다 |
| G5 | `40-console-ia.md` #6 Onboarding 스테퍼 | ①팀→②역할→③capability→④승인정책 4단계로 "완료"를 표현하는데, 실제로는 그 뒤에 리허설·커밋·프로비저닝이 있다(§8). 스테퍼가 4단계에서 끝나면 관리자가 "④가 끝났으니 다 됐다"고 오인할 수 있다 |
| G6 | `10-core-architecture.md` §4.2 (2단계 부트스트랩) | 2단계를 1단계 없이 그린필드로 시작하는 절차가 어디에도 없다(§7.2에서 지적) |
| G7 | `20-ontology-schema.md` §3.4, `:Team` 노드 | `:Party` 공통 속성으로 `closedAtRevision`은 있지만, 팀이 폐지될 때 그 아래 모든 `Membership`을 원자적으로 닫아야 한다는 규칙이 명시되어 있지 않다(§6.4에서 이 문서가 보완) |
| G8(신규) | `10-core-architecture.md` §4.1(부트스트랩 절차), §8.5(genesis 토큰 발급 주체) | 두 절 모두 "`bootstrap-master`는 `osiris-up`이 코어 기동과 함께 1회 직접 생성"을 전제로 서술되어 있다(§4.1 4단계 직후 문단, §8.5 genesis 토큰 표의 "발급 주체" 행 — 발급 자체는 무변경이나 그 표가 컨테이너 생성과 토큰 발급을 나란히 같은 시점으로 읽히게 서술함). 이 문서 §1.0.7이 생성 시점·주체를 정정했으므로 두 절의 서술이 최신 사실과 어긋난다 — **이 문서가 그 두 절을 직접 고치지 않는다**(다른 워커 소유), 여기 명시적으로 표시만 한다. **[2026-07-27 해소, 3차]** 이 갭은 이후 실제로 해소됐다 — `10-core-architecture.md` §4.1/§8.5가 생성 시점/주체를 정정했고(2차), 이어서 genesis 토큰의 **발급 시점 자체**도 컨테이너 생성 시점으로 옮겨 발급 주체를 `osiris-core`로 바꿨다(3차, §1.0.7 참고). 이 문서 §1.0.7·O12도 3차 정정을 반영해 갱신했다 |
| G9(신규) | `10-core-architecture.md` §8.4(자격증명 표), 이 문서 전체 | 에이전트별 git PAT("드라이버가 발급/주입"이라고만 되어 있음)의 **최초 값을 누가 어디서 입력하는지**가 어느 문서에도 없다 — `31-onboarding-boundary.md` §2.2가 이 감사에서 새로 발견한 갭이다. 프로바이더 키와 같은 성질(사람이 발급한 값을 시스템에 최초로 넣어줘야 함)인데 온보딩 오퍼레이션·화면 어디에도 대응 단계가 없다 |
| G10(신규) | `omniroute/operations.mjs` | 게이트웨이 토큰의 **진짜 회전(기존 키 폐기 후 재발급)** 오퍼레이션이 없다 — `issueOrGetGatewayToken`은 있으면 재조회만 한다(§1.0.6). 유출 의심 시 사람이 omniroute UI에서 직접 삭제해야 한다 |

---

## 10. 열린 질문

1. ~~**§9 G2** — 리허설(§9.2 단계 15)의 "배정 후 롤백"이 정말 커밋 **이후**(문자 그대로)를 의도한 것인지, 아니면 이 문서가 재해석한 것처럼 커밋 **이전**(같은 트랜잭션 내 롤백)을 의도한 것인지 스키마 소유자의 확인이 필요하다.~~ **해소됨** — `20-ontology-schema.md` §9.2가 순서를 커밋 이전으로 직접 정정했고 이 문서와의 정합을 스스로 확인했다(§9 G2 갱신 참고).
2. **§9 G3** — `:Role.promptTemplateMd`(persona 원문 저장 필드)를 온톨로지 스키마에 정식으로 추가할지, 아니면 그래프 밖(예: 별도 설정 저장소)에 둘지. 그래프 밖에 두면 "온보딩은 그래프에만 쓴다"(§9.1)는 경계를 깨게 되므로, 이 문서는 그래프 안(신규 필드)을 전제로 설계했다.
3. **§9 G6** — 2단계를 1단계 없이 그린필드로 시작하는 배포가 실제로 필요한 시나리오인지(예: 처음부터 멀티노드가 확정된 고객사), 아니면 "항상 1단계 → 마이그레이션"이 영구 정책인지.
