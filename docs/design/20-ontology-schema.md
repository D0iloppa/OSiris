# OSiris 온톨로지 스키마 (Ontology Schema)

> 작성일: 2026-07-26
> 대상: Neo4j 5.x Community Edition, property graph (Cypher)
> 입력: `00-research-prior-art.md` Q6(W3C ORG / FOAF / PROV-O / SKOS), `10-core-architecture.md`(진실 소유권 경계), `40-console-ia.md`(설명가능성 요구)
>
> **이 문서는 배포 단계(1단계 docker-compose / 2단계 k3s)와 무관하다.** 스키마와 배정 알고리즘은 두 단계를 가로지르는 **불변 자산**이며, 런타임이 무엇이든 동일하게 유지된다. 런타임 관측값을 담는 캐시 노드(§3.7)만 배포 단계에 따라 채워지는 값이 다를 뿐, 구조는 같다.
>
> **표기 규칙**: 표기 없는 서술 = 확정 결정. `[미검증]` = 확인 필요. 라벨은 `:PascalCase`, 관계는 `:UPPER_SNAKE`, 속성은 `camelCase`.

---

## 1. 설계 원칙 (이 스키마가 지키는 것)

| # | 원칙 | 귀결 |
|---|---|---|
| P1 | **사람과 AI는 같은 상위 개념이다** | 모든 조직 구성원은 `:Party` 라벨을 **공동으로** 갖는다. 배정·멤버십·권한·업무 관계는 전부 `:Party`를 향하고, `:Person`/`:AIAgent` 구분은 **필터일 뿐 구조가 아니다** |
| P2 | **의도만 저장한다** | 관측값(컨테이너 상태, 노드 용량)은 캐시이며 `observedAt` 없이 존재할 수 없다(`10-core-architecture.md` §6.1) |
| P3 | **역량(capability)과 권한(permission)을 분리한다** | "할 줄 안다"와 "해도 된다"는 다른 것이다. 섞으면 배정과 인가가 동시에 망가진다 |
| P4 | **과거는 재현되어야 한다** | 삭제하지 않고 `validTo`를 채운다. 배정 근거는 **비정규화 스냅샷**으로 얼린다 |
| P5 | **물리 업무와 디지털 업무는 같은 구조** | `:WorkItem` 하나. 차이는 `executionMode`와 `:CompletionCriterion`의 종류뿐 |
| P6 | **고빈도 시계열은 그래프에 넣지 않는다** | 하트비트·로그·턴별 비용은 NATS/JetStream. 그래프는 관계 질의를 위한 것 |
| P7 | **쓰기는 단일 관문** | 모든 변경은 `osiris-core`의 graph-write API를 지나며 `:OrgRevision`을 증가시킨다. LLM은 임의 Cypher를 실행하지 않는다 |

---

## 2. RDF/OWL → property graph 투영 규칙 (조사 쟁점 6의 직답)

W3C ORG / FOAF / PROV-O / SKOS는 RDF/OWL 세계의 어휘다. OSiris는 Neo4j property graph를 쓰므로 **SPARQL 호환은 명시적으로 포기하고**, 개념만 투영한다. 대신 모든 노드에 `iri` 속성을 보존해 훗날 RDF export가 가능하도록 문을 열어둔다.

### 2.1 투영 규칙표

| RDF/OWL 구성 | property graph 투영 | 근거 |
|---|---|---|
| 구체 클래스 (`org:Organization`, `foaf:Person`) | **노드 라벨** | 1:1 대응이 자연스럽다 |
| `rdfs:subClassOf` 체인 | **상위 클래스를 공동 라벨로 머티리얼라이즈** (`:Person:Party`) | Neo4j에는 추론기가 없다. 상위 개념 질의(`MATCH (p:Party)`)를 인덱스로 처리하려면 라벨을 미리 붙여야 한다 |
| object property (1:1 / 1:N) | **관계 타입** | `org:headOf` → `[:HEADS]` |
| datatype property | **노드 속성** | `foaf:name` → `name` |
| **n-ary / reified 클래스** (`org:Membership`, `org:Post`, `prov:Activity`, `prov:Association`) | **노드로 승격** (관계 아님) | 시간범위·다중 참여자·부가 속성을 담아야 한다. 관계 속성으로는 3자 관계를 표현할 수 없다 |
| `org:Role` | **노드** (관계 속성 아님) — **쟁점 6의 핵심 질문에 대한 답** | 역할은 재사용되고, 역량 요구·권한 프로파일·리소스 프로파일이 붙는 1급 개체다. 관계 속성으로 두면 "이 직무에 필요한 역량"을 표현할 곳이 없어진다 |
| 시간 유효성 (`org:memberDuring`) | 노드/관계의 `validFrom` / `validTo`. **삭제 대신 `validTo` 설정** | P4 |
| 시스템 시간(재현용) | `createdAtRevision` / `closedAtRevision` (`:OrgRevision` 번호) | 타임스탬프보다 정확한 재현. 사실상 bitemporal-lite |
| SKOS `skos:broader` | `(:Capability)-[:BROADER]->(:Capability)` | 역량 taxonomy |
| IRI | `iri` 속성으로 보존 | 향후 RDF export 여지 |
| `owl:sameAs`, 추론 규칙 | **투영하지 않음** | 추론기가 없다. 필요한 것은 전부 머티리얼라이즈한다 |

### 2.2 표준별 대응

| 표준 | 개념 | OSiris |
|---|---|---|
| FOAF | `foaf:Agent` (사람·조직·**소프트웨어** 공통 상위) | **`:Party`** ← P1의 표준적 근거 |
| FOAF | `foaf:Person` | `:Person:Party` |
| FOAF | `foaf:Agent`(소프트웨어) | `:AIAgent:Party` — 표준에 직접 선례가 없는 확장 지점 |
| W3C ORG | `org:FormalOrganization` | `:Organization:Party` |
| W3C ORG | `org:OrganizationalUnit` | `:Team:Party` |
| W3C ORG | `org:Membership` (reified) | `:Membership` 노드 (3자: Party × Team × Role) |
| W3C ORG | `org:Role` | `:Role` 노드 |
| W3C ORG | `org:headOf` | `:Membership.isHead = true` |
| W3C ORG | `org:subOrganizationOf` | `[:PART_OF]` |
| W3C ORG | `org:ChangeEvent` | `:OrgRevision` (전역 단조 증가 체인으로 단순화) |
| PROV-O | `prov:Activity` | `:WorkItem`, `:AssignmentDecision` |
| PROV-O | `prov:Entity` | `:Artifact`, `:Evidence` |
| PROV-O | `prov:Agent` / `prov:SoftwareAgent` | `:Party` / `:AIAgent` |
| PROV-O | `prov:wasAssociatedWith` | `[:PERFORMED_BY]` |
| PROV-O | `prov:wasGeneratedBy` (역) | `[:PRODUCES]` |
| PROV-O | `prov:used` | `[:USED]` |
| PROV-O | `prov:wasDerivedFrom` | `[:DERIVED_FROM]` |
| SKOS | `skos:Concept` + `broader/narrower` | `:Capability` + `[:BROADER]` |
| schema.org | — | **채택하지 않음** (SEO용 얕은 스키마) |

**정직한 갭 (조사가 지적한 것)**: "AI 에이전트를 조직 구성원으로 모델링"하는 W3C 공식 선례는 없다. FOAF가 `Agent`의 예로 소프트웨어 봇을 들고 ORG가 `org:memberOf`의 주체를 `foaf:Agent`로 잡은 것이 근거의 전부다. `:AIAgent`가 `:Party`를 공동 라벨로 갖고 `:Membership`에 참여하는 것은 **OSiris의 확장**이며 표준의 인용이 아니다. 표준을 어기지는 않지만 표준이 보장해 주지도 않는다.

**PROV-O가 특히 잘 맞는 지점**: 배정 결정(`:AssignmentDecision`) 자체가 하나의 `prov:Activity`이고, 그것이 `prov:used`한 것이 정책과 조직 스냅샷이며, `prov:generated`한 것이 `:Assignment`다. 즉 **설명가능성 요구가 PROV-O 구조와 그대로 겹친다.**

---

## 3. 노드 라벨 전체 목록

### 3.1 조직 구성원 (Party 계층)

#### `:Party` — 추상 상위. **단독으로 존재하지 않으며 항상 공동 라벨로만 붙는다.**

| 속성 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `id` | string | ✔ | 전역 고유. `emp-`, `team-`, `org-` 접두 |
| `name` | string | ✔ | 표시명 |
| `iri` | string | | 향후 RDF export용 |
| `createdAtRevision` | int | ✔ | |
| `closedAtRevision` | int | | 퇴사/폐지 시 설정. **노드는 삭제하지 않는다** |

#### `:Person:Party` — 사람 직원

| 속성 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `email` | string | ✔ | 콘솔 로그인 식별자 |
| `timezone` | string | ✔ | 마감 계산·근무시간 판정 |
| `workingHours` | string | | `"09:00-18:00"` |
| `availability` | enum | ✔ | `available` / `busy` / `off` / `leave` |
| `leaveFrom`, `leaveTo` | date | | 휴가 구간(배정 하드 필터에 사용) |
| `notifyChannels` | string[] | ✔ | `["console","email:...","telegram:..."]` — 에스컬레이션 사다리가 순서대로 사용 |
| `maxTaskPoints` | int | ✔ | 동시 수용 가능한 업무 포인트. 기본 3 |

#### `:AIAgent:Party` — AI 에이전트

| 속성 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `runtimeClass` | string | ✔ | `:RoleResourceProfile` 참조 키 |
| `modelAlias` | enum | ✔ | `osiris/planner` \| `osiris/worker` \| `osiris/cheap` — **프로바이더 모델 ID를 절대 저장하지 않는다** |
| `image` | string | ✔ | 컨테이너 이미지(핀 고정) |
| `maxTaskPoints` | int | ✔ | 기본 3 |
| `drainSeconds` | int | ✔ | 기본 600 |
| `desiredState` | enum | ✔ | `Running` / `Suspended` — **의도**이므로 여기 저장. 실제 상태는 `:AgentRuntime`(캐시) |
| `isMaster` | bool | | Agent Master 여부. 전체 조직에 1개 |

#### `:Organization:Party` / `:Team:Party`

| 라벨 | 속성 |
|---|---|
| `:Organization` | `id`, `name`, `legalName`, `timezone` |
| `:Team` | `id`, `name`, `slug`(= NATS/네임스페이스 키), `channelSubject`, `monthlyBudgetUsd` |

`:Team`에도 `:Party`를 붙이는 이유: ORG 표준에서 조직 단위도 `foaf:Agent`이며, 실제로 "팀 앞으로 배정"(담당자 미정 상태의 업무)이 필요하다. 다만 **`:Team`에 직접 배정된 태스크는 배정 미완료로 간주**하고 assignment-engine이 개인으로 내려보낸다.

### 3.2 직무 · 멤버십

#### `:Role` — 직무 (org:Role)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id`, `title` | string | `role-backend-dev`, "백엔드 개발자" |
| **`scope`** | enum | **`job`(기본) / `org`** — `[2026-07-27 신설, 60-identity-authn.md H29/H30]`. `job`은 팀 안의 직무(기존 의미 전부), `org`는 **조직 전체에 대한 역할**(관리자·매니저·구성원). `scope:'org'`인 `:Role`은 `IN_ORG` 멤버십에서만 쓰이고, 역량을 요구하지 않으며(I17), 배정 알고리즘의 대상이 아니다(§8.3). **속성을 명시하지 않은 기존 `:Role`은 전부 `job`으로 읽는다** |
| `level` | int | 1~5. tie-break와 승인 권한 판정 보조 |
| `defaultTeamId` | string | `scope:'org'`이면 null (I17) |
| `isLead` | bool | 팀장 직무 여부. `scope:'org'`이면 null (I17) |
| `responsibilitiesSummary` | string | 직무 책임 1~3문장. 프롬프트 템플릿의 `{{role.responsibilitiesSummary}}`가 참조 |
| **`promptTemplateMd`** | string(Markdown) | **이 직무의 페르소나/시스템 프롬프트 원문 템플릿.** 렌더되면 에이전트 워크스페이스의 `CLAUDE.md`가 된다. §3.2.1 |
| `promptTemplateVersion` | int | 원문이 바뀔 때마다 +1 |
| `promptAuthoredAtRevision` | int | 원문을 저술/개정한 시점의 `:OrgRevision` |

#### 3.2.1 `promptTemplateMd` — 저작·합성·불변성

