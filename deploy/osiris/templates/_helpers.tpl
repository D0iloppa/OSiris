{{/*
OSiris Helm chart 헬퍼.

★★★ 반드시 읽을 것 — 고정 Service 이름 세 개 ★★★

`10-core-architecture.md` §1.2 불변자산 10번: 에이전트 컨테이너가 소스/
설정에서 아는 호스트명은 정확히 세 개뿐이다 — `nats` · `osiris-core` ·
`osiris-egress`. 이 chart가 Helm의 관례(release 이름을 접두어로 붙이는
`{{ include "osiris.fullname" . }}-nats` 같은 패턴)를 따르면 이 불변식이
그 자리에서 깨진다 — 에이전트 이미지, orchestrator가 굽는 NO_PROXY 목록,
osiris-egress의 allowlist 어디에도 release 이름이 끼어들 자리가 없다.
그래서 아래 세 헬퍼는 `.Release.Name`을 절대 참조하지 않고 리터럴
문자열을 그대로 반환한다 — 이 chart를 어떤 release 이름으로 설치하든
Service 이름은 바뀌지 않는다.

나머지 컴포넌트(neo4j, osiris-api, osiris-orchestrator, osiris-omniroute,
osiris-console)는 에이전트가 몰라도 되는 이름이라 원칙적으로는
release-prefixed여도 무방하다. 그런데도 여기서 고정 이름을 쓰는 이유는
두 가지다:
  1) K1이 이미 "설치 패키지 하나, 클러스터당 단일 설치"를 전제한다 —
     같은 클러스터에 osiris를 여러 release로 나란히 설치하는 멀티테넌트
     시나리오를 이 설계 자체가 상정하지 않는다(§0 K1).
  2) neo4j는 core/api/orchestrator 세 컴포넌트 모두가 `NEO4J_URI` 기본값
     (`bolt://neo4j:7687`, 각 internal/config/config.go)으로 이미 그
     이름을 가정한다 — 바꾸려면 세 군데 env를 전부 override해야 하는데,
     굳이 그럴 이유가 없다.
*/}}

{{- define "osiris.natsServiceName" -}}
nats
{{- end -}}

{{- define "osiris.coreServiceName" -}}
osiris-core
{{- end -}}

{{- define "osiris.egressServiceName" -}}
osiris-egress
{{- end -}}

{{- define "osiris.neo4jServiceName" -}}
neo4j
{{- end -}}

{{/*
★ [2026-07-28 실배포 실측 — 결함 2] governing headless Service 이름.

neo4j/nats/osiris-core/osiris-omniroute 네 StatefulSet은 이제 Service를
**두 개**씩 갖는다: 클라이언트가 이름으로 붙는 일반 ClusterIP Service
(위의 `osiris.<x>ServiceName`, 예: "neo4j")와, StatefulSet.spec.serviceName
이 요구하는 governing headless Service(이 헬퍼들, "<x>-headless")다.

왜 나눴는지(실측 근거) — `neo4j`/`nats`/`osiris-core`/`osiris-omniroute`
Service가 전부 `clusterIP: None`(headless)이었을 때:
  $ nslookup neo4j.osiris-system.svc.cluster.local
  ** server can't find ...: NXDOMAIN
headless Service의 DNS는 **ready 엔드포인트가 있을 때만** 이름을
반환한다 — neo4j-0이 Ready가 되기 전에는 이름 자체가 존재하지 않는다.
그런데 osiris-core/osiris-orchestrator의 재시도 루프는 "이름은 항상
풀리고 TCP만 거부되는" 것을 전제로 짜여 있다(실측 로그 "neo4j not ready
yet … retrying in 2s" — 이 문구 자체가 그 전제의 증거다. compose에서는
`neo4j`가 컨테이너 기동 여부와 무관하게 항상 풀리는 이름이었다). 그
결과 core/orchestrator 모두 "lookup neo4j … server misbehaving" →
"fatal: context canceled"로 죽었다(둘 다 실측).

StatefulSet은 안정적 파드별 DNS(`neo4j-0.neo4j-headless...`)를 위해
governing Service가 headless이길 요구하므로(k8s 자체 제약), 이름을
없앨 수는 없다 — 대신 클라이언트용 이름(§1.2 불변자산 10번이 고정한
"nats"/"osiris-core" 포함)은 **일반 ClusterIP**로 별도로 두고, headless
쪽에는 그 이름을 절대 주지 않는다(StatefulSet.serviceName에서만 참조).
*/}}
{{- define "osiris.neo4jHeadlessServiceName" -}}
neo4j-headless
{{- end -}}

{{- define "osiris.natsHeadlessServiceName" -}}
nats-headless
{{- end -}}

