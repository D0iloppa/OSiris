import { Alert, Card, Space, Typography } from 'antd'
import { PageHeader } from '../layout/PageHeader'
import { neutral, radius } from '../theme/tokens'

const { Paragraph, Text } = Typography

/**
 * 작업 지도 — 프로젝트의 작업 노드를 그래프로 보는 화면.
 *
 * ── 이 화면이 아직 데이터를 그리지 못하는 이유 (숨기지 않고 적는다) ────────
 *
 * 참고 사례(BlueUrban 그룹웨어의 "작업 지도")는 화면이 아니라 **백엔드가
 * 본체**다. 그 구현은 `/api/progress/*` 한 벌에 얹혀 있다 — 프로젝트 목록,
 * 노드 CRUD, 그래프 조회, 정리(cleanup) 계획, 오케스트레이터 제안, 그리고
 * 에이전트가 노드를 직접 갱신하는 MCP 엔드포인트까지.
 *
 * osiris-api에는 그 대응물이 **하나도 없다.** 현재 API 표면은 온보딩·사람·
 * 기계주체·이벤트뿐이고(`api/internal/api/server.go`의 라우트 표), Neo4j가
 * 들고 있는 것은 조직 온톨로지(Person/Role/Capability/Permission)이지 작업
 * 노드 그래프가 아니다. 즉 이 화면은 "UI만 옮기면 되는" 종류가 아니라
 * **없는 데이터를 먼저 만들어야 하는** 종류다.
 *
 * 그래서 가짜 노드를 그려 두지 않는다. 빈 그래프를 멋지게 그려 놓으면
 * "연동만 남았다"처럼 읽히는데, 실제로 남은 일은 그 반대쪽이 거의 전부다.
 * 무엇이 있어야 이 화면이 살아나는지를 적어 두는 편이 정직하고, 다음
 * 작업자에게도 그게 더 쓸모 있다.
 */
export function WorkMapPage() {
  return (
    <div className="osiris-content-max" style={{ padding: '28px 38px 44px' }}>
      <PageHeader
        title="작업 지도"
        description="프로젝트의 작업 노드와 의존 관계를 그래프로 본다"
      />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 20 }}
        message="이 화면은 아직 그릴 데이터가 없습니다"
        description="UI가 아니라 백엔드가 먼저입니다 — osiris-api에 작업 노드 그래프를 다루는 표면이 아직 없고, Neo4j가 들고 있는 것은 조직 온톨로지(사람·역할·권한)이지 작업 노드가 아닙니다."
      />

      <Card
        style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}` }}
        styles={{ body: { padding: 24 } }}
      >
        <Text strong style={{ fontSize: 15 }}>
          이 화면이 살아나려면 필요한 것
        </Text>
        <Space direction="vertical" size={12} style={{ width: '100%', marginTop: 12 }}>
          <Req
            n="1"
            title="작업 노드 모델"
            body="프로젝트 / 노드(키·제목·타입·상태) / 노드 간 관계(구조 관계와 교차 관계). 온톨로지 스키마(20-ontology-schema.md)에 넣을지, 별도 저장소로 둘지가 첫 결정이다."
          />
          <Req
            n="2"
            title="읽기 API"
            body="그래프 한 벌을 내려주는 엔드포인트. 콘솔은 osiris-api의 /_api/* 만 호출하므로(§84 계약) 여기에 붙는다."
          />
          <Req
            n="3"
            title="쓰기 경로"
            body="노드 상태를 누가 갱신하는가. D1/G7/A5에 따라 Neo4j 쓰기는 osiris-core의 graph-write API만 가능하므로, 에이전트가 직접 노드를 갱신하려면 그 경로를 거쳐야 한다 — 참고 사례가 MCP 엔드포인트로 푼 지점이고, 여기서는 설계 제약이 다르다."
          />
          <Req
            n="4"
            title="실시간 갱신"
            body="노드 상태 변화를 SSE(/_api/events)로 흘릴지, 폴링으로 둘지. SSE 배선은 이미 있다(D19/D20)."
          />
        </Space>
      </Card>
    </div>
  )
}

function Req({ n, title, body }: { n: string; title: string; body: string }) {
  return (
    <div style={{ display: 'flex', gap: 12 }}>
      <div
        style={{
          flex: '0 0 24px',
          height: 24,
          borderRadius: 12,
          background: neutral.canvas,
          border: `1px solid ${neutral.border}`,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          fontSize: 12,
          fontWeight: 700,
          color: neutral.gray500,
        }}
      >
        {n}
      </div>
      <div style={{ minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 600, color: neutral.text }}>{title}</div>
        <Paragraph type="secondary" style={{ margin: 0, fontSize: 13 }}>
          {body}
        </Paragraph>
      </div>
    </div>
  )
}