**LLM이 글을 쓰는 지점은 조직 전체에서 정확히 여기 하나다.** `:Role`이 최초 생성될 때 온보딩의 LLM(`bootstrap-master` 또는 `agent-master`)이 이 원문을 **1회 저술**하고, **이후 모든 렌더링은 결정적 문자열 치환뿐**이다(`30-onboarding-provisioning.md` §4.1). 이것이 compute-over-inference 원칙(`10-core-architecture.md` A4)이 프롬프트 영역에서 적용된 형태다 — 에이전트를 띄울 때마다 LLM에게 "너의 역할 설명을 써줘"라고 묻지 않는다.

**치환 가능한 플레이스홀더** (이 목록 밖의 변수는 렌더러가 거부한다 — 템플릿이 임의 코드가 되지 않도록):

| 플레이스홀더 | 출처 |
|---|---|
| `{{role.title}}`, `{{role.responsibilitiesSummary}}`, `{{role.level}}` | 이 `:Role` |
| `{{team.name}}`, `{{team.slug}}` | 유효 `:Membership`이 가리키는 `:Team` |
| `{{capabilities}}` | `(:Party)-[:HAS_CAPABILITY]->` 조회 결과 (§10 Q10과 동일 질의) |
| `{{effectivePermissions}}` | §7.1의 `effective(party)` 계산 결과 |
| `{{isMaster}}`, `{{isLead}}` | `:AIAgent.isMaster`, `:Membership.isHead` |

**직무 템플릿과 개인차의 합성 방식**: 원문은 `:Role`에만 있고, 개인차는 **치환 값으로만** 들어간다. 같은 `:Role`을 가진 두 `:AIAgent`는 **템플릿이 같고 렌더 결과가 다르다**(개인의 역량 레벨·추가 권한이 다르므로).

> **개인별 프롬프트 원문 필드(`:AIAgent.promptOverrideMd` 류)는 두지 않는다.** 두면 (a) LLM 저작 지점이 직무 수에서 개인 수로 늘어 compute-over-inference 경계가 흐려지고, (b) 같은 직무의 두 에이전트가 설명 불가능하게 달라져 "직무는 재사용, 개인은 채움"이라는 조직 모델이 무너진다. 개인에게 다른 지시가 필요하다면 그것은 **다른 직무**라는 신호다.

**불변성 / 리비전이 바뀔 때**: `promptTemplateMd`는 **불변이 아니다**(직무 설명은 개정된다). 개정 시 `promptTemplateVersion`을 올리고 `:OrgRevision`을 증가시킨다. **이전 원문은 그래프에 보존하지 않는다** — 대신 렌더 결과가 바뀔 때마다 supervisor가 렌더된 프롬프트 전문을 `ops.transcript.<employeeId>`에 `kind: "system_prompt"` 레코드로 1회 발행하며, 이는 `TRANSCRIPT` 스트림에 30일 보존된다(`10-core-architecture.md` §7.2). 즉 **"이 에이전트가 무슨 지시를 받고 있었는가"는 30일간 추적 가능**하고, 그 이상은 추적하지 않는다.
> *이 선택의 근거*: 프롬프트 이력을 위해 새 노드 라벨(`:PromptRevision`)을 도입하는 대신 이미 있는 인프라를 재사용한다(Simplicity First). **한계는 정직하게 30일**이며, `10-core-architecture.md` §12.1의 트랜스크립트 보존 한계와 동일한 제약이다. *재검토 트리거*: 프롬프트 개정 이력이 감사 대상으로 요구되면 그때 `:PromptRevision` 노드를 도입한다.

**`SpecHash` 포함 여부 — 포함하지 않는다.** `promptTemplateMd`(및 그 렌더 결과)는 `ConfigHash`에만 들어가며, 변경 시 컨테이너 재생성이 아니라 **무중단 `ReloadConfig`**를 유발한다(`10-core-architecture.md` §6.2, A12). 합쳤다면 역할 설명문 한 줄 수정이 그 직무의 전 에이전트를 660초씩 드레인·재생성시켰을 것이다. 진행 중 턴은 건드리지 않고 **다음 태스크부터** 새 프롬프트가 적용된다.

#### `:Membership` — reified 3자 관계 (org:Membership)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id` | string | |
| `validFrom`, `validTo` | date | `validTo IS NULL` = 현재 유효 |
| `createdAtRevision`, `closedAtRevision` | int | **재현용 시스템 시간** |
| `isHead` | bool | 이 멤버십이 팀장직인가 (org:headOf) |
| `allocationPct` | int | 겸직 시 배분(기본 100) |

관계: `(:Party)-[:HAS_MEMBERSHIP]->(:Membership)-[:IN_TEAM]->(:Team)`, `(:Membership)-[:AS_ROLE]->(:Role)`

> **왜 `(:Party)-[:MEMBER_OF]->(:Team)` 지름길을 두지 않는가**: 비정규화는 어긋남의 원천이다. 조직 규모가 수백 노드이므로 3홉 질의의 비용이 무시할 만하고, 시간범위·겸직·팀장 여부를 담을 곳이 필요하다. **지름길 관계를 만들지 않는다**를 규칙으로 유지한다.

##### `[2026-07-27 신설]` 조직 직속 멤버십 — `IN_ORG`

`:Membership`은 **두 가지 소속**을 표현한다. 도착점만 다르고 노드는 같다:

```
   팀 소속  (:Person|:AIAgent)-[:HAS_MEMBERSHIP]->(:Membership)-[:IN_TEAM]->(:Team)
                                                  (:Membership)-[:AS_ROLE]->(:Role{scope:'job'})

   조직 소속 (:Person)        -[:HAS_MEMBERSHIP]->(:Membership)-[:IN_ORG]->(:Organization)
                                                  (:Membership)-[:AS_ROLE]->(:Role{scope:'org'})
```

**왜 `IN_TEAM`의 도착점을 `:Organization`까지 넓히지 않고 새 관계 타입을 두는가**: `IN_TEAM`을 넓히면 그 관계를 쓰는 **기존 쿼리 전부의 의미가 조용히 바뀐다** — 특히 §8.3의 배정 후보 집합과 I1/C4(팀마다 `isHead` 1개)의 해석이 흔들린다. 별도 타입을 두면 두 불변식이 **문자 그대로 무변경**으로 유지되고, "이 멤버십이 팀에 관한 것인가 조직에 관한 것인가"가 쿼리에서 명시적으로 드러난다.

**왜 `HAS_PROFILE{kind:'additive'}`로 권한만 주지 않고 멤버십을 쓰는가**: `HAS_PROFILE` 엣지에는 `validFrom`/`validTo`/`createdAtRevision`/`closedAtRevision`이 없다. 회수가 곧 엣지 삭제이므로 **원칙 P4(삭제하지 않는다)를 어기고**, Q8(리비전 N 기준 조직 재현)이 **과거의 권한 배치를 재현하지 못한다.** `:Membership`은 그 넷을 이미 갖고 있으므로 "2026-07-01부터 09-30까지 매니저였다"가 영구히 남는다 — reify한 이유 그대로다.

**제약**(상세 근거는 `60-identity-authn.md` §20.2):
- `IN_ORG` 멤버십의 `:Party`는 **`:Person`만**(I15/C9) — `:AIAgent`가 가지면 I2("유효 멤버십 정확히 1개")가 깨지고, 조직에 대한 위임 결정은 사람이 해야 한다.
- 한 `:Person`에 `validTo IS NULL`인 `IN_ORG` 멤버십은 **최대 1개**(I16).
- `IN_ORG` 멤버십은 **배정 후보 집합에 들어가지 않는다**(§8.3) — 권한 파생(§7.1)에만 참여한다.
- 생성·종료는 조직 의도 변경이므로 `:OrgRevision`을 +1한다(C8).

### 3.3 역량 (Capability)

#### `:Capability` — SKOS Concept 투영

| 속성 | 타입 | 설명 |
|---|---|---|
| `id`, `code` | string | `cap.code.python` — **코드는 점 구분 계층 경로** |
| `label` | string | "Python" |
| `kind` | enum | `technical` / `domain` / `physical` / `soft` |
| `catalogVersion` | string | 시스템 카탈로그 버전. 사용자가 임의 생성 불가 |
| `iri` | string | |

`(:Capability)-[:BROADER]->(:Capability)` 로 taxonomy를 이룬다. **방향은 구체 → 일반.**

#### `:Permission` — 실행 권한 (역량과 별개, P3)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id`, `code` | string | `git.commit`, `spend.request` |
| `gate` | enum | `none` / `approval` |
| `approverRole` | enum | `team-lead` / `admin` |
| `limitJson` | string | `{"currency":"KRW","max":1000000}` |
| `catalogVersion` | string | **오케스트레이터 내장 grant-catalog와 동일 버전이어야 한다**(§9.2) |

`:Permission.gate`의 2값(`none` / `approval`)은 `:ActionType.gateClass`의 3값(`none` / `approval.team_lead` / `approval.admin`)으로부터 파생되며, 축소 규칙은 `80-action-layer.md §8.1.1`을 참조.

#### `:PermissionProfile`

| 속성 | 타입 | 설명 |
|---|---|---|
| `id`, `name` | string | `builder`, `readonly`, `finance` |
| `version` | int | 변경 시 증가. 배정 근거 스냅샷에 기록됨 |

### 3.4 업무 (WBS)

#### `:WorkItem` — prov:Activity 투영. **물리·디지털 공통 (P5)**

| 속성 | 타입 | 필수 | 설명 |
|---|---|---|---|
| `id`, `title` | string | ✔ | |
| `kind` | enum | ✔ | `project` / `deliverable` / `task` / `checklistItem` |
| `executionMode` | enum | ✔ | `digital` / `physical` / `hybrid` |
| `status` | enum | ✔ | `planned` / `assigned` / `in_progress` / `awaiting_approval` / `awaiting_human` / `awaiting_verification` / `done` / `blocked` / `cancelled` |
| `effortPoints` | int | ✔ | 롤업 가중치이자 부하 계산 단위. 리프에만 의미 있음 |
| `plannedStart`, `dueAt` | datetime | | |
| `progressPct` | int | ✔ | rollup-engine이 계산 (리프는 0/100, 상위는 가중평균) |
| `actionClass` | enum | ✔ | `internal` / `external_effect` — 후자는 승인 게이트 필수 |
| `securityLabel` | enum | ✔ | `public` / `internal` / `confidential` — 배정 하드 필터 |
| `sourceRevision` | int | ✔ | 이 WBS를 만든 시점의 OrgRevision |

관계: `(:WorkItem)-[:PARENT_OF]->(:WorkItem)`, `(:WorkItem)-[:DEPENDS_ON]->(:WorkItem)`, `(:WorkItem)-[:REQUIRES_CAPABILITY {minLevel, weight}]->(:Capability)`, `(:WorkItem)-[:OWNED_BY]->(:Team)`

#### `:CompletionCriterion` — 완료를 무엇으로 인정하는가

| 속성 | 타입 | 설명 |
|---|---|---|
| `kind` | enum | `commit` / `artifact` / `checklist` / `attestation` / `approval` |
| `spec` | string(JSON) | 종류별 요구 (예: `{"repo":"…","branch":"…"}`, `{"items":["PC#1","PC#2"]}`) |

**이것이 P5를 성립시키는 장치다.** 개발 업무는 `kind: commit`, 사무실 이전 같은 물리 업무는 `kind: attestation`(사람의 체크 + 사진/메모). 트리 구조·롤업·배정은 완전히 동일하고 **리프의 완료 판정 방식만 다르다.**

#### `:Artifact` / `:Evidence` — prov:Entity 투영

| 라벨 | 속성 |
|---|---|
| `:Artifact` | `id`, `kind`(`commit`/`document`/`dataset`/`config`), `uri`, `sha`, `createdAt` |
| `:Evidence` | `id`, `kind`(`photo`/`note`/`receipt`), `uri`, `submittedBy`, `submittedAt` |

관계: `(:WorkItem)-[:PRODUCES]->(:Artifact)`, `(:WorkItem)-[:USED]->(:Artifact|:Asset)`, `(:Artifact)-[:DERIVED_FROM]->(:Artifact)`, `(:WorkItem)-[:ATTESTED_BY]->(:Evidence)`

#### `:Asset` — 자산 (콘솔 온톨로지 화면의 "자산" 필터)

`id`, `kind`(`repository`/`equipment`/`facility`/`account`), `uri`, `location`. 물리 자산(사무실, PC)과 디지털 자산(리포지토리, 광고계정)을 같은 라벨로 담는다.

### 3.5 배정

