# OSiris 액션 레이어 (Action Layer) — `:ActionType`

> 작성일: 2026-08-12
> 입력: `10-core-architecture.md` §7(버스)·§8(권한·자격증명·격리), `20-ontology-schema.md` §1(P1~P7)·§3.4·§7(권한 모델)·§11(제약)·§12(배제), `60-identity-authn.md` §19(기계 주체)·§20(조직 역할)·§21(권한 상승 분석, DG1~DG5), `30-onboarding-provisioning.md` §1.0/O2/O3/O15, `70-kubernetes-deployment.md` K1~K13, 실측(`orchestrator/internal/catalog/catalog.go`, `api/internal/authz/delegation.go` 존재 확인).
> 범위: **"무엇이 실행 가능한 행위이고, 그 행위가 무슨 게이트를 받는가"를 데이터로 표현하는 층.** `:ActionType` 노드, 정의/실행 평면 분리, 발행·버전 의미론, 조합(composition) 규칙, 오염(taint) 모델, 기존 다섯 기구의 흡수 경로.
> 비범위: **능력 스택(capability stack)** 모델 자체와 능력 키의 해석 — `81-capability-stacks.md`가 소유한다(이 문서는 `:ActionType` 쪽 계약, 즉 §5.4의 소비 규칙만 정의한다). 인바운드 메일 스택의 구현과 그것이 부착하는 출처 메타데이터(`81 §4`). 승인 UI의 화면 사양(`40-console-ia.md`/`41-console-design-system.md`). 배정 알고리즘(`20 §8`).
>
> **용어 주의**: 이 문서에서 **능력 키(`stk.*`)** 와 **역량(`:Capability`, `cap.*`)** 은 다른 것이다 — 전자는 "이 설치에 이 기능이 존재하는가", 후자는 "누가 그것을 잘하는가"다. `81 §2.1`의 경고와 CS1을 그대로 따르며, 두 어휘를 섞지 않는다.
>
> **표기 규칙**: 표기 없는 서술 = 이 문서의 확정 결정. `[미검증]` = 공식 출처·실측으로 확인하지 못한 가정. `[갭]` = 이 문서가 발견했으나 스스로 채우지 않고 표시만 한 공백. `[상위 문서 갭]` = 상위 문서에서 발견했으나 임의로 고치지 않은 지점.
>
> **배포 전제**: 배포는 **k8s 단일 대상**이다(`70` K1~K13). 이 문서는 compose 경로를 전제하는 어떤 것도 설계하지 않으며, `10`에 남아 있는 `[1단계]` 서술은 폐기된 과거 경로로 읽는다.

---

## 0. 결정 요약

| # | 결정 | 한 줄 근거 |
|---|---|---|
| **AD1** | **`:ActionType`은 그래프 네이티브 1급 노드다.** 코드 상수가 아니라 Neo4j 노드이며, 콘솔에서 질의·편집 가능하다 | 온톨로지에 명사만 있고 동사가 없다. 동사를 코드에 두면 "이 조직이 무엇을 할 수 있는가"가 온톨로지 질의로 답해지지 않는다 |
| **AD2** | AD1은 **세 안전장치를 전제로만 성립한다** — (a) 정의 평면/실행 평면 분리, (b) 발행·버전 의미론, (c) 선언은 데이터·실행체는 코드 | 보안에 관련된 표가 런타임에 변경 가능해지는 것이 AD1의 대가다. 셋 중 하나라도 빠지면 AD1은 기각되어야 한다(§3.3) |
| **AD3** | **새 권한 코드는 정확히 1개**: `actiontype.write`. 실행 인가 어휘는 한 글자도 늘리지 않는다 | `60 §19.6`("기계 주체를 위해 만든 새 권한 코드는 0개다")이 세운 규범. 어휘가 늘면 인가 판정이 흩어진다 |
| **AD4** | **새 인가 판정 함수를 만들지 않는다.** DG6~DG8은 `authz.CheckDelegation`에 추가되는 규칙이며, 액션 게이트 계산(`gateFor`)은 **판정이 아니라 순수 파생**이다 | HI16(위임 판정 단일 함수, `60 §21.3`)을 깨지 않는 유일한 방법. AC7이 이것을 린트로 검사한다 |
| **AD5** | 리비전 바인딩 단위는 **세션이 아니라 턴(태스크)**이고, 판정은 `bound` 리비전과 `current` 리비전의 **엄격한 쪽**을 쓴다 | "게이트가 도중에 사라지지 않는다"와 "강화가 즉시 반영된다"를 동시에 만족시키는 유일한 병합 방향 |
| **AD6** | **결정적 검사는 게이트 요청 시점이 아니라 집행(execution) 시점이다.** core는 승인 결정을 집행하기 직전에 액션 정의를 현재 리비전으로 다시 읽는다 | 승인 대기가 최대 24시간(`10 §8.2`)이다. 그 사이의 정의 변경에 대해 TOCTOU를 닫는 지점은 집행뿐이다 |
| **AD7** | **조합 언어는 튜링 완전이 아니다.** `COMPOSED_OF`는 순차 합성만 표현하며 분기·반복·계산·변수가 없다 | "데이터가 코드가 되지 못한다"의 문자 그대로의 형태. 분기 하나를 허용하는 순간 `:ActionType` 표는 인터프리터 입력이 된다 |
| **AD8** | **최엄격 상속(strictest-inherits) + 집행 시점 재계산.** 조합 액션의 게이트·오염 등급·효과 등급은 부품의 join이며, 저장된 값은 캐시일 뿐 집행자는 leaf에서 다시 계산한다 | join만으로는 "위조된 조합 행"을 막지 못한다. 재계산이 있어야 조합이 다운그레이드 공격이 되지 않는다(§6.4) |
| **AD9** | **오염 등급의 소유자는 `osiris-core`다.** 에이전트·supervisor가 제출한 오염 값을 core는 신뢰하지 않는다 | `10 §8.2`의 강제/협조 구분. 에이전트 컨테이너 안의 무엇도 마지막 방어선이 될 수 없다 |
| **AD10** | **오염 해제는 조작이 아니라 세션 폐기다.** 세션 내에서 오염 등급은 단조 비감소이며, 낮추는 유일한 연산은 새 `sessionId`로 시작하는 것이다 | "이 부분은 무시해도 된다"는 판단을 LLM이 내리게 하는 순간 방어가 프롬프트 방어로 되돌아간다 |
| **AD11** | **`requiresCapability`는 양면 계약이다** — 요구 **능력 키**가 하나라도 `available`이 아니면(즉 `degraded` 또는 `unavailable`, `81 §2.3` C-4의 3상태) 카탈로그에서 `disabled`로 렌더되고, 그럼에도 도달한 실행 요청은 **409 `CAPABILITY_UNAVAILABLE`로 하드 거부**된다 | 필터만 있으면 "카탈로그에 안 보이지만 호출하면 되는" 경로가 남는다. 코드에서 `if (mailEnabled)` 분기를 없애는 것이 목적이므로 두 면이 모두 필요하다. `degraded`를 가용으로 치지 않는 이유는 §5.4.1 |
| **AD12** | **`10 §8.5` genesis 스코프 허용목록은 흡수하지 않는다.** 영원히 코드 상수로 남는다 | genesis는 조직(=그래프)이 비어 있는 구간의 주체다. 그 구간의 특권을 그래프 데이터로 표현하면 (c)가 금지하는 바로 그것 — 데이터로 특권 창을 여는 경로 — 이 생긴다(§8.5) |
| **AD13** | `:Permission.gate` / `approverRole` / `limitJson` 세 필드의 **권위를 `:ActionType`으로 이관**하고, `:Permission` 쪽은 파생 미러로 강등한다 | 게이트가 두 곳에 저장되면 어긋난다. D13("선언이 하나여야 감사와 회전이 성립")을 약화시키는 것이 아니라 **선언의 위치를 정확히 하나로 만드는 것**이다 |
| **AD14** | **`degraded`는 `requiresCapability`를 충족하지 않는다**(fail-closed). 능력 상태는 어떤 축의 엄격도도 낮추지 못하며, `degraded`가 `unavailable`과 다른 유일한 지점은 **이미 승인된 집행 요청의 지연 큐 적격 여부**다 | `degraded`를 가용으로 치면 열화를 유도할 수 있는 공격자가 곧 게이트를 낮출 수 있고(열화는 외부에서 자극 가능하다), 재시도 판단이 LLM에게 넘어간다. `81 §2.2`가 `degraded`를 만든 목적(큐잉 가능 여부)은 가용성 게이트 **뒤에서** 온전히 보존된다(§5.4.1) |
| **AD15** `[2026-08-13 신설]` | **게이트 전략은 `reversibility` 축으로 분기한다.** `irreversible`은 사전 게이트를 유지(유예취소 불가). `reversible`은 사후 유예취소를 허용할 수 있다(속도 우선). `compensable`은 사전 게이트가 기본(보상이 무료가 아니다). **단, 오염된 턴에서는 유예취소를 적용하지 않는다** — §7.3의 오염 턴 승격을 유예취소로 우회하면 열화를 유도하듯 오염을 유도할 수 있는 공격자가 게이트를 낮추게 된다(AD14와 같은 논거 구조, §5.4.1 상호참조) | 자매 프로젝트 Blueurban AaaM D23이 "자동 유지 + 3분 착수 유예 + 사후 취소"로 실측 뒤집은 사례. OSiris는 **전면 채택이 아니라 가역성 축 분기로 축소 채택**한다(감독 권고, 사용자 승인) |

---

## 1. 이 문서의 자리

### 1.1 무엇과 무엇 사이에 있는가

```
20-ontology-schema.md          명사(누가·무엇이 있는가)         :Party :Role :WorkItem :Capability :Permission
        │
        │  이 문서가 채우는 빈칸 : 동사(무엇을 할 수 있는가)
        ▼
80-action-layer.md             :ActionType  ─── REQUIRES_PERMISSION ──▶ :Permission
   · 파라미터 · 검증 · 부수효과 · 게이트 등급 · 오염 등급 · 필요 능력 키
        │                            │                         │
        │ requiresCapability         │ 실행 인가                │ 4층 파생
        ▼                            ▼                         ▼
81-capability-stacks.md      60-identity-authn.md        10 §8.1 grant-catalog
   (D2 소유. 능력 키의 정적    (authorize / CheckDelegation.   (RBAC·네트워크·allowedTools·
    레지스트리와 §2.3 해석      DG1~DG5에 DG6~DG8 추가)          NATS ACL — 무변경)
    계약 C-1~C-11)
```

### 1.2 비중복 규칙 — 어떤 사실이 어디에 사는가

이 문서가 새 사실을 저장하는 곳을 늘리는 만큼, 그 경계는 명시적이어야 한다. **같은 사실을 두 곳에 두지 않는다**는 `10` D1의 규칙이 이 층에도 예외 없이 적용된다.

| 사실 | 소유 문서 | 이 문서가 하는 일 |
|---|---|---|
| "이 주체가 이 권한 코드를 갖는가" | `20 §7.1` `effective(party)` | **참조만.** 계산식을 재정의하지 않는다 |
| "이 권한 코드가 컨테이너에서 무슨 도구·네트워크·ACL이 되는가" | `10 §8.1` grant-catalog (`orchestrator/internal/catalog/catalog.go`) | **참조만.** 4층 파생은 여전히 grant-catalog의 책임이다 |
| "이 변경을 이 행위자가 해도 되는가" | `60 §21.1` DG1~DG5 | **DG6~DG8을 같은 함수에 추가한다.** 두 번째 엔진을 만들지 않는다 |
| "게이트에 걸린 요청을 누가 어떻게 승인하고 누가 집행하는가" | `10 §8.2` | **일반화한다.** 파이프라인 자체는 무변경, 하드와이어된 `git.commit`이 데이터 한 행이 된다 |
| "이 설치에 이 능력 키가 존재하고 지금 쓸 수 있는가" | `81 §2.3` (C-1~C-11) | **`requiresCapability` 필드가 그 계약을 어떻게 소비하는가만 정의한다.** 키 문법·레지스트리·3상태·응답 형상은 `81`이 소유하고, **해소기는 `osiris-core`의 `GET /v1/capabilities[/{key}]` 하나뿐이다**(`81` C-3). 이 문서는 스택·프로비저닝·헬스를 한 줄도 규정하지 않는다 |
| "이 행위가 무엇이고 무슨 게이트·오염 등급을 받는가" | **이 문서** | 신규 |
| "이 행위가 실제로 어떻게 실행되는가(실행체)" | Go 코드(effect registry) | **이름만 데이터에 둔다.** 구현은 그래프에 없다(§5.3) |
| "액션 1회 호출의 이력" | JetStream `TASKS`(90일) + `:Approval`/`:Artifact`(영구) | 그래프에 호출 노드를 만들지 않는다(P6) |

---

## 2. 진단

### 2.1 지금 "무엇을 실행할 수 있고 누가 승인해야 하는가"가 사는 다섯 곳

이 다섯은 구조가 서로 다르다. 같은 질문의 답이 다섯 가지 형태로 흩어져 있다.

| # | 기구 | 위치 | 형태 | 저작 주체 | 성질 |
|---|---|---|---|---|---|
| M1 | **grant-catalog** | `10 §8.1` (D13), 실체 `orchestrator/internal/catalog/catalog.go` | 버전 고정 Go `map[string]Grant` | 개발자(코드 릴리스) | **가장 강한 조각.** 선언 하나에서 4층이 파생된다. 이미 이 문서가 하려는 일의 축소판이다 |
| M2 | **git 커밋 승인 파이프라인** | `10 §8.2` | 하드와이어된 시퀀스 | 개발자 | PreToolUse/MCP → supervisor `:8081/gate` → `approval.request` → 콘솔 → **core가 대신 집행**. 액션 **하나**를 위해 손으로 배선됨 |
| M3 | **`:WorkItem.actionClass`** | `20 §3.4` | 노드 속성 enum(`internal`/`external_effect`) | 온보딩 LLM/사람이 **손으로** 기입 | 파생이 아니다. 액션과의 연결이 없다. 틀려도 아무도 모른다 |
| M4 | **`authz.CheckDelegation` / DG1~DG5** | `60 §21.1`, 실체 `api/internal/authz/delegation.go` | 선언적 규칙 엔진(단일 함수, HI16) | 개발자 | **진짜 규칙 엔진.** 그러나 적용 범위가 RBAC 변이 하나뿐이다 |
| M5 | **genesis 스코프 허용목록** | `10 §8.5`/§8.5.1 | core 안의 고정 허용목록 상수 | 개발자 | 세 번째 손 관리 목록. 폐쇄 논증(W1~W5)이 이 목록의 고정성에 의존한다 |

### 2.2 이 다섯이 실제로 만드는 비용 — 왜 지금인가

M2가 이 문서의 **하중을 받는 근거**다. `git.commit` **하나**를 게이트에 태우기 위해 실제로 배선된 것을 세면:

- `:Permission{code:'git.commit', gate:'approval'}` 시드(`core/internal/graphdb/seed.go`)
- grant-catalog의 한 행 + `MCPServers`/`MCPTools`/`DeniedTools` 세 열(`catalog.go`)
- `osiris-git` MCP 서버(`agent/internal/mcpserver`)의 `request_commit` 툴
- supervisor의 게이트 엔드포인트와 24시간 블로킹
- `approval.request` 페이로드 스키마와 콘솔 승인 카드
- core 쪽 **실제 커밋 실행기**(에이전트 자격증명이 읽기 전용이므로 core가 자기 자격증명으로 수행)
- `CATALOG_VERSION` 동기(I9)

AIDevOps가 **외부 SCM/CI(GitHub 등)** 경유로 확정되어 OSiris가 어댑터만 갖는 이상, 이 카탈로그는 `git.push` · `pr.create` · `pr.merge` · `ci.trigger` · `release.publish` · `secret.rotate` · `mail.send` · `issue.comment` · `deploy.promote` … 로 **30~40행 규모**가 된다. 위 7단계를 행마다 손으로 반복하면, 30행째에는 "어떤 액션이 게이트를 받는가"를 아무도 한눈에 답할 수 없게 된다. 그리고 M3(`actionClass`)가 이미 그 상태다 — 손으로 쓰는 이진 플래그는 30행 규모에서 반드시 실제와 어긋난다.

### 2.3 한 줄 문제 진술

> **OSiris 온톨로지에는 명사만 있고 동사가 없다. 그래서 "이 조직이 무엇을 할 수 있고 그중 무엇이 사람의 승인을 요구하는가"는 그래프 질의로 답할 수 없고, 구조가 서로 다른 다섯 기구를 사람이 머릿속에서 합성해야만 답할 수 있다.**

### 2.4 참조 대상 — Palantir Foundry Ontology / AIP, 그리고 ServiceNow

두 참조 모두 핵심이 같다: **액션 타입을 코드가 아니라 데이터로 만든다.** Palantir의 Action Type은 6개 슬롯(Parameters / Rules & Validation / Submission Criteria / Side Effects / Permissions / Audit)으로 이루어지고, ServiceNow의 Flow Action도 입력·조건·스텝·ACL·감사의 같은 분해를 갖는다. 이 6분해를 OSiris에 대보면 진단이 정밀해진다:

| Palantir 슬롯 | OSiris의 현재 상태 | 판정 |
|---|---|---|
| Permissions | `:Permission` + `effective(party)` + grant-catalog 4층 파생 | **이미 있다. 재사용한다** (M1) |
| Audit | `:OrgRevision` 체인 + `:AssignmentDecision`의 PROV-O 스냅샷(`20 §8.7`) + `TASKS`/`TRANSCRIPT` 스트림(`10 §7.2`) | **이미 강하다. 재사용한다** |
| Submission Criteria | `:Permission.gate`/`approverRole`/`limitJson` + `10 §8.2` 파이프라인 | **부분적으로 있다.** 액션 단위가 아니라 권한 코드 단위이고, 오염·스택 조건이 없다 |
| **Parameters** | **없다** | 신규 |
| **Rules & Validation** | **없다** | 신규 |
| **Side Effects** | **없다** (실행체가 M2에 하드와이어) | 신규 |

즉 이 문서가 실제로 새로 만드는 것은 **3개 슬롯**이고, 나머지 3개는 이미 있는 것에 정확히 접속시키는 일이다. 이것이 이 설계의 크기다 — 새 보안 모델을 만드는 것이 아니다.

---

## 3. 왜 그래프 네이티브인가

### 3.1 검토한 세 안

