// OSiris 온톨로지 스키마 — 제약(constraint) 과 인덱스.
// 출처: docs/design/20-ontology-schema.md §11 (원문 그대로, 손대지 않음).
// 이 파일은 순수 DDL(구조)이다. 조직 의도 데이터(카탈로그 시드, OrgRevision=0 등)는
// 여기 넣지 않는다 — 그것은 graph-write API 경유가 원칙이다(D17, G7).
// 자세한 사유는 docs/design/IMPLEMENTATION-NOTES.md 참조.

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
// [2026-08-12 신설, P2 stage ②] docs/design/80-action-layer.md §4.7 (원문
// 그대로). :ActionType.id/.code 유일성 — AC1. 순수 추가, 기존 제약 무변경.
CREATE CONSTRAINT actiontype_id   IF NOT EXISTS FOR (n:ActionType) REQUIRE n.id   IS UNIQUE;
CREATE CONSTRAINT actiontype_code IF NOT EXISTS FOR (n:ActionType) REQUIRE n.code IS UNIQUE;

// --- 존재성 (CE에서는 미지원 → 애플리케이션 검증. 표기만 남긴다) ---
// :Party.name, :WorkItem.status, :AgentRuntime.observedAt 는 NOT NULL 이어야 한다.
// graph-write API가 스키마 검증을 대신한다(D17이 이 검증의 단일 지점인 이유).

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
// [2026-08-12 신설, P2 stage ②] docs/design/80-action-layer.md §4.7 (원문
// 그대로). :ActionType 인덱스 4종 + §4.3의 :Artifact.kind 확장(오염 계보
// 질의용) 인덱스 1종. 순수 추가.
CREATE INDEX actiontype_status  IF NOT EXISTS FOR (n:ActionType) ON (n.status);
CREATE INDEX actiontype_gate    IF NOT EXISTS FOR (n:ActionType) ON (n.gateClass, n.taintClass);
CREATE INDEX actiontype_rev     IF NOT EXISTS FOR (n:ActionType) ON (n.createdAtRevision, n.closedAtRevision);
CREATE INDEX actiontype_origin  IF NOT EXISTS FOR (n:ActionType) ON (n.origin);
CREATE INDEX artifact_kind      IF NOT EXISTS FOR (n:Artifact)   ON (n.kind, n.createdAt);
CREATE FULLTEXT INDEX ontology_search IF NOT EXISTS
  FOR (n:Party|WorkItem|Capability|Role|Asset) ON EACH [n.name, n.title, n.label, n.code];