#### `:Assignment` — 현재 배정 (가변)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id` | string | |
| `assignedAt` | datetime | |
| `status` | enum | `active` / `awaiting_human` / `completed` / `revoked` |
| `dueAt` | datetime | |
| `taskPoints` | int | 부하 계산에 쓰이는 값(= WorkItem.effortPoints 복사) |

관계: `(:Assignment)-[:OF_TASK]->(:WorkItem)`, `(:Assignment)-[:TO]->(:Party)`

#### `:AssignmentDecision` — **불변 근거 스냅샷** (콘솔 최우선 요구)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id` | string | |
| `decidedAt` | datetime | |
| `orgRevision` | int | **결정 시점의 조직 리비전. 재현의 핵심 키** |
| `policyVersion` | int | `:AssignmentPolicy.version` |
| `algorithmVersion` | string | 배정 코드 버전 (`"assign-1.2.0"`) |
| `requiredCapsJson` | string | 요구 역량 **비정규화 복사** (`[{"code":"cap.media.buying","minLevel":3}]`) |
| `tieBreakChainJson` | string | 적용된 tie-break 순서 복사 |
| `outcome` | enum | `assigned` / `degraded` / `decompose_required` / `escalated_lead` / `escalated_admin` / `unassignable` |
| `fallbackReason` | string | outcome이 assigned가 아닐 때의 사유 코드 |

관계:
- `(:AssignmentDecision)-[:FOR]->(:WorkItem)`
- `(:AssignmentDecision)-[:SELECTED]->(:Party)` (outcome=assigned/degraded일 때만)
- `(:AssignmentDecision)-[:PRODUCED]->(:Assignment)`
- `(:AssignmentDecision)-[:USED_POLICY]->(:AssignmentPolicy)` ← prov:used
- **`(:AssignmentDecision)-[:CONSIDERED {...}]->(:Party)`** — **탈락자 포함 전 후보에 하나씩**

`[:CONSIDERED]` 엣지 속성 (전부 **결정 시점 값의 비정규화 복사**):

| 속성 | 타입 | 설명 |
|---|---|---|
| `rank` | int | 최종 순위. 탈락자는 `null` |
| `verdict` | enum | `eligible` / `excluded` |
| `exclusionReasons` | string[] | `["CAPABILITY_MISSING:cap.media.buying","CAPACITY_EXCEEDED"]` — **모든 사유를 담는다**(단축 평가하지 않음) |
| `matchedCapabilities` | string[] | `["cap.reporting@4>=3"]` |
| `missingCapabilities` | string[] | `["cap.media.buying"]` |
| `specificityScore` | int | tie-break 1단계 값 |
| `loadAtDecision` | float | tie-break 2단계 값 (활성 taskPoints / maxTaskPoints) |
| `sameTeam` | bool | tie-break 3단계 |
| `contextContinuity` | bool | tie-break 4단계 (상위 WBS를 이미 수행했는가) |
| `partyKindAtDecision` | string | `Person` / `AIAgent` — 나중에 노드가 바뀌어도 당시 종류를 보존 |
| `displayNameAtDecision` | string | **당시 이름.** 개명·퇴사해도 근거 화면이 깨지지 않는다 |

> **왜 비정규화하는가**: 콘솔 요구는 *"조직이 나중에 바뀌어도 과거 배정의 '왜'가 그대로 재현되어야 한다"*이다. 역량 노드를 참조만 해두면 그 역량이 이름이 바뀌거나 카탈로그에서 사라졌을 때 근거 화면이 깨진다. **결정은 그 시점의 사실로 얼려야 한다.** 저장 비용은 배정 1건당 수 KB로 무시할 만하다.

#### `:AssignmentOverride` — 대표의 수동 재배정 (별도 레이어)

| 속성 | 타입 | 설명 |
|---|---|---|
| `id`, `at` | | |
| `reason` | string | 없을 수 있음(콘솔이 선택 입력) |

관계: `(:AssignmentOverride)-[:OVERRIDES]->(:AssignmentDecision)`, `-[:TO]->(:Party)`, `-[:BY]->(:Party)`

**결정을 절대 수정하지 않는다.** 콘솔의 "규칙 배정: 경영관리 → 대표 override" 표시가 이 구조에서 그대로 나온다.

#### `:AssignmentPolicy`

| 속성 | 타입 | 설명 |
|---|---|---|
| `version` | int | |
| `effectiveFrom` | datetime | |
| `tieBreakChain` | string[] | `["specificity","load","sameTeam","contextContinuity","verificationRecency","partyId"]` |
| `teamStrict` | bool | 팀 경계를 하드 필터로 쓸지 |
| `levelRelaxationAllowed` | bool | 폴백 1단계 허용 여부 |
| `maxCandidatesForDecompose` | int | 집합 커버 폴백에서 고려할 최대 조합 크기(기본 2) |

### 3.6 승인 · 알림

#### `:Approval`

`id`, `approvalType`(`commit`/`message`/`spend`), `status`(`pending`/`approved`/`rejected`/`changes_requested`), `requestedAt`, `decidedAt`, `payloadRef`(JetStream seq), `amountKrw`(spend일 때), `comment`

관계: `-[:GATES]->(:WorkItem)`, `-[:REQUESTED_BY]->(:Party)`, `-[:DECIDED_BY]->(:Party)`

> **`[2026-08-13 인계]` 승인 단위 여지 — 지금 결정하지 않는다.** 위 `-[:GATES]->(:WorkItem)`은 승인 하나가 `:WorkItem` 하나를 가리키는 1:1 모양이다. 자매 프로젝트 Blueurban AaaM(`Blueurban-GroupWare/DESIGN-AaaM.md`)의 실측 경고(D17)는 **티켓 단위 확인요청이 하루 여러 건 규모에 이르면 대표(승인자) 1인 병목으로 붕괴한다**는 것이었고, Blueurban은 이 붕괴를 겪은 뒤 **계획 단위 사전승인**(상위 계획/프로젝트 수준의 승인 하나가 그 하위 다수 `:WorkItem`을 한 번에 커버)으로 승격했다. OSiris는 아직 이 규모에 도달하지 않았으므로 **지금 스키마를 바꾸지 않는다** — 다만 `:Approval`이 `:WorkItem` 1개만 가리킬 수 있다는 이 문서의 현재 모양이 실사용 규모에서 같은 병목을 만들 수 있다는 것을 **P4(구현 단계)가 승인 단위를 설계할 때 고려해야 할 경고로 인계**한다. `GATES`가 상위 `:WorkItem`(kind:`deliverable`/`project`)을 가리키는 것 자체는 현재 스키마로도 가능하므로(관계 제약이 `kind`를 한정하지 않는다), "계획 단위 사전승인"으로 가는 길을 막는 스키마 변경은 필요 없을 수 있다 — 그 판단 자체를 지금 내리지 않고 P4에게 넘긴다.

#### `:Notification`

`id`, `kind`, `seq`, `createdAt`, `readAt`, `deepLink`, `severity`(`info`/`warning`/`critical`) — `-[:FOR_PARTY]->(:Party)`

### 3.7 런타임 캐시 — **권위 아님 (P2)**

이 절의 노드는 전부 관측값의 미러다. **`observedAt` 없이 존재해서는 안 되며, 60초 이상 stale이면 콘솔이 회색 처리한다.**

#### `:AgentRuntime` (`:AIAgent`와 1:1)

`employeeId`, `phase`, `sessionId`, `containerOrPodName`, `nodeName`, `lastHeartbeatAt`, `configHash`, `restartCount24h`, `currentTaskId`, `ready`(bool), `gatewayReachable`(bool), **`observedAt`**

관계: `(:AIAgent)-[:RUNTIME]->(:AgentRuntime)`

#### `:ClusterNode`

`name`, `status`(`ready`/`pressure`/`offline`), `allocatableCpuMilli`, `allocatableMemMiB`, `usedCpuMilli`, `usedMemMiB`, `lastHeartbeatAt`, **`observedAt`**
*(1단계에서는 호스트 1개만 존재한다.)*

#### `:RoleResourceProfile` — **좌석 환산의 근거** (콘솔 요구)

`id`(= `runtimeClass`), `cpuMilli`, `memMiB`, `storageGiB`, `label`("개발자 에이전트")

이것은 캐시가 아니라 **의도**다(사람이 정한다). 여기 둔 이유는 좌석 환산이 `:ClusterNode`와 짝을 이루기 때문이며, 쓰기는 다른 의도 노드와 동일하게 graph-write API를 지난다.

### 3.8 시스템

#### `:OrgRevision` — 전역 단조 증가 체인

`n`(int, 유일), `at`, `byPartyId`, `summary`, `changeKind`
관계: `(:OrgRevision)-[:PREV]->(:OrgRevision)`

**모든 의도 변경 트랜잭션은 새 `:OrgRevision`을 만들고 `n`을 1 증가시킨다.** 이 번호가 (a) 수렴 루프의 트리거, (b) 배정 근거의 재현 키, (c) 멤버십의 시스템 시간이다.

#### `:JoinToken` (2단계에서만 채워짐)

`id`, `hash`, `issuedAt`, `expiresAt`, `usedAt`, `issuedByPartyId`, `nodeName` — 평문 토큰은 저장하지 않는다.

---

## 4. 관계 타입 전체 목록

| 관계 | 출발 → 도착 | 속성 | 표준 대응 |
|---|---|---|---|
| `HAS_MEMBERSHIP` | `:Party` → `:Membership` | | org:Membership |
| `IN_TEAM` | `:Membership` → `:Team` | | org:organization |
| **`IN_ORG`** `[2026-07-27 신설]` | `:Membership` → `:Organization` | | org:organization — **조직 직속 멤버십**(§3.2). 출발 멤버십의 `:Party`는 `:Person`만(C9). `IN_TEAM`과 **상호 배타**: 한 `:Membership`은 둘 중 정확히 하나를 갖는다 |
| `AS_ROLE` | `:Membership` → `:Role` | | org:role. `IN_TEAM` 멤버십은 `scope:'job'`, `IN_ORG` 멤버십은 `scope:'org'`인 `:Role`을 가리킨다 |
| `PART_OF` | `:Team` → `:Team`\|`:Organization` | | org:subOrganizationOf |
| `HAS_CAPABILITY` | `:Party` → `:Capability` | `level`(1-5), `source`(`declared`/`verified`/`inferred`), `verifiedAt`, `evidenceRef` | — |
| `BROADER` | `:Capability` → `:Capability` | | skos:broader |
| `REQUIRES_CAPABILITY` | `:Role`\|`:WorkItem` → `:Capability` | `minLevel`, `weight` | — |
| `REQUIRES_PERMISSION` | `:Capability` → `:Permission` | | — (§7.2) |
| `HAS_PROFILE` | `:Role`\|`:Party` → `:PermissionProfile` | `kind`(`base`/`additive`) | — |
| `GRANTS` | `:PermissionProfile` → `:Permission` | `conditionsJson` | — |
| `DENIES` | `:Party`\|`:PermissionProfile` → `:Permission` | `reason` | — (deny > allow) |
| `PARENT_OF` | `:WorkItem` → `:WorkItem` | `ordinal` | — |
| `DEPENDS_ON` | `:WorkItem` → `:WorkItem` | `kind`(`fs`/`ss`) | — |
| `OWNED_BY` | `:WorkItem` → `:Team` | | — |
| `HAS_CRITERION` | `:WorkItem` → `:CompletionCriterion` | | — |
| `PRODUCES` | `:WorkItem` → `:Artifact` | | prov:wasGeneratedBy(역) |
| `USED` | `:WorkItem` → `:Artifact`\|`:Asset` | | prov:used |
| `DERIVED_FROM` | `:Artifact` → `:Artifact` | | prov:wasDerivedFrom |
| `ATTESTED_BY` | `:WorkItem` → `:Evidence` | | — |
| `OF_TASK` | `:Assignment` → `:WorkItem` | | — |
| `TO` | `:Assignment`\|`:AssignmentOverride` → `:Party` | | — |
| `PERFORMED_BY` | `:WorkItem` → `:Party` | `startedAt`, `endedAt` | prov:wasAssociatedWith |
| `FOR` | `:AssignmentDecision` → `:WorkItem` | | — |
| `CONSIDERED` | `:AssignmentDecision` → `:Party` | §3.5의 11개 속성 | prov:used |
| `SELECTED` | `:AssignmentDecision` → `:Party` | | — |
| `PRODUCED` | `:AssignmentDecision` → `:Assignment` | | prov:generated |
| `USED_POLICY` | `:AssignmentDecision` → `:AssignmentPolicy` | | prov:used |
| `OVERRIDES` | `:AssignmentOverride` → `:AssignmentDecision` | | — |
| `BY` | `:AssignmentOverride` → `:Party` | | — |
| `GATES` | `:Approval` → `:WorkItem` | | — |
| `REQUESTED_BY` / `DECIDED_BY` | `:Approval` → `:Party` | | — |
| `FOR_PARTY` | `:Notification` → `:Party` | | — |
| `RUNTIME` | `:AIAgent` → `:AgentRuntime` | | — (캐시) |
| `RUNS_ON` | `:AgentRuntime` → `:ClusterNode` | | — (캐시) |
| `USES_PROFILE` | `:AIAgent`\|`:Role` → `:RoleResourceProfile` | | — |
| `PREV` | `:OrgRevision` → `:OrgRevision` | | org:ChangeEvent |