| 안 | 내용 | 판정 |
|---|---|---|
| (a) **코드 상수** | grant-catalog와 같은 형태의 두 번째 Go 맵(`actionCatalog`) | **기각.** ① "이 조직이 무엇을 할 수 있는가"가 여전히 그래프 질의로 답해지지 않는다 — 온톨로지가 동사를 갖지 못한 채로 남는다. ② `requiresCapability`가 코드에 있으면 스택 유무 분기가 결국 `if` 문으로 되돌아온다(AD11이 없애려는 바로 그것). ③ 조합(§6)이 불가능해진다 — 조직이 액션을 조합하려면 릴리스를 기다려야 한다. ④ **M3(`actionClass`)를 파생으로 강등할 수 없다**: 그래프 안의 `:WorkItem`이 그래프 밖의 표를 참조할 수 없기 때문이다 |
| (b) **코드 정본 + 그래프 투영** | 코드가 진실, 부팅 시 그래프에 미러를 밀어넣고 콘솔은 미러를 읽는다 | **기각 — 그러나 가장 아까운 안.** 안전성은 (a)와 같고 질의 가능성은 (c)와 같다. 지는 지점은 정확히 하나: **조직이 편집할 수 없다.** 미러는 읽기 전용이어야 하고(쓰면 다음 부팅에 덮인다), 그러면 사용자 확정 사항인 "조직은 액션을 조합할 수 있다"가 표현 불가능해진다. 조합만을 위해 "코드 정본 + 사용자 조합분은 그래프 정본"의 이중 정본을 두는 순간, 게이트 판정이 두 저장소를 합성해야 하고 그것이 D1이 금지하는 상태다 |
| (c) **그래프 네이티브** | `:ActionType`이 노드. 콘솔에서 질의·조합·편집 | **채택 (AD1).** 단 §5의 세 안전장치를 전제로 한다 |

### 3.2 정직하게 포기하는 것

**보안에 관련된 표가 런타임에 변경 가능해진다.** 이것이 (c)의 유일한, 그러나 실질적인 대가다. (a)/(b)에서는 "게이트가 바뀌려면 릴리스가 필요하다"가 구조적 사실이었다. (c)에서는 그것이 **권한 판정 문제로 격하**된다 — 그리고 권한 판정은 코드와 달리 침해될 수 있다(`60 §21.4-1`: *"`osiris-api`가 침해되면 위임 규칙 전체가 무의미하다"*).

포기하는 것을 정확히 열거하면:

| 잃는 것 | (a)/(b)에서의 성질 | (c)에서 남는 것 |
|---|---|---|
| 게이트 표의 릴리스 고정성 | 컴파일 타임 상수 | `actiontype.write` 보유자만 변경 가능(§5.1), 완화 방향은 `org.admin`만(DG8) |
| "변경 이력 = git 이력" | `git log catalog.go` | `:OrgRevision` 체인 + `security.actiontype.*` 이벤트(§5.2) |
| 진행 중 세션이 보는 표의 안정성 | 프로세스 수명 동안 불변 | 턴 바인딩 + 엄격한 쪽 병합(§5.2) |
| 실행체의 고정성 | 코드에만 존재 | **잃지 않는다** — (c)에서도 실행체는 코드에만 있다(§5.3) |

마지막 행이 핵심이다. **(c)가 (a)/(b)에 비해 잃는 것은 "게이트 배선의 가변성"이지 "실행 가능한 것의 집합"이 아니다.** 세 안전장치는 이 구분을 유지하기 위한 장치이며, 그 구분이 무너지면 AD1은 기각되어야 한다.

### 3.3 세 안전장치가 대가를 수용 가능하게 만드는 이유 — 공격자 관점의 한 문단

`actiontype.write`를 얻은 공격자를 가정한다. 그가 할 수 있는 것과 없는 것:

- **없음 — 새 코드를 실행시키는 것.** `:ActionType`에는 실행 가능한 표현을 담을 필드가 존재하지 않는다(AC4). 참조할 수 있는 것은 **실행 중인 바이너리에 이미 컴파일된 명명된 술어·이펙트**뿐이며(AC3), 없는 이름은 graph-write가 거부하고 이미 저장된 것은 `inert`로 강등된다. 조합 언어에는 분기·반복·계산이 없다(AD7). 따라서 **데이터가 코드가 되지 못한다.**
- **없음 — 그 권한을 얻는 것 자체가 `:AIAgent`에게는 불가능.** 프롬프트 인젝션으로 도달할 수 없다(DG6). 즉 이 공격은 **사람 계정 침해**를 전제한다.
- **없음(단독으로는) — 게이트를 조용히 낮추는 것.** 완화 방향 변경은 `org.admin`만 가능하고 DG5 등급의 억제 불가 알림을 동반하며 발행 실패 시 변경되지 않는다(DG8, AC11).
- **없음 — 진행 중인 요청의 게이트를 사라지게 하는 것.** 판정은 bound/current의 엄격한 쪽이고(AD5), 결정적 검사는 집행 시점의 재평가다(AD6).
- **가능 — 게이트를 강화하는 것, 액션을 철회하는 것, 조합을 만드는 것.** 이것들은 전부 **안전한 방향**이거나(강화·철회) 조합 규칙에 의해 최엄격이 상속된다(§6).

남는 실질 위험은 **가용성**(악의적 강화로 조직 업무를 멈추는 것)과 **`org.admin` 침해**다. 전자는 보안이 아니라 운영 사고이며 `:OrgRevision` 롤백으로 복구된다. 후자는 이 문서가 만든 위험이 아니라 `60 §21.4`가 이미 인정한 구조적 한계다 — `org.admin`을 침해한 공격자는 그 전에도 이미 조직을 재정의할 수 있었다.

---

## 4. `:ActionType` 스키마

### 4.1 Palantir 6슬롯 → OSiris 매핑

| Palantir 슬롯 | OSiris 표현 | 신규/재사용 |
|---|---|---|
| **Parameters** | `:ActionType.parametersSchemaJson` — 제한된 JSON Schema 서브셋(§4.4) + `parameterDefaultsJson` | **신규 필드** |
| **Rules & Validation** | `:ActionType.validationPredicates: [string]` — **명명된 술어 id 배열.** 술어의 구현은 Go 레지스트리 | **신규 필드**(구현은 코드) |
| **Submission Criteria** | `gateClass` + `approverRole` + `limitJson` + `taintClass` + `requiresCapability` + 기존 `10 §8.2` 승인 파이프라인 | **신규 필드 + 기존 기구** |
| **Side Effects** | `:ActionType.effects: [string]` — **명명된 이펙트 id 배열.** 실행체는 `osiris-core` 안의 Go 이펙트 레지스트리 | **신규 필드**(구현은 코드) |
| **Permissions** | `(:ActionType)-[:REQUIRES_PERMISSION]->(:Permission)` → `effective(party)`(`20 §7.1`) → grant-catalog 4층(`10 §8.1`) | **전부 재사용. 신규 0** |
| **Audit** | `:OrgRevision` 체인(정의 변경) + `TASKS` 스트림 `action.*`(호출) + `:Approval`(게이트 결정) + `:Artifact`/`DERIVED_FROM` PROV-O(산출물 계보) | **전부 재사용. 신규 0** |

`REQUIRES_PERMISSION`은 **기존 관계 타입**이다(`20 §7.2`에서 `:Capability`가 이미 쓴다). 새 관계 타입을 만들지 않고 출발 라벨만 늘린다 — 그러면 `20 §10 Q6`("누가 `git.commit`을 행사할 수 있는가")가 액션 쪽으로도 무수정 확장된다.

### 4.2 노드 속성

#### `:ActionType`

| 속성 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `id`, `code` | string | ✔ | `git.commit`, `pr.merge`, `secret.rotate`. **`:Permission.code`와 같은 점 구분 어휘**를 쓰되 네임스페이스는 독립이다(같은 이름이어도 되고 달라도 된다 — §4.5) |
| `label`, `descriptionMd` | string | ✔ | 사람이 읽는 이름·설명. **인가 판정에 절대 쓰이지 않는다**(HI17과 같은 이유) |
| `catalogVersion` | string | ✔ | `:Permission.catalogVersion`·grant-catalog와 **같은 버전이어야 한다**(I9 확장, AC20) |
| `origin` | enum | ✔ | `system`(코드 레지스트리가 시드) / `composed`(조직이 조합) |
| `status` | enum | ✔ | `draft` / `active` / `deprecated` / `withdrawn` / `inert`. **삭제하지 않는다**(P4) |
| `createdAtRevision`, `closedAtRevision` | int | ✔/— | 재현용 시스템 시간(`20 §2.1`) |
| `parametersSchemaJson` | string(JSON) | ✔ | 제한 서브셋(§4.4). 파라미터 없는 액션은 `{"type":"object","properties":{}}` |
| `parameterDefaultsJson` | string(JSON) | | 기본값. **승인 카드에는 항상 실효값(effective value)으로 렌더된다**(§6.5) |
| `validationPredicates` | [string] | ✔ | 명명된 술어 id. 빈 배열 허용 |
| `effects` | [string] | ✔ | 명명된 이펙트 id. `origin:'composed'`이면 **빈 배열이어야 한다**(부품에서 파생) |
| `effectClass` | enum | ✔ | `internal` / `external_effect`. **`:WorkItem.actionClass`가 이 값에서 파생된다**(§8.3) |
| `gateClass` | enum | ✔ | `none` / `approval.team_lead` / `approval.admin` |
| `approverRole` | enum | | `gateClass != none`일 때만. `:Permission.approverRole`의 어휘 재사용 |
| `limitJson` | string(JSON) | | 금액·건수 상한. `{"currency":"KRW","max":1000000}` |
| `taintClass` | enum | ✔ | `neutral` / `escalate` / `forbidden` (§7.3) |
| `reversibility` | enum | ✔ | `reversible` / `compensable` / `irreversible` |
| `requiresCapability` | [string] | ✔ | 필요한 **능력 키(capability key)** 배열 — `81 §2.3` C-1의 `^stk\.[a-z0-9-]+(\.[a-z0-9-]+){1,2}$`를 만족하는 문자열만. **스택 id가 아니다**(C-11이 `stackId`의 안정성을 약속하지 않으므로 스택에 키잉하면 계약 위반이다). `cap.*`(`:Capability`, 사람·에이전트의 숙련)와 **이름공간이 다르다**(`81` CS1) — 두 어휘를 섞는 것은 배정 하드 필터를 오염시킨다. 빈 배열 = 항상 가용(§5.4) |
| `derivedHash` | string | | `origin:'composed'`일 때, 집행 시점 재계산과 대조할 파생값 해시(§6.4, AC8) |
| `iri` | string | | RDF export 여지 보존(`20 §2.1`) |

#### 왜 이 필드들이 **없는가** (AC4의 실체)

`command`, `script`, `template`, `url`, `endpoint`, `handlerCode`, `expr`, `condition`, `webhook` — **이런 이름의 필드는 스키마에 존재하지 않으며, 존재하지 않는다는 것 자체가 불변식이다.** graph-write의 `:ActionType` 쓰기 경로는 **허용 속성 화이트리스트**로 동작하며, 목록 밖 속성이 오면 무시가 아니라 **거부**한다. 무시하면 "쓰기는 성공했는데 필드가 사라지는" 상태가 되어 공격자에게 탐색 신호를 준다.

### 4.3 관계

| 관계 | 출발 → 도착 | 속성 | 비고 |
|---|---|---|---|
| `REQUIRES_PERMISSION` | `:ActionType` → `:Permission` | — | **기존 타입 재사용**(`20 §7.2`). 다:다 |
| `COMPOSED_OF` | `:ActionType` → `:ActionType` | `ord` (int) **오직 이것뿐** | 순차 합성. `ord` 외의 속성은 화이트리스트가 거부(AC10) |
| `GATES` | `:Approval` → `:WorkItem` | — | **기존 타입**(`20 §3.6`). 무변경 |
| `FOR_ACTION` | `:Approval` → `:ActionType` | — | **신규.** 승인 카드가 무슨 액션에 대한 것인지를 영구 기록 |
| `TAINTED_BY` | `:Approval` → `:Artifact` | `taintLevel` | **신규.** 오염 출처의 영구 기록(§7.6) |
| `DERIVED_FROM` | `:Artifact` → `:Artifact` | — | **기존 타입**(`20 §3.4`). 커밋 산출물이 인바운드 메일에서 유래했음을 표현 |

**`REQUIRES_STACK` 관계는 만들지 않는다 — 철회된 항목.** 이 문서의 이전 판은 `requiresCapability`(속성)를 `(:ActionType)-[:REQUIRES_STACK]->(:CapabilityStack)`으로 머티리얼라이즈하는 것을 `81`의 노드 확정 이후로 미뤄 두었다. `81`이 확정된 지금 그 전제가 **거짓임이 확인되었다**:

1. **`81`에 `:CapabilityStack` 그래프 노드가 없다.** `81`이 정의하는 것은 그래프의 `:CapabilityStackDecl`(사람이 쓰는 **선언**, `81 §3.2`)과 k8s의 `CapabilityStack` **CR**(오퍼레이터가 파생하는 **물성**, `81 §3.5` 봉쇄①)이다. 둘 다 "능력이 지금 쓸 수 있는가"의 답이 아니다 — 선언은 의도이고 CR은 물성이며, 해석은 `81` C-3이 `osiris-core`의 `GET /v1/capabilities[/{key}]` **하나**에만 맡겼다.
2. **엣지를 걸면 계약을 깬다.** 엣지의 도착점은 스택이고, `81` C-11은 **`stackId`의 안정성을 명시적으로 약속하지 않는다**(같은 키가 다른 스택으로 옮겨갈 수 있다). 스택으로 향하는 그래프 엣지는 그 자유를 파괴적 변경으로 만든다.
3. **두 번째 해석 경로가 된다.** 그래프 엣지가 있으면 "엣지를 세어 가용성을 판정하는" 코드가 반드시 생기고, 그것은 `81` C-3(해석기는 하나)과 CS7(core 외 자체 캐시 금지)을 동시에 위반한다.

따라서 **`requiresCapability`는 영구히 속성으로만 존재한다.** 키의 존재 검증은 그래프 엣지가 아니라 `81` C-2의 **정적 키 레지스트리**에 대한 쓰기 시점 검사이며(CA8/AC21), 상태 해석은 항상 `osiris-core`의 런타임 조회다. 이것은 유예가 아니라 **결정**이므로 `[갭]`이 아니다.

`:Artifact.kind`에 `inbound_message` 값을 추가한다(`20 §3.4`의 `commit`/`document`/`dataset`/`config`에 순수 추가 — 기존 값 어느 것의 의미도 바뀌지 않는다).

### 4.4 `parametersSchemaJson` — 왜 JSON Schema **서브셋**인가

파라미터 스키마는 데이터이므로 (c)에서 사용자가 쓸 수 있다. 스키마 언어가 충분히 강하면 그 자체가 계산이 되므로, 허용 키워드를 닫는다:

**허용**: `type`(`object`/`string`/`integer`/`number`/`boolean`/`array` 중), `properties`, `required`, `enum`, `minimum`/`maximum`, `minLength`/`maxLength`, `minItems`/`maxItems`, `pattern`, `items`, `description`, `default`.
**금지(거부)**: `$ref`, `$id`, `$schema`, `allOf`/`anyOf`/`oneOf`/`not`, `if`/`then`/`else`, `patternProperties`, `additionalProperties: {schema}`(불리언만 허용), `format`(구현 의존), `contentSchema`, 그리고 **중첩 깊이 3 초과**.

`$ref`/`$schema` 금지는 원격 참조로 네트워크가 발생하는 것을 표현 불가능하게 만든다. `if`/`then`/`else`와 `oneOf` 금지는 스키마가 조건 분기를 갖지 못하게 한다 — AD7과 같은 이유다.

`pattern`의 정규식 DoS: Go 표준 `regexp`는 **RE2**이며 백트래킹을 하지 않아 입력 길이에 선형이다. 따라서 catastrophic backtracking이 구조적으로 불가능하다. 추가로 패턴 길이 상한(256자)과 입력 길이 상한(`maxLength` 필수)을 둔다.

### 4.5 `:Permission`과 `:ActionType`의 관계 — 왜 두 개인가

한 문장으로: **`:Permission`은 "컨테이너가 무엇에 손댈 수 있는가"(능력 표면)이고, `:ActionType`은 "그 손댐이 무슨 행위이며 무슨 게이트를 받는가"(의미와 조건)다.**

둘을 합치지 않는 이유는 `20 §7.2`가 역량과 권한을 합치지 않은 이유와 같은 형태다 — 관계가 다:다이기 때문이다:

- `pr.create` · `pr.merge` · `ci.trigger`는 **전부** `net.egress.github` 권한을 요구하지만, 게이트 등급은 각각 `none` / `approval.admin` / `approval.team_lead`로 다르다.
- 반대로 `release.publish` 하나가 `net.egress.github` + `secret.read.ci` **두 권한**을 요구한다.

합치면 이 다:다가 표현 불가능해지고, `pr.create`를 허용하는 순간 `pr.merge`가 함께 열린다.

### 4.6 워크드 예시 — 카탈로그 발췌 (AIDevOps 확장 후)

| `id` | `effectClass` | `gateClass` | `taintClass` | `reversibility` | `requiresCapability` | `REQUIRES_PERMISSION` | `effects` (명명된 이펙트) |
|---|---|---|---|---|---|---|---|
| `git.commit` | external_effect | approval.team_lead | escalate | compensable | `stk.scm.repo.write` | `git.commit` | `effect.scm.commit` |
| `git.push` | external_effect | approval.team_lead | escalate | compensable | `stk.scm.repo.write` | `git.push`, `net.egress.github` | `effect.scm.push` |
| `pr.create` | external_effect | none | escalate | reversible | `stk.scm.repo.write` | `net.egress.github` | `effect.scm.pr_create` |
| `pr.comment` | external_effect | none | escalate | reversible | `stk.scm.repo.write` | `net.egress.github` | `effect.scm.pr_comment` |
| `pr.merge` | external_effect | approval.admin | **forbidden** | irreversible | `stk.scm.repo.write` | `git.push`, `net.egress.github` | `effect.scm.pr_merge` |
| `ci.trigger` | external_effect | approval.team_lead | escalate | compensable | `stk.ci.run` | `net.egress.github` | `effect.ci.dispatch` |
| `release.publish` | external_effect | approval.admin | **forbidden** | irreversible | `stk.ci.run`, `stk.scm.repo.write` | `git.push`, `secret.read.ci` | `effect.ci.release` |
| `secret.rotate` | external_effect | approval.admin | **forbidden** | compensable | `stk.scm.credential.rotate` | `secret.rotate` | `effect.secret.rotate` |
| `mail.send` | external_effect | approval.team_lead | escalate | irreversible | `stk.mail.send` | `msg.external.send` | `effect.mail.send` |
| `spend.request` | external_effect | approval.admin | escalate | reversible | — | `spend.request` | `effect.finance.request_spend` |
| `wbs.update_status` | internal | none | neutral | reversible | — | — | `effect.graph.workitem_status` |
| `note.write` | internal | none | neutral | reversible | — | `fs.workspace.write` | `effect.workspace.write` |

