# OSiris

AI 조직 런타임 — 1단계(docker-compose) 인프라 골격.

설계 문서: [`docs/design/10-core-architecture.md`](docs/design/10-core-architecture.md) (필독,
특히 §1.7 금지 목록), [`docs/design/20-ontology-schema.md`](docs/design/20-ontology-schema.md),
[`docs/design/30-onboarding-provisioning.md`](docs/design/30-onboarding-provisioning.md),
[`docs/design/40-console-ia.md`](docs/design/40-console-ia.md).

구현 판단·설계와의 차이는 [`docs/design/IMPLEMENTATION-NOTES.md`](docs/design/IMPLEMENTATION-NOTES.md)에 기록되어 있다.

## 현재 이 저장소가 실제로 띄우는 것

이번 단계는 **플랫폼 계층과 진입 경로까지**다. 애플리케이션 로직(`osiris-core`,
`osiris-orchestrator`, 에이전트, 콘솔 SPA, 배정 엔진)은 다음 단계의 몫이며
`docker-compose.yml`에 주석으로 자리만 잡혀 있다.

| 서비스 | 상태 | 역할 |
|---|---|---|
| `osiris-gw` | 구현됨 | 안정적 이음새(nginx). 상위 리버스 프록시로부터 받아 내부로 라우팅, SSE 통과, 플레이스홀더 상태 페이지 서빙 |
| `neo4j` (컨테이너명 `osiris-neo4j`) | 구현됨 | 온톨로지. 제약/인덱스만 적용됨(카탈로그 시드는 osiris-core 몫) |
| `nats` (컨테이너명 `osiris-nats`) | 구현됨 | JetStream. 인증/ACL은 미도입(알려진 격차) |
| `osiris-egress` | 구현됨 | HTTP CONNECT 프록시, 도메인 허용목록, 소스 CIDR ACL |
| `osiris-api` / `osiris-core` / `osiris-orchestrator` | **미구현** | 다음 워커. compose 하단에 주석으로만 존재 |

## 디렉토리 구조

```
docker-compose.yml     코어 서비스 정의 (에이전트/조직 정보 0줄 — G4)
osiris-up              부트스트랩 스크립트 (§4.1)
.env.example           설정 예시 (실제 값은 .env, 커밋 금지)
gw/                    osiris-gw (nginx) 설정 + 플레이스홀더 상태 페이지
neo4j/                 스키마(제약/인덱스) + 적용 스크립트
nats/                  JetStream 설정
egress/                squid 기반 도메인 허용목록 egress 프록시 설정
bin/verify-isolation.sh  격리 검증 스크립트
state/                 (gitignore) creds/config/backup — orchestrator 전용, 0700
docs/design/           설계 문서 (비범위: 이 저장소가 코드로 옮기는 대상)
```

## 빠른 시작

```
cp .env.example .env && $EDITOR .env
./osiris-up
```

전제조건: 호스트에 `dev-net`(external bridge) 네트워크가 이미 있어야 하고,
상위 게이트웨이(`<upstream-proxy>`)에 `osiris.example.com` 서브도메인 conf가 이미 설치되어
있어야 한다(이 저장소는 그 파일을 절대 건드리지 않는다).

## 격리 검증

```
./bin/verify-isolation.sh
```

`osiris-agents` 네트워크에만 붙은 임시 컨테이너가 Neo4j·dev-net에는 도달하지
못하고, `nats`·`osiris-egress`(허용목록 안)에는 도달하는지, 그리고 `dev-net`에서
`osiris-egress`를 남의 오픈 프록시처럼 쓰려는 시도가 거부되는지 확인한다.

## 재현성

```
docker compose down
docker compose up -d
```

상태는 named volume(`neo4j-data`, `nats-data`)에 있으므로 컨테이너를 내렸다
올려도 데이터가 보존된다. 호스트 경로 bind mount는 워크스페이스 어디에도 없다(G1).
