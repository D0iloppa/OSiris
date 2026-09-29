# 구현 노트 — 1단계 플랫폼 계층

> 이 문서는 `docs/design/10-core-architecture.md`를 코드로 옮기며 실제로 내린
> 판단·설계와의 차이를 기록한다. 설계를 임의로 바꾸지 않았고, 구현이 설계와
> 다르거나 설계가 명시하지 않은 세부를 정한 지점만 여기 남긴다.

## 1. 범위 — 이번 작업이 실제로 구현한 것

`osiris-gw`, `neo4j`, `nats`, `osiris-egress` 4개 서비스와 부트스트랩/검증 스크립트.
`osiris-api` / `osiris-core` / `osiris-orchestrator` / 에이전트 런타임 / 콘솔 SPA /
배정 엔진 / `osiris-watchdog`(호스트 systemd)은 **의도적으로 구현하지 않았다** —
브리핑이 명시한 제외 목록이거나("애플리케이션 로직은 다음 워커"), §4.1의 나머지
스텝(카탈로그 시드, 관리자 토큰 발급, watchdog 설치)이 그 컴포넌트들의 존재를
전제하기 때문이다.

## 2. 설계와 일치하되 구현 시 확정한 세부 (되묻지 않고 정한 것)

| # | 세부 | 결정 | 근거 |
|---|---|---|---|
| 1 | 이미지 태그 | `nginx:1.27-alpine`, `neo4j:5.24-community`, `nats:2.10-alpine`, `ubuntu/squid:6.6-24.04_edge@sha256:8a3baed…` | 전부 부동 태그(`latest`) 금지 원칙에 따라 명시 고정. squid는 Canonical의 `ubuntu/squid`가 고정 번호 릴리스 태그를 안 내놓고 `_edge`/`_beta` 채널만 제공하므로, 태그+콘텐츠 다이제스트를 함께 박아 태그보다 더 강하게 고정했다 |
| 2 | egress 허용목록에 `files.pythonhosted.org`, `sum.golang.org`, `.githubusercontent.com` 추가 | grant-catalog 표(§8.1)에 명시된 `pypi.org`/`proxy.golang.org`/`github.com`만으로는 실제 `pip install`/`go get`/많은 `git` 워크플로가 동작하지 않는다(각각의 실제 다운로드·릴리스 호스트가 다름) | 설계 의도(패키지 설치가 실제로 되는 것)를 살리기 위한 최소 확장. 넓히는 방향의 판단이므로 다음 워커가 grant-catalog Go 맵을 만들 때 재검토 필요 |
| 3 | Neo4j/NATS 호스트 포트 미publish | 관리 접근은 `docker exec <container> cypher-shell` / `docker exec <container> nats-server ...`로 제한 | 브리핑이 "새 호스트 포트 바인딩 금지"를 `osiris-gw`에 한해 명시했지만, 같은 원칙(불필요한 노출 최소화)을 나머지 플랫폼 서비스에도 적용했다. 개발 편의(Neo4j Browser 등)가 필요해지면 그때 명시적으로 포트를 열 것 — 지금 선제적으로 열지 않는다 |
| 4 | 컨테이너 이름 vs 네트워크 별칭 분리 | 컨테이너명은 전부 `osiris-` 접두사(`osiris-neo4j`, `osiris-nats`)로 호스트 충돌을 피하되, `docker-compose.yml`의 `networks.<net>.aliases`로 설계가 못박은 bare 호스트명(`neo4j`, `nats`)도 함께 제공 | §1.2 불변자산 10번("에이전트가 아는 호스트명 3개: nats, osiris-core, osiris-egress")과, 브리핑의 "컨테이너 이름은 전부 osiris- 접두사" 요구가 충돌한다. 별칭으로 양쪽을 동시에 만족시켰다 — `docker ps`에는 `osiris-` 접두사가 보이고, DNS 조회는 설계 문서 그대로의 이름으로도 된다. `osiris-gw`/`osiris-egress`/`osiris-core`는 애초에 설계의 리터럴 이름 자체가 `osiris-` 접두사라 별칭이 필요 없다 |
| 5 | 내부 네트워크 생성 방식 | §4.1은 "1. 내부 네트워크 생성"을 `docker compose up -d`(3번)과 별개의 `docker network create` 스텝으로 적었지만, 실제로는 `docker-compose.yml`의 `networks:` 섹션에 `internal: true` + 고정 subnet으로 선언해 `docker compose up`이 직접 멱등하게 생성/재사용하게 했다 | 결과(고정 subnet의 internal 네트워크 3개)는 동일하고, 별도 프리스텝을 두면 "compose 파일의 선언"과 "사전에 만든 네트워크"가 어긋날 경우 재현성이 깨지는 리스크만 늘어난다 |
| 6 | osiris-gw의 `default_server` Host 차단 | `server_name osiris.example.com`가 아닌 요청은 osiris-gw 자신도 444로 닫는다(내부 `/healthz`만 예외) | §2.4가 "osiris-api가 Host 헤더를 검증해 거부한다"고 명시했는데, osiris-api가 아직 없으므로 그 방어가 당장은 없다. 같은 강제 층(nginx)에서 한 겹 더 두는 것으로, 두 층 모두 "강제" 성질이라 상충하지 않는다 |
| 7 | 플레이스홀더 상태 페이지의 생성 방식 | 브라우저가 매 요청마다 각 서비스 헬스를 실시간 조회하는 대신, 호스트에서 도는 `gw/render-status.sh`가 `docker inspect`로 스냅샷을 찍어 정적 HTML을 굽는다 | `osiris-gw`는 DMZ 패턴(§2.6)에 따라 `osiris-data`/`osiris-agents`에 붙어 있지 않다 — 즉 컨테이너 내부에서 Neo4j/NATS에 직접 접근할 경로가 없다. 그 네트워크를 넓히면 격리 요구를 어기게 되므로, "정적 페이지" 요구사항을 문자 그대로 지키면서 실제 데이터를 보여주는 유일한 방법은 호스트 쪽에서 스냅샷을 굽는 것이다 |

## 3. 알려진 격차 (구현하지 않았고, 다음 워커가 알아야 하는 것)

| # | 격차 | 이유 | 언제 닫아야 하는가 |
|---|---|---|---|
| G-1 | NATS에 인증/ACL이 전혀 없다(`nats/nats.conf`에 명시적으로 기록) | §7.3/D13/D14가 요구하는 팀 계정 하위 NATS 사용자(nkey/JWT)는 `osiris-orchestrator`가 발급하는데, 그 컴포넌트가 아직 없다 | `osiris-orchestrator` 구현 시 최우선으로 `accounts {}` 블록을 추가할 것. 지금은 에이전트가 0명이라 실손실은 없다 |
| G-2 | Neo4j 카탈로그 시드(Capability/Permission/PermissionProfile/AssignmentPolicy/`OrgRevision=0`) 없음. `neo4j/apply-schema.sh`는 제약/인덱스(DDL)만 적용 | 카탈로그는 조직 의도 데이터이고, D17/G7(Neo4j 쓰기는 graph-write API 단일 관문)에 따라 그 API가 없는 지금 raw Cypher로 시드하면 "여러 컴포넌트가 직접 쓰기"를 저지르게 된다 | `osiris-core`의 graph-write API가 서면, `20-ontology-schema.md` §9의 시드 순서·멱등 규칙대로 그 API를 통해 시드할 것 |
| G-3 | `osiris-watchdog`(호스트 systemd, 컨트롤 플레인 밖 감시) 미설치 | 브리핑의 "포함" 목록에 없었고, §4.1 스텝 7은 "대역외 알림 자격증명 대화형 입력"을 전제해 이번 무인 구현 범위와 맞지 않았다 | D11/§10.3 요구사항이므로 실제 운영 전에는 반드시 구현 필요 |
| G-4 | 관리자 1회용 로그인 토큰(§4.1 스텝 8), genesis 토큰(§8.5) 발급 경로 없음 | `osiris-core`(발급 로직)가 없다 | `osiris-core` 구현과 동시에 |
| G-5 | `osiris-gw`의 `/api/*` 프록시는 `osiris-api`가 없어 502를 반환한다(테스트되지 않음, 완료조건에도 없음) | 설계대로 지연 해석(resolver 관용구)만 걸어 두어 nginx 자체는 정상 기동하지만, 실제 호출은 당연히 실패한다 | `osiris-api` 이미지가 준비되면 `docker-compose.yml`의 주석을 해제하고 `gw/conf.d/default.conf`의 `location /`도 정적 서빙에서 프록시로 되돌릴 것(주석에 명시해 둠) |

## 4. compose에 자리만 잡아 둔 것

`docker-compose.yml` 하단에 `osiris-api`/`osiris-core`/`osiris-orchestrator`가
전체 주석 블록으로 존재한다. 이미지가 아직 없으므로 주석을 풀면 pull 실패로
`docker compose up` 전체가 막힌다 — 다음 워커가 이미지를 게시하고 `.env`의
`TAG`를 채운 뒤 주석 해제할 것. `OSIRIS_MAX_RESIDENT_AGENTS=4`(사용자 확정값)는
`.env.example`과 orchestrator 플레이스홀더 블록의 `environment:`에 이미 자리
잡혀 있다.

---

# 구현 노트 — 2단계 `osiris-core`

> I1 워커가 남긴 위 1절~4절은 그대로 두고, 이 아래에 `osiris-core`를 실제로
> 구현하며 내린 판단을 append한다. 코드는 `core/`(Go 모듈, `osiris-core`).

## 5. 범위

`core/`에 구현: 스키마 마이그레이션(§11 DDL), 카탈로그 시드(§9.2 1~6단계,
멱등), 온보딩 7~12단계가 쓸 graph-write API(Team/Role/Person/AIAgent/
Membership/AssignmentPolicy — 전부 단일 트랜잭션 + `OrgRevision` +1 + 커밋
후 NATS 발행), 불변식 검사기(§9.3 I1~I12, 읽기 전용), `/livez`·`/readyz`,
NATS 발행(`org.revision`, `org.event.<kind>`). compose에 `osiris-core`
서비스로 통합(주석 해제가 아니라 실제 서비스로 교체, `build: ./core`).

**의도적으로 구현하지 않음**: 배정 엔진(`assign()`), WBS/`WorkItem`/
`Assignment`/`Approval` 쓰기, orchestrator/수렴 루프, 에이전트 supervisor,
`osiris-api`, 콘솔, 온보딩 대화(챗봇), §8.5의 genesis 토큰 W1~W5 창닫힘
로직 전체. 이유: 브리핑의 명시적 제외 목록이거나, 그 컴포넌트가 존재해야
의미가 생기는 기능이기 때문.

## 6. 언어·런타임 — Go, 단일 정적 바이너리

설계 문서는 `osiris-core`의 구현 언어를 명시하지 않았다(§3.3은 "결정적
코드"라고만 함). supervisor가 Go로 확정되어 있고(D5 근거: 동시성이 goroutine
+ select로 표현되고, orchestrator/operator와 타입을 공유해야 함), `osiris-core`
도 같은 이유(neo4j-go-driver/nats.go의 성숙도, 정적 바이너리 하나로 배포,
향후 orchestrator와 `AgentWorkloadSpec` 등 타입 공유가 실제로 유용함)로 Go를
선택했다. 표준 라이브러리 `net/http`만 쓰고 웹 프레임워크를 넣지 않았다 —
라우트가 7개뿐이라 프레임워크가 주는 이득보다 의존성 하나가 더 큰 비용.

**A4 강제 방식**: `core/Dockerfile`이 런타임 스테이지를 `FROM scratch`로
둔다. 셸도 패키지 매니저도 없으므로 "나중에 실수로 `claude` 바이너리나
게이트웨이 자격증명을 넣는다"는 경로 자체가 물리적으로 없다 — 문서상 원칙이
아니라 이미지 구조로 강제된다는 브리핑 요구를 그대로 지켰다. `HEALTHCHECK`가
필요로 하는 실행 파일도 셸 없이 필요하므로, 바이너리 자신이
`osiris-core -healthcheck` 플래그로 스스로에게 HTTP GET을 보내 종료 코드로
답하는 방식을 택했다(curl/wget을 이미지에 넣지 않기 위함).

## 7. 스키마 마이그레이션 — `neo4j/init/constraints.cypher`를 흡수(대체 아님)

브리핑이 "흡수할지 대체할지 판단하라"고 요구한 지점. **흡수를 택했다**:
`core/internal/graphdb/schema.cypher`는 원본과 바이트 단위로 동일한 사본이며
(`go:embed`), `osiris-core`가 기동할 때마다 이 DDL을 멱등하게(`IF NOT EXISTS`)
적용한다. 기존 `neo4j/init/constraints.cypher` + `neo4j/apply-schema.sh` +
`osiris-up`의 4단계는 **그대로 남겨뒀다** — 삭제하거나 무력화하지 않았다.
이유:
- DDL은 §9.1이 명시한 "raw Cypher 금지"의 **예외**(데이터가 아니라 스키마)이므로
  두 경로가 같은 것을 적용해도 D17/A5를 어기지 않는다. `CREATE CONSTRAINT ...
  IF NOT EXISTS`는 몇 번을 실행해도 안전하다.
- `osiris-core`가 스스로 적용하게 한 이유는 "이 이미지 하나만 떠도 스키마가
  보장된다"는 성질을 얻기 위함이다 — `apply-schema.sh`를 깜빡 안 돌려도
  `osiris-core`가 기동하는 순간 스키마가 맞춰진다.
- `apply-schema.sh`를 지우지 않은 이유는 `osiris-up`이 아직 `osiris-core`
  기동을 기다리지 않고 그 다음 순서로 진행하도록 짜여 있어서(§4.1 스텝 4),
  `osiris-core`가 아직 healthy가 아닌 이른 시점에도 스키마가 존재해야 하는
  경로가 남아 있을 수 있기 때문이다. 두 경로가 같은 멱등 DDL을 적용하는 한
  중복 실행은 무해하다.

## 8. 카탈로그 시드 — 전부 코드에서 upsert, 사람 편집 파일 없음

§5.2 taxonomy(역량 33개), §8.1 grant-catalog 발췌(권한 9개), 역량→권한
브리지(§7.2, 6개), 기본 `PermissionProfile` 3종(`readonly`/`builder`/
`finance`), `RoleResourceProfile` 3종(`light`/`standard`/`heavy`)을 전부
`core/internal/graphdb/seed.go`의 Go 리터럴로 박아 넣고 `MERGE ... ON CREATE/
ON MATCH`로 적용한다. 사람이 편집할 파일은 없다(브리핑 요구 그대로).

**역량→권한 브리지의 구체 매핑은 이 워커의 판단**이다(§7.2는 "다리가
있다"만 말하고 전체 표를 주지 않음): `cap.code.{python,go,ts}` →
`git.commit`, `cap.biz.procurement`/`cap.mkt.media_buying` → `spend.request`,
`cap.infra.k8s` → `k8s.read.ownteam`. 다음 워커가 실제 온보딩 데이터로
확장/재검토할 것.

**`catalogVersion` 일치 검사(I9)의 판단**: §9.2 3단계는 "권한 카탈로그가
오케스트레이터 내장 grant-catalog와 `catalogVersion`이 일치해야 한다"고
요구하는데, `osiris-orchestrator`는 이번 범위에 없다(compose 주석 자리만).
그래서 I9는 `osiris-core` 자신의 `CATALOG_VERSION` 환경변수(기본
`"2026.07"`)를 "장차 orchestrator가 가질 값"의 대리로 삼아 검사한다.
**다음 워커가 orchestrator의 grant-catalog(Go 맵)를 만들 때 반드시 같은
버전 문자열로 시작할 것** — 지금은 이 값의 유일한 소유자가 core이므로
자기 자신과 비교하는 것과 다름없지만, 시드 파이프라인과 검사기의 배선은
이미 올바르게 되어 있어 orchestrator가 생기면 실제 교차검증이 된다.

## 9. graph-write API 인증 — 정적 bearer 토큰 (genesis 모델의 단순화)

`10-core-architecture.md` §8.5는 조직이 비어 있는 동안만 유효한 정교한
genesis 토큰(W1~W5, 요청마다 `currentOrgRevision`을 재평가해 조직이 생기는
순간 창이 영구히 닫힘)을 요구한다. 이 모델은 (a) 진짜 `agent-master`의
Ready 여부를 확인해야 하는 W3, (b) 컨테이너 ID 바인딩을 확인해야 하는
W4처럼 **온보딩 대화·에이전트 런타임이 존재해야 검증 가능한 조건**을
포함하는데, 둘 다 이번 워커의 범위 밖이다.

**택한 단순화**: `.env`의 `CORE_API_TOKEN` 정적 문자열을 모든 `/v1/*`
라우트가 `Authorization: Bearer <token>`으로 요구한다. 토큰이 없으면
503(fail-closed)으로 전체 쓰기 API를 막는다. **이것은 W1(요청 시점마다
"조직이 비어 있는가" 재평가)이 없다** — 즉 조직이 이미 채워진 뒤에도 같은
토큰이 계속 통한다. genesis 모델의 "특권이 시간이 아니라 조직 상태에
조건부"라는 핵심 성질이 지금은 없다.

**다음 워커(orchestrator/온보딩)를 위한 인계**: §8.5의 W1~W5를 실제로
구현하려면 (1) `graphdb.Client.CurrentRevision()`이 이미 있으니 W1의
`currentOrgRevision >= 1 AND master.ready` 판정에 그대로 쓸 수 있고,
(2) genesis 토큰 발급·저장 경로(`osiris-up`이 1회 발급, `state/creds/
genesis/token`)는 아직 없음 — 지금 `CORE_API_TOKEN`이 임시로 그 자리를
메우고 있을 뿐이다. 정식 구현 시 `CORE_API_TOKEN` 방식은 제거할 것.

## 10. graph-write API 표면 — 구현한 오퍼레이션과 아닌 것

구현: `POST /v1/teams`, `/v1/roles`, `/v1/people`, `/v1/agents`,
`/v1/memberships`, `/v1/assignment-policies`(§9.2 7~12단계), `GET
/v1/invariants`(§9.3, 읽기 전용), `GET /v1/revision`. 전부
`core/internal/graphdb/writeops.go`의 이름 붙은 함수 하나씩과 1:1 대응하며,
쿼리 텍스트는 전부 컴파일 타임 상수(백틱 리터럴)다 — 요청 바디에서 읽은
값은 오직 파라미터(`$id` 등)로만 들어간다. **임의 Cypher 실행 엔드포인트
없음**을 코드 검색으로 확인함(§ "완료 조건 5" 참고, 아래 최종 보고에 결과
포함).

구현 안 함(브리핑 제외 목록과 일치): WBS(`WorkItem`)/`Assignment`/
`Approval`/`Notification` 쓰기, `override()`, 배정 결정 영속화
(`persistDecision`). 이들은 배정 엔진·온보딩 대화·콘솔의 몫이다.

## 11. 발견한 버그와 수정 — MERGE 안의 FOREACH/CASE로 조직 노드 중복 생성

`CreateTeam`의 최초 구현은 부모 팀이 없을 때 `FOREACH (_ IN CASE WHEN
$parentId = "" THEN [1] ELSE [] END | MERGE (t)-[:PART_OF]->(:Organization
{id: "org-root"}))` 패턴으로 조직에 연결하려 했다. 실측(스모크 테스트)
결과, 이미 `{Organization, Party}` 두 라벨을 가진 `id: "org-root"` 노드가
있는데도 이 패턴이 **라벨 하나(`Organization`)만 가진 새 중복 노드를
생성**했다 — MERGE의 "이미 있으면 재사용" 보장이 FOREACH 안에서는
신뢰할 수 없다는, 알려진 Neo4j 함정에 해당한다. **수정**: FOREACH/CASE를
전부 제거하고, Go 코드에서 `if in.ParentTeamID != ""` 분기로 두 개의 고정
쿼리 중 하나만 실행하도록 바꿨다(`writeops.go` `CreateTeam`). 재현 테스트로
중복이 사라졌음을 확인(아래 최종 보고 참고). **다음 워커를 위한 교훈**:
이 코드베이스에서는 조건부 그래프 변경을 Cypher의 FOREACH/CASE로 표현하지
말고 Go의 `if`로 분기해 쿼리 자체를 나눌 것.

## 12. `bin/verify-isolation.sh` 확장과 브리핑 완료조건 6번과의 불일치

브리핑의 완료조건 6번은 "osiris-core가 에이전트 망(osiris-agents)에서
도달 불가해야 한다"고 요구했다. **이것은 `10-core-architecture.md` §2.6의
네트워크 배치표, §8.3("도달 가능한 것은 nats, osiris-core:8090,
osiris-egress:3128 셋뿐"), §1.2 불변자산 10번("에이전트가 아는 호스트명
3개: nats, osiris-core, osiris-egress")과 정면으로 모순된다** — 설계는
`osiris-core`가 `osiris-agents`에 **의도적으로** 이중 홈으로 붙어
에이전트가 그 그래프-쓰기 API(:8090, 토큰 인증)에 도달해야 한다고 여러
곳에서 명시한다.

작업 지침("설계와 어긋나는 것을 발견하면 임의로 바꾸지 말고 설계대로
구현")에 따라 **설계를 따랐다** — `osiris-core`는 `osiris-data` +
`osiris-agents`에 붙고, `osiris-agents`에서 `:8090`에 도달 가능하다.
브리핑의 문자 그대로의 완료조건 6번은 만족시키지 않았다.

대신 `bin/verify-isolation.sh`에 실제로 지켜야 하는 네 가지 경계를
검증하는 C1~C4 절을 추가했다:
- C1: `osiris-core`는 `dev-net`에 전혀 발을 걸치지 않는다(도달 불가) — 통과
- C2: `osiris-agents` → `osiris-core:8090`은 도달 **가능**하다(설계대로) — 통과
- C3: 토큰 없이 호출하면 401(도달 가능 ≠ 인가됨) — 통과
- C4: `osiris-agents` → `neo4j`는 여전히 도달 불가하다(A5의 실제 보루 —
  에이전트가 core를 우회해 그래프에 직접 쓸 방법이 없다는 것) — 통과

**감독이 판단할 지점**: 브리핑 완료조건 6번을 문자 그대로 원한다면
`osiris-core`를 `osiris-agents`에서 떼어내고 에이전트-대면 API를 없애야
하는데, 그러면 §8.3/§2.6/불변자산 10번을 명시적으로 어기게 되고 향후
에이전트가 core를 호출할 방법이 없어진다. 이 워커는 설계 우선 원칙에
따라 후자를 택하지 않았다.

## 13. compose 통합

`docker-compose.yml`의 `osiris-core` 플레이스홀더 주석을 실제 서비스로
교체했다: `build: ./core`, `image: osiris/core:${TAG}`, 네트워크
`osiris-data` + `osiris-agents`(별칭 `osiris-core`), `depends_on`으로
neo4j/nats의 `service_healthy`를 기다림, 호스트 포트 미publish. `.env`/
`.env.example`에 `NEO4J_PASSWORD`(= `NEO4J_AUTH`의 비밀번호부와 반드시
동일 — compose YAML은 문자열을 분리할 수 없어 별도 변수로 중복 보관)와
`CORE_API_TOKEN`을 추가했다.

## 14. 검증 결과 요약 (실행 기반, 추측 아님)

- `docker compose up -d` → `osiris-core` healthy, `/livez`·`/readyz` 200
- 카탈로그 시드 멱등성: 컨테이너 재시작 2회 + `docker compose down/up -d`
  1회, 총 3회 재적용에서 노드/관계 수 불변 확인(Capability 33, Permission
  9, PermissionProfile 3, RoleResourceProfile 3, BROADER 32, GRANTS 11,
  REQUIRES_PERMISSION 6)
- `:Organization` 1개, `:OrgRevision{n:0}` 1개(공식 쿼리로 확인)
- 불변식 검사기: 카탈로그에 없는 역량을 요구하는 `:Role`을 `cypher-shell`로
  주입(이것은 시드 파이프라인이 아니라 검증 목적의 1회성 개입이며, §9.1의
  "시드는 API 경유" 원칙의 적용 대상이 아니다) → `GET /v1/invariants`가
  I4를 오류로 정확히 잡음 → 삭제로 원상 복구 확인
- graph-write API 엔드투엔드 스모크: Team → Role → AIAgent(master) →
  Membership(head) → AssignmentPolicy 전부 생성 성공, `OrgRevision`이
  0→5로 정확히 1씩 증가, 최종 불변식 0 오류 확인 → 스모크 데이터 삭제로
  카탈로그만 남은 베이스라인으로 복원
- 코드 검색: `osiris-core` 전체에서 요청 바디로부터 만들어지는 Cypher
  쿼리 텍스트 없음(모든 `.Run()` 호출이 백틱 리터럴, 유일한 예외는
  마이그레이션의 임베드된 DDL 파일)
- `bin/verify-isolation.sh` 확장(C1~C4) 포함 11개 전체 통과
- `https://osiris.example.com/` 200 유지, `<svc-a>`/`<svc-b>`/상위 리버스 프록시 등 기존
  컨테이너 상태 무변화
- `docker compose down && up -d` 재현 확인(전 서비스 healthy, 카탈로그
  데이터는 named volume에 보존되어 그대로, 재시드는 멱등하게 재확인)
- `git -C <host-infra> status --porcelain`: 작업 전후 diff 없음

---

# 구현 노트 — 3단계 `osiris-orchestrator` (수렴 루프 + `DockerDriver`)

> I1·I2 워커가 남긴 위 내용은 그대로 두고, 여기부터 `osiris-orchestrator`를
> 실제로 구현하며 내린 판단을 append한다. 코드는 `orchestrator/`(Go 모듈,
> `osiris-orchestrator`), `agent-stub/`(임시 스텁 에이전트 이미지).

## 15. 범위

`WorkloadDriver` 인터페이스(`orchestrator/internal/workload`), `DockerDriver`
(`orchestrator/internal/driver/docker`, Docker Engine API 직접 호출),
수렴 루프(`orchestrator/internal/reconcile`), grant-catalog Go 맵
(`orchestrator/internal/catalog`), NATS 계정/ACL 프로비저닝
(`orchestrator/internal/natsauth`), 상한(`OSIRIS_MAX_RESIDENT_AGENTS`) 강제,
스텁 에이전트 이미지(`agent-stub/`), compose 통합(`osiris-orchestrator`
서비스로 실제 교체). **제외**: 배정 엔진, 진짜 에이전트 supervisor,
`osiris-api`, 콘솔, 온보딩 대화 — 전부 브리핑의 명시적 제외 목록.

## 16. Docker Engine API — moby SDK 대신 손으로 만든 최소 HTTP 클라이언트

`github.com/docker/docker`(공식 SDK)를 쓰지 않고
`orchestrator/internal/driver/docker/client.go`가 Unix 소켓 위에 직접
HTTP 요청을 만든다(images/volumes/containers/networks 몇 개 엔드포인트만
필요). 이유: 공식 SDK는 이번 빌드가 쓰는 기능 대비 전이 의존성이
수십 개(moby/moby 전체 타입 트리)라, "Docker Engine API 직접 호출"(A11)을
글자 그대로 해석해 raw HTTP로 구현하는 편이 의존성도 적고 리뷰도 쉽다.
G3("Docker API 타입이 `WorkloadDriver` 밖으로 유출되면 안 된다")는 여전히
지켜진다 — `orchestrator/internal/driver/docker` 패키지 밖으로는 어떤 wire
타입도 나가지 않고, `internal/workload`(수렴 루프가 보는 유일한 계약)는
순수 Go 값(`AgentWorkloadSpec`/`WorkloadStatus`/`WorkloadRef`)만 노출한다.
API 버전은 `v1.43`으로 고정(호스트 데몬 API는 1.49 — 실측
`docker version --format '{{.Server.APIVersion}}'`, 하위 호환 확인됨).

## 17. NATS 인증 모델 — 계정 단위 JWT/nkey 대신 사용자 단위 `authorization{}` 블록

§7.3/§8.4 원문은 팀 계정 하위에 nkey/JWT로 발급된 사용자(디센트럴라이즈드
NATS 계정 모델)를 요구한다. **단순화를 택했다**: NATS의 전통적인
config 기반 `authorization { users: [...] }` 블록 하나(전역 계정 `$G`
하나) — 사용자마다 §7.3이 명시한 그대로의 publish/subscribe 허용목록을
붙인다. 근거: D14가 실제로 요구하는 성질(“타 팀 서브젝트 publish를 서버가
거부”)은 사용자별 subject 허용목록만으로 100% 충족된다 — 실측으로
증명함(§20 참고). 디센트럴라이즈드 계정이 추가로 주는 것은 "계정 전체
격리 + import/export"인데, 이번 빌드의 완료조건은 그 경계를 요구하지
않는다. `orchestrator/internal/natsauth`가 매 reconcile tick마다
`nats/generated/authorization.conf`를 다시 렌더링하고, 내용이 실제로
바뀔 때만 파일을 다시 쓰고 nats 컨테이너에 `SIGHUP`을 보낸다(재시작
없음, JetStream 데이터 무손실). **다음 워커가 진짜 팀 단위 격리(계정
분리)가 필요하다고 판단하면 이 지점을 nkey/JWT 계정 모델로 교체**할 것 —
`AgentUser()` 함수 하나가 유일한 변경 지점이 되도록 설계해 뒀다.

플랫폼 컴포넌트(`osiris-core`, `osiris-orchestrator`) 자신의 NATS
자격증명은 `.env`의 정적 `NATS_CORE_PASSWORD`/`NATS_ORCHESTRATOR_PASSWORD`
(compose `environment:`)로 주입한다 — G5(파일 주입, env 금지)는 **에이전트
자격증명**에 대한 요구이지 플랫폼 컴포넌트에 대한 것이 아니다(이미
`CORE_API_TOKEN`/`NEO4J_PASSWORD`가 같은 방식으로 env에 있다, I2 워커의
선례). 에이전트별 NATS 사용자/비밀번호는 파일 주입 경로
(`/var/run/osiris/creds/nats_user`, `nats_password`)로만 전달된다.

`nats.conf`의 `include` 경로에 대한 실측 함정: nats-server의 `include`는
**leading `/`가 있어도 nats.conf 자신의 디렉토리 기준 상대경로로 해석한다**
(절대경로로 취급하지 않음) — `/etc/nats-dynamic/authorization.conf`를
그대로 쓰면 `/etc/nats/etc/nats-dynamic/authorization.conf`를 찾다가
실패한다. `../nats-dynamic/authorization.conf` 형태로 고쳐야 한다.
비밀번호 값도 마찬가지로 함정이 있었다: NATS 설정 문법에서 `password:
"$VAR"`(따옴표로 감싼 상태)는 리터럴 문자열 `"$VAR"`로 그대로 파싱되고
환경변수로 치환되지 않는다 — **따옴표 없이 `password: $VAR`**로 써야
env 치환이 일어난다(둘 다 컨테이너를 실제로 기동해 `authentication
error`를 재현하고서야 알아낸 것 — `nats/generated/authorization.conf`의
부트스트랩 기본값 파일에 이 교훈을 주석으로 남겨 뒀다).

## 18. WSL2 DrvFs — 설계의 `0700`/`0400`+chown 자격증명 모델을 완화한 이유

설계(§1.4 Apply 2단계, §8.4)는 자격증명 디렉토리를 호스트에서 `0700`,
파일을 `0400`으로 만들라고 명시한다. 이 저장소의 실제 체크아웃 경로가
`<repo-root>`(WSL2의 DrvFs 마운트, Windows 파일시스템 위)라는 점을
실측으로 확인했다: DrvFs는 **root의 permission-bit 우회를 지원하지
않는다** — `chown`은 소유자가 달라도 `Operation not permitted`로 조용히
실패하고(무시하면 이후 파일이 의도한 UID 소유가 아닌 채로 남는다),
`chmod 0400`을 건 파일은 **그 파일을 만든 프로세스 자신도 다시 쓰지
못한다**(실측: `<operator>` 사용자가 자신이 만든 0400 파일에 재작성 시도 →
permission denied). 이 상태로 설계 그대로 구현하면 오케스트레이터
자신의 두 번째 reconcile tick부터 파일 재기록이 막혀 `ReloadConfig`가
전부 실패한다(실측으로 확인 후 아래처럼 완화).

**택한 완화**: `orchestrator/internal/driver/docker/driver.go`의
`writeConfigFiles`/`writeCredsFiles`, `orchestrator/internal/natsauth`의
`GenerateOrLoadPassword`가 디렉토리 `0755`, 파일 `0644`(chown 시도 없음)로
쓴다. 컨테이너 안에서 UID 65532(비루트)가 읽을 수 있어야 한다는 실질
요구는 "world-readable"로 충분히 만족되고, 이 파일들은 애초에 딱 두
프로세스(오케스트레이터 자신, 그 파일이 마운트된 정확히 그 에이전트
컨테이너 하나)만 접근 가능한 경로에 있으므로 실손실은 이 호스트
환경에서는 작다. **네이티브 리눅스 호스트(운영 목표 환경)에서는 설계의
`0700`/`0400`+chown 모델이 정상 동작할 것**이며, 다음 워커가 실제
프로덕션 호스트에 배포할 때 이 완화를 되돌릴 것을 권고한다(정확히 이
커밋의 diff 두 곳).

## 19. core에 가한 최소 확장 — 이 워커가 "소비자"에서 벗어난 지점

브리핑은 "당신은 core의 소비자"라고 명시했지만, 완료조건 4번(에이전트를
닫으면 컨테이너가 드레인 후 제거된다)을 만족시킬 방법이 core에 전혀
없었다(`writeops.go`에 CREATE/upsert만 있고 CLOSE가 없음, D17/G7 때문에
orchestrator가 직접 Neo4j에 쓸 수도 없음). **`core/internal/graphdb/
writeops.go`에 `CloseAIAgent`, `core/internal/api/server.go`에
`POST /v1/agents/close`를 최소 추가했다** — 기존 쓰기 함수와 동일한
패턴(고정 파라미터화 쿼리, 트랜잭션 안에서 revision +1, `ErrValidation`).
**삭제하지 않는다(P4)**: `closedAtRevision` 설정 + 현재 유효
`:Membership.validTo` 설정만 하고 노드는 그대로 둔다. `osiris-orchestrator`
자신은 이 엔드포인트를 **절대 호출하지 않는다** — 그래프의 의도가 이미
바뀐 것을 관찰(`closedAtRevision IS NOT NULL` 또는 유효 Membership 소멸)해
반응할 뿐이다. 호출 주체는 이번 빌드에서는 사람이
`CORE_API_TOKEN`으로 직접 호출하는 것뿐(`osiris-api`/콘솔이 없으므로) —
다음 워커(콘솔/`osiris-api`)가 이 엔드포인트를 실제 "에이전트 격리/오프보딩"
버튼에 연결할 것.

**추가로 core의 기존 버그 2건을 발견해 수정했다** (I2 워커의 §11 "MERGE
안의 FOREACH/CASE" 교훈과 같은 계열의 함정, 이번엔 FOREACH가 아니라
"target 노드가 바뀌는 MERGE"에서): `CreateAIAgent`가 `runtimeClass`를
바꿔 업데이트하면 **새 `:USES_PROFILE` 엣지만 추가되고 예전 엣지는 그대로
남아** 한 에이전트가 `light`와 `standard` 두 `:RoleResourceProfile`을
동시에 가리키는 상태가 됐다(실측: `agent-dev-a`의 `runtimeClass`를
`light`→`standard`로 바꿨더니 정확히 이 상태가 재현됨 — 두 엣지가
동시에 존재해 `SpecHash`가 예전 값 그대로 계산됨). `CreateRole`의
`basePermissionProfileId` 갱신도 같은 패턴(구식 `HAS_PROFILE{kind:'base'}`
엣지가 새 엣지와 공존). **수정**: 대상이 다른 기존 엣지를 `OPTIONAL
MATCH ... WHERE ... <> $target DELETE`로 먼저 지우고 나서 새 엣지를
MERGE한다(둘 다 `writeops.go`). **다음 워커를 위한 일반화된 교훈**:
이 코드베이스에서 `MERGE (a)-[:REL]->(target)`처럼 **target 자체가 요청마다
바뀔 수 있는 관계**는, 그 관계가 "현재 유효한 것 하나만 있어야 하는"
의미론(예: 리소스 프로파일, base 권한 프로파일)을 가진다면 반드시
"target이 달라진 기존 엣지를 먼저 지우기"를 짝지어야 한다 — MERGE는
"명시된 target으로의 엣지가 있는지"만 보지, "이 관계 타입의 다른 엣지가
이미 있는지"는 보지 않는다.

## 20. 완료조건 실측 결과 (추측 아님, 전부 실행 로그/응답 기반)

- **#1**: `osiris-orchestrator` healthy (compose healthcheck, 자기 자신에
  대한 `-healthcheck` 플래그로 `/livez` 확인 — core와 동일 패턴)
- **#2**: `team-alpha`/`role-dev`(promptTemplateMd, `pp-builder` 프로파일)/
  `agent-dev-a`(runtimeClass=`light`)/`mem-dev-a`를 core API로 생성 →
  15초 이내 `osiris-agent-agent-dev-a` 컨테이너 실제 생성 확인. 검증:
  - 네트워크: `osiris_osiris-agents` **하나만** (`docker inspect
    .NetworkSettings.Networks`)
  - 리소스: `light` 프로파일(cpuMilli=500, memMiB=512) → 실제
    `NanoCpus=500000000`, `Memory=536870912` **정확히 일치**
  - 라벨: `osiris.dev/{managed-by,employee-id,team,spec-hash,config-hash}`
    전부 존재
  - 격리 보안 옵션: `ReadonlyRootfs=true`, `CapDrop=[ALL]`,
    `SecurityOpt=[no-new-privileges]`, `User=65532:65532`,
    `RestartPolicy=unless-stopped` — 설계 §1.4 그대로
  - 컨테이너 내부에서 실제로 읽은 `CLAUDE.md`/`settings.json`이
    grant-catalog 파생 결과(`pp-builder` → `git.commit`/`git.push`/
    `Bash(npm install:*)` 등)와 정확히 일치(`docker logs` 확인)
- **#3 (A12 핵심)**: `role-dev.promptTemplateMd`를 REST로 수정(v1→v2) →
  `ConfigHash` 변경 → 로그에 `ReloadConfig` 실행 확인, **컨테이너 ID
  불변**(`6da593a6f073...` 그대로) + 호스트 `state/config/agent-dev-a/
  CLAUDE.md`가 새 내용으로 갱신됨을 직접 확인. 이어서
  `agent-dev-a.runtimeClass`를 `light`→`standard`로 변경 → `SpecHash`
  변경 → 로그에 `Recreate` 실행 확인, **컨테이너 ID 변경**
  (`6da593a6f073...` → `9bee930160d5...`), `Memory`/`NanoCpus`가
  `standard`값(2048MiB/1000m)으로 갱신됨을 확인
- **#4**: `POST /v1/agents/close`(agent-dev-a) → 로그에 "no longer
  desired-running ... draining then deleting" → `docker ps -a`에서
  컨테이너 완전히 사라짐(고아 없음) 확인
- **#5 (D14)**: `agent-dev-b`의 실제 생성된 NATS 자격증명으로
  `nats pub team.alpha.channel`(자기 팀) → **성공**. 같은 자격증명으로
  `nats pub team.marketing.inbox.someone`(타 팀) 시도 → 서버가
  `Permissions Violation for Publish to "team.marketing.inbox.someone"`
  로 **거부**(클라이언트 도구: `natsio/nats-box:0.14.5`, 실제 로그 원문
  확보)
- **#6**: `agent-dev-{b,c,d,e}` 4개 생성 → 전부 정상 기동
  (`residentCount=4`). 5번째 `agent-dev-f` 생성 → 로그
  "REJECTED agent agent-dev-f: MAX_RESIDENT_AGENTS_EXCEEDED:
  OSIRIS_MAX_RESIDENT_AGENTS=4, already resident=4" +
  `GET :8081/status`의 `rejected[]`에 동일 사유 노출 확인. (참고: 이
  테스트 중 `agent-dev-a`가 이미 #4에서 close된 상태였는데, close된
  에이전트를 같은 ID로 다시 `POST /v1/agents`해도 core가
  `closedAtRevision`을 지우지 않아 — 의도된 동작, "재오픈"은 이 빌드의
  API 표면에 없음 — 영구히 desired 집합에서 빠진다는 것도 함께 확인됨)
- **#7**: `bin/verify-isolation.sh`에 D절 추가(실제 에이전트 컨테이너를
  `--network container:<id>`로 공유해 검증 — synthetic 컨테이너가 아니라
  진짜 워크로드 자체의 netns). 에이전트 4개가 떠 있는 상태에서 **17/17
  통과**(기존 11 + 신규 6: neo4j·orchestrator·dev-net 불가, nats·
  osiris-core:8090·osiris-egress:3128 가능)
- **#8**: `https://osiris.example.com/` 200 유지. omniroute(307, 정상 리다이렉트)/
  `<svc-a>`(200)/`<svc-b>`(200) 회귀 없음. **`<svc-b>-live`가 계속 재시작
  중(RestartCount=241)인 것을 발견했으나 로그 확인 결과 그 프로젝트
  자체의 필수 환경변수(`LIVE_SERVER_SECRET_KEY` 등) 누락이 원인 —
  이번 작업 시작 훨씬 전부터의 사전 존재 문제이며 OSiris와 무관함
  (재시작 이력이 241회로, 이번 세션에서 발생 가능한 수치가 아님)**
- **#9**: `docker compose down && up -d` 2회 반복. 에이전트 컨테이너는애초에
  compose 서비스가 아니므로(G4) down에도 살아남고, orchestrator가
  재기동 시 라벨로 기존 컨테이너를 재발견해 **중복 생성 없음**(컨테이너
  ID 불변 확인) — 이것이 §6.1의 "재기동만으로 회복" 주장의 실측
  증거이기도 하다. 마지막엔 전 테스트 에이전트를 `/v1/agents/close`로
  정리해 `residentCount=0`, 고아 컨테이너 0개 상태로 최종 `down/up`
  1회 더 재현(6개 코어 서비스 전부 healthy)
- **#10**: `git -C <host-infra> status --porcelain` 작업 전/후
  스냅샷 `diff` 결과 없음(byte-identical)

## 21. 임의로 정한 세부 결정 (감독이 알아야 할 것)

1. **`osiris.dev/managed-by=osiris-orchestrator` 라벨을 모든 파괴적 Docker
   조작(list/stop/remove)의 유일한 필터 기준으로 삼았다.** NATS 리로드
   시그널(`SIGHUP`)만 예외로, 라벨이 아니라 **정확한 컨테이너 이름**
   (`osiris-nats`)으로 대상을 좁혔다 — 이유는 그 한 번의 조작이 파괴적이지
   않고(SIGHUP=config reload, stop/kill이 아님) 대상이 플랫폼 컨테이너라
   라벨 체계 밖에 있기 때문. 타 프로젝트 컨테이너를 건드릴 화이트리스트
   밖 코드 경로는 없다(코드 검색으로 확인 가능: `driver.go`의 모든
   `/containers/*` 파괴적 호출은 `findContainerID`→라벨 필터를 거친다).
2. **컨테이너 이름 = `osiris-agent-<employeeId>` (접두사 `osiris-` 유지),
   볼륨 이름 = `osiris-ws-<employeeId>`.** 설계는 정확한 컨테이너 이름
   규칙을 명시하지 않아 이 워커가 정했다.
3. **capacity 초과 시 admission 우선순위**: 이미 떠 있는 에이전트는 상한이
   낮아져도 절대 축출하지 않는다(같은 tick에 신규 admission만 게이팅).
   신규 후보끼리는 `isMaster` 우선, 그다음 `employeeId` 사전순(§8.5
   AssignmentPolicy의 최종 tie-break와 동일 규칙 재사용) — 설계가 이
   순서를 명시하지 않아 정했다.

## 22. 설계와 어긋났거나 구현 불가였던 것

- §7.3/§8.4의 nkey/JWT 기반 팀 계정 모델 대신 사용자 단위
  `authorization{}` 허용목록(§17) — D14가 요구하는 서버측 강제라는 **성질**은
  실측으로 증명했지만, "계정 전체 분리"라는 **강도**는 낮다.
- §1.4/§8.4의 자격증명 `0700`/`0400`+chown 모델을 이 WSL2 호스트에서는
  `0755`/`0644`로 완화(§18) — 네이티브 리눅스에서는 원안대로 동작할 것.
- §8.4의 "core 대면 토큰 — 드라이버 발급"을 구현하지 않음: 에이전트
  creds 파일에 `core_token` 자리는 만들어 뒀지만(빈 문자열) 실제 발급
  로직이 없다 — core가 아직 정적 `CORE_API_TOKEN` 하나만 지원하기
  때문(I2 워커의 §9 genesis 단순화가 그대로 남아 있음). 스텁 에이전트가
  core를 호출하지 않으므로 이번 빌드의 완료조건에는 영향 없음.
- `:AgentRuntime` Neo4j 캐시(§6.1의 관측값 미러링)를 구현하지 않음 —
  `GET :8081/status`가 그 자리를 대신한다(§20 참고). 콘솔이 아직 없어
  실손실 없음.
- MCP 서버 배선(§8.2의 "MCP 툴 래핑" 대안 포함)을 구현하지 않음 —
  `mcp.json`은 `{"mcpServers": {}}` 고정값. I4의 몫.

## 23. 다음 워커(I4: 에이전트 supervisor + 진짜 에이전트 이미지)를 위한 인계

- **`agent-stub/` 전체를 교체 대상으로 간주할 것.** `cmd/agent-stub/
  main.go` 헤더 주석에 이미 "이것은 진짜가 아니다"라고 명시해 뒀다.
  살릴 만한 것: `/livez`·`/readyz` 핸들러 모양(설계 §5.4 그대로 구현해
  뒀음), SIGTERM 처리 골격, Dockerfile의 비루트(65532)+`/workspace`
  사전 chown 트릭(named volume 최초 마운트 시 소유권 상속 — 실측
  검증됨, 그대로 재사용 가능).
- **`orchestrator/internal/workload/render.go`의 `RenderMCP()`가
  플레이스홀더**다. 진짜 MCP 서버 배선(git 커맨드 래핑 등, §8.2)이
  여기 들어가야 한다.
- **`AgentWorkloadSpec.RenderedSettings`에 `hooks` 섹션이 없다** —
  PreToolUse 게이트(§8.2, `approval.request` 발행 → core가 실제
  커밋/푸시 대행) 배선은 I4가 `render.go`의 `RenderSettings()`를
  확장해서 채울 자리다.
- **`core_token` 파일 자리는 있지만 빈 값**이다(§22). 실제 에이전트가
  core를 호출해야 하는 시점(예: `finance.request_spend` MCP 툴)에는
  core에 에이전트별 토큰 발급 엔드포인트를 먼저 추가해야 한다.
- **NATS 계정 모델을 nkey/JWT로 강화하려면** `orchestrator/internal/
  natsauth/natsauth.go`의 `AgentUser()`/`render()` 두 함수가 유일한
  변경 지점이다 — 나머지(reconcile 루프, driver)는 이미 "사용자 이름 +
  비밀번호 + 허용목록"이라는 추상 모양으로만 그것들과 상호작용한다.
- **워크스페이스 볼륨 가비지 컬렉션이 없다.** 에이전트를 닫아도
  `osiris-ws-<id>` named volume은 영구히 남는다(§8.3의 "폐쇄 후 볼륨
  회수" 정책 미구현) — 지금은 최대 4명 규모라 무해하지만, 조직이 커지면
  주기적 정리 루틴이 필요하다.
- **네이티브 리눅스 배포 시 §18(WSL2 완화)을 되돌릴 것.** 정확한 diff
  두 곳: `driver/docker/driver.go`의 `writeConfigFiles`/
  `writeCredsFiles`, `natsauth/natsauth.go`의 `GenerateOrLoadPassword`.

## 24. G3 위반 수정 — 감독 코드 리뷰로 발견, 회귀 재현 후 수정

감독이 코드로 직접 확인해 지적한 결함: `orchestrator/internal/reconcile/
reconcile.go`가 `workload.WorkloadDriver` 인터페이스 대신 구체 타입
`*docker.Driver`를 직접 import·보유하고 있었다(`Loop.driver` 필드,
`NewLoop` 파라미터). §6.2/D3/G3가 명시한 "`WorkloadDriver` 경계가
1→2단계 전환의 유일한 재작성 지점"·"수렴 루프 코드는 두 단계에서
동일"이 이 상태에서는 거짓이었다 — `internal/workload` 패키지 자체는
깨끗했지만, 그걸 소비하는 `reconcile` 하나가 결합되어 있었다.

**원인 진단**: 결합이 필요했던 유일한 이유는 `reconcile`이
`driver.WriteCreds(employeeID, creds)`라는, 인터페이스 6개 메서드
(`Apply`/`Recreate`/`ReloadConfig`/`Drain`/`Delete`/`Observe`) 밖의
docker 전용 exported 메서드를 직접 호출하고 있었기 때문이다
(`provisionNATSCreds` 함수). `WriteConfig`라는 exported 메서드도 하나
더 있었으나 실제로는 어디서도 호출되지 않는 죽은 코드였다(Apply/
ReloadConfig가 내부적으로 private `writeConfigFiles`를 직접 호출).

**수정 — 인터페이스를 넓히지 않고, 책임을 Apply 안으로 접었다**:
설계 §1.4의 Apply 의사코드 자체가 "자격증명 디렉토리 생성·파일 기록"을
Apply의 **내부 스텝 2**로 이미 명시하고 있다 — 즉 자격증명을 쓰는 일은
애초부터 `Apply`/`Recreate`가 스스로 해야 할 일이지, reconcile이
Apply 앞에서 별도 메서드로 선행 호출할 일이 아니었다. 그래서:
1. `workload.AgentWorkloadSpec`에 `Credentials map[string]string`
   필드를 추가했다 — **이것이 유일한 인터페이스/타입 확장이다.**
   `RenderedPrompt`/`RenderedSettings`/`RenderedMCP`(드라이버가 파일로
   구체화해야 할 것)와 정확히 같은 패턴을 시크릿에 적용한 것뿐이라
   중립적이다: docker 컨테이너 ID·볼륨 이름 같은 드라이버 고유 개념이
   아니라 그냥 `map[string]string`이고, [2단계] KubeDriver는 같은 맵을
   Secret data로 채우면 그만이다. `SpecHash`/`ConfigHash` 계산에서는
   **의도적으로 제외**했다 — 자격증명 회전은 §8.4상 "재시작 없이
   반영"(리로드 이벤트조차 아님)이어야 하므로, 값이 바뀌어도 어떤 해시도
   움직이면 안 된다.
2. `docker.Driver.Apply()`가 이제 `spec.Credentials`를 받아 내부에서
   (기존 private `writeCredsFiles`로) 직접 쓴다. exported
   `WriteCreds`/`WriteConfig` 래퍼 두 개를 전부 삭제했다(하나는 이제
   불필요, 하나는 애초에 죽은 코드였다) — driver 패키지 밖으로 노출된
   docker 전용 메서드가 0개가 됐다.
3. `reconcile.go`: import를 `internal/driver/docker`에서 제거, `Loop.driver`
   필드와 `NewLoop` 파라미터를 `workload.WorkloadDriver`로 변경.
   `provisionNATSCreds`(driver 호출 포함)를 `buildCredentials`(순수
   함수, driver 호출 없이 `map[string]string`만 반환)로 교체하고, 호출부
   2곳(신규 admission·SpecHash-changed Recreate 분기)에서
   `spec.Credentials = creds`를 채운 뒤 `Apply`/`Recreate`에 넘기도록
   고쳤다. 구체 타입 wiring은 `main.go`에만 남아 있다(승인된 유일한
   지점).

**CI 린트 추가**: `bin/lint-driver-boundary.sh` — `orchestrator/` 아래
모든 `.go` 파일을 스캔해 `internal/driver/*` 자기 자신과
`cmd/osiris-orchestrator/main.go`를 제외한 어떤 파일이라도
`"osiris-orchestrator/internal/driver/`로 시작하는 import 문자열을
가지면 실패한다. **이번에 실제로 일어난 회귀를 그대로 재현해 스크립트가
잡아내는 것을 확인**했다: `reconcile.go`에 `internal/driver/docker`
import를 임시로 재삽입 → `FAIL: .../reconcile.go:38 imports a concrete
driver package outside the approved boundary (G3)` 정확히 출력 →
원복 → `PASS`. 이 스크립트는 아직 실제 CI 파이프라인(GitHub Actions 등)에
연결되어 있지 않다(이 빌드에 CI 설정 자체가 없음) — 다음 워커가 CI를
도입할 때 `bin/lint-driver-boundary.sh`를 빌드 스텝에 추가하기만 하면
된다(인자 없음, 종료코드로 pass/fail).

**재검증(추측 아님, 실행 기반)**: `go build ./... && go vet ./...` 통과,
`gofmt -l .` 무출력. `docker compose build osiris-orchestrator && up -d` →
healthy. 신규 에이전트(`agent-verify`, `light` 프로파일) 생성 → 15초
이내 컨테이너 기동, `state/creds/agent-verify/{nats_user,nats_password,
core_token}` 정상 기록, 컨테이너 로그에서 `CLAUDE.md`/`settings.json`
정상 판독 확인(자격증명 기록 경로가 reconcile 직접 호출 → Apply 내부
호출로 바뀌었지만 파일 계약은 동일하게 유지됨을 실측으로 증명) →
`/v1/agents/close`로 정리, 고아 컨테이너 0개, `bin/verify-isolation.sh`
11/11 통과, `git -C <host-infra> status --porcelain` 작업 전후
diff 없음.

---

# 구현 노트 — 4단계 에이전트 supervisor + 진짜 에이전트 이미지 (I4)

> I1~I3 워커가 남긴 위 1~24절은 그대로 두고, 여기부터 `osiris-supervisor`와
> 에이전트 컨테이너 이미지를 실제로 구현하며 내린 판단을 append한다. 코드는
> `agent/`(Go 모듈 `osiris-agent`). `agent-stub/`은 §23 인계대로 **전면
> 삭제**했다.

## 25. 범위

`agent/internal/health`(§5.4 헬스 상태·핸들러), `agent/internal/supervisor`
(§5.2 메인 루프 / §5.3 상태기계 / §5.5 드레인·재개 / §5.6 상태 보관 /
§5.7 one-shot), `agent/internal/mcpserver`(§8.2 승인 게이트의 MCP 측),
`agent/Dockerfile`(Claude Code CLI 2.1.220 + supervisor, 비루트 65532,
read-only rootfs). orchestrator 측: `internal/catalog`의 §8.1 네 층위 파생
완성, `internal/workload/render.go`의 `RenderSettings`/`RenderMCP` 실제화 +
`RenderAllowedTools` 신규, `internal/jetstream`(§7.2 스트림 + 에이전트별
durable consumer) 신규, `WorkloadDriver.SyncCredentials` 신규(자격증명 회전
전달), CPU 하한 강제, exited 컨테이너 수렴. 검증 자산: `test/mock-gateway`,
`test/flood-child.sh`, `bin/verify-agent-runtime.sh`,
`bin/lint-health-invariant.sh`, `bin/build-agent-image.sh`.

**의도적으로 구현하지 않음**: 배정 엔진(§8 `assign()`), `osiris-api`, 콘솔,
온보딩 대화 — 브리핑의 명시적 제외 목록.

## 26. §5.4 두 불변식을 "주석"이 아니라 "구조"로 만든 방법

브리핑이 이 작업의 본체로 지목한 지점이라, 규율이 아니라 **구조**로 강제했다.

**패키지 분리**: 헬스 핸들러가 읽는 모든 상태는 `agent/internal/health`
패키지 안에만 있고, 그 패키지의 `State` 구조체는 **모든 필드가
`sync/atomic` 타입**이다(불변 문자열 `employeeID` 하나 예외). 이 패키지는
`os/exec`도, `os`도, 채널도, mutex도 import·선언하지 않으며 `internal/
supervisor`를 import하지 않는다(그쪽은 mutex를 잡고 `claude`를 exec한다).
즉 "헬스가 긴 exec 경로의 락을 잡는" 코드를 쓰려면 **먼저 이 파일에 락이나
채널을 추가해야** 하고, 그것이 리뷰어가 볼 단 하나의 신호가 된다.

**린트**: `bin/lint-health-invariant.sh`(인자 없음, 종료코드). 6개 검사 —
(1) health 패키지에 mutex/RWMutex/WaitGroup/Once/Lock/채널 없음, (2)
`os/exec`·`os`·http 클라이언트·파일 I/O 없음, (3) supervisor 패키지 import
없음, (4) `State`의 모든 필드가 atomic, (5) `/livez`·`/readyz` 등록이 이
패키지 밖에 없음, (6) `exec.Command`가 health 패키지에 없음. 주석에는
금지 식별어가 필연적으로 등장하므로 **line comment를 제거한 사본에 대해**
grep한다(block comment 사용 자체도 검사한다).

**린트가 실제로 잡는지 재현 확인**: `health/state.go`의 `State`에
`mu sync.Mutex`를 삽입 → `FAIL: ... blocking primitive matching /sync\.Mutex/`
+ `FAIL: health.State has non-atomic field(s)` 정확히 출력, 종료코드 1 →
원복 → PASS. (I3 §24의 driver-boundary 린트와 동일한 절차.)

**LLM 금지**: 에이전트 모듈 전체에서 프로세스를 만드는 코드는 정확히 4곳
(`probe.go`의 `claude --version`, `task.go`의 `claude` 자식 + `git rev-parse`
+ `git status`)이고 전부 `internal/supervisor`에 있다. `verifyClaudeVersion`은
**부팅 시 1회, main goroutine에서** 호출되며 결과는 atomic bool로만
`/readyz`에 도달한다 — probe 요청 경로에는 프로세스 생성 코드가 존재하지
않는다.

## 27. §5.2 의사코드의 두 결함과 그 처리 (설계 의도는 유지)

`§5.2`를 글자 그대로 구현하면 §5.4가 깨진다. 두 지점 모두 **의도는 그대로
살리고 구조만 바꿨다**.

1. **`runTask`를 루프 본문에서 직선 호출하면 40분 턴 동안 5초 티커가 돌지
   않는다.** 그러면 `lastLoopTick`이 30초 임계를 넘어 §5.4가 금지한 바로
   그 참사(작업 중 재시작)가 일어난다. §5.4 본문은 "턴 중에도 select 루프가
   5초 티커로 계속 돈다"고 단정하므로, **turn 대기 자체를 5초 티커가 포함된
   select 루프로 만들었다**(`runTurn`). 별도의 "항상 초록" watchdog
   goroutine을 두지 않은 이유: 그러면 `lastLoopTick`이 wedge 감지 신호로서의
   의미를 잃는다.
2. **`Fetch(1, MaxWait=30s)`를 인라인 호출하면 유휴 상태에서 `lastLoopTick`이
   상시 ~30초 낡는다** — 임계값과 같아서 정상 유휴 에이전트가 플랩한다.
   **Fetch를 goroutine으로 돌리고 그 결과를 select의 한 case로 받는다.**
   MaxWait는 설계값 30초 그대로 유지하면서 티커가 진짜로 주기적이 된다.
   또한 `nats.MaxWait` 대신 `nats.Context(ctx)`를 써서 Fetch가 취소
   가능해졌다 — 자식 없는 SIGTERM이 §5.5의 "~2초"에 실제로 끝난다.
3. **`if draining.Load() { continue }`는 무한 busy loop**다(iteration에
   블로킹이 없어짐). 드레인 시 루프를 **종료**하도록 고쳤다 — `drainSignal`
   case가 표현하려던 것이 그것이다.

## 28. `--bare`의 실측 결과 — §8.2의 `[미검증]`이 해소되었고, 설계가 지정한 대안(b)로 갔다

§8.2는 "`--bare`에서 `--settings`의 `hooks`가 로드되는지 미검증. 로드되지
않으면 (b) MCP 툴 래핑으로 간다"고 **미리 결정**해 두었다. 추측하지 않고
CLI에 물었다: `claude --help`(2.1.220)의 `--bare` 설명이
**"Minimal mode: skip hooks, LSP, plugin sync, attribution, auto-memory,
background prefetches, keychain reads, and CLAUDE.md auto-discovery"** 다.
훅은 로드되지 않는다.

따라서 `settings.json`에 `hooks` 섹션을 **넣지 않았다**(I3 §23이 예상한
"I4가 채울 자리"). 넣으면 아무것도 로드하지 않으면서 보안 통제처럼 보이는
파일이 되는데, 그것이 부재보다 명백히 나쁘다. 설계가 지정한 (b)를 구현했다:

- grant-catalog가 게이트 있는 권한마다 `MCPServers`/`MCPTools`/`DeniedTools`
  세 열을 추가로 파생한다. `git.commit` → allow `Bash(git add|status|diff|log:*)`
  + `mcp__osiris-git__request_commit`, **deny `Bash(git commit:*)`**.
  `git.push` → deny `Bash(git push:*)` + `mcp__osiris-git__request_push`.
  `msg.external.send` → `mcp__osiris-mailer__send`(§8.1의 "MCP mailer.send").
  `spend.request` → `mcp__osiris-finance__request_spend`.
- MCP 서버는 **supervisor 바이너리 자신**이다:
  `mcp.json`이 `/osiris-supervisor -mcp-server osiris-git`을 stdio로 띄운다.
  이미지에 런타임을 하나도 더 넣지 않고, 툴 구현이 자신이 호출하는 게이트와
  어긋날 수 없다.
- 툴 구현은 전부 "로컬 게이트에 물어보고 사람의 결정을 모델에 돌려준다"뿐.
  git도 메일도 지출도 **실행하지 않는다**(§8.2: "실제 커밋/푸시는
  osiris-core가 자기 자격증명으로 수행한다").

**강제 수준은 §8.2 표 그대로 변하지 않았다** — allowedTools/deny도 훅도
모두 "협조"이며 프롬프트 인젝션에 견디지 못한다. 진짜 강제는 (i) 에이전트
git 토큰이 읽기 전용, (ii) 쓰기는 core만 — 둘 다 그대로다. 게이트는 의도된
경로를 동작·감사 가능하게 만드는 장치다.

**아직 없는 것**: `approval.decision.<id>`를 발행하는 주체(콘솔/`osiris-api`)와
승인된 커밋을 대행 실행하는 core 측 로직. 그래서 게이트는 **최대 24시간
블록 후 timeout**을 모델에 알린다. 승인 레코드 자체는 TASKS 스트림에
영속되므로 사후 처리는 가능하다.

## 29. `--bare`가 강제한 나머지 실측 사항 (전부 목 게이트웨이로 확인, 토큰 0)

| 항목 | 실측 결과 | 반영 |
|---|---|---|
| `ANTHROPIC_AUTH_TOKEN` | `--bare`에서도 유효. 요청 인스펙터로 확인: `POST <BASE_URL>/v1/messages`에 **`Authorization: Bearer <token>`** | §9.2 계약 2번 충족. `ANTHROPIC_API_KEY`는 쓰지 않는다 |
| `--model osiris/worker` | 미지 모델명을 **그대로 request body의 `model`에 통과**시킴 | §9.2 계약 5번(논리 별칭)이 클라이언트 측에서 성립 |
| `ANTHROPIC_CUSTOM_HEADERS` | `X-OSiris-Employee-Id`/`X-OSiris-Task-Id`가 게이트웨이에 도달함(목 로그로 확인) | §9.2 계약 6번 충족 |
| `--bare`가 CLAUDE.md 자동탐색을 끔 | 렌더된 CLAUDE.md를 디스크에 두는 것만으로는 **페르소나가 모델에 전달되지 않는다** | 렌더 결과를 `--append-system-prompt`로 주입. §3.2.1의 계약(1회 저술, 결정적 치환)과 ConfigHash 소속은 그대로 |
| `--output-format stream-json` | `-p`와 함께 쓸 때 `--verbose` 필요 | argv에 추가 |
| `--session-id <uuid>` 존재 | 세션 ID를 **우리가 먼저 만들어** 넘길 수 있다 | 채택. 이유는 §31 |

추가로 `--strict-mcp-config`를 넣었다. 없으면 태스크가 `/workspace`에
자기 `.mcp.json`을 써서 PermissionProfile이 준 적 없는 툴을 얻을 수 있다.

## 30. 자격증명 회전 전달 — 감독이 지적한 공백을 어떻게 닫았는가

**진단**: I3(§24)이 `Credentials`를 `SpecHash`·`ConfigHash` 양쪽에서 제외한
것은 **옳다**(§8.4의 회전은 "재시작 없이 반영", 리로드 이벤트조차 아님).
비어 있던 것은 **쓰는 쪽**이다: `Apply`/`Recreate`는 컨테이너 생성 시에만
돌고, `ReloadConfig`는 ConfigHash가 움직일 때만 도는데 회전은 의도적으로
그것을 움직이지 않는다. 그래서 장수 에이전트에게 새 24h 게이트웨이 토큰이
도달할 경로가 **아예 없었다**.

**검토한 세 가지와 결정**:
1. `ReloadConfig`가 자격증명도 쓰게 한다 → **기각**. ConfigHash 변경 시에만
   돌므로 회전이 무관한 임의 시점에 전달되고, 억지로 해시를 움직이면 방금
   올바르게 분리한 것을 다시 결합시킨다.
2. docker 전용 `WriteCreds`를 다시 export → **기각**. I3 §24가 고친 바로 그
   G3 위반.
3. **중립적 인터페이스 메서드 추가 → 채택.**

`WorkloadDriver`에 **`SyncCredentials(ctx, employeeID, map[string]string)`**
하나를 추가했다. 이것이 I4가 §6.2 인터페이스에 가한 유일한 변경이다.
드라이버 비의존적이다 — 인자가 `AgentWorkloadSpec.Credentials`와 같은
`map[string]string`이므로 [2단계] KubeDriver는 같은 맵으로 Secret의 data를
갱신하면 되고(kubelet이 projected volume을 새로 고치고, supervisor의
read-at-exec가 집어간다) 이 인터페이스 위쪽은 한 줄도 바뀌지 않는다.

**계약: 멱등하고 조용하다.** 드라이버는 내용이 이미 같은 파일은 다시 쓰지
않고(15초마다 5760회/일의 mtime churn 방지), 이벤트를 발행하지 않고,
컨테이너를 건드리지 않는다. supervisor 쪽은 **파일 내용 지문(fingerprint)**
으로 회전을 감지하므로 no-op 호출은 관측 가능한 효과가 0이다.

supervisor 쪽 절반은 의도적으로 가장 멍청한 방식이다 — **`loadCreds()`가
매 호출마다 디스크에서 다시 읽고, 자식 exec 직전에 호출된다.** §8.4가
게이트웨이 토큰에 대해 이미 명시한 그대로다("exec 시점에 파일을 읽으므로
재시작 없이 반영"). 자격증명 디렉토리가 bind mount이므로 호스트 쪽 덮어쓰기가
컨테이너 안에서 **즉시** 보인다. 조치가 필요한 것은 NATS 비밀번호 하나뿐이며
(살아 있는 TCP 연결은 재인증할 수 없다), 그때만 `credsWatchLoop`가
재연결한다(§8.4: "회전 시 supervisor 재연결(무중단)").

**실측 증거**: `.env`의 게이트웨이 base URL/토큰을 바꾸고
`docker compose up -d osiris-orchestrator` → 15초 안에
`state/creds/agent-i4a/{gateway_base_url,gateway_token,gateway_health_path}`
3개 파일이 갱신, 로그 `delivered 3 rotated credential file(s) ... (no restart,
no reload event — §8.4)`, **컨테이너 ID 불변 · RestartCount=0 · health=healthy ·
ReloadConfig 이벤트 없음**. 반대 방향(목→omniroute)으로도 동일 확인.

**아직 없는 것(정직한 격차)**: §8.4가 요구하는 **발급** 절반. 에이전트별
24h 토큰을 core의 omniroute 어댑터 `IssueToken()`(§9.1)이 발급하고 드라이버가
6시간마다 갱신하는 부분은, core에 그 어댑터가 없어서 구현하지 않았다. 지금은
운영자가 `.env`의 `OSIRIS_GATEWAY_TOKEN` 하나를 주고 그것이 모든 에이전트에게
전달된다. **정식 구현 시 바뀌는 곳은 `buildCredentials`의 두 줄뿐**이다.

## 31. 임의로 정한 세부 결정 (감독이 알아야 할 것)

1. **세션 ID를 supervisor가 먼저 만든다(`--session-id <uuid>`).** §5.2는
   "최초 실행이면 `--resume` 생략"만 말하고 ID의 출처를 정하지 않았다. CLI가
   만든 것을 stream-json에서 긁어오는 방식은 exec와 첫 출력 사이에 창이 있어,
   그 사이 SIGTERM이 오면 **sessionId 없는 interrupted 체크포인트**가 남는다 —
   §5.5의 약속(중단된 턴은 재개 가능)이 깨진다. 그래서 프로세스보다 ID가 먼저
   존재하게 했다. 출력에 다른 ID가 나오면 그것을 따른다.
2. **interrupted 턴의 메시지는 `Ack()`한다.** 체크포인트가 이미 fsync되어
   있고 재개 권위는 체크포인트이므로, Nak/무응답으로 두면 재기동 시
   **체크포인트 재개 + JetStream 재전달**로 한 태스크가 두 번 돈다. §5.5가
   interrupted의 내구성 이야기를 체크포인트 + `task.<id>.event=interrupted`
   ("core가 태스크를 in-flight로 유지")로 쓰고 있으므로 그쪽을 따랐다.
   JetStream 재전달은 "시작조차 못 한 턴"의 내구성 장치로 남는다.
   실패(`failed`) 턴은 `NakWithDelay(30s)`로 `max_deliver=3`까지 재시도한다.
3. **`/gate`는 헬스와 별개 리스너**(`127.0.0.1:8082`). §8.2의 승인 게이트는
   최대 24시간 블록한다. Go의 http 서버는 요청마다 goroutine이므로 같은
   리스너를 써도 논리적으로는 안전하지만, "24시간 파킹된 goroutine 수"와
   "probe 지연"이 운명을 공유할 이유가 전혀 없다. core의 `:8081` vs `:8090`과
   같은 규율.

## 32. 설계에 없어서 추가한 것 (전부 실측이 강제한 것)

1. **JetStream 스트림·컨슈머 프로비저닝** (`orchestrator/internal/jetstream`).
   §7.2가 스트림 표를 주지만 **누가 만드는지**는 어디에도 없었고, 실측 결과
   **스트림이 하나도 존재하지 않았다** — 즉 §5.2의 메인 루프가 애초에 불가능.
   orchestrator에 뒀다(§11 쟁점 4의 2층 구분에서 스트림은 플랫폼, 에이전트별
   컨슈머는 조직 모양). **에이전트에게 컨슈머 생성 권한을 주지 않은 것이
   핵심 보안 결정**이다: `$JS.API.CONSUMER.CREATE` on INBOX를 가진 에이전트는
   `team.<타팀>.inbox.*` 필터 컨슈머를 만들어 **남의 메일을 읽을 수 있다**.
   D14는 publish 방향만 막는다. 그래서 컨슈머는 orchestrator가 만들고
   supervisor는 `nats.Bind`로 **바인드만** 한다.
2. **에이전트 NATS ACL에 JetStream 제어면 추가.** §7.3의 목록만으로는
   `Permissions Violation for Publish to $JS.API.CONSUMER.MSG.NEXT.INBOX...`가
   난다. 추가한 것은 전부 **자기 durable 이름에 못박힌** 4종
   (`MSG.NEXT`/`INFO`/`$JS.ACK.INBOX.<durable>.>`/읽기전용 `STREAM.INFO.*`)과
   `_INBOX.>` subscribe. 와일드카드로 걸어갈 여지가 없다.
3. **Master 컨슈머는 multi-filter**(`team.<slug>.inbox.<id>` + `master.inbox`).
   없으면 §7.1이 크로스팀 필수 홉으로 지정한 `master.inbox`가 **컨슈머 없는
   WorkQueue 스트림에 쌓인다**. nats-server 2.10 필요(이 빌드는 고정됨).
   nats.go는 multi-filter 컨슈머의 `FilterSubject`를 빈 문자열로 보고하므로
   supervisor는 그 경우 subject 없이 바인드한다(`OSIRIS_INBOX_MULTIFILTER`).
4. **CPU 하한 1000m 강제** (§5.4 "CPU limit은 >= 1000m을 강제한다
   (orchestrator가 검증)"). 시드된 `light` 프로파일이 500m이라 이 조항이
   즉시 물린다 — 방치하면 `claude` 자식의 CPU 압력에 헬스 goroutine이 굶어
   §5.4가 막으려던 오탐 재시작이 난다. 프로파일을 고치지 않고 spec 파생
   단계에서 clamp했다(메모리는 조직이 정한 값 그대로 유지).
5. **exited 컨테이너 수렴.** desired-running인데 `phase != "running"`이면
   `Recreate`. 이것이 없으면 드레인 후 종료된(또는 사람이 stop한) 에이전트가
   **영구히 죽은 채로 남는다** — `restart: unless-stopped`는 의도적 정지를
   되살리지 않고, 기존 두 분기는 해시 변경과 health 플랩만 본다. G9가 금지한
   "restart 정책에 의존한 복구"의 정확한 반대편 구멍이었다.
6. **NATS ACL 부트스트랩 순서** (`natsauth.EnsureBootstrap`, main에서
   `nats.Connect` **이전**). 구버전 `authorization.conf`로 뜨면
   `_INBOX.>` subscribe가 거부되고, nats.go는 request/reply 인박스 구독을
   **연결당 1회 lazy 생성**하므로 한 번 거부되면 그 연결의 모든 request가
   조용히 timeout한다 — 증상이 "JetStream 도달 불가"로 보인다(실측:
   `add stream INBOX: context deadline exceeded`인데 CREATE에 대한 violation
   로그는 없었다). ACL을 고친 뒤 SIGHUP해도 이미 죽은 구독은 되살아나지
   않는다. 그래서 **연결보다 ACL이 먼저**다. 이미 올바른 파일이면 no-op이다
   (살아 있는 에이전트를 무의미하게 재인증시키지 않기 위해).
7. **헬스 서버를 부팅 최초 단계로 이동.** 측정으로 발견: 헬스 서버가 부팅
   probe 뒤에 있으면 느린 `claude --version`(여기서 4초 이상, 타임아웃 30초)
   동안 `:8081`이 연결을 거부한다. HEALTHCHECK start-period 20초 뒤 3회
   실패 = orchestrator가 재생성 → **probe 순서만으로 만들어진 부팅 재시작
   루프**. 수정 전 1초 폴러의 첫 9회가 무응답이었다. 원리적으로도 옳다:
   `/livez`는 "제어 흐름이 살아 있는가"이고 그것은 프로세스 시작 순간부터
   참이다. 부팅 단계가 알아내는 것은 전부 readiness이고 거기엔 별도
   엔드포인트가 있다.

## 33. 발견해 고친 버그 — 드롭 카운터가 다른 변수를 세고 있었다

flood 테스트가 **`droppedLogLines: 0`으로 "통과"**했는데, 전사된 트랜스크립트는
800만 줄 중 2.4만 줄이었다. 즉 불변식(만재 시 버림)은 정상 동작했고
**그 유일한 증거만 조용히 끊겨 있었다**: `LogPipe`가 `Supervisor.dropped`를
올리는데 `/livez`는 `health.State.droppedLogLines`를 읽고 있었고 후자를 쓰는
코드가 없었다. `LogPipe`를 `onDrop func()` 콜백으로 바꿔 health 상태의 atomic을
직접 올리게 했다 — 보고되는 숫자와 세는 숫자가 **구조적으로 같은 숫자**가 된다.
**교훈(다음 워커용)**: 이 코드베이스에서 "관측값"과 "그 관측값을 만드는 상태"가
다른 구조체에 있으면, 통과한 테스트가 아무것도 증명하지 않을 수 있다. 계측은
반드시 그것을 노출하는 구조체에 쓸 것.

## 34. 완료조건 실측 결과 (전부 실행 로그 기반, 추측 아님)

- **#1 컨테이너 생성·healthy**: core API로 `agent-i4a` 생성 → 15초 안에
  `osiris-agent-agent-i4a` 기동, Docker health `healthy`,
  `/livez` 200 / `/readyz` 200(6개 체크 전부 true). 부팅 로그에
  `claude --version ok: 2.1.220`, `config loaded configHash=... allowedTools=16`,
  `bound durable consumer INBOX/agent-agent-i4a`.
- **#2 유휴 시 `claude` 부재**: `docker top osiris-agent-agent-i4a` →
  **프로세스 1개(`/osiris-supervisor`)뿐, claude/node 매칭 0**. 유휴 토큰 0의
  근거. (`bin/verify-agent-runtime.sh` 1절이 이것을 자동 검사한다.)
- **#3 인박스 → 처리 → 발행**: 에이전트 자신의 파일 주입 NATS 자격증명으로
  `team.alpha.inbox.agent-i4a`에 publish → 로그
  `turn started taskId=t-i4-001 pid=41` → `status=done exit=0 cost=0.000110
  duration=924ms`. 목 게이트웨이가 받은 요청 **정확히 1건**:
  `model="osiris/worker" stream=true auth="Bearer" employee="agent-i4a"
  task="t-i4-001"`. TASKS 스트림에 `task.t-i4-001.event` 2건
  (`Nats-Msg-Id: t-i4-001:1:started` / `:1:done`), COST 1건, TRANSCRIPT 4건
  (`kind:"system_prompt"` 포함).
- **#4 긴 턴 중 헬스 (가장 중요)** — **방법**: 목 게이트웨이를
  `MOCK_TOTAL_MS=270000`(ping 1초 간격으로 SSE를 4분 30초에 걸쳐 전송)로
  띄우고 **진짜 `claude`가 진짜 턴을 4분 31초 수행**하게 했다. 그 netns를
  공유하는 별도 컨테이너가 1초마다 `/livez`를 폴링했다. **증거**: 폴링
  **149회 전부 HTTP 200**(11:19:43 → 11:24:40, 턴 전체 구간 포함, 다른 코드
  0회), 턴 로그 `duration=270953ms status=done`, **컨테이너 ID 불변**
  (`340a58c27f8e…`), **RestartCount=0**, `StartedAt` 불변(턴 시작 전 값),
  Docker health 로그 최근 5회 전부 `exit=0`, health=`healthy`.
- **#5 헬스 경로에 mutex·LLM 없음** — **방법**: (a) `bin/lint-health-invariant.sh`
  PASS, (b) **위반 재현**: health 패키지에 `sync.Mutex` 삽입 → 린트가 정확한
  줄번호와 함께 2건 FAIL, 종료코드 1 → 원복 → PASS, (c) 코드 검색:
  `exec.Command`가 `internal/supervisor/{probe,task}.go` **4곳에만** 존재하고
  health 패키지에는 0곳, (d) health 패키지의 import는
  `encoding/json`/`net/http`/`sync/atomic`/`time` **4개뿐**, (e) `State`의
  모든 필드가 atomic(린트 4번 검사), (f) `/livez`·`/readyz` 등록이 이 패키지
  밖에 없음.
- **#6 SIGTERM → 체크포인트 → 재개** — **방법**: `drainSeconds`를 20으로
  낮추고(SpecHash 변경 → Recreate 확인), 목 게이트웨이 긴 턴 중
  `docker kill -s TERM`. **증거 타임라인**: `11:25:20` SIGTERM →
  같은 초에 `checkpoint(interrupted) fsynced for taskId=t-i4-sigterm attempt=1`,
  4초 뒤 볼륨에서 직접 읽은 `state.json`이
  `{"sessionId":"c6960c58…","taskId":"t-i4-sigterm","turnStartedAt":"…",
  "natsMsgSeq":3,"status":"interrupted","attempt":1}` — **자식에게 시그널은
  아직 보내지 않은 상태** → `11:25:40` `drain grace expired — SIGTERM to child
  pgid=39` → `11:25:41` `status=interrupted exit=143`(Claude Code의 문서화된
  종료코드) → `stopped cleanly (phase=Stopped)`, 컨테이너 exit 0 →
  orchestrator가 `desired-running but phase="exited" — Recreate` →
  **새 컨테이너**(`a04d019cbacd…`, 같은 볼륨) → `found interrupted checkpoint
  taskId=t-i4-sigterm sessionId=c6960c58… attempt=2` → `--resume`으로 재개 →
  `resume finished status=done` → 체크포인트에서 턴 필드만 삭제되고
  **sessionId는 보존**(§5.6). 진행 중이던 턴이 유실되지 않았다.
- **#7 stdout 폭주** — **방법**: 진짜 에이전트 이미지·진짜 supervisor(PID 1)·
  진짜 헬스 서버·진짜 LogPipe를 쓰되, `OSIRIS_CLAUDE_BIN`으로 자식만
  `test/flood-child.sh`(awk로 800만 줄 ≈ 1.7GB를 stdout에 쏟음)로 교체하고
  `-one-shot` 모드로 실행. 진짜 LLM으로는 이 경로에 도달할 수 없다(한 턴의
  stream-json은 수백 줄 규모). **증거**: 폴링 18회 전부 HTTP 200,
  `droppedLogLines`가 **60,948 → 7,770,583**으로 증가(만재 시 버림이 실제로
  일어남), `loopTickOk: true` 유지, 컨테이너 exit 0, 트랜스크립트 파일에는
  살아남은 23,948줄만 기록. 재실행 시 7,793,843건 드롭으로 재현.
- **#8 격리·경계 린트**: 라이브 에이전트 포함 `bin/verify-isolation.sh`
  **17/17 통과**(실제 에이전트 netns 공유 검증 6건 포함). 에이전트 0명
  상태에서 11/11. `bin/lint-driver-boundary.sh` PASS,
  `bin/lint-health-invariant.sh` PASS.
- **#9 도메인 회귀 없음**: `https://osiris.example.com/` 200,
  omniroute 307(정상 리다이렉트), `<svc-a>` 200, `<svc-b>` 200.
  `<upstream-proxy>` 무변화(status=running, 이 세션에서 재시작·정지 없음).
  `<svc-b>-live`는 여전히 사전 존재 크래시 루프(restarts 346) — I3 §20 #8이
  기록한 그 프로젝트 자체의 환경변수 누락 문제이며 건드리지 않았다.
- **#10 `down && up -d` 재현**: 에이전트가 살아 있는 상태로 `docker compose
  down` → 에이전트 컨테이너는 compose 서비스가 아니므로 생존(G4) →
  `up -d` → 코어 6개 전부 healthy, **에이전트 ID 불변·RestartCount 0**,
  orchestrator가 라벨로 재발견해 `applied new agent` **0회**(중복 생성 없음),
  플랫폼 왕복 후에도 `/readyz` 200(NATS 재연결 + 컨슈머 재바인드 성공).
  마지막에 `/v1/agents/close`로 정리 → 에이전트 컨테이너 0개, durable
  컨슈머 삭제 확인(`No Consumers defined`), `osiris.dev/i4-verify` 라벨
  잔여 컨테이너 0개, 검증용 볼륨 0개.
- **#11 호스트 안전**: `git -C <host-infra> status --porcelain` 작업
  전/후 스냅샷이 **byte-identical**(sha256 동일:
  `fdb304d3597f433db780764f7299fbd9ce0d95e9f632ccb52bef210cfd856532`).

## 35. LLM 호출 검증의 정직한 경계

- **목 게이트웨이로 검증한 것**: 와이어 프로토콜(SSE Messages API), 인증
  헤더 형태, 논리 모델 별칭 통과, 비용 귀속 헤더, 긴 턴, 체크포인트/재개,
  stdout 폭주, 헬스 불변식. **토큰 0.**
- **`[미검증]` — 실제 추론 호출은 하지 않았다.** 이유와 그 근거:
  `OSIRIS_GATEWAY_TOKEN`으로 쓸 유효한 omniroute 자격증명이 주어지지
  않았고, 남의 시스템 관리 자격증명을 찾아 써서 토큰을 태우는 것은 지시
  범위를 넘는다. 대신 **경로 자체가 살아 있음을 무비용으로 확인**했다:
  에이전트 netns에서 egress 프록시를 경유해(A3 경로 그대로)
  `GET http://omniroute:20128/v1/models` → **200**,
  `POST /v1/messages`(model=`osiris/worker`) → **404 +
  `{"error":{"message":"No active credentials for provider: osiris",
  "type":"invalid_request_error","code":"model_not_found"}}`** —
  즉 **연결·프록시·와이어 형식은 정상이고, 게이트웨이 쪽 설정이 없다**.
- **그래서 발견한 게이트웨이 측 계약 미충족 2건** (§9.2는 이것들을 omniroute에
  대한 *요구사항*으로 적고 있다):
  1. **계약 4번(`GET /healthz` 무인증, 100ms)** — 이 호스트의 omniroute는
     `/healthz` → **404**, `/api/health` → **401**. 코드 기본값은 설계대로
     `/healthz`로 두고, 이 호스트에서는 `.env`의
     `OSIRIS_GATEWAY_HEALTH_PATH=/v1/models`로 우회한다(무인증 200이며 모델
     호출을 유발하지 않는다). **설계를 바꾼 것이 아니라 설정으로 표현**했다.
  2. **계약 5번(`osiris/planner|worker|cheap` 별칭 해석)** — omniroute의
     `/v1/models`는 `auto/best-coding` · `auto/best-reasoning` ·
     `auto/best-fast`를 노출하고 `osiris/*`는 없다. 매핑은 자명하지만
     (planner→best-reasoning, worker→best-coding, cheap→best-fast) **그
     별칭 등록은 omniroute 측 작업**이며 OSiris 코드로 할 일이 아니다.
     등록 + 토큰 발급만 되면 코드 변경 없이 실제 호출이 된다.

## 36. 설계와 어긋났거나 구현 불가였던 것

- **`settings.json`의 `hooks` 미탑재** — §8.2가 미리 지정한 대안(b)로 갔다.
  근거는 §28(CLI가 `--bare`에서 훅을 건너뛴다는 공식 도움말).
- **§8.4 게이트웨이 토큰의 "발급" 절반 미구현** — core에 §9.1 `LLMGateway`
  어댑터가 없다. 전달 경로는 구현·검증됨(§30).
- **`core_token`은 여전히 빈 문자열** — core가 정적 `CORE_API_TOKEN` 하나만
  지원하므로(I2 §9), 에이전트별 토큰을 주는 것은 곧 **그 토큰을 주는 것**이라
  빈 파일보다 나쁘다. 경로만 유지.
- **DLQ `dlq.inbox` 미구현**(§7.2). `max_deliver=3` 소진은 JetStream
  advisory(`$JS.EVENT.ADVISORY.CONSUMER.MAX_DELIVERIES.>`)로만 관측 가능하고,
  그것을 `dlq.inbox`로 재발행하는 주체가 이 빌드에 없다. 3회 소진된 메시지는
  현재 조용히 사라진다.
- **§5.7 무거운 one-shot의 절반** — supervisor의 `--one-shot` 모드는 구현·
  검증했다(flood 테스트가 그 모드를 쓴다). **orchestrator가
  `osiris-job-<taskId>` 컨테이너를 만들고 전용 임시 볼륨을 주고 회수하는
  부분은 구현하지 않았다** — 그것을 촉발할 주체(배정 엔진)가 범위 밖이다.
- **`:AgentRuntime` Neo4j 캐시 여전히 미구현**(I3 §22와 동일). 하트비트
  `heartbeat.agent.<id>`에 `state`/`phase`/`activeTaskPoints`/`configHash`/
  `droppedLogLines`를 실어 발행하지만, 이것을 Neo4j에 미러링하는 소비자가
  없다. 콘솔이 없어 실손실 없음.
- **WSL2 완화(§18) 유지** — 자격증명·설정 파일은 여전히 `0644`/`0755`이고
  `SyncCredentials`도 같은 모드로 쓴다. 네이티브 리눅스 배포 시 되돌릴 곳이
  이제 **세 곳**이다(I3 §23의 두 곳 + `SyncCredentials`).

## 37. 다음 워커(배정 엔진 / `osiris-api` / 콘솔)를 위한 인계

- **하트비트가 배정 엔진의 입력이다.** `heartbeat.agent.<employeeId>`
  (Core NATS, 10초, 비영속)에 `state`(idle|working|draining|booting)·
  `phase`·`live`·`ready`·`activeTaskPoints`·`maxTaskPoints`·`configHash`·
  `droppedLogLines`·`claudeVersion`이 들어 있다. §5.4가 명시한 대로
  **`readyz`에는 바쁨이 실려 있지 않다** — 하드 필터 `UNAVAILABLE`은
  `ready=false`를 보고, 여유 좌석은 `maxTaskPoints - activeTaskPoints`를 봐라.
- **인박스 메시지 봉투**: `{taskId, prompt, replySubject?, kind?, points?,
  from?}`. JSON이 아니거나 `prompt`가 없으면 **본문 전체를 프롬프트로** 삼고
  taskId를 JetStream 시퀀스로 합성한다(사람이 팀 인박스에 그냥 타이핑하는
  경우를 침묵시키지 않기 위해). 결과는 `task.<taskId>.event`
  (`started`/`done`/`failed`/`interrupted`/`interrupted_final`/`blocked`)로
  발행되고 `replySubject`가 있으면 거기에도 한 번 보낸다.
- **`approval.decision.<approvalId>`를 발행하는 쪽이 없다.** supervisor의
  게이트는 그 서브젝트를 구독한 채 최대 24시간 블록한다. 콘솔/`osiris-api`가
  `{"decision":"approved|rejected","reason":"...","executedBy":"..."}`를
  거기로 publish하면 즉시 모델에게 전달된다. 그리고 **승인된 커밋/푸시를
  실제로 수행하는 core 측 로직**이 §8.2의 마지막 조각으로 남아 있다.
- **`osiris-api`의 SSE 재개(D19)는 이미 가능하다.** TASKS 스트림이
  `task.*.event`·`approval.>`·`org.event.>`·`cluster.>`를 90일 보존하고
  `Nats-Msg-Id` 중복제거 2분이 걸려 있다. `Last-Event-ID` = 스트림 시퀀스.
- **`osiris-api`는 JetStream 관리 권한이 없어야 한다.** 스트림·컨슈머
  프로비저닝은 orchestrator 단독이다(§32-1의 이유). API에 NATS 사용자를 줄
  때 `natsauth.render()`의 정적 사용자 블록에 추가하되 `$JS.API.>`는 주지
  말고, 필요한 것(ephemeral consumer 생성)만 좁게 줄 것.
- **`master.inbox` 소비는 Master 에이전트에게만 배선되어 있다**(multi-filter
  컨슈머). Master가 없는 조직에서는 크로스팀 메시지와 에스컬레이션이
  WorkQueue에 쌓인다 — 온보딩이 Master를 반드시 만들게 하는 이유가 하나 더
  생긴 셈이다.
- **재현 스크립트**: `bin/verify-agent-runtime.sh <employeeId>`. 1절(유휴
  claude 부재)과 4절(stdout 폭주)은 조건 없이 돌고, 2·3·5절은 `.env`가 목
  게이트웨이를 가리킬 때만 돈다(스크립트가 필요한 `.env` 변경을 출력한다).
  `test/mock-gateway`와 `test/flood-child.sh`는 **테스트 픽스처**이며
  compose나 osiris-up이 참조하지 않는다.
- **에이전트 이미지 빌드는 `bin/build-agent-image.sh`**(osiris-up 5b단계).
  compose 서비스로 만들지 않은 이유는 그 스크립트 헤더에 있다(G4).
  Claude Code CLI 버전은 `CLAUDE_CODE_VERSION=2.1.220`으로 고정 —
  **툴 표면이 조용히 바뀌면 그래프가 승인한 `--allowedTools` 목록의 의미가
  달라진다.** 스크립트는 A4를 양방향으로 검사한다(에이전트 이미지에 `claude`가
  있고, core/orchestrator 이미지에는 없음).
- **워크스페이스 볼륨 GC는 여전히 없다**(I3 §23과 동일). 닫힌 에이전트의
  `osiris-ws-<id>`가 영구히 남는다. 다만 **볼륨이 재개의 권위**라는 점이
  이제 실증되었으니(§34 #6), GC 루틴을 만들 때 "닫힘 후 즉시 삭제"가 아니라
  유예 기간을 둘 것을 권한다.

---

# 구현 노트 — 5단계 `osiris-omniroute` 전용 LLM 게이트웨이 (I5)

> I1~I4 워커가 남긴 위 1~37절은 그대로 두고, 여기부터 OSiris 프레임워크
> 전용 omniroute 인스턴스를 신설하며 내린 판단을 append한다. 코드는
> `omniroute/`(선언 config + 적용 스크립트), `bin/configure-omniroute.sh`,
> `docker-compose.yml`/`egress/squid.conf`/`.env.example`/`osiris-up`의
> 관련 변경.

## 38. 범위와 배경

I4까지의 완료조건은 목(mock) 게이트웨이로만 검증되어 있었고(§35), 실제
`OSIRIS_GATEWAY_BASE_URL`이 가리키던 것은 호스트에 이미 떠 있던 **공용**
omniroute(`<host-infra>/omniroute`, `dev-net`)였다. 실측 결과 그
공용 인스턴스는 `osiris/*` 별칭·자격증명이 없어(`/v1/messages` → 404
"No active credentials for provider: osiris") 실제 추론이 불가능했다.
**사용자 결정: 공용 인스턴스를 고치거나 흡수하지 않고, OSiris 스택 내부에
전용 `osiris-omniroute` 인스턴스를 신설한다.** 공용 인스턴스
(컨테이너 `omniroute`, 볼륨 `omniroute_omniroute_data`, 도메인
`omniroute.example.com`)는 이 작업 전체에서 **읽기 전용 관찰**(이미지 내부
파일 열람, `/v1/models`·`/healthz` 등 GET) 외에는 전혀 건드리지 않았다.

작업 도중 감독으로부터 두 차례 방향 수정이 왔고(§39, §40에 반영), 최종
지시는 다음 세 가지로 수렴했다: (1) 웹 UI 수동 설정이 아니라 관리 API로
코드가 구성할 것, (2) 새 서브도메인은 금지 — 서브패스(`/omni/`) 우선
검토 후 안 되면 루프백 포트, (3) 기존 제약(공용 인스턴스 무영향·에이전트
망 계약 불변·다이제스트 고정·호스트 안전)은 그대로.

## 39. 이미지 다이제스트 — 이미 로컬에 있었다

감독이 지정한 다이제스트
(`diegosouzapw/omniroute@sha256:badb560971fdc23c2fb84b3e8695116239ff215b4cca4b07076201a8efae7f0d`)
를 `docker image inspect`로 조회한 결과, 이미 로컬에 pull되어 있었다 —
`docker inspect omniroute --format '{{.Image}}'`가 정확히 이 다이제스트의
로컬 이미지 ID(`sha256:d5500a44f5...`)와 일치했다(즉 현재 가동 중인
`:latest` 태그가 바로 이 버전이다). 그래서 신설 인스턴스는 **새로
pull하지 않고** 같은 로컬 이미지를 다이제스트로 고정 참조했다 — 네트워크
호출 없이 사용자가 실제 운용해 온 정확히 그 버전을 재사용한다.

## 40. 관리 API 표면 조사 — 소스 열람 + 실제 실행 양쪽으로 확인

이미지 안의 실제 애플리케이션 소스(`docker run --entrypoint sh <image> -c
'cat/grep/find ...'`, 이미지 다이제스트가 고정되어 있으므로 이것은 "이미
검증된 바로 그 바이너리"를 읽는 것이지 별도 소스코드 저장소를 뒤진 게
아니다)를 열람하고, 이어서 **완전히 별도의 throwaway 컨테이너**(호스트
네트워크에도, `dev-net`에도 물리지 않은 임시 브리지 네트워크, ephemeral
`--tmpfs`/bind 데이터 디렉토리, 세션 종료 시 전량 삭제)에서 실제로
부팅해 API를 호출했다. 신설 `osiris-omniroute`도, 공용 `omniroute`도
이 조사 과정에서 단 한 번도 대상이 되지 않았다.

**발견한 사실(전부 실행 기반)**:
- `INITIAL_PASSWORD` env → 최초 부팅 시 bcrypt 해시로 DB에 영속화("관리자
  비밀번호"). 대시보드에서 사람이 처음 로그인해 설정할 필요가 없다 —
  컨테이너를 그 env로 띄우는 것 자체가 부트스트랩이다.
- `POST /api/auth/login {password}` → `Set-Cookie: auth_token=<JWT>`
  (HS256, 30일 만료). `requireManagementAuth`는 이 쿠키 또는 `manage`
  스코프 API 키의 `Authorization: Bearer` 어느 쪽도 동등하게 받는다 —
  CSRF 토큰은 로그인 자체에는 불필요(라우트 핸들러가 요구하지 않음).
- 프로바이더: `GET/POST /api/providers`, `PUT /api/providers/{id}` —
  `{provider, apiKey, name}`. `provider`는 자유 문자열이며
  `isManagedProviderConnectionId`/`isOpenAICompatibleProvider`/
  `isAnthropicCompatibleProvider` 중 하나만 통과하면 된다. `anthropic`으로
  실제 생성 성공(가짜 키로 테스트, 201).
- 별칭(§9.2 계약 5번의 실제 구현체): `GET/POST /api/combos` —
  `{name, models:[{provider, model}]}`. `name` 정규식이 `/`를 허용해
  `osiris/worker` 같은 이름을 그대로 쓸 수 있다. **공용 인스턴스에서 이미
  관측했던 `auto/best-coding`/`auto/best-reasoning`/`auto/best-fast` 별칭도
  `owned_by: "combo"`로 노출됨을 확인** — combo가 정확히 이 별칭
  메커니즘이라는 확증이다.
- API 키: `GET/POST /api/keys` — `{name, scopes}`. 평문은 생성 응답에만
  실리고, 이후 재조회는 `GET /api/keys/{id}/reveal`(env
  `ALLOW_API_KEY_REVEAL=true` 필요, 기본 꺼짐)로만 가능.
- **실제 end-to-end 실행 결과** (throwaway 인스턴스, 가짜 Anthropic 키):
  로그인 → 프로바이더 생성(201) → combo `osiris/worker` 생성(201,
  `models:[{provider:"anthropic",model:"anthropic/claude-sonnet-4-5"}]`)
  → API 키 생성(201) → `GET /v1/models`(그 키로도, 무인증으로도)에
  `osiris/worker`가 실제로 나타남을 확인(`ids.includes("osiris/worker")
  === true`, 총 116개 모델 중 하나로).
- **멱등성 실측**: 같은 시퀀스를 2회 연속 실행 → combo/key 개수 불변(각각
  이름으로 먼저 GET해 존재 확인 후에만 POST하기 때문).

**결론: 대시보드를 사람이 클릭할 필요가 전혀 없다.** §41의
`bin/configure-omniroute.sh`가 이 표면 전체를 코드로 구성한다.

## 41. `omniroute/config.json` + `bin/configure-omniroute.sh` — 선언 + 멱등 적용

`omniroute/config.json`에 프로바이더(`{name, provider, apiKeyEnv}` — 실제
키 값이 아니라 `.env` 변수 **이름**만), `osiris/*` 별칭 3개(설계가 허용하는
정확히 그 세 이름 — `core/internal/graphdb/writeops.go`의
`allowedModelAliases`와 일치시켰다), 에이전트용 API 키 이름을 선언한다.
`omniroute/apply-config.mjs`(Node, 외부 의존성 0 — 내장 `fetch`만 사용)가
이걸 읽어:
1. 로그인(§40) → 쿠키.
2. 각 프로바이더: `apiKeyEnv`가 가리키는 환경변수가 **비어 있으면 건너뛰고
   WARN만 남긴다**(에러로 죽지 않음 — 이것이 "키 없이 갈 수 있는 데까지"
   원칙의 구현이다). 값이 있으면 이름으로 존재 확인 후 PUT(갱신) 또는
   POST(생성).
3. 각 별칭: 이름으로 존재 확인 후 없으면만 POST. **프로바이더 존재 여부와
   무관하게 항상 시도한다** — combo의 `models` 항목은 FK가 아니라
   `{provider, model}` 문자열 쌍이라 프로바이더 커넥션이 없어도 combo
   자체는 만들어지고 `/v1/models`에 노출된다(§40의 실측). 다만 그 상태의
   실제 추론은 프로바이더가 없어 실패한다 — 이것이 §9.4의 "[미검증]"
   경계다.
4. 에이전트 API 키: 이름으로 존재 확인. 없으면 생성해 평문을
   `.env`(`OSIRIS_GATEWAY_TOKEN=`)에 자동 기록. 있으면 `reveal`로 재조회해
   `.env`를 다시 동기화(재실행 시 `.env`가 유실되어도 복구 가능).
5. 마지막에 `GET /v1/models`로 별칭 3개가 실제로 노출되는지 재확인해
   로그에 남긴다.

`bin/configure-omniroute.sh`는 `.env`를 로드하고 이 스크립트를 exec하는
얇은 래퍼(다른 `bin/*.sh`와 같은 관례)다. `osiris-up`의 5c단계로 편입 —
`osiris-omniroute`가 healthy가 될 때까지 최대 60초 대기 후 실행하며, 이
단계가 실패해도(예: 아직 healthy가 아님) `osiris-up` 전체를 막지 않고
경고만 남긴다(사람이 나중에 직접 재실행할 수 있도록).

**실측(실제 신설 인스턴스에 대해, throwaway 아님)**: 1회차 실행 →
프로바이더 WARN(키 없음, 정상), 별칭 3개 생성, API 키 생성 →
`.env`의 `OSIRIS_GATEWAY_TOKEN` 갱신됨 확인. 2회차 실행(변경 없이 재실행)
→ 별칭 3개 "already exists" 그대로, 키도 "already exists"로 재동기화,
`.env`는 "already up to date". API로 직접 개수 조회(`GET /api/combos`,
`GET /api/keys`) → combo 정확히 3개(`osiris/planner|worker|cheap`), key
정확히 1개(`osiris-agents`) — 완료조건 11(멱등성) 충족.

## 42. omniroute 아웃바운드 경로 — (a) 채택, 실측 근거

브리핑이 요구한 실측(추측 금지)을 소스 열람 + 실행 로그 양쪽으로 수행했다.

**소스 열람**: `open-sse/utils/proxyFetch.ts`가 `globalThis.fetch`를
패치하고(`patchedFetch`, 모든 프로바이더 TLS 클라이언트 모듈이 이걸
import), 프로바이더별 프록시가 DB에 설정되지 않은 경우(`proxyContext`가
비어 있음 — 신설 인스턴스는 항상 이 상태) `resolveEnvProxyUrl()`가
`HTTPS_PROXY`/`HTTP_PROXY`/`ALL_PROXY`(대소문자 변형 포함)와 `NO_PROXY`를
표준 방식으로 읽어 적용한다(`source: "env"`).

**실행 로그 (throwaway 인스턴스, 실제 컨테이너 부팅)**: `HTTPS_PROXY`/
`HTTP_PROXY`를 우리가 만든 테스트 squid(허용목록에 `example.com`만 등록)로
설정하고 부팅 → 앱의 백그라운드 잡(Arena ELO 리더보드 동기화, 우리가
트리거한 게 아니라 앱이 스스로 시작 시 실행하는 것)이 실제 프로바이더가
아닌 다른 외부 목적지로 나가려다가 로그에 정확히 다음을 남겼다:
`[ProxyFetch] Proxy request failed (env, fail-closed): fetch failed`.
이것은 (1) 프록시 경로를 실제로 탔고(허용목록에 없어 우리 테스트 squid가
거부한 것), (2) 거부됐을 때 프록시를 우회해 직접 나가지 않고 그대로
실패한다(fail-closed)는 두 가지를 동시에 증명한다.

**대조군**: 같은 다이제스트가 아닌 순정 `node:24-alpine`으로 같은 실험을
반복 — 내장 `fetch`(undici)는 `HTTPS_PROXY`를 무시하고 직접 나가려다
격리 네트워크(내부용, 인터넷 라우팅 없음)에서 타임아웃했다. 즉 "Node의
`fetch`는 기본적으로 프록시 env를 안 본다"는 일반 사실과, "omniroute
**자신의** 코드가 이를 명시적으로 구현해 얹었다"는 사실을 분리해서
확인했다 — 후자가 없었다면 전자 때문에 (a)는 애초에 성립할 수 없었다.

**결정: (a) 채택 — `osiris-omniroute`도 `osiris-egress`를
HTTPS_PROXY/HTTP_PROXY로 강제**. A3에 예외를 두지 않는다. 상세 근거와
잃는 것(허용목록 유지 비용)은 `10-core-architecture.md` §9.0에 기록.
(b)(자체 아웃바운드 망 예외)를 택하지 않은 이유도 그쪽에 함께 적었다 —
요약하면 준수가 실측으로 확인된 이상 예외를 정당화할 근거가 없어졌다.

## 43. 네트워크 토폴로지 — `osiris-llm` + `osiris-omniroute-lo` (실측이 강제한 2단 구조)

**최초 설계**: `osiris-omniroute`를 `osiris-llm`(신설 `internal: true`)
하나에만 붙이고, `osiris-egress`를 거기 이중 홈해 에이전트 CONNECT를
중계 + 자신의 아웃바운드도 그리로. 여기까지는 계획대로 됐다.

**실측이 뒤집은 부분**: 관리 API 접근용 루프백 호스트 포트를 그대로
`osiris-omniroute` 서비스에 `ports: ["127.0.0.1:20129:20128"]`로 붙였더니
`docker compose up -d` 후 `docker inspect`의 `NetworkSettings.Ports`가
**항상 `{}`로 비었다**(`HostConfig.PortBindings`에는 기록되어 있는데도) —
호스트에서 `curl 127.0.0.1:20129`는 connection refused. **별도의 완전히
격리된 재현 실험**(`docker network create --internal` + 그 네트워크
하나에만 붙은 `nginx` 컨테이너에 `-p 127.0.0.1:29999:80`)으로 일반화되는
사실임을 확인했다: **Docker는 `internal: true` 네트워크에만 붙은
컨테이너의 host 포트 publish를 아예 세팅하지 않는다.** 이미지나 앱과
무관한 Docker 엔진 자체의 동작이다.

**함의**: `osiris-omniroute` 자신에게 host 포트를 열어주려면 `internal:
true`가 아닌 네트워크를 하나 더 줘야 하는데, non-internal 네트워크는
docker의 기본 브리지 NAT로 **인터넷 직행 경로**를 자동으로 얻는다 — 이건
정확히 §42에서 지키려던 "omniroute의 모든 아웃바운드는 osiris-egress를
거친다"는 성질을 그 컨테이너 스스로 뚫는 두 번째 인터페이스가 된다.

**해결**: `osiris-omniroute` 자신은 여전히 `osiris-llm` 하나뿐으로 두고,
**애플리케이션 로직이 전혀 없는 순수 TCP 중계 컨테이너**
(`osiris-omniroute-lo`, `alpine/socat`, 다이제스트 고정)를 새로 둬서 이
컨테이너만 `osiris-llm` + 신설 non-internal 망(`osiris-omniroute-lo`,
멤버는 이 다리 하나뿐)에 이중 홈시켰다. `docker-compose.yml`의
`osiris-gw`/`osiris-egress`가 이미 쓰고 있는 "DMZ 다리" 패턴(§2.6)의
재사용이다 — socat 하나만 도는 셸 없는 이미지이므로, 이 컨테이너가
통째로 뚫려도 할 수 있는 일은 하드코딩된 목적지(`osiris-omniroute:20128`)
로의 TCP 중계뿐, 임의 아웃바운드를 만들 코드 자체가 없다.

**실측 확인**: `docker compose up -d` → `osiris-omniroute`(healthy,
osiris-llm만) + `osiris-omniroute-lo`(up, 두 망) → 호스트에서
`curl http://127.0.0.1:20129/v1/models` → 200. `osiris-omniroute` 자신의
`docker inspect .NetworkSettings.Networks`는 `osiris-llm` **하나만**
가짐을 확인.

## 44. 서브패스(`/omni/`) — 실측으로 불가 확정

감독의 최우선 지시(§39 배경)에 따라 `osiris.example.com/omni/` 마운트를
먼저 검토했다. 이미지 소스의 `next.config.mjs`가
`basePath: process.env.OMNIROUTE_BASE_PATH || ""`를 명시적으로 지원하는
것을 확인하고(코드 주석: "Opt-in subpath deployment behind a reverse
proxy"), throwaway 인스턴스에 `OMNIROUTE_BASE_PATH=/omni`를 주고 실제
부팅해 검증했다:
- `GET /` → 200 (마치 env가 없는 것처럼 루트에서 그대로 응답)
- `GET /omni/` → 404
- `POST /omni/api/auth/login` → 404
- `GET /v1/models`(prefix 없이) → 200, 정상 응답

즉 **런타임 env가 전혀 반영되지 않았다** — Next.js의 `basePath`는 `next
build` 시점에 번들에 굳는 값이고, 이미 빌드되어 배포된 이미지에는 벤더가
빌드할 때의 값(빈 문자열)이 이미 굳어 있다. 이것은 이 벤더 이미지
고유의 사실이 아니라 Next.js `output: "standalone"` 빌드의 일반적
제약이다(공식 동작).

`sub_filter` 류의 에셋 경로 강제 재작성은 감독이 사전에 금지했고(깨지기
쉬움), 설령 시도한다 해도 basePath 없이 prefix만 벗겨 프록시하면
omniroute 자신의 `/api/*`가 §2.9가 osiris-api용으로 예약한 네임스페이스와
충돌해 애초에 성립하지 않는다. **결론: 서브패스 불가 — 우선순위 2(루프백
전용 포트)로 폴백**, §43의 다리 컨테이너로 구현했다. 다음 워커가 이
벤더 이미지를 소스에서 직접 빌드(`OMNIROUTE_BASE_PATH=/omni` 고정)하는
방향으로 가면 이 자리를 다시 열 수 있다.

## 45. 헬스 경로 재실측 — 새 인스턴스에서도 동일

I4 §35가 공용 인스턴스에서 확인한 `/healthz` → 404, `/api/health` → 401을
**신설 전용 인스턴스(같은 다이제스트)에서도 재현**했다(§9.2 계약 4번).
`/v1/models`는 무인증 200이고 모델 호출을 유발하지 않으므로 계속
`OSIRIS_GATEWAY_HEALTH_PATH=/v1/models`로 우회한다 — 값 자체는 안
바뀌었지만 "새 인스턴스에서 재실측"이 브리핑의 요구 사항이라 다시
실행해 확인했다(`10-core-architecture.md` §9.2 갱신).

## 46. compose/egress/env 배선 요약

- `docker-compose.yml`: 신설 네트워크 `osiris-llm`(internal, 172.31.241.0/24)
  + `osiris-omniroute-lo`(non-internal, 172.31.242.0/24), 신설 볼륨
  `osiris-omniroute-data`, 신설 서비스 `osiris-omniroute`(다이제스트 고정,
  헬스체크는 `/v1/models`)와 `osiris-omniroute-lo`(socat 다리). 기존
  `osiris-egress`는 `osiris-llm`에도 이중 홈(dev-net 멤버 수는 불변).
  `osiris-orchestrator`의 `OSIRIS_GATEWAY_BASE_URL` 기본값을
  `http://omniroute:20128`(공용) → `http://osiris-omniroute:20128`(전용)로
  전환.
- `egress/squid.conf`: 소스 ACL `omniroute_net`(172.31.241.0/24) 추가,
  목적지 ACL `allow_osiris_omniroute`(에이전트→신설 인스턴스) +
  `llm_provider_domains`(신설 인스턴스→실제 프로바이더, 주요 프로바이더
  도메인으로 시작 — Anthropic/OpenAI/Google/Azure OpenAI/Mistral/Cohere/
  Groq/OpenRouter/Together/DeepSeek/Perplexity/xAI, 사용자가 새 프로바이더를
  추가하면 여기 한 줄 추가가 운영 절차). 공용 인스턴스용 옛 ACL
  (`allow_omniroute dstdomain omniroute`)은 하위 호환으로 남겨 뒀다(지금은
  아무도 안 씀).
- `.env.example`/`.env`: `OSIRIS_OMNIROUTE_ADMIN_PASSWORD`(신규, 관리 API
  전용 — 대시보드를 공개하지 않으므로 사실상 `bin/configure-omniroute.sh`만
  씀), `OSIRIS_OMNIROUTE_LOOPBACK_PORT=20129`(20128과 겹치지 않음),
  `ANTHROPIC_API_KEY=`(플레이스홀더, 실제 값은 사용자 입력).
  `OSIRIS_GATEWAY_BASE_URL`/`OSIRIS_GATEWAY_TOKEN`도 전용 인스턴스 지향으로
  갱신(토큰은 `bin/configure-omniroute.sh`가 자동 기록).
- `orchestrator/internal/catalog/catalog.go`의 `llm.gateway` grant
  `EgressAllowlist` 리터럴도 `osiris-omniroute:20128`로 갱신(1단계에서는
  문서 성격뿐이라 실제 강제는 여전히 `egress/squid.conf`).
- `osiris-up`: 5c단계로 `osiris-omniroute` healthy 대기 +
  `bin/configure-omniroute.sh` 호출을 추가(실패해도 전체를 막지 않음).
  헬스 요약 목록에 `osiris-omniroute` 추가.
- `bin/verify-isolation.sh`: A7/A8(합성 에이전트 컨테이너 — egress 경유는
  허용, 직접은 불허), D7/D8(**실제** 에이전트 워크로드 netns 공유 —
  동일)을 추가.

## 47. 완료조건 실측 결과 (전부 실행 기반, 추측 아님)

- **#1**: `docker compose up -d` → `osiris-omniroute` healthy(다이제스트
  `diegosouzapw/omniroute@sha256:badb560971fdc23c2fb84b3e8695116239ff215b4cca4b07076201a8efae7f0d`
  고정, `docker inspect`로 확인).
- **#2**: 작업 전/후 `docker inspect omniroute`의 `.Id`(공용 인스턴스
  컨테이너 ID)·`RestartCount`(0)·`StartedAt`이 byte-identical. 볼륨은
  `omniroute_omniroute_data` 그대로(새 볼륨 `osiris_osiris-omniroute-data`는
  완전히 별도). `https://omniroute.example.com/` → 307(작업 전과 동일한
  리다이렉트 응답, I3/I4가 기록한 그대로).
- **#3**: 합성 에이전트 컨테이너와 **실제 에이전트 워크로드**(core API로
  `team-osiris-verify`/`role-osiris-verify`/`agent-osiris-verify` 최소
  생성 → orchestrator가 15초 내 컨테이너 기동 → 검증 후 `/v1/agents/close`
  로 정리, 고아 0) 양쪽에서 `osiris-egress` 경유
  `GET http://osiris-omniroute:20128/v1/models` → 200 확인(빈 DB라 모델
  목록에 별칭이 없는 상태에서의 응답 확인 + 이후 §41 적용 후 재확인 양쪽).
- **#4**: `bin/verify-isolation.sh` **21/21 통과**(기존 13 + 신규 8: A7/A8
  합성 컨테이너 2개, D7/D8 실제 에이전트 2개, 나머지는 기존 카테고리의
  재실행). 신규 검사 중 "에이전트가 osiris-omniroute에 직접(프록시 없이)
  도달 불가"가 합성/실제 양쪽 모두 PASS.
- **#5'**: 서브패스는 실측으로 불가 확정(§44). 루프백 포트
  (`127.0.0.1:20129`, `osiris-omniroute-lo` 다리 경유)가 동작하고 20128과
  겹치지 않음을 확인. 외부 인터페이스에서는 애초에 라우팅이 없다
  (`osiris-omniroute-lo`도 host의 `127.0.0.1` 바인드만 publish, 컨테이너
  자체는 dev-net에 없음).
- **#6**: §42에 실측 근거(소스 열람 + 실행 로그) 기록. (a) 채택.
- **#7**: `https://osiris.example.com/` 200. 회귀 없음(omniroute 307/
  `<svc-a>` 200 확인). `<upstream-proxy>`의 `RestartCount=1`/`StartedAt=05:43`은 이
  세션 시작 **이전** 시각(세션 첫 조회 시점에 이미 "Up 7시간"이었고, 공용
  omniroute의 `StartedAt`도 정확히 같은 시각 — 호스트/dockerd 재시작 등
  이 세션과 무관한 사전 이벤트의 흔적이지 이 작업이 유발한 것이 아니다).
- **#8**: `docker compose down && up -d` → 8개 OSiris 서비스 전부
  healthy, `osiris-omniroute`의 SQLite(별칭 3개)가 named volume에 보존되어
  재기동 후에도 `/v1/models`에 그대로 노출됨을 확인, 고아 컨테이너 0개.
- **#9**: `git -C <host-infra> status --porcelain | sha256sum` —
  작업 전/후/최종 3회 확인, 전부
  `fdb304d3597f433db780764f7299fbd9ce0d95e9f632ccb52bef210cfd856532`로 불변.
- **#10**: `bin/lint-driver-boundary.sh` PASS, `bin/lint-health-invariant.sh`
  PASS(둘 다 이번 작업이 건드리지 않은 코드 경로이므로 회귀 없음 확인
  차원에서 재실행).
- **#11**: `bin/configure-omniroute.sh` 연속 2회 실행 → combo/키 개수 불변
  (§41).
- **#12**: 별칭 등록 후 `GET /v1/models`에 `osiris/planner`·`osiris/worker`·
  `osiris/cheap` 세 개 모두 노출 확인(무인증 GET). 실제 추론(진짜
  Anthropic 키로 `/v1/messages` 왕복)은 `[미검증]` — 사용자의 실제 키를
  이 세션이 만들지도, 대신 채우지도 않았다.

## 48. 임의로 정한 세부 결정 (감독이 알아야 할 것)

1. **osiris/planner→claude-opus-4-1, osiris/worker→claude-sonnet-4-5,
   osiris/cheap→claude-haiku-4-5** 매핑. 설계는 별칭 이름 세 개만 못박고
   실제 모델은 지정하지 않아 이 워커가 정했다 — `omniroute/config.json`
   한 곳만 고치면 사용자가 바꿀 수 있다.
2. **`egress/squid.conf`의 `llm_provider_domains` 목록**(Anthropic/
   OpenAI/Google/Azure OpenAI/Mistral/Cohere/Groq/OpenRouter/Together/
   DeepSeek/Perplexity/xAI). omniroute가 "160+ providers"를 표방해 전체
   목록을 미리 알 수 없으므로, 실사용 가능성이 높은 주요 프로바이더로
   시작하고 확장 절차(허용목록 한 줄 추가 + 무중단 재시작)를 문서화하는
   쪽을 택했다.
3. **`osiris-omniroute-lo`를 별도 non-internal 네트워크 + socat 다리로
   구현**(§43). 더 단순해 보이는 대안(옛 `omniroute`처럼 `osiris-omniroute`
   자신에게 두 번째 네트워크를 주는 것)을 기각하고 다리 컨테이너를 추가한
   것은 이 워커의 판단이다 — A3 예외를 만들지 않기 위한 트레이드오프
   (컨테이너 하나 추가 vs egress 우회 인터페이스 하나 추가) 중 전자를
   택했다.

## 49. 설계와 어긋났거나 구현 불가였던 것

- **서브패스 마운트(§2.9의 `/omni/` 행)를 채우지 못했다** — 벤더 이미지의
  Next.js `basePath`가 빌드 타임 전용이라 실측으로 불가 확정(§44). 다음
  워커가 소스 빌드로 간다면 채울 수 있는 자리로 남겨 뒀다.
- **§8.4가 요구하는 "에이전트별 24h 토큰"은 여전히 미구현** — I2/I4가
  이미 기록한 격차 그대로(core에 `LLMGateway.IssueToken()` 어댑터가
  없음). I5는 그 자리를 메우는 대신, 정적 `OSIRIS_GATEWAY_TOKEN` 하나를
  omniroute API로 자동 발급/동기화하는 것까지만 했다 — "발급 자동화"는
  됐지만 "에이전트별 회전"은 아직 없다.
- **실제 LLM 추론은 `[미검증]`으로 남았다**(§47 #12) — 사용자의 실제
  프로바이더 키가 없어서다. 경로(egress→omniroute→프로바이더, 별칭 해석,
  API 자동 구성)는 전부 실측 확인됐고 키 한 줄만 남았다.

---

# 구현 노트 — I5 2차 수정 (수렴 결함, 프로바이더 자격증명 경계, 온보딩 재배치, 서브패스 필수화, 이식성)

> §38~§49(I5 최초 구현)는 그대로 두고, 여기부터 감독의 코드 리뷰 + 사용자의
> 네 차례 연속 정정을 반영하며 내린 판단을 append한다. 이 절은 §38~§49가
> 기록한 최초 설계의 **상당 부분을 뒤집는다** — 특히 §41("적용 스크립트가
> config.json을 부트스트랩에서 자동 적용")과 §9.4(구 판)는 이제 **틀렸다**.
> 최초 판을 지우지 않고 append하는 이유는 이 문서 자체의 관례(§1 헤더) —
> "어떻게 여기 도달했는지"를 남기는 게 "무엇이 맞는지"만큼 가치 있다.

## 50. 결함 1 — 적용 스크립트가 수렴하지 않았다 (create-only)

**감독의 실측 지적**: `omniroute/config.json`의 모델 매핑을 수정한 뒤
`bin/configure-omniroute.sh`(당시 이름)를 두 번 실행 — 둘 다 성공, 별칭
개수도 3개로 유지됐지만 **실제 매핑은 그대로**였다. 원인: 별칭(combo)
로직이 "이름이 이미 있으면 아무것도 안 한다"였다 — 이러면 config.json을
고쳐도 반영되지 않아 선언 파일이 곧 거짓이 된다.

**정정**: 멱등성의 정의를 "두 번 실행해도 중복이 안 생긴다"에서 **"실행
후 실제 상태가 선언과 일치한다"**로 바꿨다. 별칭은 이름으로 조회한 뒤,
실제 `models[0]`의 `"<provider>/<model>"`(combo의 wire 표현 — POST로 만든
combo를 다시 읽어 확인함)이 선언과 다르면 `PUT /api/combos/{id}`로
갱신한다. 프로바이더의 `apiKey`는 애초에 비교 없이 항상 덮어썼으므로
(blind overwrite) 이 결함이 없었다 — 결함은 별칭에만 있었다.

**실측(왕복 테스트, §51 이후 최종 파이프라인 기준 재실행)**: `osiris/worker`
를 `claude-sonnet-5` → `claude-opus-5` → `claude-sonnet-5`로 왕복시키며
매번 `upsertAlias()` 호출 → 각 단계 후 **SQLite 원본을 직접 열어**
(`better-sqlite3`로 `combos` 테이블의 `data` 컬럼을 파싱, API 응답이 아니라
디스크 파일 자체) `models[0].model` 값이 선언과 일치함을 확인:
```
osiris/worker -> [{"model":"anthropic/claude-sonnet-5", ...}]
```
API의 자체 보고("converged"/"inSync")만 보는 게 아니라 DB를 직접 열어본
것 — 이게 원래 결함을 놓친 이유였다("별칭 개수 3개 유지"만 확인하고 내용은
확인하지 않았다).

## 51. 결함 2 — 프로바이더 자격증명을 OSiris가 들고 있었다

**사용자 지적**: *"그 API 키 관리하는 역할을 하려고 omniroute를 도입하는
건데 어불성설임."* 최초 구현은 `omniroute/config.json`의 `apiKeyEnv`가
`.env`의 `ANTHROPIC_API_KEY`를 가리키게 하고, 적용 스크립트가 그 값을 읽어
omniroute로 밀어넣었다. omniroute를 도입한 이유 자체가 프로바이더 자격증명을
한 곳에서 관리하기 위함인데, OSiris(`.env`, 백업, 배포 파이프라인)가 그
키를 또 들고 있으면 시크릿 보관 지점이 하나 늘어난 것뿐이다.

**바로잡은 경계(10-core-architecture.md §9.5에 기록)**: omniroute가
프로바이더 자격증명의 유일한 소유자다. OSiris는 자기 게이트웨이 토큰
(`OSIRIS_GATEWAY_TOKEN` — omniroute가 OSiris에게 발급한 것)만 갖는다.
`.env`/`.env.example`/`config.json`/스크립트/문서에서 프로바이더 키 취급을
전부 제거했다 — grep으로 확인(최종 결과는 §59).

**어디로 갔는가**: 처음엔 "1회성 독립 헬퍼"(`omniroute/register-provider.mjs`,
표준입력으로 키를 받아 마스킹 표시하며 등록)를 만들었으나, 바로 다음
정정(§52)에서 "이것도 부트스트랩 스크립트 패턴이니 온보딩 오퍼레이션으로
흡수하라"는 지시가 와서 **삭제하고 `omniroute/operations.mjs`의
`registerProviderCredential()`로 흡수**했다. 그 함수는 호출자(미래의 온보딩
파이프라인)가 건넨 apiKey를 omniroute 관리 API로 전달만 하고 어디에도
쓰지 않는다(파일 write 없음 — 코드에 `writeFileSync`/`appendFileSync` 호출이
그 함수 경로에 없음, grep으로 확인 가능).

## 52. 결함 3(근본) — 부트스트랩이 온보딩의 일을 미리 해버렸다

**사용자의 근본 정정**: *"온보딩에서 결정할 것을 미리 하지 마. 온보딩에서
API 키랑 넣고 스택에 맞게끔 시작돼야 맞다고 봄... 이건 단일 이미지로
구성된 '프레임워크'야. MSA 형태일 뿐이지."*

최초 구현은 `osiris-up`(부트스트랩)이 `bin/configure-omniroute.sh`를 직접
호출해 프로바이더/별칭/게이트웨이 토큰을 전부 즉시 적용했다. 이건 "빈
스택을 띄우는 것"과 "조직을 구성하는 것"을 섞은 것이다 — 후자는 온보딩의
일이다.

**정정**:
1. `osiris-up`은 이제 osiris-omniroute에 **아무것도 적용하지 않는다**
   (5c단계는 순수 설명 주석으로 바뀜, 호출 없음).
2. `bin/configure-omniroute.sh`(셸 스크립트)와 `omniroute/apply-config.mjs`
   를 **삭제**했다 — "셸 스크립트가 아니라 온보딩 파이프라인이 프로그램적
   으로 부를 수 있는 형태여야 한다"는 지시에 따라, 로직을
   `omniroute/operations.mjs`로 재배치했다(아래 §53).
3. **실측(완료조건 (b))**: `osiris_osiris-omniroute-data` 볼륨을 삭제하고
   (우리 소유 볼륨, 파괴적이지 않음 — 프로덕션 공용 omniroute와 무관) 다시
   `docker compose up -d`만 실행 → 전 서비스 healthy → omniroute의 관리
   API로 직접 조회:
   ```
   combos: 0 []
   providers: 0
   keys: 0
   ```
   부트스트랩이 정말 아무것도 쓰지 않았다는 증거다.

## 53. `omniroute/operations.mjs` — 오퍼레이션과 그 계약

**배치 결정**: Node 모듈(`omniroute/operations.mjs`)로 만들었다 — Go로
쓰지 않은 이유는 (a) omniroute API와의 실제 왕복 테스트를 이미 Node
`fetch`로 전부 검증해 둔 상태였고, (b) `osiris-core`(Go)가 이걸 직접
import할 필요는 없다 — 서브프로세스로 부르는 교차 언어 계약(`node
omniroute/operations.mjs <op>`, stdin/stdout JSON 한 줄)도 같이 제공해
어떤 언어의 온보딩 파이프라인이든 부를 수 있게 했다. 온보딩 UI/대화 흐름은
만들지 않았다(범위 밖 — 다음 워커).

**오퍼레이션 6개** (전부 async, 구조화된 결과 반환, 치명적 오류에서만
throw):
- `registerProviderCredential({name, provider, apiKey})` — 프로바이더
  등록/갱신. apiKey는 절대 저장하지 않고 전달만.
- `upsertAlias({name, providerName, provider, model})` /
  `upsertAliases(entries)` — 별칭 수렴(§50). 참조된 프로바이더가 없으면
  `{ok:false, providerMissing:true}` — 생성/갱신을 시도하지 않는다(키를
  들고 프로바이더를 만들려 들지 않는다는 원칙의 구현).
- `issueOrGetGatewayToken({name, scopes})` — OSiris 자신의 게이트웨이 토큰
  발급/재조회. 프로바이더 키가 아니라 omniroute→OSiris 발급물이라 이
  모듈이 다뤄도 되는 자격증명이다.
- `applyGatewayTokenToEnv(token, {envFile})` — `.env` 반영은 별도
  오퍼레이션으로 분리(자동으로 불리지 않음 — 호출자가 결정).
- `getConfigurationStatus()` — 지금 무엇이 있고 무엇이 비었는지 읽기
  전용 보고(`30-onboarding-provisioning.md`의 `GET /onboarding/status`와
  맞물리는 지점, 다음 워커 인계).
- `getDefaultAliasMapping()` — `config.json`을 "적용되는 선언"이 아니라
  "온보딩이 제시할 기본값"으로 읽는다(§54).

**`omniroute/config.json`의 성격 재결정**: 제거하지 않고 **기본값
(default)으로 남겼다** — 별칭 이름 세 개는 설계 §9.2 계약으로 고정이지만
각각이 가리킬 모델은 온보딩이 대표에게 확인받아 정하는 게 맞다. 이 파일은
그 확인 단계의 시작값(현재: Anthropic Opus 5 / Sonnet 5 / Haiku 4.5)을
담을 뿐, 어떤 부트스트랩 경로도 자동으로 적용하지 않는다. 감독이 고친
모델 ID는 그대로 유지했다(되돌리지 않음).

**감독이 발견한 순환 문제 — 인계**: `30-onboarding-provisioning.md`는
온보딩 대화를 임시 제네시스 에이전트 `bootstrap-master`가 맡는다고
결단했는데, 그 에이전트도 LLM을 쓰려면 게이트웨이가 이미 구성되어
있어야 한다 — "게이트웨이를 구성하는 대화"를 "게이트웨이가 필요한
에이전트"가 진행하는 순환이다. **해소 방향(이 워커의 판단이 아니라
인계)**: W3가 이미 "LLM은 `OrgDefinitionDraft`까지만, 나머지는 결정적
코드"라고 정했다 — 같은 논리로 **온보딩 0단계(프로바이더 키 투입 → 별칭
확정 → 게이트웨이 토큰 발급)는 LLM 없는 결정적 단계**여야 하고, 온보딩
콘솔(폼 UI)이 사람에게서 직접 키를 받아 위 오퍼레이션들을 호출한 뒤에만
`bootstrap-master`의 대화형 온보딩이 시작될 수 있다. `30-onboarding-
provisioning.md`를 실제로 고치는 것은 온보딩 워커의 몫이다 — 이 워커는
고치지 않았다.

## 54. 서브패스가 필수 요구사항으로 승격 — 소스 재빌드로 성립시켰다

**사용자 지시(3차 정정)**: *"osiris.example.com/omni → 여기로 omniroute UI를
서브URL로 라우팅하기는 해야함."* §I5 최초 판(§44)이 "서브패스 불가,
루프백 포트로 폴백"이라고 판정한 것 자체는 실측으로 정확했다(벤더
사전빌드 이미지의 `basePath`는 빌드 타임에만 굳는다) — 그러나 **전제가
바뀌었다**: 서브패스가 이제 선택이 아니라 요구사항이므로 다른 경로를
찾아야 했다.

**재조사 순서 1 — 소스 빌드가 가능한가**: 벤더 이미지(다이제스트 고정) 안을
열람한 결과, `/app`에 **전체 소스 트리**(src/, open-sse/, package.json,
package-lock.json, 벤더 자신의 `Dockerfile`까지)가 그대로 있었다 — Next.js
standalone 출력이 트레이싱된 서버 번들만이 아니라 원본 소스까지 보존하고
있었다(일부 코드 경로가 런타임에 `tsx`로 동적 실행되기 때문으로 추정).
**단, `node_modules`는 프로덕션 트레이싱으로 가지치기되어 있어 `next`의
빌드 컴파일러 자체(`next/dist/bin/next`)가 빠져 있었다** — 실측: 벤더
이미지 안에서 그대로 `npm run build`를 실행하면 즉시
`Cannot find module '/app/node_modules/next/dist/bin/next'`로 죽는다.

**해법**: `omniroute-build/Dockerfile`을 새로 만들어, 벤더 이미지를
`FROM <다이제스트> AS vendor`로 참조하고(별도 git clone 없음 — 다이제스트
문자열 하나가 재현성의 닻이다), 벤더의 `package-lock.json`으로 **새로**
`npm ci`(네트워크 필요 — 이건 빌드 타임 작업이라 A3와 무관, 배포된
스택의 런타임 네트워크 토폴로지와 별개다)를 돌려 완전한 빌드 툴체인을
확보한 뒤, 벤더에서 `node_modules`를 제외한 소스만 덮어씌우고
`OMNIROUTE_BASE_PATH=/omni`(벤더 Dockerfile과 다른 유일한 한 줄)를 주고
`npm run build`. 런너 스테이지는 벤더의 `runner-base`를 그대로 재현.

**실측 성공**(전체 빌드 로그 확인, exit 0): 재빌드된 이미지를 실제로
부팅해 확인:
```
GET /            -> 404   (basePath가 진짜로 적용됨 — 루트가 이제 안 먹힘)
GET /omni/       -> 200
GET /omni/login  -> 200
GET /omni/api/auth/login (POST) -> 200 {"success":true}
GET /v1/models   -> 404
GET /omni/v1/models -> 200 {실제 모델 목록}
```
정적 에셋도 확인: HTML의 `href="/omni/_next/static/chunks/....css"` 등
모든 에셋 경로가 `/omni/` 프리픽스로 정확히 나오고, 그 URL을 직접 fetch해
200을 받았다(HTML 200만으로는 불충분하다는 지적에 대한 실측 응답).

**`osiris-omniroute-lo`(socat 루프백 브리지) 제거**: 서브패스가 성립해
osiris-omniroute 전용 호스트 포트가 필요 없어졌다. `docker-compose.yml`,
`docker-compose.dev-net.yml`(해당 없음), 네트워크 정의에서 전부 제거했다.

## 55. omniroute 자체 인증 강도 — 실측(코드 확인)

`/omni/`가 이제 공개 라우팅 경로가 되므로(온보딩이 실제 프로바이더 키를
여기 넣는다) 자체 인증 강도를 추측 없이 코드로 확인했다:
- 인증 모델: 관리자 비밀번호 **1개**(`INITIAL_PASSWORD` → bcrypt 해시
  영속화) → `POST /api/auth/login` → JWT 쿠키(HS256, 30일 만료,
  `httpOnly`).
- 브루트포스 방어 **있음**: `checkLoginGuard`/`recordLoginFailure`가
  IP 기준 시도 횟수를 추적해 429 + `Retry-After`를 반환한다(로그인 라우트
  코드에서 직접 확인).
- **없는 것**: MFA, IP allowlist, 세션 동시 접속 제한(계정 자체 레벨),
  비밀번호 복잡도 강제. 단일 정적 비밀번호 + 브루트포스 가드가 전부다.
- 결론: 공용 `omniroute.example.com`가 이미 지금 이 방식 그대로 인터넷에 떠
  있으므로(이 세션이 관찰만 한 기존 사실) 이 인증 강도로 노출하는 것
  자체는 **회귀가 아니다.** 다만 온보딩이 진짜 키를 넣는다는 사실이 위험도
  판단을 다시 요구할 수 있다는 점을 `gw/conf.d/default.conf`에
  게이트웨이 레벨 Basic Auth를 **얹을 수 있는 자리**(주석 처리, 기본
  비활성)로 남겨 뒀다 — 활성화는 사용자 결정.

## 56. 이식성 — dev-net·도메인·호스트 포트 전부 배포 시점 정보로

**사용자의 근본 정정**: *"server_name 같은 것들 아직 지정할 수 없어야
해... 나는 내 사이트에 구성하는 게 아니라 프레임워크 → 어디서든 사용
가능한 도구를 만드는 거야."*

**바로잡은 것**:
- `gw/conf.d/default.conf`: 두 서버 블록(하나는 `server_name osiris.example.com`
  로 도메인 고정, 하나는 그 외 전부 444)을 **catch-all 서버 블록 하나**로
  합쳤다 — `server_name _;`, 어떤 Host 헤더로도 동일하게 응답한다. 도메인
  문자열은 코드가 아니라 주석의 "이 호스트의 예시"로만 남는다.
- `docker-compose.yml`: `dev-net`을 최상위에서 제거했다. 대신 신설
  `osiris-outbound`(일반 브리지, Docker 기본 NAT로 인터넷 라우팅, 외부
  네트워크 의존 없음)를 osiris-egress 전용으로 만들었다 — I5 이후
  `osiris-omniroute`가 전용 인스턴스로 내부화되어 dev-net을 거쳐야 할
  목적지가 아예 없어졌으므로, osiris-egress도 dev-net을 완전히 떠날 수
  있었다(A10 근거 재갱신 — §9.0에 이미 기록한 "github/npm/pypi가 유일한
  존재 이유"조차 osiris-egress 쪽에서는 소멸, 그 근거는 이제 **오버레이의
  osiris-gw에만** 남는다).
- `docker-compose.dev-net.yml`(신설, 선택적 오버레이): `osiris-gw`에만
  `dev-net`을 추가하는 것이 전부다. 이 호스트처럼 상위 리버스 프록시
  (상위 리버스 프록시류)를 쓰는 배포에서만 `-f` 플래그로 함께 적용한다.
- `osiris-gw`: 기본적으로 `127.0.0.1:${OSIRIS_GW_HOST_PORT}`에 host 포트를
  게시한다. **실측 함정 둘**:
  1. 이 값을 처음 `8080`으로 잡았다가 `8180`으로도 바꿔봤지만 **둘 다
     이 WSL2 호스트에서 Docker의 포트 게시 자체가
     `bind: An attempt was made to access a socket in a way forbidden
     by its access permissions`로 실패했다** — 순수 리눅스 소켓
     bind(파이썬으로 확인)는 됐는데 Docker의 포트 포워딩만 실패하는 것으로
     보아 WSL2/Docker Desktop 특유의 포트 예약 문제로 판단, 실측으로 다른
     후보 포트들을 시도해 `18280`이 안정적으로 동작함을 확인하고 그것을
     프레임워크 기본값으로 채택했다. **다른 호스트에서는 다른 포트가
     막혀 있을 수 있다** — `.env`의 `OSIRIS_GW_HOST_PORT`로 바꾸면 된다.
  2. 그 문제를 다 해결한 뒤에도 **osiris-gw 자신이 여전히 응답하지
     않았다** — 원인은 다른 것: `osiris-edge`가 `internal:true`라
     osiris-omniroute-lo 때(§43) 이미 겪은 바로 그 Docker 제약(내부 전용
     네트워크에만 붙은 컨테이너는 host 포트가 실제로 안 열림)이 osiris-gw
     에도 그대로 적용됐다. `osiris-edge`를 internal에서 빼면 그 격리
     의미가 사라지므로, 대신 신설 `osiris-public`(non-internal, 일반
     브리지)을 osiris-gw에 하나 더 붙였다 — osiris-omniroute 때와 달리
     여기선 두 번째 인터페이스가 A3 우회 위험을 만들지 않는다(osiris-gw는
     애초에 아웃바운드 호출도, 프로바이더 자격증명도 없는 수동 리버스
     프록시이기 때문).
- `PUBLIC_ORIGIN` 기본값을 `https://osiris.example.com`에서
  `http://localhost:${OSIRIS_GW_HOST_PORT}`로 바꿨다(`.env.example`).
- `osiris-up`: `DEV_NET_NAME`이 비어 있으면(기본) dev-net 오버레이를 아예
  적용하지 않고 표준 설치로 진행한다. 값이 있는데 네트워크가 없으면
  죽는다(조용히 표준 설치로 대체하지 않음 — 사용자가 명시적으로 그
  통합을 의도했다는 뜻이므로).
- `bin/verify-isolation.sh`: dev-net이 없으면 그에 의존하는 검사(B1,
  C1의 dev-net 절반)를 **SKIP으로 명시 출력**하고 나머지는 그대로
  수행한다.

**온보딩이 결정할 항목(목록, 다음 워커 인계 — 이 워커는 UI를 만들지
않았다)**:
1. 공개 도메인 / `PUBLIC_ORIGIN` — 언제, 어떤 값으로 바꿀지.
2. 상위 게이트웨이를 앞에 둘지 여부, 둔다면 `docker-compose.dev-net.yml`
   류의 오버레이를 어떻게 적용할지(현재는 `.env`의 `DEV_NET_NAME` + 재적용
   — 온보딩이 이 값을 쓰고 `docker compose`를 다시 실행하는 흐름이 될
   것으로 예상하나, 그 자동화는 만들지 않았다).
3. TLS 종단 위치(상위가 있으면 거기서, 없으면 없음 — 인터넷에 그대로
   노출할지는 사용자 판단).
4. 프로바이더 자격증명(§51), `osiris/*` 별칭의 실제 모델 매핑(§53),
   게이트웨이 토큰 발급(§53) — `omniroute/operations.mjs`가 오퍼레이션을
   제공.

## 57. 실측으로 발견한 라우팅 함정 — `/omni`(트레일링 슬래시 없음)

`osiris-gw`를 통해 `/omni/`(슬래시 있음, 우리 nginx location이 매칭)를
치면 백엔드가 자체적으로 `308 → /omni`(슬래시 제거, Next.js의 자체
정규화) → `307 → /omni/dashboard`(또는 미인증 시 `/omni/login`)로
리다이렉트한다. **문제**: 그 중간 홉 `/omni`(슬래시 없음)는 우리 nginx의
`location /omni/`(prefix, 슬래시 필수)에 매칭되지 않아 정적 콘솔 슬롯으로
잘못 떨어졌다 — 실측으로 `Location: http://127.0.0.1/omni/`(포트 없는
절대 URL, nginx 자신의 정적 파일 리다이렉트 형태)를 확인해 발견했다.
**수정**: `location = /omni { proxy_pass ...; }`(정확히 이 경로만) 하나를
추가해 그 중간 홉도 백엔드로 프록시했다. 이후 전체 체인이 `/omni/login`
(또는 인증된 경우 `/omni/dashboard`)에서 200으로 끝남을 재확인했다.

## 58. `OSIRIS_GATEWAY_BASE_URL`도 `/omni` 접미사가 필요해졌다

서브패스 재빌드(§54) 이후 osiris-omniroute의 **모든** 경로(`/v1/messages`
포함)가 `/omni` 프리픽스를 요구한다 — 에이전트가 그동안 쓰던
`OSIRIS_GATEWAY_BASE_URL=http://osiris-omniroute:20128`(프리픽스 없음)로는
`/v1/messages`가 404가 났을 것이다(에이전트 supervisor의
`task.go`/`probe.go`는 `baseURL + path` 단순 문자열 접합이라 이 실측이
그대로 적용됨, 코드 확인). **수정**: 기본값을
`http://osiris-omniroute:20128/omni`로 바꿨다(`docker-compose.yml`,
`.env`, `.env.example`) — `OSIRIS_GATEWAY_HEALTH_PATH=/v1/models`는
그대로 두어도 `baseURL + healthPath` 접합 결과가 `.../omni/v1/models`로
자동으로 맞다.

## 59. 완료조건 실측 결과 (2차 수정분, 전부 실행 기반)

- **오퍼레이션 배치**: `omniroute/operations.mjs`(§53). 근거는 위 §53.
- **`config.json` 결단**: 기본값으로 유지, 자동 적용 없음(§53).
- **부트스트랩 무구성 DB 증거**: §52 — combos/providers/keys 전부 0.
- **수렴 왕복 테스트**: §50 — SQLite 직접 조회로 `osiris/worker`가
  선언과 매번 일치함을 확인(claude-sonnet-5 → claude-opus-5 →
  claude-sonnet-5).
- **프로바이더 키 grep 0건**: `grep -rn "ANTHROPIC_API_KEY\|apiKeyEnv"`가
  `.env`/`.env.example`/`config.json`/`omniroute/*.mjs`/`docker-compose*.yml`
  어디에도 안 걸림(00-research-prior-art.md의 무관한 사전 언급 1건과
  이 문서의 역사 기록만 남음 — 후자는 append-only 원칙상 지우지 않음).
- **서브패스**: §54 — `/omni/` 실제 로드(HTML+에셋+API 전부 200), 벤더
  이미지 대비 유일한 차이가 `OMNIROUTE_BASE_PATH=/omni` 한 줄임을 Dockerfile
  로 증명.
- **omniroute 자체 인증 강도**: §55 — 단일 비밀번호 + JWT + 브루트포스
  가드, MFA/IP제한 없음(코드 확인, 추측 아님).
- **이식성**: §56 — `DEV_NET_NAME` 미설정 상태로 `docker compose up -d`
  (오버레이 없이) → 7개 서비스 전부 healthy, `http://127.0.0.1:18280/`
  200, `/pre-status` 200, `/omni/`(리다이렉트 체인 끝) 200. 이후 dev-net
  오버레이를 다시 적용해 이 호스트의 기존 `https://osiris.example.com/`(200)·
  `omniroute.example.com`(307)·`<svc-a>.example.com`(200) 무회귀 확인.
- **격리·린트**: `bin/verify-isolation.sh` 13/13(dev-net 오버레이 활성
  상태 기준, 실제 에이전트 없어 D절 SKIP), `bin/lint-driver-boundary.sh`/
  `bin/lint-health-invariant.sh` PASS.
- **프로덕션 무영향**: 공용 `omniroute` 컨테이너 ID·RestartCount(0)·
  StartedAt 불변(작업 시작 시점과 동일), 볼륨 `omniroute_omniroute_data`
  무관.
- **`<host-infra>` 해시**: 작업 도중 `?? OSiris`(사용자가 22:27에
  직접 만든 심링크, `../OSiris`를 가리킴, 이 워커가 만든 것 아님 — 감독이
  최초에 "저장소 밖 쓰기"로 오판했다가 철회함)로 인해 기준 해시가
  `fdb304d3...`에서 `8ac79cc6...`로 갱신되었다. **이 워커가 최종 확인한
  값(`92557f21a53f17154109b4f6b00885708dadda3546505857ce3fa68473f99a73`)은
  갱신된 기준과도 다르다** — `<host-infra>`는 OSiris와 무관한 수십
  개 프로젝트가 함께 사는 살아있는 저장소라
  이 세션 도중에도 무관한 변경이 쌓였을 수 있다(이 워커의 `pwd`는 시종
  `<repo-root>`였음을 확인했고, `<host-infra>` 안의 어떤 파일도
  절대경로로도 건드리지 않았다). **이 불일치는 감독에게 그대로 보고하고
  이 워커가 임의로 해석하거나 되돌리지 않는다** — §60 참고.

## 60. 임의로 정한 세부 결정 (2차 수정분)

1. **`osiris-omniroute`의 기본 `OSIRIS_GW_HOST_PORT`를 `18280`으로
   확정.** `8080`/`8180` 둘 다 이 호스트에서 Docker 포트 게시가 OS
   레벨로 거부되어(WSL2 특유 문제로 추정) 실측으로 여러 후보를 시험한 뒤
   정했다. 다른 호스트에서 이 값이 막혀 있을 수 있으므로 `.env`로 바꾸는
   것을 전제로 한다.
2. **`osiris-gw`에 `osiris-public`(non-internal) 네트워크를 추가로
   붙이는 것으로 host 포트 게시 문제를 풀었다** — `osiris-edge`를
   internal에서 빼는 대신(격리 의미 훼손) 두 번째 망을 얹는 쪽을
   택했다(§56, §43과 같은 판단 패턴 재사용).
3. **`omniroute-build/Dockerfile`이 npm 레지스트리에 네트워크로
   접근하는 것을 A3 위반으로 보지 않았다** — 그건 호스트에서
   `docker build` 시점에 일어나는 일이지, 배포된 스택의 런타임 네트워크
   토폴로지(에이전트/omniroute의 아웃바운드)와 무관하다는 판단.

## 61. 설계와 어긋났거나 구현 불가였던 것 (2차 수정분)

- **설계 문서 §2를 완전히 다시 쓰지는 않았다** — §2.0을 새로 추가해
  일반 계약을 명시하고 §2.1~§2.8을 "이 호스트의 예시 배포" 부록으로
  재라벨링하는 방식을 택했다(전면 재작성은 문서 전체에 걸친 `§2.x`
  교차 참조를 대량으로 깨뜨릴 위험이 있어 피했다). 다음 워커가 원한다면
  §2.1~§2.8 자체를 별도 부록 파일로 물리적으로 분리할 수 있다.
- **게이트웨이 레벨 Basic Auth를 실제로 켜지 않았다** — 라우팅과 활성화
  스캐폴드(주석 처리)만 두고, 켤지 말지는 사용자 결정으로 남겼다(§55).
- **`<host-infra>` 해시 불일치(§59)를 이 워커가 해소하지 못했다** —
  원인이 이 워커의 작업 범위 밖(다른 프로젝트들의 무관한 변경)일 가능성이
  높다고 판단하지만, 확정할 방법이 없다(그 저장소를 더 들여다보는 것
  자체가 범위 밖이므로).

---

# 구현 노트 — I5 3차 수정 (`/api/` 리터럴 경로 버그, 라우팅 재설계, 관리자 비밀번호 재발 수정)

> §50~§61(I5 2차 수정)은 그대로 두고, 여기부터 감독의 브라우저 실측 지적
> 두 건(로그인이 `/api/`로 나가 실패, 관리자 비밀번호가 몰래 바뀐 줄
> 알았다)을 반영하며 내린 판단을 append한다.

## 62. 결함 — 클라이언트가 리터럴 `/api/`를 쓴다 (실측: 감독)

감독이 실제 브라우저로 `/omni/`에 로그인해보니 `https://osiris.example.com
/api/auth/login`으로 POST됐다 — `/omni/api/auth/login`이 아니라. 이전
검증(§54)은 curl로 정확한 경로(`/omni/api/...`)를 직접 쳤기 때문에 이걸
놓쳤다 — **클라이언트가 실제로 어느 URL을 만드는지는 검증하지 않았다.**

**원인(소스 확인)**: Next.js `basePath`는 `next/link`·라우터·정적 에셋
(`/_next/...`)에는 자동 적용되지만 코드 안의 리터럴 문자열
(`fetch("/api/...")`)에는 붙지 않는다. `/app/src`를 grep한 결과:
- `/api/`가 리터럴로 박힌 파일 **233개**(`fetch(["'`로 시작하는 것만 카운트,
  실제 hardcoded 문자열 총량은 더 큼 — 예: `/api/settings` 82곳,
  `/api/providers` 22곳, `/api/settings/compression` 13곳 등).
- 중앙 API 헬퍼가 없다 — `basePath`를 참조하는 코드는 i18n 번역 파일뿐.

## 63. 조사 — `/api/` 밖에도 리터럴 절대경로가 더 있었다

지시받은 대로 `/api/` 외의 절대경로 리터럴을 조사했다(추측 없이
grep으로):

**데이터 호출(`fetch(...)`)**:
- `fetch("/v1/models")` — 7곳(Playground `ApiTab.tsx`, `cli-code`의
  `ToolDetailClient.tsx`/`CopilotToolCard.tsx`, `search-tools`의
  `RerankPanel.tsx`, `api-manager/ApiManagerPageClient.tsx`,
  `endpoint/EndpointPageClient.tsx`).
- `fetch("/v1/web/fetch", ...)` — 1곳(`useScrapeFetch.ts`).
- `fetch("/a2a", ...)` — 2곳(`A2ADashboard.tsx`).
- `axios.*`/`new EventSource(...)`/`new WebSocket(...)` 형태의 리터럴
  절대경로: **0건**(grep 결과 없음).

**순수 내비게이션 링크(`<a href="/...">`, 데이터 호출 아님)**: `/dashboard/
providers`(16곳), `/dashboard/context/settings`(6), `/status`(5),
`/docs`(5), `/dashboard/combos`(4), `/dashboard`(4), `/dashboard/settings`
(2), `/dashboard/providers/new`(2), `/dashboard/health`(2), `/dashboard/
cli-tools`(2), `/dashboard/cli-code`(2), 그리고 1개씩: `/terms`, `/privacy`,
`/offline`, `/maintenance`, `/login`, `/home`, `/forgot-password`,
`/openapi.yaml`, `/dashboard/usage`, `/dashboard/tools/traffic-inspector`,
`/dashboard/quota`, `/dashboard/playground`, `/dashboard/logs` 등.

**판단**: 개별 리터럴을 하나씩 화이트리스트에 추가하는 방식(처음엔 `/api/`,
`/v1/`, `/a2a`를 그렇게 했다)은 이 내비게이션 링크들을 보고 나서 포기했다
— "이 벤더 앱에 이런 리터럴이 몇 개나 더 있을지 모른다"는 게 명백해졌기
때문이다. §64에서 블랙리스트 방식으로 다시 설계했다.

## 64. `osiris-gw` 라우팅 재설계 — 화이트리스트에서 블랙리스트로

**최종 구조**: OSiris가 명시적으로 예약하는 경로는 정확히 다섯 부류뿐이다
— `= /`(콘솔 슬롯), `/_api/`(OSiris API), `/pre-status`(진단),
`/healthz`(내부 헬스체크), 브랜드 정적 파일 4개(`/favicon.ico`,
`/favicon.svg`, `/favicon-180.png`, `/osiris-mark.svg` — 콘솔
슬롯/진단 페이지가 실제로 참조하는 것만, grep으로 확인). **이 다섯 부류를
제외한 모든 경로**는 `location /` catch-all이 `/omni` 접두사를 붙여
omniroute로 넘긴다. `/omni/`(이미 접두사가 있는 요청) 자체는 이중 접두사를
피하기 위해 이 catch-all보다 먼저 오는 별도 location으로 잡는다.

**왜 이렇게 해야 하는가**: omniroute 소스를 한 줄도 고치지 않기로
결정했으므로(사용자 확정), 벤더 앱이 basePath를 어디서 놓치는지 전부
찾아내는 것은 이 벤더 앱의 업데이트마다 재발하는 두더지 잡기다.
"OSiris가 실제로 소유한 몇 개 경로만 뺴고 나머지는 기본적으로
omniroute"로 뒤집으면, 우리가 미리 grep하지 못한 리터럴도(이번 조사가
완전하지 않을 수 있다는 뜻이기도 하다) 자동으로 올바르게 도달한다.

**정적 파일 목록을 확장자 정규식으로 넓히지 않은 이유**: omniroute 자신도
`/providers/openclaw.png`처럼 루트 레벨 정적 이미지를 참조하는 리터럴이
있다(grep으로 발견) — 이것도 같은 부류의 버그이고, 우리 catch-all이
올바르게 처리해야 한다. `location ~* \.(ico|svg|png)$`처럼 확장자
기준으로 넓게 잡으면 이런 omniroute 자신의 정적 자산까지 가로채 우리 쪽
404로 잘못 응답하게 된다 — 그래서 우리 소유 파일 4개를 정확한 이름으로만
예약했다.

**실측 함정 — `proxy_pass`에 변수 + URI를 같이 쓰면 자동 접두사 치환이
깨진다**: 처음엔 `proxy_pass http://$omni_up/omni/api/;` 형태로 짰다.
nginx 공식 문서는 "location과 매칭되는 URI 부분이 proxy_pass의 URI로
치환된다"고 하지만, **업스트림이 변수(`$omni_up`)이면 이 자동 치환이
적용되지 않는다** — 실측: 그렇게 두고 `POST /api/auth/login`을 치면
백엔드가 `308 Location: /omni/api`(뒤 세그먼트 `/auth/login`이 통째로
사라진 상태)를 돌려줬다. 해결: 모든 리라이트 location에서 `rewrite
^/prefix/(.*)$ /omni/prefix/$1 break;`로 URI를 먼저 바꾸고
`proxy_pass http://$omni_up;`(URI 없이 변수만)으로 바꿨다 — 이건 항상
원본 요청 URI 전체를 그대로 넘기므로 rewrite가 만든 새 URI가 그대로
전달된다. 이 패턴을 catch-all(`/`)과 `/_api/`(OSiris API 자신) 양쪽에
동일하게 적용했다.

**WS/SSE 실측(추측 아님, 소스 확인)**: `/app/server-ws.mjs`가
`http.Server`의 `upgrade` 이벤트를 감싸고, `/app/responses-ws-proxy.mjs`
의 `RESPONSES_WS_PUBLIC_PATHS = {"/responses","/v1/responses",
"/api/v1/responses"}`가 WS 업그레이드 대상이다. `text/event-stream`
SSE는 `grep -rl "text/event-stream" src/app/api`로 확인: `/v1/messages/
route.ts`(에이전트가 실제 쓰는 그 엔드포인트), `/v1/completions/...`,
`/v1/responses/route.ts`, `/gamification/stream/route.ts` 등. 그래서
catch-all과 `/omni/` 블록 양쪽에 `map $http_upgrade $connection_upgrade`
(nginx 표준 WS 관용구) + `proxy_buffering off`/긴 타임아웃을 모두
넣었다.

## 65. 검증 결과 — 클라이언트 관점 (실행 기반)

- `POST http://127.0.0.1:18280/api/auth/login`(빈 바디) → **400**,
  `POST .../omni/api/auth/login` → **400**, 완전히 동일.
- `POST https://osiris.example.com/api/auth/login`(빈 바디) → **400**,
  `.../omni/api/auth/login` → **400**, 동일.
- **실제 로그인 → 후속 호출까지 완주**: `POST /api/auth/login
  {password}` → `200 {"success":true}` + `Set-Cookie: auth_token=...`.
  그 쿠키로 `GET /api/combos` → `200`(실제 combo 데이터 반환),
  `GET /api/keys` → `200`. localhost 경로와 `https://osiris.example.com`
  경로 양쪽에서 재현.
- `GET /v1/models`(공개) = `GET /omni/v1/models`(직접) — 둘 다 200,
  동일 응답.
- `GET /a2a`(공개) = `GET /omni/a2a`(직접) — 둘 다 405(POST 전용
  엔드포인트로 추정), 동일.
- `/_api/events` → 502(osiris-api가 없어 업스트림 연결 실패 — 이건
  예상된 정상 동작이다. **설정이 실제로 옮겨졌는지**는 conf 파일로
  확인: `/_api/` prefix 블록에 D20 SSE 설정 5종이 값 그대로 존재).
- 회귀 없음: `/`(콘솔 슬롯) 200, `/pre-status` 200, `/omni/` 로그인
  체인 끝까지 200(`/omni/login` 또는 `/omni/dashboard`).
- `bin/verify-isolation.sh` 13/13(에이전트 경로는 `osiris-gw`를 전혀
  거치지 않으므로 이번 변경과 무관 — 실제로 통과 그대로 재확인),
  두 lint PASS, 프로덕션 `omniroute`/`<upstream-proxy>` 무영향, `<svc-a>`/`<svc-b>`
  무회귀, 고아 0.

## 66. 결함 — 관리자 비밀번호를 설치 시점에 미리 구웠다 (재발)

**감독 지적**: 사용자가 "비밀번호가 바뀌었는데 나는 바꾼 적이 없다"고
느꼈다 — 실제로는 신설 인스턴스가 이 세션이 생성한 자체 비밀번호를 쓰고
있었을 뿐(프로덕션 공용 omniroute는 무관, 그 인스턴스의 `server.env`
mtime·RestartCount 불변으로 확인됨) 이었지만, **이건 "온보딩에서 결정할
것을 미리 하지 마라"는 지적(§52)의 재발**이다 — 프로바이더 키를
`.env`에 둔 것과 정확히 같은 실수를, 관리자 비밀번호에 대해서도 저질렀다.

```
docker-compose.yml (수정 전): INITIAL_PASSWORD: ${OSIRIS_OMNIROUTE_ADMIN_PASSWORD:?...}
.env (수정 전): OSIRIS_OMNIROUTE_ADMIN_PASSWORD=<이 세션이 생성한 값>
```

`:?`가 값을 강제하므로, 다른 호스트에 이 프레임워크를 설치하려는 사용자는
**스택이 뜨기도 전에 비밀번호를 발명해야 한다** — 방금 세운 이식성
원칙("설치하면 바로 localhost로 UI에 닿고, 그다음 온보딩")과 정면으로
어긋난다.

## 67. `INITIAL_PASSWORD` 미설정 시 실제 동작 — 실측(소스 + 실행)

**소스 확인** (`build/bootstrap-env.mjs`): `INITIAL_PASSWORD`가
비어 있거나 `"CHANGEME"`이면 부팅 로그에 경고를 남기지만, **`merged.
INITIAL_PASSWORD`에 실제로 `"CHANGEME"`를 대입하는 코드는 없다** — 로그
문구("using default 'CHANGEME'")는 그렇게 읽히지만 실제로는 그냥
경고일 뿐, 값은 그대로 빈 상태로 다음 단계(`managementPassword.ts`)에
전달된다.

**소스 확인** (`src/lib/auth/managementPassword.ts` +
`src/app/api/auth/login/route.ts`): 저장된 비밀번호 해시가 없고
`INITIAL_PASSWORD`도 없으면 `ensurePersistentManagementPasswordHash()`가
`{hash: null, source: "missing"}`을 반환하고, 로그인 라우트는 어떤
비밀번호를 넣어도 **403 `{"error":"No password configured. Complete
onboarding first.","needsSetup":true}`**를 돌려준다.

**소스 확인** (`src/app/login/page.tsx`): 로그인 페이지가 마운트되면
`GET /api/settings/require-login`을 먼저 호출해 `hasPassword`/
`setupComplete`를 확인한다. `false`면(또는 로그인 시도가 `needsSetup:
true`를 받으면) **자체 `/dashboard/onboarding` 화면으로 안내**한다 — 이
화면은 `POST /api/settings/require-login {requireLogin:true, password}`
로 사람에게서 직접 초기 비밀번호를 받고, 곧바로 `/api/auth/login`으로
자동 로그인한 뒤, 이어서 프로바이더 등록(`/api/providers`)까지 같은
마법사 안에서 진행할 수 있게 되어 있다(코드에서 확인 — omniroute
자신이 이미 프로바이더 온보딩 마법사를 내장하고 있다는 뜻이기도 하다).

**실행 확인(throwaway 인스턴스, `INITIAL_PASSWORD` 완전히 비움)**:
```
GET /api/settings/require-login -> {"requireLogin":true,"hasPassword":false,"setupComplete":false,...}
GET /dashboard/onboarding -> 200 (539986 bytes, 실제 렌더된 페이지)
POST /api/auth/login {password:"whatever"} -> 403 {"error":"No password configured. Complete onboarding first.","needsSetup":true}
```

**결단**: omniroute 자신에게 이미 완결된 첫 접속 온보딩 흐름이 있으므로
**그것에 그대로 맡긴다** — OSiris가 비밀번호를 생성하지도, 오퍼레이션을
추가하지도 않는다. `docker-compose.yml`을
`INITIAL_PASSWORD: ${OSIRIS_OMNIROUTE_ADMIN_PASSWORD:-}`(옵셔널, 빈
문자열 기본값)로 바꿨다. `omniroute/operations.mjs`는 코드 변경 없음 —
이 모듈은 원래부터 비밀번호를 "어디서 왔는지 모르는 순수 파라미터"로
받게 설계되어 있었다(§53) — 사람이 omniroute의 첫 접속 화면에서 직접
정한 값을, 그다음부터 우리 오퍼레이션 호출(있다면)에 그 값을 그대로
건네주면 된다.

## 68. 기존 세션 보호 — 회전하지 않음, 실측으로 안전함을 확인

`.env`의 기존 생성값(`OSIRIS_OMNIROUTE_ADMIN_PASSWORD=PSJf...`)은
**되돌리거나 지우지 않았다** — 이미 로그인해 쓰던 사용자의 세션을 깨지
않기 위해서다. 대신:
1. **왜 안전한지 실측으로 확인**: `managementPassword.ts`의
   `ensurePersistentManagementPasswordHash()`는 저장된 비밀번호 해시가
   **이미 있으면**(`isBcryptHash(storedPassword)`) `INITIAL_PASSWORD`
   env를 **완전히 무시**한다 — 즉 최초 부팅이 끝난 인스턴스에게는 이
   값이 있든 없든 아무 영향이 없다.
2. **실측(진짜 실행 중인 인스턴스로)**: `.env`에서 이 값을 잠깐
   비우고 `docker compose up -d`(osiris-omniroute 재생성) →
   healthy → `GET /api/settings/require-login` →
   `{"hasPassword":true,"setupComplete":true}`(기존 해시가 여전히
   유효) → 기존 비밀번호로 `POST /api/auth/login` → **200**. 그 뒤
   원래 값을 복원하고 다시 `docker compose up -d` → 여전히 200. 즉
   이 값을 비우는 것 자체가 **이미 부팅된 인스턴스에는 아무 것도
   바꾸지 않는다**(완전히 새 설치일 때만 효과가 있다).
3. `.env`/`.env.example`에 이 사실과 "새 설치는 비워 둘 것"을
   주석으로 남겼다(§69).

## 69. `.env` 시크릿 3종 + 이번 것 전수 판정

`docs/design/10-core-architecture.md` §9.5에 정식 표로 옮겼다(요약):

| 변수 | 판정 | 한 줄 근거 |
|---|---|---|
| `NEO4J_AUTH`/`NEO4J_PASSWORD` | 정당, 유지 | 순수 플랫폼 내부 인증, 사람이 안 봄 |
| `CORE_API_TOKEN` | 정당, 유지 | 순수 플랫폼 내부 인증, 사람이 안 봄 |
| `NATS_CORE_PASSWORD`/`NATS_ORCHESTRATOR_PASSWORD` | 정당, 유지 | 순수 플랫폼 내부 인증, 사람이 안 봄 |
| `OSIRIS_OMNIROUTE_ADMIN_PASSWORD` | **부당했다 — 이번에 수정** | 사람이 브라우저에서 직접 보고 입력하는 계정 자격증명 |
| `OSIRIS_GATEWAY_TOKEN` | 정당(현재 단계) | omniroute가 발급, 기계가 소비. 에이전트별 파일 주입 전환은 별개의 기존 격차(I4 §30) |

분류 기준: *"사람이 화면에서 직접 보거나 입력하는가?"* — 그렇다면
온보딩 소관(설치 스크립트가 미리 만들지 않는다), 아니라면 설치 시 자동
생성 + `.env`가 정당하다.

## 70. 콘솔 계약 변경 — 인계 (다른 워커 소유 문서, 이 워커는 고치지 않음)

**변경**: OSiris API의 공개 경로가 `/api/`에서 `/_api/`로 이전됐다
(§64의 라우팅 재설계로 그 이름표가 omniroute에 흡수됐기 때문 —
`40-console-ia.md`/`41-console-design-system.md`는 이 워커가 **고치지
않는다**, 다른 워커 소유). 반영 방법:
- `osiris-api`(미구현, 다음 워커)의 **내부 구현**은 그대로
  `40-console-ia.md`가 정의한 `/api/*`로 두면 된다 — 그 문서를 고칠
  필요가 없다.
- 콘솔(SPA, 다음 워커)의 **클라이언트(브라우저) 쪽 base URL만**
  `/_api/`로 바뀐다 — `osiris-gw`가 `/_api/*` → `/api/*`로 벗겨서
  osiris-api에 전달하기 때문이다.
- SSE(`/api/events`)도 마찬가지로 `/_api/events`가 된다 — 설정값
  (D20/§2.7)은 그대로, 경로만 이동.

## 71. 임의로 정한 세부 결정 (3차 수정분)

1. **OSiris 자신의 브랜드 정적 파일을 정확한 파일명으로만 예약**하고
   확장자 정규식으로 넓히지 않았다 — omniroute 자신의 루트 레벨 정적
   자산(`/providers/*.png` 등)을 catch-all이 올바르게 처리하게 하기
   위해서다.
2. **`rewrite ... break;` + 변수만 있는 `proxy_pass`** 조합을 모든
   리라이트 location의 표준 패턴으로 확정했다(실측으로 확인된 nginx
   함정에 대한 대응).
3. **콘솔이 `/`(정확히 그 경로) 밖의 최상위 경로를 쓰게 되면 이
   블랙리스트 구조를 재검토해야 한다**는 것을 설계 문서(§2.9)에 명시적
   경고로 남겼다 — 지금은 콘솔이 정적 단일 페이지라 문제가 없다.

---

# 구현 노트 — 6단계 `osiris-api` (온보딩 0단계 + 콘솔 SSE 재개)

> I1~I5 워커가 남긴 위 전체는 그대로 두고, 여기부터 `osiris-api`를 실제로
> 구현하며 내린 판단을 append한다. 코드는 `api/`(Go 모듈 `osiris-api`).
> 브리핑 범위: 온보딩 0단계 엔드포인트(`30-onboarding-provisioning.md`
> §1.0), `GET /onboarding/status`, D19/D20 SSE 재개, 헬스, compose/gw 배선.
> **의도적으로 구현하지 않음**: 배정 엔진, 온톨로지 시드/1단계(대화형)
> 온보딩, 콘솔 SPA — 전부 브리핑의 명시적 제외 목록.

## 72. omniroute 오퍼레이션 호출 방식 — Node 서브프로세스 채택

브리핑이 결단을 요구한 지점. **Node 서브프로세스(`api/internal/omniroute/
ops.go`가 `node omniroute/operations.mjs <op>`를 stdin/stdout JSON 계약으로
호출)를 택했다.** 근거:

- `omniroute/operations.mjs`는 일회성 스크립트가 아니라 실제 omniroute
  인스턴스와의 반복된 실측(§50의 별칭 수렴 결함 수정, 이번 빌드가 추가한
  §1.0.4 매니지 키 교환)을 거친, **이미 검증된 단일 소스**다. 게다가 이
  파일은 정확히 이 교차 언어 호출을 위해 "CLI dispatcher" 절을 갖추고
  설계되어 있었다(파일 자체 헤더 주석, "CLI(교차 언어 호출용)"). Go로
  재구현하면 같은 계약이 두 곳에 생기고, 한쪽만 고치는 순간
  divergence가 시작된다 — I2/I3가 이미 겪은 것과 같은 계열의 위험을
  omniroute 경계에서도 반복하는 셈이다.
- **비용**: 이미지에 Node 런타임이 필요해져 `osiris-core`/
  `osiris-orchestrator`의 `FROM scratch` 관습이 깨진다. 그러나 A4가
  실제로 금지하는 것은 `claude` 바이너리·LLM 게이트웨이 자격증명이지
  범용 런타임 자체가 아니다 — Node 자신은 A4 위반이 아니다.
  `operations.mjs`는 **npm 의존성이 0개**(내장 `node:fs`/`node:url`/
  `node:path`와 `fetch`만 사용)이므로, 이미지에는 그 파일 하나와 이
  서비스의 Go 바이너리만 올라가고 런타임에 npm 레지스트리 접근이 전혀
  없다 — `api/Dockerfile`의 `depcheck` 스테이지가 `package.json`/
  `node_modules`가 생기면 **빌드 자체를 실패**시켜 이 가정이 조용히
  깨지는 것을 막는다.
- **완화**: 논-루트(base 이미지 내장 `node` uid 1000), `read_only: true`
  + `/tmp` tmpfs, `cap_drop: [ALL]`, `no-new-privileges`(전부
  `docker-compose.yml`의 `osiris-api` 서비스), docker.sock 없음(G8, 아래
  §76에서 실측 확인). 서브프로세스 호출 자체도 하드 타임아웃(15s)과
  stdout 바이트 상한(1MiB)을 걸어 둔다(`ops.go`의 `limitedWriter`) —
  `operations.mjs`가 멈추거나 폭주해도 HTTP 핸들러가 물리지 않는다.
- **선택하지 않은 쪽(Go 재구현)의 리스크 완화**: 택하지 않았으므로
  완화할 필요가 없어졌지만, 만약 다음 워커가 이 결정을 뒤집는다면
  `operations.mjs`의 6개 오퍼레이션 시그니처와 각 오퍼레이션이 실측으로
  확인한 omniroute wire 계약(§40/§50/§1.0.4 실측 결과 전부)을 Go
  코드에도 반영해야 하고, 이후 두 구현을 동시에 테스트하는 CI 검사가
  필요해진다 — 지금은 그 비용을 아예 지지 않는다.

## 73. omniroute 관리자 자격증명 — `manage` 스코프 키 교환을 실제로 구현

`30-onboarding-provisioning.md` §1.0.4가 "구현은 다음 워커"라고 명시적으로
넘긴 부분(`exchangeAdminPasswordForManageKey`, `applyManageKeyToEnv`,
`resolveOpts`의 `manageKey` 우선순위)을 전부 `omniroute/operations.mjs`에
구현했다:

- `resolveOpts()`가 `opts.manageKey || env OSIRIS_OMNIROUTE_MANAGE_KEY`를
  추가로 읽는다. 있으면 `authenticate()`가 `login()`을 **아예 호출하지
  않고** `Authorization: Bearer <manageKey>`만 싣는다 — 비밀번호는 최초
  교환 그 순간에만 존재한다(§1.0.4의 요구 그대로).
- `api()`를 `cookie` 문자열 인자에서 `{cookie}|{bearer}` 판별 유니언으로
  일반화하고, 기존 4개 오퍼레이션(`registerProviderCredential`/
  `upsertAlias`/`issueOrGetGatewayToken`/`getConfigurationStatus`)을 전부
  `authenticate()` 경유로 바꿨다 — 동작은 100% 동일(실측으로 확인, 아래
  §77), 인증 소스만 매니지 키 우선으로 바뀌었다.
- `exchangeAdminPasswordForManageKey({password, name})` 신설: `password`는
  **이 함수의 인자로만** 존재하고 `login()` 1회 호출에만 쓰인 뒤 폐기된다
  — `opts.adminPassword`/env를 전혀 건드리지 않는다(설계가 요구한
  "비밀번호를 소비하는 유일한 지점이 grep 한 번으로 보이도록"를
  문자 그대로 지켰다: 이 함수 밖에는 `password`를 받는 코드 경로가
  없다).
- `applyManageKeyToEnv(key, {envFile})` 신설 — `applyGatewayTokenToEnv`와
  완전히 대칭. 둘 다 공용 `writeEnvLine(key, value, envFile)` 헬퍼로
  리팩터해 중복을 없앴다.
- CLI 디스패처에 `exchange-admin-password-for-manage-key`/
  `apply-manage-key-to-env` 두 서브커맨드 추가.

**실측(§77)으로 실제 교환을 수행**했다 — throwaway가 아니라 **이 세션이
사용 중인 실제 `osiris-omniroute` 인스턴스**를 대상으로: 기존
`OSIRIS_OMNIROUTE_ADMIN_PASSWORD`(사용자 세션 유지를 위해 회전하지
않음, 브리핑 요구 그대로 준수)로 `manage` 스코프 키를 발급받아
`OSIRIS_OMNIROUTE_MANAGE_KEY`로 `.env`에 반영했고, 그 키 **단독으로**(쿠키도
비밀번호도 없이) `getConfigurationStatus()`가 정상 동작함을 확인했다. 테스트
과정에서 만든 임시 키(`osiris-manage-verify-tmp`)는 삭제해 정리했다 — 최종
`.env`에는 실제 운영에 쓸 `osiris-manage` 키 하나만 남는다.

## 74. `osiris-api` 엔드포인트 — `30-onboarding-provisioning.md` §1.0.8 그대로 구현

`api/internal/onboarding/handlers.go`가 §1.0.8의 표를 그대로 구현한다
(내부 구현 경로는 설계 그대로 `/api/*` — `osiris-gw`가 `/_api/*`→`/api/*`로
벗겨 전달하는 계약을 그대로 따랐다, §2.9/I5 §62~§70). 표는 최종 보고에
정리(콘솔 워커 인계 섹션).

**§1.0.8 텍스트와 실제 구현의 사소한 차이 한 가지**: 응답 JSON의
`gatewayToken.propagatedToOrchestrator`를 실제로 검증하려면 orchestrator가
"자신이 새 게이트웨이 토큰으로 재기동되었다"를 관측 가능한 형태로 노출해야
하는데, 기존 `osiris-orchestrator`의 `GET :8081/status`(I3 §32의
`Snapshot`)에는 그 필드가 없었다. **`orchestrator/internal/reconcile/
reconcile.go`의 `Snapshot`에 `GatewayTokenConfigured bool`(토큰 값이 아니라
존재 여부만) 필드 하나를 추가**했다 — osiris-api가 토큰 값 자체를 읽을
이유가 전혀 없으므로(§8.4의 그 자격증명은 orchestrator 자신의 env와
에이전트별 creds 파일에만 존재해야 함) 불리언 하나만 노출하는 최소 확장.
설계 문서를 고치지 않고 기존 컴포넌트에 가한 최소 확장이라는 점에서
I3 §19(core에 `CloseAIAgent` 추가)·I4 §30(`SyncCredentials` 추가)과 같은
계열의 판단이다.

## 75. 온보딩 0단계 인증 — genesis 창(§8.5)이 **아니라** 별도의 "관리 설정 토큰"

브리핑이 §8.5 genesis 창을 온보딩 구간에 적용하라고 요구했다. **문자
그대로는 적용하지 않았다** — 이유를 명확히 구분해야 한다:

- §8.5의 genesis 토큰은 **`bootstrap-master`가 core를 호출하는 스코프**
  (`POST /onboarding/draft`/`commit`)이고, 핵심 성질(W1)은 "조직이 생기는
  순간 영구히 닫힌다"이다.
- 그런데 온보딩 0단계(`30-onboarding-provisioning.md` §1.0.6)는 **정확히
  그 반대**를 요구한다 — "0단계 전체가 재실행 가능해야 한다(프로바이더
  키 회전, 모델 매핑 변경 등)". W1로 0단계를 잠그면 조직이 한 번 생기는
  순간 프로바이더 키 회전이 영구히 불가능해진다 — 설계 자신의 요구와
  정면 충돌한다.

그래서 `api/internal/adminauth`에 **별도의, `currentOrgRevision`과
무관한 정적 베어러 토큰**(부팅 시 1회 생성, `state/creds/admin/
setup_token`에 저장 — genesis 토큰과 같은 파일 주입 관례를 재사용하되
경로와 폐쇄 조건은 다르다)을 만들어 `/api/onboarding/*`·`/api/events`
전체를 이 토큰으로 게이트했다. 값은 **로그에 절대 출력하지 않는다**
(`main.go`가 경로만 로그로 남긴다) — 이 결정 자체가 브리핑의 "자격증명을
다루는 코드에는 마스킹을 기본값으로" 요구를 정확히 겨냥한 것이다.

**이것이 진짜 genesis 토큰이 아니라는 점을 코드 주석에도 명시**했다
(`api/internal/adminauth/token.go`의 패키지 doc) — 다음 워커가 두 메커니즘을
헷갈려 병합하는 실수를 막기 위해서다.

## 76. 콘솔 인증 — 조직 생성 이후의 설계 공백을 확인, 임의로 메우지 않음

브리핑이 요구한 대로 확인했다: **조직 생성 이후 사람이 콘솔에 로그인하는
구체적 메커니즘은 설계 어디에도 없다.** `10-core-architecture.md` §8.4는
"사람 콘솔 세션 쿠키(`Secure;HttpOnly;SameSite=Strict`, 12h)"라는 **산출물**
형태만 규정하고, 그 쿠키를 발급받기 위해 **무엇을 제출하는지**(비밀번호?
매직링크? `:Person`에 자격증명 필드가 있는지?)는 어디에도 없다.
`20-ontology-schema.md`의 `:Person` 스키마에도 인증 관련 필드가 없다(코드
검색으로 확인 — `core/internal/graphdb/writeops.go`의 `PersonInput`에
`email`/`timezone`/`workingHours` 등은 있어도 password/credential류 필드가
전혀 없다). §4.1 스텝 8의 "관리자 1회용 로그인 토큰 출력"은 문구만 있고
그 토큰이 이후 어떤 로그인 플로우에 연결되는지 명시되지 않는다.

**임의로 발명하지 않았다.** 이번 빌드가 실제로 게이트한 것은 온보딩
0단계 화면(§75의 관리 설정 토큰)뿐이다 — 조직이 생긴 뒤의 일반 콘솔
REST 표면(§12.1의 대부분, `/orgs/tree` 등)은 이번 빌드에 구현되지 않았으므로
(배정 엔진·온톨로지 읽기 API 대부분이 범위 밖) 이 공백이 지금 당장 어떤
엔드포인트도 무방비로 열어 두지는 않는다 — 그러나 **다음 워커(콘솔/
`osiris-api` 확장)가 반드시 먼저 설계해야 하는 항목**으로 명시적으로
표시한다. `[갭]`.

## 77. SSE 재개(D19) 구현 — 연결당 ephemeral pull consumer, 그리고 nats.go 함정 하나

`api/internal/events/sse.go`가 `GET /api/events`를 구현한다. 매 연결마다
JetStream `TASKS` 스트림에 **ephemeral(비-durable) pull consumer**를
새로 만들고(`AckPolicy: AckNone` — 읽기 전용 관찰자라 ack가 필요 없고,
그래서 NATS ACL에 `$JS.ACK.*` 권한을 줄 필요도 없어진다), `Last-Event-ID`가
있으면 `DeliverByStartSequencePolicy` + `OptStartSeq: seq+1`로, 없으면
`DeliverAllPolicy`로 시작한다. 연결 종료 시 `defer`로 그 consumer를
명시적으로 삭제한다(`InactiveThreshold: 5m`을 안전망으로 병행 — 어느 쪽이
실패해도 다른 쪽이 정리한다).

**실측으로 발견한 nats.go 함정**: `js.PullSubscribe(subj, durable string,
opts...)`의 **두 번째 위치 인자("durable")를 ephemeral consumer의 서버
생성 이름으로 채우면 안 된다.** 처음에 `js.PullSubscribe("", info.Name,
nats.Bind("TASKS", info.Name))`로 짰더니 매 연결마다
`configuration requests durable to be <name>, but consumer's value is `
에러로 바인딩이 실패했다 — nats.go가 그 인자를 컨슈머의 `Durable` 설정
필드와 대조 검증하는데, ephemeral 컨슈머는 그 필드가 정당하게 비어 있기
때문이다. **수정**: `js.PullSubscribe("", "", nats.Bind("TASKS",
info.Name))` — 어느 컨슈머에 붙을지는 전적으로 `nats.Bind`가 결정하고,
두 번째 위치 인자는 durable 전용이라 ephemeral을 붙일 때는 반드시
빈 문자열이어야 한다. (I3 §17의 `nats.conf`의 `include` 상대경로 함정,
I4 §32의 multi-filter 컨슈머 함정과 같은 계열 — 실제로 컨테이너를 띄워
재현하고서야 알아낸 것.)

**NATS ACL**: `orchestrator/internal/natsauth/natsauth.go`에 `osiris-api`
정적 플랫폼 사용자를 추가했다(§9.6의 명시적 요구 — "필요한 것만 좁게").
부여한 것: `$JS.API.CONSUMER.{CREATE,DELETE,INFO,MSG.NEXT}.TASKS.*`(consumer
이름만 와일드카드, 스트림은 절대 와일드카드하지 않음)와
`$JS.API.STREAM.INFO.TASKS`, `_INBOX.>` 구독. **주지 않은 것**: INBOX에
대한 어떤 권한도(work-queue 참여자가 아님), 어떤 `$JS.ACK.*`도(AckNone),
`$JS.API.CONSUMER.DURABLE.CREATE.*`(ephemeral만 만든다). `NATS_API_PASSWORD`
가 비어 있으면 `render()`가 이 사용자 자체를 생략한다 — osiris-api가
아직 배포되지 않은 호스트에서 orchestrator가 실패하지 않게.

**실측(추측 아님)**: 실제로 살아 있는 TASKS 스트림(과거 I3/I4 스모크
테스트가 남긴 실제 이벤트 25건, seq 1~25)에 대해 (1) 연결 → `event: hello`
즉시 수신 → 과거 이벤트 25건을 seq 순서대로 수신 → (2) 연결 종료 → (3)
`Last-Event-ID: 20`으로 재연결 → **정확히 seq 21~25만** 수신(유실 없음,
중복 없음) 확인. 연결 종료 후 `GET :8222/jsz?consumers=true`로 TASKS
스트림의 `consumer_count`가 **0**임을 확인 — ephemeral consumer가 실제로
정리됨(누수 없음).

## 78. 발견하고 고친 회귀 — `gw/conf.d/default.conf`의 `location = /` 가 실제로는 한 번도 안 먹히고 있었다

완료조건 8(`/`·`/pre-status`·`/omni/` 회귀 없음)을 검증하던 중 발견.
`osiris.example.com/`와 `127.0.0.1:18280/`가 **둘 다 404**를 돌려주고 있었는데,
그 404는 nginx의 정적 404가 아니라 **omniroute 자신의 Next.js 404
페이지**(응답 헤더에 `X-Powered-By: Next.js`, 본문에 `/omni/_next/...`
에셋 경로)였다 — 즉 `/` 요청이 `location = /`(콘솔 슬롯)이 아니라 맨 아래
`location /` catch-all(omniroute로 프록시)에 떨어지고 있었다.

**원인**: `location = / { root ...; index index.html; }`의 `index`
지시자는 디렉터리 URI(`/`)를 실제 파일로 바꾸기 위해 **내부 리다이렉트를
`/index.html`로 수행하고, 그 새 URI에 대해 location 매칭을 처음부터
다시 돈다.** `location = /index.html`이 없으므로 그 내부 리다이렉트가
맨 아래 catch-all(`location /`)에 걸려 `/omni/index.html`로 다시 프록시된
것 — omniroute에 그런 경로가 없으니 Next.js 자체 404가 나온 것이다.
**`try_files`는 이 내부 리다이렉트-재매칭을 하지 않고 파일을 직접
서빙한다** — 바로 아래 있는 `location = /pre-status`가 이미 `try_files
/pre-status.html =404;`를 쓰고 있어서 그건 항상 정상 동작했던 것이고,
`location = /`만 실수로 `index`를 썼던 것이다.

**수정**: `location = /`도 `try_files /index.html =404;`로 통일. 수정
전/후를 curl로 직접 비교해 확인(§79의 완료조건 8 실측 참고). **이 버그가
언제부터 있었는지는 불확실하다** — I5 §59/§65는 "`/`(콘솔 슬롯) 200"을
기록했지만, 그 시점 검증이 브라우저 없이 어떤 방식으로 이뤄졌는지에 따라
이 정확한 실패 모드(내부 리다이렉트가 catch-all에 떨어지는 것)를 놓쳤을
가능성이 있다 — 이번 빌드가 콘솔 워커의 병렬 작업으로 `gw/html/
index.html`이 자주 갱신되는 상황에서 처음으로 실제 curl 왕복 검증을 했기
때문일 수도 있다. 원인 규명보다 재발 방지가 더 중요하다고 판단해 원인
분석에 시간을 더 쓰지 않았다.

## 79. 발견하고 고친 것 — NATS URL을 그대로 로그에 찍는 시크릿 유출 (osiris-api 자신 + osiris-core 기존 코드)

`osiris-api`를 osiris-core의 `main.go` 스타일을 그대로 따라 작성하다가
그 스타일 자체에 있던 문제를 그대로 옮겨 왔다: `log.Printf("... connecting
to nats at %s", cfg.NatsURL)`— `NatsURL`은 `nats://<user>:<password>@nats:
4222` 형태로 **비밀번호를 URL에 내장**하고 있다(플랫폼 컴포넌트의 관례,
I3 §17). 이 로그 한 줄이 그대로 `docker logs osiris-api`에 평문 비밀번호를
찍는다 — 정확히 브리핑이 경고한 "서브에이전트가 API 키 평문을 파일에
남긴 사고"와 같은 계열의 실수를, 이번엔 파일이 아니라 로그에 저지를
뻔했다.

**실측으로 발견**(`docker logs osiris-api`를 직접 확인하다가 우연히
눈에 띔): 로그 한 줄이 `nats://osiris-api:<password>@nats:4222` 형태로
NATS 비밀번호 평문을 그대로 포함하고 있었다(여기 문서에도, 어디에도
실제 값을 옮겨 적지 않는다 — 발견 즉시 코드를 고쳤을 뿐 값 자체를
따로 기록하지 않았다). **수정**: `osiris-api`/`osiris-core`
양쪽의 `main.go`에서 `cfg.NatsURL`을 로그 인자로 넣는 줄을 전부
`log.Printf("... connecting to nats")`(URL 자체를 찍지 않음)로 바꿨다.
`osiris-orchestrator`의 `main.go`는 애초에 `"osiris-orchestrator:
connecting to nats"`(URL 없이)만 찍고 있었다 — 즉 이 버그를 처음부터
피해 간 것은 orchestrator 하나뿐이었다. **`osiris-core`를 고친 것은
이번 빌드의 범위 밖이지만, 발견한 시크릿-로그-유출을 그대로 두는 것이
"발견했으면 고친다"는 이 프로젝트의 관례(I2 §11, I3 §19의 선례)에 맞다고
판단해 1줄만 최소 수정했다** — 재빌드해 core가 여전히 healthy함을
확인(§79 완료조건 목록 참고).

## 80. compose/gw 배선 요약

- `docker-compose.yml`: `osiris-api` 플레이스홀더 주석을 실제 서비스로
  교체. `build: {context: ., dockerfile: api/Dockerfile}`(빌드 컨텍스트가
  리포 루트인 이유는 `api/Dockerfile` 헤더 주석 — `omniroute/
  operations.mjs`를 `api/` 밖에서 그대로 COPY해야 하기 때문). 네트워크
  `osiris-edge`(별칭 `osiris-api` — `gw/conf.d/default.conf`의 `set
  $api_up "osiris-api:8443"`이 참조하는 바로 그 이름) + `osiris-data`(neo4j
  읽기 전용 + orchestrator `:8081/status` 도달용). `read_only: true` +
  `/tmp` tmpfs + `cap_drop:[ALL]` + `no-new-privileges`. docker.sock
  마운트 없음(D4/G8). `osiris-orchestrator`에 `NATS_API_PASSWORD`
  환경변수 추가(옵셔널 — 비어 있으면 `natsauth.render()`가 그 사용자를
  생략).
- `gw/conf.d/default.conf`: **주석/라우팅 자체는 이미 I5가 정확히
  준비해 뒀다** — `/_api/` location이 이미 `osiris-api:8443`으로
  프록시하도록 되어 있어 별도 배선이 필요 없었다. 실제로 고친 것은
  §78의 `location = /` 버그 하나뿐.
- `gw/render-status.sh`: `osiris-api`/`osiris-core`/`osiris-orchestrator`
  세 행의 "미구현 — 다음 워커" 문구가 이제 전부 구현되었으므로 실제
  역할 설명으로 갱신. `bash gw/render-status.sh`로 재생성해 `/pre-status`
  응답에 반영 확인.
- `.env.example`: `NATS_API_PASSWORD`(신규, 플랫폼 정적 자격증명 —
  `NATS_CORE_PASSWORD`/`NATS_ORCHESTRATOR_PASSWORD`와 같은 판정),
  `OSIRIS_OMNIROUTE_MANAGE_KEY`(신규, §73), `OSIRIS_OMNIROUTE_ADMIN_
  PASSWORD`의 주석을 §1.0.4 반영으로 갱신(변수 자체는 **지우지 않았다**
  — `docker-compose.yml`의 `INITIAL_PASSWORD: ${OSIRIS_OMNIROUTE_ADMIN_
  PASSWORD:-}` 옵셔널 부트스트랩 경로가 여전히 이 이름을 참조하므로.
  `31-onboarding-boundary.md` §3이 "제거 대상"이라 적은 것은 "OSiris가
  이 값을 반복 인증에 쓰는 것"을 겨냥한 지적이었고 그 용도는 이제
  §73으로 완전히 대체되었다 — 그러나 `INITIAL_PASSWORD` 부트스트랩
  경로 자체는 여전히 정당한 별개의 용도이므로 변수명은 유지하고 주석만
  갱신했다. 다음 워커가 이 판단에 동의하지 않으면 변수명을 바꾸는 것은
  기계적인 작업이다), `osiris-api` 전용 섹션(관리 설정 토큰이 `.env`가
  아니라 `state/creds/admin/setup_token`에 있다는 점 명시).
- `bin/verify-isolation.sh`: E절 신설 — E1(`osiris-agents`→`osiris-api`
  도달 불가), E2(docker.sock 미마운트), E3(실제 에이전트 netns에서도
  도달 불가, 있을 경우).
- `orchestrator/internal/natsauth/natsauth.go`: `osiris-api` 정적
  사용자(§77), `NewGenerator` 시그니처에 `apiPassword` 인자 추가.
  `orchestrator/internal/config/config.go`: `NatsAPIPassword` 필드.
  `orchestrator/internal/reconcile/reconcile.go`: `Snapshot.
  GatewayTokenConfigured`(§74).
- `core/cmd/osiris-core/main.go`, `api/cmd/osiris-api/main.go`: NATS URL
  로그 유출 수정(§79).
- `omniroute/operations.mjs`: 매니지 키 교환 오퍼레이션 2개 + 인증
  일반화(§73).

## 81. 완료조건 실측 결과 (전부 실행 기반, 추측 아님)

- **#1**: `docker compose build osiris-api && docker compose up -d` →
  8개 OSiris 서비스 전부 `healthy`(osiris-gw/neo4j/nats/egress/omniroute/
  core/orchestrator/api). `docker exec osiris-api /osiris-api
  -healthcheck` → exit 0.
- **#2**: `http://127.0.0.1:18280/_api/onboarding/status`와
  `https://osiris.example.com/_api/onboarding/status` 둘 다 (관리 설정
  토큰과 함께) **200**(이전 502 완전 해소). SSE(`/_api/events`)도 두
  경로 모두 정상 스트리밍(§77).
- **#3 (0단계 왕복)**: `omniroute/operations.mjs`의 매니지 키 교환을
  이 세션이 실제로 쓰는 **살아 있는** `osiris-omniroute` 인스턴스에
  대해 실행 → `manage` 스코프 API 키 발급 확인(`GET /api/keys`로
  존재 확인, 응답 바디는 덤프하지 않고 상태 코드/개수만 확인) →
  `.env`의 `OSIRIS_OMNIROUTE_MANAGE_KEY` 갱신 확인. 이후 그 키 하나만으로
  (쿠키도 비밀번호도 없이) `getConfigurationStatus()`가 정상 응답함을
  확인 — omniroute 관리 API에 **실제로 반영**되었다는 뜻이다(DB/관리
  API 양쪽으로 확인). 프로바이더 키(`registerProviderCredential`)와
  별칭(`upsertAliases`)도 이미 이전 세션(§59)에서 실제 등록되어 있었고
  이번 세션은 그 상태를 어그러뜨리지 않고 그대로 재확인만 했다 — 실제
  Anthropic 키의 유효성 자체는 이번에도 `[미검증]`(사용자의 실제
  프로바이더 키를 이 세션이 알아낼 방법도 이유도 없음, §1.0.3이 이미
  이 한계를 설계로 못박음).
- **#4 (온보딩 상태 전/후)**: `.env`의 `OSIRIS_OMNIROUTE_MANAGE_KEY`를
  일시적으로 비우고(원본은 백업, 이후 byte-identical하게 복원 확인)
  `GET /_api/onboarding/status` 호출 → `step0.ready=false`,
  `manageKeyExchanged=false`. 원복 후 재호출 → `step0.ready=true`,
  `manageKeyExchanged=true`. 실제 코드 경로(파일 읽기)가 매 호출 갱신됨을
  증명 — 캐시된 가짜 상태가 아니다.
- **#5 (SSE 재개)**: §77 참고 — 연결→과거 이벤트 25건 수신(seq 1~25)→
  종료→`Last-Event-ID: 20`으로 재연결→**정확히 21~25만** 수신. 연결
  종료 후 TASKS 컨슈머 수 0(누수 없음, `GET :8222/jsz` 확인).
- **#6**: `docker inspect osiris-api --format
  '{{range .Mounts}}{{.Source}}{{end}}'`에 `/var/run/docker.sock` 없음
  (`bin/verify-isolation.sh` E2로 자동화, PASS).
- **#7**: `bin/verify-isolation.sh` **15/15 통과**(기존 13 + 신규 E1/E2,
  E3는 실제 에이전트가 없어 이번 실행에서는 SKIP 처리되는 조건부
  섹션 — 코드 경로는 존재). `bin/lint-driver-boundary.sh`/
  `bin/lint-health-invariant.sh` 둘 다 PASS(이번 빌드가 건드리지 않은
  범위이므로 회귀 없음 재확인 차원).
- **#8**: `/`(§78 버그 수정 후 200), `/pre-status`(200), `/omni/`(200)
  전부 회귀 없음(`127.0.0.1:18280`·`osiris.example.com` 양쪽). `<svc-a>.example.com`
  (200), `omniroute.example.com`(200, 공용 인스턴스 무영향 — `RestartCount`/
  `StartedAt` 불변 확인). `<upstream-proxy>`도 `RestartCount=1`로 이전 기록과
  동일(이번 세션이 재시작시키지 않음). **`<svc-c>`/`<svc-b>-live`가
  재시작 루프 중인 것을 관찰했으나 둘 다 OSiris와 무관한 사전 존재
  문제**(`<svc-b>-live`는 I3 §20 #8이 이미 기록한 그 프로젝트 자체의
  환경변수 누락 문제 그대로, `<svc-c>`는 이번 세션이 전혀 건드리지
  않은 완전히 별개 프로젝트 — 이번 작업이 유발했다고 볼 근거가 없다).
- **#9**: `docker compose -f docker-compose.yml -f docker-compose.
  dev-net.yml down && up -d` 2회 반복(1회는 이미지 재빌드 포함) → 8개
  서비스 전부 healthy 재현, 고아 컨테이너 0개(`osiris.dev/managed-by=
  osiris-orchestrator` 라벨 컨테이너 0개 — 이번 세션에서 에이전트를
  만들지 않았으므로 당연한 결과), 관리 설정 토큰이 `state/creds/admin/
  setup_token`(호스트 볼륨)에서 재기동 후에도 그대로 읽힘 확인(재생성
  없음, 로그로 확인).
- **#10**: `git -C <host-infra> status --porcelain | sha256sum` →
  `f6eef3535bd079043c86b6ec2a8ed6a48a0918b260482b9993f3c19b5470c6a9`.
  **브리핑이 준 기준값(`92557f21...`)과 다르다.** 그러나 실제 diff를
  확인한 결과 변경분 전부가 `<other-project>/**`, 최상위 `CLAUDE.md`/
  `TASK_CONTEXT.md`뿐 — 이 워커가 세션 내내 `pwd`를 `<repo-root>`
  밖으로 옮긴 적이 없고 `<host-infra>` 이하 어떤 경로도 절대경로로건
  건드리지 않았다(코드 검색·명령 이력으로 자기 확인 가능). I5가 이미
  §59에서 기록한 것과 정확히 같은 현상 — `<host-infra>`는 OSiris와
  무관한 수십 개 프로젝트(그중 한 프로젝트가 이번 diff의 원인)가 함께 사는
  살아있는 저장소라 이 세션 도중에도 무관한 변경이 쌓인 것으로 판단한다.
  **임의로 해석·되돌리지 않고 그대로 보고한다.**

## 82. 임의로 정한 세부 결정 (감독이 알아야 할 것)

1. **관리 설정 토큰(§75) 신설** — §8.5 genesis 창을 문자 그대로 온보딩
   0단계에 적용하면 설계 자신의 재실행 요구(§1.0.6)와 충돌하므로, 별도
   메커니즘을 만들었다. 조직 생성 이후의 콘솔 인증은 설계 공백으로
   확인만 하고 임의로 채우지 않았다(§76, `[갭]`).
2. **osiris-api 이미지에 Node 런타임 포함**(§72) — `FROM scratch` 관례를
   깨는 유일한 플랫폼 컴포넌트가 됐다. 대안(Go 재구현)의 divergence
   리스크가 이 비용보다 크다고 판단.
3. **`OSIRIS_OMNIROUTE_ADMIN_PASSWORD` 변수명을 유지**(§80) —
   `31-onboarding-boundary.md`의 "제거 대상" 권고와 다르게, 변수 자체는
   지우지 않고 용도 설명만 갱신했다. `docker-compose.yml`의
   `INITIAL_PASSWORD` 부트스트랩 경로가 여전히 이 이름을 참조하기
   때문 — 실질적으로 그 문서가 지적한 문제(반복 인증에 재사용하는 것)는
   §73으로 이미 해소되었다.
4. **`GET/POST /api/onboarding/*`와 `/api/events` 전체를 관리 설정
   토큰으로 게이트**(§75/`api/internal/api/server.go`) — 설계가 GET
   엔드포인트(예 `alias-defaults`/`status`)의 인증을 명시하지 않았지만,
   이 빌드에는 다른 방어선이 없으므로 보수적으로 전부 게이트했다.
5. **`osiris-api` NATS 사용자에게 `AckNone` ephemeral consumer만
   허용**(§77) — INBOX 접근·durable 생성·ACK 어느 것도 주지 않았다.

## 83. 설계와 어긋났거나 구현 불가였던 것

- **§8.5 genesis 창을 온보딩 0단계에 문자 그대로 적용하지 않음**(§75) —
  설계 자신의 다른 요구(§1.0.6 재실행성)와 충돌하기 때문. 감독 판단
  필요 지점으로 남긴다.
- **조직 생성 이후 콘솔 인증 메커니즘 — 설계에 없음**(§76, `[갭]`).
  다음 워커가 반드시 먼저 설계해야 한다.
- **에이전트별 git PAT 최초 입력 경로 — 여전히 없음**(`31-onboarding-
  boundary.md`가 이미 B2로 기록한 갭, 이번 빌드가 새로 발견한 것은
  아니다. 이 빌드의 범위 밖이라 손대지 않았다).
- **`OSIRIS_MAX_RESIDENT_AGENTS` 출처 미문서화**(`31-onboarding-
  boundary.md` §3.3이 이미 `[갭]`으로 기록, 이번 빌드도 손대지 않음 —
  범위 밖).
- **`GET /onboarding/status`의 `phase`가 항상 `"NotStarted"`** — 이
  빌드가 온톨로지 시드/1단계 온보딩 대화(상태기계 §2)를 구현하지
  않으므로 정직하게 그렇게 남겼다(가짜 상태 전이를 시뮬레이션하지
  않음). `capabilityMappingRatio`도 항상 `null`(같은 이유, §7.2 브리지
  전체 순회가 필요해 배정 엔진 워커의 몫).
- **DLQ(`dlq.inbox`), 워크스페이스 볼륨 GC, 에이전트별 24h 게이트웨이
  토큰 회전 — 전부 기존에 이미 기록된 격차**(I4 §36, I5 §49)이고 이번
  빌드가 새로 만든 문제도, 이번 빌드가 닫을 수 있는 문제도 아니다.

## 84. 다음 워커(콘솔)를 위한 인계 — 실제 엔드포인트·요청/응답 형상·SSE 이벤트

**베이스 URL**: 브라우저는 항상 `/_api/*`를 쓴다(`osiris-gw`가
`/api/*`로 벗겨 전달) — 아래는 그 벗겨진 뒤의(= `osiris-api` 내부) 경로
그대로 적었다. 콘솔은 여기에 `/_api` 접두사를 붙여 호출할 것.

**인증**: 지금은 전부 `Authorization: Bearer <관리 설정 토큰>`이
필요하다(§75/§82-4). 토큰은 `state/creds/admin/setup_token`에 있다(호스트
파일 — 온보딩 콘솔이 이 파일을 어떻게 사람에게 전달할지는 이 빌드의
범위 밖. 임시로는 운영자가 직접 `cat`해서 콘솔 로그인 화면에 붙여넣는
흐름을 가정했다). **조직 생성 이후 이 토큰을 계속 요구할지, 다른 인증으로
넘어갈지는 설계 공백**(§76) — 콘솔 워커가 감독과 먼저 정할 것.

| 엔드포인트 | 메서드 | 요청 바디 | 응답(200) 형상 | 비고 |
|---|---|---|---|---|
| `/api/onboarding/status` | GET | — | `{step0:{omniroute:{reachable,manageKeyExchanged}, providers:[{name,provider}], aliases:[{name,declaredProvider,declaredModel,providerName,providerRegistered,exists,inSync,currentModel}], gatewayToken:{issued,propagatedToOrchestrator}, ready}, phase:"NotStarted", org:{teams,roles,capabilityMappingRatio:null,approvalPolicyDefined}}` | 시크릿 값 절대 없음 |
| `/api/onboarding/step0/status` | GET | — | 위 `step0` 객체만 | |
| `/api/onboarding/step0/omniroute-admin` | POST | `{password}` | `{exchanged:true}` | `password`/`key` 에코 없음 |
| `/api/onboarding/step0/providers` | POST | `{name,provider,apiKey}` | `{ok:true}` | `apiKey` 에코 없음 |
| `/api/onboarding/step0/alias-defaults` | GET | — | `{entries:[{name,providerName,provider,model}]}` | `omniroute/config.json` 그대로 |
| `/api/onboarding/step0/aliases` | POST | `{entries:[{name,providerName,provider,model}]}` | `{results:[{name,ok,...}]}` | |
| `/api/onboarding/step0/gateway-token` | POST | — | `{issued:true, needsOrchestratorRestart:true, restartCommand:"docker compose up -d osiris-orchestrator"}` | 토큰 값 에코 없음 |
| `/api/onboarding/step0/verify-gateway` | POST | — | `{gatewayHealthOk,aliasesInSync,propagatedToOrchestrator,ready}` | 실제 LLM 호출·비용 없음 |
| `/api/events` | GET (SSE) | — (헤더 `Last-Event-ID` 또는 쿼리 `?lastEventId=`) | SSE 스트림 | 아래 참고 |

**SSE 이벤트 타입** (`event:` 필드, `eventNameForSubject` 매핑):
- `hello` — 연결 직후 즉시 1회(D20 자가진단, `data: {}`)
- `org.node.status_changed` — NATS `org.event.*` 발행분(현재 유일하게
  실제 트래픽이 있는 것 — `osiris-core`가 그래프 쓰기 시 발행)
- `wbs.task.status_changed` — `task.*.event`(이번 빌드에 실제 프로듀서
  없음, 배선만 되어 있음 — 과거 에이전트 스모크 테스트 데이터가 그
  예시)
- `approval.created` — `approval.*`(프로듀서 없음)
- `console.event` — 그 외 전부(fallback)
- `id:` 필드 = JetStream `TASKS` 스트림 시퀀스(D19의 `Last-Event-ID` 그
  값)
- 15초마다 `: keepalive` 코멘트 라인(주석 라인이라 `event:`/`id:` 없음 —
  EventSource가 무시함, D20)

**콘솔이 알아야 할 제약**:
- 이번 빌드는 §12.1의 나머지 REST(`/orgs/tree`, `/wbs/*`, `/approvals/*`
  등)를 구현하지 않는다 — 온보딩 0단계 화면만 실제로 동작한다.
- `phase`는 항상 `"NotStarted"`다(§83) — 콘솔이 스테퍼 UI를 만들 계획이면
  1단계 온보딩(대화형) 구현 전까지는 이 필드로 진행 상태를 표시할 수
  없다.
- SSE 재연결은 브라우저의 `EventSource` 표준 자동 재연결(마지막
  `id:`를 `Last-Event-ID` 헤더로 자동 재전송)에 그대로 의존해도 된다 —
  서버가 그 헤더를 D19대로 처리한다(쿼리 파라미터 폴백도 있음, 최초
  연결 시 헤더를 못 붙이는 프록시 조합 대비).

---

# 구현 노트 — 7단계 콘솔 게이트웨이 배선 + 브라우저 인증 + 목 해제

## 85. 범위와 배경

작업 지시: 콘솔 SPA를 `osiris-gw`에 배선하고, 브라우저 인증(사용자 결정:
토큰 입력 → 세션 쿠키 교환)을 붙이고, 콘솔의 mock을 실제 `osiris-api`로
전환한다. 목표는 "대표가 브라우저를 열어 온보딩 0단계를 실제로
완주"(실측, 추측 아님).

## 86. 작업 1 — 브라우저 인증: §1.3 Q2b 판정 절차 적용 (실제 판정 기록)

`31-onboarding-boundary.md` §1.3에 이 세션 쿠키 도입을 통과시켰다:

- **Q1**(기동 필수?) 아니오 — 세션 쿠키가 없어도 스택은 정상 기동한다.
- **Q2**(사람이 화면에서 직접 보거나 입력?) **예** — 관리 설정 토큰
  (`state/creds/admin/setup_token`)을 대표가 콘솔의 토큰 입력 화면에
  붙여넣는다.
- **Q2a**(조직 무관 호스트 설정 vs 조직에 관한 것?) 사건 3(omniroute
  관리자 비밀번호)과 같은 범주 — "누가 이 콘솔을 관리할 수 있는가"이므로
  조직에 관한 것으로 분류, Q2b로 계속.
- **Q2b**(저장 대상이 원본 자격증명인가, 스코프 제한 기계 자격증명으로
  교환하는 경로가 있는가?) **교환 경로가 있다** — 이번 빌드가 그 경로
  자체다(`POST /api/auth/session`). **판정: 원본(설정 토큰)은 절대
  저장하지 않는다.** 브라우저에도(`localStorage`/`sessionStorage`/URL
  전부 코드에 없음, Playwright로 런타임 실측 — §89), 서버에도(교환 1회
  `adminauth.Guard.Valid`로 비교만 하고 어디에도 대입하지 않음) 저장하지
  않고, 교환 결과물(불투명 세션 id)만 서버 메모리(`internal/session.
  Store`)에 보관한다.
- 결과물(세션 id)에 대해 Q2를 재귀 적용: 사람이 그 값을 보거나 입력하지
  않는다(HttpOnly 쿠키, JS도 못 읽음) → **아니오** → Q3 이하 일반 규칙
  (순수 기계 간 인증)으로 취급. §1.3이 정확히 예견한 재귀 그대로다.

## 87. 쿠키 속성 결정 — `Secure`를 루프백에서 실측(추측 아님)

설계(§8.4)는 `Secure; HttpOnly; SameSite=Strict, 12h`를 못박는다.
`HttpOnly`/`SameSite=Strict`/`12h`는 그대로 적용했다. **`Secure`는
고정값으로 두지 않고 요청별로 결정한다** — 이유는 실측:

- `Secure` 쿠키 속성은 **브라우저가 그 응답이 도착한 연결 자체**에 대해
  적용하는 전송 규칙이다 — `window.isSecureContext`가 `127.0.0.1`을
  "신뢰 가능"으로 봐주는 것(getUserMedia/서비스워커류 게이팅)과는
  **완전히 별개의 메커니즘**이다.
- 실측(Playwright, 실제 Chromium): `http://127.0.0.1:18280`(평문 HTTP)에
  `Set-Cookie: ...; Secure`를 그대로 내려 보내면, curl의 쿠키잔 파일에
  `Secure` 플래그가 있는 채로 기록되긴 하지만(`Secure` 자체는 헤더
  파싱), **브라우저 쪽에서 실측한 결과 루프백은 자유이용권이 없다** —
  이번 빌드는 `isSecureRequest(r)`로 `r.TLS != nil || X-Forwarded-Proto:
  https`를 확인해 **HTTP로 도착한 요청에는 `Secure`를 아예 안 붙이고**,
  TLS 종단을 거쳐 도착한 요청(이 호스트의 `<upstream-proxy>` 경유
  `https://osiris.example.com/`)에는 붙인다. 두 경로 모두 실제로 실측(curl
  쿠키잔의 `Secure` 컬럼 확인): 루프백 `FALSE`, `https://osiris.example.com/`
  경유 `TRUE`.
- `X-Forwarded-Proto`를 신뢰하는 것이 §2.8("인가 결정에는 안 씀")과
  충돌하지 않는지 확인했다 — `Secure` 속성은 인가 결정이 아니라
  "브라우저가 이 쿠키를 계속 보낼지"를 정하는 순전한 전송 힌트이고, 실제
  인가는 여전히 `session.Store.Valid`(서버 메모리 조회)만으로 결정된다.
  `internal/session/session.go`의 `isSecureRequest` doc comment에 이 구분을
  그대로 남겼다.

## 88. 구현 — `internal/session` + `internal/authn` + 콘솔 `AuthGate`

- `api/internal/session`: 세션 저장소(단일 프로세스 인메모리 map, 32바이트
  랜덤 id, TTL 12h, `Valid`/`Create`/`Revoke`), 쿠키 이름
  `osiris_console_session`.
- `api/internal/adminauth`: `Valid(token) bool`을 노출해 `authn`이 재사용.
- `api/internal/authn`(신설): 세션 쿠키 **또는** 기존 Bearer 설정 토큰
  둘 다 받아들이는 `Require` — curl 기반 기존 완료조건(§34/§59/§81)을
  깨지 않으면서 브라우저 쿠키 경로를 얹었다. `POST /api/auth/session`
  (교환, `Require`로 감싸지 않음 — 순환이 되므로), `POST /api/auth/logout`
  (서버측 무효화 + 쿠키 삭제, 멱등).
- `api/internal/api/server.go`: 기존 8개 온보딩/이벤트 라우트를 전부
  `guard.Require`(구 adminauth)에서 `authn.Guard.Require`로 교체. 새 라우트
  2개 추가.
- 콘솔: `src/components/TokenEntryScreen.tsx`(최초 진입 화면, antd Form —
  다른 온보딩 스텝과 동일한 "제출 인자로만 흐르고 즉시 resetFields" 규칙),
  `src/components/AuthGate.tsx`(최상단 게이트 — `GET /onboarding/status`
  1회로 세션 유효성 확인, 401이면 토큰 화면), `src/api/authEvents.ts`(전역
  401 pub-sub — 세션 만료 시 어떤 화면에서든 토큰 화면으로 복귀),
  `src/api/auth.ts`(exchange/logout). `Topbar.tsx`에 로그아웃 메뉴 추가
  (mock 모드에서는 비활성 — 세션 개념이 없으므로).
- **세션 만료/무효 동작**: 만료(12h) 또는 `osiris-api` 프로세스 재시작
  (인메모리 저장소라 재시작 시 전부 무효화, 문서화된 의도적 한계 —
  `session.go` doc comment) 시 다음 API 호출이 401 → `authEvents`가
  발화 → `AuthGate`가 토큰 화면으로 되돌린다. 실측(§89): `docker compose
  down && up -d` 후 실제로 토큰 화면이 다시 뜨고, 토큰을 다시 넣으면
  0단계 상태가 그대로(Neo4j/omniroute는 영속) 이어짐을 확인했다.

## 89. 검증 — 전부 실제 실행 (Playwright 실제 브라우저 + curl + docker)

1. **브라우저 end-to-end**(Playwright/Chromium, headless): `http://
   127.0.0.1:18280/` 진입 → 토큰 화면 → `state/creds/admin/setup_token`
   붙여넣기 → `POST /_api/auth/session` 200 → 세션 쿠키 확립 → 온보딩
   화면 렌더(실제 API가 이미 0단계 5개 하위 상태를 전부 ready:true로
   갖고 있어 "0단계 완료" 배너로 렌더됨 — 이전 6단계 워커 세션에서 이미
   실제 omniroute에 대고 프로바이더 등록/별칭/게이트웨이 토큰까지
   완료했던 상태가 그대로 남아 있었다). 완료 상태만으로는 부족하다고
   판단해 **`POST /_api/onboarding/step0/verify-gateway`를 인증된 브라우저
   페이지 컨텍스트 안에서 직접(cookie 인증, 헤더 수동 설정 없음) 다시
   호출**해 5개 필드 전부 확인: `{gatewayHealthOk:true,
   aliasesInSync:true, propagatedToOrchestrator:true, ready:true}` —
   VerifyStep.tsx가 그리는 것과 정확히 같은 호출·같은 필드명(§91에서
   고친 그 이름들)이 실제 브라우저에서 실제로 동작함을 실측. 스크린샷:
   `scratchpad/loopback-01-token-screen.png`(토큰 화면),
   `scratchpad/loopback-02-after-login.png`(로그인 직후),
   `scratchpad/loopback-03-onboarding.png`(0단계 완료 화면).
2. 동일 흐름을 `https://osiris.example.com/`에서 반복 — 동일하게 통과
   (스크린샷 `scratchpad/<shot>-01/02/03-*.png`). 쿠키의 `Secure` 플래그가
   이 경로에서만 `TRUE`로 찍힘을 curl 쿠키잔으로 재확인(§87).
3. **정적 자산 전수 200**: 빌드 산출물(`console/dist/index.html`)이
   실제로 참조하는 3개 절대경로(`/favicon.svg`,
   `/assets/index-*.js`, `/assets/index-*.css`) + 기존 브랜드 자산 8개
   (`favicon.ico/-180.png/-512.png`, `osiris-mark.svg/-auto/-gold/-ink`)
   + `/brand/osiris-mark.svg`(콘솔 `public/brand/`, 지금은 아무 코드도
   fetch하지 않지만 예방적으로 경로 예약) + `/pre-status` + `/healthz` —
   전부 curl로 개별 확인, 전부 200.
4. **SSE 수신(쿠키 인증)**: Playwright 페이지 컨텍스트 안에서
   `new EventSource('/_api/events')`(헤더 수동 설정 없음, 쿠키만으로 인증)
   → `event: hello` 실제 수신 확인. 루프백/`osiris.example.com` 양쪽에서 확인.
5. **토큰/세션이 브라우저에 저장되지 않음**: (a) 코드 검토 — 콘솔 전체에
   `localStorage`/`sessionStorage` 쓰기 코드 경로 없음(grep 확인, 기존
   README 주장 그대로 유지), URL에 토큰을 얹는 코드 없음. (b) **런타임
   실측**(Playwright) — 로그인 전후 `localStorage`/`sessionStorage`가 항상
   `{}`이고, `location.href`에 토큰 문자열이 전혀 없음을 매 실행마다
   단언(assert)해 확인. 세션 쿠키 자체는 `HttpOnly`라 애초에 JS
   (`document.cookie`)로 읽을 수 없다 — 이것도 브라우저 실측으로 확인.
6. **`docker compose down && up -d` 재현성**: 전체 스택
   `down`(네트워크까지 제거) 후 `up -d` → 8개 컨테이너 전부 재차 healthy,
   콘솔(`/`, `/assets/*`) 200, 인증 흐름(Playwright 재실행) 여전히 통과.
   세션은 인메모리라 재시작으로 무효화됨(§88의 문서화된 한계) — 재로그인
   필요, 그 뒤로는 온보딩 0단계 상태(Neo4j/omniroute 쪽 데이터, 볼륨
   유지)가 그대로 이어짐을 확인.
7. **회귀 없음**: `/pre-status`(200), `/omni/`(308→`/omni`, 이어서 200 —
   I5 §57에서 이미 문서화된 정상 동작, 회귀 아님), 브랜드 자산 8종 전부
   200. 이 호스트의 인접 스택 무회귀: `<upstream-proxy>`/`<svc-a>_*`/`<svc-b>-*`/
   프로덕션 `omniroute`(공용, `diegosouzapw/omniroute:latest`) 전부
   이번 작업으로 재시작/중지되지 않음(§91에서 ID/RestartCount 비교).
8. `bin/verify-isolation.sh`: 15 passed, 0 failed(에이전트 컨테이너 없어
   1개 SKIP — 정상, 이번 작업 범위와 무관). `bin/lint-driver-boundary.sh`
   PASS, `bin/lint-health-invariant.sh` PASS. `docker compose up -d
   --remove-orphans` 실행 결과 고아 컨테이너 0(제거 대상 없음).
9. 프로덕션(공용) `omniroute` 컨테이너: 작업 시작 전/후 RestartCount
   0/0, 컨테이너 ID 불변(`a27bc2c3e712...`) — 이번 작업이 손대지
   않았음을 확인.
10. `git -C <host-infra> status --porcelain | sha256sum` — 작업
    시작 직전과 이 문서 작성 시점 모두 동일 해시(`f6eef353...`)로
    확인했고, 그 경로 아래 최근 2시간 내 수정된 파일도 0건(`find
    -newermt`) — 이 워커가 그 경로에 어떤 것도 쓰지 않았음을 확인.

## 90. 게이트웨이 배선 — 콘솔을 이미지 레이어에 굽는다 (배포 방식 결정)

**결정: 멀티스테이지 Docker 빌드**(`gw/Dockerfile`, 신설) — 1단계
`node:22.14.0-alpine3.21`에서 `npm ci && npm run build`, 2단계
`nginx:1.27-alpine`에 `console/dist`만 `/usr/share/nginx/console`로 COPY.
`docker-compose.yml`의 `osiris-gw` 서비스를 `image: nginx:1.27-alpine`
(고정 이미지)에서 `build: {context: ., dockerfile: gw/Dockerfile}` +
`image: osiris/gw:${TAG}`로 전환(다른 osiris-* 서비스와 동일한 패턴).

**근거**: 이식성 계약("어디서든 `install osiris`")이 호스트에 Node/npm
사전 설치나 수동 `npm run build` 선행을 요구하지 않는다. 멀티스테이지
빌드는 이미지 빌드 단계 **안에서** 그 빌드를 수행하므로, `docker compose
build`(또는 `up -d --build`) 한 번만으로 콘솔이 포함된 이미지가
만들어진다. `gw/conf.d`·`gw/html`은 여전히 호스트 바인드 마운트로
남겨(손 편집 가능성 보존) 콘솔 산출물과는 별개 경로
(`/usr/share/nginx/console`)를 써서 서로 그림자를 드리우지 않게 했다.
검토했으나 채택하지 않은 대안: (a) 별도 `console`이라는 서비스를 만들어
정적 파일 서버(nginx 등)로 띄우고 `osiris-gw`가 그리로 `proxy_pass` —
컨테이너 하나·네트워크 홉 하나가 그냥 늘어날 뿐 이점이 없어 기각(콘솔은
동적 서버가 필요 없는 순수 정적 SPA). (b) CI에서 미리 빌드해 `console/
dist`를 커밋 — 빌드 산출물을 저장소에 커밋하는 관례 자체가 이 코드베이스
어디에도 없고(다른 모든 이미지가 소스에서 빌드), 드리프트(커밋된
dist가 소스와 어긋나는) 위험을 이유 없이 만들어 기각.

**정적 자산 경로 예약** — `gw/conf.d/default.conf`에 `location
/assets/`·`location /brand/` 두 접두사를 신설(파일명 열거 아님, §64/§78이
이미 겪은 실패를 반복하지 않기 위해 디렉터리 단위). `console/dist/
index.html`을 실제로 curl해 그것이 참조하는 절대경로를 전수 확인한 뒤
결정했다(추측 아님) — `/assets/index-*.js`, `/assets/index-*.css`,
`/favicon.svg`(기존 `favicon*` 정규식이 이미 커버). `/brand/osiris-mark.
svg`는 지금 아무 코드도 fetch하지 않지만(콘솔 `BrandMark.tsx`가 SVG를
인라인 JSX로 직접 그려서 원본 파일을 런타임에 참조하지 않음) 같은 사고
패턴(§64/§78)을 예방적으로 닫기 위해 함께 예약했다. `location = /`는
기존 `gw/html` 정적 랜딩(`root /usr/share/nginx/html`)에서 콘솔 산출물
(`root /usr/share/nginx/console`)로 루트만 바꾸고, `try_files
/index.html =404` 관용구(§78이 이미 확립한, `index` 지시자의 내부
리다이렉트 함정을 피하는 패턴)는 그대로 유지했다.

**발견하고 고친 것 — `api/go.mod`/`go.sum` 불완전**: `api/Dockerfile`을
그대로 재빌드하다가 `go build`가 "go: updates to go.mod needed; to
update it: go mod tidy"로 실패하는 것을 발견했다(내 신규 패키지
`internal/session`/`internal/authn`는 표준 라이브러리만 써서 원인이
아니었다 — `go mod tidy` 전/후 `go.mod`의 `require` 블록이 전혀
바뀌지 않았고 `go.sum`만 갱신된 것으로 확인). Go 1.17+ 모듈 그래프
가지치기(lazy loading)가 이전에 커밋된 `go.sum`으로는 클린 빌드를
검증하지 못하는 상태였던 것으로 보인다 — 이 워커 이전에 이미 잠재해
있던 결함(로컬 Go 모듈 캐시가 있는 환경에서만 우연히 안 걸렸을
가능성). `go mod tidy`로 `go.sum`만 갱신해 고쳤다(`go.mod` 내용은
불변) — 발견 즉시 수정(이 코드베이스의 기존 관례, I2 §11/I3 §19 계승).

## 91. 목 해제 + 계약 대조 6건 — 결과 (API 소스를 직접 읽어 확정)

전부 `api/internal/onboarding/handlers.go`·`omniroute/operations.mjs`·
`omniroute/config.json` 소스를 직접 읽어 확정했다(추측 없음). 상세
근거는 `console/src/api/types.ts`의 각 타입 주석에 남겼다 — 요약:

1. **provider 식별자 enum**: 존재하지 않는다. `registerProviderCredential`
   은 값을 검증 없이 omniroute 관리 API에 그대로 전달한다. **콘솔의
   자유 입력 유지가 맞는 결정이었다** — 변경 없음.
2. **별칭 3개(I10)**: 정확히 `osiris/planner`/`osiris/worker`/
   `osiris/cheap`(`omniroute/config.json`의 `_aliasComment`가 "설계가
   정확히 이 세 이름만 허용"이라고 명시, `core`의 `allowedModelAliases`와
   일치해야 하는 고정값). 콘솔은 여전히 `GET .../alias-defaults` 응답을
   그대로 쓰므로(하드코딩 아님) 로직 변경 없음 — mock의 예시 배열만
   실제 3개 이름으로 맞췄다(`osiris/reviewer`는 실재하지 않는 이름이라
   제거).
3. **`verify-gateway` 응답 필드명 — 불일치 발견, 콘솔을 API에 맞춰
   수정**: 실제는 `gatewayHealthOk`/`aliasesInSync`/
   `propagatedToOrchestrator`/`ready`. 이전 워커가 추정한
   `healthPathOk`/`orchestratorRestarted`는 API에 없는 이름이었다 —
   `VerifyStep.tsx`가 그 필드를 읽고 있었으므로(항상 `undefined`,
   `falsy`) **실제로는 항상 "실패"로 잘못 표시되고 있었을 버그**다(mock
   모드에서만 mock이 같은 잘못된 이름을 써서 우연히 맞았던 것 — §92에
   재현 확인 기록). `types.ts`와 `VerifyStep.tsx` 둘 다 API에 맞춰 고쳤고,
   §89-1에서 실제 브라우저로 재확인했다.
4. **`registerProviderCredential`/`upsertAliases` 응답의 status enum**:
   **둘 다 이전 워커의 추정이 실재하지 않았다.** (a)
   `POST /onboarding/step0/providers`는 `{ok:true}`만 반환한다 —
   `status:'created'|'updated'` 필드 자체가 API에 없다(핸들러가 omniroute
   응답의 `created`/`updated`를 받고도 버린다). (b)
   `POST /onboarding/step0/aliases`의 `results[]`는 단일 `status`
   문자열이 아니라 **불리언 플래그 조합**(`{ok, inSync?}` |
   `{ok, converged?, before?, after?}` | `{ok, created?, after?}` |
   `{ok:false, providerMissing?, error?}`)을 그대로 통과시킨다. `types.ts`
   를 실제 형태로 교체했다 — 다행히 `ProviderStep.tsx`/`AliasStep.tsx`
   어느 쪽도 이 필드들을 렌더링하지 않고 있었어서(둘 다 `step0` 쪽 상태만
   읽는다) 런타임 동작 변화는 없다, 타입만 정직해졌다.
5. **`GET /onboarding/status`의 `phase`**: `step0.ready`와 무관하게
   **항상 무조건 `"NotStarted"`**(`Status` 핸들러가 리터럴로 고정,
   1단계 미구현이라 전이할 상태 자체가 없음, doc comment에 "phase can
   only ever be NotStarted here, honestly"). 콘솔이 이미 `step0.ready`를
   1차 게이트로 쓰고 `phase`를 보조 표시로만 쓰던 기존 방어적 설계가
   맞았다 — 확정 사실을 반영해 주석의 `[계약 미확정]` 표시만 제거.
6. **`GET /onboarding/step0/status` 단독 엔드포인트**: **존재한다**
   (`api/internal/api/server.go`에 실제로 등록돼 있고 `s.ob.Step0Status`가
   구현함, §81/§82에 이미 기록돼 있었다). 콘솔은 지금 결합
   `GET /onboarding/status`만 쓰고 이 단독 엔드포인트는 호출하지 않는데,
   그래도 무방하다 — 결합 응답이 `step0`을 최상위 필드로 그대로 포함하고
   있어 별도로 부를 이유가 없다(중복 호출 방지). 존재 확인만 기록하고
   호출부는 추가하지 않았다.

## 92. 재현 — VerifyStep.tsx의 실제 버그가 있었음을 mock 전환 전 확인

콘솔을 API에 맞추기 전, 고치지 않은 상태의 콘솔 코드를 잠깐 실행해
재현했다: `result.healthPathOk`/`result.orchestratorRestarted`는
실제 API 응답 어디에도 없는 키라 항상 `undefined`이고, JSX의
`result.healthPathOk ? ... : <StatusTag kind="error">실패</StatusTag>`
조건이 항상 거짓 분기를 타 **실제로는 헬스 검증이 통과했어도 화면은
"실패"/"대기 중"만 보여주고 있었을 것**(직접 클릭 재현: 고치기 전
브라우저에서 verify 버튼을 눌러 (a)(c) 항목이 `ready:true`인 응답에도
빨간 실패 태그로 렌더되는 것을 확인, 콘솔 devtools 없이도 스크린샷으로
확인 가능했던 시각적 버그). 고친 뒤(§91-3) 동일 호출에서 정상적으로 초록
"OK" 태그가 뜸을 재확인(§89-1의 브라우저 실측과 동일 지점).

## 93. 임의로 정한 세부 결정 (감독이 알아야 할 것)

1. **세션 저장소를 단일 프로세스 인메모리로 결정**(외부 저장소 없음) —
   `osiris-api`가 이 빌드에서 항상 단일 레플리카이고(수평 확장 스토리
   자체가 이 코드베이스 어디에도 없음), 재시작 시 재로그인 요구 정도의
   비용이 이 단계에서 감내 가능하다고 판단했다. 스케일아웃이 실제
   요구사항이 되면 재검토 대상으로 `session.go` doc comment에 명시.
2. **쿠키 이름 `osiris_console_session`, Path=`/`** — 설계가 이름까지
   못박지 않아 임의로 정했다. `osiris_` 접두사는 omniroute 자신의
   쿠키(별도 도메인 경로 `/omni/`지만 같은 오리진)와 이름이 겹치지 않게
   하려는 방어적 선택.
3. **로그아웃 UI를 Topbar 아바타 드롭다운에 최소 추가**(설계에 로그아웃
   버튼 위치가 명시돼 있지 않음) — `41-console-design-system.md`를
   수정하지 않고(수정 금지 문서) 기존 탑바 구조 안에 얹는 선에서
   그쳤다. 더 나은 배치가 있다면 콘솔 디자인 워커가 재검토.

## 94. 남은 갭

- §76의 "조직 생성 이후 콘솔 인증을 계속 이 방식으로 할지"는 여전히
  설계 공백이다 — 이번 빌드는 그 공백을 "이 세션 쿠키가 지금 당장의
  유일한 콘솔 인증"으로 메웠을 뿐, 1단계(대화형 온보딩)나 조직 생성 이후
  다중 사용자 인증(예: `:Person`별 로그인)은 여전히 미설계다.
- 세션은 단일 정적 토큰과 1:1 대응(누가 로그인했는지 구분 없음) —
  다중 운영자 감사 로그(누가 무엇을 했는지)가 필요해지면 이 세션 모델은
  재검토가 필요하다.
- 브루트포스 가드가 `POST /api/auth/session`에 없다(레이트리밋 없음) —
  omniroute 자신의 로그인에는 있다고 §55가 기록했지만 이 엔드포인트는
  없다. 루프백 기본 배포 + 32바이트 랜덤 토큰이라는 낮은 노출면을
  감안해 이번 빌드 범위에서는 넣지 않았다 — 인터넷에 완전히 여는
  배포라면 재검토 대상.

## 95. 부트스트랩 마찰 — 감독 피드백에 따라 설정 토큰을 터미널에 표면화

감독이 실제로 토큰 입력 화면을 보고 "이 값은 어디서 오는가"를 물었다 —
화면이 파일 경로만 알려주고 그 파일이 어떻게 생기는지, 다시 볼 방법이
있는지 알려주지 않아서였다. Jupyter/Gitea류 셀프호스팅 도구의 표준
관례(부트스트랩 직후 터미널에 접속 정보를 바로 보여줌)를 따라 다음을
추가했다:

- **`osiris-up` 8단계**(기존에는 "osiris-api 구현 후 가능 — 건너뜀"이던
  플레이스홀더 — 이제 실제로 구현됨): `docker compose up -d` 이전 시각
  (`UP_START_TS`, 유닉스 epoch)을 기준으로 `docker compose logs --since
  "$UP_START_TS" osiris-api`를 검사해 `main.go`가 실제로 찍는 정확한
  문구("generated a new onboarding admin setup token")가 있는지로 "이번
  실행에서 방금 생성됐는지"를 판별한다. 컨테이너가 이번 실행에서
  재생성되지 않고 그대로 떠 있었다면(변경 없음) 그 시각 이후 로그 자체가
  없으므로 자동으로 "기존 토큰" 분기로 떨어진다 — 별도의 "재생성
  여부" 상태를 스크립트가 직접 추적할 필요가 없다(로그가 이미 그 사실을
  들고 있다). **최초 생성 시에만** 콘솔 URL + 토큰 값을 stdout에 찍고,
  그 외에는 값 대신 "기존 설정 토큰이 있습니다 — 다시 보려면
  `bin/show-setup-token.sh`"만 안내한다. 실측(§96)으로 두 분기 모두
  확인했다.
- **`bin/show-setup-token.sh`**(신설): 조작자가 명시적으로 실행했을 때만
  값을 stdout에 출력하는 재조회 경로. "호스트 파일을 직접 cat하라"는
  기존 안내(그 자체가 마찰이었다)를 대체한다. 콘솔 URL도 함께 출력하되
  **루프백 주소만** 찍는다 — 상위 게이트웨이 도메인은 이 스크립트가 알
  방법이 없는 배포 시점 정보이기 때문(`gw/conf.d/default.conf` 헤더
  주석과 같은 원칙, `.env`의 `OSIRIS_GW_HOST_PORT`만 읽어 조립, 하드코딩
  없음).
- **콘솔 `TokenEntryScreen.tsx` 문구 수정**: "호스트의 `state/creds/
  admin/setup_token` 파일 내용을 그대로 붙여넣습니다"만 있던 것을,
  "이 값은 사람이 정하는 것이 아니라 설치 시 `osiris-api`가 자동 생성한
  값"이라는 출처 설명 + "최초 설치 시 `./osiris-up` 터미널에 한 번
  출력되었고, 놓쳤거나 다시 봐야 하면 `bin/show-setup-token.sh`를
  실행하라"는 재조회 경로 안내로 교체했다. 감독의 질문이 정확히 이
  지점("화면만 봐서는 출처를 알 수 없다")에서 나왔으므로, 이 문구가 그
  질문에 화면 자체에서 답하도록 만든 것이 이 수정의 목적이다.

## 96. 판단 — 단회용(single-use) 토큰으로 전환할지: **지금은 하지 않는다(b)**

감독이 명시적으로 판단을 요구한 항목. **결론: 재사용 가능하게 둔다 —
지금 단회용으로 만들지 않는다.** 근거는 감독의 직감(b)과 일치하지만,
추측이 아니라 이번 빌드 자신이 실측으로 이미 확인한 사실에 근거한다:

- §88/§89-6에서 실측했듯, 이 빌드의 세션은 **`osiris-api` 프로세스
  재시작만으로 전부 무효화된다**(인메모리 저장소, 문서화된 의도적
  한계). 프로세스 재시작은 드문 사건이 아니다 — `docker compose up -d`
  (이미지 갱신), 호스트 재부팅, `docker compose restart osiris-api`
  전부가 여기 해당하고, 이번 검증 절차 자체에서 최소 세 번 발생했다
  (§89-6의 `down && up -d`, §96 판단을 위해 토큰을 실제로 지우고
  재기동한 실측, 이 두 절차 사이의 재빌드/재기동들).
- **§76이 이미 기록한 설계 공백**(조직 생성 이후 사람이 콘솔에
  인증하는 다른 메커니즘이 설계 어디에도 없다)이 아직 열려 있다. 지금
  이 설정 토큰이 이 콘솔의 **유일한** 인증 진입점이다 — 대체 로그인
  경로가 전혀 없다.
- 이 둘을 결합하면: 토큰을 단회용(최초 교환 즉시 폐기/회전)으로
  만드는 순간, **그 다음 `osiris-api` 재시작(또는 12h TTL 만료) 이후
  대표에게는 콘솔에 다시 들어갈 방법이 없다** — 새 토큰을 발급받을
  대체 경로(예: "인증된 세션 안에서 새 설정 토큰을 회전 발급하는
  엔드포인트")가 이번 빌드에 없기 때문이다. 이건 "약간의 불편"이 아니라
  **완전한 잠금(lockout)**이고, 이번 빌드가 막 없앤 마찰(§95)보다 훨씬
  나쁜 실패 모드를 새로 만드는 것이다.
- **재검토 조건을 명시해 둔다**: (1) §76의 post-org 콘솔 인증 설계가
  채워지거나, (2) 최소한 "이미 유효한 세션 안에서 새 설정 토큰을
  회전 발급"하는 별도 엔드포인트(그 자체는 세션 인증으로 보호됨 —
  즉 로그인된 상태에서만 다음 로그인용 토큰을 미리 갈아 끼울 수 있는
  경로)가 생기면, 그때는 단회용 전환의 잠금 위험이 사라지므로 재검토
  대상이다. 이 조건 자체를 §94(남은 갭)에도 교차 기록한다.

## 97. 검증 추가분 — 부트스트랩 표면화 3종 (전부 실제 실행)

1. **최초 생성 시 출력**: 설정 토큰 파일을 실제로 삭제하고
   `osiris-api`를 재기동해(둘 다 "회전 금지" 목록에 없는 값 — 그 목록은
   `OSIRIS_OMNIROUTE_ADMIN_PASSWORD`/`OSIRIS_OMNIROUTE_MANAGE_KEY`뿐)
   "최초 생성" 조건을 재현했다. `docker compose logs --since
   <재기동 직전 시각> osiris-api`에 `generated a new onboarding admin
   setup token`이 실제로 찍힘을 확인 → 이 조건에서 `osiris-up`이 값을
   출력하는 분기를 탐을 로직으로 확인(스크립트를 통째로 재실행하는 대신
   그 판별 로직만 별도로 재현해 확인 — 전체 `osiris-up`은 부트스트랩
   전체를 다시 돌려야 해서 이번 검증에서는 판별 로직만 분리 실행).
2. **재기동 시 미출력**: 그 직후 아무 변경 없이 같은 판별을 다시
   실행 → `--since`가 그 시각 이후로 좁혀지므로 로그에 생성 문구가 없어
   "기존 토큰" 분기로 정확히 떨어짐을 확인.
3. **재조회 커맨드 동작**: `bin/show-setup-token.sh` 실행 → 콘솔 URL +
   토큰 값이 stdout에 정상 출력됨을 확인(값 자체는 이 문서/보고서
   어디에도 옮겨 적지 않음 — 상태 코드/존재 여부만 확인하는 프로젝트
   관례 그대로 적용). 이후 이 토큰으로 Playwright 브라우저 로그인 →
   세션 확립 → SSE 수신까지 다시 전부 통과함을 재확인
   (`scratchpad/retokened-*.png`).

## 98. 설계 문서 3건 정합 반영 — `10`/`31` 갱신, 코드 변경 없음

감독 지시로 앞선 워커들의 산출물이 서로 어긋난 채 남아 있던 세 지점을
`docs/design/` 문서에만 반영했다. **코드는 한 줄도 건드리지 않았다** —
`git status`/작업 로그로 확인 가능한 변경 파일은
`10-core-architecture.md`·`31-onboarding-boundary.md`·이 노트뿐이다.
`<host-infra>/` 경로는 열람도 쓰기도 하지 않았다.

### 반영 1 — `notify-adapter` 책임 분리 (`10-core-architecture.md`)

§3.2가 `notify-adapter`를 코어 내부 책임으로만 적어 두어, 뒤에 확정된
`50-integration-adapters.md`(알림을 격리된 `NotificationAdapter` 컨테이너로
분리)와 글자 그대로 겹쳐 읽으면 "텔레그램에 실제로 HTTP를 쏘는 코드가
`osiris-core` 안에 있다"는 오독이 가능했다. `50-integration-adapters.md`
§7 Q1이 이미 이 오독 가능성과 권고 문구를 남겨 두었으므로, 그 권고를
그대로 반영했다:

- §3.2 표의 `osiris-core` 행을 갈랐다 — **남는 것**: 누구에게/언제/어느
  심각도로 알릴지 결정해 `NotifiableEvent`를 만들어 어댑터 대면 포트
  (`:8091`)에 얹는 것까지. **나가는 것**: 실제 텔레그램 Bot API 호출(격리된
  `osiris-adapter-telegram`이 수행).
- §7.1 NATS 서브젝트 트리의 `human.notify.<employeeId>` 주석을 "notify-adapter가
  외부 채널로 팬아웃"에서 "core가 소비해 `:8091`에 얹고, 실제 팬아웃은
  격리된 어댑터가 수행"으로 정정 — 원 주석이 이 분리 이전의 상태를
  전제하고 있었다.
- §10.3의 "notify-adapter → SMTP" 문구도 같은 이유로 정정.

**파급효과 4개 점검 결과**:
1. **G-금지사항(A4/A5/G7 계열) 정합** — §1.7 표 뒤에 점검 문단을 추가했다.
   A4("core 이미지에 게이트웨이 자격증명 없음")와 같은 논리를 텔레그램 봇
   토큰 등 알림 채널 자격증명에도 적용한 결과라, 이 분리는 G5/A4를
   위반하지 않을 뿐 아니라 **"외부로 나가는 자격증명은 그것을 실제로
   쓰는 컴포넌트 하나에만 있어야 한다"는 원칙을 코어 밖으로 한 겹 더
   넓혀 강화한다** — G7(Neo4j 단일 관문)·G8(docker.sock 미마운트)·G4(compose에
   조직정보 없음)와도 개별적으로 대조해 위반 없음을 확인했다.
2. **§8.3 네트워크 배치** — 어댑터 격리 근거(에이전트와 동일 등급이지만
   근거는 "제3자 코드"로 다름), `osiris-adapters` 신설망 단독 소속,
   `osiris-core`의 세 번째 대면 포트(`:8091`), egress 경유 강제, D10
   불변(dev-net 멤버는 여전히 2개)을 §8.3에 새 문단으로 추가했다. §2.6의
   네트워크 배치표에는 어댑터 행을 신설해 "상세 배치는 `50-integration-
   adapters.md` §2.2가 소유"라고 명시적으로 위임했다(표를 복제하지 않음 —
   두 문서가 각자 다른 값을 들고 있게 되는 사고를 피하기 위함).
3. **§9.5 시크릿 표** — Plane API 토큰·텔레그램 봇 토큰 두 행을 추가했다.
   저장 위치(`/var/run/osiris/creds/<adapter>_token`, 0400), 소비자(해당
   어댑터 컨테이너뿐, core는 값을 보지 않음), 판정 근거(31의 Q2b 적용
   결과)를 기존 표의 형식 그대로 채웠다.
4. **§2.9 경로 네임스페이스** — 소스를 확인한 결과(`50-integration-
   adapters.md` §4.4, 텔레그램은 `getUpdates` long-polling으로 확정)
   두 어댑터 모두 인바운드 게이트웨이 경로가 필요 없다. 표에 행을 추가하는
   대신 "지금은 필요 없다"는 사실과 재검토 트리거(향후 webhook 전용
   어댑터 추가 시 `/_hooks/<adapter>` 예약 필요)를 명시하는 문단을
   추가했다 — 사실이 아닌 것을 표로 만들지 않기 위해서다.

### 반영 2 — `31-onboarding-boundary.md` 축C에 Plane·텔레그램 행 추가

§1.3의 7문항 절차를 실제로 실행해 §2.3에 두 행을 추가했다(결론만 옮기지
않고 Q1→Q2→Q2a→Q2b 각 단계의 판정을 표에 그대로 남겼다):

- **Plane API 토큰**: Q1 아니오 → Q2 예(대표가 화면에서 붙여넣음) → Q2a
  예(조직의 미러링 대상) → **Q2b 그렇다** — Plane 워크스페이스 API 토큰은
  애초부터 계정 비밀번호가 아니라 이미 스코프 제한된 API 전용
  자격증명이라, "교환"이 필요 없고 사람이 건네는 값 자체가 이미 Q2b가
  요구하는 최종 형태다. 사건 4(교환 경로를 확인하지 않고 그냥 저장한
  실수)와는 다르다 — 여기서는 "교환할 원본이 따로 없음"을 확인한 뒤
  저장을 결정했다.
- **텔레그램 봇 토큰**: Q1 아니오 → Q2 예 → Q2a 예("이 조직의 알림을
  어느 봇이 보내는가") → **Q2b 그렇다, 다른 근거로** — 봇 토큰은 애초에
  사람 계정을 대표하지 않는 단일 목적 기계 자격증명이므로(텔레그램이
  계정 비밀번호와 봇 토큰을 애초에 분리해 둠) 교환 없이 원본 저장이
  정당하다.
- 개인 텔레그램 `chat_id`는 축C에 행을 추가하지 않았다 — 베어러
  시크릿이 아니므로 Q2 자체를 타지 않고 Q3(그래프의 일부, 축D)로 바로
  간다는 점을 비고로 남겼다(`50-integration-adapters.md` E7과 일치).
- §6에 B5 행을 추가해 `50-integration-adapters.md` §7 Q3의 인계(축C
  공백)가 이번 작업으로 해소되었음을 기록했다.

### 반영 3 — `bootstrap-master` 생성 시점 정정 (`10-core-architecture.md` §4.1/§8.5)

§4.1·§8.5가 "`osiris-up`이 코어 기동과 동시에 `bootstrap-master`를
만든다"는 낡은 서술을 그대로 갖고 있었다 — `30-onboarding-provisioning.md`
§1.0(온보딩 0단계)이 이미 이 순서를 바꿔 놓았는데도(감독이 G8 위반으로
표시). §4.1에 0단계 삽입 이후의 실제 흐름(`osiris-up` → 0단계 통과 →
`osiris-orchestrator`가 API 트리거로 `bootstrap-master` 생성 → 대화형
온보딩)을 반영했다 — `bootstrap-master`의 **정체성**(그래프 노드 아님,
genesis 라벨, 권한 스코프, 폐기 경로)은 무변경이며 바뀐 것은 **생성
시점과 호출 주체**뿐임을 명시했다.

**genesis principal 정합성 검증 결과 — 다섯 겹 중 넷은 안 깨지고, 하나는
문구가 깨져 있어 정정했다**:

- W1(구조적 폐쇄)·W3(정상 revoke)·W5(재온보딩 비재개방): "조직이 비어
  있는가"만으로 조건이 서술되어 있어 `bootstrap-master` 생성 지연과
  무관하게 그대로 성립한다.
- W2(24시간 TTL): 안 깨지지만 새로운 상호작용을 발견해 명시했다 —
  온보딩 0단계가 24시간을 넘기면 토큰이 `bootstrap-master` 생성 전에
  만료될 수 있다. 재해는 아니다(`currentOrgRevision`이 여전히 0이므로
  재발급으로 복구 가능) — 그러나 이 경계 조건은 원 설계에 없던 것이라
  §8.5의 W2 행에 주석으로 추가했다.
- **W4(컨테이너 바인딩)는 실제로 깨져 있었다.** 원 문구 "토큰은 발급
  시 genesis 컨테이너 ID에 바인딩된다"는 발급과 컨테이너 생성이
  사실상 동시였던 구판 설계를 전제한다. 온보딩 0단계 삽입으로 발급
  시점(`osiris-up` 실행 중)과 컨테이너 생성 시점(0단계 통과 후, 사람이
  폼을 채우는 임의 길이의 구간 뒤) 사이에 간격이 생기면서, "발급 시
  바인딩"은 바인딩할 대상이 아직 존재하지 않아 문자 그대로 성립할 수
  없는 모순이 됐다. **직접 정정했다** — "최초 사용 시점(=
  `bootstrap-master`가 처음 core에 연결하는 순간)에 바인딩"으로
  바꾸고, 그 사이 구간에 토큰이 유출되면 유출자가 먼저 접속해 바인딩을
  가로챌 수 있다는 잔여 위험을 잔여 위험으로 명시했다(TOFU의 일반적
  한계, W2의 24시간 상한이 이 구간을 좁히는 역할을 한다는 점도 함께).
  이 정정은 §11 결정표 A13("특권은 토큰 소지가 아니라 '조직이 비어
  있는가'에 조건부")이나 §9.5 시크릿 표, G-금지사항 어느 것과도
  충돌하지 않는다고 판단해 상한(멈추고 보고)에 해당하지 않는 것으로
  보고 직접 정정했다 — W4는 그 표들이 다루는 결정이 아니라 §8.5 자신의
  구현 세부(바인딩이 언제 일어나는가)이기 때문이다.

### 상한에 걸려 멈춘 것

없다. 세 반영과 파급효과 점검 전부가 기존 G-금지사항·§9.5 시크릿
표·§11 결정표(A1~A13)와 충돌 없이 완료됐다. W4 재문구화는 위에서
설명한 대로 "결정의 번복"이 아니라 "서술 오류의 정정"으로 판단했다.

### 이번 작업에서 임의로 내린 결정 전부

1. §3.1 다이어그램 자체(ASCII 박스의 `notify-adapter` 라벨)는 손대지
   않고, 그 앞뒤에 정정 문단만 추가하는 쪽을 택했다 — 박스 폭을
   맞추려 라벨을 바꾸면 다이어그램 정렬이 깨질 위험이 있었고, 이 문서의
   표기 관례(정정은 원문 보존 + 갱신 각주)와도 더 맞는다고 판단했다.
2. §9.5에 추가한 두 자격증명 행의 정확한 파일 경로(`plane_api_token`,
   `telegram_bot_token`)는 `50-integration-adapters.md` §1.2가 준
   패턴(`/var/run/osiris/creds/<adapter>_token`)을 그대로 따랐다 — 그
   문서가 이미 확정한 값이라 임의 결정이라기보다 이관이지만, 정확한
   파일명 문자열 선택은 이 워커가 했다.
3. §2.9에 새 표 행을 추가하지 않고 문단으로만 기록한 것 — 표는 "실제로
   예약된 경로"만 담는 것이 지금까지의 관례였고, "필요 없음"이라는
   부정적 사실을 표 행으로 만들면 나중에 다른 워커가 빈 칸을 채워야
   할 항목으로 오인할 위험이 있다고 판단했다.
4. W4 문구를 "발급 시 바인딩"에서 "최초 사용 시 바인딩"으로 바꾼 것 —
   위에서 설명했듯 상한 조건(G-금지사항/§9.5/§11 충돌)에 해당하지
   않는다고 스스로 판단해 멈추지 않고 직접 정정했다. 이 판단 자체가
   감독이 검토해야 할 가장 중요한 임의 결정이다.
5. §4.1 step 3 주석에 "`osiris-omniroute`도 빈 DB로 기동"을 추가하며
   기존 "코어 7개"라는 표현은 그대로 두었다 — `osiris-omniroute`가 이제
   `osiris-up`이 띄우는 8번째 서비스인지, 여전히 "코어 7개" 밖의
   부속물로 셀지는 이 세 반영의 범위 밖이라 판단해 숫자 자체는 고치지
   않았다. 다음 감사에서 확인이 필요한 잔여 사항으로 남긴다.

## 99. genesis 토큰 발급 시점을 컨테이너 생성 시점으로 재이동 — §98의 TOFU를 되돌림 (설계 문서만, 코드 변경 없음)

**배경과 지시 범위**: §98(직전 워커)은 온보딩 0단계(`30-onboarding-provisioning.md`
§1.0) 삽입으로 `bootstrap-master` 컨테이너 생성이 사람의 폼 입력 이후로
늦춰지자, genesis 토큰의 **발급 시점은 그대로 두고**(`osiris-up` 실행 중)
W4("발급 시 컨테이너 ID에 바인딩")만 "최초 사용 시 바인딩"(TOFU)으로
고쳤다. 그 워커는 이 재문구화를 "상한(멈추고 보고) 조건에 해당하지
않는다"고 스스로 판단해 직접 정정했다 — 그러나 그 판단은 워커의 것이
아니었다. 감독·사용자가 이 지점을 재검토해 **다른 결정**을 내렸다:
TOFU를 유지하는 대신, **genesis 토큰의 발급 시점 자체를 `osiris-up`에서
`bootstrap-master` 컨테이너 생성 시점으로 옮긴다.** 이 작업은 그 확정된
결정을 설계 문서에 정합하게 반영하는 것이었고, 결정의 타당성 자체는
재검토 대상이 아니었다. **코드는 한 줄도 건드리지 않았다** — genesis
토큰은 여전히 미구현 상태이고(`CORE_API_TOKEN`이 임시로 그 자리를
메우고 있음, §9), 이번 작업은 `docs/design/` 세 파일만 수정했다.
`<host-infra>/` 경로는 열람도 쓰기도 하지 않았다.

### 왜 이 재정정이 필요했는가

TOFU(§98)는 동작은 했으나, **발급~최초 바인딩 사이에 토큰이 유출되면
유출자가 먼저 접속해 바인딩을 선점할 수 있는 잔여 위험**이 남았고, 그
구간의 길이가 사람의 온보딩 0단계 폼 입력 시간이라 통제 불가능했다.
발급 시점을 컨테이너 생성 시점으로 옮기면 그 구간 자체가 사라져 W4가
원래 의도(발급=바인딩)대로 문자 그대로 성립한다. 부수 효과로 호스트에
0400으로 상주하던 고정 경로(`state/creds/genesis/token`)가 없어진다.

### 고친 곳 전부

**`10-core-architecture.md`**
- §4.1 부트스트랩 절차 8단계 출력 문구: "genesis 토큰은 이 실행 중 1회
  발급"을 "genesis 토큰도 아직 발급되지 않는다"로 정정.
- §4.1의 "[2026-07-27 갱신]" 문단 — "genesis 토큰의 발급 시점 자체는
  이 정정과 무관하게 그대로다"라는, 이번 정정으로 거짓이 된 문장을
  걷어내고 3차 정정 이력(1차 동시발급 → 2차 TOFU → 3차 발급시점 이동)을
  요약해 추가.
- §8.5 genesis 토큰 표: 발급 주체(`osiris-up` → `osiris-core`, 요청은
  orchestrator가 트리거), 발급 조건(판정 주체를 core로 명시), 저장·주입
  (호스트 고정 경로 소멸, 컨테이너별 경로 `state/creds/<containerID>/
  genesis_token`으로 대체) 세 행 전면 개정.
- §8.5의 "[2026-07-27 신설] 발급 시점과 소비 시점의 분리" 문단 전체를
  재작성 — 1차/2차/3차 이력, **창이 실제로 0이 되는 7단계 순서**
  (`ContainerCreate`(미기동) → ID 확보 → core에 ID로 발급 요청 →
  `currentOrgRevision==0` 확인 후 발급=바인딩(원자적) → 파일 주입 →
  `ContainerStart`), "왜 좁힌 것이 아니라 없앤 것인가"의 근거(바인딩이
  "누가 먼저 접속하는가"의 경쟁이 아니라 core의 단일 발급 결정이라는
  점), core 요청 인증 주체(기존 `CORE_API_TOKEN`)와 revision 판정
  주체(core 자신)의 명시, W1~W5 재검증 결과를 모두 담았다.
- W1~W5 표: W2(24시간 TTL) 행에서 직전 워커가 남긴 "0단계가 24시간을
  넘기면 만료될 수 있다"는 경계조건이 사라졌음을 명시. W4 행을 "발급 시
  바인딩"으로 복구하고 TOFU였던 이력과 잔여 위험이 사라진 이유를 남김.
- §9.6 인계 문단의 "genesis 토큰 발급-소비 시점 분리, W4 바인딩 문구
  정정"이라는, 이제 낡은 요약을 3차 정정 결과로 갱신.

**`30-onboarding-provisioning.md`**
- O12 행: "genesis 토큰 발급은 여전히 `osiris-up`이 1회 수행(무변경)"을
  걷어내고 발급도 함께 이동했음을 명시, 근거 열에 TOFU 잔여 위험과 그
  제거를 추가.
- §1.0.7 "생성 주체도 바뀐다" 문단: "genesis 토큰의 발급 시점은
  무변경이다"라는 문장을 걷어내고, 발급이 컨테이너 생성 시퀀스 안으로
  옮겨졌음을 구체적 순서와 함께(§8.5로 위임) 재서술.
- §1.4 흐름도: "관리자 1회용 로그인 토큰 출력. genesis 토큰도 이 시점에
  발급(§8.5, 무변경)" 줄을 "아직 발급되지 않는다"로 정정하고,
  `bootstrap-master` 컨테이너 생성 화살표에 실제 발급 시퀀스를 인라인으로
  추가.
- §9(상위 문서 갭 추적표)의 G8(신규) 행에 "이 갭은 이후 실제로 해소됐다"는
  후속 각주를 추가(2차 해소 이후 3차 정정까지의 경위 요약) — 표 자체의
  판정을 바꾸지 않고 이력만 덧붙였다.
- 헤더의 작성일 각주에 3차 정정 날짜 추가.

**`31-onboarding-boundary.md`**
- §2.2 축B 자격증명 표의 "Genesis 토큰(§8.5)" 행: "`osiris-up`이 발급,
  `state/creds/genesis/token`"이라는 이제 거짓인 서술을 "`osiris-core`가
  발급, `state/creds/<containerID>/genesis_token`(고정 상주 경로 없음)"
  으로 정정하고, 발급 주체가 바뀐 이력을 괄호로 남겼다. 이 파일은 원
  작업 지시서의 "먼저 읽으세요" 목록에는 없었지만, 완료조건 2번(grep으로
  "genesis 발급은 `osiris-up`이 수행"이 어디에도 남지 않아야 함)을
  만족시키려면 반드시 고쳐야 하는 지점이었다 — grep으로 실제로 찾아서
  고쳤다(아래 grep 결과 참고).

### 완료조건 2 — grep 검증

아래 세 질의로 확인했다(전부 `docs/design/` 기준):

```
grep -n "osiris-up" *.md | grep -i "발급"
grep -n "무변경" *.md | grep -i "osiris-up\|genesis"
grep -n "genesis.*osiris-up.*발급\|osiris-up.*genesis.*발급\|발급.*osiris-up.*genesis" *.md
```

세 질의를 합쳐 총 9줄이 매치됐다(`10-core-architecture.md` 4곳,
`30-onboarding-provisioning.md` 3곳, `31-onboarding-boundary.md` 1곳,
`IMPLEMENTATION-NOTES.md` 1곳). 하나씩 확인한 결과:

- `10-core-architecture.md`·`30-onboarding-provisioning.md`·
  `31-onboarding-boundary.md`의 매치는 전부 **"이전에는 `osiris-up`이
  발급했으나 지금은 아니다"라는 이력 서술**이거나 **"더 이상 발급하지
  않는다"는 부정문**이다 — "genesis 토큰 발급은 `osiris-up`이 수행한다"는
  현재형 단언은 이 세 파일 어디에도 남아 있지 않다.
- `IMPLEMENTATION-NOTES.md:150`(§9, 6단계 구현 워커의 기록)만 과거
  현재형("`osiris-up`이 1회 발급") 그대로다 — **이 파일은 append-only이고,
  이 문장은 그 문장이 쓰인 시점(genesis 토큰 미구현, §9 당시 설계)의
  기록으로서는 정확했으므로 고치지 않았다.** 살아있는 설계 문서
  (10/30/31)가 아니라 과거 구현 세션의 로그이므로, 이 문장을 지금 와서
  다시 쓰면 오히려 append-only 원칙을 어기고 그 세션이 실제로 무엇을
  했는지의 기록을 훼손하게 된다. 완료조건 2("`docs/design/` 어디에도
  남아 있지 않을 것")의 취지는 **살아있는 설계 서술**을 향한 것으로
  읽었고, 이 절(§99)이 그 사실을 명시적으로 인지하고 있다는 것 자체가
  "방치"와 "의도적으로 보존"을 구분해 준다고 판단했다.

### 창이 실제로 0이 되는 순서 — 설계 요지

컨테이너 ID는 컨테이너를 만들기 전에는 존재하지 않으므로, "발급이 곧
바인딩"이 성립하려면 발급 시점에 이미 core가 그 ID를 알고 있어야 한다.

```
1. osiris-api: 온보딩 0단계(0-1~0-5) 전부 통과 감지 → orchestrator에
   bootstrap-master 생성 요청
2. osiris-orchestrator: ContainerCreate만 호출(ContainerStart는 아직).
   이미지·라벨(osiris.dev/genesis=true)·네트워크는 §1.3과 동일.
   → 이 호출은 프로세스를 기동하지 않고 컨테이너 ID만 확정한다.
   (기존 §1.4 Apply()가 이미 create/start를 분리된 두 호출로 다루는
   패턴 그대로 — 새 메커니즘 아님)
3. orchestrator: 반환된 실제 컨테이너 ID 획득. 이 시점까지 genesis
   토큰은 존재하지 않는다.
4. orchestrator → osiris-core:8090, {containerID} 실어 발급 요청
   (기존 CORE_API_TOKEN으로 인증 — 새 신뢰 경로 아님)
5. osiris-core: currentOrgRevision==0 확인(core 자신이 Neo4j 직접
   읽어 판정, D17/G7과 정합) → 참이면 그 자리에서 발급 + 그 containerID
   에 바인딩(하나의 원자적 호출 — "발급됐지만 안 묶인" 상태가 없음)
6. orchestrator: 응답받은 토큰을 컨테이너가 아직 시작되지 않은 상태에서
   호스트의 자격증명 디렉토리(2단계에서 이미 /var/run/osiris/creds로
   마운트 선언됨)에 파일로 기록(G5 파일 주입)
7. ContainerStart
```

**왜 "좁다"가 아니라 "0"인가**: 2차 정정(TOFU)의 잔여 위험은 "유출된
토큰을 누가 먼저 써서 바인딩을 선점하는가"라는 **경쟁**이었다 — 바인딩이
"누가 먼저 연결하는가"로 결정됐기 때문이다. 이번 설계는 바인딩을 그
경쟁에서 완전히 빼낸다: 바인딩은 core가 4~5단계에서 orchestrator의
인증된 요청 하나만 보고 **일방적으로** 확정하며, 그 확정은 제3자가
"먼저 접속"하는 것으로 바꿀 수 있는 값이 아니다. 따라서 "유출 후 선점
경쟁"이라는 범주의 위험 자체가 성립할 수 없다 — 시간을 줄인 것이
아니라 경쟁 구조 자체를 없앤 것이다.

**인증·판정 주체 (지시받은 두 질문에 대한 답)**:
- core가 발급 요청을 받을 때 그 요청을 인증하는 것: 기존
  `CORE_API_TOKEN`(orchestrator가 core의 graph-write API를 부를 때 이미
  쓰는 정적 플랫폼 토큰)으로 충분하다고 판단했다 — 이 요청도 성격상
  기존 graph-write 호출과 같은 "orchestrator→core 플랫폼 내부 호출"이고,
  오직 orchestrator만 docker.sock을 가져(G8) "방금 만든 컨테이너의 진짜
  ID"를 주장할 수 있는 유일한 발신자이기 때문이다.
- `currentOrgRevision == 0` 판정 주체: `osiris-core` 자신(D17/G7 — core가
  유일한 그래프 관문이므로 이 판정의 소유자이기도 하다). 판정 불가
  (Neo4j 다운) 시 거부한다는 §8.5의 기존 fail-closed 원칙은 무변경이다.

### W1~W5 재검증 결과

- **W1(구조적 폐쇄)**: 안 깨짐. "조직이 비어 있는가"만으로 조건이
  서술되어 발급 시점 이동과 무관.
- **W2(24시간 TTL)**: 안 깨짐 — 그리고 §98이 남긴 "0단계가 24시간을
  넘기면 만료될 수 있다"는 경계조건이 **사라진다.** TTL 기산점이 이제
  컨테이너 생성(=발급) 시점이므로, 사람의 0단계 폼 입력은 이미 발급보다
  앞서 끝나 있다.
- **W3(정상 경로 revoke)**: 안 깨짐. revoke는 온보딩 커밋 이후의 일이므로
  발급 시점과 무관.
- **W4(컨테이너 바인딩)**: **복구됐다.** "발급 시 바인딩"으로 원래
  문구가 되돌아왔고, TOFU의 잔여 위험도 사라졌다(위 "왜 0인가" 참고).
- **W5(재온보딩 비재개방)**: 안 깨짐. W1이 닫힌 이후의 이야기이므로
  발급 시점 이동과 무관.

다섯 겹 폐쇄 조건 중 어느 것도 이번 정정으로 깨지지 않았다.

### 상한에 걸려 멈춘 것

없다. 창을 0으로 만드는 순서를 찾았고(위), W1~W5 전부 재검증을
통과했으며, §1.7 G-금지사항·§9.5 시크릿 표·§11 결정표(A1~A13) 중
**결정 자체**를 고쳐야 하는 항목은 없었다 — §8.5 genesis 토큰 표와
31-onboarding-boundary.md의 시크릿 판정표 행은 이번 지시(항목 1·7)가
명시적으로 요구한 **사실 갱신**(발급 주체·저장 경로가 실제로 바뀌었으므로
그 표의 값을 갱신하는 것)이지, G1~G10의 금지 내용이나 A1~A13의 결정문,
§9.5 표의 분류 기준(예: "사람이 화면에서 직접 보는가?") 자체를 바꾼
것이 아니다. G5(파일 주입)·G8(docker.sock은 orchestrator에만)·D17/G7
(core가 유일한 그래프 관문)은 이번 설계가 그대로 재사용한 기존 원칙이며
예외를 하나도 추가하지 않았다.

### 이번 작업에서 임의로 내린 결정 전부

1. core의 새 발급 요청 엔드포인트를 구체적인 경로 문자열로 명명하지
   않았다 — `osiris-core:8090`(기존 에이전트 대면 포트, 기존
   `CORE_API_TOKEN` 인증)을 그대로 재사용한다는 것만 명시하고, 정확한
   라우트 이름(`POST /internal/genesis-token` 류)은 실제 구현 워커의
   재량으로 남겼다. `osiris-api`→`osiris-orchestrator` 트리거 API도
   마찬가지로 기존에 이미 미상세 상태로 남아 있던 지점이라(§1.0.7이
   원래부터 "API 요청으로 트리거"라고만 적어 두었음) 이 작업이 새로
   구체화하지 않았다.
2. 호스트 경로 명명(`state/creds/<containerID>/genesis_token`)을
   기존 일반 에이전트 패턴(`<host>/creds/<id>/`, §1.4 Apply() 2단계)의
   `<id>` 자리에 컨테이너 ID를 그대로 대입하는 방식으로 정했다 — 다른
   이름(예: 임시 발급요청 ID)도 가능했겠으나, 기존 패턴과 최대한
   동형으로 맞추는 쪽을 택했다.
3. orchestrator가 크래시로 `ContainerCreate`와 `ContainerStart` 사이에서
   중단되는 경우(컨테이너는 존재하나 토큰 발급/주입이 안 된 상태)의
   복구 절차를 W1~W5의 새 조건으로 만들지 않고, 기존 Apply()의 멱등
   재시도 철학이 그대로 적용된다는 가정만 짧게 남기고 상세 설계는 하지
   않았다 — 이 케이스는 지시받은 다섯 겹 폐쇄조건(W1~W5) 중 어느 것도
   아니고, 지시 범위("창을 0으로 만드는 순서")를 벗어난 별도의 장애
   복구 설계라고 판단했다. 다음 워커가 필요시 다룰 사항으로 남긴다.
4. IMPLEMENTATION-NOTES.md §9(6단계 구현 워커의 기록, line 150 부근)의
   "genesis 토큰 발급·저장 경로(`osiris-up`이 1회 발급...)"라는 과거
   기록을 고치지 않기로 한 것 — append-only 원칙과 "그 시점 기록으로는
   정확했다"는 점을 근거로 판단했으나, 이 판단 자체는 감독이 재확인할
   가치가 있다(완료조건 2가 이 줄도 없애라는 뜻이었다면 append-only
   원칙과 충돌한다).

## 100. 사람 신원·인증 설계 — `60-identity-authn.md` 신설 (설계 문서만, 코드 변경 0줄)

**지시 범위**: §76이 `[갭]`으로 올려 두고 §94가 세 항목으로 구체화한 공백
— "조직이 생성된 이후 사람이 콘솔에 인증하는 방법이 설계에 존재하지
않는다" — 을 설계로 메운다. **구현 코드는 한 줄도 쓰지 않았다.** 변경한
파일은 `docs/design/60-identity-authn.md`(신설)·`docs/design/40-console-ia.md`
(감독이 명시 허용)·이 노트뿐이다. `api/`·`core/`·`console/`·`orchestrator/`·
`gw/`·`docker-compose.yml`은 **읽기만** 했고(현행 동작을 근거로 삼기 위해
`api/internal/{session,authn,api}`·라우터 등록부를 실제로 읽었다),
`<host-infra>/` 경로는 열람도 쓰기도 하지 않았다. 자격증명 값은
어디에도 출력·기록하지 않았다.

### 100.1 산출물

- **`docs/design/60-identity-authn.md`(신설)** — 결정 **H1~H21**, 불변조건
  **HI1~HI12**(`bin/lint-identity-authn.sh` 명세 포함), 온톨로지 인접
  제안 **OD1~OD4**, 상위 문서 발견사항 **F1~F8**.
- **`docs/design/40-console-ia.md`(수정)** — 화면 #0(로그인/등록) 신설,
  "전원 동일 사이드바" 전제 수정, 전역 인증 UI 3종, §F REST 7행 추가,
  비-API 계약 2항 추가.

### 100.2 가장 중요한 판단 — 자격증명은 온톨로지에 넣지 않는다 (H1)

`:Person`에 필드를 추가하는 안과 별도 저장소를 두는 안을 둘 다 검토했고
**별도 저장소를 택했다.** 결정적 근거는 **C8**(`20-ontology-schema.md`
§11: "모든 의도 변경 트랜잭션은 `:OrgRevision`을 정확히 1 증가시킨다")
이다 — 비밀번호를 그래프에 두면 (a) 비밀번호 변경이 조직 리비전을
올려 수렴 루프를 돌리고 Q8(리비전 기준 조직 재현)을 오염시키거나,
(b) C8에 예외를 파야 한다. **(b)는 기존 불변조건의 약화이므로 이 작업의
상한에 해당한다** — 즉 안 A는 채택하는 순간 멈추고 보고해야 하는
설계였다. 안 B는 그 문제를 만들지 않는다. 나머지 네 근거(D1 3분할
어디에도 속하지 않음 / `:Person`이 광범위하게 읽히는 노출 표면 /
Neo4j 오프호스트 백업 경계 / 온보딩 단일 트랜잭션과 수명이 다름)는
60 §2.2에 있다.

**저장소는 NATS JetStream KV**(`AUTHN_CRED`/`AUTHN_SESSION`/
`AUTHN_TICKET`/`AUTHN_THROTTLE`, `osiris-api` 단독 소유). 호스트 파일과
새 DB를 기각한 결정적 이유는 **A2**다 — [2단계]에서 `osiris-api`는
서버 노드 DaemonSet(다중 레플리카)이 되므로 노드 로컬 저장은 "[2단계]로
가는 길을 막는 결정"이 된다. 버스는 §1.2가 이미 "두 단계를 가로지르는
불변 자산"으로 못박은 것이라 새 컴포넌트가 늘지 않는다.

**저장 형태**: 재생 가능한 원본은 하나도 저장하지 않는다(H3) — 세션
id·티켓은 `sha256`만, 비밀번호는 Argon2id 검증자만. 이건 새로 만든
규칙이 아니라 `20-ontology-schema.md` §3.8의 `:JoinToken`("평문 토큰은
저장하지 않는다")을 사람 자격증명으로 넓힌 것이다.

### 100.3 §96의 판단을 뒤집는다 — 단회용 전환은 이제 가능하다

§96은 단회용을 유보하며 재검토 조건 (1)"§76의 post-org 콘솔 인증 설계가
채워지면"을 명시했다. **이 문서가 그 조건을 충족한다.** 그리고 §96이
단회용을 막았던 **유일한 실질 근거**("세션이 `osiris-api` 재시작만으로
전부 무효화되므로 단회용으로 만들면 완전한 잠금")는 H2(KV 세션)로
소멸했다 — 재시작·재부팅·`up -d` 어느 것도 세션을 죽이지 않는다.

**두 겹으로 닫는다**: (a) **H11** 교환 성공 시 즉시 회전(사실상 단회용,
조직 생성 *전* 창을 좁힘), (b) **H10** `currentOrgRevision >= 1` **이고**
초기 관리자가 자격증명을 가짐 → 영구 거부(조직 생성 *후* 창을 없앰).
H10을 두 조건으로 만든 이유는 데드락 회피다 — 조건 1만으로 닫으면
세션 승격이 실패한 경우(브라우저를 닫았다든지) 아무도 못 들어간다.
이 2조건 구조는 **W1을 본뜬 것이지 W1을 수정한 것이 아니다**(W1은
genesis 스코프에, H10은 콘솔 설정 토큰에 적용되며 서로 참조하지 않는다).

### 100.4 W1~W5 / D1~D20 / A1~A13 / G1~G10 / I1~I12 전수 대조 — 위반 0건

- **W1~W5**: 60 §4.2에 다섯 줄 표로 대조했다. 핵심은 **genesis 토큰은
  `bootstrap-master`가 `osiris-core`를 부르는 스코프**이고 **콘솔 세션은
  사람이 `osiris-api`를 부르는 신원**이라 발급자·저장소·검증 경로·폐쇄
  조건이 전부 다르다는 것이다(§75가 이미 확립한 구분 위에 세웠다).
  조직 생성 커밋 시 세션을 초기 관리자로 승격하는 H9도 core의 genesis
  스코프를 발급하지 않고 `currentOrgRevision`을 되돌리지 않는다.
  break-glass(H17/H18)도 마찬가지다.
- **D1**: 자격증명은 의도/물성/관측 셋 중 어디도 아닌 **네 번째 종류**
  임을 명시하고 그래프 밖에 뒀다. Neo4j에 두 번째 writer가 생기지 않고
  D17/G7/A5의 단일 관문이 그대로다.
- **A2**는 뒤집은 것이 아니라 H2의 **근거로 사용**했다. **A13**은 무영향.
- **G1~G10**: 워크스페이스·드라이버·docker.sock·compose·네트워크 모드를
  건드리지 않는다. `.env` 신규 변수 **0건**(H13)이라 G5·31 §5.1의
  태그 대상이 늘지 않는다.
- **I1~I12**: 60 §14.2에 12줄 전수 대조표를 넣었다. 약화 0건.
  OD2(권한 코드 `org.admin` 1개 추가)를 채택하면 **I9에 준수 요구가
  생기지만**(카탈로그와 grant-catalog의 `catalogVersion`을 함께 올려야
  함) 이는 약화가 아니라 강화된 준수 조건이다.

### 100.5 `31-onboarding-boundary.md` §1.3 7문항 — 실제로 돌린 결과

두 개의 새 값에 절차를 실행하고 각 문항의 판정을 60 §5.3에 남겼다
(결론만 옮기지 않았다).

**① 사람의 비밀번호**: Q1 아니오 → Q2 **예** → Q2a 조직에 관한 것 →
**Q2b: 교환 경로가 있다 — 이 설계가 그 경로다.** 두 겹으로 그렇다:
(i) 반복 사용되는 값은 비밀번호가 아니라 세션 id(불투명 기계
자격증명)이고 비밀번호는 로그인 요청 1회에서만 소비된다, (ii) 서버가
보관하는 것은 원본이 아니라 **Argon2id 검증자** — 복원 불가, 어떤
시스템에도 제출 불가, 재사용 불가. → **원본 절대 미저장.** 산출물
(세션 id)에 Q2 재귀 → 아니오(HttpOnly) → Q3 이하 순수 기계 간 인증.
Q3에서 **자격증명은 그래프의 일부가 아님**(=질문 1의 답)이 확인되고,
`:Person` 노드 자체는 Q3 예(온보딩)로 갈린다. **최종: 런타임 중 —
사람이 최초 로그인 때 스스로 정한다. 설치 시점 0건, 온보딩 0건.**

> **사건 4와의 차이를 명시했다.** 사건 4는 omniroute 비밀번호의
> **평문**(= omniroute에 그대로 재제출 가능한 원본)을 `.env`에 저장하려던
> 것이었다. 여기서 저장하는 것은 **제출 불가능한 검증자**다. "해시니까
> 괜찮다"로 건너뛰지 않고 Q2b를 실제로 물은 결과가 이 구분이다.

**② 등록/복구 티켓**: Q1 아니오 → Q2 예 → Q2a 조직에 관한 것 →
**Q2b: 그렇다 — Plane 토큰·텔레그램 봇 토큰과 같은 모양으로 통과하되
한 걸음 더 간다.** 티켓은 애초부터 스코프 제한된 단일 목적 자격증명
(1회용·7일·단일 `:Person` 바인딩·"자격증명 최초 설정" 액션 하나)이라
교환할 원본이 없다(여기까지 31 §2.3의 두 사례와 동일). **다른 점**:
Plane/텔레그램 토큰은 외부 API에 제출해야 해서 원본 보관이 불가피했지만
티켓은 우리가 검증만 하면 되므로 **원본 보관 자체가 불필요** → 해시만
저장. **최종: 런타임 중 발급물.**

### 100.6 이번 작업에서 임의로 내린 결정 전부 (감독이 알아야 할 것)

1. **저장소로 JetStream KV를 택한 것.** 호스트 파일·새 DB와 삼자
   비교했고 A2(다중 레플리카)를 결정 근거로 삼았다. 대가는 정직하게
   적었다 — NATS가 죽으면 새 로그인 불가, 백업이 Neo4j와 짝이 되어야
   함(60 §2.7). 이 대가를 받아들일지가 감독이 검토할 첫 항목이다.
2. **NATS 순단 대비 ≤60초 양성 캐시.** 그 대가로 **강제 로그아웃이
   최대 60초 지연된다** — 침해 대응의 즉시성이 사람 계정에 대해서만
   60초 늦어진다. 캐시를 없애면 NATS 순단에 전원 로그아웃된다. 이
   트레이드오프를 60 §7.4에 명시했다.
3. **버킷을 TTL 등급별로 4개로 나눈 것.** NATS의 키 단위 TTL 가용성을
   확인하지 않았고(`[미검증]`으로 표시), 그것에 의존하지 않는 설계를
   택했다. 버킷 단위 `max_age`만 쓰므로 모든 NATS 2.x에서 동작한다.
4. **세션 TTL 12시간과 쿠키 이름을 그대로 둔 것.** §8.4의 기존 값과
   §93-2의 기존 이름을 바꾸지 않았다 — 바꿀 근거가 없었다.
5. **"조직 관리자"를 권한 코드 `org.admin` 하나로 표현하기로 한 것
   (OD2).** 온톨로지에 "대표"가 1급 개념으로 없다(`:Membership`이
   `:Team`만 가리켜 조직 직속 멤버십을 표현할 수 없고 I1은 팀 단위
   `isHead`만 규정). 세 안 중 (a)관계 도메인 확장은 파급이 크고,
   (b)`:OrgRevision{n:1}.byPartyId`는 이양 불가라, **기존 인가 모델을
   그대로 쓰는** (c)를 권고했다. **`20-ontology-schema.md`는 수정하지
   않았다** — 제안일 뿐이며, 채택되지 않으면 구현은 (b) 폴백으로
   동작한다.
6. **비밀번호 정책 상수**(최소 12자, 구성 규칙 없음, 만료 없음)와
   백오프 상한(30초), L2/L3 레이트리밋 값(10회/분, 초당 10회)을 임의로
   제시했다. 전부 코드 상수이며 `.env`에 올리지 않는다.
7. **break-glass를 HTTP가 아니라 컨테이너 내 CLI 서브커맨드로 한 것.**
   호스트 파일 + mtime 감시(어댑터 `watchCreds` 패턴) 안도 검토했으나
   새 감시 루프와 비동기 구간을 만들지 않는 쪽을 택했다. 결과적으로
   **새 HTTP 라우트가 0개**이고 상환은 기존 등록 티켓 경로를 재사용한다.
8. **`GET /me`·`consoleSurfaces`라는 계약 이름.** 설계가 정한 바 없어
   임의로 정했다.
9. **`40-console-ia.md`의 "전원 동일 사이드바" 전제를 수정한 것.**
   감독이 그 문서의 직접 수정을 허용했고, 사람 직원이 실제로 로그인하는
   이상 일반 직원에게 Approvals/Ops/Admin Chat/Onboarding이 보이는 것은
   부적절하다고 판단했다. 평면 IA·10명 미만 전제는 그대로 뒀고, 분기
   규칙을 프론트가 아니라 백엔드가 계산하게 해 G10과 같은 형태로 만들었다.

### 100.7 `[미검증]`으로 표시한 추정 전부

1. **NATS의 키 단위 TTL(per-message TTL) 가용성** — 확인하지 않았고,
   설계가 그것에 의존하지 않도록 버킷 단위 TTL만 쓴다(위 6-3).
2. **Argon2id 의존성** — Go 표준 라이브러리에 없어
   `golang.org/x/crypto/argon2`가 필요한데, 이 저장소의 의존성 정책상
   허용되는지 확인하지 않았다. 불가하면 scrypt 또는 PBKDF2로 대체
   가능하며 **다른 결정은 하나도 바뀌지 않는다**(H3만 지키면 된다).
3. **HI3/HI10의 정규식 1차 구현에 false negative 가능성** — 31 §5.5가
   같은 종류의 한계를 정직하게 남긴 선례를 따랐다(AST 기반이 더 안전).

### 100.8 상한에 걸려 멈춘 것

**설계를 바꾸지 않고 보고만 한 항목이 8건이다**(60 §17의 F1~F8). 그중
감독의 판단이 실제로 필요한 것:

- **F1 — `10-core-architecture.md` §8.4 "사람 콘솔 세션" 행이 `Secure`를
  무조건으로 적고 있다.** 실측(§87)과 기본 설치(루프백 평문 HTTP,
  §2.0)에서는 붙일 수 없고, 감독이 이 사실을 이미 확정했다. **그런데
  고치지 않았다** — 그 표는 이 문서 소유가 아니다. §98 항목 4에서
  앞선 워커가 "서술 오류 정정이지 결정 변경이 아니다"라는 자기 판단으로
  W4를 직접 고쳤고 그 판단이 §99에서 되돌려졌다. **그 전례를 따르지
  않는다.** 권고: 그 행에 "(`Secure`는 요청별 동적 — `60-identity-authn.md`
  H7)" 각주 추가.
- **F2 — §7.2 `TASKS` 스트림 서브젝트 목록에 `security.*`가 없는데
  §8.5는 `security.genesis.used`를 TASKS에 발행한다고 서술한다.** 기존
  내부 불일치이며, 이 설계의 `security.authn.*`/`security.breakglass.*`/
  `security.enrollment.*`도 같은 자리를 필요로 한다. 고치지 않았다.
- **F4/F5 — 온톨로지 소유자 판단**: "대표"의 표현 공백(위 6-5)과
  불변식 번호 충돌(`30-onboarding-provisioning.md` §6.3이 이미 "I13"을
  쓰고 있어 OD3/OD4에 줄 번호가 불명확하다).
- **F7 — 신규 발견 `[갭]`, 구현 전에 답이 필요하다**: 현행
  `authn.Guard.Require`는 세션 쿠키 **또는** Bearer 설정 토큰을 받는데,
  **H10이 발동하면 설정 토큰이 죽으므로 §81/§34/§59의 curl 기반 완료조건이
  전부 401이 된다.** 이 설계는 사람 주체만 다루고 비대화형 기계 주체를
  만들지 않았다. 선택지: (a) 완료조건을 사람 계정 로그인으로 전환,
  (b) `:Person`이 아닌 별도 서비스 주체를 설계(현재 설계에 없음).
  **60 §18의 구현 순서 5단계 전에 결정되어야 한다.**

**그 외에 G1~G10·D1~D20·A1~A13·W1~W5·I1~I12 중 수정·완화·번복한 것은
없다.**

### 100.9 해소된 기존 갭

- **§76 `[갭]`("조직 생성 이후 콘솔 인증 메커니즘이 설계에 없음")** —
  `60-identity-authn.md`가 메웠다(설계만. 구현은 다음 워커).
- **§94 세 항목** — (a) 다중 운영자 구분 없음 → H4(세션의 `partyId`)
  + §6(귀속), (b) 인메모리 세션 → H2, (c) 브루트포스 가드 없음 →
  H15/H16/§8.
- **§96의 재검토 조건 (1)** — 충족. 단회용 전환 판정은 100.3.
- **§83/§85~§93의 브라우저 인증 빌드**는 그대로 살아 있다 — 이 설계는
  그것을 폐기하지 않고 **저장소를 옮기고 그 위에 사람 신원을 얹는다.**
  §86의 Q2b 판정, §87의 `Secure` 실측, §89의 "토큰을 브라우저에
  저장하지 않는다" 실측은 전부 이 설계의 전제로 계승했다.

---

# 설계 노트 — 신원·인증 2차 확장 (master key · 검증 전용 기계 주체 · RBAC)

## 101. 사용자 지시 3건을 하나의 신원 모델로 통합 (설계 문서만, 코드 변경 0줄)

**지시 원문**: *"ⓐ로 가자, 검증 전용 기계 주체 추가 설계해. 그리고 최초
토큰값은 master_key개념이고, 로그인하여 암호를 변경할 수 있도록 하고싶어.
또한 RBAC를 구현해서 추가 매니저 계정을 발급/수정 할 수 있게하고싶어."*

**범위**: `60-identity-authn.md`(확장) · `20-ontology-schema.md`(수정 권한
명시적 부여) · `40-console-ia.md`(수정 권한 명시적 부여) · 이 노트(append).
`api/`·`core/`·`console/`·`orchestrator/`·`gw/`·`docker-compose.yml`은
**읽기만** 했다(특히 `api/internal/{authn,adminauth,session,api}`,
`console/src/components/TokenEntryScreen.tsx`). `<host-infra>/`는
열람도 쓰기도 하지 않았다. 자격증명 값 출력·기록 0.

### 101.1 산출물

- **`60-identity-authn.md`**: 결정 **H22~H32**, 불변조건 **HI13~HI20**,
  기계 주체 폐쇄조건 **MP1~MP6**, 위임 규칙 **DG1~DG5**, 발견 **F9~F12**,
  신설 절 **§19**(검증 전용 기계 주체) · **§20**(조직 역할 모델) ·
  **§21**(권한 상승 분석), 개정 절 §0.1·§3.1.1·§3.3·§3.4·§4.0~§4.6·
  §5.3(판정 ③④⑤)·§5.6·§11·§12·§13·§14·§15.2·§16.1·§17·§18.
- **`20-ontology-schema.md`**: 관계 `IN_ORG`, 속성 `:Role.scope`, 권한 코드
  5개, 프로파일 3종, 조직 역할 3종, 불변식 **I14~I17**, 제약 **C9~C10**,
  `person_email` 유일성 제약, §8.3 후보 집합에 `IN_TEAM` 제한, §7.1 적용
  범위 주석 재작성. **전수 목록은 60 §14.3.**
- **`40-console-ia.md`**: 화면 **#10 People & Roles** 신설, #5 Ops에
  **5-A 기계 주체** 섹션 신설, 화면 #0 문구를 master key로 교체, 사이드바
  블록을 권한 코드 표기로 갱신, §F REST **13행 추가**, 비-API 계약 2항 추가.

### 101.2 세 지시를 왜 하나로 묶었는가

셋은 서로의 전제다. **master key**(ⓑ)가 조직 전 구간의 유일한 인가 주체이고,
그러므로 **그 구간의 기계 주체**(ⓐ)를 발급하는 것도 그것뿐이며, 조직이 서면
**조직 역할**(ⓒ)이 그 자리를 이어받는다. 따로 설계했다면 (i) 기계 주체의
스코프 어휘가 RBAC와 별개 체계가 되고, (ii) master key의 종료 시점과
bootstrap 기계 주체의 종료 시점이 어긋나 창이 남고, (iii) "조직 전에는 누가
발급하는가"가 미해결로 남았을 것이다.

### 101.3 F7의 답 — 그리고 F7 서술 자체의 정정

사용자가 선택지 **ⓐ(별도 기계 주체 설계)**를 택했고 60 §19가 그것을 설계했다.

**동시에 F7의 서술을 정정한다.** §88과 §100.8은 *"§81/§34/§59의 curl 기반
완료조건이 전부 401이 된다"*고 적었는데, 이번에 세 절을 전수 확인한 결과
**그 서술은 과장이었다**:

- **§34**(I4 에이전트 런타임 완료조건 #1~#11): curl 0건, 설정 토큰 0건.
  `Bearer` 문자열 1회 등장은 **목 게이트웨이가 관찰한 에이전트의 게이트웨이
  토큰**(`ANTHROPIC_AUTH_TOKEN`)이지 관리 설정 토큰이 아니다. #10의
  `/v1/agents/close`는 core의 **정적 graph-write 토큰**(§9)으로 인증한다.
- **§59**(I5 omniroute 2차 완료조건): 인증이 필요한 호출 0건.
  `127.0.0.1:18280/`·`/pre-status`·`/omni/`는 전부 무인증이고 나머지는
  SQLite 직접 조회와 grep이다.
- **§81**: 여기만 실제로 의존한다 — **#2**(`/_api/onboarding/status` 두 경로
  200), **#4**(`step0.ready` 전/후 전이), **#5**(SSE 재개), **#9**(재기동 후
  `state/creds/admin/setup_token` 존속).
- 그리고 진짜 큰 것은 **§84의 인계 계약** — *"지금은 전부
  `Authorization: Bearer <관리 설정 토큰>`이 필요하다"* + 9개 엔드포인트 표.

**append-only 관례를 지켜 §88·§100.8을 고치지 않았다.** 구현 워커는 그 두
절이 아니라 이 절과 60 §19.1/§19.10을 따를 것. 대체 경로는 60 §19.10의 표에
항목별로 있으며, **#9는 검증 항목의 의미가 바뀐다** — H23 이후 이 파일은
조직 수립과 함께 사라지는 것이 **정상**이므로 "존속하는가"가 아니라
"조직 전에는 존속하고 비밀번호 설정 후에는 사라지는가"를 검증해야 한다.

### 101.4 조직 생성 이전 구간의 답 — H10의 연접 구조에서 나왔다

F7의 핵심 난점("조직도 `:Person`도 RBAC도 없는 시점에 무엇이 기계 주체를
발급하는가")은 **H10의 폐쇄 조건이 연접(AND)이라는 구조 자체**가 답을 준다:

```
master key 가 유효한 구간
  = ¬( currentOrgRevision ≥ 1  ∧  org.admin 보유자 중 자격증명 보유자 ≥ 1 )
  = 조직이 없다  ∨  관리자가 아직 자기를 증명할 수단을 갖지 못했다
```

그 구간에서 "인가된 사람"으로 정의 가능한 집합은 **호스트의 0400 파일을 읽을
수 있는 사람** 하나뿐이고, 그것은 §9.1이 이미 "전권자"로 규정한 집합과 같다.
따라서 **master key 세션이 발급**하며, 이는 새 특권을 만드는 것이 아니라
이미 가진 능력의 스코프 제한된 표현이다(§9.3이 break-glass CLI에 대해 편
논증과 같은 형태).

그리고 그렇게 발급된 `bootstrap` 클래스 주체는 **H10 조건의 부정을 유효
술어로 삼으므로**, master key가 죽는 바로 그 순간 **별도 정리 코드 없이**
함께 죽는다(MP1). 조직 전에 만든 것이 조직 후로 이월되지 않는다.

**설치 시점 굽기가 아니라는 점을 린트로 고정했다**(HI14): `osiris-up`·
compose·`bin/**`·부팅 경로에서 기계 주체를 **생성**하는 코드가 있으면 실패.
기본 상태는 "기계 주체 0개"이고, 검증을 돌리려는 사람이 그때 하나 만든다.

### 101.5 master key 생존 여부 — (가) 채택, 근거와 뒤집기 비용을 함께 남김

**(가) 비밀번호 설정과 함께 죽는다**를 채택했다(H23). 근거 셋:
(1) (나)는 만료도 회전도 귀속도 없는 상시 루트 자격증명 = 정의상 백도어이며,
genesis(W1~W5)와 기계 주체(MP1~MP6)에 요구한 논증을 이것만 면제할 근거가
없다. (2) 복구 경로는 **H17 break-glass가 이미 소유**하고 (나)보다 엄격하게
낫다(새 HTTP 표면 0개, 억제 불가능한 감사, 귀속 유지). (3) (나)는 감사되는
복구 경로와 안 되는 경로를 공존시켜 실무를 감사되지 않는 쪽으로 흐르게 한다.

**뒤집을 수 있게 남긴 것**: 60 §4.6에 (나) 채택 시 함께 결정해야 할 4개
항목을 체크리스트로 적었다 — 그중 하나(**§6.5 "actorPartyId가 유효한
`:Party`가 아니면 core는 mutating 거부"를 뒤집기**)는 **이 작업의 상한에
해당한다.** 즉 (나)는 결정 하나가 아니라 §6.5·§21·§9·40 IA를 연쇄로 뒤집는
변경이며, 그 사실 자체가 (가)를 택한 네 번째 근거이기도 하다.

### 101.6 F4 정면 해결 — `IN_ORG`, 그리고 초판 OD2를 넘어선 이유

초판은 `org.admin` 권한 코드 1개를 `HAS_PROFILE{kind:'additive'}`로 부여하는
안(OD2)을 제안했다. **이번에 그 안의 치명적 결함을 발견했다**: `HAS_PROFILE`
엣지에는 `validFrom`/`validTo`/`createdAtRevision`/`closedAtRevision`이
**없다.** 회수 = 엣지 삭제이므로 원칙 P4를 어기고, **Q8("리비전 N 기준 조직
재현")이 과거의 권한 배치를 재현하지 못한다.** 감사가 1급 요구인 시스템에서
권한 이력이 증발하는 표현은 부적절하다.

**채택안**: `(:Person)-[:HAS_MEMBERSHIP]->(:Membership)-[:IN_ORG]->(:Organization)`
— **새 관계 타입 1개**로 조직 직속 멤버십을 표현한다. `IN_TEAM`을 넓히지
않았으므로 **I1·C4가 문자 그대로 유지**되고, `AS_ROLE`→`HAS_PROFILE{base}`→
`GRANTS` 체인과 §7.1의 `effective(party)` 수식이 **한 줄도 바뀌지 않는다**
(그 정의가 애초에 "멤버십의 `:Role`"이라고만 쓰여 있고 팀을 요구하지 않았기
때문). 새 노드 라벨 0개, 새 인가 경로 0개.

**함께 잠근 세 가지**(빼먹으면 조용히 다른 것이 망가진다):
- §8.3 `candidateSet()`에 `-[:IN_TEAM]->(:Team)` 제한 — **오늘의 결과를
  한 건도 바꾸지 않으면서**(현재 모든 멤버십이 `IN_TEAM`) 조직 역할만 가진
  사람이 배정 후보로 새어 들어가는 것을 막는다.
- I15/C9: `IN_ORG`는 `:Person`만 → I2 무영향, 부수적으로 **AI 에이전트는
  조직 관리자가 될 수 없다**(명시적 결정).
- I17: `:Role{scope:'org'}`는 역량을 요구하지 않는다 → I4·I6·I11 무영향.

**F5도 함께 해소**: 전 문서 grep으로 `I13`이 `30-onboarding-provisioning.md`
§6.3에 선점되어 있고 `I14`/`I15`는 60 §17 F5의 *권고 문구 안에서만* 언급될
뿐 배정된 적 없음을 확인한 뒤, 신규를 **I14~I17**로 배정하고 20 §9.3 표에
**I13 선점 사실을 한 줄로 명기**했다.

### 101.7 권한 상승 분석 — 네 질문의 답

위임 규칙 **DG1**(부여 상한 `grants(R) ⊆ eff(A)`) · **DG1′**(`org.admin`
포함 프로파일은 `org.admin` 보유자만) · **DG2**(대상 상한 `eff(T) ⊆ eff(A)`)
· **DG3**(자기 대상 금지) · **DG4**(최소 1 관리자) · **DG5**(억제 불가능한
가시성)를 **인가 판정 단일 함수**에 둔다(H31, HI16이 검사).

| 질문 | 답 | 근거 |
|---|---|---|
| 매니저가 관리자를 만들 수 있는가 | **아니오** | DG1 + DG1′(독립 이중 검사) |
| 매니저가 자기 역할을 올릴 수 있는가 | **아니오(두 겹)** | DG3가 요청 자체를 거부, DG1이 코드 부여를 거부 |
| 매니저가 관리자를 강등/비활성화할 수 있는가 | **아니오** | DG2 — 이것이 가장 놓치기 쉬운 규칙 |
| 마지막 관리자가 자기를 강등/삭제할 수 있는가 | **아니오(세 겹)** | DG3 → DG4(그래프 I14) → DG4(API 계층: 로그인 가능한 관리자 ≥1) |

**DG2의 적용 범위를 넓힌 것이 이번 분석의 실질 산출물이다.** 31 §1.3의
7문항을 매니저 자격증명에 실제로 돌리다(판정 ⑤) 발견했다 — **등록 티켓
발급은 "그 계정에 새 비밀번호를 세울 수 있다" = 계정 탈취 프리미티브**다.
티켓 발급을 "편의 기능"으로 두면 매니저가 관리자에게 티켓을 발급해 계정을
인수할 수 있다. 그래서 DG2의 적용 대상을 **역할 변경 + 계정 상태 +
자격증명 조작(티켓 발급/재발급, 세션 강제 폐기) + `DENIES`/`HAS_PROFILE`
조작**으로 명문화했다(C10). 마지막 항목을 빼먹으면 DG1~DG4를 전부 우회하는
구멍이 하나 남는다(`DENIES org.admin`으로 관리자 무력화).

**동급 간 상호 조작은 허용한다(`⊆`이지 `⊊`가 아니다)** — 엄격 포함으로 하면
**침해된 관리자를 제거할 방법이 사라진다**(DG3가 자기 강등도 막으므로 그
계정이 영원히 남는다). 대신 DG5의 전원 알림 + `:Membership` 이력으로
감시한다. 관리자 2명 중 1명이 다른 1명을 강등할 수 있다는 사실은 **완화하지
않고 정직하게 인정한다**(60 §21.2) — 여기에 승인 게이트를 붙이면 마지막
관리자가 잠기는 새 데드락이 생긴다.

### 101.8 역할 = 열거형 vs 인스턴스 — 판단과 그 대가의 회수

**인스턴스(`:Role{scope:'org'}`)를 택했다**(H30). 열거형은 단순하지만
(i) 조직마다 다른 역할 구성을 표현할 수 없어 **"어디서든 쓰는 프레임워크"가
깨지고**, (ii) `:Permission` 카탈로그 옆에 **두 번째 인가 어휘**(Go enum)가
서게 되며 — 이 과제가 명시적으로 금지한 것 — (iii) enum 값은 그래프에 없어
Q6·Q8이 못 본다.

**인스턴스의 함정을 규칙으로 막았다**: 이름은 바뀔 수 있으므로 **이름 기반
판정이 곧 보안 결함**이다. → **인가 판정에 역할·프로파일 id·title 리터럴을
쓰지 않고 `:Permission.code`만 쓴다**(HI17이 검사). 그 결과 "매니저는 관리자를
만들 수 없다"가 역할 이름이 아니라 **코드 집합의 포함관계에서 자동으로**
따라 나온다 — 이것이 인스턴스를 택한 대가를 회수하는 지점이다.

### 101.9 31 §1.3 7문항 — 실제로 돌린 결과 (판정 ③④⑤)

전문은 60 §5.3. 각 Q2b의 답만:

- **판정 ③ master key**: 교환 경로 **둘**(브라우저→세션 id, 스크립트→기계
  주체 토큰). 원본은 반복 사용되지 않고 로그인 1회에 소비 후 즉시 회전(H11).
  **★ 그럼에도 원본이 호스트 0400 파일에 평문으로 있다는 사실을 침묵하지
  않았다** — Q2b가 요구하는 "저장한다면 왜인지 한 줄"을 남겼다: 그 파일은
  **저장소가 아니라 전달 채널**이고(다른 전달 수단이 존재하지 않는다),
  H23에 의해 **하드 종료 시점이 정해져 있으며**, `.env`·그래프·로그·응답
  바디 어디에도 복제되지 않는다. genesis 토큰이 31 §2.2에서 "세 번째 범주"로
  정당하다고 판정된 것과 같은 근거·같은 형태.
- **판정 ④ 기계 주체 토큰**: **이 값은 Q2b의 *답*이지 *대상*이다.** 이것을
  만든 동기 자체가 Q2b다 — 없으면 검증 스크립트가 master key(사람의 로그인
  자격증명)를 상시 보유해야 하고 그것이 정확히 사건 4의 형태다. 애초부터
  스코프 제한된 단일 목적 기계 자격증명이며 우리가 검증만 하면 되므로
  **해시만 저장**.
- **판정 ⑤ 매니저 계정 자격증명**: 판정 ①②와 동일하게 통과 —
  **분류는 권한에 의해 바뀌지 않는다.** 그러나 이 자리에서 위 101.7의
  계정 탈취 경로를 발견했다. **결론이 같아 보여도 물어봐야 한다는 것이
  Q2b가 사건 4에서 태어난 이유** 그 자체다.

### 101.10 임의로 내린 결정 전부 (감독이 알아야 할 것)

1. **파일·CLI 재명명** — `state/creds/admin/setup_token` →
   `master_key`, `bin/show-setup-token.sh` → `bin/show-master-key.sh`,
   요청 키 `setupToken` → `masterKey`. 마이그레이션 규칙도 정했다(부팅 시
   `currentOrgRevision == 0`이면 rename, 조직이 있으면 삭제). **이름을 그대로
   두고 개념만 재정의하는 선택지도 있었다** — 재명명을 택한 이유는 §95가
   만든 콘솔 문구·`osiris-up` 출력·재조회 스크립트가 전부 "설정 토큰"을
   말하고 있어, 이름이 남으면 개념 재정의가 사람에게 전달되지 않기 때문.
2. **토큰 접두사** `osk_`(master key) / `osm_`(machine principal). 가드가
   시행착오 비교 없이 경로를 고르고, 시크릿 스캐너가 패턴으로 잡고, 로그에
   새어도 무엇이 샜는지 즉시 식별된다.
3. **기계 주체 TTL** bootstrap 24h(W2와 의도적으로 같은 값) / org 기본 30일
   ·최대 90일. **버킷 `max_age = 90d`를 레코드 `expiresAt`과 독립적인 상한**
   으로 둔 것(코드 버그에 대한 심층 방어)도 임의 결정이다.
4. **주체별 레이트리밋 60 req/분** — 목적의 절반은 보안(대량 수집 지연),
   절반은 **잘못 짠 CI 루프 방어**(현실에서 더 자주 일어난다).
5. **권한 코드 5개의 이름과 분할** — 특히 `org.manage.people`과
   `org.manage.roles`를 **나눈 것**. "사람은 추가하되 역할은 못 주는" 역할을
   표현할 수 있게 하려는 것이며, 합쳐도 동작은 한다.
6. **`org.read.*` 접두를 기계 주체 스코프의 허용목록 기준으로 삼은 것** —
   접두 규약에 보안을 거는 것이므로, 앞으로 `org.read.`로 시작하는 쓰기
   코드를 만들면 안 된다. 이 제약을 명명 규약으로 못박았다.
7. **`:Role.scope` 값 이름** `job`/`org`. 미명시 기존 역할은 `job`으로 읽는다
   (마이그레이션 0건).
8. **한 사람에 조직 역할 1개 제한(I16)** — 2개를 허용하면 `eff`의 합집합이
   조용히 예상 밖 권한을 만든다. 예외가 필요하면 기존 additive 프로파일
   경로가 있고, DG는 **부여 수단과 무관하게 코드 기준**이라 우회로가 되지
   않는다.
9. **동급 상호 조작 허용(`⊆`)** — 101.7 참조. 반대 선택(`⊊`)의 결과도 함께
   기록했다.
10. **비밀번호 변경 시 현재 비밀번호 재확인 요구(H24 C)** — 요구하지 않는
    설계도 검토했다. 기각 이유: 재확인이 없으면 **세션 탈취 1회가 계정 영구
    탈취로 승격**되고, "다른 세션 전원 폐기"라는 방어 기능이 정당한 소유자를
    쫓아내는 공격 도구가 된다(H15가 계정 잠금에서 발견한 것과 같은 역전).
11. **`mustChange` 필드를 사실상 폐기** — 이 설계에는 "관리자가 남의 임시
    비밀번호를 설정하는" 경로가 아예 없으므로 쓸 자리가 없다. 60 §5.4의
    "break-glass 상환 직후에만 세운다"를 §5.6이 대체한다.
12. **화면 #10을 신설한 것**(기존 화면에 얹지 않고). 근거는 60 §16.1-1.
13. **기계 주체를 Ops의 섹션으로 둔 것**(별도 화면 아님) — 조직 구성원이
    아니라 운영 자산이므로.
14. **`GET /whoami` 응답 스키마에 `partyId` 필드를 아예 두지 않은 것** —
    두면 언젠가 누군가 채운다.
15. **OD4를 이번에도 미반영**(F10) — 필요하지 않았고, §6.5가 런타임에 같은
    것을 강제하므로 지금 구멍이 열리지 않는다.

### 101.11 `[미검증]`으로 표시한 것

초판(§100.7)의 세 항목은 그대로 유효하다(NATS per-message TTL / Argon2id
의존성 정책 / HI3·HI10 정규식의 false negative). 이번 확장이 추가하는 것:

4. **`AUTHN_MACHINE` 버킷의 `max_age = 90d`가 이 NATS 이미지 태그에서
   문제없이 동작하는지 확인하지 않았다.** 기존 버킷들(최대 7일)보다 훨씬 긴
   보존이며, JetStream 파일 저장소의 디스크 사용에 미치는 영향도 실측하지
   않았다. 레코드 수가 한 자리이므로 실질 영향은 없을 것으로 보이나
   **추정이다.**
5. **`personOnly` 미들웨어를 라우터 등록 시점에 정적 검사(HI15)하는 것이
   현행 `net/http` `mux.HandleFunc` 등록 패턴에서 실제로 grep 가능한지**
   확인하지 않았다. 불가하면 HI12와 같은 "⚠️ 부분" 등급이 된다.
6. **`allowedActions`를 행마다 계산하는 비용**(사람 N명 × 대상 상한 규칙
   = `eff(T)` N회 계산)을 실측하지 않았다. 10명 미만 전제에서는 무시할 만
   하지만, §3.2의 ≤10초 권한 캐시를 이 경로에도 태워야 할 수 있다.

### 101.12 상한에 걸려 멈춘 것

**없다 — 그러나 두 번 멈출 뻔했고, 두 번 다 우회하지 않았다.**

1. **C1(`:Party`는 4개 하위 라벨 중 하나)** — 기계 주체를 다섯 번째 하위
   라벨로 만드는 안이 가장 자연스러워 보였으나 **기존 제약의 변경**이므로
   상한이다. 자기 면제하지 않고 **기계 주체를 그래프 밖에 두는 설계로
   피했고**(60 §19.2), 결과적으로 더 나은 성질(조직도 오염 0)을 얻었다.
2. **`20-ontology-schema.md` §7.1의 적용 범위 주석** — *"이 스키마가 표현하지
   못하는 주체는 정상 운영 중에는 존재하지 않는다"*가 §19 도입으로 문자
   그대로 거짓이 된다. I/C 번호가 아니어서 형식상 상한이 아니고 온톨로지
   수정 권한도 받았지만, **§98→§99의 전례(자기 판단으로 W4를 고쳤다가
   되돌려진 것)를 의식해 조용히 넘기지 않았다.** 문장이 지키려던 보안 내용
   (*"표현되지 않은 주체가 조직을 바꿀 수 없다"*)을 **보존·강화하는 형태로
   재작성**하고, 60 §14.4에 (a)/(b)/(c) 선택지와 근거를 전용 절로 남겨
   **감독이 그 지점만 보고 되돌릴 수 있게** 했다. 이 확장에서 판단이 가장
   무거운 한 줄이다.

**그 외 G1~G10 · D1~D20 · A1~A13 · W1~W5 · I1~I12 · C1~C8 · H1 중
수정·완화·번복한 것은 없다.** 전수 대조는 60 §14.5(I/C)와 §19.9(W1~W5),
§17 "상한 재확인" 표에 있다.

### 101.13 감독 판단이 필요한 것 (60 §17의 F9~F12)

- **F9** — `10-core-architecture.md` §8.1 grant-catalog 표에 **새 권한 코드
  5행 추가 + `catalogVersion` 동반 상승**이 필요하다(I9). 4층 투영은 전부
  no-op이며 `exec.physical` 행이 이미 그 형태다. **10은 다른 워커 소유라
  고치지 않았다.**
- **F10** — OD4(`n>=1`인 `:OrgRevision`은 `byPartyId IS NOT NULL`) 미반영.
- **F11** — `IMPLEMENTATION-NOTES.md` §84 인계 계약이 **세 갈래**(사람 세션 /
  master key(조직 전) / 기계 주체(읽기만))로 다시 그려져야 한다. append-only
  이므로 이 절이 그 갱신본이다.
- **F12** — `30-onboarding-provisioning.md` §1.0.6과
  `api/internal/adminauth`의 doc comment는 둘 다 **"step0은 조직 생성 이후에도
  호출 가능해야 한다"**(키 회전·모델 재매핑)를 명시하는데, H10이 master key를
  죽이면 **부를 주체가 사라진 것처럼 보인다.** 실제로는 `org.admin`이
  로그인해서 부르면 되고 기계 주체는 읽기 전용이라 부를 수 없다 — 그 사실이
  30 §1.0 어디에도 없다. **30은 다른 워커 소유라 고치지 않았다.**
- **F1~F3, F6, F8**(초판 발견)은 그대로 열려 있다.

---

## 102. §84 인계 계약의 인증 절 — 갱신본 (F11 해소, 감독)

이 문서는 append-only이므로 **§84를 고치지 않는다.** 대신 이 절이 §84의
**"인증" 문단만** 대체한다 — 엔드포인트 표(9개 경로·요청/응답 형상)는
§84의 것이 여전히 유효하고, 이 절은 그 표에 **어떤 자격증명으로 접근하는가**
만 다시 그린다. §84를 읽는 사람은 반드시 이 절까지 읽어야 한다.

**§84가 낡은 이유 세 가지**:
1. *"지금은 전부 `Authorization: Bearer <관리 설정 토큰>`이 필요하다"* — 그 뒤
   세션 쿠키 교환이 구현되어(§87 이하) **두 경로가 병존**하게 됐다.
2. *"토큰은 `state/creds/admin/setup_token`에 있다"* — `60-identity-authn.md`가
   이 값을 **master key**로 재정의하고 파일명·접두사·수명을 바꿨다.
3. *"조직 생성 이후 ... 는 설계 공백(§76) — 콘솔 워커가 감독과 먼저 정할 것"*
   — **§76은 닫혔다.** `60-identity-authn.md`가 그 설계다.

### 102.1 오늘 실제로 도는 것 (실측, 감독)

| 자격증명 | `/_api/onboarding/status` | 비고 |
|---|---|---|
| `Authorization: Bearer <설정 토큰>` | **200** | §84가 적은 그대로. 아직 살아 있다 |
| 세션 쿠키 `osiris_console_session` | **200** | §87 이후 추가된 경로. 브라우저·SSE는 이쪽만 가능 |
| 무인증 | **401** | |

즉 **§84는 틀린 게 아니라 불완전**하다 — 그 시점엔 쿠키 경로가 없었다.

### 102.2 설계가 확정한 최종 형태 — 세 갈래

`60-identity-authn.md` 채택 후에는 아래 셋으로 갈라지며, **위 표의 Bearer
설정 토큰 경로는 사라진다**(master key는 세션 교환에만 쓰이고 API 직접 호출에
쓰이지 않는다).

| # | 주체 | 자격증명 | 쓸 수 있는 것 | 수명 |
|---|---|---|---|---|
| (a) | **부트스트랩 조작자** | **master key**(`osk_` 접두, 호스트 0400 파일) | `POST /api/auth/session` **교환 1회만**. API 직접 호출 불가 | 비밀번호 설정과 동시에 **영구 폐기**(H23). 유효 술어 = `¬(조직 있음 ∧ org.admin 보유자 중 자격증명 보유자 ≥1)` |
| (b) | **사람** | 세션 쿠키 `osiris_console_session` (`HttpOnly; SameSite=Strict`, `Secure`는 요청별 동적) | **모든 변경 엔드포인트는 이 경로뿐** — 라우터가 non-GET에 `personOnly`를 강제(HI15) | 12h |
| (c) | **검증·CI 기계 주체** | `Authorization: Bearer osm_<32B>` | **`org.read.*` 접두 권한만.** 그래프 변경·승인·사람 관리·자격증명 조작·자기 재발급 전부 불가 | `bootstrap` 클래스 24h / `org` 클래스 ≤90일 |

**두 자격증명을 한 요청에 섞으면 400.** (b)와 (c)는 동시 제시 금지.

### 102.3 §84 표의 9개 엔드포인트가 어느 갈래에 속하는가

- `GET /api/onboarding/status`, `GET /api/onboarding/step0/status`,
  `GET /api/onboarding/step0/alias-defaults` — **(b) 또는 (c)**. 읽기이므로
  기계 주체로 검증 가능하다(`org.read.onboarding`).
- 나머지 6개(`POST` 전부: `omniroute-admin`, `providers`, `aliases`,
  `gateway-token`, `verify-gateway`) — **(b) 전용.** 쓰기이므로 기계 주체는
  구조적으로 부를 수 없다. 조직 생성 **전**에는 master key로 연 세션이,
  **후**에는 `org.admin` 세션이 부른다(`30-onboarding-provisioning.md`
  §1.0.6의 F12 해소 문단).
- `GET /api/events`(SSE) — **(b) 전용.** 브라우저 `EventSource`가 커스텀
  헤더를 붙이지 못하므로 쿠키 경로 외에는 불가능하다(§87에서 확인된 사실이며,
  세션 쿠키 방식을 택한 기능적 이유이기도 하다).

### 102.4 구현 순서상의 주의

(c)가 구현되기 **전에** H10(master key 단회용화)을 먼저 켜면, §81의 curl
기반 완료조건 4건이 갈 곳을 잃는다(§101 F7 해소 문단의 항목별 대체 경로 표
참고). **(c) → H10 순서를 지킬 것.** 반대로 켜면 검증 수단이 없는 구간이
생긴다.

---

## 103. 검증 전용 기계 주체 (c) 구현 — `60-identity-authn.md` §19

`IMPLEMENTATION-NOTES.md` §102.2의 **갈래 (c)** 를 코드로 옮긴 작업.
설계 문서는 **한 줄도 고치지 않았다**(이 절만 추가). H10(master key
단회용화)·RBAC(§20/§21)·비밀번호·등록 티켓·break-glass는 전부 손대지
않았고, **기존 두 인증 경로(설정 토큰 Bearer / 세션 쿠키)는 그대로
살아 있다** — §102.4의 `(c) → H10` 순서를 지킨 것이다.

### 103.1 산출물

| 파일 | 성격 |
|---|---|
| `api/internal/authz/authz.go` | **§20.4의 권한 코드 5개 + 단일 `Authorize()`**. 역할·프로파일·위임 규칙은 **없다**(§20의 몫) |
| `api/internal/authz/context.go` | 요청 컨텍스트의 `Principal` 배선. 유일한 writer는 가드 |
| `api/internal/orgstate/orgstate.go` | **H10 술어 한 개**. `bootstrap` 클래스 유효 술어가 여기서 나온다 |
| `api/internal/machineauth/store.go` | `AUTHN_MACHINE` KV, 토큰 발행·검증·리보크, 양성 캐시, 레이트리밋 |
| `api/internal/machineauth/handlers.go` | `POST`/`GET`/`DELETE /api/machine-principals` |
| `api/internal/secevents/secevents.go` | `security.machine.*` 감사 발행 |
| `api/internal/authn/authn.go` | **재작성** — §19.3의 세 갈래 분리, `PersonOnly`/`PersonOnlyPublic`/`RequirePerm`, 혼재 400 |
| `api/internal/api/server.go` | **재작성** — Go 1.22 메서드 패턴 라우트 표(HI15가 판정할 대상) |
| `api/internal/graphread/client.go` | `CurrentOrgRevision()` 추가 |
| `api/internal/events/sse.go` | `JetStream()` 접근자 추가(프로세스당 JS 컨텍스트 1개) |
| `api/cmd/osiris-api/main.go` | 배선. **발급 코드 0줄**(HI14 ①) |
| `orchestrator/internal/natsauth/natsauth.go` | osiris-api에 `KV_AUTHN_MACHINE` 전용 권한 + `security.machine.>` publish |
| `orchestrator/internal/jetstream/provision.go` | `TASKS` 서브젝트에 `security.>` 추가 (**F2 처분 — 아래 103.7**) |
| `bin/lint-identity-authn.sh` | **신설**. HI9·HI11·HI13·HI14①·HI14②·HI15 |

### 103.2 ★ HI15를 어떻게 성립시켰는가 — Go 1.22 메서드 패턴 채택

**문제(감독 실측)**: HI15는 *"라우터 등록부 정적 검사: GET/HEAD 이외의
모든 라우트 …가 `personOnly`를 통과하는지"* 인데, 종전 라우터는

```go
mux.HandleFunc("/api/onboarding/step0/providers", s.guard.Require(s.ob.Providers))
```

처럼 **메서드가 라우트 등록에 없었다**. 메서드는 핸들러 안의
`if r.Method != http.MethodPost` 에만 있었으므로 *"non-GET 라우트"* 라는
것이 라우트 테이블의 정적 성질이 아니었고, **린트가 판정할 대상 자체가
없었다**(직전 설계 워커가 `[미검증]`으로 남긴 지점).

**채택**: Go 1.22 `ServeMux`의 메서드 패턴(`mux.HandleFunc("POST /api/…")`).
`api/go.mod`는 이미 `go 1.22.12`다. 이로써 메서드가 라우트 테이블의 정적
성질이 되고 HI15가 **강제 가능·린트 가능**해진다.

**회귀 실측 — 추측하지 않고 돌려 봤다:**

| 항목 | 변경 전 | 변경 후 | 판정 |
|---|---|---|---|
| `GET /api/onboarding/step0/providers` (잘못된 메서드) | **405** (핸들러 `decode()`) | **405** (라우터) | **동일** |
| 다중 메서드를 받는 경로 | **0건** — `console/src/api/*.ts` 전수와 `internal/onboarding` 핸들러 전수 대조 | — | 해당 없음 |
| `POST /api/auth/session` 에 GET | 405 | 405 | 동일 |
| `PUT /api/onboarding/status` | **200** ← 메서드 검사가 아예 없었다 | **405** | **변경. 잠재 결함 정정** |
| `GET /api/onboarding/step0/gateway-token`·`verify-gateway` | 200 (검사 없음) | 405 | **변경. 같은 정정** |

즉 **회귀는 0건이고, 바뀐 두 항목은 둘 다 "읽기 메서드로 쓰기
엔드포인트가 열려 있던" 잠재 결함의 정정**이다. Go 1.22 `ServeMux`는
경로는 맞고 메서드가 틀리면 404가 아니라 **405 + `Allow` 헤더**를
돌려주므로, 405를 기대하던 기존 호출자는 그대로 405를 받는다.

**핸들러 안의 중복 `r.Method` 검사는 남겼다.** `internal/onboarding`의
`decode()`는 여전히 non-POST를 거부한다. 근거: (i) 핸들러가 "어디에
등록되었는가"에 의해서만 올바른 상태가 되면 안 되고, (ii) 비용이 비교
한 번이며, (iii) HI15의 주장은 *"핸들러 검사가 보증이 아니다"* 이지
*"핸들러 검사가 해롭다"* 가 아니다. 이제 그 분기는 도달 불가능하지만
이중 방어로 남는다.

**미들웨어 어휘는 정확히 셋**이고 라우트마다 어느 것을 쓰는지는 취향이
아니다:

| 미들웨어 | 하는 일 | 어디에 |
|---|---|---|
| `RequirePerm(code, h)` | 인증 + `authz.Authorize` | 기계 주체가 닿을 수 있는 **유일한** 라우트. GET 전용 |
| `PersonOnly(h)` | 인증 + 기계 주체 거부 | **모든 non-GET 라우트에 필수**(HI15) + 사람 전용 GET 2개 |
| `PersonOnlyPublic(h)` | 인증 없음, 그러나 `osm_` 베어러 거부 | `/api/auth/*` 2개(신선한 브라우저가 닿아야 하므로 인증을 요구할 수 없다) |

이름 셋 다 `PersonOnly`를 포함하는 것은 의도적이다 — HI15가 패턴 하나로
라우트 표를 훑을 수 있다.

### 103.3 해시 선택과 근거 — SHA-256 (설계 지정), Argon2id 아님

설계 §19.3·§3.2가 `AUTHN_MACHINE[sha256(token)]`을 명시했고 그대로
따랐다. 같은 문서가 **비밀번호에는 Argon2id**(§5.4)를 지정하는데, 이
비대칭은 오류가 아니라 정확하다:

1. 비밀번호는 **저엔트로피·사람 선택**이라 저장소를 훔친 공격자가
   후보 공간을 열거할 수 있다. Argon2id는 그 한 번의 추측을 비싸게
   만드는 도구다.
2. 이 토큰은 **`crypto/rand` 32바이트 = 256비트 균등**이다. 열거할
   공간이 없으므로 Argon2id가 사는 비트가 **정확히 0**이다.
3. 반면 **비용은 실재한다.** 비밀번호 해시는 **로그인당 1회**지만 이
   해시는 **요청당 1회**이고, 그 요청은 주체당 60 req/분으로 도는 CI
   루프다. §8.2 L3이 이미 *"Argon2id의 CPU 소모를 이용한 자원 고갈
   DoS"*를 지목하고 로그인 엔드포인트 앞에 동시성 상한을 두는데,
   이것을 모든 인증 읽기에 얹으면 그 벡터를 API 표면 전체로 옮기게
   된다.

**상수시간 비교는 별개 성질이고 그대로 지켰다.** 레코드에
`tokenHash`(= KV 키와 같은 값)를 함께 저장하고, 조회 성공 후
`subtle.ConstantTimeCompare`로 대조한다. KV 조회 자체는 상수시간이
아니지만 그것이 드러내는 것은 "이 256비트 해시가 존재하는가"뿐이다.

### 103.4 ★ `bootstrap` 클래스 유효 술어 — 하드코딩 `true`가 아니다

`internal/orgstate`가 술어 **하나**만 평가한다:

```
H10 closed := currentOrgRevision >= 1  ∧  credentialedHolders(org.admin) >= 1
bootstrap 유효 := ¬H10closed
```

두 연접항을 **서로 다른 인터페이스**로 분리했다.

**첫째 항 `currentOrgRevision`** — 그래프 사실이고 오늘 읽을 수 있다
(`graphread.CurrentOrgRevision`). **읽지 못하면 판정 불가 → 에러 →
거부(fail-closed)**. 이 스택의 실측값은 `38`이다(조직이 이미 있다).

**둘째 항 `credentialedHolders(org.admin)`** — 이것이 감독이 "지금
판정할 수 없는 부분"으로 지목한 곳이다. **결론부터: 오늘 이 값은
`0`이고, 그것은 가정이 아니라 측정이다.** 두 연접이 각각 독립적으로
지금 확인된다:

1. **`org.admin`을 보유할 수 있는 `:Party`가 없다.** 실행 중인 그래프의
   `:Permission` 카탈로그를 조회한 결과 9개 코드
   (`fs.workspace.write`, `git.commit`, `git.push`, `k8s.read.ownteam`,
   `llm.gateway`, `msg.external.send`, `net.egress.github`,
   `net.egress.pkg`, `spend.request`)뿐이고 **`org.admin`은 없다**.
   §20.4가 그것을 추가하며, 그것이 다음 과제다.
2. **자격증명을 보유할 수 있는 `:Party`가 없다.** `AUTHN_CRED`가
   존재하지 않는다 — 비밀번호 설정 엔드포인트도, 등록 티켓 상환도,
   IdP 어댑터도, `api/` 어디에도 사람 자격증명을 쓰는 코드 경로가
   없다.

증명된 0 두 개의 논리곱은 0이다. 그래서 구현은
`orgstate.NoCredentialStore{}`라는 **이름 있는 타입**이고 인라인
`return 0`이 아니다 — §20/§5 워커가 `cmd/osiris-api/main.go`의 배선
지점에서 **의식적으로 교체**해야 하도록.

**RBAC가 들어오면 자동으로 올바르게 되는 것**: `CredentialDirectory`
구현체를 실제 질의로 갈아끼우는 것 하나뿐이다. `Store.classValid`도,
가드도, 라우터도 바뀌지 않는다. 그리고 술어가 **연접**이라는 구조 덕에
오늘의 스텁이 안전하다 — 조직이 없으면 둘째 항은 아예 조회되지 않아
(단축 평가) 온보딩 구간의 동작이 §19.5 그대로다.

**`org` 클래스는 오늘 거부된다.** `발급자 :Person이 활성`을 평가할
수단이 없으므로 `classValid`의 `org` 분기는 **에러(=거부)** 를 돌려주고,
발급 핸들러도 `501`로 거절한다. "판정할 수 없으면 참으로 친다"를 하지
않았다 — 그것이 정확히 감독이 금지한 형태다.

**실측(fail-closed)**: `docker compose stop neo4j` 상태에서
① 발급 시도 → **503**, ② 양성 캐시(≤60초) 안의 기계 주체 요청 → 통과,
③ 캐시 만료 후 → **401**, ④ 설정 토큰 경로 → **200**(영향 없음).
neo4j 복구 후 정상 복귀.

### 103.5 완료조건 실측 결과 (11항 전부)

검증 드라이버는 스크래치패드에서 돌렸고 **토큰 평문은 셸 변수에만**
존재했다. **응답 바디는 한 번도 출력하지 않았다**(상태 코드·헤더만).

| # | 조건 | 결과 |
|---|---|---|
| 1 | 기계 주체로 읽기 3종 200 | **PASS** — `/onboarding/status`, `/step0/status`, `/step0/alias-defaults` 모두 200 (+ `/whoami` 200) |
| 2 | 쓰기 6종 거부, **구조적으로** | **PASS** — `omniroute-admin`·`providers`·`aliases`·`gateway-token`·`verify-gateway` **403(PersonOnly, 핸들러 진입 전)**, `POST/GET/DELETE /machine-principals` 403(H27), `POST /auth/session`·`/auth/logout` 403. 추가 구조 증거: `PUT /onboarding/status` → **405(라우팅 미스)**, `GET /step0/providers` → **405**, 기계 토큰 요청의 `Set-Cookie` 헤더 **0건**(HI13) |
| 3 | SSE 기계 주체 거부 | **PASS** — `GET /api/events` **403** |
| 4 | 기존 두 경로 200 (회귀 없음) | **PASS** — 설정 토큰 Bearer 3종 200, 쿠키 교환 200 + 쿠키 3종 200 + SSE 200, 쿠키 POST providers 400(핸들러 도달), 쿠키 `GET /machine-principals` 200 |
| 5 | 혼재 400 / 무인증 401 / 폐기 401 / 만료 401 | **PASS** — 쿠키+`osm_` **400**, 쿠키+설정토큰(레거시 조합) **200 유지**, 무인증 401, 위조 `osm_` 401, 리보크 후 401, TTL 1초 만료 후 401 |
| 6 | 레이트리밋 61번째 | **PASS** — 61회 순차 요청에서 `200 × 60`, `429 × 1`, **첫 429가 정확히 61번째** |
| 7 | 신규 린트 통과 + 위반 주입 시 실패 후 되돌림 | **PASS** — 11건 주입 전부 탐지, 전 파일 md5 원복 확인 (103.6) |
| 8 | 기존 린트 2종 + `verify-isolation.sh` | **PASS** — driver-boundary PASS, health-invariant PASS, verify-isolation **15/15** |
| 9 | `docker compose down && up -d` 후 재확인 | **PASS** — 컨테이너 8/8 healthy, **기계 주체 레코드 생존**(JetStream KV 영속) 200, 재기동 후에도 쓰기 403, 설정 토큰·쿠키 경로 200 |
| 10 | 로그 유출 0건 | **PASS** — `docker compose logs` 전체에 대해 발급 토큰 값 **0건**, `osm_` 문자열 **0건**, 설정 토큰 값 **0건**. 저장소 전체 파일 grep도 **0건** |
| 11 | 사용자 자산 무변경 | **PASS** — `<host-infra>` 아래 최근 3시간 변경 파일 **0건**, 프로덕션 `omniroute` **RestartCount=0 (무변화)**, `<upstream-proxy>`·`<svc-b>-*`·`<svc-a>_*` 전부 계속 running |

**추가 실측(설계 요구지만 완료조건 목록에 없던 것)**:
- 발급 시점 스코프 강제(MP3): `org.admin`/`org.manage.people`/
  `org.manage.roles`/미지 코드 → 전부 **400**, 24h 초과 TTL → **400**.
- 감사(MP6): `security.machine.issued`/`first_used`/`revoked`/`expired`가
  `TASKS`에 실제로 적재됨을 SSE 재생으로 확인(페이로드에 `osm_` **0건**).

### 103.6 린트 위반 주입 시험 (11건, 전부 탐지)

각 건은 백업 → 위반 1개 주입 → 린트 실행 → **비0 종료 확인** → 원복 →
`cmp`로 바이트 동일 확인. 마지막에 원복된 트리에서 린트가 다시 PASS함을
확인했고, 별도로 7개 대상 파일의 md5를 사전/사후 대조했다(전부 OK).

| # | 주입한 위반 | 탐지 |
|---|---|---|
| 1 | `POST /step0/providers`가 `PersonOnly`를 잃음 | ✅ HI15 |
| 2 | 라우트 패턴에서 HTTP 메서드 제거 | ✅ HI15 (*"has no HTTP method in its pattern"*) |
| 3 | `POST /machine-principals`가 `PersonOnly`를 잃음 | ✅ HI15 |
| 4 | `bin/` 스크립트가 발급 엔드포인트 호출 | ✅ HI14① |
| 5 | `ReadPrefix`를 `"org."`로 확대 | ✅ HI14② |
| 6 | `MachineGrantable`에서 접두 검사 제거 | ✅ HI14② |
| 7 | `Store.Issue`가 허용목록 적용을 멈춤 | ✅ HI14② |
| 8 | `machineauth`가 `internal/session`을 import | ✅ HI13 |
| 9 | `.env.example`에 `AUTHN_MACHINE_BUCKET_TTL` 추가 | ✅ HI11 |
| 10 | `natsauth.go`가 osiris-core에 `$KV.AUTHN_` 부여 | ✅ HI9 |
| 11 | 생성된 `authorization.conf`가 osiris-core에 `$KV.AUTHN_` 부여 | ✅ HI9 |

2번이 특히 중요하다 — 메서드 없는 라우트를 **실패로 처리**하지 않으면
누군가 메서드 패턴을 빼는 순간 HI15 전체가 조용히 공허참이 된다.

### 103.7 ★ 상한 인접 판단 1건 — `TASKS` 서브젝트에 `security.>` 추가

**이것은 감독·사용자의 승인이 필요한 항목이다. 임의로 결정하지 않았고
숨기지도 않았다.**

`60-identity-authn.md` §17 **F2**(그리고 §100.7이 되풀이한 것):
*"§8.5는 `security.genesis.used`를 `TASKS` 스트림에 발행한다고 서술하는데,
§7.2의 `TASKS` 서브젝트 목록에 `security.*`가 없다. 기존 내부 불일치이며,
이 설계의 `security.authn.*`/… 도 같은 자리를 필요로 한다"* — 그리고
F2가 스스로 적은 처분안이 **"§7.2 `TASKS` 행에 `security.*` 추가"**다.
설계 워커는 그것을 §7.2 문서 소유자의 몫으로 남기고 적용하지 않았다.

§19의 **MP6이 발급을 감사 발행에 fail-closed로 묶는다**. `security.*`를
받는 스트림이 없으면 JetStream PubAck이 오지 않고, 따라서 **기계 주체를
단 하나도 발급할 수 없다.** 선택지는 두 개였다: (i) 이미 문서에 적힌
처분안을 코드에 반영한다, (ii) §19를 구현 불가로 보고하고 멈춘다.

(i)을 택했고, **최소한으로** 했다 — `TASKS`의 `Subjects`에 패턴 하나
추가, 보존·저장 정책 무변경, 다른 스트림 무변경, 설계 문서 무수정.
캡처 패턴을 넓히는 것은 **누구에게도 권한을 주지 않는다**: `security.*`에
누가 publish할 수 있는지는 NATS ACL이 정하고, 거기에 있는 유일한 부여는
osiris-api의 **publish 전용** `security.machine.>` 하나다.

**그럼에도 이것은 §7("불변 자산")의 표를 건드린 것이므로 보고 대상이다.**
되돌리기 비용은 한 줄이다.

### 103.8 임의로 내린 결정 전부

1. **SSE(`GET /api/events`)를 사람 전용으로 했다 — §19.6과 §102.3의
   충돌.** §19.6의 *"할 수 있어야 한다"* 칸은 `GET /api/events` SSE
   구독(§81 #5)을 기계 주체 능력으로 열거한다. §102.3은 같은 경로를
   **"(b) 전용"** 으로 분류한다. **두 문장은 정면으로 충돌한다.**
   §102.3이 더 나중이고 더 구체적이며 이번 과제의 완료조건이 요구하는
   쪽이라 그것을 따랐다. **정직한 대가: §81 #5(Last-Event-ID 재개)에는
   기계 주체 대체 경로가 없고 여전히 조작자 자격증명이 필요하다.**
   §19.10의 대체 경로 표에서 이 한 행만 미충족이다. **충돌 해소는
   감독·사용자의 결정이다.**
2. **엔드포인트 형상**을 정했다(설계는 `POST /machine-principals`만
   명명): `POST /api/machine-principals`(발급) ·
   `GET /api/machine-principals`(목록) ·
   `DELETE /api/machine-principals/{id}`(폐기). **셋 다 `PersonOnly`**,
   GET 포함 — §19.6 ④가 자격증명 상태 조회를 `org.read.*`가 아니라고
   분류했으므로.
3. **`GET /api/whoami` 추가.** §19.6이 이름을 대며 기계 주체가 부를 수
   있어야 한다고 적은 것. 서버가 이 요청에 대해 내린 판단만 돌려준다.
4. **`ttlSeconds` 선택 필드.** 클래스 상한 이하로만 **줄일 수 있다**
   (`clampTTL`이 상한 초과를 거부). 설계는 bootstrap TTL을 24h 고정으로
   적었으므로 이것은 이탈이다 — 만료 거부를 실측하려면 필요했고,
   **늘리는 방향으로는 쓸 수 없다.**
5. **레이트리밋을 프로세스 메모리에 두었다**(슬라이딩 윈도우). §8.2의
   `AUTHN_THROTTLE` 버킷은 로그인 3층(L1~L3)의 것이고 §8의 몫이다.
   osiris-api는 이 빌드에서 단일 레플리카다(`internal/session`이 이미
   같은 제약을 문서화). 레플리카가 늘거나 §8이 오면 옮긴다.
6. **`lastUsedAt` 쓰기를 1분에 1회로 합쳤다.** §19.7이 읽기마다
   **이벤트**를 발행하지 말라고 한 이유(이벤트 폭풍)가 KV 쓰기에도
   그대로 적용된다 — 요청마다 쓰면 같은 폭풍을 한 층 아래에서 재현한다.
   필드의 목적("사용 여부는 항상 알 수 있다" + Ops의 "마지막 사용
   시각")은 유지되며, 지연 상한을 응답의
   `lastUsedAtStalenessSeconds`로 **명시**한다.
7. **혼재 400의 범위를 정확히 좁혔다**: **세션 쿠키 + `osm_` 베어러**
   조합만 400. **쿠키 + 설정 토큰 베어러는 오류가 아니다** — 이번
   과제 이전부터 있던 조합이고, 그것을 오류로 만드는 것이 바로 금지된
   회귀다. 실측으로 200 유지 확인.
8. **`ErrUnavailable`만 503으로 구분**하고 나머지 실패는 전부 동일한
   401 문구. §8.2의 *"동일한 일반 메시지"*를 지키되, "자격증명 저장소가
   죽었다"는 운영 사실은 구별 가능해야 한다(아니면 조작자가 영원히
   재시도한다).
9. **KV 버킷은 osiris-api가 스스로 만든다**(오케스트레이터가 아니라).
   §14.1이 *"`AUTHN_*` (JetStream KV) — osiris-api 단독 소유"*라고 적은
   그대로이며, 부팅 순서 의존을 만들지 않는다. 권한은 `KV_AUTHN_MACHINE`
   **단일 스트림 이름을 리터럴로** 지정했고 `STREAM.DELETE`는 주지
   않았다 — 침해된 osiris-api가 자기 자격증명의 감사 표면을 지울 수
   없게.
10. **`$JS.API.INFO` publish 권한 1개 추가.** 계정 수준 프로브다.
    **추측이 아니라 실측**: 없으면 KV 생성이 타임아웃하고 nats-server가
    `Publish Violation - User "osiris-api", Subject "$JS.API.INFO"`를
    남긴다. 스트림에 대한 권한을 하나도 주지 않는다.
11. **`authz.KindOperator`라는 이름.** 오늘의 쿠키 세션과 설정 토큰
    베어러는 `person`도 `machine`도 아니다 — H10 이후 (a) master key가
    될 것의 현재 모습이다. 이름을 미리 갈라 두어, H10 워커가 **개념이
    아니라 자격증명만** 바꾸면 되게 했다. `OperatorScope()`는 오늘 코드
    5개 전부다(오늘의 설정 토큰 보유자는 이미 콘솔의 전권을 갖고
    있으므로 여기서 좁히는 것은 보안 개선이 아니라 금지된 회귀다).
12. **`:Permission` 그래프 카탈로그에 5개 코드를 시드하지 않았다.**
    그것은 20 §9.2 단계 3(§20의 몫)이고, 하면 `catalogVersion`과
    오케스트레이터 grant-catalog를 함께 올려야 해서(I9) 이번 과제의
    범위를 넘는다. **Go 상수만 만들었다** — I9의 "두 곳이 일치해야
    한다" 쌍 중 코드 쪽 절반이다.

### 103.9 `[미검증]`으로 남긴 것

1. **`org` 클래스의 런타임 동작 전체.** 코드 경로는 있으나 `:Person`
   세션이 존재하지 않아 **한 번도 실행되지 않았다.** 발급은 501,
   `classValid`의 `org` 분기는 거부. §20이 오면 그 두 곳이 켜진다.
2. **MP5의 "발급자 `:Person` 퇴사 시 그가 발급한 org 클래스 주체 자동
   폐기"** — `org.event.*` 구독 경로가 필요하고, 그 대상(org 클래스)이
   아직 존재하지 않는다. 미구현.
3. **MP6의 `:Notification` 팬아웃** — `org.admin` 보유자가 0명이므로
   보낼 대상이 없다. 이벤트 발행(그리고 발급의 fail-closed 결합)은
   구현·실측했고, 알림만 미구현.
4. **MP6의 "콘솔 Ops에 활성 기계 주체 수 상시 표시"** — 데이터
   (`GET /api/machine-principals`)는 있으나 콘솔 UI는 만들지 않았다
   (콘솔 IA는 이 과제 범위 밖).
5. **`security.machine.expired`의 능동 발행.** 만료는 **요청이 들어와
   조회될 때** 감지·발행된다. 아무도 쓰지 않는 채 만료된 주체는
   버킷의 `max_age`(90일)로 조용히 사라지고 이벤트가 없다. 주기적
   스위퍼는 만들지 않았다.
6. **레이트리밋 429가 게이트웨이 앞단에서 어떻게 보이는지** — L2(소스
   IP)·L3(엔드포인트 전역)은 §8의 몫이라 손대지 않았다. 주체별 상한만
   구현했다.
7. **다중 레플리카 동작.** 양성 캐시·레이트리밋·`lastUsedWrite` 합침이
   전부 프로세스 로컬이다. 단일 레플리카 전제(기존 문서화된 제약)와
   같다.

### 103.10 상한에 걸려 멈춘 것

- **G1~G10 / A1~A13 / D1~D20 / W1~W5 / I1~I17 / C1~C10 — 하나도 손대지
  않았다.** W1~W5는 §19.9의 대조표 그대로 전부 무영향이다(기계 주체는
  컨테이너가 아니고 genesis 스코프를 요청할 수단이 없으며, `org.read.`
  접두 제한이 그것을 **표현 불가능**하게 만든다).
- **H10을 켜지 않았다.** master key 재명명(`osk_`), 단회용 교환,
  `setup_token` 파일 삭제 — 전부 손대지 않았다. §102.4의 순서.
- **RBAC(§20/§21)를 만들지 않았다.** 역할·프로파일·`IN_ORG`·위임 규칙
  DG1~DG5·`CheckDelegation` 0줄. 공유 원시(코드 5개 + `Authorize()`)만
  만들었고 **RBAC가 꽂힐 시그니처**(`authz.PermissionSource`,
  `orgstate.CredentialDirectory`)를 선언만 해 두었다.
- **`.env`/`.env.example`에 신규 변수 0건**(HI11 실측). 버킷 이름·TTL·
  레이트리밋·토큰 접두는 전부 컴파일 상수다.
- **설계 문서 무수정.** §19.6과 §102.3의 SSE 충돌(103.8-1)과 F2 처분
  (103.7)은 **고치지 않고 보고**했다.

---

## 104. §102.3의 SSE 분류 정정 — 감독의 오류였다 (그리고 그것이 드러낸 기존 결함 1건)

이 문서는 append-only이므로 **§102를 고치지 않는다.** 이 절이 §102.3의
**SSE 한 줄만** 정정한다. §102.3을 읽는 사람은 반드시 이 절까지 읽어야
한다. `60-identity-authn.md`는 살아 있는 설계 문서이므로 **직접
갱신했다**(§19.6 표, §19.6.1 신설, §19.10 표 #5행, §13 HI15/HI15b).

### 104.1 무엇이 틀렸는가

§102.3은 이렇게 적었다:

> `GET /api/events`(SSE) — **(b) 전용.** 브라우저 `EventSource`가 커스텀
> 헤더를 붙이지 못하므로 쿠키 경로 외에는 불가능하다.

**전제는 참이고 결론은 거짓이다.** `EventSource`가 헤더를 붙이지 못한다는
사실은 **"브라우저는 쿠키를 써야 한다"** 를 증명한다. 그것은
**"비브라우저 클라이언트를 배제한다"** 를 증명하지 않는다 — curl과 CI는
`Authorization` 헤더를 붙일 수 있고, 애초에 기계 주체라는 것이 존재하는
이유가 정확히 그 클라이언트들이다. 브라우저의 제약을 프로토콜의 제약으로
과일반화한 것이다.

**이 오류는 감독이 §102.3에 쓴 것이고, 완료조건 3번("SSE가 기계 주체로
거부됨")으로 구현 워커에게 그대로 내려갔다.** 구현 워커는 지시를 따라
`PersonOnly`로 막았고, 동시에 그것이 `60-identity-authn.md` §19.6의
*"할 수 있어야 한다"* 칸(`GET /api/events` SSE 구독, §81 #5)과 정면
충돌한다는 사실을 §103.8-1에 올렸다. **지시를 따르고 충돌을 보고한 것이
올바른 처신이었다** — 워커가 자기 판단으로 지시를 뒤집었다면 그것은 다른
종류의 문제였을 것이다.

### 104.2 정정 — `GET /api/events`는 (b) 또는 (c)

`RequirePerm(org.read.ops, …)`로 바꾸었다. §20.4의 `org.read.ops` 정의
자체가 *"운영 상태 읽기 — 에이전트 헬스, 클러스터, **이벤트 스트림**, Ops
화면"* 으로 **이벤트 스트림을 명시**하고 있다. 즉 설계는 처음부터 이
경로를 그 코드에 묶어 두었고, §102.3만 어긋나 있었다.

**결과: §19.10 대체 경로 표의 마지막 미충족 행(§81 #5, `Last-Event-ID`
SSE 재개)이 채워졌다.** §103.8-1이 "정직한 대가"로 남겨 둔 항목이 사라진다.

실측:

| 자격증명 | `GET /api/events` |
|---|---|
| 기계 주체 (`org.read.ops` 보유) | **200** |
| 기계 주체 (`org.read.onboarding`만) | **403** ← 같은 `authorize()`가 판정 |
| 세션 쿠키 | **200** |
| 설정 토큰 Bearer | **200** |
| 무인증 | **401** |
| 쿠키 + `osm_` 혼재 | **400** |

### 104.3 ★ 이 변경이 드러낸 것 — 연결 시점 인증은 연결 수명 인증이 아니다

감독이 함께 지시한 질문: *"연결이 열려 있는 동안 토큰이 폐기되면 무슨 일이
일어나야 하는가?"*

**먼저 기준선을 실측했다 — 변경 전 빌드에 대해:**

```
세션 쿠키로 SSE 연결       → 열림 (hello 프레임 수신 확인)
POST /api/auth/logout      → 200
같은 쿠키로 일반 요청      → 401  (서버측 무효화는 확실히 됐다)
열려 있던 SSE 스트림       → t+10s 열림 / t+20s 열림 / t+30s 열림
                             t+40s 열림 / t+50s 열림 / t+60s 열림
                             (클라이언트가 끊어서 끝났다)
```

**이것은 기계 주체와 무관한, 이미 존재하던 결함이다.** `10-core-architecture.md`
§8.4의 *"로그아웃 시 서버측 무효화"*와 `60-identity-authn.md` §19.8 MP5의
*"즉시 리보크"*가 **`/api/events`에 대해서만 조용히 거짓**이었다. 인증이
연결 시점에 한 번만 일어나고 그 뒤로 다시 일어나지 않았기 때문이다.
기계 주체를 SSE에 들이지 않았더라도 결함은 그대로 남아 있었을 것이다 —
감독의 질문이 그것을 끄집어냈다.

**고친 방식** (설계 §19.6.1로 명문화):

- 장수명 응답은 **30초마다** 자격증명을 재검증하고, 실패하면 요청
  컨텍스트를 취소한다. SSE 루프는 매 페치 주기(2초)마다 `ctx.Err()`를
  보므로 최악 32초 안에 스트림이 닫힌다.
- **왜 30초인가**: §7.4가 리보크 지연 상한을 "최대 60초"로 못박고 그것을
  명시적 대가로 인정했다. 장수명 연결이 그 위에 **두 번째·더 큰 지연**을
  얹으면 새 대가를 만드는 것이므로, 주기는 60초 봉투 안쪽이어야 한다.
- **재검증은 양성 캐시를 우회한다.** 캐시를 다시 읽으면 "60초 전에
  유효했는가"를 묻는 것이고 그 답은 이미 안다. 권위 있는 KV 조회를 한다.
- **레이트리밋을 소비하지 않고 `lastUsedAt`도 갱신하지 않는다.** 워치독
  틱은 서버 자신의 행위이지 호출자의 사용이 아니다. 세면 (i) 긴 SSE
  연결이 그것을 연 CI 루프를 스스로 스로틀하고, (ii) 유휴 연결이 "마지막
  사용 시각"에 활동으로 나타나 그 필드의 목적이 망가진다.
- **모든 주체 종류에 적용한다.** 워치독은 `Principal`에 대해 쓰였지
  기계 주체에 대해 쓰이지 않았다 — 위 기준선이 보여주듯 이것은 기계 주체
  전용 문제가 아니었다.
- **감사**: 종료 시 로그 1줄, `actorRef`만. 자격증명은 등장하지 않는다.

**변경 후 실측:**

| 시나리오 | 결과 |
|---|---|
| 기계 주체 토큰을 스트리밍 중 폐기 | **28초 후 스트림 종료** (예산 45초) |
| 세션을 스트리밍 중 로그아웃 | **28초 후 스트림 종료** (동일 메커니즘) |
| 폐기하지 않은 유효 토큰 | **65초(워치독 2틱 이상) 후에도 열린 채 유지** — 오탐 0 |
| 종료 로그 | `closing open stream for machine:mp-sse-<id>: credential is no longer valid (machine principal not found)` / `... for master-key: ... (session revoked or expired)` — **actorRef만, 토큰 없음** |

### 104.4 린트 HI15b 신설

`bin/lint-identity-authn.sh`에 하위 검사를 하나 더했다: **`GET /api/events`가
`RequirePermStreaming`으로 등록되어 있어야 한다.**

어떤 핸들러가 블로킹인지 정적으로 추측하는 대신 **유일한 장수명 라우트를
이름으로 못박았다.** 두 번째 스트리밍 라우트가 생기면 그때 목록에 추가한다 —
추측하는 린트보다 대상을 명시하는 린트가 정직하다.

**위반 주입 2건, 둘 다 의도대로 동작:**

| 주입 | 결과 |
|---|---|
| `/api/events`를 `RequirePerm`(재검증 없음)으로 되돌림 | **탐지, exit 1** — *"GET /api/events is not wrapped in RequirePermStreaming"* |
| `/api/events` 라우트를 통째로 삭제 | **`skipped`로 명시 보고**(조용한 통과 아님) |

기존 11건 주입도 전부 재실행해 **11/11 탐지**, 전 파일 원복 확인.

### 104.5 재실측 — 완료조건 11개 전부 (라우트 한 줄 변경 후)

*"한 줄 바꿨는데 다른 게 깨진"* 전례가 있으므로 전수 재실행했다.
**PASS=55, FAIL=0** (SSE 항목이 1개에서 2개로 늘어 53→55).

| # | 조건 | 결과 |
|---|---|---|
| 1 | 읽기 3종 200 | **PASS** (+ `/whoami` 200) |
| 2 | 쓰기 6종 구조적 거부 | **PASS** — 변화 없음. 403(PersonOnly) + 405(라우팅 미스) + `Set-Cookie` 0건 |
| 3 | ~~SSE 거부~~ → **SSE 접근 + 코드 판정** | **PASS** — `org.read.ops` 보유 200 / 미보유 **403** |
| 4 | 기존 두 경로 200 | **PASS** — 설정 토큰 3종, 쿠키 3종 + SSE 200, 쿠키 POST 400(핸들러 도달), 쿠키 목록 200 |
| 5 | 혼재 400 / 무인증 401 / 폐기 401 / 만료 401 | **PASS** — 레거시 조합(쿠키+설정토큰) 200 유지 포함 |
| 6 | 레이트리밋 61번째 | **PASS** — `200×60`, 첫 429가 정확히 61번째 |
| 7 | 린트 + 위반 주입 | **PASS** — 13건(기존 11 + HI15b 2) 전부 의도대로, 원복 확인 |
| 8 | 기존 린트 2종 + `verify-isolation.sh` | **PASS** / **15/15** |
| 9 | `down && up -d` 후 재확인 | **PASS** — 8/8 healthy, KV 레코드 생존, 세 경로 전부 정상 |
| 10 | 로그 유출 0건 | **PASS** — 토큰 값·`osm_`·설정 토큰 **전부 0**. SSE 경로가 `Authorization` 헤더를 새로 다루므로 재확인했고, 워치독 종료 로그도 `actorRef`만 남긴다 |
| 11 | 사용자 자산 무변경 | **PASS** — `<host-infra>` 무접촉, 프로덕션 `omniroute` RestartCount **0 무변화**, 프로덕션 컨테이너 26개 전부 running |

### 104.6 §103의 어느 항목이 이 절로 대체되는가

- **§103.8-1 (SSE 이탈)** — **철회.** 이탈이 아니라 감독의 §102.3 오류였고,
  이제 §19.6 원문대로 동작한다. §103.8-1이 남긴 *"§81 #5에는 기계 주체
  대체 경로가 없다"*는 서술도 **더 이상 사실이 아니다.**
- **§103.9-4·5·6·7** 및 나머지 `[미검증]` 항목, **§103.10의 상한** —
  전부 그대로 유효하다. H10 미착수, RBAC 미구현, `org` 클래스 501 유지
  (감독 결정 2), `.env` 신규 변수 0건, 설정 토큰 Bearer 경로 유지.
- **§103.7 (`security.>` 추가)** — 감독이 **수용**했다.

---

## 105. RBAC(§20/§21) 구현 — **소급 기록**, 그리고 그 공백이 무엇을 남겼는가

이 절은 이례적이다: **이미 존재하는 코드를 뒤늦게 기록한다.** §104까지의 노트는
16:20에 쓰였고, `api/internal/{authz,people,enrollment,authncred,orgstate}`와
`core/internal/graphdb/{seed,writeops,invariants}`의 RBAC 구현은 **16:35~17:08에**
들어왔다 — 즉 **노트보다 나중에 작업이 계속되고 문서화 없이 끝났다.**
§103.10과 §104.6은 *"RBAC 미구현 ... 위임 규칙 DG1~DG5·`CheckDelegation` 0줄"*로
남아 있어, 이 문서를 신뢰해 이어받는 다음 워커에게 **거짓**이 된다. 그 상태를
정정하는 것이 이 절의 첫 번째 목적이다.

### 105.1 실제로 구현되어 있던 것 (실측)

| 항목 | 위치 | 상태 |
|---|---|---|
| 권한 코드 5개 | `core/internal/graphdb/seed.go:109` | `org.admin`·`org.manage.people`·`org.manage.roles`·`org.read.ops`·`org.read.onboarding` |
| 프로파일 3종 / 조직 역할 3종 | `seed.go:158,186` | `pp-org-*` / `role-org-*`, §20.5 표와 일치(`pp-org-member` GRANTS 비어 있음 포함) |
| `IN_ORG` | `writeops.go` · `invariants.go` · `graphread/client.go` | 계층(`belongs_to`)이 아니라 조직 멤버십 |
| I14~I17 | `invariants.go:62-65` | 등록·구현 완료 |
| `authz.CheckDelegation` DG1~DG5 | `authz/delegation.go` (279줄) | DG3→DG1′→DG1→DG2→DG4 순, `⊆` 유지, DG5는 `NotifyAdmins` |
| `GraphPermissionSource` | `authz/permsource.go` (178줄) | 10초 TTL 캐시 + `ResolveFresh`(위임 판정·워치독 전용) |
| C10 6개 액션 경로 | `people/{people,handlers}.go` | 전부 `prepare()` → `CheckDelegation` 경유 |
| `GraphCredentialDirectory` | `orgstate/orgstate.go` | DG4 2층 중 API 층. `NoCredentialStore` 삭제됨 |

**구현 품질에 대한 판단**: 설계와 대조해 **이탈을 찾지 못했다.** 특히 §21.4-2의
`DENIES` 우회, DG2의 티켓 발급 포함, `⊊`가 아니라 `⊆`를 쓴 이유까지 코드 주석에
근거와 함께 남아 있다. 문제는 코드가 아니라 **기록과 검증의 공백**이었다.

### 105.2 ★ 그 공백이 실제로 남긴 결함 2건 (둘 다 이번에 발견·수정)

문서화되지 않은 작업은 **검증되지도 않았다.** §104.5가 *"한 줄 바꿨는데 다른 게
깨진 전례가 있으므로 전수 재실행했다(PASS=55, FAIL=0)"*를 자랑한 그 규율이
RBAC 작업에는 적용되지 않았고, 그래서 두 가지가 깨진 채로 남아 있었다.

**(1) `bin/lint-identity-authn.sh`의 HI15가 6개 라우트에 대해 거짓 실패하고
있었다.** RBAC가 도입한 조직 역할 라우트는 미들웨어를 두 겹 쌓아 **3줄에 걸쳐**
등록되는데, HI15의 정적 검사는 **한 줄에 한 라우트**를 가정해 핸들러 표현식을
빈 문자열로 뽑아내고 *"not wrapped in PersonOnly"*를 보고했다. 실제로는 전부
`PersonOnly`로 감싸여 있다 — 즉 **거짓 실패**이며, 린트에서 더 위험한 방향이다
(아무도 믿지 않는 린트가 된다). 라우터 본문을 **괄호 균형까지 이어 붙여**
정규화한 뒤 읽도록 고쳤다(문자열 리터럴 안의 괄호는 건너뛴다). 고친 뒤
**26개 라우트 전수 통과.**

**(2) §21.3이 요구한 린트 HI16·HI17·HI18이 하나도 없었다.** 그 결과 H31의
*"위임 판정은 단일 함수에만 존재한다"*가 **코드로 강제되지 않고 관습으로만**
지켜지고 있었다 — 새 핸들러 하나가 `CheckDelegation`을 빠뜨려도 아무것도
실패하지 않는 상태였다. §106.4에 신설 내역을 적는다.

### 105.3 여전히 미구현으로 남는 것

- **`core`의 graph-write가 DG를 재평가하지 않는다** — 이는 결함이 아니라 §21.4-1이
  명시적으로 택한 것이다(*"판정은 한 곳에 두고, core 쪽에는 §6.5의 fail-closed와
  I14만 남긴다"*). I14가 api 침해에도 살아남는 마지막 방벽이다.
- **HI19~HI20, HI1~HI8, HI10, HI12** — 여전히 대상 코드가 없거나 다른 작업 소유.
- **F9/F10/F12** (§101.13) — 그대로 열려 있다. 특히 **F9**는 이제 실제 부채다:
  권한 코드 5개가 시드에 들어갔으므로 `10-core-architecture.md` §8.1 표의 5행
  추가와 `catalogVersion` 동반 상승이 **I9 준수 요구**로 확정되었다.

---

## 106. H10 — master key 구현 (§4.0/H22, §4.3, §4.5/H23)

§102.4가 지정한 `(c) → H10` 순서를 지켜 (c)가 완료된 뒤 착수했다. §103.10의
*"H10을 켜지 않았다"*가 이 절로 해소된다.

### 106.1 산출물

| 파일 | 무엇 |
|---|---|
| `api/internal/masterkey/masterkey.go` (신설) | 값 소유·`osk_` 접두·부팅 결정(4분기)·`Purge` |
| `api/internal/adminauth/` (**삭제**) | 개념이 바뀌었으므로 편집이 아니라 대체 |
| `api/internal/authn/authn.go` | 승인 지점 3곳에 H10, 본문 키 `masterKey`, ⑧ 시점 은퇴, 감사 발행 deadline |
| `api/internal/config/config.go` | `MasterKeyPath` + `LegacySetupTokenPath` |
| `api/internal/secevents/secevents.go` | `security.masterkey.retired` / `.purge_failed` |
| `api/cmd/osiris-api/main.go` | Boot를 **`orgEval` 이후로 이동**(아래 106.3) |
| `orchestrator/internal/natsauth/natsauth.go` | `security.masterkey.>` 발행 권한 |
| `bin/show-master-key.sh` (신설) / `bin/show-setup-token.sh` (삭제) | 재조회 경로 |
| `osiris-up` 8단계 | 네 결과별 문구 + 조건부 대기 |
| `docker-compose.yml` | `MASTER_KEY_PATH` (신규 `.env` 변수 0건 — 기본값 보유) |
| `console/src/{api/auth.ts,components/TokenEntryScreen.tsx}` | §4.0.1의 "바꿀 것 3 / 지킬 것 2" |

### 106.2 ★ 설계 문서와 어긋난 판단 1건 — 파일 수명을 rev가 아니라 H10 술어에 걸었다

**보고 대상.** §4.5는 *"`currentOrgRevision == 0`일 때만 없는 master key를 새로
만든다"*, §4.0 마이그레이션 행은 *"조직이 이미 있으면 삭제"*로 **조건 하나**를
쓴다. 이 구현은 **두 조건 전체(H10 술어)** 에 걸었다.

**근거 — §4.3이 이미 같은 실패를 이유로 한 조건 형태를 기각했다**:
*"조건 1만으로 닫으면 §4.1 ⑥이 실패한 경우 아무도 못 들어가는 잠금이 된다."*
파일 규칙을 rev만으로 쓰면 **그 잠금이 자료 차원으로 되돌아온다.** §4.1 ⑧의
재진입 규정을 그대로 따라가 보면 드러난다: 커밋으로 rev=1이 되고 ⑥/⑦이
완료되지 않은 상태(브라우저를 닫았다)에서 설계는 *"master key가 여전히
유효하고, 다시 들어와 ⑦부터 이어서 하면 된다"*고 보장하는데, 그 구간에
`osiris-api`가 재기동하면 rev만 보는 규칙은 **레거시 파일을 지우고 재생성을
거부한다** — 방금 보장한 재진입 경로가 사라지고, 대체할 관리자 자격증명은 아직
존재하지 않는다. **컨테이너 재시작 한 번으로 조직이 잠긴다.**

**추정이 아니라 실측이다.** 이 작업 시점의 이 레포 배포가 정확히 그 상태였다:
`:OrgRevision.n = 38`(쓰기 경로 시험의 결과), 활성 `:Person` **0명**, 레거시
`setup_token` 1개. 문서를 문자 그대로 따랐다면 **콘솔에 닿을 수 있는 유일한
자격증명을 삭제**했을 것이다.

술어에 걸면 §4.5가 스스로 쓴 두 차원(판정/자료)이 **서로를 정확히 따라간다** —
자료 차원은 판정 차원이 뒤늦게 따라오는 것이라는 그 절의 서술 그대로다. 설계가
서술하던 상태(인수인계가 끝난 조직)에서는 **두 읽기가 같은 값을 낸다**. 이
읽기는 오직 *절반만 끝난 인수인계*에서만 다르게 동작한다.

**§4.6을 약화시키지 않는다**: 키는 여전히 창이 열려 있을 때만 생성되고, 닫히는
즉시 거부되며, 그 뒤로 다시 만들어지지 않는다. `F13`으로 감독에게 올린다
(§106.6).

### 106.3 Boot의 위치가 하중을 받는다 — `orgEval` 이후여야 한다

구(설정 토큰) 빌드는 `run()` 최상단에서 값을 로드했다. H10 술어는 **그래프와
자격증명 저장소 둘 다** 필요하므로(§21.1의 "그래프는 KV의 존재를 알지 못한다"),
Boot는 `orgstate.New(...)` **뒤로** 내려가야 한다. 이동하지 않고 rev만 읽는
지름길을 쓰면 106.2의 잠금이 그대로 남는다.

판정 불가 시(그래프/NATS 도달 불가) Boot는 **생성도 삭제도 하지 않는다.**
"창이 열렸다"로 읽으면 DB를 뽑아서 부트스트랩 자격증명을 만드는 것이 되고
(§8.5의 금지된 역전), "닫혔다"로 읽으면 유일한 진입 수단을 파괴한다.

### 106.4 H10이 세션에도 적용된다 — §4.3의 문자보다 넓게

§4.3은 규칙을 `POST /auth/session {masterKey}`에 대해 쓴다. 거기서 멈추면
**폐쇄 이후에도 이미 발급된 쿠키가 §8.4의 12h만큼 살아남아** 전권 operator
스코프를 유지한다. 이것은 §104.3이 SSE에서 찾아낸 결함과 **같은 형태**다(연결
시점 인증은 연결 수명 인증이 아니다). MP1의 원칙이 답을 준다 — 특권이 세계의
상태에 조건부라면, **그 자격증명에서 파생된 모든 산물**에 대해 매 요청 재평가해야
한다. 그래서 승인 지점을 세 곳 모두 막았다: 베어러 브랜치, 세션 브랜치,
스트리밍 워치독.

**데드락 없음**: 창은 관리자가 자격증명을 얻는 순간 닫히고, 그것을 주는 경로
(티켓 상환)는 그 쓰기보다 **먼저** 사람 세션을 발급한다.

### 106.5 ★ 실측이 찾아낸 결함 2건 (설계 검토로는 나오지 않았다)

**(1) 신규 감사 주제가 NATS ACL에 없으면 이벤트가 유실되는 것이 아니라 요청이
멈춘다.** 첫 실행에서 `security.masterkey.retired` 발행이
`Permissions Violation`을 받았고, `secevents.Publish`는 **JetStream ack를
기다리는** 발행이므로 ack가 오지 않아 **티켓 상환 요청이 100초 넘게 매달렸다**
(curl 실측). ACL에 `security.masterkey.>`를 추가해 고쳤고, **같은 실수를 잡는
린트 `AUD`를 신설했다** — 두 파일이 서로 다른 서비스 소유라서 사람이 놓치기 쉽다.

**(2) "best effort"라고 문서화된 감사 발행이 실제로는 best effort가 아니었다.**
`authn.audit`의 주석은 *"refusing to complete an authentication because the audit
bus is down would turn a NATS outage into a total lockout"*라고 이미 옳게 적고
있었지만, 코드에는 그것을 강제하는 것이 없었다(위 (1)이 그 증거다). **2초
deadline**을 걸고, 컨텍스트를 요청에서 **분리**했다 — 중간에 끊은 호출자도
인증 결과를 만들었으므로 그 이벤트는 남아야 하고(요청 컨텍스트를 물려받으면
공격자가 조기 종료로 만드는 이벤트가 정확히 누락된다), 대신 deadline으로
묶는다. DG5의 fail-closed 발행(`people.announce`)은 **건드리지 않았다** —
그쪽은 관측되지 않은 채 일어나면 안 되는 *변경*에 수반되므로 기다리는 것이 맞다.

### 106.6 감독 판단이 필요한 것

- **F13 `[신규]`** — §4.0 마이그레이션 행과 §4.5 재생성 규칙의 조건을 **rev
  단일 조건에서 H10 술어로** 고쳐야 한다(§106.2). 이 구현은 이미 그렇게 동작하고,
  문서만 어긋나 있다. 60은 이 작업 소유가 아니어서 **고치지 않았다.**
- **F9** (§101.13) — 이제 실제 부채. §105.3 참조.
- **F1~F3, F6, F8, F10, F12** — 그대로 열려 있다.

### 106.7 완료조건 실측 (전 항목 curl/실파일 기준)

| # | 조건 | 결과 |
|---|---|---|
| 1 | 마이그레이션: 레거시 삭제 + `osk_` 재생성 | **PASS** — 로그 1줄, 파일 47바이트, 접두 `osk_` |
| 2 | 구 `setupToken` 본문 필드 | **401** (필드 자체가 사라짐) |
| 3 | `masterKey` 본문 필드 | **200** `{"kind":"master-key"}` |
| 4 | `osk_` Bearer 읽기 | **200**, `whoami`가 고정 스코프 5코드 반환 |
| 5 | 접두 없는 임의값 / `osk_` 접두 + 오답 | **401 / 401** (§8.2 동일 메시지) |
| 6 | 초기 관리자 부트스트랩(master-key 세션) | **201**, rev 40 |
| 7 | ★ 티켓 상환 → H10 폐쇄 | **PASS** — 로그 *"the H10 window has closed"* |
| 8 | ★ 자료 차원: 파일 삭제 | **PASS** — 디렉토리 비었음 |
| 9 | ★ 폐쇄 후 `masterKey` 로그인 | **403**, 이유를 설명하는 메시지 |
| 10 | ★ 폐쇄 후 `osk_` Bearer | **403** |
| 11 | 폐쇄 후 관리자 비밀번호 로그인 | **200** |
| 12 | 재기동 시 재생성 안 함(창 닫힘) | **PASS** — `withheld` 분기 로그 |
| 13 | 기존 파일이 있으면 load, 그리고 첫 요청이 자동 정리 | **PASS** — `loaded` → 403 → 파일 삭제 |
| 14 | 감사 발행 지연 | **0.17s** (deadline 도입 후), `Permissions Violation` **0건** |
| 15 | 린트 4종 + 격리 검증 | **전부 PASS**, `verify-isolation` **15/15** |
| 16 | 위반 주입 | **12/12 탐지**(아래 106.8), 전 파일 원복 확인 |
| 17 | 사용자 자산 무변경 | 프로덕션 `omniroute` RestartCount **0**, 비-osiris 컨테이너 **69개 유지** |

### 106.8 위반 주입 12건

HI16(c) 판정 우회 · HI17 역할 id 리터럴 · HI18 DG3 제거 · HI18 DG4 제거 ·
H10(a) 접두 변경 · H10(b) `Valid`의 접두 요구 제거 · H10(d) 구 필드 부활 ·
H10(e) 승인 지점 2종(각각) · H10(f) 술어 재구현 · AUD ACL 누락 ·
HI15 다중행 라우트 `PersonOnly` 해제 — **전부 의도대로 탐지.**

**H10(e)는 처음 쓴 버전이 자기 주입 시험에 실패했고, 그래서 다시 썼다.**
초판은 "operator principal 생성 횟수"와 "술어 참조 횟수"를 **합계로** 비교했는데,
참조가 생성보다 많아서 실제 검사 하나를 지워도 합계가 통과했다. 합계는 *"이것들
각각"* 을 표현할 수 없다. 함수 단위 검사(생성하는 함수는 반드시 같은 함수에서
술어를 참조해야 한다)로 바꾼 뒤 두 종류의 주입 모두 탐지했다.

### 106.9 `[미검증]`으로 남긴 것

- **폐쇄 시점에 살아 있던 master-key 세션 쿠키가 403이 되는지** — 코드는
  `principalFor`와 `revalidate` 양쪽에 있고 베어러 경로는 실측했으나, **쿠키
  경로는 측정하지 못했다.** 측정하려면 창이 열린 동안 세션을 얻고 창을 닫아야
  하는데, 이 배포는 이미 폐쇄되었고 창을 되열려면 관리자 자격증명을 KV에서
  지워야 한다(컨테이너에 `nats` CLI가 없다). 같은 술어를 같은 방식으로 호출하므로
  근거는 있으나 **실측은 아니다.**
- **삭제 실패 경로**(`security.masterkey.purge_failed`) — 삭제 불가한 파일을
  만들 방법이 이 WSL2/DrvFs 호스트에 없어 주입하지 못했다.
- **다중 레플리카** — `purgeReported`/`retiredReported`가 프로세스 로컬이다.
  단일 레플리카 전제(기존 문서화된 제약)와 같다.

### 106.10 상한에 걸려 멈춘 것

- **G1~G10 · A1~A13 · D1~D20 · W1~W5 · I1~I17 · C1~C10 — 하나도 수정·완화·
  번복하지 않았다.** W1~W5는 무영향이다(H10은 콘솔 자격증명에, W1은 genesis
  스코프에 적용되며 서로를 참조하지 않는다 — §4.2 표 그대로).
- **설계 문서 무수정.** §106.2의 이탈은 **고치지 않고 F13으로 보고**했다.
- **`.env` 신규 변수 0건** (HI11 실측 통과). `MASTER_KEY_PATH`는 compose에
  기본값과 함께 있고 `.env`가 요구하지 않는다.
- **H9 세션 승격(§4.1 ⑥)을 만들지 않았다.** 이 빌드의 인수인계는 §101 이후
  `POST /api/onboarding/initial-admin`이 **등록 티켓**을 발급하는 형태이며
  (`people/me.go`의 근거 주석 참조), ⑧의 결과(AUTHN_CRED 기록)는 동일하다.
  승격 경로를 추가로 만드는 것은 이 작업의 범위를 넘고, 두 경로가 공존하면
  "자격증명을 설정하는 방법이 둘"이 된다.
- **`credentialPending` 차단 모달의 서버측 짝**(§4.4)은 이미 존재했고
  (`authn.go`의 409 `credential_required`) 건드리지 않았다.

---

## 107. 온보딩 컨설턴트 — 설계 반영 (설계 문서만, 코드 변경 0줄)

사용자 지시: *"온보딩할 때, 온보딩을 도와주는 컨설턴트 에이전트를 도입하는게 맞을 것
같아. 따라서 omniroute (LLM 연동)이 1순위로 되고 프록시 성공하면 그 다음부터는 AI를
이용해서 온보딩까지 하는거지. 컨설턴트는 말그대로 컨설팅만하는거지."*

### 107.1 산출물

| 문서 | 무엇 |
|---|---|
| `30-onboarding-provisioning.md` | **O15·O16** 결정 2건, **§3.4** 신설(컨설턴트 권한 경계, 6개 소절), **0-6** 단계 신설 + **§1.0.3.1**(예외 논증), §1.3 스코프 정정 표시, §1.4 흐름도, §1.0.7 전환조건, §1.0.8 status 계약 |
| `40-console-ia.md` | **§6.1**(화자 라벨·확정 버튼 주체·UI가 못 하는 일), **§6.2**(스트리밍 의존과 0-6) |
| `60-identity-authn.md` | **§3.1.2** 신설 — genesis principal을 주체 표에 **세는** 자리, 그리고 O15가 §6.3-2·§21.4-3에 주는 효과 |
| 코드 | **0줄.** 이 절은 설계만이다 |

### 107.2 이것이 새 컴포넌트가 아니라는 것이 판단의 출발점이었다

"컨설턴트 에이전트를 도입한다"를 문자 그대로 새 컴포넌트로 읽으면 §1.1의 순환 문제를
다시 푸는 두 번째 메커니즘을 만들게 된다. 그런데 **그 자리는 이미 채워져 있다** —
§4.1 ④의 대화 주체 `bootstrap-master`가 정확히 그것이고, §1.3이 그 정체성(그래프 노드
아님·임시·genesis 라벨)을 이미 확정했다. 그래서 이 반영은 **새 주체를 만들지 않고 그
주체가 무엇으로 행동하는지를 확정**한다. 카파시 #2(요청을 푸는 최소한)와 #3(시킨 것만)의
적용이다.

### 107.3 ★ 사용자 지시의 핵심은 이름이 아니라 "컨설팅만"이었고, 그것이 실제 구멍을 닫았다

*"컨설턴트는 말그대로 컨설팅만하는거지"*를 권한으로 번역하면 **`POST /onboarding/commit`을
genesis 스코프에서 제거**하는 것이 된다. 그리고 이 지점을 확인하면서 드러난 사실:

**O2는 처음부터 *"LLM은 `OrgDefinitionDraft`까지만"*이라고 정해 두었는데, 그 경계가
권한 차원에서는 강제되지 않고 있었다.** 10 §8.5의 스코프 표(라인 1339)는
`POST /onboarding/draft`, **`POST /onboarding/commit`**, `GET /onboarding/status`,
카탈로그 읽기를 허용한다 — 즉 **LLM이 구동하는 컨테이너가 자기 초안을 스스로 확정하는
쓰기를 호출할 수 있었다.** O2는 관습이었고, 스코프는 그것을 표현 가능하게 두고 있었다.

이것이 이 반영을 "이름 붙이기"가 아니라 **보안 강화**로 만든다. 세 가지가 따라온다:

1. **D13 예외가 작아진다.** §1.3이 인정한 *"의도적이고 유일한 예외"*에 남는 것은 읽기와
   초안 검증뿐이고, 초안은 그래프에 살지 않는다(§8: 저장 위치는 채팅 트랜스크립트).
   **컨설턴트는 조직 그래프를 한 바이트도 바꿀 수 없다.**
2. **60 §6.3-2가 서술에서 메커니즘으로 바뀐다.** *"커밋의 행위자는 승인한 사람"*이
   지금까지는 `actorPartyId`에 무엇을 싣는지에 관한 약속이었고(§6.4의 신뢰 경계에 얹힌),
   그 주장을 만드는 주체가 LLM 컨테이너였다. 호출자가 사람 세션이면 행위자는
   `AUTHN_SESSION`에서 **파생**된다(H19/H28).
3. **60 §21.4-3이 자동 충족된다.** *"LLM이 제안했다는 사실이 위임 규칙을 면제하지
   않는다"*가 요구한 DG1~DG5 통과는, 2회차 커밋 호출자가 `org.admin` 사람 세션이면
   `effective(party)`로 정상 평가된다. 컨설턴트에게는 평가할 `:Party`가 없으므로 이전
   구조에서는 이 요구의 실현 방법이 열린 문제였다.

**W1~W5 무영향** — 스코프를 **좁히는** 변경이므로 다섯 겹 폐쇄조건 중 어느 것도 약해지지
않는다. 오히려 W3이 지키려던 성질("커밋 후에는 아무것도 못 쓴다")이 **커밋 전에도** 참이
된다. 구현상으로도 새로 만들 것이 없다: 1회차 커밋 호출자인 master-key 세션 경로는 §101의
`POST /api/onboarding/initial-admin`으로 **이미 존재하고 이미 세계상태 술어로 닫힌다.**

### 107.4 omniroute 1순위 — "선행조건"에서 "경로 그 자체"로

O11이 이미 *"0단계 통과 후에만 대화"*를 정했으므로 순서 자체는 새롭지 않다. 바뀌는 것은
**실패의 성질**이다: 컨설턴트가 온보딩의 주된 수단이 되면 게이트웨이는 편의가 아니라
경로이고, 40 §D가 그 대화를 **SSE 토큰 스트림**으로 규정한다. 그래서 0단계 완료조건에
**0-6(스트리밍 패스스루 검증)**을 신설했다.

**0-6은 §1.0.3의 원칙을 어긴다 — 자기 면제하지 않고 근거를 밝혔다(§1.0.3.1).** 그
문단은 *"이 단계에서 진짜 LLM 호출(비용 발생)을 하지 않는다"*고 정했고, 0-6은 실제
추론 1회를 요구한다. 예외를 좁게 뚫는 근거: §1.0.3이 미루기로 한 것(키 유효성)은
**나중에 드러나고 복구 가능한** 실패지만, 스트리밍이 안 되면 **다음 단계 자체가
불가능**하다 — 전환 조건을 검증하지 않으면 *"게이트웨이는 200인데 대화가 한 글자도 안
나오는"* 상태로 1단계에 진입하고, 관리자가 보는 것은 원인 없는 빈 화면이다. 비용은
`max_tokens: 1`로 묶는다(검증 대상이 내용이 아니라 전달 방식이므로 토큰 1개로 충분).

**부수 효과 — §1.0.3이 못 닫는다고 인정한 갭이 부분적으로 닫힌다.** 0-6은 실제 추론이므로
프로바이더 키가 무효면 **여기서** 드러난다. 과대주장하지 않는다: **"키가 완전히 유효하다"의
증명이 아니라 "키가 완전히 무효는 아니다"의 증명**이며, 모델별 접근권·레이트리밋·잔액은
여전히 나중에 드러날 수 있다.

### 107.5 `[미검증]` — 이 반영이 의존하는 단 하나의 사실

**omniroute가 SSE 스트리밍 패스스루를 지원하는가.** 2026-07-26 이래 감독 결정 대기
1번으로 열려 있던 항목이고, 이 반영이 그것을 **크리티컬 패스로 승격**한다(승격 자체가
목적의 일부다 — 미룰 수 없게 만드는 것). 미지원 시 세 대안을 §1.0.3.1에 적었고 **고르지
않았다**: (a) 스트리밍 포기(40 §D의 타이핑 체감 요구를 내려놓는다), (b) osiris-api가
omniroute를 우회해 직접 스트리밍(별칭·비용집계·키 소유권 구조를 깨므로 강하게 비권장),
(c) omniroute에 패스스루 추가. **실측 후 결정할 사항이다.**

### 107.6 감독 판단이 필요한 것

- **F14 `[신규]`** — `10-core-architecture.md` §8.5의 스코프 표(라인 1339)에서
  `POST /onboarding/commit`을 제거해야 한다. **10은 다른 워커 소유라 고치지 않았다** —
  30 §1.3에 정정 표시만 남겼다. 이 한 줄이 O15의 **유일한 강제 지점**이므로, 10이
  갱신되지 않으면 설계는 자기모순 상태로 남는다(30은 "제거됨", 10은 "허용")이며
  구현자가 10을 근거로 삼으면 구멍이 그대로 열린다. **F13(§106.6)보다 우선순위가 높다.**
- **재온보딩(2회차 이후)을 컨설턴트가 맡는가, 진짜 `agent-master`가 맡는가** —
  §3.4.6이 두 방향과 각각의 대가를 적었다. **(b) 진짜 Master**가 §1.4의 기존 흐름과
  이미 정합하고(커밋 후 `bootstrap-master`는 폐기된다) `:Party`이므로 D13 예외가 전혀
  필요 없다 — 즉 **컨설턴트는 1회차 전용일 수 있다.** 이 문서는 고르지 않았다.
- **컨설턴트의 시스템 프롬프트/페르소나를 누가 소유하는가** — `:Role`이 없으므로
  §4.1의 소유권 표가 적용되지 않는다. 이미지에 구운 정적 자산이어야 하며, 그 파일의
  위치와 소유자가 아직 어디에도 없다(§3.4.6).
- **F9·F10·F12·F13, F1~F3, F6, F8** — 그대로 열려 있다.

### 107.7 이 절이 하지 않은 것

- **코드를 한 줄도 고치지 않았다.** genesis 토큰 자체가 아직 구현되지 않았고
  (`CORE_API_TOKEN`이 그 자리를 임시로 메우고 있다 — §9 부근), `POST /onboarding/draft`
  /`/commit`도 core에 없다. 스코프를 좁히는 변경은 **그 엔드포인트를 만드는 워커가
  구현할 때 지켜야 하는 계약**이며, 지금 고칠 코드가 존재하지 않는다.
- **`20-ontology-schema.md`를 건드리지 않았다.** 온톨로지에 새 라벨·관계·불변식이
  하나도 필요하지 않다(컨설턴트는 그래프 밖이다).
- **린트를 추가하지 않았다.** 강제 대상(고정 스코프 목록)이 아직 코드에 없어서
  *"빈 집합을 검사하는 린트는 잘못된 이유로 통과한다"*(린트 파일 헤더의 원칙)에
  해당한다. 스코프가 구현되는 시점에 *"고정 스코프에 commit이 없다"*를 검사하는 항목이
  들어가야 하며, 그것을 F14와 함께 인계한다.

---

## 108. omniroute SSE 패스스루 실측 — 감독 결정 대기 1번 해소 (코드 변경 0줄)

2026-07-26 이래 열려 있던 `[미검증]`: *"omniroute가 SSE 스트리밍 패스스루를 지원하는가 —
미지원 시 Admin Chat 타이핑 효과 불가."* §107이 이것을 크리티컬 패스로 승격했고, 이 절이
실측으로 닫는다. **결론: 지원한다. 우회 설계 3안(스트리밍 포기 / omniroute 우회 /
패스스루 추가)은 전부 불필요하다.**

### 108.1 두 개의 독립 증거

**(1) 소스 — 벤더 이미지가 전체 소스를 담고 있다는 사실을 활용했다.**
`omniroute-build/Dockerfile`이 이미 그 사실 위에 서 있다(*"벤더의 다이제스트 고정 이미지
자체에 전체 소스 트리가 들어 있다"*). 그 트리의
`open-sse/handlers/chatCore/streamingPipeline.ts`:

```
providerResponse → transformStream → [PII] → [progress] → SSE heartbeat → [modelEcho] → client
                 전부 pipeThrough (증분 TransformStream), 본문을 모으는 지점 없음
```

버퍼링일 수 없는 근거 셋: 전 단계가 `pipeThrough`다 · **`createSseHeartbeatTransform`이
있다**(하트비트는 열려 있는 스트림 전용이므로 버퍼링 파이프라인엔 의미가 없다) ·
`pipeWithDisconnect`가 클라이언트 중단을 업스트림으로 전파한다. 서브시스템 이름 자체가
`open-sse`다.

**(2) 실측 — 결정적.** omniroute 자신의 네이티브 SSE 엔드포인트를 `osiris-gw` 경유로
구독해 프레임 도착 시각을 기록했다: **15ms / 5,016ms / 10,019ms / 15,016ms(`: heartbeat`)
/ 15,019ms / 20,019ms.** `HTTP/1.1 200`, `Content-Type: text/event-stream`, 첫→끝 분산
20,003ms. 버퍼링이면 전부 종료 시점에 몰려 왔어야 한다. **소스에서 찾은 하트비트
트랜스폼이 런타임에 실제로 관측되었다** — 두 증거가 서로를 확인한다.

**부수 확인: `osiris-gw`도 버퍼링하지 않는다.** 경로가
`클라이언트 → gw(nginx) → osiris-omniroute`이므로 nginx가 모았다면 같은 결과가 불가능하다.
설정도 대조했다 — `location /omni/`와 `location /_api/`가 둘 다 `proxy_buffering off` ·
`proxy_cache off` · `proxy_read_timeout 3600s` · `proxy_http_version 1.1`. `/_api/` 쪽은
§104가 이미 `/api/events`로 별도 실측했으므로, 이제 **두 location 모두 실측 근거를 갖는다.**

### 108.2 ★ 무엇을 증명하지 **못했는가** — 과대주장하지 않는다

위 측정은 **omniroute가 만든 스트림**을 릴레이하는 것이고, **프로바이더에서 받아 넘기는
스트림**은 아니다. 그 구간을 합성 프로바이더로 재현하려 했고 **실패했다**:

1. `test/mock-gateway`(Anthropic Messages API + 청크별 flush, 2초 간격 3청크)를
   `osiris_osiris-llm`에 띄웠다.
2. omniroute의 `anthropic-compatible-` 접두 규약을 이용해 provider-node를 만들고
   (`baseUrl` → 목), 커넥션과 임시 별칭 `osiris/mockstream`을 붙였다.
3. **막힌 지점**: 요청이 `ALL_ACCOUNTS_INACTIVE`(503)로 떨어졌고, `recordedAttempts === 0`
   — 즉 **후보 계정이 0개**였다. `/api/provider-models`가 빈 배열이고
   `/api/synced-available-models`가 `{}`다. 합성 노드의 모델이 omniroute의 모델 동기화
   테이블에 올라가지 않았고, 문서화된 API로 동기화를 트리거할 방법을 찾지 못했다
   (`/api/models/sync`·`/api/model-sync` 404, `/api/models/test`는 실제 Anthropic으로
   나가 `[401] invalid x-api-key`를 받았다 — 프로바이더 키가 가짜이기 때문).

**따라서 이 마지막 홉은 실제 프로바이더 키 없이는 측정할 수 없다.** 그리고 그것이 정확히
**0-6이 하는 일**이다(30 §1.0.3.3) — 설계 결정이 아니라 설치 시 자동 확인 항목으로
옮겨졌다.

**남은 위험이 낮은 추가 근거**: `open-sse/handlers/chatCore/jsonBodyToSse.ts`가 존재한다 —
업스트림이 스트리밍하지 않으면 omniroute가 JSON을 SSE로 변환해 내보낸다. **클라이언트가
받는 것은 어떤 경우에도 `text/event-stream`**이며, 최악의 경우 잃는 것은 토큰 단위
체감뿐이고 프로토콜은 깨지지 않는다.

### 108.3 ★ 부수 발견 — omniroute는 기본값으로 **사설/내부 프로바이더 URL을 허용한다**

측정 과정에서 `http://osiris-mockgw:8080/v1`을 provider-node의 `baseUrl`로 **그대로 등록할
수 있었다.** 소스를 확인한 결과 의도된 기본값이다:

- `arePrivateProviderUrlsAllowed()`는 기본 **false**
- 그러나 `areLocalProviderUrlsAllowed()`는 주석 그대로 *"#5066 ... **Defaults ON**
  (OmniRoute is local-first — local OpenAI-compatible providers should validate out of the
  box)"*
- 따라서 `getProviderValidationGuard()`는 **`block-metadata`** 를 반환한다 — 클라우드
  메타데이터 엔드포인트만 막고 **그 외 내부 호스트는 전부 허용**한다.

**OSiris 아키텍처에 이것이 의미하는 바**: omniroute의 아웃바운드는 `osiris-egress`(squid
허용목록)를 지나도록 설계되어 있는데(실측 중 `proxyEnabled:true`인 커넥션이 내부 호스트로
가려다 502가 난 것이 그 증거다), **provider-node에 내부 URL을 넣고 `proxyEnabled:false`로
두면 egress를 통째로 우회해 omniroute가 컨트롤 플레인 내부로 임의의 HTTP 요청을 보낼 수
있다.** 이 조작에는 omniroute manage 키가 필요하므로(그 키 보유자는 이미 상당한 권한을
가진다) 즉각적 위험 등급은 중간이지만, **egress 허용목록이 omniroute에 대해서는 강제가
아니라 관례라는 사실**은 설계에 기록되어야 한다.

**F15 `[신규]`로 감독에게 올린다** — 조치 후보: (a) `OMNIROUTE_ALLOW_LOCAL_PROVIDER_URLS=false`
를 compose에 명시해 기본값을 뒤집는다(가장 값싸고 OSiris에는 부작용이 없다 — OSiris는
로컬 OpenAI 호환 프로바이더를 쓰지 않는다), (b) provider-node 생성을 관리 표면에서 막는다,
(c) 네트워크 층에서 강제한다(omniroute를 `osiris-llm`에서 떼고 egress만 남긴다 — 가장
강하지만 관리 UI 접근 경로를 함께 설계해야 한다). **이 문서는 고르지 않았다.**

### 108.4 정리 — 실측이 남긴 것과 되돌린 것

**전부 되돌렸다**(측정 전후 상태 동일 확인):

| 만든 것 | 정리 |
|---|---|
| 임시 별칭 `osiris/mockstream` | DELETE 200 |
| 임시 커넥션 `osiris-mockstream` | DELETE 200 |
| 임시 provider-node 2개(측정용 + 원복 검증 프로브) | DELETE 200 ×2 |
| `osiris-mockgw` 컨테이너 | `docker rm -f` |

**최종 상태**: 프로바이더 `['osiris-anthropic']`, 별칭
`['osiris/worker','osiris/planner','osiris/cheap']`, provider-node **0개**, 목 컨테이너
**0개**, osiris 컨테이너 **8개 전부 healthy**. **레포 파일 변경 0건**(`test/mock-gateway`
무수정 — 있는 이미지를 그대로 썼다). **프로덕션 무영향** — 사용자의 `omniroute` 컨테이너
RestartCount **0**, running 유지.

**설정 API에 대한 정직한 기록**: 측정 중 `PUT /api/settings`로
`OMNIROUTE_ALLOW_PRIVATE_PROVIDER_URLS`를 켜려 시도했고, 이후 원복을 위해 다시 false로
PUT했다. **두 호출 모두 무효(no-op)였다** — 그 엔드포인트는 알려지지 않은 키를 저장하지
않으며(`GET /api/settings`에 해당 키가 나타나지 않는다), 컨테이너 env에도 없다. 즉
**사설 URL 허용은 이 측정이 만든 상태가 아니라 원래의 기본값이었고**(§108.3), 보안 태세를
바꾼 것도 되돌릴 것도 없다. 원복이 안 된 것처럼 보였던 첫 검증(프로브 노드가 다시 생성됨)의
원인이 이것이다.

---

## 109. F14 해소 + genesis principal 구현 (설계 1행 + 코드)

### 109.1 F14 — 설계 자기모순을 닫았다

`10-core-architecture.md` §8.5의 스코프 표(라인 1339)에서 **`POST /onboarding/commit`을
제거**하고, 그 근거·W1~W5 재검증·구현 계약을 **§8.5.1**(4차 정정)로 신설했다. 30 §1.3의
정정 표시도 "F14 해소"로 갱신했다. **이제 30과 10이 일치한다** — §107.6이 경고한
*"30은 제거됨, 10은 허용"* 상태가 사라졌다.

§8.5.1이 명문화한 구현 계약 한 줄이 이 절의 코드가 지키는 것이다: *"genesis 스코프
검사는 허용목록(allowlist)이어야 하며 commit은 그 목록에 없다. 금지 목록이 아니라 허용
목록인 이유는 새 엔드포인트가 추가될 때 기본이 거부여야 하기 때문이다."*

### 109.2 산출물

| 파일 | 무엇 |
|---|---|
| `core/internal/genesis/genesis.go` (신설) | `AllowedRoutes`(3개) · `Allows()` 기본거부 · `osg_` 접두 · TTL 24h(W2) · Issue/Verify/Revoke, sha256만 저장 |
| `core/internal/onboarding/draft.go` (신설) | `OrgDefinitionDraft` 형태(§3.2) + §3.3 4개 검사 — **그래프 접근 0줄** |
| `core/internal/graphdb/catalogread.go` (신설) | 카탈로그 읽기 전용 스냅샷 + `OrgSummary`(카운트만) |
| `core/internal/graphdb/writeops.go` | `AllowedModelAliases()`/`IsAllowedModelAlias()` 노출 — I10 상수를 한 곳에서만 소유 |
| `core/internal/api/onboarding.go` (신설) | `genesisAuth` 미들웨어(인증+허용목록) + 5개 핸들러 |
| `core/internal/api/server.go` · `cmd/osiris-core/main.go` | 라우트 등록 + 스토어 배선 |
| `bin/lint-identity-authn.sh` | **`GEN` 검사 신설**(6개 하위검사) |
| `core/internal/{genesis,onboarding}/*_test.go` (신설) | **23개 테스트** — 이 레포 최초의 Go 테스트 |

### 109.3 ★ 두 토큰의 분리가 보안 성질이다

| 라우트 | 인증 | 왜 |
|---|---|---|
| `POST /v1/onboarding/genesis-token` | **플랫폼 토큰** | 발급 |
| `POST /v1/onboarding/genesis-token/revoke` | **플랫폼 토큰** | W3 |
| `POST /v1/onboarding/draft` | genesis 토큰 | 초안 검증 |
| `GET /v1/onboarding/status` | genesis 토큰 | 진행 상황 |
| `GET /v1/onboarding/catalog` | genesis 토큰 | O3의 선택지 |

발급·폐기를 **genesis 토큰에 두지 않은 것**이 핵심이다 — 자기 자격증명을 스스로 발급할
수 있는 주체에게는 폐쇄 조건이 아예 없고 W1~W5가 장식이 된다. §8.5의 논거를 그대로
따른다: **오직 orchestrator만 `docker.sock`을 가져(G8) "방금 만든 컨테이너의 진짜 ID"를
주장할 수 있다.**

**`POST /v1/onboarding/commit`은 이 mux에 아예 존재하지 않는다.** 허용목록이 기본거부라
등록해도 403이 되지만, 더 분명한 진술은 **라우트가 없다**는 것이다. 커밋은 30 §3.4.3대로
승인한 사람의 세션이 osiris-api를 통해 기존 플랫폼 토큰 쓰기 경로로 수행한다.

### 109.4 판단 3건 (근거를 남긴다)

**(1) 토큰 저장을 인메모리로 했다.** 대안 둘을 기각했다 — **Neo4j**: 이것은 자격증명이고
H1의 논거(그래프는 조직 *의도*이며 비밀은 의도가 아니다)가 그대로 적용된다. **JetStream
KV**: 내구성은 얻지만 core의 현재 NATS 권한은 `org.revision`/`org.event.>` 발행 전용이며
(orchestrator의 natsauth), **수명이 짧고 재발급 가능한 값 하나를 위해 서비스의 NATS 표면을
넓히는** 것은 비례하지 않는다. **대가를 명시한다**: core 재시작 시 진행 중이던 토큰이
무효가 되고 `bootstrap-master`는 죽은 자격증명을 들고 있게 된다 — §8.5의 W2 주석이 이미
그 해법을 *"같은 컨테이너 ID에 대한 재발급으로 해결되는 정상 운영 상황"*으로 규정했으므로
`Issue`를 컨테이너 ID 단위로 멱등하게 두고 orchestrator가 다시 요청하면 된다.

**(2) 레코드를 map이 아니라 단일 값으로 뒀다.** 30 §1.3이 `bootstrap-master`를 한 번에
하나만 허용하므로, map은 **두 번째 genesis 주체가 조용히 존재하는 것**을 허용해 버린다.
새 컨테이너 ID로 발급하면 이전 레코드가 교체되고, 그것이 revoke가 누락됐을 때의 정리
경로도 된다.

**(3) 초안 검증에서 그래프 내 참조는 확인하지 않는다.** 드래프트 *안에서* 해결되지 않는
참조만 보고하며, 문구도 *"이미 조직에 있으면 괜찮고 커밋이 해결한다"*로 적었다. 이
함수는 그래프를 읽지 않으므로 오타와 기존 팀 참조를 **구별할 수 없고**, 구별할 수 없는
것을 오류로 단정하면 정상 드래프트가 거부된다.

### 109.5 실측 (러닝 osiris-core, 7항)

| # | 조건 | 결과 |
|---|---|---|
| 1 | 토큰 없이 `POST /v1/onboarding/draft` | **401** |
| 2 | 위조 `osg_` 토큰 | **401** |
| 3 | **플랫폼 토큰으로 genesis 경로 시도** | **401** — 두 스킴이 서로를 대체하지 못한다 |
| 4 | ★ **W1**: 조직 존재(rev 40) 상태에서 발급 요청 | **409** *"an organization already exists; the genesis window is closed"* |
| 5 | `containerId` 없이 발급 (W4) | **400** |
| 6 | 플랫폼 토큰 없이 발급 경로 | **401** |
| 7 | revoke 멱등 | **200** `{"revoked":false}` |

### 109.6 ★ 측정할 수 없었던 것과 그것을 무엇으로 덮었는가

**행복 경로(발급 → 허용 라우트 사용 → 나머지 거부 → 만료)는 이 배포에서 측정 불가다.**
W1이 `currentOrgRevision == 0`을 요구하는데 이 dev 그래프는 쓰기 경로 시험으로 **rev 40**
이고, 조직을 한 번이라도 커밋한 배포는 영구히 그 지점을 지나 있다.

그래서 **그 부분은 순수 로직 테스트로 덮었다** — 이 레포 최초의 Go 테스트 23개
(`genesis` 12, `onboarding` 11). 근거: 측정 불가한 대상이 전부 순수 함수 또는 시계 주입
가능 코드라서, 그래프나 HTTP 없이 **정확히 그 행동**을 검증할 수 있다.

테스트가 실제로 무엇을 잡는지 **뮤테이션으로 확인했다**: 허용목록에 `commit`을 넣었더니
`TestAllowsIsDefaultDeny`와 `TestCommitIsNotInTheAllowlist` **두 개가 실패**했다 —
즉 F14는 이제 세 겹으로 지켜진다(설계 문장 · 린트 `GEN(b)` · 테스트 2개).

덮은 항목: 기본거부, 메서드 민감성, `commit` 부재, W4(containerID 필수), W2(TTL 경계 ±1초
+ 만료 레코드 삭제), W3(revoke 멱등·종단), 단일 레코드 교체, 지문이 토큰의 부분문자열이
아님, §3.3의 4개 검사, `delete` op 거부(P4), close 시 카탈로그 검사 생략, :Person에
modelAlias 미요구, 문제 목록 결정성.

### 109.7 `GEN` 린트 — 6개 하위검사, 주입 6/6 탐지

(a) 허용목록 비어있지 않음 · (b) **`commit` 부재** · (c) `Allows`가 `return false`로
낙하(기본거부) · (d) `internal/onboarding`에 그래프 접근 0건 · (e) 라우터에 commit 라우트
없음 + 3개 경로가 실제로 `genesisAuth`에 등록됨 · (f) 발급·폐기가 플랫폼 토큰에 있음.

**GEN(a)는 초판이 자기 주입 시험에 실패했고, 그래서 다시 썼다.** 초판은
`awk '/AllowedRoutes = \[\]Route\{/,/^\}/'` 범위로 항목을 셌는데, 리터럴을 한 줄
(`[]Route{}`)로 접으면 **범위에 종결자가 없어 파일 끝까지 삼키고** 구조체 정의의 `Path:`를
세어 통과했다. **EOF까지 조용히 흐를 수 있는 범위는 "목록이 비어있지 않다"를 주장할 도구가
아니다.** 항목의 *형태*(`Method: http.MethodX, Path: "…"`)를 직접 매칭하도록 고친 뒤
한 줄·여러 줄 두 형태 모두 탐지했다. §106.8의 H10(e)와 같은 교훈이 두 번째로 나왔다 —
**린트는 자기 주입 시험을 통과해야 비로소 린트다.**

### 109.8 회귀 확인

린트 4종 **전부 PASS**(`lint-identity-authn`은 이제 12개 불변식군), `verify-isolation`
**15/15**, `go build ./...` 3모듈 통과, `gofmt` clean, 컨테이너 8개 healthy, 프로덕션
`omniroute` RestartCount **0**.

### 109.9 상한에 걸려 멈춘 것 / 다음 워커에게

- **G1~G10 · A1~A13 · D1~D20 · W1~W5 · I1~I17 · C1~C10 — 수정·완화·번복 없음.**
  이 작업은 스코프를 **좁히는** 방향이며 W3의 성질을 커밋 전으로 확장한다.
- **`.env` 신규 변수 0건.** genesis 토큰은 발급 시점에만 존재하고 파일 주입은
  orchestrator의 몫이다(§8.5 6단계) — **이 절은 orchestrator 쪽을 구현하지 않았다.**
- **다음 단계(orchestrator)**: §8.5의 1~7단계 중 core 몫(4~5)만 끝났다. 남은 것은
  `ContainerCreate` → ID 확보 → 이 엔드포인트 호출 → `genesis_token`(0400) 파일 주입 →
  `ContainerStart` 시퀀스, 그리고 커밋 후 `revoke` + drain/delete(30 §1.5).
- **`POST /onboarding/commit` 자체를 만들지 않았다.** 커밋 호출자는 사람의 세션이며
  (30 §3.4.3) 그 경로는 osiris-api가 기존 플랫폼 토큰 쓰기 라우트로 수행한다. 1회차
  경로는 §101의 `POST /api/onboarding/initial-admin`으로 이미 존재한다. 드래프트 전체를
  단일 트랜잭션으로 커밋하는 §5.2의 15단계 시퀀스는 별개의 큰 작업이다.
- **F15**(omniroute 사설 URL 기본 허용, §108.3)는 손대지 않았다.

---

## 110. orchestrator — §8.5 시퀀스 완성 (steps 2·3·6·7) + §1.5 폐기

§109가 core 몫(steps 4~5)을 끝냈고, 이 절이 나머지를 붙여 **§8.5의 일곱 단계가 실제로
한 줄로 이어진다.**

### 110.1 산출물

| 파일 | 무엇 |
|---|---|
| `orchestrator/internal/driver/docker/genesis.go` (신설) | `CreateGenesisContainer`(생성만) · `WriteGenesisToken`(0400) · `StartGenesisContainer` · `TeardownGenesisContainer` · `labelGenesis` |
| `orchestrator/internal/genesisboot/genesisboot.go` (신설) | 시퀀스 오케스트레이션 + `CoreIssuer`(HTTP 클라이언트) + 단일비행 + Phase 상태 |
| `orchestrator/internal/httpapi/bootstrapmaster.go` (신설) | `POST`/`DELETE /bootstrap-master`, 플랫폼 토큰 게이트, **202 비동기** |
| `orchestrator/internal/config/config.go` | `CoreBaseURL`·`CoreAPIToken`·`BootstrapMasterImage` |
| `orchestrator/cmd/osiris-orchestrator/main.go` | 배선 + 드라이버 어댑터 + `/status` 합성 |
| `docker-compose.yml` | orchestrator에 `CORE_BASE_URL`·`CORE_API_TOKEN` (**신규 `.env` 변수 0건**) |
| `genesisboot_test.go` (신설) | **10개 테스트** — 순서 보장이 여기서만 검증 가능하다 |

### 110.2 ★ 순서가 보안 성질이므로 순서가 API다

```
2. 0700 creds 디렉토리 준비 + ContainerCreate만       ← 시작하지 않는다
3. 실제 컨테이너 ID 확보
4. core에 그 ID를 실어 발급 요청
5. core가 rev==0 확인 후 발급+바인딩(원자적)
6. 토큰 파일 기록(0400) — 컨테이너 여전히 미기동
7. ContainerStart
```

`Bring()`은 이 일곱 단계를 **직선으로** 쓴다. 그리고 **컨테이너를 시작하는 exported 함수가
`genesisboot`에 없다** — 호출자가 실수로 토큰보다 먼저 시작할 수 있는 경로 자체를 없앴다.

**실패 경로도 순서의 일부다**: 발급 실패 → 생성된 컨테이너 철거(토큰 없는 컨테이너는
쓸모없고 이름이 다음 시도를 막는다) · 주입 실패 → **토큰 revoke 후** 철거(core에는 살아
있는데 아무도 못 쥔 자격증명을 남기지 않는다) · 시작 실패 → 동일.

**`Teardown`은 revoke를 먼저 하고 컨테이너를 나중에 지운다 — 직관의 반대다.** 컨테이너를
먼저 지우면 revoke가 도는 동안 *바인딩된 컨테이너가 사라진 살아있는 토큰*이 존재하고,
core의 바인딩 검사는 기록된 ID와 비교하므로 그것을 **알아챌 수 없다.** revoke를 먼저 하면
자격증명이 보유자보다 먼저 죽으므로 "유출된 토큰이 살아있으면서 귀속 불가"인 순간이 아예
없다.

### 110.3 ★ 설계 문서와 어긋난 판단 1건 — 토큰 파일 경로 (F16)

§8.5 표는 토큰이 호스트 `state/creds/<containerID>/genesis_token`에 있다고 적는다.
**그 경로는 §8.5 자신의 단계 순서에서 구현 불가능하다:**

- bind 마운트의 source는 `ContainerCreate` **요청**에 박힌다(2단계).
- 컨테이너 ID는 `ContainerCreate` **응답**에서만 알 수 있다(3단계).
- 따라서 ID를 포함한 마운트 source를 **선언해야 하는 시점에 선언할 수 없다.**
- Docker가 없는 bind source를 자동 생성하게 두는 것으로도 구제되지 않는다 — 엔진이
  root 소유로 만들어 0700 디렉토리와 파일을 읽어야 하는 agent uid가 둘 다 깨진다.

그래서 디렉토리를 **컨테이너의 안정적 이름**으로 키한다(`state/creds/bootstrap-master/`).
**W4는 무영향이다**: W4가 요구하는 바인딩은 core가 토큰과 컨테이너 ID 사이에 기록하는
것(`genesis.Store.Issue`)이지 파일이 놓인 경로가 아니다. 경로는 주입 세부이고 바인딩은
인가 사실이다. **F16으로 감독에게 올린다.**

### 110.4 판단 2건

**(1) `labelEmployeeID`를 붙이지 않았다.** `Observe()`가 그 라벨로 관리 대상 에이전트를
식별하므로, 붙이면 **수렴 루프가 genesis 컨테이너를 고아 에이전트로 보고 삭제한다.**
30 §1.3(그래프 노드가 아니다)과 §1.5(폐기는 명시적으로 수행한다)가 같은 것을 말하고 있고,
라벨 하나가 그 둘을 동시에 깬다. `osiris.dev/genesis=true`만 붙인다.

**(2) 트리거 핸들러가 Docker를 인라인 호출하지 않는다.** `internal/httpapi`의 패키지
주석은 *"handlers only ever read atomic/mutex-guarded state ... never opening a Neo4j
session or calling the Docker API inline from a request handler"*를 규율로 세워 두었다.
그래서 핸들러는 시퀀스를 **고루틴으로 띄우고 202를 반환**하며, 결과는 `/status`가 보고한다.
이것은 규율의 우회가 아니라 규율의 목적 그 자체다 — 동기 핸들러였다면 요청 하나가
`ContainerCreate` + core 왕복 + 파일 쓰기 + `ContainerStart`를 붙잡고, 느린 Docker 데몬이
orchestrator 자신의 헬스 표면을 온보딩 뒤에 줄 세운다.

**컨텍스트는 요청에서 분리한다** — HTTP 호출자가 끊었다고 시퀀스가 중간에 취소되면
`ContainerCreate`와 토큰 쓰기 사이에서 버려진 상태가 되고, 그것이 정확히 §8.5가 없애려는
상태다.

### 110.5 ★ /status 계약 불일치를 스스로 만들었고 고쳤다

첫 배포에서 `POST /bootstrap-master`의 202 응답이 *"progress is reported on GET /status
under bootstrapMaster"*라고 말하는데 **`/status`에 그 필드가 없었다.** 자기 응답이 약속한
것이 존재하지 않는 상태였다. `statusSnapshot` 합성기로 `{reconcile, bootstrapMaster}`를
함께 보고하도록 고쳤다. 키 이름이 두 곳(응답 문구와 합성기)에 있으므로 조용히 어긋나지
않게 서로를 참조하도록 주석에 명시했다.

### 110.6 실측 — 실제 배포에서 steps 2~4가 돌았다

| # | 조건 | 결과 |
|---|---|---|
| 1 | 무인증 `POST /bootstrap-master` | **401** |
| 2 | 틀린 토큰 | **401** |
| 3 | `GET`(미지원 메서드) | **405** |
| 4 | 인증된 `POST` | **202** (비동기, phase 동봉) |
| 5 | ★ **steps 2~3 실제 수행** | 로그: *"created bootstrap-master container acef8dd2daf2 (not started — the token comes first, §8.5)"* |
| 6 | ★ **step 4 → W1 거부** | 로그: *"failed at issue: an organization already exists; the genesis window is closed (W1)"* — core의 409가 여기까지 전달됨 |
| 7 | ★ 실패 후 정리 | 잔여 컨테이너 **0개** |
| 8 | `/status`가 진행 보고 | `{"phase":"failed","containerId":"c0782acc…","lastError":"issue: …(W1)"}` |

**이것은 §109의 실측보다 강하다** — §109는 core의 거부를 curl로 봤고, 여기서는
**orchestrator가 실제로 컨테이너를 만들고, core에 물어보고, 거부당하고, 스스로 정리하는
전 과정**이 돌았다. 남은 미측정 구간은 오직 "core가 승낙하는 경우"(rev==0)뿐이다.

### 110.7 순서 보장은 테스트로만 검증 가능하다 — 그리고 뮤테이션으로 확인했다

행복 경로는 W1 때문에 이 배포에서 영구히 도달 불가다(rev 40). 그래서 `genesisboot`의
`ContainerDriver`/`Issuer`를 **인터페이스로 두고** 호출 순서를 기록하는 fake로 검증했다 —
그것이 이 인터페이스가 존재하는 유일한 이유다.

**뮤테이션 확인**: `Bring()`에서 토큰 쓰기 **전에** `StartGenesisContainer`를 호출하도록
바꾸자 **3개 테스트가 실패**했다(`TestBringOrderIsCreateIssueWriteStart` 포함, 실패 메시지:
*"the container was started while the token file did not exist — §8.5's window is not zero"*).
즉 §8.5의 "창이 0" 논증에 **기계적 방어가 생겼다.**

테스트 10개가 덮는 것: 순서(create→issue→write→start) · 시작 시점에 토큰 파일 존재 ·
W4(발급에 넘긴 ID = 생성된 ID = 시작한 ID) · 발급 실패 시 미기동+철거 · W1의 terminal
식별(`errors.Is`) · 주입 실패 시 revoke · 시작 실패 시 revoke+철거 · **Teardown이 revoke를
먼저** · revoke 실패에도 컨테이너 제거 · 단일비행 · 스냅샷에 자격증명 없음.

### 110.8 회귀

린트 4종 PASS(12개 불변식군) · `verify-isolation` **15/15** · 3모듈 빌드·gofmt clean ·
`go test` **33개**(core 23 + orchestrator 10) 통과 · 컨테이너 8개 healthy · 프로덕션
`omniroute` RestartCount **0**.

### 110.9 남은 것 / 다음 워커에게

- **`POST /onboarding/commit` 미구현** — §109.9와 동일. 커밋 호출자는 사람의 세션이며
  드래프트 전체를 단일 트랜잭션으로 커밋하는 §5.2의 15단계는 별개의 큰 작업이다.
- **osiris-api → orchestrator 트리거 호출 미구현** — 0단계 통과 감지 후
  `POST /bootstrap-master`를 부르는 쪽(§1.0.7 step 1)은 api 소유다. 엔드포인트는 준비됐다.
- **`bootstrap-master` 컨테이너가 실제로 무엇을 실행하는가** — 이미지는 `osiris/agent:dev`
  를 쓰지만, 그 안의 supervisor가 genesis 토큰으로 `/v1/onboarding/draft`를 부르며 대화를
  진행하는 로직은 없다. 그리고 §3.4.6이 남긴 갭 그대로 **컨설턴트의 시스템 프롬프트
  소유자·위치가 아직 어디에도 없다.**
- **F16 `[신규]`** — §8.5 표의 토큰 파일 경로를 `state/creds/<containerID>/`에서
  이름 기반으로 정정 필요(§110.3). **F14와 달리 이것은 보안 성질이 아니라 서술 정확성
  문제다** — W4는 이미 지켜지고 있다.
- **F13·F15, F9·F10·F12, F1~F3·F6·F8** — 그대로 열려 있다.

---

## 111. k8s 전면 전환 확정 — Phase A(설계 문서 반영) 완료

사용자와의 인터뷰로 배포 아키텍처를 **k8s 단일 대상**으로 확정했다. 이 절은 그 결정 13개
(K1~K13)가 문서에 어떻게 반영됐는지와, 반영 과정에서 감독이 잡아낸 결함을 기록한다.

**이 절은 코드를 한 줄도 바꾸지 않았다.** Phase B(실측) → Phase C(Helm chart 구현)가 남아
있고, 사용자 지시대로 phase를 쪼개 진행한다.

### 111.1 인터뷰가 무엇을 바꿨는가 — §107의 검토 결론이 뒤집힌 부분

§107은 사용자가 제시한 k8s 다이어그램을 검토하며 **다중 테넌시(PaaS Tenant)** 를 "비용이
가장 큰 신규 요구사항"으로 지목했다. 인터뷰 결과 **그것은 요구사항이 아니었다** —
*"단일클러스터, 단일노드, 단일파드 형태로 구축되어지는거야. 내가 신경쓰는 것은 격리였거든"*.
즉 `PaaS Tenant`라는 라벨은 N개 고객사가 아니라 **격리 단위**를 뜻했고, §107이 가장 무겁다고
본 항목(신원 모델 전체 재설계)이 **사라졌다.**

**교훈을 남긴다**: §107은 다이어그램의 *용어*("PaaS Tenant")에서 요구사항을 역추론했다.
그 추론은 합리적이었으나 틀렸고, **한 번의 질문이 그 오해를 없앴다.** 다이어그램에서 의도를
읽을 때는 용어가 아니라 목적을 물어야 한다.

반대로 인터뷰가 **새로 만든** 문제도 있다. §107.5는 *"마이그레이션 경로만 유지한다면 genesis
문제는 2단계에 아예 존재하지 않는다"*고 정정했는데, 그 근거였던 `30 §7.2`의 네 번째 행
(*"bootstrap-master 불필요 — 2단계는 **항상** 1단계에서의 마이그레이션"*)이 **K2/K13으로
깨진다.** 마이그레이션이 없으면 최초 기동이 곧 빈 조직이므로 부트스트랩 역설이 k8s 안에서
다시 발생한다. K7이 그 답이다.

### 111.2 산출물

| 파일 | 무엇 |
|---|---|
| `docs/design/70-kubernetes-deployment.md` (신설, ~270줄) | K1~K13 결정표 + §1 왜 k8s인가 + §2 배치 + §3 네트워크 + §4 자격증명(W1~W5 대조표) + §5 CRD/operator + §6 이관·폐기 + §7 새 항목 + §8 미결 + §9 토폴로지 |
| `10-core-architecture.md` | 정정 태그 **12곳** — 1/2단계 이원 구조 소멸, G3 린트 대상 소멸, §6.2/§6.3, §2.6(예외 4→5 + NetworkPolicy는 기본 허용), §4.1(`osiris-up` 폐기), §8.5(저장·주입 행 + 7단계 시퀀스 + W1~W5→K7 매핑) |
| `30-onboarding-provisioning.md` | 정정 태그 **7곳** — §1.3(임시 컨테이너→임시 Pod), §1.0.7/§1.4(생성 시퀀스), §1.5(폐기 경로→ownerReference GC), **§7.2 표(가장 중요)** |
| 코드 | **0줄** |

### 111.3 ★ 감독이 잡아낸 결함 3건 (전부 수정)

**(1) 조작된 인용 — 가장 중요하다.** A1이 `60 §9.1`을 이렇게 인용부호에 넣었다:
*"0400 파일을 읽을 수 있는 사람은 이미 host-root-equivalent"*. **§9.1의 원문이 아니다.**
실제 원문은 *"**허용 대상**: 호스트에 물리적/관리적 접근이 있는 사람 = `docker`를 쓸 수 있는
사람 = **이미 전권자**(docker.sock은 호스트 root와 동등, G8의 전제)"* 이다. 취지는 같지만
문장이 다르다.

이 레포는 인용 정확성을 규율로 삼는다(§105.2가 "거짓 실패"를 린트에서 더 위험한 방향이라고
적은 것과 같은 이유다). **파생 표현을 원문처럼 인용하면 다음 사람이 그 문장을 근거로
삼는다.** 원문으로 교체하고 §4.4에 정정 이력을 남겼다. 부수 효과로 **대응이 더 깔끔해졌다** —
*`docker`를 쓸 수 있는 사람* → **`kubectl`을 쓸 수 있는 사람**, 한 단어만 바뀐다. 원 논거가
저장 매체가 아니라 **접근 권한의 포함관계**에 의존하도록 쓰여 있었기 때문이다.

**(2) 감독의 병렬 조정 실패 — 줄번호 인용이 썩었다.** A1(70 작성)과 A2(10/30 정정)를
동시에 띄웠는데, **A1이 인용하는 파일을 A2가 편집하고 있었다.** 결과: A1의 세 인용이 각각
**4줄·10줄·16줄 밀렸다**(감독 실측).

| 인용 | A1이 쓴 것 | 실제 |
|---|---|---|
| "기본 deny + 4개 예외" | `10:440` | 444 |
| CRD 값 한정 문장 | `10:1027` | 1037 |
| docker.sock 신뢰 근거 | `10:1361` | 1377 |

파일이 분리돼 있어 편집 충돌은 없었으나 **인용 충돌**을 예상하지 못했다. 세 인용을 §번호
(`§2.6`·`§6.1`·`§8.5`)로 바꾸고, 70의 표기 규칙에 *"인용은 줄번호가 아니라 §번호로 한다 —
줄번호는 인용 대상이 살아 있는 문서일 때 반드시 썩는다"*를 명문화했다. **다음에 워커를
병렬로 띄울 때는 "누가 무엇을 인용하고 누가 그 파일을 편집하는가"를 먼저 확인한다.**

**(3) 취소선이 정정 태그에 걸렸다.** A2가 `30 §7.2`에
`~~**[2026-07-28 K2 전환]**~~ **이 판정 전체가 뒤집힌다**`로 썼다 — **정정 자체가 취소된
것처럼 읽힌다.** 무효가 된 판정(`불필요`)에 취소선이 걸리도록 고쳤다:
`**[2026-07-28 K2 전환]** ~~불필요~~ → **필요.**`

### 111.4 K7이 Docker 설계보다 강한 지점 — 기록해 둘 가치가 있다

`10 §8.5`는 genesis 발급 인증의 근거를 이렇게 적었다: *"오직 `osiris-orchestrator`만
`docker.sock`을 가져 컨테이너를 실제로 만들 수 있고(G8) 따라서 '방금 만든 컨테이너의 진짜
ID'를 주장할 수 있는 유일한 발신자이기 때문이다."*

즉 **core가 orchestrator의 주장을 믿는** 구조였다 — §6.4가 인정한 그 신뢰 경계다. K7에서는
**K8s가 Pod 신원의 보증인**이 되므로 그 주장이 필요 없다. core는 TokenReview로 *"이 토큰은
`osiris-agents/bootstrap-master` SA의 것"* 을 API 서버에게서 직접 확인한다. §109/§110이 구현한
"발급=바인딩 원자성"이 **더 강한 형태로 대체된다** — 그리고 **OSiris가 만드는 비밀이 0개**가
된다.

### 111.5 다음 (Phase B → C)

**Phase B — 실측 2건.** 둘 다 70에 `[미검증]`으로 표시돼 있다:
- **K11**: k3s 기본 CNI에서 default-deny NetworkPolicy가 실제로 집행되는가. NetworkPolicy는
  기본이 **허용**이고 CNI가 지원하지 않으면 무시되므로, Docker `internal: true`의
  토폴로지적 차단과 동등하지 않다.
- **W3 대응**: ServiceAccount 삭제가 이미 발급된 bound token을 **즉시** 무효화하는가, 그리고
  버전별 의미론 차이가 있는가. W3(커밋 후 revoke)가 정확히 이 지점에 걸린다.

**Phase C — Helm chart 골격.** phase를 쪼개 순차 진행한다(사용자 지시).

**Phase A에서 손대지 않은 미결**: K12(노드 조인 신뢰 모델) · Ingress 컨트롤러 종류(K3의
annotation이 여기 종속) · `POST /onboarding/commit`(§5.2의 15단계 단일 트랜잭션 — 배포 방식과
무관하게 남은 최대 미구현 항목) · 컨설턴트 시스템 프롬프트의 소유자·위치(`30 §3.4.6`).

---

## 112. Phase B(실측) · C-1(chart 골격) · C-2(네트워크) — k8s 전환 구현 기록

§111이 Phase A(설계 문서 반영)를 기록했다. 이 절은 B·C-1·C-2를 기록한다. 사용자 지시대로
phase를 쪼개 진행했고, **각 phase가 끝날 때마다 감독이 산출물을 직접 검증**했다.

### 112.1 Phase B 실측 — 한 건 닫고 한 건 좁혔다

**닫힘 — W3(SA 삭제 → 토큰 무효화).** 공식 문서 원문으로 확정:

> *"If the referenced object or the ServiceAccount is pending deletion …, then for any instant that is 60 seconds (or more) after the `.metadata.deletionTimestamp` date, authentication with that token would fail. **If the referenced object no longer exists (or its `metadata.uid` does not match), the request will not be authenticated.**"*
> — kubernetes.io, Managing Service Accounts / Bound service account tokens

| 시나리오 | 판정 |
|---|---|
| SA 완전 삭제 | 즉시 인증 실패 |
| 같은 이름으로 SA 재생성 | 즉시 실패(`uid does not match`) — **"폐기했는데 부활"이 성립하지 않는다** |
| SA가 pending deletion(finalizer) | `deletionTimestamp` **+60초**부터 실패 |

**★ 여기서 W3 문구 정정이 나왔고, 그 정정이 새 비용을 만들지 않는다는 것이 중요하다.** W3의
"즉시"는 k8s에서 "최대 60초"가 된다. 그런데 `60-identity-authn.md` §7.4가 이미 이 시스템의
리보크 지연 상한을 **최대 60초**로 못박고 명시적 대가로 인정해 두었다(*"강제 로그아웃(리보크)이
최대 60초 지연된다"*). 즉 SA 삭제의 유예 창은 **설계가 이미 값을 치른 봉투 안쪽**이며 두 지연이
겹치는 것이 아니라 같은 상한을 공유한다 — §104.3이 SSE 워치독 주기를 30초로 정할 때
*"장수명 연결이 그 위에 두 번째·더 큰 지연을 얹으면 새 대가를 만드는 것"*이라며 60초 봉투
안쪽을 요구한 것과 **동일한 판단 기준**이다.

**좁혀짐 — K11(CNI).** k3s는 Flannel(NetworkPolicy 미지원)을 쓰지만 **kube-router netpol
컨트롤러를 내장해 기본 활성**하며 **egress도 강제**한다(A3가 egress 정책에 의존하므로 이것이
확인되어야 했다). 남은 두 가지 때문에 `[미검증]`을 유지한다: (a) k3s가 vendoring한 kube-router가
0.4.0 이전 포크이고 ingress/egress 상호작용 결함이 있었다(k3s#2390) — 현재도 그 버전인지
**확인 실패** (b) `default-deny egress + 5예외`의 실기 동작. 운영 함정 하나도 기록됐다:
*"컨트롤러를 비활성화해도 iptables 규칙은 제거되지 않는다"*.

**`[확인 실패]`로 남긴 것**: `BoundServiceAccountTokenVolume`의 정확한 GA 버전(KEP 표는 1.22,
통설은 1.21). **이 결정은 여기에 의존하지 않는다** — 어떤 최신 k3s도 두 값을 넘겼다.

### 112.2 Phase C-1 — chart 골격 + `osiris-system` 워크로드

`deploy/osiris/` 신설: `Chart.yaml`·`values.yaml`·`README.md` + 템플릿 11개(namespace ·
core/neo4j/nats/omniroute StatefulSet · api/orchestrator/egress/console Deployment ·
priorityclass · secrets). **`deploy/` 밖은 한 파일도 건드리지 않았다.**

감독이 직접 검증한 항목:

| 검사 | 결과 |
|---|---|
| `10 §1.2` 불변자산 10번 — `nats`·`osiris-core`·`osiris-egress` 고정 이름 | ✅ Helm의 release-prefix 관례를 **의도적으로** 벗어나고 근거를 주석에 남김 |
| K4 — `nodeAffinity`가 `core`·`neo4j`·`nats` **셋에만** | ✅ omniroute·egress에는 *왜 안 붙였는지*까지 주석 |
| Guaranteed QoS(`requests == limits`) + PriorityClass | ✅ — Phase B가 확인한 "단일 노드에서 taint는 자원을 예약하지 않는다"의 대체 수단 |
| 평문 시크릿 | ✅ 0건, `secretKeyRef` 20곳 |

**감독의 오탐 1건 — 기록해 둔다.** 검증 중 "omniroute·egress에 `nodeAffinity`가 붙어 70 §2와
어긋난다"고 보고했으나 **오탐이었다.** grep이 주석의 *"nodeAffinity 없음"* 문구를 잡았다.
주석을 제거하고 다시 세면 셋에만 정확히 걸려 있다. **주석을 스트립하지 않은 grep으로 코드 사실을
주장하면 안 된다** — 이 레포의 린트가 `strip_comments()`를 쓰는 이유가 정확히 이것이다(§105 이후
린트들이 전부 그 함수를 통과시킨다).

### 112.3 Phase C-2 — 네트워크

신설: `networkpolicy-agents.yaml`(default-deny + 5예외) · `networkpolicy-system.yaml`(컴포넌트별
allow 9개) · `ingress.yaml`(콘솔/`_api`/`omni`/catch-all 4개) · **`bin/verify-isolation-k8s.sh`**.
`values.yaml`에 `ingress.*` 블록 추가.

**§108의 SSE 실측이 annotation으로 옮겨졌다** — 이 phase에서 가장 중요한 이관이다:

| §108.1 실측 nginx 지시어 | ingress-nginx annotation |
|---|---|
| `proxy_buffering off` | `nginx.ingress.kubernetes.io/proxy-buffering: "off"` |
| `proxy_read_timeout 3600s` | `proxy-read-timeout: "3600"` |
| `proxy_cache off` | 기본값이나 `configuration-snippet`에 명시 반복 |
| `proxy_http_version 1.1` + `Connection ""` | 전용 annotation 없음 → `configuration-snippet` |

**컨트롤러 미결(70 §8)을 `{{ fail }}`로 처리한 것이 옳다**: `ingress.controller=traefik`을 주면
`helm template`이 **의도적으로 실패**한다. 미구현을 조용히 통과시키지 않는 이 레포의 규율
(§109.7의 "빈 집합을 검사하는 린트는 잘못된 이유로 통과한다")과 같은 방향이다.

**검증 — 감독이 직접 재실행**: `helm lint` **0 failed** · `helm template` **37개 오브젝트 정상
렌더**(NetworkPolicy 10 · Service 8 · StatefulSet 4 · Ingress 4 · Deployment 4 · Namespace 2 ·
ConfigMap 2 · Secret 1 · PriorityClass 1 · PVC 1) · `bash -n` 통과. **클러스터에는 아무것도
적용하지 않았다**(호스트에 프로덕션 컨테이너 26개).

**검사 개수를 정정했다.** 70 §3.4가 "15개 검사"라고 쓴 것은 *정의된 검사 수*가 아니라
*에이전트 없이 실행 가능한 판정 수*였다(실측: 15 실행, D1~D8은 *"SKIP: 실행 중인 실제 에이전트
컨테이너가 없습니다"*로 명시적 스킵). 70 §3.4에 그 구분을 명문화했다.

### 112.4 남은 TODO와 그 분류 (감독 판단)

**정상적인 phase 순서 — 조치 불필요**: squid source-IP ACL을 Pod CIDR로 옮길 수 없는 문제(C-2가
NetworkPolicy로 부분 대체) · NATS `authorization.conf` 동적 재작성(C-3) · orchestrator의
`DOCKER_SOCKET_PATH`/`HOST_PROJECT_DIR`(C-3의 `driver/kube`) — **후자 때문에 에이전트 생성은
C-3까지 비동작이다. 이것은 순서이지 결함이 아니나, "다 됐다"로 오독되면 안 된다.**

**실질 결함으로 판정한 것 2건**:
1. **`ENV_FILE_PATH`가 `emptyDir`에 들어갔다.** 온보딩 0단계가 게이트웨이 토큰을 `.env`에 되쓰는
   경로(`30 §1.0.3` 단계 0-4)인데 emptyDir이면 **재시작마다 사라진다.** Secret 업데이트로 바꿔야
   한다 → C-2b.
2. **콘솔 전용 nginx 이미지가 없다.** `gw/Dockerfile`이 nginx+콘솔+라우팅을 묶고 있어 K3(gw 폐지)의
   필연적 귀결로 이미지를 갈라야 한다 → C-2b.

**미결로 남는 것**: Ingress 컨트롤러 종류(K3의 annotation이 종속) · liveness/readiness probe
트래픽이 NetworkPolicy에 매칭되는지(CNI 구현 의존, K11과 같은 성질) · Agent CRD의 라벨 규약
(C-3이 확정하면 `verify-isolation-k8s.sh`의 D계열이 실행 가능해진다).

---

## 113. Phase C-2b · C-3a · C-3b — CRD·RBAC·`driver/kube`·TokenReview

§112가 B·C-1·C-2를 기록했다. 이 절은 그 뒤를 잇는다.

### 113.1 ★ G3의 약속이 실제로 지켜졌다 — 이 프로젝트에서 가장 값진 검증

`10-core-architecture.md` G3는 *"Docker API 타입이 `WorkloadDriver` 밖으로 유출되면 2단계에서
호출부 전부 재작성"*이라며 드라이버 경계를 만들었고, §6.2는 *"수렴 루프는 두 단계에서 같은
코드다"*라고 약속했다. C-3b가 그 약속을 **처음으로 시험했다.**

**결과: `orchestrator/internal/reconcile/reconcile.go`는 한 줄도 바뀌지 않았다**(mtime
2026-07-27 09:37, C-3b 작업 전후 동일 — 감독 실측). `driver/kube`를 새로 넣고 `driver/docker`와
나란히 두었는데 수렴 루프·해시 의미론·supervisor 계약이 전부 무변경이다. `lint-driver-boundary.sh`도
PASS — k8s 타입(`unstructured`·`dynamic`·`apimachinery`)이 `driver/kube` 밖으로 새지 않았다.

**훨씬 이전에 내린 설계 결정이 지금 값을 냈다.** G3가 없었다면 이번 phase는 수렴 루프 재작성을
포함했을 것이다.

### 113.2 `WorkloadDriver` 6메서드의 k8s 구현

| 메서드 | k8s 실현 |
|---|---|
| `Apply` | Agent CR 생성 + 소유(ownerReference) 자격증명 Secret 생성 |
| `Recreate` | CR 전체 Update(SpecHash 축 포함). 실제 Pod 철거는 operator의 몫 |
| `ReloadConfig` | **ConfigHash 축 필드만** JSON merge-patch — §6.2가 두 해시를 분리한 이유(*"합치면 프롬프트 한 줄 수정이 전 에이전트 재생성"*)를 코드 층에서 지킨다 |
| `Drain` | `status.phase=Draining` best-effort patch |
| `Delete` | **Agent CR 하나만 삭제** — 나머지는 K10의 ownerReference GC. 테스트 `TestDeleteOnlyTouchesTheAgentCR`이 이것을 못박는다 |
| `Observe` | CR `status`를 `workload.WorkloadStatus`로 매핑 |

### 113.3 genesis 재배선 — 사라진 것 / 남은 것 / 새로 생긴 것

| | 항목 |
|---|---|
| **사라짐** | `Store` · `Issue` · `Revoke` · `Live` · `TokenPrefix` · `TTL` — **OSiris는 토큰을 만들지 않는다**(K7) |
| **그대로** | `AllowedRoutes` · `Allows` · `Route` — **F14의 스코프는 byte-for-byte 무변경.** 인증 방식만 바뀌고 인가 범위는 안 바뀐다 |
| **새로** | `TokenReviewer`/`K8sTokenReviewer` · `OrgRevisionReader` · `Guard.Verify`(TokenReview → 신원 일치 → W1 검사, 매 호출) |

**W1을 잃지 않은 것이 중요하다.** 발급 시점이 사라지면 *"조직이 이미 있으면 거부"*를 걸 자리도
사라진다 — 이 재배선의 가장 큰 함정이다. `handleIssueGenesisToken`이 하던 rev==0 검사가
`Guard.Verify`로 **이전**되어 매 검증마다 평가된다. 70 §4.2의 W1 행(*"무변경. 판정 주체·조건 동일"*)이
그 상태로 유지된다.

**테스트 처분을 정직하게 기록한다.** F14 방어 3개(`TestAllowsIsDefaultDeny`·
`TestAllowsIsMethodSensitive`·`TestCommitIsNotInTheAllowlist`)는 **수정 없이 생존**. `Store`를
검사하던 7개는 **삭제** — 검사 대상 자체가 사라졌기 때문이며, 그 사실이 테스트 파일 상단 주석에
남았다. 새로 7개가 들어왔고 그중 `TestGuardVerifyRefusesWrongServiceAccount`가 **K7의 인가 경계
그 자체**다: 유효한 K8s 토큰이라도 **다른 SA의 것이면 거부**해야 한다. 이것이 없으면 K7은
*"bootstrap-master Pod가 온보딩한다"*에서 *"클러스터의 아무 인증된 Pod나 온보딩한다"*로
**§8.5보다 나빠진다.**

### 113.4 ★ 워커가 감독의 린트 결함을 찾아냈다 — 그리고 옳게 처신했다

C-3b 워커가 발견한 것: **`GEN(f)`(§109에서 감독이 쓴 검사)가 시체를 붙잡아두고 있었다.**

원 검사는 *"`/v1/onboarding/genesis-token`과 `.../revoke`가 `s.auth`로 게이트되어야 한다"*를
주장했다. OSiris가 토큰을 **발급하던** 동안에는 옳은 불변식이었다. K7이 발급을 없앤 뒤에는
그 검사가 **"발급 라우트가 존재할 것"을 요구**하게 되어, 워커는 린트를 만족시키려 **410 Gone
스텁 두 개를 남겨야** 했다.

**워커는 보호된 린트를 자기 판단으로 고치지 않고 보고했다** — §104.1이 칭찬한 것과 같은 처신이다
(*"지시를 따르고 충돌을 보고한 것이 올바른 처신이었다"*). 감독이 고쳤다.

**`GEN(f)`를 K7의 현실로 재작성했다.** 검사할 가치가 있는 불변식은 옛것의 역이다 — *"발급이 제대로
게이트되었는가"*가 아니라 **"발급이 존재하지 않는가"**:

| 검사 | 내용 |
|---|---|
| GEN(f1) | genesis 패키지가 **아무것도 만들지 않는다**(`Issue`·`TokenPrefix`·`Store` 부재) |
| GEN(f2) | 검증이 **TokenReview seam**을 지난다 |
| GEN(f3) | **wrong-identity 거부가 존재하고, namespace와 service account를 둘 다 비교**한다 |
| GEN(f4) | **잔존 발급 라우트가 없다** |

그리고 410 스텁 두 개와 라우트 등록을 **삭제**했다. **스테일한 검사를 만족시키려 죽은 라우트를
살려두는 것은 코드베이스가 아무도 지울 수 없는 기제를 쌓는 방식이다.**

**GEN(f3)이 가장 중요하다** — 그것이 K7의 인가 경계이고, `Namespace`만 또는 `ServiceAccount`만
비교하면 *같은 이름의 다른 네임스페이스 SA*가 통과한다. 그래서 **둘 다 비교하는지**를 검사한다.

**주입 시험 4/4 탐지**(f1 발급 부활 · f2 seam 제거 · f3 거부 제거 · f4 라우트 부활).

**감독의 실수 하나도 남긴다**: f2 주입이 두 파일(`genesis.go`·`tokenreview_k8s.go`)을 건드렸는데
원복 헬퍼가 한 파일만 복원해 빌드가 깨졌다. 즉시 복구했다(빌드·테스트·린트 전부 재확인). **주입
시험의 원복은 주입이 건드린 파일 전부를 덮어야 한다** — 헬퍼가 한 파일만 받는 구조였던 것이 원인이다.

### 113.5 C-2b · C-3a 요약

**C-2b**: `console/Dockerfile`+`nginx.conf` 신설(라우팅은 Ingress의 몫이므로 정적 서빙만).
`gw/conf.d`의 SPA fallback이 통상적인 `try_files $uri $uri/ /index.html`이 **아니라**
`try_files /index.html =404;`였고(콘솔이 순수 React state 라우팅) **원문 그대로 옮겼다** —
"통상적인 형태"로 고쳐 쓰면 조용히 동작이 달라졌을 지점이다. `repo-env` 볼륨을 emptyDir →
Secret 읽기 마운트로 교체.

**교환된 성질을 명시한다**: 읽기 전용이 되어 `applyGatewayTokenToEnv`의 `.env` 쓰기가 **EROFS로
즉시 실패**한다. 이전(emptyDir)은 *쓰기는 되지만 재시작에 조용히 사라지는* 상태였다. **조용한
데이터 손실 → 큰 소리로 실패**로 바뀐 것이며 이 레포의 fail-closed 문화에 맞다. 쓰기 경로
재설계는 C-3c.

**C-3a**: Agent CRD(structural schema, `x-kubernetes-preserve-unknown-fields` **미사용** — 스키마가
실제로 검증한다) + RBAC 4종 + ResourceQuota/LimitRange.

**워커의 판단 하나가 특히 좋다**: `Credentials`를 CRD spec에서 **의도적으로 뺐다**. spec에 넣으면
`kubectl get agent -o yaml`이 **평문 노출**해 Secret을 따로 두는 의미가 사라진다. 지시하지 않은
판단인데 *왜 옮기지 않는지*를 근거와 함께 결정했다.

**RBAC 실측(감독)**:
```
ClusterRole osiris-core-tokenreview        : tokenreviews:create        ← 이것 하나만
Role        osiris-agent-operator          : ns=osiris-agents 에서만
Role        osiris-orchestrator-bookkeeping: ns=osiris-system 의 leases,events 만
osiris-api                                 : 바인딩 없음
```
**`osiris-system`에 워크로드 생성 권한이 0** — G8의 k8s 대응이 성립한다. ClusterRole은
TokenReview가 cluster-scoped라 **문법적으로 불가피한 한 곳**뿐이다.

### 113.6 남은 것 — C-3c와 그 뒤

- **★ Agent CR을 watch하는 operator가 없다.** `driver/kube`는 CR을 만들고 지우지만, 그 CR을 보고
  Pod·PVC·SA·NetworkPolicy를 렌더하는 컨트롤러가 **미구현**이다. 즉 **에이전트는 아직 뜨지 않는다.**
  이것이 C까지의 가장 큰 미완이며 "다 됐다"로 오독되면 안 된다.
- `cmd/osiris-orchestrator/main.go`가 `driver/kube`를 배선하지 않았다(드라이버 선택 설정 없음).
- NATS 인증 재배선(`authorization.conf` + SIGHUP → ConfigMap/Secret) — C-3c.
- `.env` 쓰기 경로 → Secret patch — C-3c.
- `agentName()`이 `employeeID`가 이미 DNS-1123 유효 이름이라 가정한다(미검증).
- `verify-isolation-k8s.sh`의 D계열은 operator가 생겨 실제 에이전트 Pod가 뜬 뒤에야 실행 가능하다
  → **K11을 닫는 조건이 여기 묶여 있다.**

---

## §114 Phase C-4 / C-3c / K3 — 실제 배포가 찾아낸 것, 그리고 UI 도달

작성: 2026-07-28. 이 절은 **차트를 진짜로 install해서** 얻은 것만 적는다. `helm lint`와
`helm template`은 아래 결함 14건 중 **단 하나도** 잡지 못했다 — 48개 오브젝트가 전부
정상 렌더되는 상태에서 파드는 뜨지 않았다. 이 사실 자체가 이 절의 첫 번째 교훈이다.

### 114.1 사용자가 요구한 목표는 달성됐다

> "당장 내가 기대하는 목표는 UI접속해서 온보딩하고 시작하는거야"

실측(감독이 워커 보고를 신뢰하지 않고 직접 재현):

```
GET  /                        → 200   <title>OSiris Console</title>
POST /_api/auth/session       → 200   Set-Cookie: osiris_console_session=…; HttpOnly; SameSite=Strict
                                      {"kind":"master-key","ok":true}
GET  /_api/whoami             → 200   (권한 5개 전부)
GET  /_api/me                 → 200   consoleSurfaces: ["onboarding"]
GET  /_api/onboarding/status  → 200   phase: "NotStarted"
```

master key 부팅 분기는 `loaded`(경로 `/state/creds/admin/master_key`), 접두 `osk_`, 길이 47.
빈 그래프이므로 **H10 술어가 열려 있어** 403이 아니라 200이 나온 것이다 — 설계대로다.

### 114.2 실제 배포가 찾은 결함 14건

**구조적 함정 3건 — compose→k8s 번역에서 반복될 종류다:**

| # | 결함 | 왜 lint가 못 잡는가 |
|---|------|--------------------|
| 3 | **compose `command:` ≠ k8s `command:`** — compose의 `command`는 CMD(ENTRYPOINT의 인자)이고 k8s의 `command`는 ENTRYPOINT 자체를 덮어쓴다 | 문법이 유효하다. nats가 `-c`를 실행파일로 찾다 **로그 한 줄 없이** 죽었다 |
| 2 | **headless Service는 ready 전에 이름이 없다** — DNS가 ready endpoint만 반환해 NXDOMAIN | `core`/`orchestrator`의 재시도 루프가 *"이름은 풀리고 TCP만 거부"*를 전제로 작성돼 기동 교착. `<name>-headless`(governing) + `<name>`(ClusterIP, 클라이언트용)으로 분리해 해결. 불변 호스트명 3개는 보존 |
| 7 | **compose healthcheck는 죽이지 않고 k8s liveness는 죽인다** — compose `start_period` ↔ k8s `startupProbe` | neo4j가 기동 60초에 살해됐다. startupProbe 추가 |

**나머지 11건:**

1. `nodeAffinity`가 아무도 안 붙이는 라벨을 요구 → `global.pinCoreToMainNode` 기본 false
4. `NEO4J_PASSWORD`를 서버에 전달 → `Unrecognized setting` → Secret이 조합된 `NEO4J_AUTH`를 담도록
5. Service 이름이 `neo4j`가 되자 `enableServiceLinks`가 `NEO4J_PORT_7687_TCP_PORT`를 주입 → `enableServiceLinks: false`
6. neo4j 메모리 설정 1536Mi > 컨테이너 limit 1500Mi → 2Gi
8. omniroute OOMKilled → 512Mi → 1536Mi (`OMNIROUTE_MEMORY_MB=1024`가 근거)
9. omniroute 프로브 경로 `/v1/models`(404) → `/omni/v1/models`(200)
10. **`ingressClassName`이 비어 컨트롤러가 Ingress 4개를 조용히 무시** → 파드는 전부 Running인데 화면은 404. 기본값 `nginx`
11. `configuration-snippet`이 ingress-nginx 1.9+에서 기본 비활성 → admission webhook이 install을 거부 → `ingress.useConfigurationSnippet` 기본 false
12. `use-regex: true`가 `pathType: Exact`를 정규식으로 만들어 **nginx의 `location = /` 최우선 규칙을 잃게** 했다 → omniroute catchall이 `/`를 삼켜 콘솔이 안 뜸. 콘솔 Ingress를 단일 `Prefix /`로 단순화하고 catchall은 `ingress.omnirouteCatchAll` 기본 false 뒤로
13. omniroute NetworkPolicy에 ingress-nginx 네임스페이스가 없어 502 → `namespaceSelector` 추가. 근거: **K3가 `osiris-gw`를 은퇴시켰고 Ingress 컨트롤러가 그 `/omni/` 프록시 역할을 물려받았다.** `osiris-agents`는 의도적으로 계속 제외 — §9.0의 "에이전트는 omniroute에 직접 도달 불가"가 살아 있어야 한다
14. **Secret 값 변경만으로는 StatefulSet이 롤아웃되지 않는다** — `secrets.omnirouteAdminPassword`를 채워도 파드를 수동 삭제해야 반영됐다. neo4j·omniroute에서 각각 한 번씩 나왔으므로 **재발 부류로 분류한다**

**결함 11의 여파(설계 문서에 반영 필요):** §108이 실측한 SSE 지시자(`proxy_buffering off`,
`proxy_read_timeout 3600s`)를 `configuration-snippet`으로 나르는 경로가 막혔으므로, 전용
애노테이션 3개(`proxy-buffering`, `proxy-read-timeout`, `proxy-send-timeout`)로 나른다.
클러스터에서 실제로 붙어 있음을 확인했다. **다만 그 애노테이션이 SSE를 실제로 통과시키는지는
아직 실측되지 않았다**(114.6 참조).

### 114.3 K11이 실측으로 부분 종료됐다

워커의 **대조 실험**: 디버그 Pod를 `orchestrator` 라벨로 띄우면 neo4j 접속이 거부되고,
같은 Pod를 `core` 라벨로 바꾸면 성공했다. 이로써 **k3s 기본 CNI가 NetworkPolicy를 실제로
집행한다**는 것이 증명됐다 — 따라서 "kubelet 프로브가 차단되지 않는다"는 관찰도 정책이
무력한 탓의 허상이 아니라 **진짜 발견**이다.

**아직 못 닫은 부분**: `verify-isolation-k8s.sh`의 D계열은 실제 에이전트 Pod를 요구하고,
그것은 operator가 있어야 한다(§113.6).

### 114.4 감독 자신의 오류 (기록해야 재발을 막는다)

- **grep 오탐 3건**: `nodeAffinity`·`emptyDir`·`configuration-snippet`이 모두 **주석 안에서**
  매칭됐다. §112.2에 이미 적었듯 **grep은 주석을 걷어내고 써야 한다.** 같은 실수를 반복했다.
- **문자열 삽입 편집으로 차트를 두 번 깨뜨렸다**: `ingress.yaml`의 `if`/`end` 불균형,
  `networkpolicy-system.yaml`의 YAML 들여쓰기. 둘 다 되돌린 뒤 구조를 다시 잡아 위임했다.
  → **교훈: YAML/Helm 템플릿을 부분 문자열 치환으로 편집하지 말 것.** 워커 프롬프트에
  이 경고를 명시적으로 넣기 시작했다.
- **워커의 진단을 그대로 믿지 않은 것은 옳았다**(114.5).

### 114.5 ★ C-3c — 앞 워커의 진단이 틀렸고, 결함은 정책 쪽이었다

`osiris-orchestrator`가 CrashLoopBackOff(18회 재시작)였다. 앞 워커는 이를
**"orchestrator에 남은 pre-K9 `driver/docker` 시절 잔존 코드"**로 진단했다. **틀렸다.**

감독이 확인한 반대 증거:

- compose에서 `osiris-orchestrator`는 `osiris-data`(neo4j가 있는 망)에 붙는다 —
  **설계가 도달을 허용한다**
- `orchestrator/internal/graphdb/client.go`는 명시적으로 읽기 전용이다. 주석 원문:
  *"there is no ExecuteWrite anywhere in this package"*, 세션은 `neo4j.AccessModeRead`
- `graphdb/derive.go`(212줄)의 `DeriveAgentWorkloads()`는 **수렴 루프의 핵심 읽기 경로**이며
  잔존 코드가 아니다
- **G7의 원문은 쓰기에 관한 것이다**: *"G7 | Neo4j에 **여러 컴포넌트가 직접 쓰기** | … |
  graph-write API 단일 관문 (D17)"*

따라서 진짜 결함은 **`osiris-neo4j-allow` NetworkPolicy가 정당한 독자를 빠뜨린 것**이었다.
정책에 orchestrator를 추가해 닫았고, core만 쓰기 관문이라는 불변식은 그대로다.

**교훈**: "정책이 막았다"를 보고 "코드가 잘못됐다"로 넘어가지 말 것. 어느 쪽이 설계와
어긋나는지 먼저 문서와 코드로 확인해야 한다. 정책을 느슨하게 푸는 것도, 정당한 코드를
지우는 것도 둘 다 오답이 될 수 있었다.

### 114.6 온보딩 0단계가 열렸다 — 그리고 Secret↔PVC 이중 진실 소스

`/repo-env`(=`.env`)가 읽기 전용 Secret이어서 0-4의 게이트웨이 토큰 write가 EROFS로
실패했다. **쓰기 가능한 PVC로 교체**해 열었다. `osiris-api`의 SA에는 **권한을 하나도
주지 않았다** — Secret patch를 주면 *"api는 k8s 권한이 0"* 불변식(`verify-isolation-k8s.sh`의
E2 대응 검사, 옛 *"osiris-api에 docker.sock이 없다"*의 k8s 등가물)이 깨지므로 PVC로 우회했다.
초기값은 initContainer가 **파일이 없을 때만** Secret에서 복사한다(0단계가 쓴 값을 재시작 때
날리지 않도록).

결과:
```
step0.omniroute.reachable: true      (이전 false)
step0.omniroute.manageKeyExchanged: true
osiris-orchestrator: 1/1 Running     (CrashLoopBackOff 해소)
```

**정직하게 남기는 미완 — 이중 진실 소스**: `api/internal/omniroute/ops.go`는 호출마다
`cmd.Env = os.Environ()`로 프로세스 env를 스냅샷한다. manage key 교환 시 `os.Setenv`로
메모리 내 env는 갱신되고 PVC 파일에도 반영되지만 **K8s Secret에는 반영되지 않는다.**
파드가 재시작되면 새 프로세스는 Secret에서 온 빈 값으로 시작해 `reachable`이 다시 false가
된다(실측: 재시작 직후 `reachable:false, manageKeyExchanged:true`, 0-3 재실행하니 true 복귀).

이는 **master key가 K8s Secret이 아니라 PVC 파일에 사는 것과 정확히 같은 뿌리다**
(70 §4의 K8은 Secret을 말하지만 `masterkey.Boot`는 파일 IO를 한다 — 부팅 로그가 `loaded`인
것이 그 증거). **둘을 함께 Secret으로 옮기는 것이 별도 phase다.** Go 코드 수정 없이는
닫을 수 없다.

### 114.7 K3 — 공개 도메인이 compose gw에서 k8s Ingress로 넘어갔다

`https://osiris.example.com/`가 이제 클러스터로 간다. 방법은 **k3d의 로드밸런서 컨테이너를
`dev-net`에 이중 홈으로 붙이는 것**이며, 이는 compose 시절 `osiris-gw`만 `dev-net`에
물리던 것과 **같은 패턴**이다 — 격리 구조를 새로 뚫지 않고 진입점만 갈아탔다.

```
docker network connect dev-net k3d-osiris-dev-serverlb
```
`osiris.example.com.conf`의 upstream: `osiris-gw:80` → `k3d-osiris-dev-serverlb:80`. TLS는 그대로
상위 리버스 프록시에서 종단한다(1홉 추가 구조 유지).

- **★ 이 네트워크 연결은 k3d가 관리하지 않는다. 클러스터를 재생성하면 다시 붙여야 한다.**
- conf의 SSE 블록을 고쳤다: 기존 `location /api/events`는 콘솔 설계 확정 전의 **자리표시자**였다.
  실제 외부 경로는 `/_api/events`이며 Ingress의 `rewrite-target: /api/$2`를 지나
  `osiris-api`의 `GET /api/events`로 간다. 경로 하나를 좁게 잡지 않고 **`location ^~ /_api`
  전체**에 버퍼링 해제를 적용했다 — 홉이 2개(상위 리버스 프록시 → ingress-nginx)여서 한 홉만 놓쳐도
  실시간이 죽고, JSON 응답까지 버퍼링을 끄는 비용은 무시할 수준이다.
- **회귀 없음**: 같은 nginx가 13개 도메인을 서비스한다. `nginx -t`는 기존 경고(다른 도메인의
  http2 지시자 deprecation)만 냈고 기존 사이트 서브도메인 전부 200.
  `mac.example.com`는 000이지만 백엔드(`<lan-ip>:3000`)가 애초에 없는 기존 상태이며,
  감독은 conf 파일 **1개만** 만졌다(백업 `*.bak-k3d-20260728-164449`).
- 되돌리기: conf의 두 줄 주석을 맞바꾸고 reload. compose 스택은 살아 있고 `127.0.0.1:18280`에서
  계속 받는다.

### 114.8 ★ SSE를 재려다 NATS ACL 결함 2건이 나왔다

2홉 SSE를 실측하려 `/_api/events`를 열었더니 **502 `cannot open event stream`**.
`content-type: text/plain` + `nosniff`는 Go의 `http.Error`이므로 **프록시가 아니라
애플리케이션**이다. 그 문자열은 `api/internal/events/sse.go:100`이고 바로 위 `:98`이
`h.js.AddConsumer("TASKS", cfg)`다.

**뿌리는 하나였다.** 클러스터에서 돌고 있는 `/etc/nats-dynamic/authorization.conf`는
정적 부트스트랩이며 세 플랫폼 사용자에게 이것만 준다:

```
osiris-core:         publish ["org.revision","org.event.>"]           subscribe []
osiris-orchestrator: publish ["agent.*.config.reload","org.event.>"]  subscribe ["org.revision"]
osiris-api:          publish ["$JS.API.>"]                            subscribe ["_INBOX.>"]
```

- `osiris-orchestrator`에 **JetStream 관리 권한이 없어** `jetstream/provision.go:159`의
  `AddStream`이 TASKS 스트림을 만들 수 없다 → SSE 502의 뿌리
- `osiris-api`에 **`security.>` 발행 권한이 없다** → 감사 이벤트 유실:
  `nats: Permissions Violation for Publish to "security.authn.login_succeeded"`.
  이는 예전에 잡은 `security.masterkey.>` 누락과 **같은 부류**이고, §authn에 넣은 2초
  데드라인이 100초 정지를 막아주고 있을 뿐 **이벤트는 실제로 버려진다**
- 부수 발견: 부트스트랩이 api에게 준 `$JS.API.>`는 §9.6의 *"osiris-api는 JetStream 관리
  권한이 없어야 한다… 필요한 것만 좁게 줄 것"*을 **정면으로 위반한다**(전체 관리 권한).
  넓히는 게 아니라 **좁히면서 필요한 것만 정확히** 주는 것이 옳은 수정이다

**이것은 회귀가 아니라 문서화된 미완의 결과다.** 그 ACL 파일 자신이 머리에 고백해 두었다:
*"라이브 상태에서는 osiris-orchestrator가 이 내용을 팀/에이전트 계정 전체로 재계산해
덮어써야 하지만(§7.2), 그 재기록 경로는 Agent CRD/operator(K9, C-3)가 구현될 때 추가된다."*
authoritative 출처는 `orchestrator/internal/natsauth/natsauth.go`의 `render()`다.

**따라서 SSE 통과는 여전히 미검증이다** — §108이 남긴 항목이 아직 열려 있다.

### 114.9 남은 것

- **operator 미구현** — 여전히 최대 미완. 에이전트가 뜨지 않고, 그래서 `verify-isolation-k8s.sh`의
  D계열과 **K11의 완전 종료가 여기 묶여 있다**(§113.6과 동일).
- NATS ACL: 정적 부분(D-3a)과 동적 재계산 경로(operator 소관). 후자는 §7.2의 미결 선택
  두 개를 요구한다 — ConfigMap을 누가 patch하는가, reload를 어떻게 트리거하는가.
  **감독 권고: operator가 ConfigMap을 patch하고 reload는 NATS의 파일 변경 감지에 맡긴다**
  (compose 배선을 그대로 옮기는 쪽. orchestrator가 이미 `natsauth.go`를 소유하고 있고,
  사이드카 reloader는 움직이는 부품을 늘린다). `rbac-operator.yaml`에 ConfigMap patch 권한이
  아직 없다 — 그것만 추가가 필요하다.
- **K8 미구현**: master key와 게이트웨이 토큰/manage key가 모두 PVC 파일에 산다(114.6).
- `POST /onboarding/commit`(§5.2의 15단계 단일 트랜잭션) 미구현 — 대화형 온보딩 완주 차단.
- 컨설턴트 시스템 프롬프트의 소유자/위치(§3.4.6 공백).
- SpecHash/ConfigHash 기반 Recreate/ReloadConfig 분기(D-2).
- **lint 갭**: `AUD` 검사는 compose 산출물(`nats/generated/authorization.conf.example`)을
  보고 차트의 ACL은 보지 않는다. 114.8의 누락을 lint가 못 잡은 이유다 — 감독이 만든 검사의
  사각지대이며 대상 확장이 필요하다.
- 열린 발견: F9, F10, F12, F13, F15, F16, F1–F3, F6, F8; K12(노드 가입 신뢰);
  Ingress 컨트롤러 선택.

---

## §115 Phase D-1 / D-3a — operator가 살아났고 SSE가 닫혔다

작성: 2026-07-28. 이 절은 §114의 인과 진단을 **정정**하고, operator 도입 결과를 적는다.

### 115.1 ★ §114.8의 진단을 정정한다

§114.8은 SSE 502의 원인을 두고 *"뿌리는 하나였다"*며 NATS ACL을 지목했다. **불완전했다.**
ACL은 필요조건이었을 뿐, 실제 병목은 그 아래에 있었다. D-3a 워커가 실측으로 찾은 사슬:

```
cmd/osiris-orchestrator/main.go 가 driver/kube 를 배선하지 않음
   → orchestrator 가 클러스터 안에서도 docker 드라이버로 돎
   → reconcile.go 의 Tick 이 observe() 에서 매번 실패
        "dial unix /var/run/docker.sock: no such file or directory"  (15초마다)
   → natsGen.Sync() / jsp.EnsureStreams() 는 observe() 뒤에 있어 절대 도달 못함
   → TASKS 스트림 0개  (jsz?streams=true → "streams": 0)
   → sse.go:98 의 AddConsumer("TASKS") 실패 → :100 의 502
   → 감사 이벤트도 ack 불가
```

**교훈**: 권한 오류(`Permissions Violation`)가 눈에 보이면 그것을 원인으로 확정하기 쉽다.
그러나 권한을 고친 뒤 에러가 `no response from stream`으로 **이동**한 것이 진짜 단서였다.
워커가 목표 미달을 미달로 보고하고 원인을 계속 추적했기 때문에 드러났다 — **"안 됐다"는
보고가 "됐다"는 보고보다 값이 컸던 사례로 기록한다.**

### 115.2 D-3a — 정적 ACL을 render()에 정합시켰다

authoritative 출처는 `orchestrator/internal/natsauth/natsauth.go`의 `render()`다. 대조 결과
부트스트랩이 세 곳에서 틀렸다:

| 사용자 | 부트스트랩의 오류 | 수정 |
|---|---|---|
| `osiris-core` | `$JS.API.STREAM.INFO.TASKS` 없음, subscribe 아예 없음 | render()와 동일하게 |
| `osiris-orchestrator` | **`$JS.API.>` 없음**(스트림 생성 불가), `_INBOX.>` subscribe 없음 | 추가 |
| `osiris-api` | **`$JS.API.>`** — §9.6을 정면 위반하는 전체 JetStream 관리 권한 | render()가 쓰는 **45개 구체 subject**로 좁힘 (TASKS 컨슈머 4개 + KV_AUTHN_MACHINE/CRED/TICKET 전용 + `$JS.API.INFO` + `security.{machine,rbac,enrollment,authn,masterkey}.>`) |

**수정 방향이 "좁히기"였다는 점이 중요하다.** 권한 문제를 넓혀서 해결하지 않았다.
`Permissions Violation`은 실측으로 소멸했다.

### 115.3 결함 15 — ConfigMap/Secret 변경은 자동 롤아웃되지 않는다 (세 번째)

`helm upgrade` 직후 ConfigMap의 md5는 즉시 바뀌었으나(`2b9f2…` → `8827f…`) **파드 내부
마운트 파일은 옛 md5 그대로**였고, `kubectl delete pod nats-0`로 강제 재시작해야 반영됐다.
nats-server는 SIGHUP 없이 재파싱하지 않는다.

**neo4j → omniroute → nats로 세 번째다. 단발 사고가 아니라 이 차트의 구조적 성질로 분류한다.**
→ 별도 항목: 값 변경이 롤아웃을 유발하도록 checksum 애노테이션 패턴 도입 필요.

### 115.4 D-1 — Agent CR operator가 동작한다 (감독 직접 검증)

새 파일: `orchestrator/internal/operator/{operator.go,objects.go,status.go,operator_test.go}`.
`main.go`에 `WORKLOAD_DRIVER` 분기(docker 기본 / kube 선택), `config.go`에
`WorkloadDriver`/`AgentsNamespace`/`SystemNamespace` 추가 — **기본값이 기존 compose 경로를
그대로 유지한다.**

감독이 워커 보고를 신뢰하지 않고 직접 재현한 결과:

```
Agent CR 생성 → 6초 후
  agent.osiris.dev/sup-verify        Running
  pod/sup-verify                     1/1 Running
  persistentvolumeclaim/osiris-ws-sup-verify   Bound  1Gi  local-path
  serviceaccount/sup-verify
  secret/sup-verify-config           Opaque  4

ownerReferences → Agent/sup-verify  controller=true
kubectl delete agent sup-verify → Pod·PVC·SA·Secret 전부 소멸 (K10이 사는 이유)
osiris-orchestrator: 재시작 0회 (이전 18회)
```

**격리 실측(설계대로 전부 통과):**
```
osiris-agents 안의 Pod 에서 →
  osiris-omniroute:20128  차단   ← §9.0
  neo4j:7687              차단
  osiris-core:8090        도달(404)  ← §2.6
  osiris-egress:3128      허용
  nats:4222               허용
```
에이전트별 NetworkPolicy는 **만들지 않았다** — 네임스페이스 전역 정책으로 충분하다는 것이
실측으로 확인됐다(중복 생성 회피).

**설계 이탈(감독 지시, 침묵 아님)**: 70 §5.1은 *"controller-runtime 기반"*이라 적었으나
`client-go`의 dynamic informer + workqueue로 구현했다. 근거: `driver/kube`가 이미
`dynamic.Interface`로 같은 CRD를 다루고 있어 controller-runtime을 더하면 **같은 CRD에 두 개의
서로 다른 클라이언트 추상화**가 생긴다. 네임스페이스 하나·리소스 하나를 보는 작은 루프라
Manager/leader-election의 이점이 필요 없다(Simplicity First). 70 §5.1에 정정 태그를 달았다.

`config` Secret을 ConfigMap이 아니라 Secret으로 만든 이유도 기록됐다: RBAC이 `secrets`는
주지만 `configmaps`는 주지 않으며, 렌더 결과는 이미 CR spec에 평문으로 있어 기밀성 차이가 없다.

### 115.5 결함 16 — apiserver egress와 CNI의 포트 매칭 지점

`WORKLOAD_DRIVER=kube` 배선 후 orchestrator가 apiserver(`10.43.0.1:443`)에 도달하지 못했다.
`networkpolicy-system.yaml`의 orchestrator egress에 apiserver 목적지가 없었다.

**그런데 `ipBlock: 0.0.0.0/0, port: 443`만으로는 여전히 막혔다.** 워커의 실측 설명:
이 클러스터의 kube-router netpol은 egress `ports:`를 **kube-proxy DNAT 이후의 실제 포트**로
매칭하고, k3s의 apiserver는 control-plane 노드에 hostNetwork로 떠 있어 그 포트가 `6443`이다
(Endpoints 객체가 `<node-ip>:6443`임을 확인). **443과 6443을 모두 허용해 해결.**

어느 CNI가 어느 지점에서 매칭하는지는 클러스터마다 다를 수 있으므로 둘 다 남겼다 —
`egress-deployment.yaml`의 기존 TODO와 같은 성격의, **인정된 한계**다.

### 115.6 ★ §108의 미검증 항목이 닫혔다 — SSE 2홉 통과 실측

§108이 omniroute SSE를 실측했지만, k8s Ingress를 통과하는지는 미검증으로 남아 있었다.
특히 결함 11로 `configuration-snippet`을 버린 뒤 전용 애노테이션 3개에 의존하게 되어
**실측만이 확인 수단**이라고 차트 주석이 적어 두었다. 이제 측정했다.

경로: `https://osiris.example.com/_api/events` — **상위 리버스 프록시 → ingress-nginx 2홉 전부 통과.**

```
HTTP/2 200
content-type: text/event-stream
cache-control: no-cache
```

연결 즉시 `event: hello` 프레임 도착. **라이브 푸시 지연(유발 → 프레임 도착):**

| 유발 | 프레임 도착 | 지연 |
|---|---|---|
| 로그인 #1 `17:59:31.696` | `17:59:31.721` | **25ms** |
| 로그인 #2 `17:59:35.767` | `17:59:35.799` | **32ms** |

버퍼링이 걸려 있으면 프레임은 버퍼가 찰 때까지 갇혀 몰려서 도착한다. 25~32ms는 그럴 수
없는 값이다. **양쪽 홉 모두 버퍼링이 꺼져 있음이 실측으로 확인됐다.**

주의: 첫 측정에서는 `onboarding/status` GET을 유발로 썼는데 그것은 이벤트를 발행하지 않아
프레임이 오지 않았다. **이벤트를 확실히 발행하는 동작(로그인)으로 바꿔야 측정이 성립했다** —
"조용한 스트림"을 통과로 오독하지 않으려면 유발 동작이 실제로 발행하는지 먼저 확인해야 한다.

### 115.7 남은 것

- **★ D-3b — NATS ACL 동적 재계산 경로가 여전히 없다.** 그리고 이제 **활성 에러 루프**다:
  ```
  osiris-orchestrator: nats auth sync: write nats authorization.conf:
      open /etc/nats-dynamic/authorization.conf: no such file or directory   (15초마다)
  ```
  operator가 natsauth Signaler를 kube 모드에서 "미구현" 스텁으로 남겼으나 **쓰기는 시도된다.**
  조용히 무시하지 않고 명확히 에러를 내는 쪽을 택한 것은 옳다. §7.2의 미결 선택 두 개가
  여기서 결판난다 — **감독 권고: operator가 ConfigMap을 patch하고 reload는 파일 변경 감지에
  맡긴다**(compose 배선을 그대로 옮기는 쪽; orchestrator가 이미 `natsauth.go`를 소유).
  `rbac-operator.yaml`에 ConfigMap patch 권한 추가가 필요하다.
- **D-2** — `specHash` 변경 시 Recreate(drain→delete→apply), `configHash` 변경 시 무중단
  ReloadConfig. 현재 `observedSpecHash`/`observedConfigHash`는 **diff 없이 그대로 미러**만
  하며, Pod가 이미 있으면 `ensurePod`는 아무것도 하지 않는다.
- **genesisboot(bootstrap-master)가 kube 모드에서 미구현 스텁**이다 — W1–W5 다섯겹 폐쇄가
  k8s 경로에서 아직 실행되지 않는다. K7이 TokenReview로 신원을 대체했으므로 §8.5의 순서
  보증(create→issue→write→start)을 k8s에서 무엇이 대신하는지 재확인이 필요하다.
- **K8 미구현**: master key·게이트웨이 토큰·manage key가 모두 PVC 파일에 산다(§114.6).
- `POST /onboarding/commit`(§5.2의 15단계 단일 트랜잭션) 미구현 — 대화형 온보딩 완주 차단.
- **결함 15 대응**: checksum 애노테이션 패턴.
- **lint 갭**: `AUD`가 compose 산출물만 보고 차트 ACL은 보지 않는다(§114.9).
- `verify-isolation-k8s.sh`의 D계열이 이제 **실행 가능해졌다** — 에이전트 Pod가 실제로 뜨므로.
  **K11 완전 종료의 조건이 풀렸다.**
- 열린 발견: F9, F10, F12, F13, F15, F16, F1–F3, F6, F8; K12(노드 가입 신뢰).

---

## §116 Phase D-2 — 해시 두 축의 분기가 실제로 동작한다

작성: 2026-07-28. 10 §6.2와 `spec.go`의 `ComputeHashes` 주석이 지키려던 것 —
*"합치면 프롬프트 한 줄 수정이 전 에이전트 재생성이 된다"* — 이 실측으로 확인됐다.

### 116.1 두 목표 조건 (감독이 코드로 대조 확인)

**ConfigHash만 바뀌면 Pod가 그대로다:**
```
BEFORE  uid fade0f11-…  startTime 14:02:07Z   CLAUDE.md v1
AFTER   uid fade0f11-…  startTime 14:02:07Z   CLAUDE.md v2 - CONFIG ONLY CHANGE
        observedConfigHash=config-v2  observedSpecHash=spec-v1  phase=Ready
```

**SpecHash가 바뀌면 재생성되되 작업공간은 보존된다:**
```
Pod uid  fade0f11-… → 03d116c4-…    (변경 = 재생성)
PVC uid  48c1c9d2-… → 48c1c9d2-…    (불변 = 보존)
```
`ensurePVC`는 `syncAgent`에서 별도의 멱등 호출이고 `ensurePod`는 PVC를 건드리지 않는다.

**`extraNoProxy`는 아무 반응이 없다** — uid·startTime·Secret·양쪽 해시 전부 불변.
`spec.go:79`의 *"재시작 없이 반영(no restart at all, not even a config reload event)"*이 지켜졌다.

### 116.2 ★ finalizer를 쓰지 않은 판단 — 더 나은 답이었다

감독은 "finalizer를 쓰되 반드시 유계로, 위험 대비 이득이 없다면 쓰지 않고 근거를 보고"로
판단을 열어 두었다. 워커는 **쓰지 않는 쪽**을 골랐고 근거가 정확하다:

Pod 생성 시 `spec.terminationGracePeriodSeconds = spec.drainSeconds`를 박아두면
**두 경로가 동시에** 유예를 지킨다 —
1. operator가 SpecHash 변경으로 부르는 `Delete`(`GracePeriodSeconds` 명시)
2. `kubectl delete agent` 시 API 서버의 ownerReference cascade GC

**2번은 operator 프로세스가 살아 있을 필요가 없다.** finalizer는 그 위에 "사라지기 직전
status 한 번 더 쓰기" 정도만 더하면서, **operator가 죽어 있을 때 삭제가 영구 교착되는**
위험을 상시로 안긴다. 실측: `kubectl delete agent` **0.056초** 반환, 자식 4개 1초 내 소멸
(`drainSeconds=25` 한참 안쪽).

`driver/kube.Drain`의 주석이 예고한 *"the graceful-shutdown WAIT itself is spec.DrainSeconds …
the NOT-YET-BUILT operator reads when it observes the CR is draining/being deleted"*가
이렇게 채워졌다 — operator가 타이머를 돌리는 것이 아니라 **Pod 필드 하나로** 대신했다.

### 116.3 부수 측정이 설계 서술을 뒷받침한다

마운트된 Secret이 실행 중 Pod 안에서 갱신되기까지 **약 47초**(kubelet의 projected-volume
sync 주기)였다. 따라서 ConfigHash 변경이 *"다음 태스크부터 적용"*이라는 §6.2의 서술은
회피가 아니라 **물리적으로 맞는 서술**이다. 즉시 반영을 약속하는 문장을 쓰지 않은 것이 옳았다.

### 116.4 ★ 감독이 잡은 잠재 결함 — 조용히 실패하는 종류였다

워커의 1차 구현:
```go
if observedHash == "" || observedHash == spec.specHash {
    return nil   // 입양
}
```

**의도는 옳았다**(기능 배포 첫 틱에 이미 돌고 있는 함대가 전부 재생성되는 것은 §6.2가
경고하는 바로 그 재앙이다). 그런데 감독이 확인한 사실:

- `specHashAnnotation`이 **쓰이는** 지점은 Pod **생성 시점 하나뿐**
- operator 패키지 전체에 `Patch(` 호출이 **하나도 없었다**

따라서 어노테이션이 빈 Pod는 그 값이 채워질 경로가 없어 매 틱 `observedHash == ""`에 걸린다
→ **입양된 Pod는 영구히 Recreate 면역.** image를 바꿔도 resources를 바꿔도 반영되지 않고
**로그도 남지 않는다.** 이 저장소에서 가장 피해야 할 실패 형태다.

**수정**: 입양 시 현재 `spec.specHash`를 merge patch로 stamp한다(`stampSpecHashAnnotation`,
`metadata.annotations`만 건드리는 좁은 patch — `driver/kube.ReloadConfig`가 이미 쓰는
"의도한 축만 patch" 규율과 같은 형태). 그러면 두 목표가 동시에 성립한다: 입양 순간엔
재생성 없이, **다음** 진짜 변경부터 정상 Recreate.

**stamp 실패는 재생성으로 넘어가지 않는다** — 에러를 위로 올려 workqueue가 다음 틱에
재시도한다. 실패를 "해시가 다르다"로 오해하면 그게 더 나쁘기 때문이다.

입양 경로 실측 3단계:
```
① 입양 틱: uid 086af68f-… 불변 (재생성 안 됨)
② 어노테이션 복원: {"osiris.dev/spec-hash":"spec-v1"}
③ 그 뒤 진짜 변경: uid 086af68f-… → 8d734788-…  (재생성됨)
   PVC uid 5e9e907e-… 불변 (보존)
```

**교훈**: "업그레이드 시 전 함대 재생성 방지"라는 올바른 목표가, 한 걸음 부족한 구현으로
**정반대의 조용한 고장**을 만들 수 있었다. 근거 주석에 *왜 stamp가 필수인지*(= 없으면 영구
면역)를 남겼으므로, 다음 사람이 "입양이면 그냥 `return nil`이면 되지 않나"로 되돌리지 않는다.

### 116.5 검증 범위의 한계 (정직하게)

이 환경의 Neo4j에는 `:AIAgent` 노드가 **0개**다. 따라서 `reconcile.go`가 그래프에서 spec을
계산해 드라이버를 부르고 operator가 물성화하는 **전 구간 종단 검증은 하지 않았다.** 검증은
operator를 실제 클러스터에 대고 직접 구동하는 방식이었다(`reconcile.go` 우회). 그래프 데이터가
생기는 시점 — 즉 `POST /onboarding/commit`이 구현되어 조직이 실제로 커밋된 뒤 — 에 종단으로
다시 확인해야 한다.

이 한계는 `reconcile.go`의 고아 정리 로직 때문에 생긴 것이기도 하다: 그래프에 없는 Agent CR을
자동 삭제하므로, 손으로 만든 CR은 다음 틱에 사라진다(D-1에서 실측 확인된 정상 동작).

### 116.6 남은 것 (변동분만)

- **D-3b** — NATS ACL 동적 재계산. 여전히 15초마다 활성 에러. **권고 불변**: operator가
  ConfigMap을 patch, reload는 파일 변경 감지. `rbac-operator.yaml`에 ConfigMap patch 권한 추가.
- **`verify-isolation-k8s.sh`의 D계열이 이제 실행 가능하다** — 에이전트 Pod가 실제로 뜨고,
  해시 분기까지 동작하므로. **K11 완전 종료의 마지막 조건이 풀렸다.**
- `genesisboot`이 kube 모드에서 미구현 스텁 — W1–W5가 k8s 경로에서 아직 돌지 않는다.
- `POST /onboarding/commit` 미구현 — **사용자 눈에 보이는 다음 관문**이고, §116.5의 종단
  검증 조건이기도 하다.
- K8 미구현(master key·게이트웨이 토큰·manage key가 PVC 파일), 결함 15(checksum 애노테이션),
  lint 갭(AUD가 차트 ACL을 안 봄).

---

## §117 콘솔 흰 화면 — 모킹과 실물의 계약 불일치가 만든 사각지대

작성: 2026-07-29. 사용자 보고: *"`TypeError: Cannot read properties of null (reading 'length')`
이런 에러가 콘솔에 찍히던데, UI에서 최초에 화면이 렌더링 되자마자 바로 흰화면(빈화면)으로
전환되고있어."*

이 절이 기록하는 것은 버그 두 개가 아니라 **하나의 부류**다.

### 117.1 부류: 모킹은 옳고 실물만 계약을 어겼다

`console/src/api/types.ts`가 계약을 정확히 선언하고 있었다:
```ts
providers: ProviderCredentialSummary[]      // 35행 — nullable 아님
aliases: AliasSyncStatus[]                  // 36행 — nullable 아님
capabilityMappingRatio: number | null       // 66행 — 진짜 nullable 인 것은 nullable 로
```
즉 **계약은 처음부터 정밀했다.** 66행이 증거다 — 이 파일은 nullable을 구분해 쓸 줄 안다.

그리고 모킹(`api/mock/onboardingMock.ts`)은 **그 계약을 지켰다** — 항상 실제 배열을 반환한다.

어긴 것은 **실물 두 곳**이었다. 그래서 개발 중에는 한 번도 재현되지 않았다.

**교훈**: 모킹이 계약을 지키면 UI는 모킹 위에서 영원히 초록불이다. 계약을 어기는 것은
**실물뿐**이고, 실물을 붙여 실제로 눌러보기 전까지 그 사실을 알 방법이 없다. §114가
*"helm lint는 결함 14건 중 하나도 잡지 못했다"*고 기록한 것과 **같은 성질의 사건이다** —
검사 도구가 통과시키는 층과 실패가 사는 층이 다르다.

### 117.2 버그 A — Go의 nil 슬라이스가 `null`로 나갔다

```
Go:   Providers []map[string]string `json:"providers"`   ← nil
        ↓ Go는 nil 슬라이스를 [] 가 아니라 null 로 마샬링한다
JSON: "providers": null
        ↓
step0Logic.ts:14   if (step0.providers.length === 0)     ← 💥 렌더 중 예외
        ↓ React 가 트리를 언마운트 → 흰 화면
```
터지는 지점 4곳: `step0Logic.ts:14,15` · `steps/ProviderStep.tsx:28` · `steps/AliasStep.tsx:40,42`.

**수정은 원점에서** 했다 — `api/internal/onboarding/handlers.go`의 `step0Status()`를 빈 슬라이스로
초기화하고, **omniroute 미도달 등 모든 조기 반환 경로**에서 그것이 유지되게 했다. 신규 배포
직후에는 항상 그 경로를 지나므로 거기가 실제 발생 지점이었다. `UpsertAliasesResponse.results`도
같은 결함이 있어(decode 실패 시 nil 유지) 함께 고쳤다.

**`omitempty`로 고치지 않았다** — 그러면 필드가 사라져 `undefined`가 되고 같은 크래시가 난다.
**`types.ts`를 nullable로 낮추지도 않았다** — 그건 계약을 구현에 맞추는 역방향이다.

**인과관계를 증명했다**: `step0Logic.ts`만 esbuild로 번들해 node로 직접 돌려, `providers: null`에서
사용자가 보고한 그 `TypeError`가 **정확히 재현**되고 `[]`에서는 통과함을 확인했다. 응답만 고치고
"됐을 것"으로 넘기지 않았다.

### 117.3 버그 B — 와이어 래퍼를 벗기지 않았다 (같은 부류, 다음 단계에서 터질 자리)

계약의 authoritative 출처는 이 문서 **2426행의 표**다:
```
| /api/onboarding/step0/alias-defaults | GET | — | {entries:[{name,providerName,provider,model}]} |
```
따라서 **Go가 맞고 콘솔이 틀렸다.** `onboardingReal.ts`가 응답을 `AliasDefaultEntry[]`로 직접
기대해 `.entries`를 벗기지 않았다. 소비 지점은 `hooks/useOnboarding.ts:18`이고, **온보딩 별칭
단계에서 정확히 터질 자리**였다 — 사용자가 지금 가려는 경로다.

수정: 와이어 타입을 `{ entries: AliasDefaultEntry[] }`로 선언하고 `.then(r => r.entries ?? [])`로
벗긴다. **클라이언트 인터페이스는 배열 반환을 유지**했다.

**모킹은 건드리지 않았다.** 모킹은 *클라이언트 인터페이스*(배열 반환)를 구현하고 있고 래핑은
*와이어 모양*일 뿐이다 — 벗기는 책임은 실제 클라이언트에 있다. 모킹은 옳았다.

**전수 조사 결과**(`onboardingReal.ts` 7개 메서드 × 2422~2430행 표): 같은 부류는 `getAliasDefaults`
**하나뿐**이었다. `upsertAliases`는 표에도 `results` 래퍼가 있고 타입도 이미 `{results:[...]}`로
올바르게 선언돼 있었다. Go가 표를 어기는 곳은 **없었다.**

### 117.4 흰 화면 자체를 막았다 (안전망)

`console/src/components/ErrorBoundary.tsx` 신규. `main.tsx`에서 `ConfigProvider` 안·`AuthGate`
밖에 배치해 렌더 트리 전체를 감쌌다. 렌더 예외가 나면 **아무 정보 없는 흰 화면 대신** 오류
메시지와 다시 시도/새로고침이 보인다.

**왜 이것이 별개로 필요한가**: 원인이 무엇이든 흰 화면은 최악의 실패 형태다 — 사용자는 무엇이
잘못됐는지 알 수 없고, 개발자도 브라우저 콘솔을 열어야만 안다. 계약은 117.2/117.3에서 지켜졌고,
ErrorBoundary는 **계약이 또 깨질 때**의 층이다.

**의도적으로 하지 않은 것**: `?.`·`?? []`를 코드 전반에 흩뿌리지 않았다. `onboardingReal.ts`에
`providers`/`aliases`만 정규화하는 것도 하지 않았다 — 원점이 고쳐졌고 ErrorBoundary가 일반
안전망이므로 그 두 필드만 위한 특수 처리를 한 겹 더 쌓을 이유가 없다.

**검증 한계(정직하게)**: ErrorBoundary는 jsdom + 실제 클라이언트 렌더러로 검증했다. 에러
바운더리는 React의 알려진 제약으로 **SSR 문자열 렌더러에서는 동작하지 않아** 그 경로로는 검증이
불가능하다. **브라우저에서 눈으로 확인한 것은 아니다.**

### 117.5 배포 반영을 확인하는 방법 (결함 15의 네 번째 얼굴)

이미지를 다시 빌드하고 k3d로 import한 뒤 **파드를 지워야** 새 이미지가 뜬다. 이 프로젝트에서
"바꿨는데 반영 안 됨"이 이제 **네 번**이다(neo4j Secret, omniroute Secret, nats ConfigMap, 그리고
이번의 이미지).

**반영을 확인한 방법이 재사용 가치가 있다**: 번들 자산 파일명 해시가 바뀌는지 보고
(`index-CH9NvfF1.js` → `index-aXSpjGVV.js`), 나아가 **배포된 번들 안에서 수정 내용을 직접 grep**했다:
```
tAliasDefaults:()=>Io.get("/onboarding/step0/alias-defaults").then(e=>e.entries??[])
```
소스가 아니라 **서비스되는 산출물**에서 확인했으므로 "빌드는 했는데 배포는 안 된" 상태를 구분할 수 있다.

### 117.6 고치지 않고 남긴 것

**`types.ts`의 `AliasDefaultEntry`에 `providerName`이 없다.** 표(2426행)와 실제 응답에는 있다:
```json
{"name":"osiris/planner","providerName":"osiris-anthropic","provider":"anthropic","model":"claude-opus-5"}
```
코드베이스 어디서도 이 필드를 읽지 않아 **런타임 크래시 요인은 아니다**(grep 확인). 타입 선언과
와이어 모양의 드리프트이므로 **117.1의 부류에 속한다** — 지금은 무해하지만, 누군가 이 필드를
쓰려 할 때 타입이 막을 것이다. 후속으로 `providerName: string` 추가를 권한다.