---

## 5. 역량(Capability) 모델과 taxonomy

### 5.1 구조

`(specific)-[:BROADER]->(general)`. 보유 판정 규칙:

> **`p`가 역량 `c`를 레벨 `L` 이상으로 보유한다** ⟺ `p`가 `c` 자신 또는 **`c`의 하위(더 구체적인) 역량**을 레벨 `L` 이상으로 보유한다.

Cypher로: `(p)-[h:HAS_CAPABILITY]->(hc)-[:BROADER*0..3]->(c) WHERE h.level >= L`

즉 "Python"을 보유하면 "프로그래밍" 요구를 충족하지만, "프로그래밍"을 보유한다고 "Python" 요구를 충족하지는 않는다. **방향이 한쪽뿐인 것이 핵심**이며, 이것이 tie-break의 `specificity`(정확 일치가 상위 일치보다 낫다)와 짝을 이룬다.

깊이를 `*0..3`으로 제한하는 이유: 무제한 경로는 카탈로그에 사이클이 생기면 폭주한다. 3단계면 `프로그래밍 > 백엔드 > Python > Django` 수준을 커버한다. 사이클은 §11의 제약으로 방지한다.

### 5.2 기본 taxonomy (시드 카탈로그, `catalogVersion: "2026.07"`)

```
cap.root
├── cap.tech                          기술
│   ├── cap.code                      코드 작성
│   │   ├── cap.code.python
│   │   ├── cap.code.go
│   │   ├── cap.code.ts
│   │   └── cap.code.review           코드 리뷰
│   ├── cap.infra
│   │   ├── cap.infra.k8s
│   │   ├── cap.infra.network
│   │   └── cap.infra.db
│   └── cap.data
│       ├── cap.data.analysis
│       └── cap.data.pipeline
├── cap.biz                           경영·회계
│   ├── cap.biz.accounting
│   ├── cap.biz.budgeting
│   ├── cap.biz.contract
│   └── cap.biz.procurement
├── cap.mkt                           마케팅
│   ├── cap.mkt.research
│   ├── cap.mkt.copy
│   ├── cap.mkt.creative
│   ├── cap.mkt.media_buying
│   └── cap.mkt.reporting
├── cap.ops                           총무·운영
│   ├── cap.ops.facility
│   ├── cap.ops.equipment
│   └── cap.ops.vendor
└── cap.exec                          실행 양태 (★ 특수)
    ├── cap.exec.physical             물리 세계 실행 — **:Person 만 보유 가능(하드 제약)**
    ├── cap.exec.digital
    └── cap.exec.decision             의사결정 권한이 필요한 실행
```

**`cap.exec.physical`의 설계 의도**: "사무실 → 컴퓨터 셋팅 → 네트워크 구성" 같은 물리 업무를 AI에게 배정하지 않기 위해, **별도의 규칙을 두지 않고 역량 하나로 해결한다.** WBS 리프에 `REQUIRES_CAPABILITY cap.exec.physical`이 붙으면, 그 역량을 가질 수 있는 것은 `:Person`뿐이므로 후보가 자연히 사람으로 좁혀진다. `if (task.isPhysical) assignToHuman()` 같은 특례 코드가 필요 없다 — **P5(같은 구조)를 지키면서 물리/디지털을 가르는 장치**다.

### 5.3 레벨 척도 (고정, 5단계)

| level | 의미 | 판정 근거 (`source`) |
|---|---|---|
| 1 | 인지 — 개념을 안다 | `declared` |
| 2 | 보조 — 지시받아 수행 가능 | `declared` |
| 3 | **독립 수행** — 기본 배정 기준선 | `verified`(온보딩 리허설 통과 또는 실적) |
| 4 | 숙련 — 비정형 상황 대응 | `verified` |
| 5 | 지도 — 타인의 산출물 검증 가능 | `verified` |

`WorkItem.REQUIRES_CAPABILITY.minLevel`의 기본값은 3이다. LLM(Master)이 WBS를 만들 때 이 값을 정하되 **1~5 정수로 제한**된다.

---

## 6. 업무(WBS) 계층 모델

### 6.1 계층과 롤업

```
:WorkItem{kind:project}          "예시 앱 신규 광고 캠페인"   executionMode: hybrid
 ├ :WorkItem{kind:deliverable}   "1. 시장/경쟁 리서치"            digital,  effort 5
 ├ :WorkItem{kind:deliverable}   "3. 예산안 수립"                 digital,  effort 3
 └ :WorkItem{kind:deliverable}   "네트워크 구성"                  physical, effort 8
     ├ :WorkItem{kind:checklistItem} "PC #1 셋팅"                 physical, effort 1
     ├ :WorkItem{kind:checklistItem} "PC #2 셋팅"                 physical, effort 1
     └ :WorkItem{kind:checklistItem} "스위치 배선"                physical, effort 2
```

**롤업 규칙 (결정적 코드)**: 리프의 `progressPct`는 0 또는 100. 상위는 `Σ(리프 effortPoints × progressPct) / Σ(리프 effortPoints)`. 하위가 없으면 자기 자신이 리프다. rollup-engine이 `task.*.event` 수신 시 조상 경로만 재계산한다(전체 재계산 금지).

**배정 단위**: `:Assignment`는 **어느 계층에나 붙을 수 있다**. 실무적으로는 `deliverable` 또는 `checklistItem`에 붙는다. 상위에 배정이 있고 하위에도 있으면 하위가 우선한다(가장 구체적인 배정이 실행 담당).

### 6.2 물리 업무와 디지털 업무 (P5)

| | 디지털 | 물리 |
|---|---|---|
| `executionMode` | `digital` | `physical` |
| 요구 역량 | `cap.code.*` 등 + `cap.exec.digital` | 도메인 역량 + **`cap.exec.physical`** |
| `:CompletionCriterion.kind` | `commit` / `artifact` | `attestation` / `checklist` |
| 완료 제출 | 에이전트가 `PRODUCES → :Artifact` | 사람이 `ATTESTED_BY → :Evidence`(사진/메모) + 콘솔 `complete-report` |
| 검증 | CI 결과 + 승인 게이트 | 대표/팀장의 `verify` → `awaiting_verification` → `done` |
| 트리·롤업·배정 | **완전히 동일** | **완전히 동일** |

`hybrid`는 하위에 두 종류가 섞인 상위 노드에 붙인다(예: "네트워크 구성"에 물리 배선과 원격 설정 스크립트가 함께 있는 경우). `hybrid`인 리프는 허용하지 않는다(§11 제약).

### 6.3 상태 전이

```
planned ──배정──▶ assigned ──착수──▶ in_progress
                                        │
       ┌────────────────────────────────┼─────────────────────┐
       │ 외부영향 액션                    │ 사람 담당              │ 완료
       ▼                                ▼                     ▼
 awaiting_approval              awaiting_human        awaiting_verification
       │ 승인                          │ 완료보고               │ 검증
       └──────────▶ in_progress ◀──────┘                      ▼
                       │ 반려/실패                             done
                       ▼
                    blocked ──재배정──▶ assigned
```

`cancelled`는 어느 상태에서나 진입 가능(대표만). `blocked`는 assignment-engine의 `unassignable` 결과 또는 24시간 무응답 에스컬레이션(`10-core-architecture.md` §7.5)으로 진입한다.

---

## 7. 권한 모델

### 7.1 유효 권한(effective permission) 계산 — 결정적

```
effective(party) =
      ( ∪  grants(profile)  for profile in base_profiles(party) )        # 직무 프로파일
   ∪  ( ∪  grants(profile)  for profile in additive_profiles(party) )    # 개인 추가
   −  denies(party)                                                       # 개인 거부
   −  ∪ denies(profile)                                                   # 프로파일 거부
```

- `base_profiles(party)` = 현재 유효한 `:Membership`의 `:Role`에 붙은 `HAS_PROFILE{kind:'base'}`
  — **`[2026-07-27]` 이 정의는 무변경이며, 그래서 `IN_ORG` 조직 멤버십(§3.2)이 아무 수식 변경 없이 여기 포함된다.** 이 정의가 애초에 "팀"을 요구하지 않고 "멤버십의 `:Role`"이라고만 쓰여 있었기 때문이다. 한 사람이 팀 직무 1개 + 조직 역할 1개를 가지면 base가 두 개이며 합집합이 된다 — 겸직(`allocationPct`)이 이미 전제하던 성질과 같다
- `additive_profiles(party)` = `(party)-[:HAS_PROFILE{kind:'additive'}]->` (개인 예외)
- **deny가 항상 이긴다.** 개인 additive로 권한을 더할 수는 있어도, base에서 뺄 수는 없다 — 뺄 때는 반드시 `DENIES`로 명시한다. 이유: "왜 이 사람에게 권한이 없는가"가 항상 명시적 사실로 남아야 감사가 성립한다.

이 계산 결과가 `10-core-architecture.md` §8.1의 4개 층위(RBAC / 네트워크 허용목록 / Claude Code allowedTools+게이트 / NATS ACL)로 파생된다. **파생은 그래프가 아니라 코드(grant-catalog)의 책임이며, 그래프는 "무엇을 허용했는가"만 저장한다.**

> **적용 범위: `effective(party)`는 `:Party`에만 정의된다.** 그래프에 노드가 없는 주체는 이 계산의 대상이 아니다. 그런 주체는 **두 종류뿐이며, 둘 다 조직 의도를 바꿀 수 없다:**
>
> 1. **제네시스 에이전트 `bootstrap-master`** — 최초 온보딩 시점에 정확히 하나. 인가는 그래프가 아니라 `osiris-core`의 고정 스코프이고, **"조직이 비어 있는 동안"에만 유효하도록 요청 시점마다 재평가된다**(`10-core-architecture.md` §8.5, A13). 조직에 `:Party`가 하나라도 생기고 Master가 Ready가 되는 순간 그 경로는 **영구히 닫힌다**(W1).
> 2. **`[2026-07-27 신설]` 검증 전용 기계 주체 및 master key 세션** — `osiris-api`가 소유하는 비대화형/부트스트랩 자격증명(`60-identity-authn.md` §19, §4.0). 이들은 `:Party`가 아니므로 이 계산의 대상이 아니지만, **같은 `:Permission` 카탈로그의 코드로만 인가된다**(별도 권한 어휘를 만들지 않는다). 기계 주체의 스코프는 `org.read.*` 접두 코드로 상한이 걸려 있어 **그래프를 변경하는 것이 표현 자체로 불가능**하고, master key 세션은 H10의 두 조건이 참이 되는 순간 영구히 사라진다.
>
> **따라서 이 스키마가 유지하는 성질은 다음과 같이 정밀하게 쓴다 — 그리고 이 형태가 원래 의도한 것보다 강하다:**
> **조직 의도(`:OrgRevision`을 올리는 모든 변경)를 일으킬 수 있는 주체는 `:Party`뿐이다.** 그래프에 없는 주체는 (1) 조직이 비어 있는 동안의 제네시스이거나, (2) 읽기만 하는 기계 주체이며, 둘 다 `:OrgRevision.byPartyId`·`DECIDED_BY`·`BY`에 도달할 수 없다 — 후자는 `10-core-architecture.md` §8.5의 W1과 `60-identity-authn.md` §6.5(*"`actorPartyId`가 유효한 `:Party`가 아니면 core는 mutating 요청을 거부"*)가 각각 독립적으로 보장한다.
>
> *(이력: 초판은 "이 스키마가 표현하지 못하는 주체는 정상 운영 중에는 존재하지 않는다"였다. `60-identity-authn.md` §19가 상시 존재하는 읽기 전용 기계 주체를 도입하면서 그 문장은 문자 그대로는 거짓이 되었다. **약화가 아니라 정밀화다** — 원래 문장이 지키려던 보안 내용("표현되지 않은 주체가 조직을 바꿀 수 없다")은 그대로 유지되고, 오히려 명시적으로 진술되었다.)*