읽는 법 세 가지:

1. **`pr.merge`와 `release.publish`가 `taintClass: forbidden`인 이유**: 되돌릴 수 없고(`irreversible`), 되돌릴 수 없는 것을 "승인으로 승격"하는 것만으로는 부족하다 — 오염된 턴이 만든 승인 요청은 승인자를 속이는 데 성공할 수 있고 성공하면 복구가 없다. 오염 턴에서는 아예 **호출 자체가 불가능**해야 한다(§7.3).
2. **`pr.create`/`pr.comment`가 `gateClass: none`인 이유**: 되돌릴 수 있고, PR은 그 자체로 사람의 검토 지점이다. 그러나 `taintClass: escalate`이므로 **오염 턴에서는 게이트가 생긴다** — 인젝션이 PR 코멘트로 조직 밖에 무언가를 쓰는 경로가 닫힌다.
3. **`spend.request`가 `requiresCapability: —`인 이유**: 이것은 그래프 안에 승인 요청을 만드는 액션이며 외부 스택을 요구하지 않는다. `external_effect`인 것은 돈이 나가기 때문이지 통합이 필요해서가 아니다.
4. **`requiresCapability` 열이 스택 이름이 아니라 키인 이유**: `81` C-11이 `stackId`의 안정성을 약속하지 않고, `81` CS9가 **한 스택의 키들이 서로 다른 상태를 가질 수 있다**고 못박았다. 그래서 `mail.send`는 `stk.mail.send` 하나만 요구한다 — `stk.mail.receive`가 `unavailable`이어도(릴레이 모드, `81 §4.2`) 발신은 정상 가용이어야 하며, "메일 스택이 있는가"로 물었다면 그 정상 경로가 통째로 막힌다. 같은 이유로 인바운드 처리 액션은 `stk.mail.receive`를, 사서함 발급 액션은 `stk.mail.address.per-party`를 각각 따로 요구한다.
5. **`secret.rotate`의 키는 `81`이 채웠다** `[2026-08-12 해소]`: 초판에서는 `81` C-2의 정적 레지스트리에 자격증명 회전에 대응하는 키가 없어 이 행이 `—`였다. C-2가 "키는 선택이지 발명이 아니다"라고, CS4가 미지의 키를 정의 시점 하드 오류로 만들므로 이 문서가 이름을 **발명할 수 없었고**, G-A1로 회신했다. `81` C-1이 `stk.scm.credential.rotate`를 레지스트리에 추가해 해소됐다 — 공급 주체는 SCM 스택이고 회전 대상은 그 스택이 보관하는 PAT·웹훅 시크릿이다. **LLM 프로바이더 키 회전은 이 액션의 범위가 아니다**(OmniRoute는 능력 스택이 아니라 필수 구성요소 — `81 §1`, `79 §6`).

### 4.7 제약과 인덱스 (`20 §11` 형식)

```cypher
// --- 유일성 ---
CREATE CONSTRAINT actiontype_id   IF NOT EXISTS FOR (n:ActionType) REQUIRE n.id   IS UNIQUE;
CREATE CONSTRAINT actiontype_code IF NOT EXISTS FOR (n:ActionType) REQUIRE n.code IS UNIQUE;

// --- 인덱스 ---
CREATE INDEX actiontype_status  IF NOT EXISTS FOR (n:ActionType) ON (n.status);
CREATE INDEX actiontype_gate    IF NOT EXISTS FOR (n:ActionType) ON (n.gateClass, n.taintClass);
CREATE INDEX actiontype_rev     IF NOT EXISTS FOR (n:ActionType) ON (n.createdAtRevision, n.closedAtRevision);
CREATE INDEX actiontype_origin  IF NOT EXISTS FOR (n:ActionType) ON (n.origin);
// §4.3의 :Artifact.kind 확장에 따른 오염 계보 질의용
CREATE INDEX artifact_kind      IF NOT EXISTS FOR (n:Artifact)   ON (n.kind, n.createdAt);
```

**애플리케이션이 대신 강제하는 제약** (Neo4j CE에 존재성 제약이 없다 — `20 §11`과 같은 이유로 graph-write의 책임):

| # | 제약 |
|---|---|
| CA1 | `:ActionType`의 속성은 §4.2의 화이트리스트 밖일 수 없다. 목록 밖 속성이 오면 **거부**(무시가 아니다) |
| CA2 | `origin:'system'` 노드의 `id`는 실행 바이너리의 액션 레지스트리에 존재해야 한다. 없으면 부팅 시 `status:'inert'`로 강등 |
| CA3 | `validationPredicates`/`effects`의 모든 원소는 각 레지스트리에 존재해야 한다. 쓰기 시 거부, 부팅 시 `inert` 강등 |
| CA4 | `origin:'composed'` 노드는 `effects`가 빈 배열이고 `COMPOSED_OF` 엣지를 1개 이상 갖는다 |
| CA5 | `COMPOSED_OF` 그래프에 사이클이 없고, 깊이 ≤ 3, leaf 이펙트 총수 ≤ 8 |
| CA6 | `:ActionType` 변경 트랜잭션은 `:OrgRevision`을 정확히 1 증가시킨다 (C8의 적용, 새 규칙 아님) |
| CA7 | `:ActionType` 노드는 삭제되지 않는다. 철회는 `status:'withdrawn'` + `closedAtRevision` (P4) |
| CA8 | `requiresCapability`의 모든 원소는 `81 §2.3` C-1의 키 정규식을 만족하고 C-2의 **정적 키 레지스트리에 존재**해야 한다. 미지의 키는 **정의 시점 하드 오류**(쓰기 거부, `81` CS4)이며, 이미 저장된 행은 부팅 시 `status:'inert'` 강등 — CA3과 **같은 형태**다. 런타임 해석에서의 미지 키는 오류가 아니라 `unavailable{reason:"UNKNOWN_KEY"}`이며(`81` C-5), 이 비대칭은 의도된 것이다(정의 시점은 엄격, 런타임은 fail-closed하게 관대) |

---

## 5. 세 안전장치

### 5.1 (a) 정의 평면 / 실행 평면 분리

#### 5.1.1 두 평면

| | **정의 평면** | **실행 평면** |
|---|---|---|
| 무엇을 하는가 | `:ActionType`을 만들고·조합하고·게이트를 정한다 | 정의된 액션을 1회 호출한다 |
| 주체 | **`:Person`만** | `:Person` · `:AIAgent` 공통 |
| 권한 코드 | `actiontype.write` (신규, 1개) | `git.commit`, `msg.external.send` … (기존 어휘, 무변경) |
| 인가 판정 | `authz.CheckDelegation` (DG1~DG8) | `authorize(principal, code)` (`60 §19.6`, 무변경) |
| 변경의 효과 | `:OrgRevision` +1 | `:OrgRevision` 변화 없음(호출은 조직 의도가 아니다) |
| 리비전 의미 | 발행(publish) | 바인딩(bind) |

#### 5.1.2 새 위임 규칙 DG6~DG8

`60 §21.1`의 표기를 그대로 쓴다: `eff(X)` = `effective(party X)`, `A` = 행위자, `T` = 대상, `R` = 부여하려는 프로파일/역할. 추가로 `Δ` = 이 트랜잭션이 만드는 `:ActionType` 변경.

**H31 확장.** 아래 세 규칙은 **같은 단일 함수** `authz.CheckDelegation`에 추가된다. 새 함수를 만들지 않으며, HI16(위임 판정이 단 하나의 함수에만 존재)은 문자 그대로 유지된다.

| # | 규칙 | 형식 | 이것이 막는 것 |
|---|---|---|---|
| **DG6** | **정의 평면은 사람만** — `actiontype.write`를 포함하는 `R`은 `:AIAgent`에게 부여될 수 없고, `:AIAgent`는 그것을 위임받을 수도 대리 행사할 수도 없다 | `actiontype.write ∈ grants(R) ⟹ T:Person ∧ A:Person ∧ actiontype.write ∈ eff(A)` | **프롬프트 인젝션이 게이트 표에 도달하는 모든 경로.** 인젝션된 에이전트는 자기가 받을 게이트를 바꿀 수 없다 |
| **DG7** | **자기 이익 편집 금지** — `Δ` 적용 후 `eff(A)`가 증가하거나, `A`가 호출 주체인 액션의 게이트가 완화되면 거부 | `eff_after(A) ⊆ eff_before(A)` ∧ `∀a: gate_after(a) ≥ gate_before(a) ∨ A ∉ callers(a)` | 정의 권한으로 자기 실행 권한을 키우는 것. DG3(자기 대상 금지)의 액션 평면 대응물 |
| **DG8** | **완화는 최상위만 + 억제 불가능한 가시성** — `gateClass`·`taintClass`·`effectClass`·`reversibility`를 **덜 엄격한 쪽**으로 옮기거나 액션을 `withdrawn`에서 되살리는 변경은 `org.admin` 보유자만 가능하고, `org.admin` 전원에게 `:Notification{severity:"critical"}` + `security.actiontype.relaxed` 이벤트를 발행한다. **발행 실패 시 변경하지 않는다(fail-closed)** | `relaxes(Δ) ⟹ org.admin ∈ eff(A)` ∧ 발행 성공 | "조용한 게이트 제거". DG5가 조용한 관리자 교체에 쓴 기법의 재사용 |

**강화는 `actiontype.write`만으로 가능하다.** 비대칭은 의도적이다 — 게이트를 조이는 실수는 업무를 멈추고(가역), 푸는 실수는 외부에 영향을 남긴다(비가역). `60 §21.1`이 DG1′를 "DG1로 함의되지만 가장 중요한 한 줄이므로 별도로 명문화"한 것과 같은 이유로, DG8은 DG1으로 부분적으로 함의되지만 별도 검사로 둔다.

#### 5.1.3 P1의 한정 — 그리고 이 문서가 발견한 정정

사용자 브리프는 DG6를 *"P1(`20 §P1`: 사람과 AI는 같은 상위 개념)에 대한 첫 명시적 한정"*이라고 서술했다. **정확히는 네 번째다.** 실측하면:

| # | 기존 한정 | 위치 | 형태 |
|---|---|---|---|
| 1 | `cap.exec.physical`의 `HAS_CAPABILITY` 출발점은 `:Person`만 | `20 §11` C2 | 역량 |
| 2 | `org.admin`/`org.manage.people`/`org.manage.roles`는 **사람 전용 콘솔 권한**(4층 투영이 전부 `—`이고, 투영하려는 시도가 곧 설계 오류) | `10 §8.1`, `60 §20.4` | 권한 |
| 3 | `IN_ORG` 멤버십을 갖는 `:Party`는 `:Person`만 | `60 §21.3` I15 / `20 §11` C9 | 멤버십 |
| 4 | **`actiontype.write`는 `:AIAgent`에게 부여·위임될 수 없다** | 이 문서 DG6 | 권한 |

**이 사실이 설계를 약화시키지 않고 강화한다.** 브리프의 서술대로였다면 이 문서는 전례 없는 예외를 새로 만드는 것이었다. 실제로는 이미 세 번 있었던 한정이 **암묵적 패턴으로만 존재**했고, 이 문서가 그것을 처음으로 **닫힌 규칙**으로 일반화한다:

> **P1-Q(한정 규칙).** `:AIAgent`가 보유·위임받을 수 없는 권한 코드의 집합 `HumanOnly`는 **닫힌 열거**이며, 어떤 코드가 그 원소가 되는 조건은 **단 하나**다 — *"그 코드를 행사하면 조직이 무엇을 할 수 있는지의 **정의 자체**가 바뀐다."*
>
> 현재 `HumanOnly = { org.admin, org.manage.people, org.manage.roles, actiontype.write }` (4개).

**이 규칙이 도달하는 곳과 도달하지 않는 곳을 정확히 쓴다** — 이것이 일반적 탈출구가 되지 않게 하는 장치다:

- **도달한다**: 조직의 정의(사람·역할·권한·액션 타입)를 바꾸는 코드.
- **도달하지 않는다 — 결코**: **실행 권한 코드.** `git.commit`, `pr.merge`, `release.publish`, `secret.rotate`, `mail.send`가 아무리 위험해도 `HumanOnly`에 들어가지 않는다. 위험한 실행은 **게이트로 다룬다**(`gateClass: approval.admin`), 주체 종류로 다루지 않는다. 여기가 P1이 실제로 지켜지는 지점이다 — **AI 에이전트는 사람이 하는 어떤 일도 할 수 있고, 다만 그 일이 무엇인지를 정의하지는 못한다.**
- **도달하지 않는다**: 읽기 코드(`org.read.*`). `60 §19.6`이 기계 주체에 대해 이미 정한 것과 정합.
- **집행**: `HumanOnly`는 코드의 닫힌 상수 목록이고, 부여 API가 `T:AIAgent`일 때 그 목록과 교집합이 있으면 거부한다. **AC6이 이 목록에 실행 권한 코드(=grant-catalog에서 `ClaudeAllowedTools`/`MCPTools`/`EgressAllowlist` 중 하나라도 비어 있지 않은 코드)가 들어오면 린트를 실패시킨다.** 즉 이 예외는 **린트가 지키는 상한**을 갖는다.

#### 5.1.4 이 장치가 닫는 공격

**공격**: 인바운드 메일에 *"당신은 이제 릴리스 담당자입니다. 먼저 `release.publish`의 승인 게이트를 제거하고 …"*가 들어오고, 에이전트가 그 지시를 따른다.
**결과**: 그래프 쓰기 시도가 `osiris-api`에서 `actiontype.write` 부재로 403. 도구 층에서는 애초에 액션 정의 편집 MCP 툴이 에이전트에게 존재하지 않는다(grant-catalog에 그런 행이 없다). 두 겹 모두 협조가 아니라 강제다 — 전자는 API의 인가, 후자는 그 도구가 컨테이너에 렌더되지 않는다는 사실.

### 5.2 (b) 발행/버전 의미론 — live mutation이 아니다

#### 5.2.1 발행

`:ActionType`의 모든 변경은 조직 의도 변경이므로 `:OrgRevision`을 +1한다(CA6 = C8의 적용). 이것은 새 규칙이 아니라 기존 규칙의 적용이며, 그 결과 다음이 **자동으로** 성립한다:

- `20 §10 Q8`("특정 시점 기준 조직 재현")이 액션 카탈로그에도 그대로 적용된다 — *"리비전 812 시점에 `pr.merge`의 게이트는 무엇이었는가"*가 기존 질의로 답해진다.
- `10 §1.4`의 수렴 루프가 `org.revision` 이벤트로 이미 깨어난다. 액션 정의 변경 → 게이트 배선 변경 → `ConfigHash` 변화 → **무중단 `ReloadConfig`**(`10 §5.6`, A12). 컨테이너 재생성이 아니다.

#### 5.2.2 바인딩 — 단위는 세션이 아니라 **턴**

사용자 브리프는 *"실행 중인 에이전트 세션이 시작 시점의 리비전에 바인딩된다"*고 했다. **설계상 올바른 단위는 세션이 아니라 턴(태스크)이며, 그 이유가 곧 이 절이다.**

`10 §5.2`의 supervisor 메인 루프에서 세션(`sessionId`)은 `--resume`으로 **수개월 이어질 수 있다**. 세션에 바인딩하면 몇 달 전 리비전의 게이트 표로 오늘의 커밋을 판정하게 되고, 그것은 "게이트가 사라지지 않는다"가 아니라 **"게이트가 영원히 갱신되지 않는다"**가 된다. 반면 턴(인박스 메시지 1건 = `runTask` 1회)은 `10 §5.6`이 이미 설정 갱신의 경계로 쓰고 있는 단위다(*"진행 중 턴은 건드리지 않고 다음 태스크부터"*). **새 경계를 만들지 않고 기존 경계를 재사용한다.**

```
turn.begin(taskId):
    boundRevision := currentOrgRevision          # core가 인박스 메시지에 실어 보낸다
    기록: /workspace/.osiris/state.json  +  task.<taskId>.event{boundRevision}
    (core는 taskId → boundRevision 을 자기 쪽에도 기록한다 — 에이전트의 주장을 믿지 않는다)
```

#### 5.2.3 판정 — 엄격한 쪽 병합(strictest merge)

```
available(a, turn) := available@bound(a) ∧ available@current(a)
gate(a, turn)      := max( gate@bound(a), gate@current(a), gateFromTaint(a, taint(turn)) )
taintClass(a,turn) := max( taintClass@bound(a), taintClass@current(a) )
```

| 정의 변경의 방향 | 진행 중인 턴에 언제 반영되는가 | 근거 |
|---|---|---|
| **강화** (게이트 상향, 오염 등급 상향, 철회) | **즉시** | `@current`가 `max`/`∧`를 통해 이긴다 |
| **완화** (게이트 하향, 오염 등급 하향, 철회 취소) | **다음 턴부터** | `@bound`가 `max`/`∧`를 통해 이긴다 |
| 신규 액션 추가 | 다음 턴부터 | `available@bound`가 거짓 |

이 병합이 브리프의 요구("게이트가 도중에 사라질 수 없다")를 만족시키면서 동시에 **핀 고정의 위험**(오래된 리비전에 영원히 묶여 새 게이트를 못 받는 것)을 없앤다. 두 요구는 서로 반대 방향이며, `max`만이 둘을 동시에 만족시킨다.

#### 5.2.4 장수 턴과 철회

**"영원히 핀 고정되는가?"** — 아니다. 바인딩은 턴 수명을 넘지 않고, 턴은 반드시 끝난다: JetStream `ack_wait=30m`(`InProgress`로 연장, `max_deliver=3`), 승인 대기 상한 24시간(`10 §8.2`), drain 시 `terminationGracePeriodSeconds: 660`. **강제 재바인딩(forced re-bind)이라는 별도 기구를 만들지 않는다** — 만들 필요가 없고, 만들면 진행 중 턴을 중단시킬 새 실패 모드가 생긴다.

**"액션이 제거되면?"** — `:ActionType`은 삭제되지 않고 `status:'withdrawn'`이 된다(CA7). 철회는 강화 방향이므로 `available@current`가 거짓이 되어 **진행 중 턴에도 즉시 반영**된다. 세 시점을 나누어 보면:

| 철회 시점 | 진행 중인 것 | 결과 |
|---|---|---|
| 에이전트가 아직 호출하지 않음 | — | 다음 호출이 409 `action_withdrawn` |
| 게이트 요청은 나갔고 승인 대기 중 | `:Approval{status:'pending'}` | 승인 카드에 **철회됨** 배지. 승인 버튼 비활성. `security.actiontype.withdrawn_pending` 발행 |
| **이미 승인되었고 core가 집행 직전** | `:Approval{status:'approved'}` | **집행하지 않는다.** AD6의 집행 시점 재평가가 이 경우를 잡는다(아래) |