{{- define "osiris.coreHeadlessServiceName" -}}
osiris-core-headless
{{- end -}}

{{- define "osiris.omnirouteHeadlessServiceName" -}}
osiris-omniroute-headless
{{- end -}}

{{- define "osiris.apiServiceName" -}}
osiris-api
{{- end -}}

{{- define "osiris.orchestratorServiceName" -}}
osiris-orchestrator
{{- end -}}

{{- define "osiris.omnirouteExternal" -}}
{{- if eq (.Values.omniroute.mode | default "embedded") "external" -}}
{{- $_ := required "omniroute.external.ip is required when omniroute.mode=external" .Values.omniroute.external.ip -}}true
{{- else if ne (.Values.omniroute.mode | default "embedded") "embedded" -}}
{{- fail "omniroute.mode must be embedded or external" -}}
{{- end -}}
{{- end -}}

{{- define "osiris.omnirouteServiceName" -}}
osiris-omniroute
{{- end -}}

{{- define "osiris.consoleServiceName" -}}
osiris-console
{{- end -}}

{{- define "osiris.systemNamespace" -}}
osiris-system
{{- end -}}

{{- define "osiris.agentsNamespace" -}}
osiris-agents
{{- end -}}

{{/* 자격증명 Secret 이름 — existingSecretName이 채워지면 그것을 참조하고,
아니면 이 chart가 렌더하는 osiris-secrets를 쓴다(templates/secrets.yaml). */}}
{{- define "osiris.secretName" -}}
{{- if .Values.secrets.existingSecretName -}}
{{ .Values.secrets.existingSecretName }}
{{- else -}}
osiris-secrets
{{- end -}}
{{- end -}}

{{- define "osiris.commonLabels" -}}
app.kubernetes.io/part-of: osiris
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
K4 — 코어 노드 고정 nodeAffinity 조각. osiris-core/neo4j/nats 세
StatefulSet의 pod spec에서만 include한다("코어는 노드 고정", §2 배치도).
requiredDuringScheduling(soft가 아니라 hard)을 쓰는 이유: K4는 "가능하면
같은 노드"가 아니라 "반드시 그 노드"를 요구한다 — 대가(메인 노드를 잃으면
컨트롤 플레인이 죽는다)를 흐리지 않고 그대로 드러내는 편이 soft affinity로
숨기는 것보다 정직하다.

★ [2026-07-28 실배포 실측 — 결함 1] `global.pinCoreToMainNode`(기본
false)로 이 required affinity 자체를 켜고 끈다.

실측: 단일 노드 k3d 클러스터에 이 chart를 그대로 설치하면 neo4j-0/
nats-0/osiris-core-0가 전부 `Pending`으로 멈춘다.
  FailedScheduling: 0/1 nodes are available: 1 node(s) didn't match
  Pod's node affinity/selector.
`osiris.dev/role=main` 라벨은 **chart가 붙일 수 없다** — 노드는
클러스터 스코프 객체이고 이 chart의 렌더 대상이 아니다. 그러면서도
required affinity를 무조건 렌더하면, "라벨을 아직 아무도 붙이지 않은
채 기본 설치"라는 정상 경로(단일 노드 dev 클러스터, 또는 아직 노드
토폴로지를 안 정한 최초 설치)가 100% 실패한다 — K4(코어 노드 고정)가
멀티노드에서 지키려는 불변식이, 아직 멀티노드가 아닌 설치를 걷어차는
부작용을 낳는다.

그래서 K4 자체는 버리지 않되(멀티노드로 갈 때 여전히 필요하다),
required affinity 렌더 여부를 스위치로 뺐다:
  - `pinCoreToMainNode: false`(기본) — affinity를 아예 렌더하지 않는다.
    단일 노드에는 애초에 불필요하고(스케줄러가 갈 곳이 하나뿐이다),
    "라벨이 없으면 설치가 죽는다"는 함정을 기본값에서 없앤다.
  - `pinCoreToMainNode: true` — 이 조각을 그대로 렌더한다(여전히
    required, soft로 완화하지 않는다 — required가 대가를 숨기지
    않는다는 근거는 그대로 유효하다). **멀티노드로 갈 때는 설치자가
    먼저 `kubectl label node <메인-노드> osiris.dev/role=main`을 하고
    이 값을 true로 켜야 한다** — README.md "멀티노드 전환" 절 참고.
*/}}
{{- define "osiris.mainNodeAffinity" -}}
{{- if .Values.global.pinCoreToMainNode }}
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: {{ .Values.global.nodeAffinityKey }}
              operator: In
              values:
                - {{ .Values.global.nodeAffinityValue }}
{{- end -}}
{{- end -}}