### 7.2 역량 → 실행 권한의 다리 (P3를 지키면서 연결하기)

`(:Capability)-[:REQUIRES_PERMISSION]->(:Permission)` 의 의미:

> **이 역량을 실제로 행사하려면 이 권한이 필요하다.**

예: `cap.code.python -[:REQUIRES_PERMISSION]-> git.commit`, `cap.biz.procurement -[:REQUIRES_PERMISSION]-> spend.request`

두 곳에서 쓰인다:
1. **배정 하드 필터 `NOT_PERMITTED`** — 태스크가 요구하는 역량들의 `REQUIRES_PERMISSION` 합집합이 후보의 유효 권한에 포함되지 않으면 탈락. "할 줄은 아는데 해도 되지 않는 사람"에게 배정하지 않는다.
2. **온보딩 검증 경고 `INERT_CAPABILITY`** — 어떤 에이전트가 역량은 갖지만 그 역량이 요구하는 권한이 없으면 "무용지물 역량"으로 경고한다. 조직 설계 실수를 온보딩 단계에서 잡는다.

**역량과 권한을 하나로 합치지 않은 이유**: 합치면 "역량은 있지만 권한은 유보"(신입 에이전트에게 커밋 권한을 아직 안 줌)와 "권한은 있지만 역량이 없음"(대표는 모든 권한을 갖지만 Python을 못 씀)을 표현할 수 없다. 두 축은 독립이다.

---

## 8. 결정적 배정 알고리즘 (의사코드)

> **이 알고리즘은 LLM을 호출하지 않는다.** 실행되는 프로세스(`osiris-core`)에는 `claude` 바이너리도 게이트웨이 자격증명도 없다(`10-core-architecture.md` A4). 런타임 토큰 비용 0.

### 8.1 입력

```
Task {
  id, owningTeamId, dueAt, effortPoints,
  requiredCapabilities: [ {code, minLevel, weight} ],
  actionClass:  internal | external_effect,
  securityLabel: public | internal | confidential,
  executionMode: digital | physical | hybrid
}
OrgGraph      : Neo4j 현재 상태 (유효 Membership, HAS_CAPABILITY, 권한)
RuntimeState  : { partyId -> {ready, activeTaskPoints, maxTaskPoints, availability} }
                 ← :AgentRuntime 캐시 + :Person.availability. stale(>60s)이면 ready=false 취급
Policy        : :AssignmentPolicy (현재 유효 버전)
```

### 8.2 본체

```
FUNCTION assign(task, asOf):
    policy   := loadActivePolicy(asOf)
    rev      := currentOrgRevision()
    cands    := candidateSet(task, policy)          # §8.3 그래프 질의 1회
    evals    := []

    FOR c IN cands:
        e := evaluate(c, task, policy)              # §8.4. 모든 필터를 끝까지 평가
        evals.append(e)

    eligible := [e FOR e IN evals IF e.verdict == ELIGIBLE]

    IF eligible IS EMPTY:
        RETURN fallback(task, evals, policy, rev)   # §8.6

    ranked := sortStable(eligible, comparator(policy.tieBreakChain))   # §8.5
    FOR i, e IN enumerate(ranked): e.rank := i + 1

    winner   := ranked[0]
    decision := persistDecision(task, evals, policy, rev, outcome=ASSIGNED, selected=winner)
    assignment := createAssignment(task, winner.party, decision)
    publish("task." + task.id + ".event", {type: "assigned", to: winner.party.id})
    RETURN assignment
```

**모든 후보를 끝까지 평가하고 단축하지 않는다.** 콘솔의 근거 팝오버가 "탈락자와 탈락 사유"를 요구하기 때문이다. 후보 수는 조직 규모(수십)이므로 비용은 무시할 만하다.

### 8.3 후보 집합 (하드 필터 1차 — 그래프에서)

```
FUNCTION candidateSet(task, policy):
    # 현재 유효한 **팀** 멤버십을 가진 모든 Party (사람 + AI 동일 취급, P1)
    base := MATCH (p:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(:Team)
            WHERE m.validTo IS NULL AND p.closedAtRevision IS NULL
              AND (p:Person OR p:AIAgent)          # :Team, :Organization 은 후보 아님
    IF policy.teamStrict AND task.owningTeamId != NULL:
        base := base WHERE (m)-[:IN_TEAM]->({id: task.owningTeamId})
    RETURN base
```

> **`[2026-07-27]` 첫 줄의 `-[:IN_TEAM]->(:Team)` 제한은 신설이며, 오늘의 결과를 한 건도 바꾸지 않는다** — 이 문장이 쓰일 당시 모든 `:Membership`이 `IN_TEAM`이었기 때문이다. §3.2가 조직 직속 멤버십(`IN_ORG`)을 도입하면서, 제한이 없으면 **조직 역할만 가진 사람(예: 팀에 속하지 않는 관리자)이 `teamStrict:false`인 배정의 후보로 들어간다.** 배정 엔진은 팀 소속과 역량으로 사람을 고르는 것이지 조직 권한으로 고르는 것이 아니므로, 새 관계가 이 알고리즘에 **닿지 못하게 명시적으로 잠근다**(`60-identity-authn.md` §20.2 ①, HI20).

### 8.4 후보 평가 (하드 필터 2차 + 점수 재료)

```
FUNCTION evaluate(c, task, policy):
    reasons := []      # 단축하지 않고 전부 수집
    matched := [];  missing := [];  specificity := 0

    # F1 역량
    FOR req IN task.requiredCapabilities:
        best := maxLevel( c HAS_CAPABILITY hc WHERE (hc)-[:BROADER*0..3]->(req.code) )
        IF best.level >= req.minLevel:
            matched.append(req.code + "@" + best.level + ">=" + req.minLevel)
            IF best.node.code == req.code: specificity += 2      # 정확 일치
            ELSE:                          specificity += 1      # 상위 일치
        ELSE:
            missing.append(req.code)
            reasons.append("CAPABILITY_MISSING:" + req.code)

    # F2 권한 — 요구 역량들이 필요로 하는 권한을 모두 갖는가 (§7.2)
    needPerms := ∪ { REQUIRES_PERMISSION(cap) FOR cap IN task.requiredCapabilities }
    IF task.actionClass == external_effect:
        needPerms := needPerms ∪ { gatePermissionFor(task) }
    FOR perm IN needPerms:
        IF perm ∉ effectivePermissions(c):                        # §7.1
            reasons.append("NOT_PERMITTED:" + perm)

    # F3 가용성
    rt := RuntimeState[c.id]
    IF c IS AIAgent:
        IF NOT rt.ready OR rt.phase IN {Failed, Suspended, Provisioning}:
            reasons.append("UNAVAILABLE:" + coalesce(rt.notReadyReason, rt.phase))
    ELSE:                                                          # Person
        IF c.availability IN {off, leave} OR onLeaveAt(c, task.dueAt):
            reasons.append("UNAVAILABLE:leave")

    # F4 용량
    load := rt.activeTaskPoints / c.maxTaskPoints
    IF rt.activeTaskPoints + task.effortPoints > c.maxTaskPoints:
        reasons.append("CAPACITY_EXCEEDED")

    # F5 데이터 스코프
    IF NOT scopeAllows(c, task.securityLabel):
        reasons.append("SCOPE_VIOLATION:" + task.securityLabel)

    # F6 예산 (팀 월 예산 소진)
    IF c IS AIAgent AND teamBudgetExhausted(teamOf(c)):
        reasons.append("BUDGET_EXHAUSTED")

    RETURN Eval {
        party: c,
        verdict: (reasons IS EMPTY) ? ELIGIBLE : EXCLUDED,
        exclusionReasons: reasons,
        matchedCapabilities: matched,
        missingCapabilities: missing,
        specificityScore: specificity,
        loadAtDecision: load,
        sameTeam: (teamOf(c) == task.owningTeamId),
        contextContinuity: hasWorkedOnAncestor(c, task),
        verificationRecency: minVerifiedAt(c, task.requiredCapabilities),
        partyKindAtDecision: labelOf(c),
        displayNameAtDecision: c.name
    }
```

**F1의 `specificity` 가중치(정확 2점, 상위 1점)**가 tie-break 1단계의 값이다. "Python 요구에 Python 보유자"가 "Python 요구에 프로그래밍 보유자"보다 앞선다.

### 8.5 tie-break 체인 (순서 고정, 전순서, 결정적)

| 순위 | 기준 | 방향 | 이유 |
|---|---|---|---|
| 1 | `specificityScore` | 내림차순 | 더 정확히 맞는 사람 |
| 2 | `loadAtDecision` | 오름차순 | 덜 바쁜 사람 (콘솔 팝오버가 명시한 규칙) |
| 3 | `sameTeam` | `true` 우선 | 팀 경계 존중(soft) |
| 4 | `contextContinuity` | `true` 우선 | **상위 WBS를 이미 수행한 사람** — 컨텍스트 재구축 토큰이 절약된다. compute로 얻는 실질 이득 |
| 5 | `verificationRecency` | 최근 우선 | 최근에 검증된 역량이 더 신뢰할 만함 |
| 6 | **`party.id` 사전순** | 오름차순 | **최종 전순서 보장. 난수 절대 금지** |

6번이 있어야 "같은 입력이면 항상 같은 결과"가 성립하고, 그래야 배정 근거가 재현 가능하다. 정렬은 **stable sort**를 쓴다.

의사코드:
```
FUNCTION comparator(chain):
    RETURN λ(a, b):
        FOR key IN chain:
            d := compareByKey(key, a, b)
            IF d != 0: RETURN d
        RETURN compare(a.party.id, b.party.id)    # 체인이 다 비겨도 여기서 끝난다
```

### 8.6 폴백 (순서대로 시도, 첫 성공에서 종료)

```
FUNCTION fallback(task, evals, policy, rev):
    blockers := union of all exclusionReasons

    # FB1. 레벨 완화 — 딱 1단계만
    IF policy.levelRelaxationAllowed
       AND blockers ⊆ {CAPABILITY_MISSING}
       AND ∃ c: c가 모든 요구 역량을 (minLevel - 1) 로 보유:
        relaxed := assign(task with minLevel-1, asOf)
        mark relaxed.decision.outcome = DEGRADED
        require approval from team lead before dispatch
        RETURN relaxed

    # FB2. 분해 필요 — 결정적 집합 커버(작은 집합이므로 완전 탐색)
    IF blockers ⊆ {CAPABILITY_MISSING}:
        cover := minimalCover(task.requiredCapabilities, cands,
                              maxSize = policy.maxCandidatesForDecompose)   # 기본 2
        IF cover != NULL:
            persistDecision(outcome = DECOMPOSE_REQUIRED, hint = cover)
            notify(master, "이 태스크는 단일 담당자가 없습니다. 분해 제안: " + cover)
            RETURN DecomposeRequired(cover)

    # FB3. 팀장 에스컬레이션 — 능력은 있으나 지금 못 함
    IF blockers ⊆ {UNAVAILABLE, CAPACITY_EXCEEDED, BUDGET_EXHAUSTED}:
        eta := earliestFreeTime(candidates)
        persistDecision(outcome = ESCALATED_LEAD)
        enqueue(teamLead.inbox, {task, eta, reason: blockers})
        task.status := blocked
        RETURN EscalatedToLead(eta)

    # FB4. 대표 에스컬레이션 — 권한/스코프 문제는 절대 자동 완화하지 않는다
    IF NOT_PERMITTED ∈ blockers OR SCOPE_VIOLATION ∈ blockers:
        persistDecision(outcome = ESCALATED_ADMIN)
        notify(admin, severity=warning, {
            task, missingPermissions, suggestedAction:
              "권한 부여 또는 해당 권한을 가진 직무의 담당자 지정이 필요합니다" })
        task.status := blocked
        RETURN EscalatedToAdmin()

    # FB5. 역량 자체가 조직에 없음 → 채용/온보딩 트리거
    persistDecision(outcome = UNASSIGNABLE)
    notify(admin, severity=warning, {
        task, capabilityGap: missingAcrossAllCandidates,
        suggestedAction: "이 역량을 가진 직원(사람 또는 AI)이 조직에 없습니다. 온보딩이 필요합니다" })
    task.status := blocked
    RETURN Unassignable()
```

**FB4의 원칙: 권한 부족은 절대 자동 완화하지 않는다.** 역량은 한 단계 낮춰볼 수 있지만 권한은 사람이 명시적으로 부여해야 한다. 이것이 승인 게이트의 존재 이유를 지킨다.