#### 5.2.5 AD6 — 집행 시점 재평가가 결정적 검사다

`10 §8.2`의 강제는 *"★ 실제 커밋/푸시는 osiris-core가 자기 자격증명으로 수행한다"*이다. 그러므로 **마지막으로 정의를 읽는 주체는 core**이며, 그 지점이 TOCTOU를 닫는 유일한 지점이다.

```
core.execute(approvalId):
    a  := reload(:ActionType by id, at currentOrgRevision)     # 저장된 스냅샷이 아니라 다시 읽는다
    a' := recompute(a)                                          # 조합이면 leaf 에서 join 재계산 (§6.4)
    if a.status != 'active'                       → 집행 안 함, 승인 취소, security.actiontype.drift
    if gate(a') > :Approval.grantedGateClass      → 집행 안 함, 재승인 요구
    if a'.derivedHash != a.derivedHash            → 집행 안 함, security.actiontype.drift
    if ∃k ∈ requiresCapability(a'): state(k) != 'available'
                                                  → 집행 안 함,
                                                    409 CAPABILITY_UNAVAILABLE{key,state,reason}
                                                    state=='degraded' 이면 §5.4.1의 지연 큐 후보
    if taintClass(a') == 'forbidden' ∧ taint>t0   → 집행 안 함
    → 통과하면 effects 를 ord 순으로 실행
```

**승인이 "무엇에 대한 승인이었는가"는 얼려진다**: `:Approval`은 `FOR_ACTION` 엣지와 `grantedGateClass`·`boundRevision`·파라미터 실효값을 기록한다. 승인 후 게이트가 **강화**되면 그 승인은 더 이상 충분하지 않고 재승인이 필요하다. 반대로 승인 후 게이트가 완화되어도 이미 받은 승인은 유효하다(이미 더 엄격한 것을 통과했으므로).

#### 5.2.6 이 장치가 닫는 공격

**공격**: 공격자가 `git.commit`의 `gateClass`를 `none`으로 바꾼 뒤, 이미 게이트에 걸려 대기 중이던(또는 방금 시작한) 턴이 그 완화를 즉시 흡수하기를 기대한다.
**결과**: `gate@bound`가 `approval.team_lead`이므로 `max`가 여전히 게이트를 요구한다. 다음 턴부터 완화가 반영되지만, 그 완화 자체가 DG8에 의해 `org.admin` + 억제 불가 알림을 요구한다. **"조용히, 즉시" 두 조건 중 어느 것도 성립하지 않는다.**

### 5.3 (c) 선언은 데이터, 실행체는 코드

#### 5.3.1 두 레지스트리

`osiris-core` 안에 grant-catalog와 **같은 성질**(버전 고정, 런타임 편집 불가, 코드 릴리스 주기)의 레지스트리 두 개를 둔다:

| 레지스트리 | 키 | 값 | 예 |
|---|---|---|---|
| **술어 레지스트리** | `predicate.*` | `func(ctx, params, graphSnapshot) (ok bool, reason string)` — **부작용 없음. 순수 함수.** | `predicate.diff.size_under(1000)`, `predicate.branch.not_protected`, `predicate.amount.within_limit`, `predicate.target.owned_by_own_team` |
| **이펙트 레지스트리** | `effect.*` | `func(ctx, params) (Artifact, error)` — 부작용 있음. **`osiris-core`의 자격증명으로만 실행** | `effect.scm.commit`, `effect.scm.pr_merge`, `effect.ci.dispatch`, `effect.mail.send`, `effect.graph.workitem_status` |

두 레지스트리 모두 `CATALOG_VERSION`을 공유한다(I9 확장). 부팅 시 core는 그래프의 모든 `:ActionType`을 훑어 참조 이름을 검사하고, **미지의 이름을 가진 행을 `status:'inert'`로 강등**한 뒤 `security.actiontype.unknown`을 발행한다.

**"건너뛴다"가 아니라 "강등한다"인 이유**: grant-catalog는 미지의 grant 코드를 조용히 건너뛴다(`AllowedTools`의 주석대로, 버전 드리프트는 `CheckCatalogVersion`이 잡는다). 액션에서는 그럴 수 없다 — 건너뛴 액션이 "게이트가 없는 액션"으로 읽히면 정확히 반대의 결과가 나온다. **fail-closed: 모르는 것은 실행 불가.**

#### 5.3.2 보안 성질 — 데이터가 코드가 되지 못한다

세 겹으로 진술한다.

1. **어휘 상한**: `:ActionType`이 지시할 수 있는 계산의 전체 집합은 `predicate.*` ∪ `effect.*`이며, 이 집합은 실행 중인 바이너리에 의해 결정된다. 데이터는 이 집합의 **원소를 고를 수 있을 뿐 원소를 만들 수 없다**.
2. **구성 상한**: 원소를 결합하는 문법은 `COMPOSED_OF{ord}`의 순차 합성 하나뿐이다. 분기·반복·조건·변수·산술이 없다(AD7). 따라서 **조합 언어는 튜링 완전이 아니며**, 어떤 `:ActionType` 데이터도 "부품들의 유한 순차 실행" 이상을 표현할 수 없다.
3. **표현 상한**: 코드 문자열을 담을 필드가 스키마에 없고(AC4/CA1), 파라미터 스키마 언어에도 조건·참조가 없다(§4.4).

> **`:ActionType`을 완전히 장악한 공격자가 실행시킬 수 있는 것의 집합은, 그 이전에 이미 실행 가능했던 것의 집합과 정확히 같다.** 그가 바꿀 수 있는 것은 **어떤 순서로, 어떤 게이트를 거쳐** 실행되는가뿐이며, 게이트에 대해서는 (a)가 주체를, (b)가 시점을, §6이 조합을 각각 막는다.

#### 5.3.3 정직한 한계 두 가지

- **`[갭]` 파라미터를 통한 이펙트 오용.** 어휘와 문법은 닫았지만 **인자는 닫지 않았다.** `effect.mail.send`에 공격자가 고른 수신자 기본값을 넣은 `:ActionType`을 만드는 것은 가능하다. 완화는 §6.5(승인 카드에 실효값 렌더)와 술어(`predicate.mail.recipient_in_directory`)이며, **완전히 닫히지는 않는다.** 닫으려면 각 이펙트가 자기 인자 도메인을 코드로 제한해야 하고(예: 수신자는 `:Person.email`에 존재하는 값만), 그것은 이펙트 구현의 책임으로 넘긴다 — **이펙트 레지스트리 등록 심사 항목**으로 명문화한다.
- **`[미검증]` 레지스트리 이름의 안정성.** 이펙트 이름은 그래프 데이터가 참조하는 계약이므로 **리네임이 곧 파괴적 변경**이다. `deprecated` 별칭 유지 기간을 정해야 하는데, 이 문서는 정하지 않는다(릴리스 정책의 문제이며 이 문서 범위 밖).

### 5.4 `requiresCapability` — `81`과의 계약

**이 절이 소유하는 것은 `:ActionType` 쪽 절반뿐이다.** 능력 스택의 정의·프로비저닝·상태 해석은 `81-capability-stacks.md`(워커 D2)가 소유하며, 그 문서의 **§2.3 해석 계약 C-1~C-11이 확정**되었으므로 이 절은 더 이상 가정 위에 서 있지 않다. 아래 표의 모든 행은 `81`의 확정 계약에 대한 **소비 규칙**이다.

| 계약 항목 | 값 |
|---|---|
| **필드가 담는 것** | **능력 키(capability key)의 배열**(`81` C-1). 스택 id가 **아니다** — C-11이 `stackId`의 안정성을 명시적으로 약속하지 않으므로 스택에 키잉하면 계약을 어긴다. 키는 `^stk\.[a-z0-9-]+(\.[a-z0-9-]+){1,2}$`를 만족하고 C-2의 **정적 키 레지스트리**에 존재해야 한다. `cap.*`(사람·에이전트의 숙련, `20 §5`)와는 이름공간이 배타적이다(`81` CS1) |
| **의미** | **AND.** 배열의 **모든** 키가 `available`이어야 액션이 가용하다(`degraded`는 충족하지 않는다 — §5.4.1). OR가 필요하면 액션 두 개를 만든다 — OR를 허용하면 그것이 조건 분기이고 AD7 위반이다 |
| **키 단위 평가 강제** | 각 키를 **개별로** 조회한다. "스택이 healthy면 그 스택의 키는 전부 `available`"을 **가정하지 않는다** — `81` CS9가 그 가정을 금지한다(릴레이 메일 스택은 `stk.mail.send`만 공급하고 `stk.mail.receive`는 `unavailable`이다, `81 §4.2`). 스택 단위로 묻는 코드는 AC21 위반 |
| **해소자** | **`osiris-core`가 `GET /v1/capabilities[/{key}]`로**(`81` C-3 — 해석기는 하나). 카탈로그 렌더 시점과 **집행 시점 양쪽**에서 해소한다. 에이전트도 supervisor도 `osiris-api`도 아니다 — 집행자가 해소해야 TOCTOU가 없다 |
| **상태의 가짓수** | **정확히 3개**: `available` / `degraded` / `unavailable`(`81` C-4, 확장 금지). 이 문서의 이전 판은 2상태를 가정하고 "stale = absent"라고 적었다 — **그 가정은 폐기한다**(§5.4.1) |
| **해소의 신선도** | `observedAt`은 응답의 **필수 필드**이고(`81` C-4), core는 해석 결과를 **최대 15초** 캐시할 수 있다(C-7). **소비 측의 자체 캐시는 금지**(`81` CS7) — 이 문서의 집행 경로·카탈로그 렌더·MCP 툴 렌더 어디에도 능력 상태의 사본을 두지 않는다. 신선도 상한이 계약으로 15초이므로 "stale을 어떻게 읽을 것인가"라는 문제 자체가 소비 측에 존재하지 않는다. 한 액션 판정은 **하나의 스냅샷**으로 평가한다(C-6 / `81` CS6) |
| **미지의 키 — 양면** | ① **정의 시점**(`:ActionType` 쓰기): 레지스트리에 없는 키는 **하드 오류**로 쓰기를 거부한다(`81` CS4 = 이 문서 CA8). ② **런타임**(해석): 404가 아니라 `{state:"unavailable", reason:"UNKNOWN_KEY"}`이며(`81` C-5), 이 문서는 그것을 **다른 `unavailable`과 똑같이** 다룬다 — 오류 경로와 부재 경로를 갈라두면 둘 중 하나는 반드시 테스트되지 않는다. 두 반쪽 다 fail-closed이며 비대칭은 의도된 것이다 |
| **가용하지 않을 때 에이전트가 보는 것** | ① **카탈로그**: 액션이 목록에 남되 `disabled: true` + `disabledReason`(아래 형식)으로 렌더된다. 사라지지 않는다 — 사라지면 LLM이 대체 경로를 찾아 헤매고, 그 헤맴이 실제로 위험한 행동을 만든다. ② **도구 층**: 해당 MCP 툴이 렌더되지 않거나 렌더되되 즉시 거부 응답을 반환한다. ③ **호출 시**: `409 CAPABILITY_UNAVAILABLE` (아래 형상). **404가 아닌 이유**: 404는 "그런 액션은 없다"이고 그것은 거짓이다 |
| **거부의 형상** | `81` C-9를 그대로 쓴다 — HTTP **409** + 본문 `{"error":"CAPABILITY_UNAVAILABLE", "key":"stk.mail.send", "state":"unavailable"\|"degraded", "reason":"NOT_DECLARED"}`. **403 `PERMISSION_DENIED`와 반드시 구분된다**: 403은 "당신이 못 한다"이고 409는 "이 설치가 못 한다"이다. 뭉개면 관리자가 "권한을 더 주면 되겠지"라는 **틀린 조치**를 한다. 이 문서의 다른 409(`action_withdrawn`, `taint_forbidden`)와도 `error` 코드로 구별된다 |
| **`disabledReason` 형식** | `"<STATE>:<key>:<reason>"` — 예: `"UNAVAILABLE:stk.mail.send:NOT_DECLARED"`, `"DEGRADED:stk.scm.repo.write:UPSTREAM_5XX"`. 세 조각 전부 `81` C-4 응답의 **기계 판독 필드**에서 그대로 온다. **이전 판의 `capability_absent:mail`은 폐기한다** — `81 §4.1`이 메일을 `stk.mail.send`/`stk.mail.receive`/`stk.mail.address.per-party` 세 키로 쪼갰으므로 `mail`이라는 키는 **존재하지 않고**, 스택 이름으로 사유를 적으면 CS9가 금지한 "스택 단위 사고"가 문자열을 통해 되살아난다. 막는 키가 둘 이상이면 `requiresCapability` **배열 순서상 첫 번째**를 싣고(판정의 결정성), 전량은 구조화 필드 `disabledKeys: [{key,state,reason}]`로 함께 준다. `reasonText`(사람용)는 **판정에 쓰지 않는다**(`81` C-4) |
| **하드 에러인가 필터인가** | **양쪽 다(AD11).** 필터만 두면 카탈로그를 우회한 직접 호출이 통과한다. 하드 에러만 두면 에이전트가 매번 시도했다가 실패하는 낭비가 생긴다 |
| **코드에 남아서는 안 되는 것** | 실행 경로에 `if mailEnabled` / `if scmConfigured` 류의 분기, **능력 키·스택 이름 리터럴**, 그리고 **스택 단위 가용성 질의**. **AC18·AC21이 린트/CI로 검사한다**(HI17이 인가 경로의 역할 이름 리터럴을 금지한 것과 같은 형태) |
| **`81` 쪽에 남기는 것** | 키 문법과 정적 레지스트리(`81 §2.3` C-1·C-2), 상태 해석과 그 근거인 스택 수명주기(`81 §2.2`), 스택 간 의존(`81 §2.5`), 스택별 실사례(`81 §4`·§5), 부재의 국소성 보증(C-8 / `81` CS2). **이 문서는 그 어느 것도 재서술하지 않는다** |

### 5.4.1 ★ `degraded`에서 액션은 무엇을 하는가

`81` C-4가 상태를 3개로 고정하면서 이 문서에 실제로 새 질문이 하나 생겼다. `unavailable`은 이 문서가 이미 답을 갖고 있었지만(거부), **`degraded`는 이 문서가 한 줄도 정한 바 없는 상태**다. 그리고 `81 §2.2`는 `degraded`를 *"소비 측이 재시도/큐잉을 선택할 수 있는 유일한 상태"* 로 정의했으므로, 여기서 침묵하면 `81`이 만든 구분이 실제로는 아무 데도 쓰이지 않거나, 더 나쁘게는 구현자가 각자 다르게 해석한다.

**결정 (AD14).** `degraded`는 `requiresCapability`를 **충족하지 않는다.** 가용성 술어는 `∀k ∈ requiresCapability(a): state(k) == 'available'`이며 `degraded`는 `unavailable`과 **동일하게 액션을 차단**한다 — 카탈로그에서 `disabled`, 호출 시 409. `degraded`가 사는 자리는 **가용성 판정이 아니라 그 뒤**다: **이미 모든 게이트를 통과해 승인된 집행 요청에 한해**, core가 그것을 **지연 큐**에 넣는 것이 허용되는 유일한 상태가 `degraded`다. `unavailable`에서는 큐잉 자체가 금지된다(영영 나가지 않는 큐가 된다 — `81 §2.2`의 논거를 그대로 승계).

| 시점 | `available` | `degraded` | `unavailable` |
|---|---|---|---|
| 카탈로그 렌더 | 정상 | `disabled: true` + `DEGRADED:…` | `disabled: true` + `UNAVAILABLE:…` |
| MCP 툴 렌더 | 렌더 | 렌더 안 함(또는 즉시 거부) | 렌더 안 함(또는 즉시 거부) |
| 호출(게이트 요청) | 진행 | **409** | **409** |
| 집행 시점(AD6, 승인 완료 후) | 집행 | **지연 큐에 적재 가능** | 집행 안 함, 승인 취소 |

**왜 fail-closed인가 — 세 논거.**

1. **"부분적으로 되는 것"에 대한 판단을 LLM에게 넘기지 않는다.** `degraded`를 가용으로 치면 에이전트에게 보이는 것은 "호출은 되는데 가끔 실패하는 액션"이고, 그러면 재시도 여부·횟수·대체 경로를 LLM이 결정하게 된다. §7.1이 세운 원리(*"방어를 프롬프트로 하지 않는다"*)의 가용성 판 대응물이다 — 큐잉은 **결정적 계층인 core의 정책**이어야지 모델의 판단이어서는 안 된다.
2. **유도된 열화가 게이트 우회가 되지 않게 한다.** `degraded`를 "진행하되 완화"로 다루면(예: *"어차피 실패할 테니 승인 없이 시도해 보게 하자"*), **열화를 유도할 수 있는 공격자가 곧 게이트를 낮출 수 있다.** 열화는 인젝션보다 유도하기 쉽다 — 외부 SMTP 릴레이에 대량 발송으로 레이트리밋을 유발하거나, SCM 업스트림에 5xx를 유발하거나, DNS 전파를 흔들면 된다(`81 §2.2`의 `degraded` 진입 조건들이 전부 **외부에서 관측·자극 가능**하다). 그래서 이 문서에서 **능력 상태는 어떤 축의 엄격도도 낮추지 않는다.** `degraded`가 할 수 있는 일은 오직 **차단**과 **지연**뿐이며, 둘 다 안전한 방향이다.
3. **3상태를 2상태로 뭉개는 것이 아니다.** 가용성 판정에서는 `degraded`와 `unavailable`이 같게 취급되지만, **거부 응답과 지연 큐 적격 여부에서는 끝까지 구별된다**(위 표). `81`이 두 상태를 나눈 목적(큐잉 가능 여부의 결정)은 정확히 보존되고, 다만 그 결정이 **가용성 게이트 앞이 아니라 뒤에서** 이루어진다.

**`effectClass`·`reversibility`·`taintClass`에 따라 답이 갈리는가 — 갈리지 않는다.** 셋 다 **차단 여부에는 영향을 주지 않는다**(전부 차단). 검토한 분기와 기각 사유:

- *`internal`은 통과시킬까?* — 무의미하다. `internal` 액션은 그래프만 만지며 `requiresCapability`가 비어 있다(§4.6의 `wbs.update_status`·`note.write`). 키를 요구하는 `internal` 액션이 나중에 생겨도 같은 규칙을 받는다 — 예외를 미리 파두면 그 구멍으로 `external_effect`가 재분류되어 들어온다.
- *`irreversible`만 막고 나머지는 진행시킬까?* — 기각. 그러면 `degraded`의 의미가 액션마다 달라지고, 조합(§6)에서 부품마다 다른 규칙을 join해야 한다. 더 결정적으로, `81`이 `degraded` 큐잉의 **동기 사례로 든 것이 발신 메일**인데 이 문서에서 `mail.send`는 `irreversible`이다 — 즉 "비가역은 큐잉 금지"는 canonical 문서의 동기 사례를 그대로 부정한다. 큐잉의 적격 조건은 가역성이 아니라 **"이미 사람의 승인을 받았는가"** 다.
- *`taintClass`로 갈릴까?* — 갈리지 않는다. 오염은 지연 큐에 들어가기 **전에** 이미 게이트로 흡수되었고(§7.3), 큐 항목은 그때의 `taintLevel`을 얼려서 갖는다. 시간이 지난다고 오염이 낮아지지 않는다(AD10의 단조성).

**지연 큐의 규율 (AC22).** 큐는 **core만 소유**한다(에이전트·supervisor의 큐는 능력 상태의 두 번째 사본이 되어 `81` CS7 위반이다). 큐 항목은 `{actionTypeId, 파라미터 실효값, boundRevision, taintLevel, grantedGateClass, derivedHash, 원 승인 id}`를 **얼려서** 갖고, **재시도 1회마다 AD6의 집행 시점 재평가를 통째로 다시 통과**해야 한다(정의가 그 사이 강해졌으면 재승인, 철회되었으면 폐기). 마감은 승인 유효 상한과 같은 **24시간**(`10 §8.2`)이며, 넘기면 큐 항목은 `CAPABILITY_UNAVAILABLE`로 실패하고 **새 승인을 요구한다**. 큐잉·집행·폐기는 전부 `action.deferred.*` 이벤트로 관측 가능하고, 지연 집행 시 승인자에게 알린다.

**조합 규칙과의 관계 — 최엄격 상속은 능력 상태에도 확장된다.** `requiresCapability` 자체가 합집합이므로(§6.2) 조합 액션의 능력 상태는 그 합집합에 속한 **모든 키의 상태의 meet**(가장 나쁜 것)이다. 순서는 `unavailable < degraded < available`이며 조합값은 **min**이다 — 부품 하나가 `degraded`면 조합은 `degraded`이고, 하나가 `unavailable`이면 조합은 `unavailable`이다. 이것은 새 규칙이 아니라 "모든 키가 `available`"이라는 AND 술어의 직접적 귀결이며, §6.2의 표에 한 행으로 명시한다. 여기서도 **키 단위**로 평가한다 — 조합이 커질수록 "스택 단위로 한 번만 묻자"는 유혹이 커지지만 그것이 정확히 `81` CS9가 금지한 것이다.

**잔여 위험 — 정직하게 두 가지.**

1. **가용성 공격은 열려 있다.** `degraded`를 유도할 수 있는 공격자는 그 키에 의존하는 액션을 **멈출 수 있다.** 이것은 fail-closed의 대가이며 완화하지 않고 인정한다 — §3.3이 `actiontype.write` 공격자에 대해 *"남는 실질 위험은 가용성"* 이라고 적은 것과 같은 층위다. 반대 선택(`degraded`에서 진행)의 대가는 **무결성**이었고, 가용성과 무결성 중 무결성을 택하는 것이 이 문서 전체의 방향이다.
2. **지연 큐는 시간 이동을 만든다.** 승인자가 본 세계와 실제 집행 시점의 세계가 최대 24시간 어긋날 수 있다. 다만 이것은 **이 결정이 새로 만든 위험이 아니다** — `10 §8.2`의 승인 대기 상한 24시간이 이미 같은 창을 갖고 있고, AD6의 재평가·파라미터 동결·`boundRevision` 고정이 그 창에 대해 이미 설계된 방어다. 지연 큐는 그 창을 **넓히지 않고 재사용**한다(마감을 같은 24시간으로 맞춘 이유가 이것이다).

### 5.4.2 AD15 — 게이트 전략: 가역성 축으로 분기 `[2026-08-13 신설, 감독 권고·사용자 승인]`

> **근거**: 자매 프로젝트 Blueurban AaaM(`Blueurban-GroupWare/DESIGN-AaaM.md`) D23이 실전 배치 2주 동안 사전 승인 일변도의 원안을 "자동 유지 + 3분 착수 유예 + 사후 취소"로 뒤집은 실측 사례. **OSiris는 이 결과를 전면 채택하지 않는다** — `reversibility`(`:ActionType.reversibility`, §4.2)라는 이미 존재하는 축에 맞춰 **분기 적용으로 축소 채택**한다.

이 문서는 지금까지 모든 게이트를 사전(§5.2) 하나의 모양으로만 다뤘다. AD15는 여기에 **집행 이후 유예 기간 동안 취소할 수 있는 두 번째 게이트 모양(사후 유예취소)** 을 도입하되, 그 적용 여부를 `reversibility`로 가른다.

| `reversibility` | 게이트 전략 | 이유 |
|---|---|---|
| `irreversible` | **사전 게이트 유지.** 유예취소 불가 | 발송된 메일, 발행된 릴리스는 되돌아오지 않는다 — 유예 기간 중 취소되지 않으면 그대로 돌이킬 수 없는 결과가 나므로 유예취소는 애초에 안전장치가 되지 못한다 |
| `reversible` | **사후 유예취소 허용 가능**(속도 우선) | 되돌릴 수 있으므로 "먼저 진행하고, 문제가 있으면 유예 기간 안에 취소"가 안전 마진을 갖는다 |
| `compensable` | **사전 게이트 유지가 기본** | 보상이 무료가 아니다 — `git.commit`은 `revert`로 되돌릴 수 있지만 이력에는 남는다(§4.6). "되돌릴 수 있다"와 "되돌리는 비용이 0이다"는 다른 말이며, 이 문서는 후자를 요구하지 않는 한 사전 게이트를 기본값으로 둔다 |

**결정적 제약 — 오염된 턴에서는 유예취소를 적용하지 않는다.** §7.3은 오염 턴의 `external_effect`를 승인 게이트로 **승격**시킨다(`gateFromTaint`). 이 승격이 성립한 자리에 유예취소를 허용하면, **오염을 유도할 수 있는 공격자가 게이트를 낮출 수 있다** — 이것은 §5.4.1이 `degraded`를 가용으로 치지 않는 이유로 든 논거(*"열화를 유도할 수 있는 공격자가 곧 게이트를 낮출 수 있다"*, AD14)와 **정확히 같은 구조**다. 열화 대신 오염을 유도 수단으로 바꾼 것뿐이다. 따라서:

```
graceCancelEligible(a, turn) :=
    reversibility(a) == 'reversible'
    ∧ taint(turn) == t0                      # 오염 턴이면 무조건 사전 게이트 (§7.3 승격 유지)
    ∧ ∃ effect e ∈ leafEffects(a): 등록된 반대 액션(counter-action)이 e를 취소·철회할 수 있다   # §4.6/§11 인계와 연결
```

**선행 조건 — 취소 액션이 등록되어야 성립한다.** 사후 유예취소는 "취소할 방법"이 실제로 존재할 때만 의미가 있다. 이 문서의 현재 카탈로그(§4.6)에는 반대 액션 범주가 전무하다(`pr.create`/`pr.merge`는 있고 `pr.close`가 없다) — 그 갭이 메워지기 전까지 `graceCancelEligible`은 **어떤 액션에 대해서도 사실상 거짓**이다. §11의 카탈로그 확충 인계(④ 나머지 행)에 "반대 액션 존재 여부" 체크를 추가한 것이 이 선행조건과 직결된다.

**AC 신설 여부 — 신설하지 않는다, 갭으로만 표기한다.** AD15는 새 결정이므로 원칙적으로 §9에 대응 불변식이 있어야 하지만, 이 문서는 **`AC23`을 §9에 등재하지 않는다** — 등재하면 `bin/lint-action-layer.sh`가 검사하지 않는 항목이 새로 생기고, AC 표는 "린트 또는 런타임/통합테스트로 실제 검사되는 것"이라는 지금까지의 관행(§9 서문)과 어긋난다. 대신:

- **`[갭]`**: 사후 유예취소 큐(§5.4.1의 능력 열화 지연 큐와는 **다른** 큐다 — 저것은 이미 승인된 요청을 늦게 집행하는 것이고, 이것은 승인 없이 먼저 집행한 뒤 유예 기간 동안 취소창을 여는 것이다)의 저장 위치·유예 시간·취소 UI·`taint(turn)`을 유예 기간 동안 다시 확인하는 재평가 루프(AD6과 같은 이유로 필요할 것) 중 **어느 것도 이 문서가 설계하지 않았다.** 구현 착수 전 별도 설계가 필요하다.
- G-A16(§10)으로 이 갭을 명문화한다.

---

## 6. 조합 규칙

### 6.1 무엇을 조합할 수 있는가

| 조합 가능 | 조합 **불가** |
|---|---|
| `status:'active'`인 `:ActionType`(`system` 또는 `composed`) | `draft`/`withdrawn`/`inert` 액션 |
| 이미 등록된 명명된 이펙트의 **선택** | 새 이펙트·새 술어·새 통합의 **등록** |
| 파라미터 매핑(부모 파라미터 → 부품 파라미터, 항등 또는 상수) | 파라미터의 **계산**(연결·산술·조건) |
| 순차 순서(`ord`) | 분기·반복·병렬·재시도 정책 |
| 추가 술어(부품이 요구하는 것 **위에**) | 부품이 요구하는 술어의 **제거** |

**조직이 조합할 수 없는 것을 한 줄로**: 새 실행 가능 코드, 새 외부 통합, 새 자격증명 경로. 이 셋은 릴리스 경계 안에만 있다(§5.3).

### 6.2 최엄격 상속 — 속성별 반격자(semilattice)

각 파생 속성은 **전순서 join-semilattice**이며, 조합값은 부품들의 **join(=max)** 이다.

| 속성 | 순서(느슨 → 엄격) | 조합값 |
|---|---|---|
| `gateClass` | `none` < `approval.team_lead` < `approval.admin` | max |
| `taintClass` | `neutral` < `escalate` < `forbidden` | max |
| `effectClass` | `internal` < `external_effect` | max |
| `reversibility` | `reversible` < `compensable` < `irreversible` (엄격 = 더 위험) | max |
| `requiresCapability` | 집합 포함 | **합집합 ∪** (AND 의미이므로 합집합이 곧 강화) |
| 능력 **상태**(파생, 저장 안 함) | `unavailable` < `degraded` < `available` (엄격 = 낮은 값) | **min** — 합집합에 속한 **모든 키를 개별 조회**한 결과의 meet. 하나라도 `degraded`면 조합은 `degraded`, 하나라도 `unavailable`이면 조합은 `unavailable`(§5.4.1). **스택 단위로 묶어 한 번에 묻지 않는다**(`81` CS9, AC21) |
| `REQUIRES_PERMISSION` | 집합 포함 | **합집합 ∪** — 조합 액션 호출자는 **모든** 부품 권한을 가져야 한다 |
| `validationPredicates` | 집합 포함 | **합집합 ∪** — 부품 술어는 하나도 빠지지 않는다 |
| `limitJson`의 수치 상한 | 큰 값 < 작은 값 | **min**(가장 낮은 상한이 이긴다) |
| `approverRole` | `team-lead` < `admin` | max |

`limitJson`이 min인 것에 주의: 이 축만 "엄격 = 작은 값"이다. 반격자의 방향은 **엄격한 쪽**이지 큰 값이 아니다.

### 6.3 실행 의미론

```
execute(composed c, params):
    for part in c.COMPOSED_OF order by ord:
        run all validationPredicates(part)      # 하나라도 거짓이면 중단
        run effects(part)                        # part 가 조합이면 재귀 (깊이 ≤ 3)
        on error:
            중단. 이후 ord 의 부품은 실행하지 않는다.
            이미 실행된 부품 중 reversibility=='compensable' 인 것만 보상 이펙트 실행.
            'irreversible' 부품은 보상하지 않고 사실만 기록한다.
            task.<taskId>.event{kind:'action.partial_failure', executedOrds:[…]}
```

**부분 실패를 사가(saga)로 자동 복구하지 않는다.** `10 §11`(쟁점 5)이 Temporal 도입을 기각한 것과 같은 판단이다 — 자동 보상은 그 자체로 새 외부 효과이며, 그 보상이 다시 게이트를 받아야 하는지의 문제를 낳는다. **사실을 정확히 기록하고 사람에게 넘긴다.**

### 6.4 다운그레이드 불가능성 — 증명 스케치

**주장.** 임의의 조합 액션 `c`와 그 임의의 부품 `a`에 대해, `c`를 통한 `a`의 실행은 `a`를 직접 실행하는 것보다 **결코 덜 엄격하지 않다**.

**증명.**

1. (join의 성질) 각 파생 속성 `p`에 대해 `p(c) = ⋁_{a ∈ parts(c)} p(a)`이고, join-semilattice의 정의에 의해 `⋁ S ≥ s (∀s ∈ S)`이다. 따라서 `p(c) ≥ p(a)`. `limitJson`은 순서를 뒤집어 정의했으므로 min이 곧 join이며 같은 결론이 성립한다.
2. (재귀) `COMPOSED_OF`는 사이클이 없고 깊이 ≤ 3이므로(CA5) 위 논증을 leaf까지 유한 회 적용할 수 있다. 따라서 `p(c) ≥ p(leaf)`가 모든 leaf에 대해 성립한다.
3. (**핵심 — 저장값을 믿지 않는다**) 1·2는 *저장된* `p(c)`가 join으로 계산되었을 때만 성립한다. 저장은 데이터이므로 공격자가 `gateClass:'none'`인 조합 행을 직접 쓸 수 있다. 그러므로 **집행자는 저장값을 쓰지 않는다** — `core.execute`는 `COMPOSED_OF`를 leaf까지 전개해 `p`를 **다시 계산**하고(AD6/AD8), 재계산값과 저장된 `derivedHash`가 다르면 집행을 거부하며 `security.actiontype.drift`를 발행한다(AC8). 즉 집행에 실제로 쓰이는 값은 항상 재계산된 join이다.
4. (호출 인가) `REQUIRES_PERMISSION`도 합집합으로 재계산되므로, 부품 권한을 갖지 못한 주체가 조합을 통해 그 부품을 실행하는 경로가 없다.

∎ **따라서 조합은 게이트·오염·권한 어느 축에서도 다운그레이드 수단이 되지 않는다.** 그리고 3이 없으면 1·2는 무력하다 — 이 증명이 실제로 의존하는 것은 반격자가 아니라 **재계산**이다.

### 6.5 증명이 닫지 **못하는** 것 — 서술 다운그레이드

위 증명은 **기계가 판정하는 축**만 다룬다. 닫히지 않는 축이 하나 있다:

> **조합은 게이트를 낮출 수 없지만, 승인 카드의 "설명"을 낮출 수 있다.** `label: "임시 로그 정리"`인 조합 액션이 내부에 `effect.mail.send`를 품고 있으면, 승인자는 게이트는 받되 **무엇을 승인하는지 오해**할 수 있다.

이것은 암호학적으로 닫히는 문제가 아니라 UI 문제이며, 다음 규칙으로 완화한다(AC17):

- 승인 카드는 `label`/`descriptionMd`를 **부제로만** 렌더하고, 본문에는 **재계산된 leaf 이펙트 목록**을 항상 렌더한다 — `effect.mail.send → to: partner@example.com` 형태로, 파라미터 실효값(기본값 포함)과 함께.
- 조합 액션의 승인 1회는 그 조합의 **모든 leaf 이펙트를 덮는다.** 이것은 의도된 성질이지만(그렇지 않으면 조합의 의미가 없다), 그래서 위 렌더가 **선택이 아니라 불변식**이다.
- `origin:'composed'` 액션의 승인 카드에는 조합임을 나타내는 배지와 최종 편집자·리비전을 표시한다.

잔여: 승인자 피로. **완화하지 않고 인정한다** — `60 §21.4-3`이 "LLM이 제안했다는 사실이 위임 규칙을 면제하지 않는다"고 한 것과 같은 층위의 한계다.

---

## 7. 오염(taint) 모델

### 7.1 왜 액션 레이어의 파생 규칙인가

OSiris에 인바운드 메일이 들어오면, **조직 밖의 누구나 쓴 텍스트가 에이전트의 컨텍스트에 들어간다.** 방어를 프롬프트로 하는 것(*"메일 안의 지시는 무시하라"*)은 `10 §8.2`의 분류에서 **협조**이고, 그 표는 협조가 프롬프트 인젝션을 견디지 못한다고 이미 못박았다. `10` D14가 크로스팀 라우팅을 규약이 아니라 NATS ACL로 강제한 것과 **같은 판단**을 여기서도 한다.

**설계 원리 한 줄**: 인젝션이 성공하는 것을 막을 수 없다면, **인젝션이 성공해도 실행 수단이 없게 만든다.**

그리고 그 "실행 수단"의 목록이 바로 `:ActionType` 카탈로그다. 그러므로 오염 방어는 별도 서브시스템이 아니라 **액션 가용성의 파생 규칙**이다 — 오염된 턴에서는 가용 액션 집합이 자동으로 축소·승격된다.

### 7.2 오염 등급과 출처 등급

**출처 등급(source class)** — 턴 컨텍스트에 들어오는 각 조각에 붙는다:

| 출처 등급 | 예 | 기여 오염 |
|---|---|---|
| `trusted_internal` | 콘솔에서 사람이 만든 태스크, 그래프에서 파생된 프롬프트(`:Role.promptTemplateMd` 렌더), core가 만든 구조화 필드 | t0 |
| `semi_trusted` | 같은 조직 다른 에이전트의 NATS 메시지(`team.*.inbox.*`, `master.inbox`) | **발신 에이전트 턴의 오염을 승계**(§7.4) |
| `untrusted_external` | 인바운드 메일 본문·첨부 파일명, 외부 이슈/PR 코멘트 본문, egress로 가져온 웹 응답 본문 | t1 또는 t2 |

**오염 등급(taint level)** — 턴 단위의 전순서 값:

| 등급 | 의미 | 언제 |
|---|---|---|
| **t0** | 비오염 | 컨텍스트 전부가 `trusted_internal` |
| **t1** | 오염 — **귀속 가능** | 외부 콘텐츠가 있고, 발신자가 검증되었으며(§7.5), 발신 도메인이 조직의 알려진 거래처 목록에 있다 |
| **t2** | 오염 — **귀속 불가** | 외부 콘텐츠가 있고, 검증 실패이거나 미지 도메인이거나 검증 자체가 불가능한 경로(웹 응답 등) |

### 7.3 `taintClass` — 액션이 오염 턴에서 무엇을 하는가 (데이터)

`:ActionType.taintClass`가 이 규칙을 **행마다 데이터로** 선언한다.