**FB5는 온보딩의 입력이 된다** — "capability gap"이 곧 "새 직원이 필요한 이유"이고, 콘솔의 Onboarding 화면 뱃지가 이 신호로 켜진다.

### 8.7 근거 스냅샷 영속화

```
FUNCTION persistDecision(task, evals, policy, rev, outcome, selected):
    CREATE (d:AssignmentDecision {
        id: uuid(), decidedAt: now(),
        orgRevision: rev, policyVersion: policy.version,
        algorithmVersion: ALGO_VERSION,
        requiredCapsJson:  json(task.requiredCapabilities),   # 비정규화 — 얼린다
        tieBreakChainJson: json(policy.tieBreakChain),
        outcome: outcome, fallbackReason: ...
    })
    CREATE (d)-[:FOR]->(task)
    CREATE (d)-[:USED_POLICY]->(policy)
    FOR e IN evals:                                  # ★ 탈락자 포함 전원
        CREATE (d)-[:CONSIDERED {
            rank: e.rank, verdict: e.verdict,
            exclusionReasons: e.exclusionReasons,
            matchedCapabilities: e.matchedCapabilities,
            missingCapabilities: e.missingCapabilities,
            specificityScore: e.specificityScore,
            loadAtDecision: e.loadAtDecision,
            sameTeam: e.sameTeam, contextContinuity: e.contextContinuity,
            partyKindAtDecision: e.partyKindAtDecision,
            displayNameAtDecision: e.displayNameAtDecision
        }]->(e.party)
    IF selected: CREATE (d)-[:SELECTED]->(selected.party)
    RETURN d
```

**단일 트랜잭션**으로 `:AssignmentDecision` + `:Assignment` + `:WorkItem.status` 변경을 커밋한다. 부분 커밋으로 "배정은 됐는데 근거가 없는" 상태가 생기면 설명가능성이 무너진다.

### 8.8 override (대표의 수동 재배정)

```
FUNCTION override(taskId, newPartyId, byPartyId, reason):
    d := latestDecision(taskId)                  # 절대 수정하지 않는다
    CREATE (o:AssignmentOverride {at: now(), reason: reason})
    CREATE (o)-[:OVERRIDES]->(d)
    CREATE (o)-[:TO]->(newParty)
    CREATE (o)-[:BY]->(byParty)
    revoke(currentAssignment(taskId))            # status = revoked (삭제 아님)
    createAssignment(task, newParty, decision=d, viaOverride=o)
    # 규칙값과 override값이 둘 다 남는다 → 콘솔 C절 UI가 성립
```

**override는 권한 검사를 우회하지 않는다.** `newParty`가 `NOT_PERMITTED`면 override는 거부되고 콘솔에 사유가 표시된다. 대표라도 권한 없는 사람에게 외부영향 액션을 시킬 수는 없다(대신 대표는 권한을 부여할 수 있고, 그것은 별개의 명시적 행위다).

---

## 9. 온보딩이 이 스키마를 시드하는 순서

> 온보딩 파이프라인 자체는 다른 워커의 담당이다. 여기서는 **그 워커가 딛고 설 인터페이스**만 정의한다.

### 9.1 원칙

- **모든 시드는 `MERGE ... ON CREATE / ON MATCH`로 멱등**하다. 재실행이 안전해야 한다(온보딩은 상시 재실행형).
- **삭제하지 않는다.** 조직 변경은 `validTo` / `closedAtRevision` 설정이다.
- 모든 쓰기는 `osiris-core`의 graph-write API를 지난다. 온보딩은 **Cypher를 직접 실행하지 않는다.**
- **시드 전체가 하나의 Neo4j 트랜잭션이다.** 단계는 논리적 구분일 뿐 각각이 커밋되지 않으며, `:OrgRevision`은 마지막에 **한 번만** 증가한다(중간 상태로 수렴 루프가 돌지 않도록). 검증·리허설도 이 트랜잭션 안에서 수행하고 실패 시 통째로 롤백한다.

### 9.2 순서

| # | 단계 | 생성물 | 주의 |
|---|---|---|---|
| 0 | 스키마 마이그레이션 | 제약·인덱스(§11) | 부트스트랩이 수행. 온보딩 전에 완료되어 있어야 함 |
| 1 | 조직 루트 | `:Organization:Party`, `:OrgRevision{n:0}` | 1개만 |
| 2 | **역량 카탈로그** | `:Capability` + `[:BROADER]` (§5.2) | **시스템 소유.** 사용자가 임의 추가 불가. `catalogVersion` 고정 |
| 3 | **권한 카탈로그** | `:Permission` | **오케스트레이터 내장 grant-catalog와 `catalogVersion`이 일치해야 한다.** 부트스트랩이 코드에서 upsert하며 사람이 편집하지 않는다. 불일치 시 온보딩이 실패해야 함. **`[2026-07-27]` 조직/콘솔 권한 코드 5개 추가** — `org.admin`, `org.manage.people`, `org.manage.roles`, `org.read.ops`, `org.read.onboarding`(`60-identity-authn.md` §20.4). 이 5개의 §8.1 4층 투영은 전부 **no-op**이며(`exec.physical` 행의 기존 형태), `osiris-api`가 소비한다. **추가와 함께 `catalogVersion`을 올리고 grant-catalog도 같은 버전으로 올려야 I9가 유지된다** |
| 4 | `[:REQUIRES_PERMISSION]` | 역량→권한 다리 (§7.2) | 카탈로그와 함께 코드에서. **조직/콘솔 코드 5개는 어떤 `:Capability`의 대상도 아니다** — 따라서 I7(무용지물 역량 경고)에 무영향 |
| 5 | 기본 권한 프로파일 | `:PermissionProfile` 3종 (`readonly` / `builder` / `finance`) + `[:GRANTS]` **+ `[2026-07-27]` 조직 역할 프로파일 3종** (`pp-org-admin` / `pp-org-manager` / `pp-org-member`) | 시스템 기본. 사용자는 복제해서 변형. 조직 역할 프로파일의 GRANTS 구성은 `60-identity-authn.md` §20.5 |
| 6 | 리소스 프로파일 | `:RoleResourceProfile` (`standard`, `heavy`, `light`) | 좌석 환산의 근거 |
| 7 | **팀** | `:Team:Party` ×4 (alpha / 예시 앱 / 경영회계 / 마케팅) + `[:PART_OF]->(:Organization)` | 콘솔 온보딩 ① 단계 |
| 8 | **직무** | `:Role{scope:'job'}` + `[:REQUIRES_CAPABILITY]` + `[:HAS_PROFILE{kind:'base'}]` **+ `[2026-07-27]` 조직 역할 `:Role{scope:'org'}` 3종** (`role-org-admin` / `role-org-manager` / `role-org-member`) | 콘솔 온보딩 ②. 조직 역할 3종은 **시스템 시드**이며 §2.4(31 문서)의 "프레임워크 기본값"과 같은 성격 — 조직이 이름을 바꾸거나 역할을 더 정의할 수 있고, **인가 판정은 이름이 아니라 권한 코드로만 한다**(`60-identity-authn.md` H30) |
| 9 | **사람 직원** | `:Person:Party` + `[:HAS_CAPABILITY]` | `cap.exec.physical`은 여기서만 부여 가능 |
| 10 | **AI 에이전트** | `:AIAgent:Party` + `[:HAS_CAPABILITY]` + `[:USES_PROFILE]->(:RoleResourceProfile)` | `isMaster` 는 정확히 1개 |
| 11 | **멤버십** | `:Membership` — **팀 소속**(Party × Team × Role, 팀장은 `isHead:true`) **+ `[2026-07-27]` 조직 소속**(`:Person` × Organization × `:Role{scope:'org'}`, `IN_ORG`) | 콘솔 온보딩 ③. **최초 커밋에는 `org.admin`을 갖는 조직 멤버십이 최소 1건 있어야 한다**(I14) — 없으면 조직은 생겼는데 아무도 관리자가 아닌 상태로 커밋된다 |
| 12 | 배정 정책 | `:AssignmentPolicy{version:1}` | 콘솔 온보딩 ④(승인정책)와 함께 |
| 13 | **검증** | §9.3의 불변식 전수 검사 (읽기 전용) | 오류 존재 시 트랜잭션 **롤백**하고 중단 |
| 14 | **리허설** | 더미 `:WorkItem`으로 §8.2의 `assign()`을 **그대로** 호출해 `outcome == ASSIGNED` 확인 → **같은 미커밋 트랜잭션 안에서 더미 전부 삭제** | 배정 경로 전체의 스모크 테스트. 별도 시뮬레이션 함수를 만들지 않는다(로직이 두 벌이 되면 실제 배정과 어긋난다) |
| 15 | **커밋** | `:OrgRevision` +1, `[:PREV]` 연결 → **COMMIT** → 그 후 `org.revision` 이벤트 발행 | **유일한 durable 순간.** 여기서 처음으로 수렴 루프가 컨테이너/Pod를 만든다 |

**1~15가 전부 하나의 Neo4j 트랜잭션이며, 커밋은 15에서 단 한 번 일어난다.** 검증(13)이나 리허설(14)이 실패하면 트랜잭션째 롤백되므로 **그래프에 아무것도 남지 않는다** — 반쯤 정의된 조직으로 에이전트가 프로비저닝되는 일도, 실패한 리허설의 흔적이 남는 일도 없다.

> **⚠️ 이 순서는 초판(13 검증 → 14 커밋 → 15 리허설)에서 정정된 것이다.** 초판대로라면 리허설이 **커밋 이후**에 실행되어, 이미 durable해진 리비전 위에서 `:AssignmentDecision`을 만들고 그것을 "롤백"(= 삭제)해야 한다. 이는 §11 제약 **C5(`:AssignmentDecision`과 `[:CONSIDERED]`는 append-only)** 를 정면으로 위반하며, "삭제하지 않는다"는 원칙 P4에서 배정 결정만 유일한 예외가 되는 결과를 낳는다.
>
> **정정 방식으로 "C5에 리허설 예외를 두는 것"이 아니라 "순서를 고치는 것"을 택했다.** 이유: 예외를 두면 "커밋된 `:AssignmentDecision` 중 어떤 것은 지워질 수 있다"가 되어, 배정 근거의 감사 가치(콘솔 최우선 요구)가 조건부가 된다. 반면 순서를 고치면 **리허설 산출물은 애초에 커밋된 적이 없으므로 C5의 적용 대상 자체가 아니다** — 예외 없이 불변식이 유지된다.
>
> 이 정정은 `30-onboarding-provisioning.md` §5.2의 재해석(“커밋 이전, 같은 트랜잭션 안”)과 **일치하며**, 그 문서의 열린 질문 1과 §9 G2를 해소한다. 온보딩 문서 쪽 변경은 필요 없다.

### 9.3 검증 불변식 (리허설이 검사할 것)

| # | 불변식 | 위반 시 |
|---|---|---|
| I1 | 모든 `:Team`에 `isHead:true` 멤버십이 **정확히 1개** | 오류 — 진행 불가 |
| I2 | 모든 `:AIAgent`에 유효한 `:Membership`이 **정확히 1개** | 오류 |
| I3 | `isMaster:true`인 `:AIAgent`가 **정확히 1개** | 오류 |
| I4 | 모든 `:Role`의 `REQUIRES_CAPABILITY` 대상이 카탈로그에 존재 | 오류 |
| I5 | `cap.exec.physical`을 보유한 `:Party`는 전부 `:Person` | **오류 — 하드 제약** |
| I6 | 어떤 `:Role`의 요구 역량도 조직 내 레벨 3 이상 보유자가 0명이 아님 | 경고 (`CAPABILITY_GAP`) |
| I7 | 모든 `:AIAgent`의 유효 권한 ⊇ (보유 역량의 `REQUIRES_PERMISSION`) | 경고 (`INERT_CAPABILITY` — 무용지물 역량) |
| I8 | `:Capability`의 `BROADER` 그래프에 사이클 없음 | 오류 |
| I9 | `:Permission.catalogVersion`이 오케스트레이터 grant-catalog와 일치 | 오류 |
| I10 | 모든 `:AIAgent`의 `modelAlias`가 허용된 3개 중 하나 | 오류 |
| I11 | 물리 실행이 필요한 `:Role`이 하나라도 있으면 그 팀에 사람 멤버가 1명 이상 | 경고 |
| I12 | `executionMode:'hybrid'`인 리프 `:WorkItem` 없음 | 오류 |
| — | *(**I13**은 이 목록의 번호가 아니다 — `30-onboarding-provisioning.md` §6.3이 **온보딩 전용 경고**로 선점해 사용 중이다. 아래 신규 번호는 그것을 피해 **I14부터** 배정했다. `60-identity-authn.md` §17 F5의 해소)* | |
| **I14** `[2026-07-27 신설]` | **`org.admin`을 유효 권한으로 갖는 `closedAtRevision IS NULL`인 `:Person`이 1명 이상** — 최초 커밋을 포함한 **모든** 커밋에 적용 | **오류 — 조직 잠금 방지** |
| **I15** `[2026-07-27 신설]` | `IN_ORG` 멤버십을 소유한 `:Party`는 전부 `:Person` (`:AIAgent`·`:Team`·`:Organization` 불가) | 오류 |
| **I16** `[2026-07-27 신설]` | 한 `:Person`에 `validTo IS NULL`인 `IN_ORG` 멤버십은 **최대 1개** | 오류 |
| **I17** `[2026-07-27 신설]` | `:Role{scope:'org'}`는 `REQUIRES_CAPABILITY`가 0개이고 `defaultTeamId`·`isLead`가 null. 역으로 `IN_ORG` 멤버십의 `AS_ROLE` 대상은 반드시 `scope:'org'`, `IN_TEAM` 멤버십의 대상은 반드시 `scope:'job'` | 오류 |