| `taintClass` | t0 | t1 | t2 |
|---|---|---|---|
| `neutral` | 정의된 게이트 | 정의된 게이트 | 정의된 게이트 |
| `escalate` | 정의된 게이트 | **max(정의, `approval.team_lead`)** | **max(정의, `approval.admin`)** |
| `forbidden` | 정의된 게이트 | **호출 불가**(409 `taint_forbidden`) | **호출 불가** |

파생 함수:

```
gateFromTaint(a, level) :=
    a.taintClass == 'neutral'    → none
    a.taintClass == 'escalate'   → { t0: none, t1: approval.team_lead, t2: approval.admin }[level]
    a.taintClass == 'forbidden'  → { t0: none, t1: DENY, t2: DENY }[level]
```

**기본값 규칙**: `effectClass:'external_effect'`인 액션은 `taintClass`가 최소 `escalate`여야 한다(AC15). `neutral`인 `external_effect` 액션은 graph-write가 거부한다 — 이것이 브리프의 요구(*"오염 턴에서 모든 `external_effect`가 사람 승인으로 승격"*)를 데이터 불변식으로 표현한 형태다. `irreversible`인 것은 `forbidden`을 권고하되 강제하지는 않는다(`mail.send`가 `irreversible`이지만 오염 턴에서 승인 하에 답장을 보내는 것은 정당한 업무다).

**`gateFor`는 `:Party`를 인자로 받지 않는다.** `gate(a, turn) = max(gate@bound, gate@current, gateFromTaint(a, taint(turn)))` 어디에도 주체가 없다 — 이것은 **요구되는 게이트 등급의 파생**이지 인가 판정이 아니다. 인가는 그 뒤에 기존 `authorize(principal, code)`와 기존 승인 파이프라인이 한다. **AC7이 이 서명을 린트로 고정**하며, 그것이 이 문서가 세 번째 인가 엔진을 만들지 않았다는 기계 검증 가능한 증거다(AD4, HI16 보호).

### 7.4 성립·전파·해제

#### 성립 — core가 소유한다 (AD9)

```
인바운드 메일 → 격리된 메일 어댑터 (50 §2.2 등급) → osiris-core:8091
   core 가 하는 일 (어댑터의 판단을 신뢰하지 않는다):
     1. 원문 헤더를 core 가 직접 재파싱해 인증 결과를 판정 (§7.5)
     2. :Artifact{kind:'inbound_message', uri, sha, createdAt} 생성   ← 영구 증거
     3. taskId 를 만들고  core 의 기록에  taskId → {taintLevel, artifactIds[]}  저장
     4. team.<team>.inbox.<employeeId> 로 인박스 메시지 발행 (본문 + taskId + boundRevision)
```

**이것이 이 모델을 강제 층으로 만드는 지점이다.** 나중에 에이전트가 게이트를 호출하면 core는 **요청 바디의 오염 값을 읽지 않고** 자기 기록의 `taskId → taintLevel`을 본다. 에이전트가 자기 오염도를 낮춰 주장하는 것은 **표현 자체가 불가능**하다. `10 §8.5`가 genesis 스코프를 "표현 불가능하게" 만든 것과 같은 기법이다. **AC13이 게이트 핸들러가 요청 바디의 taint 필드를 읽지 않음을 린트로 검사한다.**

#### 전파

| 경로 | 규칙 |
|---|---|
| 턴 내부 | `taint(turn) = max` over 모든 컨텍스트 조각의 기여 오염 |
| 턴 → 턴 (`--resume`) | **세션의 `taintFloor`**: 그 세션에서 관측된 최대 오염도. `taint(turn) = max(taintFloor(session), 이번 턴 기여분)`. `--resume`이 이전 컨텍스트를 그대로 되살리므로 이것 외의 규칙은 거짓이 된다 |
| 에이전트 → 에이전트 (NATS) | 발신 에이전트가 오염 턴에서 발행한 메시지는 **core가 그 사실을 기록**하고(발행 시점의 `taskId` → 오염도), 수신 에이전트의 턴에 `semi_trusted` 기여분으로 승계된다. **한 단계 감쇠 없음** — 인젝션은 중계로 희석되지 않는다 |
| 조합 | `taintClass`는 join(§6.2). 부품 중 하나라도 `forbidden`이면 조합도 `forbidden` |

**크로스에이전트 전파가 특히 중요하다.** 이것이 없으면 인젝션된 에이전트가 `master.inbox`로 요청을 던져 **비오염 에이전트에게 대신 실행시키는** 경로가 열린다(`10 §7.3`이 강제한 라우팅이 그대로 공격 통로가 된다). 승계로 이 경로를 닫는다.

#### 해제 — 세션 폐기뿐 (AD10)

**세션 안에서 오염 등급은 단조 비감소다.** 낮추는 연산은 하나뿐이다: **`--resume` 없이 새 `sessionId`로 시작하는 것.** 그것은 이전 컨텍스트를 실제로 버리는 것이므로 오염이 사실로서 사라진다.

- 누가 폐기하는가: supervisor가 태스크 경계에서 자동으로(오염 태스크는 전용 세션에서 처리하고 끝나면 폐기), 또는 사람이 콘솔에서 명시적으로.
- **"이 부분은 무시해도 된다"는 판단을 LLM이 내리는 경로는 존재하지 않는다.** 존재하면 방어가 프롬프트 방어로 되돌아간다.
- **`[갭]` 파일 경유 잔류 오염**: 세션을 폐기해도 오염 턴이 `/workspace`에 쓴 파일은 남고, 새 세션이 그것을 읽으면 오염이 사실상 부활한다. §7.7-1에서 정직하게 다룬다.

### 7.5 발신자 검증(SPF/DKIM/DMARC)과의 상호작용

**핵심 명제: SPF/DKIM/DMARC는 신뢰를 증명하지 않는다. 귀속(attribution)을 증명한다.**

DMARC pass는 *"이 메일이 주장하는 도메인에서 실제로 왔다"*를 말할 뿐, *"그 도메인의 사람이 정직하다"*를 말하지 않는다. 그러므로 **검증 통과가 t0을 만들지 않는다** — 통과해도 외부 콘텐츠는 오염이다. 검증이 바꾸는 것은 **오염의 등급**과 **사후 감사의 강도**다.

| 인증 결과 | 도메인 | 오염 | 근거 |
|---|---|---|---|
| DMARC pass (SPF 또는 DKIM 정렬) | 조직의 알려진 거래처 목록에 있음 | **t1** | 나중에 "누가 보냈는가"를 답할 수 있다. 사고가 나면 실제 상대에게 책임을 물을 수 있다 |
| DMARC pass | 미지 도메인 | **t2** | 귀속은 되지만 상대가 누구인지 조직이 모른다 |
| DMARC fail / none / 헤더 부재 | 무관 | **t2** | 귀속 불가 |
| **수신 MTA가 우리 것이 아님** | 무관 | **t2 (무조건)** | 아래 |

**신뢰 경계 — 왜 마지막 행이 필요한가.** `Authentication-Results` 헤더는 **수신 MTA가 붙이는 주장**이다. 그 MTA가 우리 통제 밖이면 헤더 자체가 위조 가능한 입력이고, 그것을 근거로 t1을 부여하는 것은 오염된 데이터로 오염 판정을 하는 것이다. 그러므로:

> **core는 자신이 신뢰하는 메일 스택(스택 선언으로 등록된 것)이 붙인 인증 결과만 인정하고, 그 외에는 무조건 t2를 부여한다.**

**`81 §4.5` 확정 후의 정밀화 — 무엇을 core가 다시 하고 무엇을 하지 않는가.** 이 문서의 이전 판은 *"어댑터는 격리된 제3자 코드 등급이므로(`50 §2.2`) core가 원문 헤더를 직접 재파싱한다"* 고 적었다. `81`이 메일을 **1급 능력 스택**으로 세우면서 그 전제가 바뀌었다 — 인바운드의 상류는 제3자 어댑터가 아니라 신뢰 경계 안에서 오퍼레이터가 수렴시킨 스택이고, `81 §4.5`의 규율 1이 *"외부에서 도착한 동명 `Authentication-Results` 헤더는 **제거(strip)** 한 뒤 자신의 것을 삽입한다"* 를, 규율 2가 *"`externalTrust`는 파생값이며 **스택이 계산한다**"* 를 이미 못박았다. 두 문서가 같은 파싱을 두 벌 하면 조합 규칙이 어긋나므로(`81` C-3의 단일 해석기 원칙과 같은 이유), 역할을 이렇게 나눈다:

| 무엇 | 누가 | 근거 |
|---|---|---|
| 외부 `Authentication-Results` strip + 자기 판정 삽입, `authResults.{spf,dkim,dmarc}`·`externalTrust` 계산 | **메일 스택** | `81 §4.5` 규율 1·2 |
| 그 `:InboundMessage`가 **등록된 신뢰 스택에서 왔는가**(`stackId` 대조), 필수 필드가 전부 채워졌는가, 낙관적 채움(`81 §4.5` 규율 3 위반)이 없는가 | **core** | 스택은 신뢰 경계 안이지만 **입력 검증까지 면제되지는 않는다** |
| `authResults`/`externalTrust` → **오염 등급(t0/t1/t2)** 파생 | **core, 단독** | AD9. 오염 등급은 이 문서의 소유이며 스택은 오염이라는 단어를 모른다(`81 §4.8`) |

즉 **core는 헤더를 다시 파싱하지 않고 오염을 다시 파생한다.** 바뀐 것은 파싱의 위치뿐이고, *"에이전트 컨테이너 밖의 무엇도 오염 값을 주장할 수 없다"*(AD9)는 성질은 그대로다 — 스택이 주장하는 것은 `spf=pass` 같은 **사실**이지 `taintLevel`이 아니다. 그리고 신뢰 스택 밖에서 온 메시지는 위 표의 두 번째 행에서 걸려 무조건 t2가 된다.

`[미검증]` **잔존.** 어떤 MTA 구현체를 쓸지는 여전히 미정이다 — `81` Q4가 *"이 문서는 역할만 정하고 구현체(Postfix/Dovecot, docker-mailserver, Stalwart 등)를 고르지 않는다"* 로 열어 두었다. 따라서 선택된 MTA가 `81 §4.5`의 필드 전부를 실제로 산출할 수 있는지는 구현체 확정 후 실측해야 한다. **바뀐 것**: 이제 미검증인 것은 "필드 계약이 무엇인가"가 아니라 "특정 구현체가 그 계약을 만족하는가"다 — `81 §4.5`가 필수 필드 목록을 확정했으므로 계약 자체는 더 이상 가정이 아니다.

### 7.6 오염 provenance — "이 커밋은 외부 메일에서 왔다"를 나중에 증명하기

세 층에 기록하며, **보존 기간이 다르다**:

| 층 | 무엇 | 보존 |
|---|---|---|
| `TASKS` 스트림 | `task.<taskId>.event{kind:'turn.begin', taintLevel, taintSources[], boundRevision}` / `action.invoke{actionTypeId, taintLevel}` | 90일 |
| **그래프 — `:Approval`** | `taintLevel` 속성 + `(:Approval)-[:TAINTED_BY {taintLevel}]->(:Artifact{kind:'inbound_message'})` + `(:Approval)-[:FOR_ACTION]->(:ActionType)` | **영구** |
| **그래프 — 산출물 계보** | `(:Artifact{kind:'commit'})-[:DERIVED_FROM]->(:Artifact{kind:'inbound_message'})` | **영구** |

`DERIVED_FROM`은 **기존 관계 타입**(`20 §3.4`, PROV-O `prov:wasDerivedFrom`)이며 새로 만들지 않는다. core가 이펙트를 집행하며 `:Artifact`를 만들 때, 그 턴의 오염 출처 `:Artifact`들로 `DERIVED_FROM`을 함께 건다.

감사 질의 (`20 §10`의 Q 형식):

```cypher
// Q-A1. 이 커밋은 외부 메일에서 유래했는가 — 그리고 어느 메일에서?
MATCH (c:Artifact {kind:'commit', sha:$sha})-[:DERIVED_FROM*1..3]->(m:Artifact {kind:'inbound_message'})
OPTIONAL MATCH (ap:Approval)-[t:TAINTED_BY]->(m)
OPTIONAL MATCH (ap)-[:FOR_ACTION]->(at:ActionType)
OPTIONAL MATCH (ap)-[:DECIDED_BY]->(p:Party)
RETURN c.sha, m.uri, m.createdAt, t.taintLevel, at.id, p.name AS approvedBy, ap.decidedAt
ORDER BY m.createdAt;

// Q-A2. 지난 90일간 오염 턴에서 나온 승인 전부 — 승인자별 (승인자 피로 관측용)
MATCH (ap:Approval)-[:TAINTED_BY]->(:Artifact)
WHERE ap.decidedAt > datetime() - duration('P90D')
MATCH (ap)-[:DECIDED_BY]->(p:Party)
MATCH (ap)-[:FOR_ACTION]->(at:ActionType)
RETURN p.name, ap.taintLevel, at.id, count(*) AS n
ORDER BY n DESC;
```

**`[갭]` 게이트를 받지 않는 오염 액션의 이력은 90일 후 소실된다.** `:Approval`이 생기지 않으므로 영구 기록이 없다(`TASKS`만 남고 90일 만료). 그래프에 호출 노드를 만들지 않는다는 P6 결정의 직접적 대가다. 필요해지면 `taintClass != 'neutral'`인 액션의 호출에 한해 경량 `:ActionInvocation` 노드를 도입하는 것이 최소 변경 경로이며, **지금은 도입하지 않는다.**

### 7.7 잔여 위험 — 정직하게

이 설계로도 인젝션이 여전히 달성할 수 있는 것을 열거한다. **완화하지 않고 인정하는 것과, 완화 경로가 있는 것을 구분한다.**

1. **`[갭]` 오염 세탁(taint laundering) — 가장 큰 잔여 위험.**
   오염 턴에서 `external_effect`는 막히지만 **`internal` 액션은 그대로 실행된다.** 인젝션은 `/workspace`의 소스에 백도어를 쓸 수 있고, 그 파일은 세션 폐기 후에도 남는다. 나중에 **비오염 턴**의 에이전트가 그 코드를 정상 작업의 일부로 커밋하면, 게이트는 통과하고 `DERIVED_FROM` 계보도 붙지 않는다 — **오염이 파일을 경유해 세탁된다.**
   *부분 완화*: supervisor가 오염 턴에서 변경된 파일 경로 집합을 core에 보고하고, core가 이후 커밋 diff와 교집합을 계산해 오염을 승계시킨다. **그러나 supervisor의 보고는 협조 층**이므로 컨테이너를 장악한 공격자에게는 무력하다.
   *닫으려면 필요한 것*: **워크스페이스 변경의 권위를 core가 갖는 것.** core는 이미 커밋 diff를 본다(`10 §8.2`의 강제). 여기에 더해 core가 턴 경계마다 워크스페이스 스냅샷 해시를 취하면(에이전트 협조 없이 볼륨을 읽어), "이 diff의 어느 hunk가 어느 턴에서 생겼는가"가 core 쪽 사실이 된다. 비용은 턴마다의 스냅샷 I/O이며, **이 문서는 그것을 설계하지 않고 갭으로 표시한다.**
2. **승인 UI를 통한 사회공학 — 부분 완화.**
   인젝션이 그럴듯한 커밋 + 설득력 있는 승인 설명을 만들어 승인자를 통과시킬 수 있다.
   *완화(설계함)*: ① 승인 카드에 오염 배지와 원문 메일 링크를 **항상, 접을 수 없게** 표시한다. ② `t2`는 `approval.admin`만 승인 가능. ③ **승인 카드에서 LLM이 생성한 자유 서술과 결정적으로 파생된 필드를 시각적으로 분리**하고, 오염 턴에서는 LLM 서술을 **기본 접힘**으로 렌더한다 — 승인자가 먼저 보는 것이 항상 구조화된 사실(어떤 이펙트, 어떤 파라미터 실효값, 어떤 대상)이 되도록. ④ AC17(leaf 이펙트 렌더).
   *잔여*: 승인자 피로. Q-A2가 그것을 **관측 가능**하게 만들 뿐 막지는 못한다. **완화하지 않고 인정한다.**
3. **내부 그래프 상태 오염 — 인정.**
   `wbs.update_status`, 태스크 반려, 잘못된 배정 요청 같은 `internal` 액션은 오염 턴에서도 실행된다. 인젝션은 이것으로 조직의 업무 흐름을 교란할 수 있다.
   *왜 막지 않는가*: 이 액션들을 오염 턴에서 막으면 **인바운드 메일을 처리하는 것 자체가 불가능해진다**(메일을 읽고 태스크 상태를 갱신하는 것이 정확히 그 업무다). 그리고 이 변경은 전부 `:OrgRevision`/`:Assignment` 이력에 남아 **가역**이다.
   *경계*: `internal`이면서 비가역인 액션은 존재해서는 안 된다. `effectClass:'internal' ∧ reversibility:'irreversible'`인 `:ActionType`은 graph-write가 **거부**한다(AC15의 짝).
4. **유출(exfiltration) — 대부분 강제 층이 막는다.**
   인젝션이 비밀을 외부로 보내려면 egress를 지나야 하고, egress 허용목록은 **강제**다(`10 §8.2`). 남는 경로는 허용목록 안의 목적지로의 유출(예: `github.com`의 공개 PR 코멘트)이며, 그것은 `pr.comment`가 `taintClass: escalate`로 게이트를 받는 이유다. `ops.log.*`로의 유출은 조직 내부에 머문다.
   *잔여*: 허용목록 안 목적지 + 게이트 없는 액션의 조합. 현재 카탈로그에는 그런 행이 없으나, **새 액션을 추가할 때마다 확인해야 하는 항목**이다 — 액션 추가 리뷰 체크리스트에 명문화한다.
5. **`[미검증]` egress 응답 본문의 오염 부여 — 경합이 있다.**
   메일과 달리 웹 응답은 core를 지나지 않는다(에이전트 → `osiris-egress` → 외부). 따라서 core는 그 콘텐츠가 컨텍스트에 들어온 것을 모른다.
   *설계*: core는 게이트 시점에 **egress 감사를 당겨 읽는다**(push 소비가 아니라 pull) — *"이 `employeeId`가 이 `taskId`의 창(window) 동안 egress 요청을 했는가"*. 했으면 그 턴의 오염 하한을 t2로 올린다. `ops.egress`는 이미 JetStream `EGRESS` 스트림에 30일 보존된다(`10 §7.2`).
   *왜 pull인가*: push 소비에 의존하면 "core가 이벤트를 소비하기 전에 에이전트가 게이트를 호출하는" 경합이 생긴다. 게이트 시점의 pull 질의는 그 경합을 없앤다.
   *`[미검증]`*: squid 감사 레코드가 `employeeId`와 시각으로 이 질의를 만족시킬 만큼 구조화되어 있는지 실측하지 못했다.