`GET /onboarding/status`(콘솔 요구)는 이 검사 결과를 단계별로 집계해서 반환한다.

> **`[2026-07-27]` I14~I17은 최초 온보딩 커밋뿐 아니라 조직 역할 변경 트랜잭션에서도 검사된다.** 역할 부여·회수·`:Person` 비활성화는 `:OrgRevision`을 올리는 의도 변경이므로(C8) 커밋 전에 이 넷을 다시 통과해야 한다 — 다만 전체 리허설(§9.2 단계 14의 더미 배정)까지 돌리지는 않는다. 조직 역할은 배정 경로에 닿지 않기 때문이다(§8.3의 `IN_TEAM` 제한). 상세는 `60-identity-authn.md` §20.6/§21.3.
>
> **I14는 `60-identity-authn.md`의 위임 규칙 DG4와 짝을 이루는 절반이다.** 나머지 절반("관리자 중 최소 1명이 실제로 로그인 가능한 자격증명을 보유")은 **이 스키마가 검사할 수 없다** — 자격증명은 그래프 밖에 있고 그래프는 그 저장소의 존재를 알지 못하기 때문이다(H1의 단방향 참조). 그 조건은 `osiris-api` 계층의 사전조건으로 강제된다. 이 분담이 의도적임을 여기 기록한다.

---

## 10. 대표 쿼리 (Cypher)

### Q1. 이 업무를 맡을 수 있는 후보 찾기 (배정 하드 필터 + 점수 재료)

```cypher
MATCH (w:WorkItem {id: $taskId})-[req:REQUIRES_CAPABILITY]->(rc:Capability)
WITH w, collect({code: rc.code, node: rc, minLevel: req.minLevel}) AS reqs

MATCH (p:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(t:Team)
WHERE m.validTo IS NULL
  AND p.closedAtRevision IS NULL
  AND (p:Person OR p:AIAgent)
OPTIONAL MATCH (m)-[:AS_ROLE]->(role:Role)
OPTIONAL MATCH (p)-[:RUNTIME]->(rt:AgentRuntime)

CALL {
  WITH p, reqs
  UNWIND reqs AS r
  OPTIONAL MATCH (p)-[h:HAS_CAPABILITY]->(hc:Capability)-[:BROADER*0..3]->(r.node)
  WITH r,
       coalesce(max(h.level), 0)                                   AS lvl,
       max(CASE WHEN hc = r.node THEN 1 ELSE 0 END)                AS exact
  RETURN
    sum(CASE WHEN lvl >= r.minLevel THEN 1 ELSE 0 END)             AS metCount,
    sum(CASE WHEN lvl >= r.minLevel THEN (1 + coalesce(exact,0))
             ELSE 0 END)                                           AS specificity,
    collect(CASE WHEN lvl < r.minLevel THEN r.code END)            AS missing
}
WITH w, p, t, role, rt, reqs, metCount, specificity,
     [x IN missing WHERE x IS NOT NULL] AS missingCaps

RETURN p.id                                        AS partyId,
       p.name                                      AS name,
       head([l IN labels(p) WHERE l IN ['Person','AIAgent']]) AS kind,
       t.id                                        AS teamId,
       role.title                                  AS role,
       metCount = size(reqs)                       AS capabilityOk,
       specificity                                 AS specificityScore,
       missingCaps                                 AS missingCapabilities,
       coalesce(rt.ready, p.availability = 'available')          AS ready,
       coalesce(rt.phase, p.availability)          AS phase
ORDER BY capabilityOk DESC, specificityScore DESC, partyId ASC;
```
*(용량·권한·스코프 필터와 최종 tie-break는 §8의 코드에서 적용한다 — 런타임 부하와 유효 권한 계산이 그래프 밖 상태를 섞기 때문.)*

### Q2. 이 배정이 왜 이 사람에게 갔는지 재현 (콘솔 ⓘ 팝오버)

```cypher
MATCH (w:WorkItem {id: $taskId})<-[:FOR]-(d:AssignmentDecision)
WITH d ORDER BY d.decidedAt DESC LIMIT 1
OPTIONAL MATCH (d)-[:SELECTED]->(sel:Party)
OPTIONAL MATCH (d)<-[:OVERRIDES]-(ov:AssignmentOverride)-[:TO]->(ovTo:Party)
OPTIONAL MATCH (ov)-[:BY]->(ovBy:Party)
MATCH (d)-[con:CONSIDERED]->(cand:Party)
RETURN d.decidedAt          AS decidedAt,
       d.orgRevision        AS orgRevision,
       d.policyVersion      AS policyVersion,
       d.algorithmVersion   AS algorithmVersion,
       d.outcome            AS outcome,
       d.requiredCapsJson   AS requiredCapabilities,
       d.tieBreakChainJson  AS tieBreakChain,
       { id: sel.id, name: sel.name }                             AS ruleSelected,
       CASE WHEN ov IS NULL THEN NULL ELSE
         { at: ov.at, reason: ov.reason,
           to: { id: ovTo.id, name: ovTo.name },
           by: { id: ovBy.id, name: ovBy.name } } END              AS override,
       collect({
         partyId:   cand.id,
         name:      con.displayNameAtDecision,     // ★ 당시 이름 (개명·퇴사에도 안전)
         kind:      con.partyKindAtDecision,
         rank:      con.rank,
         verdict:   con.verdict,
         excludedBecause: con.exclusionReasons,
         matched:   con.matchedCapabilities,
         missing:   con.missingCapabilities,
         specificity: con.specificityScore,
         load:      con.loadAtDecision,
         sameTeam:  con.sameTeam
       })                                                          AS candidates;
```

이 쿼리는 **`:Capability` 노드를 전혀 참조하지 않는다.** 그래서 조직이 바뀌어도, 역량 카탈로그가 개정되어도, 그 사람이 퇴사해도 과거 배정의 "왜"가 그대로 재현된다. **P4가 실제로 작동하는 지점.**

### Q3. 조직도 전체 (사람 + AI 혼합) — `GET /orgs/tree`

```cypher
MATCH (o:Organization {id: $orgId})
MATCH (t:Team)-[:PART_OF*1..3]->(o)
OPTIONAL MATCH (t)<-[:IN_TEAM]-(m:Membership)<-[:HAS_MEMBERSHIP]-(p:Party)
  WHERE m.validTo IS NULL AND p.closedAtRevision IS NULL
OPTIONAL MATCH (m)-[:AS_ROLE]->(r:Role)
OPTIONAL MATCH (p)-[:RUNTIME]->(rt:AgentRuntime)
OPTIONAL MATCH (a:Assignment)-[:TO]->(p)
  WHERE a.status IN ['active','awaiting_human']
WITH t, p, r, m, rt, count(a) AS activeTasks
RETURN t.id AS teamId, t.name AS teamName,
       collect(CASE WHEN p IS NULL THEN NULL ELSE {
         partyId: p.id, name: p.name,
         kind:    head([l IN labels(p) WHERE l IN ['Person','AIAgent']]),
         role:    r.title, isHead: m.isHead,
         status:  CASE
                    WHEN p:Person AND activeTasks > 0 AND p.availability='available'
                         THEN 'awaiting_human'
                    WHEN p:Person  THEN p.availability
                    WHEN rt IS NULL THEN 'unknown'
                    WHEN rt.observedAt < datetime() - duration('PT60S') THEN 'stale'
                    WHEN rt.phase = 'Working' THEN 'busy'
                    WHEN rt.ready THEN 'idle'
                    ELSE 'error' END,
         activeTasks: activeTasks
       } END) AS members
ORDER BY teamId;
```
`stale` 처리에 주목: **캐시가 오래되면 "정상"이라고 말하지 않는다**(P2).