6. **인정 — `org.admin` 침해.** 이 층위의 어떤 장치도 `org.admin`을 침해한 공격자를 막지 않는다. `60 §21.4-1`이 이미 인정한 한계이며 이 문서가 늘리지도 줄이지도 않는다.

### 7.8 승인 확정 경로 — 터미널 승인 금지 `[2026-08-13 신설]`

> **근거**: 자매 프로젝트 Blueurban AaaM(`Blueurban-GroupWare/DESIGN-AaaM.md`) §13 기각 목록.

**규칙**: **승인의 확정은 세션 인증된 REST 경로를 통해서만 이루어진다.** 터미널·에이전트 트랜스크립트에 승인 문구("승인합니다", "approve" 등)를 타이핑하는 것은 승인이 아니다 — 그것은 **모델이 해석한 기록이지 사람이 승인한 기록이 아니다.** 사람이 실제로 세션 인증된 화면에서 승인 버튼을 눌렀는지, 아니면 에이전트 트랜스크립트에 그런 취지의 텍스트가 있었을 뿐인지는 근본적으로 다른 사실이고, 후자를 승인으로 인정하면 §7.7-2가 막으려는 "승인 UI를 통한 사회공학"과 같은 종류의 구멍이 **UI를 거치지도 않고** 열린다.

**왜 OSiris에 특히 중요한가.** `agent/internal/supervisor/gate.go`가 `approval.decision.<id>`를 **구독만** 하고 있고, 이 서브젝트의 **발행자가 현재 0건**이다(`gateMaxWait = 24 * time.Hour` 동안 블로킹만 하다가 timeout으로 빠진다 — §5.2.4의 24시간 상한이 바로 이 코드다). 즉 승인 확정 경로 자체가 아직 배선되지 않은 상태다. **이 미배선 상태에서 P4(구현 단계)가 이 게이트를 실제로 배선할 때, "에이전트 터미널이 승인을 실어 나르게" 만드는 것이 구현 난이도상 가장 쉬운 유혹이다** — supervisor 프로세스가 이미 그 트랜스크립트를 보고 있고, 거기서 승인 문구를 파싱해 `approval.decision.<id>`를 대신 발행하면 REST 표면을 하나도 새로 만들지 않고 이 24시간 블로킹을 즉시 풀 수 있어 보이기 때문이다. **이 규칙은 정확히 그 지름길을 금지한다** — `approval.decision.<id>`의 유일한 정당한 발행자는 세션 인증된 콘솔/REST 경로이며, `osiris-supervisor`(§3.2가 이미 "Neo4j에 접속하지 않는다"고 못박은 그 컴포넌트)나 에이전트 프로세스가 그 서브젝트에 발행하는 코드는 어떤 형태로도 들어와서는 안 된다.

**감독이 확인한 것**: `docs/design/` 전수 grep 결과 이 규칙은 **이번 개정 이전에는 어느 문서에도 없었다.** §7.7-2(승인 카드 3규칙: 오염 배지 접기 불가·LLM 서술 기본 접힘·구조화 필드 우선)는 승인 카드가 **무엇을 보여주는가**를 규율했을 뿐, 승인이 **어느 경로로 확정되는가**는 다루지 않았다 — 이번 신설이 그 공백을 메운다.

---

## 8. 기존 다섯 기구의 흡수 경로

| # | 기구 | 판정 | 이 문서 이후의 상태 |
|---|---|---|---|
| M1 | grant-catalog (`10 §8.1`) | **유지 + 역할 정밀화** | 4층 파생은 그대로 grant-catalog. 단 `gate`/`approverRole`/`limitJson`의 **권위가 `:ActionType`으로 이동**(AD13) |
| M2 | git 커밋 승인 파이프라인 (`10 §8.2`) | **흡수(일반화)** | 파이프라인 무변경. 하드와이어된 `git.commit`이 `:ActionType` 한 행 + `effect.scm.commit`이 된다 |
| M3 | `:WorkItem.actionClass` (`20 §3.4`) | **파생으로 강등** | 손 저작 폐지. 액션의 `effectClass` join에서 계산 |
| M4 | `authz.CheckDelegation` / DG1~DG5 (`60 §21.1`) | **별개 유지 + 규칙 추가** | 같은 함수에 DG6~DG8 추가. **HI16 무손상** |
| M5 | genesis 스코프 허용목록 (`10 §8.5.1`) | **별개 유지 — 영구히** | 흡수하지 않는다 |

### 8.1 M1 — grant-catalog: 경계선을 정확히 긋는다

**두 표는 서로 다른 질문에 답한다.**

| | grant-catalog (`:Permission`) | `:ActionType` |
|---|---|---|
| 질문 | *"이 컨테이너가 무엇에 손댈 수 있는가"* | *"그 손댐이 무슨 행위이며 무슨 조건을 받는가"* |
| 산출 | RBAC · egress 허용목록 · allowedTools/deny · NATS ACL | 파라미터 · 검증 · 이펙트 · 게이트 등급 · 오염 등급 · 필요 능력 키 |
| 저장 | **코드**(`orchestrator/internal/catalog/catalog.go`) | **그래프** |
| 릴리스 주기 | 바이너리 | `:OrgRevision` |

**AD13 — `gate` 필드의 권위 이관.** 현재 `:Permission.gate`(`20 §3.2`)와 grant-catalog `Grant.Gate`가 게이트를 들고 있다. 액션이 게이트를 갖게 되면 같은 사실이 두 곳에 생긴다. 그래서:

- **권위**: `:ActionType.gateClass` / `.approverRole` / `.limitJson`.
- **파생 미러**: `:Permission.gate` = `그 코드를 요구하는 모든 :ActionType의 gateClass의 min`. 왜 min인가 — `:Permission.gate`의 소비자는 grant-catalog이고, 그것이 결정하는 것은 *"이 도구를 MCP 게이트 경로로 노출할 것인가, 평범한 Bash로 노출할 것인가"*다. 하나라도 게이트 없는 액션이 있으면 도구 자체는 게이트 없이 노출되어야 하고, **실제 게이트는 액션 단위로 core가 건다.** 반대(max)로 하면 게이트 없는 액션까지 승인 대기에 걸려 업무가 멈춘다.
- **직접 쓰기 금지**: `:Permission`의 이 세 필드는 graph-write가 직접 쓰기를 거부한다. 부팅 시 파생값과 저장값이 다르면 **부팅 실패**(I9와 같은 등급, AC20).
- **D13은 깨지지 않는다.** D13은 *"권한은 하나의 선언에서 네 층위로 파생된다"*이며, 이 이관은 그 "하나의 선언"이 어디인지를 정확히 하는 것이지 선언을 둘로 늘리는 것이 아니다.

### 8.1.1 — 3값 → 2값 축소 규칙 `[2026-08-15 명문화]`

위의 파생 공식에서 `:ActionType.gateClass`는 3값(`none` / `approval.team_lead` / `approval.admin`)이지만, `20 §7.2`의 `:Permission.gate`는 2값(`none` / `approval`)이다. min을 그대로 대입할 수 없으므로:

- min-rank == `gateClassRank["none"]` (0) ⟹ `gate` = `"none"`
- min-rank > 0 ⟹ `gate` = `"approval"`

이 축소는 안전한 방향이다: `approval.team_lead`와 `approval.admin`을 모두 `approval`로 접는 것은 grant-catalog 관점에서 *"이 도구를 MCP 게이트 경로로 노출한다"*는 동일한 결론을 낳으며, 두 승인 등급의 구분은 `:ActionType.approverRole`이 액션 단위로 계속 보유하므로 정보가 소실되는 것이 아니라 grant-catalog 층에서 필요 없을 뿐이다. 이 규칙은 `core/internal/graphdb/actiontype_permission_mirror.go`의 `gateClassRank` 및 `derivePermissionGate` 함수에 구현되었으며, `core/cmd/osiris-core/main.go`의 AC20 부팅 검사가 이것을 실행한다 (AC20 §9 참조).

이전에는 이 축소 규칙이 문서 근거 없이 코드 내 `[판단]` 자인으로만 존재했다 — `[2026-08-13]` 구현 완료 이후 `[2026-08-15]`에 문서 명시.

### 8.2 M2 — 파이프라인의 일반화 (이 문서의 하중)

`10 §8.2`의 시퀀스는 **한 글자도 바뀌지 않는다.** 바뀌는 것은 각 단계가 **무엇을 참조하는가**다:

| 단계 | 지금 | 이후 |
|---|---|---|
| MCP 툴 노출 | grant-catalog의 `MCPTools` 하드코딩 | 동일 — 단 `requiresCapability` 미해소 시 렌더되지 않음 |
| supervisor `:8081/gate` | `approvalType`을 툴 이름에서 유추 | `actionTypeId`를 그대로 실어 보냄 |
| `approval.request` 페이로드 | 액션별 애드혹 필드 | `{actionTypeId, params, boundRevision, taintLevel, taintSources[]}` **고정 스키마** |
| 콘솔 승인 카드 | 커밋 diff 전용 렌더러 | `:ActionType`에서 렌더(파라미터 스키마 → 필드, leaf 이펙트 목록, 오염 배지) |
| core 집행 | `git commit` 하드코딩 | `effects`를 `ord` 순으로 실행 + AD6 재평가 |
| 새 액션 추가 비용 | **7개 지점 수정 + 릴리스** | **`:ActionType` 한 행**(이펙트가 이미 등록된 경우) / 이펙트 등록 + 한 행(새 통합) |

마지막 행이 §2.2가 제기한 문제의 답이다. 30~40행 카탈로그에서 **행마다 반복되는 배선이 0이 된다.**

### 8.3 M3 — `:WorkItem.actionClass`를 파생으로 강등

**다리**는 기존 관계만으로 이미 존재한다:

```cypher
MATCH (w:WorkItem {id:$id})-[:REQUIRES_CAPABILITY]->(:Capability)
      -[:REQUIRES_PERMISSION]->(p:Permission)<-[:REQUIRES_PERMISSION]-(a:ActionType)
WHERE a.status = 'active'
RETURN CASE WHEN any(x IN collect(a.effectClass) WHERE x = 'external_effect')
            THEN 'external_effect' ELSE 'internal' END AS derivedActionClass;
```

- **전이 절차**: `actionClass` 속성은 남기되(기존 질의 보호), graph-write가 쓰기 시 파생값과 대조한다. 불일치는 온보딩 경고 등급(`I13`류)으로 커밋 전에 보여준다 — `20 §7.2`의 `INERT_CAPABILITY` 경고와 같은 형태. 손 저작은 **폐지**한다.
- **정직한 성질 — 과승인(over-approximation)이 생긴다.** 역량이 넓은 업무(`cap.code.python`이 `git.commit`을 요구)는 실제로 커밋하지 않더라도 `external_effect`로 판정된다. **이것은 안전한 방향의 오류**이며, 손으로 쓴 플래그가 조용히 `internal`로 틀리는 것보다 낫다. 정밀도가 필요해지면 `(:WorkItem)-[:MAY_INVOKE]->(:ActionType)` 직접 엣지를 도입하는 것이 최소 변경 경로이나, **지금은 도입하지 않는다**(추가 저작 부담이 M3의 원죄와 같아진다).

### 8.4 M4 — `authz.CheckDelegation`: HI16을 어떻게 지키는가

**이것이 이 문서에서 가장 깨지기 쉬운 지점이다.** `60 §21.4-1`은 판정을 두 곳에 두는 것을 명시적으로 기각했고 HI16은 그것을 린트로 지킨다.

이 문서는 **판정 함수의 개수를 늘리지 않는다.** 판정 함수는 이 문서 이전에도 이후에도 **둘**이며, 둘은 원래부터 서로 다른 질문에 답한다:

| 함수 | 질문 | 이 문서의 영향 |
|---|---|---|
| `authorize(principal, code)` (`60 §19.6`) | *"이 주체가 이 권한 코드를 갖는가"* | **무변경.** 어휘에 `actiontype.write` 1개 추가(AD3) |
| `authz.CheckDelegation` (`60 §21.1`) | *"이 변경을 이 행위자가 해도 되는가"* | **규칙 3개 추가**(DG6~DG8). 함수는 그대로 하나 |

그리고 **`gateFor`는 세 번째 함수가 아니다** — 판정이 아니라 파생이기 때문이다:

- 반환값이 **허가/거부가 아니라 "요구되는 게이트 등급"**이다.
- **인자에 주체가 없다**(§7.3). 순수하게 `:ActionType` + 리비전 + 오염 등급의 함수다.
- 그 요구를 실제로 집행하는 것은 기존 승인 파이프라인(`10 §8.2`)과 기존 `authorize`다.

**AC7이 이것을 린트로 고정한다**: `gateFor`의 시그니처에 `Party`/`principal`/`partyId` 타입이 나타나면 실패. 이것이 "두 번째 인가 엔진을 만들지 않았다"의 **기계 검증 가능한 형태**다 — HI17이 "인가 경로에 역할 이름 리터럴이 없다"를 검사하는 것과 같은 종류의 증거.

`60 §21.4-1`의 한계는 그대로 승계된다: DG6~DG8도 `osiris-api` 안에서 평가되므로 api 침해에는 무력하다. **core 쪽의 마지막 방벽**은 I14와 같은 성질의 것 — CA1(속성 화이트리스트)·CA3(레지스트리 존재)·AD6(집행 시점 재평가)이며, 이 셋은 **core 안에 있으므로 api 침해에도 살아남는다.** 특히 CA3와 §5.3의 어휘 상한은 api를 완전히 장악한 공격자조차 **새 코드를 실행시킬 수 없게** 한다.

### 8.5 M5 — genesis 스코프: 흡수하지 않는 이유 (AD12)

세 가지 독립적인 이유가 있고, 각각으로도 충분하다.

1. **부트스트랩 순환.** genesis 주체는 `currentOrgRevision == 0`인 구간, 즉 **그래프가 비어 있는 구간**의 존재다(`10 §8.5`). `:ActionType`은 그래프 노드이므로 그 구간에 존재하지 않는다. 그래프에 없는 것으로 그래프가 없는 구간의 인가를 정의할 수 없다.
2. **(c) 위반이 된다.** genesis 스코프를 데이터로 표현하면 *"데이터를 써서 특권 창을 여는"* 경로가 생긴다 — §5.3이 금지하는 바로 그 형태다. W1(*"특권이 '토큰을 가졌는가'가 아니라 '조직이 비어 있는가'에 조건부"*)의 논증은 스코프 목록이 **코드에 고정되어 있다는 사실**에 의존한다.
3. **`10 §8.5.1`이 이미 그 방향을 정했다.** 4차 정정은 genesis 스코프를 *"허용목록(allowlist)이어야 하며 새 엔드포인트가 추가될 때 기본이 거부"*로 못박고 `bin/lint-identity-authn.sh`의 `GEN` 검사로 강제했다. 그 성질은 코드 상수에서만 성립한다.

**따라서 M5는 흡수 대상이 아니라 영구히 별개인 기구다.** 다섯 중 하나가 남는 것은 설계의 미완성이 아니라 **경계가 정확히 그어졌다는 증거**다.

---

## 9. 불변식 (AC1~AC22)

`20 §12` / `60 §21.3` 형식. **린트** 열의 ✅ 는 정적 린트로 검사 가능함을 뜻한다 — `bin/lint-identity-authn.sh`(864줄)가 선례이며, 이 목록은 **`bin/lint-action-layer.sh`** 를 요구한다.