> **`[2026-07-27]` 이 쿼리는 `IN_TEAM` 멤버십만 순회하므로 무변경이며, 그 결과 한 가지 표시 공백이 생긴다**: **팀에 속하지 않고 조직 역할(`IN_ORG`)만 가진 사람**(예: 어느 팀에도 소속되지 않은 대표)은 조직도에 나타나지 않는다. 이는 **의도된 무변경**이다 — Q3는 "팀 단위 조직도"이고, 여기에 조직 직속 멤버를 끼워 넣으려면 트리의 형태(팀 → 멤버)를 바꿔야 해서 콘솔 Org 화면(#2)의 렌더링 계약까지 함께 움직인다. 실무적으로 대표는 보통 팀 멤버십도 함께 갖는다. 조직 직속 멤버를 조직도 최상단에 별도 그룹으로 보여줄 필요가 생기면 그때 `OPTIONAL MATCH (o)<-[:IN_ORG]-(m2:Membership)<-[:HAS_MEMBERSHIP]-(p2:Person)` 한 줄을 더해 `orgDirect` 배열로 반환하면 된다 — **지금 만들지 않는다**(Simplicity First). 조직 역할 자체는 화면 #10(People & Roles)과 `GET /me`의 `orgRole`이 이미 표면화한다.

### Q4. WBS 진척 롤업

```cypher
MATCH (root:WorkItem {id: $rootId})
MATCH (root)-[:PARENT_OF*0..6]->(n:WorkItem)
WHERE NOT (n)-[:PARENT_OF]->(:WorkItem)          // 리프만
  AND n.status <> 'cancelled'
WITH root,
     sum(n.effortPoints)                                                  AS total,
     sum(n.effortPoints * n.progressPct)                                  AS weighted,
     sum(CASE WHEN n.status='blocked' THEN 1 ELSE 0 END)                  AS blocked,
     sum(CASE WHEN n.status='awaiting_human' THEN 1 ELSE 0 END)           AS awaitingHuman,
     sum(CASE WHEN n.status='awaiting_approval' THEN 1 ELSE 0 END)        AS awaitingApproval
RETURN root.id, root.title,
       CASE WHEN total = 0 THEN 0 ELSE toInteger(weighted / total) END AS progressPct,
       total AS totalPoints, blocked, awaitingHuman, awaitingApproval;
```

### Q5. 역량 갭 — 조직에 없는 역량 때문에 막힌 업무 (온보딩 트리거)

```cypher
MATCH (w:WorkItem)-[req:REQUIRES_CAPABILITY]->(c:Capability)
WHERE w.status IN ['planned','blocked']
OPTIONAL MATCH (p:Party)-[h:HAS_CAPABILITY]->(hc:Capability)-[:BROADER*0..3]->(c)
  WHERE h.level >= req.minLevel
    AND EXISTS { (p)-[:HAS_MEMBERSHIP]->(m:Membership) WHERE m.validTo IS NULL }
WITH c, req.minLevel AS needLevel,
     count(DISTINCT p) AS holders,
     collect(DISTINCT w.id)[0..5] AS sampleBlockedTasks,
     count(DISTINCT w) AS blockedCount
WHERE holders = 0
RETURN c.code AS capability, c.label, needLevel, blockedCount, sampleBlockedTasks
ORDER BY blockedCount DESC;
```

### Q6. 권한 감사 — 누가 `git.commit`을 행사할 수 있는가 (경로 포함)

```cypher
MATCH (perm:Permission {code: $permCode})
MATCH (p:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:AS_ROLE]->(r:Role)
WHERE m.validTo IS NULL
OPTIONAL MATCH (r)-[:HAS_PROFILE {kind:'base'}]->(bp:PermissionProfile)-[:GRANTS]->(perm)
OPTIONAL MATCH (p)-[:HAS_PROFILE {kind:'additive'}]->(ap:PermissionProfile)-[:GRANTS]->(perm)
WITH p, r, perm,
     collect(DISTINCT bp.name) + collect(DISTINCT ap.name) AS grantedVia
WHERE size(grantedVia) > 0
  AND NOT EXISTS { (p)-[:DENIES]->(perm) }
RETURN p.id, p.name,
       head([l IN labels(p) WHERE l IN ['Person','AIAgent']]) AS kind,
       r.title AS role, grantedVia, perm.gate AS gate, perm.approverRole
ORDER BY kind, p.id;
```

### Q7. 좌석 환산 — 이 직무의 에이전트를 몇 명 더 앉힐 수 있는가

```cypher
MATCH (rp:RoleResourceProfile {id: $runtimeClass})
MATCH (n:ClusterNode) WHERE n.status <> 'offline'
WITH rp,
     sum(n.allocatableCpuMilli - n.usedCpuMilli) AS freeCpu,
     sum(n.allocatableMemMiB  - n.usedMemMiB)    AS freeMem,
     collect({node: n.name,
              seats: apoc.coll.min([                       // apoc 없으면 코드에서 min
                (n.allocatableCpuMilli - n.usedCpuMilli) / rp.cpuMilli,
                (n.allocatableMemMiB  - n.usedMemMiB)  / rp.memMiB ])}) AS perNode
RETURN rp.label AS roleLabel,
       CASE WHEN freeCpu / rp.cpuMilli < freeMem / rp.memMiB
            THEN freeCpu / rp.cpuMilli ELSE freeMem / rp.memMiB END AS totalSeats,
       freeCpu, freeMem, perNode;
```
> 좌석은 **노드별로 계산해 합산**해야 정확하다(파편화). 위 쿼리는 총합과 노드별을 모두 반환하며, `osiris-api`는 노드별 최소값 합을 보수적 값으로 쓴다. `[미검증]` apoc 설치 여부 — 없으면 노드별 계산을 코드에서 수행한다.

### Q8. 특정 시점(리비전) 기준 조직 재현

```cypher
MATCH (p:Party)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(t:Team)
MATCH (m)-[:AS_ROLE]->(r:Role)
WHERE m.createdAtRevision <= $rev
  AND (m.closedAtRevision IS NULL OR m.closedAtRevision > $rev)
  AND p.createdAtRevision <= $rev
  AND (p.closedAtRevision IS NULL OR p.closedAtRevision > $rev)
RETURN t.name AS team, r.title AS role, p.name AS member, m.isHead
ORDER BY team, m.isHead DESC, member;
```
`:AssignmentDecision.orgRevision`과 결합하면 **"그때 조직이 이랬고, 그래서 이 배정이 나왔다"**를 완전히 복원할 수 있다.

### Q9. 물리 업무 체크리스트 진행 (콘솔 Work 보드 리프 상세)

```cypher
MATCH (parent:WorkItem {id: $itemId})-[:PARENT_OF]->(item:WorkItem)
WHERE item.kind = 'checklistItem'
OPTIONAL MATCH (item)-[:ATTESTED_BY]->(e:Evidence)
OPTIONAL MATCH (a:Assignment)-[:OF_TASK]->(item), (a)-[:TO]->(who:Party)
RETURN item.id, item.title, item.status, item.executionMode,
       who.name AS assignee,
       collect({kind: e.kind, uri: e.uri, at: e.submittedAt}) AS evidence
ORDER BY item.id;
```

### Q10. 에이전트 1명의 전체 컨텍스트 (Org 상세 패널)

```cypher
MATCH (p:Party {id: $partyId})
OPTIONAL MATCH (p)-[:HAS_MEMBERSHIP]->(m:Membership)-[:IN_TEAM]->(t:Team)
  WHERE m.validTo IS NULL
OPTIONAL MATCH (m)-[:AS_ROLE]->(r:Role)
OPTIONAL MATCH (p)-[h:HAS_CAPABILITY]->(c:Capability)
OPTIONAL MATCH (a:Assignment)-[:TO]->(p) WHERE a.status = 'active'
OPTIONAL MATCH (a)-[:OF_TASK]->(w:WorkItem)
OPTIONAL MATCH (p)-[:RUNTIME]->(rt:AgentRuntime)
RETURN p.name, labels(p), t.name AS team, r.title AS role,
       collect(DISTINCT {code: c.code, label: c.label,
                         level: h.level, source: h.source}) AS capabilities,
       collect(DISTINCT {taskId: w.id, title: w.title,
                         progress: w.progressPct, due: w.dueAt}) AS activeWork,
       rt {.phase, .ready, .sessionId, .restartCount24h, .observedAt} AS runtime;
```

---

## 11. 제약(constraint)과 인덱스

```cypher
// --- 유일성 ---
CREATE CONSTRAINT party_id       IF NOT EXISTS FOR (n:Party)              REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT role_id        IF NOT EXISTS FOR (n:Role)               REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT membership_id  IF NOT EXISTS FOR (n:Membership)         REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT cap_code       IF NOT EXISTS FOR (n:Capability)         REQUIRE n.code IS UNIQUE;
CREATE CONSTRAINT perm_code      IF NOT EXISTS FOR (n:Permission)         REQUIRE n.code IS UNIQUE;
CREATE CONSTRAINT profile_id     IF NOT EXISTS FOR (n:PermissionProfile)  REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT workitem_id    IF NOT EXISTS FOR (n:WorkItem)           REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT assignment_id  IF NOT EXISTS FOR (n:Assignment)         REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT decision_id    IF NOT EXISTS FOR (n:AssignmentDecision) REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT approval_id    IF NOT EXISTS FOR (n:Approval)           REQUIRE n.id IS UNIQUE;
CREATE CONSTRAINT revision_n     IF NOT EXISTS FOR (n:OrgRevision)        REQUIRE n.n  IS UNIQUE;
CREATE CONSTRAINT runtime_emp    IF NOT EXISTS FOR (n:AgentRuntime)       REQUIRE n.employeeId IS UNIQUE;
CREATE CONSTRAINT node_name      IF NOT EXISTS FOR (n:ClusterNode)        REQUIRE n.name IS UNIQUE;
CREATE CONSTRAINT rrp_id         IF NOT EXISTS FOR (n:RoleResourceProfile) REQUIRE n.id IS UNIQUE;
// [2026-07-27 신설] :Person.email 은 §3.1이 "콘솔 로그인 식별자"로 규정한 값이다.
// 유일성이 강제되지 않으면 "이 이메일로 로그인하는 사람이 누구인가"가 비결정적이 된다
// (60-identity-authn.md H6/OD1). 순수 추가 — 기존 제약 어느 것도 약화되지 않는다.
CREATE CONSTRAINT person_email   IF NOT EXISTS FOR (n:Person)             REQUIRE n.email IS UNIQUE;

// --- 존재성 (CE에서는 미지원 → 애플리케이션 검증. 표기만 남긴다) ---
// :Party.name, :WorkItem.status, :AgentRuntime.observedAt 는 NOT NULL 이어야 한다.
// [미검증] Neo4j Community Edition은 property existence constraint를 지원하지 않는다.
//         graph-write API가 스키마 검증을 대신한다(D17이 이 검증의 단일 지점인 이유).

// --- 인덱스 ---
CREATE INDEX party_kind      IF NOT EXISTS FOR (n:Party)      ON (n.closedAtRevision);
CREATE INDEX member_validity IF NOT EXISTS FOR (n:Membership) ON (n.validTo);
CREATE INDEX member_rev      IF NOT EXISTS FOR (n:Membership) ON (n.createdAtRevision, n.closedAtRevision);
CREATE INDEX work_status     IF NOT EXISTS FOR (n:WorkItem)   ON (n.status);
CREATE INDEX work_due        IF NOT EXISTS FOR (n:WorkItem)   ON (n.dueAt);
CREATE INDEX assign_status   IF NOT EXISTS FOR (n:Assignment) ON (n.status);
CREATE INDEX approval_status IF NOT EXISTS FOR (n:Approval)   ON (n.status, n.requestedAt);
CREATE INDEX decision_time   IF NOT EXISTS FOR (n:AssignmentDecision) ON (n.decidedAt);
CREATE INDEX notif_unread    IF NOT EXISTS FOR (n:Notification) ON (n.readAt, n.createdAt);
CREATE FULLTEXT INDEX ontology_search IF NOT EXISTS
  FOR (n:Party|WorkItem|Capability|Role|Asset) ON EACH [n.name, n.title, n.label, n.code];
```

**애플리케이션이 대신 강제하는 제약** (Neo4j CE의 한계로 DB에서 못 거는 것 — graph-write API의 책임):

| # | 제약 |
|---|---|
| C1 | `:Party`는 반드시 `:Person`/`:AIAgent`/`:Organization`/`:Team` 중 하나를 공동 라벨로 가진다 |
| C2 | `cap.exec.physical`의 `HAS_CAPABILITY` 출발점은 `:Person`만 (I5) |
| C3 | `:Capability`의 `BROADER` 그래프에 사이클 없음 (I8) |
| C4 | 한 `:Team`에 `isHead:true`이고 `validTo IS NULL`인 `:Membership`은 1개 (I1) |
| C5 | `:AssignmentDecision`과 그 `[:CONSIDERED]` 엣지는 **생성 후 수정·삭제 금지** (append-only). **예외 없음.** 온보딩 리허설(§9.2 단계 14)의 더미 결정은 커밋되지 않는 트랜잭션 안에서만 존재하므로 이 제약의 대상이 아니다 — "예외"가 아니라 "애초에 커밋된 적 없음"이다 |
| C6 | 리프 `:WorkItem`의 `executionMode`는 `hybrid`가 아니다 (I12) |
| C7 | `:AgentRuntime`의 모든 쓰기는 `observedAt`을 함께 갱신한다 |
| C8 | 모든 의도 변경 트랜잭션은 `:OrgRevision`을 정확히 1 증가시킨다 |
| **C9** `[2026-07-27 신설]` | `IN_ORG` 멤버십을 소유한 `:Party`는 `:Person`만이며, 한 `:Membership`은 `IN_TEAM`과 `IN_ORG` 중 **정확히 하나**를 갖는다 (I15와 §3.2) |
| **C10** `[2026-07-27 신설]` | **조직 역할 멤버십의 생성·종료, `:Person` 비활성화, `HAS_PROFILE`/`DENIES` 조작, 등록 티켓 발급/재발급은 `60-identity-authn.md` §21의 위임 규칙 DG1~DG5를 전부 통과해야 한다.** `osiris-api`가 판정하고 graph-write API가 그래프 측 사전조건(I14~I17)을 재검사한다 — **판정 로직 자체는 한 곳(`osiris-api`)에만 존재하며**(H31), core가 갖는 것은 판정의 복제가 아니라 **불변식 검사**다 |

> **`[2026-07-27]` C9·C10은 순수 추가이며 C1~C8 어느 것도 약화하지 않는다.** 특히 **C1**(`:Party`는 4개 하위 라벨 중 하나)은 **무변경**이다 — 이 확장은 새 `:Party` 하위 라벨을 만들지 않았다. `60-identity-authn.md` §19가 도입한 검증 전용 기계 주체는 `:Party`가 **아니며** 그래프에 노드를 갖지 않는다(다섯 번째 하위 라벨을 만드는 안을 검토했으나 C1의 변경에 해당하므로 기각했다 — 60 §19.2).

---

## 12. 이 스키마가 지원하지 않는 것 (의도적 배제)

| 배제 | 이유 | 필요해지면 |
|---|---|---|
| SPARQL / RDF 트리플 저장 | Neo4j property graph를 택한 시점의 필연. `iri` 속성만 보존 | `iri` 기반 export 잡을 추가 |
| OWL 추론 | 추론기 없음. 필요한 상위 개념은 공동 라벨로 머티리얼라이즈 | 규칙이 늘면 머티리얼라이즈 잡을 추가 |
| 멀티테넌시 (조직 여러 개) | 대표 1인 + 소수 직원 전제 | `:Organization` 기준 파티셔닝 + 쿼리에 `orgId` 필터 |
| 역량 자동 추론(실적 기반 레벨 상향) | 초기에 신뢰할 데이터가 없다. `source:'inferred'` 필드만 미리 열어둠 | 배정·완료 이력이 수백 건 쌓인 뒤 |
| 고빈도 시계열(비용·로그·하트비트) | P6. JetStream이 담당 | 없음 |
| 조직 시뮬레이션("이 사람을 뽑으면 처리량이 얼마나?") | 요청되지 않음 | Q5(역량 갭)와 Q7(좌석)의 조합으로 근사 가능 |