| # | 불변식 | 검사 방법 | 린트 | 등급 |
|---|---|---|---|---|
| **AC1** | `:ActionType.id`/`.code`는 유일하고, `:Permission.code`와 같은 점 구분 어휘를 쓴다 | Neo4j 제약(§4.7) + 형식 검사 | — | 오류 |
| **AC2** | `origin:'system'` 노드는 실행 바이너리의 액션 레지스트리에 같은 id가 존재한다. 없으면 부팅 시 `inert` 강등 + `security.actiontype.unknown` | 부팅 검사 | — | 오류 |
| **AC3** | `validationPredicates`/`effects`의 모든 원소가 각 레지스트리에 존재한다. 쓰기 시 거부, 부팅 시 `inert`. **fail-closed — 건너뛰지 않는다** | graph-write + 부팅 | 부분 | **치명** |
| **AC4** | `:ActionType`에 실행 가능한 표현(코드·스크립트·URL·템플릿·표현식)을 담는 속성이 **존재하지 않는다.** 화이트리스트 밖 속성은 거부(무시 아님) | 속성 화이트리스트 상수 검사 | ✅ | **치명** |
| **AC5** | `actiontype.write`는 `:AIAgent`에게 부여·위임될 수 없다 (DG6) | `CheckDelegation` 분기 존재 + 통합테스트 2건 | ✅ | **치명** |
| **AC6** | `HumanOnly` 집합은 닫힌 열거이며, 그 원소 중 grant-catalog에서 `ClaudeAllowedTools`/`MCPTools`/`EgressAllowlist` 중 하나라도 비어 있지 않은 코드가 있으면 실패 (P1-Q 상한) | 두 상수 목록 교차 검사 | ✅ | 오류 |
| **AC7** | `gateFor`의 시그니처에 `Party`/`principal`/`partyId`가 나타나지 않는다 — 액션 게이트 계산이 세 번째 인가 엔진이 아님의 증거 (HI16 보호) | 시그니처 정적 검사 | ✅ | **치명** |
| **AC8** | 조합 액션의 파생 속성은 집행 시점에 leaf에서 재계산되며, `derivedHash` 불일치 시 집행 거부 + `security.actiontype.drift` | 런타임 + 통합테스트 | — | **치명** |
| **AC9** | `COMPOSED_OF` 그래프에 사이클 없음, 깊이 ≤ 3, leaf 이펙트 ≤ 8 | graph-write | — | 오류 |
| **AC10** | `COMPOSED_OF` 엣지의 속성은 `ord` 하나뿐이다 (조합 언어에 조건이 없음의 형식적 표현, AD7) | 관계 속성 화이트리스트 | ✅ | **치명** |
| **AC11** | 게이트·오염·효과·가역성의 **완화** 변경은 `org.admin`만 가능하고, `security.actiontype.relaxed` + `org.admin` 전원 `:Notification{critical}` 발행에 성공해야 커밋된다 (DG8, fail-closed) | `CheckDelegation` + 통합테스트 | ✅ 부분 | **치명** |
| **AC12** | core는 집행 직전 `:ActionType`을 현재 리비전으로 다시 읽는다. `status != 'active'`이거나 재계산 게이트가 승인 등급보다 엄격하면 집행하지 않고 승인을 취소한다 (AD6) | 런타임 + 통합테스트 3건 | — | **치명** |
| **AC13** | 게이트 핸들러는 요청 바디의 taint 필드를 읽지 않는다. 게이트 판정에 쓰이는 오염 값은 core의 `taskId` 기록이다 (AD9) | 핸들러 정적 검사 | ✅ | **치명** |
| **AC14** | 오염 등급은 세션 내 단조 비감소이며, 낮추는 유일한 연산은 새 `sessionId` 시작이다 (AD10) | 통합테스트 | — | 오류 |
| **AC15** | `effectClass:'external_effect'` ⟹ `taintClass ≥ 'escalate'`. 그리고 `effectClass:'internal'` ⟹ `reversibility != 'irreversible'` | graph-write | — | 오류 |
| **AC16** | 오염 턴에서 생성된 `:Approval`은 `taintLevel`과 **1개 이상의 `TAINTED_BY` 엣지**를 갖는다. 없으면 승인 카드를 렌더하지 않는다(fail-closed) | graph-write + 콘솔 | — | **치명** |
| **AC17** | 승인 카드는 조합 액션의 **재계산된 leaf 이펙트 목록과 파라미터 실효값**을 항상 렌더한다. `label`만으로 렌더하지 않는다 (§6.5) | 콘솔 컴포넌트 검사 + 시각 회귀 | 부분 | 오류 |
| **AC18** | 실행 경로에 능력 키·스택 이름 리터럴과 `if <stack>Enabled` 류 분기가 없다. 가용성은 `requiresCapability` 해소로만 결정된다 (AD11) | 리터럴 검사 (HI17과 동형) | ✅ | 오류 |
| **AC19** | `:ActionType` 변경 트랜잭션은 `:OrgRevision`을 정확히 1 증가시킨다 (C8의 적용) | graph-write | — | 오류 |
| **AC20** | `:Permission.gate`/`approverRole`/`limitJson`은 `:ActionType`에서 파생된 미러이며 직접 쓰기 금지. 부팅 시 불일치면 **부팅 실패** (AD13, I9와 같은 등급) | 부팅 검사 + graph-write 거부 | 부분 | 오류 |
| **AC21** | `requiresCapability`는 **모든 키가 정확히 `available`일 때만** 충족된다 — `degraded`도 `unavailable`도 충족하지 않는다(AD14). 해소는 `81` C-3의 `GET /v1/capabilities[/{key}]`로 **키마다 개별** 수행하며, "스택이 healthy면 그 스택의 키는 전부 `available`"을 가정하는 코드가 없다(`81` CS9). 능력 상태의 사본을 core 밖에 두지 않는다(`81` CS7). 키는 `81` C-1의 정규식과 C-2의 정적 레지스트리를 만족한다(CA8) | graph-write(정의 시점) + 런타임 + CI(전 키를 `degraded`로 고정한 상태에서 해당 액션이 전부 409인지 / 한 스택의 키를 서로 다른 상태로 고정한 상태에서 통과 여부가 키별로 갈리는지) | 부분 | **치명** |
| **AC22** | 능력 `degraded`로 인한 **지연 큐는 `osiris-core`만 소유**하고, ① 이미 승인된 집행 요청에만 적재되며 ② 항목은 `boundRevision`·`taintLevel`·`grantedGateClass`·파라미터 실효값·`derivedHash`를 **동결**하고 ③ 재시도 1회마다 AD6 재평가를 전부 다시 통과하며 ④ 마감 24시간(`10 §8.2` 승인 상한과 동일)을 넘기면 재승인을 요구한다. `unavailable`에서는 큐잉하지 않는다 (§5.4.1) | 런타임 + 통합테스트 4건 | — | **치명** |

**린트 가능 항목 요약** (`bin/lint-action-layer.sh`가 검사할 것): AC4 · AC5 · AC6 · AC7 · AC10 · AC11(부분) · AC13 · AC18, 그리고 AC3·AC17·AC20·AC21의 정적 부분(AC21은 능력 상태 조회부가 core 밖에 없는지 = `81` CS7의 린트와 같은 대상). 나머지는 graph-write의 런타임 검증과 통합 테스트가 담당한다.

**치명(치명적 결함) 등급의 의미**: 위반 시 그 기능을 끄는 것이 아니라 **부팅을 거부**하거나 **집행을 거부**한다. `10 §8.5`의 fail-closed 원칙과 같은 등급이다.

---

## 10. 미해결 / 갭

| # | 항목 | 성질 | 누가/언제 |
|---|---|---|---|
| ~~G-A1~~ | **해소됨 `[2026-08-12]`** — `81` C-1 레지스트리에 `stk.scm.credential.rotate`가 추가되어 §4.6의 `secret.rotate` 행이 채워졌다. 공급 주체는 SCM 스택 | ✅ 종결 | — |
| G-A2 | **오염 세탁(파일 경유)이 닫히지 않는다.** 완화 경로는 있으나(core가 워크스페이스 스냅샷 해시를 소유) 설계하지 않았다(§7.7-1) | `[갭]` — **가장 큰 잔여 위험** | 별도 결정 필요 |
| G-A3 | **egress 응답 본문의 오염 부여**: squid 감사 레코드가 `employeeId`+시각으로 게이트 시점 pull 질의를 만족시키는지 실측하지 못함(§7.7-5) | `[미검증]` | 구현 착수 전 실측 |
| G-A4 | **메일 스택의 MTA 구현체가 미정이다.** 설계·필드 계약·신뢰 스택 등록 절차는 `81 §4`(특히 §4.5의 필수 출처 메타데이터)가 **확정했으므로 더 이상 갭이 아니다**. 남은 것은 구현체 선택뿐이며(`81` Q4가 열어 둠), 선택된 MTA가 `81 §4.5`의 필드 전부를 산출하는지는 실측해야 한다(§7.5). `50-integration-adapters.md`에 메일 행이 없는 것은 이제 갭이 아니라 **정합**이다 — 인바운드 상류는 제3자 어댑터가 아니라 능력 스택이다 | `[미검증]` | `81` Q4 확정 후 실측 |
| G-A5 | **게이트 없는 오염 액션의 영구 이력이 없다.** `:Approval`이 생기지 않으므로 90일 후 소실. `:ActionInvocation` 노드 도입이 최소 경로이나 P6와 충돌(§7.6) | `[갭]` — 의도적 미도입 | 감사 요구가 실제로 생기면 |
| G-A6 | **이펙트 이름의 안정성 정책 미정.** 리네임 = 파괴적 변경. `deprecated` 별칭 유지 기간이 정해지지 않음(§5.3.3) | `[갭]` | 릴리스 정책 |
| G-A7 | **이펙트의 인자 도메인 제한이 이펙트 구현의 책임으로 위임되어 있고, 그 심사 절차가 문서화되지 않았다**(§5.3.3) | `[갭]` | 이펙트 등록 심사 체크리스트 필요 |
| G-A8 | **조합 편집 UI 사양 미정.** 액션 편집기의 화면·상호작용·미리보기는 `40-console-ia.md`/`41-console-design-system.md`가 소유. AC17(leaf 렌더)이 그 문서에 대한 요구로 인계됨 | `[갭]` | 콘솔 워커 |
| G-A9 | **`COMPOSED_OF` 깊이 3 / leaf 8의 근거가 임의값이다.** 실제 조합 패턴을 관측한 뒤 조정해야 하며, 지금은 "명백히 충분하고 명백히 유한"만 만족한다 | `[미검증]` | 카탈로그가 20행을 넘을 때 재검토 |
| G-A10 | **DG6~DG8·AC 계열의 린트 미구현.** `bin/lint-action-layer.sh`가 존재하지 않는다 | `[갭]` | 구현 착수 시 |
| ~~G-A11~~ | **해소됨 `[2026-08-13]`** — `core/internal/graphdb/actiontype_permission_mirror.go`의 `CheckActionTypePermissionMirror`가 이제 "엣지가 있으면 무조건 부팅 거부"가 아니라 `:Permission.gate` = min(그 코드를 요구하는 모든 `:ActionType.gateClass`)를 **실제로 계산·대조**한다(불일치 시 부팅 실패는 유지, I9와 같은 등급). `orchestrator/internal/catalog/catalog.go`의 `Grant.Gate`를 읽는 코드가 전무하고 `:Permission.gate`를 인가 판정에 읽는 코드도 전무함을 전수 확인했으며(`bin/lint-action-layer.sh` 신설 AC20 섹션이 이 두 가지를 린트로 고정, 인위 결함 3종 주입으로 검증), `git.commit` 행에 `REQUIRES_PERMISSION`(§4.6)을 실제로 채워 이 경로를 실증했다. **남은 한 가지 판단**: `:ActionType.gateClass`는 3값(`none`/`approval.team_lead`/`approval.admin`)이지만 `20-ontology-schema.md`의 `:Permission.gate`는 2값(`none`/`approval`)이라 min을 그대로 대입할 수 없다 — 이 문서와 `20`이 그 축소 규칙을 명시한 적이 없어, min-rank가 0이면 `none`, 아니면 `approval`로 접는 규칙을 §8.1의 논증문("이 도구를 게이트 경로로 노출할 것인가 아닌가"라는 이분법)에 근거해 구현했다(코드 주석에 명시). `approverRole`/`limitJson`은 §8.1이 파생 공식을 주지 않으므로 **의도적으로 대조하지 않는다** — 그 갭은 해소되지 않았고 이 항목 종결과 별개로 남아 있다(대조 안 함이 그 자체로 미검증이라는 뜻은 아니다: 애초에 규칙이 없다) | ✅ 종결(단, approverRole/limitJson 미검증은 별개로 잔존) | — |
| G-A12 | **`:WorkItem`↔액션 다리의 과승인.** `MAY_INVOKE` 직접 엣지를 도입하지 않기로 했으나, 카탈로그가 커지면 과승인 비율이 실무적으로 쓸 수 없는 수준이 될 수 있다(§8.3). **`[2026-08-13] 재확인`**: `core/internal/assign/evaluate.go`의 `GatePermissionFor`(20 §8.4의 `gatePermissionFor(task)`, line 688)가 항상 `("", false)`만 반환하는 근본 원인은 **G-A11이 아니라 이 항목**이다 — G-A11은 이제 해소되었지만(위 참조) `GatePermissionFor`는 여전히 막혀 있다. 이유: (1) `Task`(20 §3.4의 `:WorkItem`)에 `ActionTypeID` 같은 필드가 없다, (2) §8.3의 기존 다리는 역량 경유(`:WorkItem`→`:Capability`→`:Permission`←`:ActionType`)라 특정 `:ActionType` 하나를 지목하지 못하고 §8.3 스스로 그 결과를 과승인으로 인정한다, (3) `actionClass`는 2값(`internal`/`external_effect`)뿐인데 `:ActionType`은 이미 12개 이상(§4.6)이라 `actionClass`만으로 역산할 카디널리티가 성립하지 않는다. 따라서 G-A11이 완전히 해소되어도 `GatePermissionFor`는 여전히 fail-closed로 남으며, 그 함수를 풀려면 이 G-A12(`:WorkItem`→`:ActionType` 직접 다리)가 먼저 해소되어야 한다. `evaluate.go`의 `GatePermissionFor` 주석도 이 재귀속에 맞춰 정정했다 | `[갭]` — 의도적 미도입, 이제 `GatePermissionFor`의 명시적 차단 사유 | 관측 후 / `GatePermissionFor` 재작성 시 |
| G-A13 | **`semi_trusted` 크로스에이전트 오염 승계의 비용 미측정.** 한 에이전트의 오염이 `master.inbox` 경유로 조직 전체에 번져 모든 `external_effect`가 승인 대기에 걸리는 "오염 폭풍"이 가능하다. 감쇠를 두지 않기로 한 것은 보안 판단이며, 운영상 감내 가능한지는 미검증 | `[미검증]` | 실 운영 관측 |
| G-A14 | **`degraded` 지연 큐의 마감 24시간이 실제 스택 회복 시간과 맞는지 미검증.** 마감을 `10 §8.2`의 승인 상한과 같은 값으로 맞춘 것은 "창을 넓히지 않는다"는 설계 논증(§5.4.1)이지 관측이 아니다. 릴레이 레이트리밋·DNS 전파·업스트림 5xx의 회복이 24시간을 넘게 걸리면 큐가 재승인 요구로 반복 실패하고, 그것은 fail-closed가 아니라 그냥 사용 불가다 | `[미검증]` | 실 운영 관측 |
| G-A15 | **`81` CS3(키가 거짓말하지 않는다)의 자동 검사가 없다**(`81` Q9). 이 문서의 AD11/AD14는 전부 "`state`가 실제 능력과 일치한다"를 전제로 서 있으므로, 스택이 폴백을 `available`로 보고하면 이 층의 fail-closed가 통째로 무력해진다. **이 문서가 만든 갭이 아니라 승계한 갭**이며, 이 문서는 그 위에 무엇도 얹지 않는다 | `[갭]` — 승계 | `81` Q9 (워커 D2) |
| G-A16 `[2026-08-13 신설]` | **AD15(가역성 축 게이트 분기·사후 유예취소)의 큐 저장 위치·유예 시간·취소 UI·유예 기간 중 재평가 루프가 설계되지 않았다.** §5.4.1의 능력 열화 지연 큐와는 별개 개념이며, 대응 불변식(AC23 상당)도 §9에 **의도적으로 등재하지 않았다**(등재 시 `bin/lint-action-layer.sh`가 검사하지 않는 항목이 생기므로) | `[갭]` — 의도적 미등재 | 구현 착수 전 별도 설계. 반대 액션(counter-action) 카탈로그가 먼저 채워져야 선행조건이 성립한다(§11 인계 참조) |

---

## 11. 다음 워커를 위한 인계

- **D2(`81-capability-stacks.md`) — 요청이 아니라 정합 기록.** 이 문서가 D2에게 확정을 부탁했던 세 항목은 전부 `81`에서 결정되었고, 이 문서는 그 결정에 맞춰 조정을 끝냈다:
  - ① **네임스페이스**: 필드가 담는 것은 스택 id가 아니라 **능력 키**다 — `81 §2.3` C-1(`^stk\.[a-z0-9-]+(\.[a-z0-9-]+){1,2}$`) + C-2(정적·닫힌 레지스트리), `cap.*`와 배타(`81` CS1). C-11이 `stackId` 안정성을 약속하지 않으므로 스택 키잉은 애초에 불가다. → §4.2·§4.6 반영.
  - ② **신선도**: 상태는 **3개**(`81` C-4)이고 `observedAt`은 필수, core 캐시 상한 15초, **소비 측 자체 캐시 금지**(C-7 / `81` CS7). 이 문서가 가정했던 "stale = absent(2상태)"는 **폐기**했다. `degraded`의 처리는 이 문서가 새로 결정했다(AD14, §5.4.1) — `81`이 정의한 "큐잉 가능한 유일한 상태"라는 성질은 **가용성 게이트 뒤의 지연 큐**로 보존된다.
  - ③ **거부·`disabledReason` 형식**: `81 §2.3` C-9의 **409 `CAPABILITY_UNAVAILABLE{key,state,reason}`**(403 `PERMISSION_DENIED`와 구분 필수)을 그대로 채택하고, `disabledReason`은 그 기계 판독 필드에서 파생한 `"<STATE>:<key>:<reason>"`이다. 이전의 `capability_absent:mail`은 폐기했다 — `81 §4.1`이 메일을 세 키로 쪼갰으므로 `mail` 키는 존재하지 않는다.
  - ④ **`REQUIRES_STACK`은 철회했다.** `81`은 `:CapabilityStack` 그래프 노드를 정의하지 않았다(정의한 것은 `:CapabilityStackDecl` 선언과 k8s CR이며, 상태 해석은 C-3의 `GET /v1/capabilities[/{key}]` 하나뿐이다). 근거는 §4.3.
  - **D2에게 남은 것은 없다** `[2026-08-12]` — G-A1(자격증명 회전 키)은 `81` C-1에 `stk.scm.credential.rotate`가 추가되어 종결됐다. 같은 정리에서 `81`의 사서함 키 예시가 자기 정규식을 위반하던 것(`perParty` → `per-party`)도 두 문서 전부에서 정정됐다.
- **콘솔 워커(`40`/`41`)**: AC17(leaf 이펙트 + 파라미터 실효값 렌더), §7.7-2의 승인 카드 3규칙(오염 배지 접기 불가 / LLM 서술 기본 접힘 / 구조화 필드 우선), 액션 편집기의 조합 UI가 인계된다.
- **`60` 소유 워커**: DG6~DG8이 `authz.CheckDelegation`에 추가되고 `HumanOnly` 집합이 4개가 된다. §21.3의 불변식 표에 AC5/AC6/AC7을 상호참조로 걸어야 하며, HI16의 린트 대상에 `gateFor` 시그니처 검사(AC7)를 추가하는 것이 자연스럽다.
- **구현 워커**: 착수 순서 권고 — ① 이펙트/술어 레지스트리 골격 + AC3의 fail-closed, ② `:ActionType` 스키마 + graph-write 화이트리스트(AC4/CA1), ③ `git.commit` **한 행**을 데이터로 옮겨 `10 §8.2` 파이프라인이 일반 경로로 도는 것을 실증, ④ 그 뒤에 나머지 행. ③이 통과하기 전에 조합(§6)이나 오염(§7)에 착수하면 검증 없이 층이 쌓인다.
  **`[2026-08-13 신설]` ④ 나머지 행을 채울 때의 필수 체크 항목 — "반대 액션(counter-action) 존재 여부".** 자매 프로젝트 Blueurban AaaM 부록 A(39개 액션 분류, 취소/회수·재촉·보류/해제 범주 포함)와 대조하면 **OSiris §4.6의 12개 행에는 반대 액션 범주가 전무하다**(`pr.create`/`pr.merge`는 있고 `pr.close`가 없다). 카탈로그를 채우는 워커는 액션 하나를 추가할 때마다 **"이 액션에 대응하는 취소·철회 액션이 있는가"를 반드시 확인**하고, 없으면 있어야 하는지(있어야 한다면 같은 행에서 함께 추가) 또는 왜 없어도 되는지를 명시적으로 남긴다. 이것은 장식이 아니다 — AD15(§5.4.2)의 사후 유예취소는 취소 경로가 존재해야만 성립하므로, 반대 액션의 부재는 그 자체로 게이트 전략의 선택지를 줄인다.
  **방법론도 함께 인계한다**: Blueurban 부록 A는 액션마다 `reversible`/`gate`/`requires`/근거를 **표로 먼저 완성한 뒤** 구현에 들어갔다 — 구현 세션이 그 자리에서 다시 판단하지 않도록 미리 결정을 표로 굳히는 방식이다. §4.6을 확장하는 워커는 같은 순서(표 먼저, 구현은 그 뒤)를 따르는 것을 권고한다.
