# 41. OSiris 콘솔 디자인 시스템 스펙 (Console Design System Spec)

> **적용범위**: OSiris 관리 콘솔(WebUI) 전체 — `40-console-ia.md`가 정의한 화면 9개(Admin Chat / Org / Work / Approvals / Ops / Onboarding / Ontology / My Tasks / Notifications). 콘솔 SPA·`osiris-api`는 아직 코드가 없다 — 이 문서는 **그린필드 착수 기준 설계 스펙**이다, 마이그레이션 문서가 아니다.
> **디자인 표준**: `<repo-root>/DESIGN-admin.md`(원본 앱 관리자 콘솔용, 375줄)의 톤 운용 체계를 계승한다. 원본은 사용자 소유이며 이 문서는 그 파일을 **읽기만** 했고 수정하지 않았다.
> **스택**: React 18.3 + Vite 5 + antd 5.21 + @tanstack/react-query + recharts 3. 새 스타일링 툴체인(Tailwind 등) 도입 없음 — antd `ConfigProvider theme` + CSS 변수가 유일한 토큰 공급 경로.
> **표기 규칙**: 표기 없는 서술 = 이 문서의 확정 결정. `[미검증]` = 확인 필요한 가정. 대비 비율은 전부 WCAG 상대휘도 공식(sRGB → 선형화 → `0.2126R+0.7152G+0.0722B`)으로 직접 계산한 실측값이며, 계산 스크립트는 `/tmp/claude-1000/-mnt-c-DEV-OSiris/c082df91-ddf9-4c15-910f-80e02f8c5ca2/scratchpad/contrast.js`(임시 산출물, 저장소에는 없음)로 재현 가능하다.

---

## 0. 무엇을 계승하고 무엇을 바꾸는가 (요약)

`DESIGN-admin.md`가 8장의 QueryMind 레퍼런스에서 뽑아낸 것은 **톤 운용 체계**(라이트 캔버스, 틴트 상태 표현, 보더 우선, 넉넉한 밀도, 회색 위계)였고, 그 문서 스스로 "이는 hue와 무관하게 성립한다"고 명시했다(§3). OSiris는 이 논리를 그대로 적용해 **hue만 라피스 블루로 교체**하고, 톤 운용 체계·antd 이관 방식·컴포넌트 추출 전략은 그대로 가져온다. 상세 차이표는 §9.

핵심 결정(재확인, 재논의 대상 아님):
- primary = 라피스 블루 `#1B4F9C`(600 앵커), 후순 단계 `#143C78`(700), 틴트 `#EAF0FA`(50), 캔버스 `#F7F9FC`.
- 금 `#E8B54B`은 **마크 전용**(파비콘·로고·어두운 색면 위 브랜드 마크). primary로 쓰지 않는다 — 백색 위 대비 1.88:1로 AA 미달(§2.1.3 실측).
- 콘솔은 `/`(정확히 그 경로)에 루트 마운트, 클라이언트(브라우저) 쪽 base URL은 `/_api/*`(SSE는 `/_api/events`) — `osiris-gw`가 `/_api/*`를 `/api/*`로 벗겨서 `osiris-api`에 전달한다. `osiris-api`의 **내부** 라우트 자체는 여전히 `/api/*`다(`10-core-architecture.md` §2.9, I5 3차 수정 — 원래 §2.2가 확인했던 `/api/*`가 omniroute 소스의 리터럴 절대경로 233개와 충돌해 `/_api/`로 이전됨).

---

## 1. 디자인 원칙 — 계승, 무변경

`DESIGN-admin.md` §1의 5원칙을 그대로 채택한다(라이트 저대비 캔버스·틴트로 상태 표현·보더 우선·넉넉한 밀도·회색 위계). OSiris 고유로 추가하는 원칙 2개:

6. **상태는 항상 이중 인코딩.** `40-console-ia.md`가 Org 화면에서 이미 못박은 원칙("색만으로 상태를 구분하지 않음")을 콘솔 전역 규칙으로 승격한다 — 색맹 사용자에게 색만으로 의미를 전달하는 지점을 만들지 않는다. 상태 팔레트(§2.3)는 항상 아이콘/모양 또는 텍스트 라벨과 함께 나타난다.
7. **실시간은 조용히, 확실하게.** SSE로 들어오는 갱신은 깜빡임·색 플래시 같은 시선 강탈형 애니메이션이 아니라 낮은 진폭의 펄스·페이드로 표현한다(§7). 대표 1인이 하루종일 켜두는 화면이므로 "실시간감"이 피로감이 되지 않게 한다.

---

## 2. 디자인 토큰

토큰 소스 전략은 `DESIGN-admin.md` §2와 동일하다 — **`src/theme/tokens.ts` 단일 소스**, antd `ConfigProvider theme={{ token, components, cssVar: true }}` + CSS 변수(`--osiris-*`) 이원 공급. **CSS 변수 프리픽스는 `--admin-*`가 아니라 `--osiris-*`로 바꾼다** — 콘솔 이름이 다르고, 장기적으로 `osiris-webui`가 다른 example.com 계열 콘솔과 같은 인프라를 공유할 가능성이 있어 네임스페이스를 처음부터 분리한다(§9 차이표 항목).

> 규칙 계승: **antd 토큰 키가 존재하면 그것이 정본**이고 CSS에서는 `var(--ant-…)`를 참조한다. `--osiris-*`는 antd가 커버하지 못하는 개념(사이드바 세부, 차트, 틴트, 좌석 타일 등)에만 만든다.

### 2.1 컬러 — 브랜드 (라피스 블루)

#### 2.1.1 10단계 스케일

사용자 기준값 `#1B4F9C`(anchor, 600)·`#143C78`(700)·`#EAF0FA`(틴트, 50)·`#F7F9FC`(캔버스)를 출발점으로, `DESIGN-admin.md`의 teal 스케일과 같은 구조(hue 고정, saturation 완만한 곡선, lightness 단조 감소)로 나머지 8단계를 생성했다. 생성 방식: HSL에서 hue=216(주어진 두 값의 평균 hue), saturation 62~71%로 완만하게, lightness는 아래 표의 실측값.

| 토큰 | 값 | H / S / L | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|---|
| lapis-50 | `#EAF0FA` (기준값) | 217.5 / 61.5 / 94.9 | `--osiris-lapis-50` | 선택 pill 배경, hover 틴트, 콜아웃 배경 |
| lapis-100 | `#D1DFF5` | 216 / 64 / 89 | `--osiris-lapis-100` | 아이콘 타일 배경, 아바타 배경 |
| lapis-200 | `#A6C2ED` | 216 / 66 / 79 | `--osiris-lapis-200` | 선택 pill 보더(필요 시), 차트 보조 |
| lapis-300 | `#729FE4` | 216 / 68 / 67 | `--osiris-lapis-300` | 비활성 primary 강조, 다크모드 primary 후보 |
| lapis-400 | `#266ED9` | 216 / 70 / 50 | `--osiris-lapis-400` | 포커스 링, 그래프 강조, hover 상태 |
| lapis-500 | `#1F5BB5` | 216 / 71 / 41.5 | `colorPrimaryHover` 근사 | 버튼 hover(밝게) |
| **lapis-600** | **`#1B4F9C`** (기준값) | 215.8 / 70.5 / 35.9 | **`colorPrimary`** → `--ant-color-primary` | **primary 버튼·선택 상태·링크 강조 (정본)** |
| lapis-700 | `#143C78` (기준값) | 216 / 71.4 / 27.5 | `colorLink`, 버튼 active | 링크, primary active/pressed |
| lapis-800 | `#112E5A` | 216 / 68 / 21 | `--osiris-lapis-800` | 진한 강조 텍스트, 다크 색면(로그인/사이드바 브랜드 영역 배경 후보) |
| lapis-900 | `#0F223E` | 216 / 62 / 15 | `--osiris-lapis-900` | 가장 어두운 브랜드 색면 — 금 마크가 놓이는 배경 후보 |

**기준값에서 조정한 것**: lapis-400을 초기 계산값(L53, `#3376DB`)에서 **L50(`#266ED9`)으로 낮춤** — 조정 전 값은 백색 위 대비 4.42:1로 AA(4.5:1, 일반 텍스트) 미달이었다(§2.1.3에서 실측). 아이콘/포커스링 전용이라면 문제 없었겠지만 텍스트로도 쓰일 가능성을 차단하기 위해 4.87:1로 여유를 확보했다. 이 외 계단은 조정 없음 — 600/700은 사용자 기준값을 그대로 앵커로 썼고, 50(틴트)도 기준값 그대로다.

**힌트**: 뉴트럴 스케일(§2.2)의 hue를 실측했더니 이미 210~222° 대(gray-500 `#64748B` = H215.4)로, lapis(216)와 사실상 같은 계열이었다 — DESIGN-admin.md의 slate 그레이가 원래 "약간 블루가 도는 회색"이었기 때문에, **뉴트럴 스케일은 hue 조정 없이 그대로 재사용**해도 라피스와 자연스럽게 어울린다. 이는 계획한 것이 아니라 실측 후 발견한 우연한 정합이며, 이 문서에서 뉴트럴 값을 손대지 않은 근거다.

#### 2.1.2 금 — 마크 전용, 이유와 제약

| 토큰 | 값 | 용도 |
|---|---|---|
| gold-mark | `#E8B54B` | `brand/osiris-mark.svg`(호루스의 눈, `currentColor` 상속) 전용. **버튼·링크·배지·차트 등 어떤 UI 색으로도 쓰지 않는다.** |
| badge-dark | `#0F1419` | 마크가 놓이는 표준 어두운 배지 배경(`brand/README.md` 기준값과 동일) |

근거는 `brand/README.md`가 이미 명시한 배색(호루스의 눈 = 금+검정 배지)과 §3에 준 사용자 결정을 그대로 따른다. 규칙: 금은 "어두운 색면 위"에서만 쓴다 — lapis-800/900 또는 badge-dark 계열. 라이트 캔버스(F7F9FC/백색) 위에는 절대 놓지 않는다.

#### 2.1.3 대비 실측 — primary 스케일

| 쌍 | 대비 | 기준 | 판정 |
|---|---|---|---|
| lapis-600(`#1B4F9C`) 텍스트/버튼면 vs 백색 | **7.94:1** | 텍스트 AA 4.5:1 | PASS (AAA 7:1도 충족) |
| lapis-500(hover, `#1F5BB5`) vs 백색 | **6.52:1** | 텍스트 AA 4.5:1 | PASS |
| lapis-700(active/link, `#143C78`) vs 백색 | **10.78:1** | 텍스트 AA 4.5:1 | PASS |
| lapis-400(focus ring/아이콘, `#266ED9`, 조정값) vs 백색 | **4.87:1** | 비텍스트 3:1 / 텍스트 AA 4.5:1 | PASS (조정 전 `#3376DB`는 4.42:1로 텍스트 AA 미달 — §2.1.1 참고) |
| lapis-600 텍스트 vs lapis-50 틴트(`#EAF0FA`, 선택 메뉴 pill) | **6.93:1** | 텍스트 AA 4.5:1 | PASS |
| lapis-700 텍스트 vs lapis-50 틴트 | **9.42:1** | 텍스트 AA 4.5:1 | PASS |
| gold-mark(`#E8B54B`) vs 백색 | **1.88:1** | 텍스트 AA 4.5:1 | **FAIL — 그래서 primary로 쓰지 않는다(사용자 결정과 일치)** |
| gold-mark vs badge-dark(`#0F1419`) | **9.82:1** | 텍스트 AA 4.5:1 | PASS — 어두운 배지에서만 쓰는 이유 |
| gold-mark vs lapis-900(`#0F223E`) | **8.45:1** | 텍스트 AA 4.5:1 | PASS — 로그인/사이드바 브랜드 영역을 lapis-900으로 어둡게 깔면 그 위에도 금을 쓸 수 있다는 근거 |
| gold-mark vs lapis-800(`#112E5A`) | **7.13:1** | 텍스트 AA 4.5:1 | PASS |

결론: primary 스케일은 조정 후 **AA 미달 없음**. gold는 애초에 라이트 면 사용을 배제하는 설계이므로 "미달"이 아니라 "용도 제한의 근거"로 남긴다.

### 2.2 컬러 — 뉴트럴 (slate 계열, `DESIGN-admin.md`에서 무변경 계승)

값은 admin 문서와 동일하되 CSS 변수만 `--osiris-*`로 바꾼다. 캔버스만 사용자 지정값 `#F7F9FC`로 교체(admin의 `#f5f7fa`는 원본 앱 고유값이라 유지할 이유가 없음).

| 토큰 | 값 | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|
| white | `#ffffff` | `colorBgContainer` | 카드·사이드바·탑바·테이블 |
| gray-50 | `#f8fafc` | `--osiris-gray-50` | 테이블 헤더, Descriptions 라벨 배경 |
| **layout-bg(canvas)** | **`#F7F9FC`** (사용자 지정) | `colorBgLayout` | 컨텐츠 영역 배경 |
| gray-100 | `#f1f5f9` | `--osiris-gray-100` | filled input 배경, neutral 시맨틱 틴트(§2.3) |
| gray-200 | `#e2e8f0` | `--osiris-gray-200` | 분리선 진한 단계 |
| border | `#e3e8ef` | `colorBorder` | 카드·테이블·입력 보더 |
| border-light | `#edf0f4` | `colorBorderSecondary` | 테이블 행 구분선, 카드 헤더 하단선 |
| gray-300 | `#cbd5e1` | `--osiris-gray-300` | 비활성 아이콘 |
| gray-400 | `#94a3b8` | `colorTextQuaternary` | placeholder, 비활성 텍스트(WCAG 예외 대상) |
| gray-500 | `#64748b` | `colorTextTertiary` | 캡션·보조 텍스트 |
| gray-600 | `#475569` | `colorTextSecondary` | 서브타이틀, **neutral 시맨틱 본색으로 겸용(§2.3)** |
| gray-700 | `#334155` | `--osiris-gray-700` | 테이블 본문 텍스트 |
| text | `#172033` | `colorText` | 기본 텍스트 |
| gray-900 | `#0f172a` | `--osiris-gray-900` | 헤드라인, KPI 숫자 |

대비 실측(전부 백색 카드 기준):

| 쌍 | 대비 | 판정 |
|---|---|---|
| colorText(`#172033`) vs 백색 | 16.27:1 | PASS |
| colorTextSecondary(`#475569`) vs 백색 | 7.58:1 | PASS |
| colorTextTertiary(`#64748b`) vs 백색 | 4.76:1 | PASS (AA 4.5 여유 0.26 — 근소하나 통과) |
| colorTextQuaternary(`#94a3b8`) vs 백색 | 2.56:1 | 미달이나 **WCAG 예외**(플레이스홀더/비활성 텍스트는 1.4.3 대상 아님) |
| gray-900(`#0f172a`) vs 백색 | 17.85:1 | PASS |
| colorBorder(`#e3e8ef`) vs 백색 | 1.23:1 | 의도적 저대비 — §2.2.1 참고 |

#### 2.2.1 보더 대비에 대한 판단

`colorBorder` vs 백색은 1.23:1로 WCAG 1.4.11(비텍스트 대비 3:1)에 크게 못 미친다. 이것은 결함이 아니라 **`DESIGN-admin.md`가 못박은 "보더 우선, 그림자는 보조" 철학의 의도된 결과**다 — 헤어라인 보더는 카드·테이블 행을 나누는 순수 레이아웃 장식으로, WCAG 1.4.11이 요구하는 "컴포넌트의 상태·경계를 식별하는 데 필수적인 정보"에 해당하지 않는다(GitHub·Linear·Notion 등 동종 SaaS 대시보드의 헤어라인도 대개 1.1~1.3:1 대역이다). 다만 **경계가 상태를 나르는 지점**(포커스 링, 선택된 CheckableTag, invalid 입력 필드 보더)은 별도로 3:1 이상을 만족해야 한다 — 이 콘솔은 그 역할을 보더가 아니라 **배경 틴트**로 옮겨 해결한다(선택 메뉴 = 틴트 배경, admin §5.1과 동일 전략)이므로 실질적으로 "상태를 나르는 보더"가 거의 없다. 유일하게 남는 것은 포커스 링인데, 여기엔 lapis-400(4.87:1, §2.1.3)을 명시적으로 지정해 3:1 요건을 확보한다.

### 2.3 컬러 — 시맨틱 (본색 + 틴트 쌍)

`DESIGN-admin.md`의 구조("틴트 배경 + 본색 텍스트" pill)를 그대로 따르되, **값은 재계산했다** — admin 문서 자체가 대비 실측을 하지 않은 채 값을 정했고, OSiris 캔버스·용도에 그대로 가져와 계산해보니 두 곳에서 AA 미달이 나왔다(아래 §2.3.1).

| 의미 | 본색(최종) | 틴트 배경 | antd 키 | 비고 |
|---|---|---|---|---|
| success | `#15793a` | `#e8f7ee` | `colorSuccess` | admin값 `#16a34a`에서 **조정**(§2.3.1) |
| warning | `#9b5608` | `#fdf3e3` | `colorWarning` | admin값 `#d97706`에서 **조정** |
| error / critical | `#c4271c` | `#fdecec` | `colorError` | admin값 `#dc2626`에서 **조정**. `40-console-ia.md`가 Org/Ops에서 쓰는 "critical" 용어는 이 토큰의 별칭이다(색은 하나, 이름만 문맥에 따라 error/critical) |
| info | `#722ed1` | `#f1ebf9` | `colorInfo` | **hue 자체를 변경**(§2.3.2) — admin은 파랑(`#2563eb`)이었으나 OSiris는 primary가 이미 파랑(lapis)이라 그대로 쓰면 시맨틱 정보색과 브랜드색이 겹친다 |
| neutral(기각·완료 등 종결) | `#475569`(= gray-600 재사용) | `#f1f5f9`(= gray-100 재사용) | — | 새 색을 만들지 않고 기존 뉴트럴 스케일에서 대비가 충분한 단계를 그대로 가져다 씀 |

#### 2.3.1 success/warning/error 조정 — 왜, 그리고 실측

admin 문서가 정의한 원래 값(success `#16a34a`, warning `#d97706`, error `#dc2626`)을 그대로 OSiris에 가져와 "본색 텍스트 on 틴트 배경"(pill 라벨, 11px, 일반 텍스트 취급 — 아이콘이 붙어도 텍스트 자체의 4.5:1 요건은 면제되지 않는다) 조합으로 계산하면:

| 쌍(조정 전) | 대비 | 판정 |
|---|---|---|
| success `#16a34a` vs 자기 틴트 `#e8f7ee` | 2.98:1 | **FAIL** (텍스트 AA 4.5, 비텍스트 3:1도 미달) |
| warning `#d97706` vs 자기 틴트 `#fdf3e3` | 2.90:1 | **FAIL** |
| error `#dc2626` vs 자기 틴트 `#fdecec` | 4.23:1 | **FAIL**(텍스트 AA 4.5 기준. 비텍스트 3:1은 통과) |
| success `#16a34a` vs 백색 | 3.30:1 | FAIL(텍스트 AA) |
| warning `#d97706` vs 백색 | 3.19:1 | FAIL(텍스트 AA) |
| error `#dc2626` vs 백색 | 4.83:1 | PASS(텍스트 AA) |

**세 색 모두 hue는 유지한 채 lightness만 낮춰(더 진하게) 재계산**했다. 조정 후:

| 쌍(조정 후) | 대비 | 판정 |
|---|---|---|
| success `#15793a` vs 백색 | 5.49:1 | PASS |
| success `#15793a` vs 자기 틴트 `#e8f7ee` | 4.96:1 | PASS |
| warning `#9b5608` vs 백색 | 5.63:1 | PASS |
| warning `#9b5608` vs 자기 틴트 `#fdf3e3` | 5.12:1 | PASS |
| error `#c4271c` vs 백색 | 5.75:1 | PASS |
| error `#c4271c` vs 자기 틴트 `#fdecec` | 5.03:1 | PASS |

틴트 배경 hex는 admin값을 그대로 유지했다(틴트 자체는 배경일 뿐 텍스트 대비 계산의 대상이 아니므로 조정 불필요, 조정은 본색에서만).

#### 2.3.2 info를 파랑에서 보라로 바꾼 이유

`DESIGN-admin.md` §2.3은 "teal은 브랜드/인터랙션, 파랑은 정보 시맨틱"이라며 info를 파랑(`#2563eb`)으로 분리했다 — teal과 파랑이 서로 다른 hue라서 성립하던 논리다. OSiris는 **primary 자체가 파랑(lapis)**이므로 이 매핑을 그대로 가져오면 "브랜드 인터랙션색"과 "정보 시맨틱색"이 같은 hue 계열이 되어 admin이 피하려던 문제(§2.3의 원래 teal=info 겸용 문제)가 그대로 재발한다. 그래서 info는 **hue를 보라 계열(H≈265)로 이동**했다: `#722ed1`(antd 프리셋 purple-6과 유사한 계열). 실측: 백색 대비 6.94:1, 자체 틴트(`#f1ebf9`) 대비 5.94:1 — 둘 다 AA 여유 충분. primary(H216)와 hue 거리 약 49°로 육안 구분이 뚜렷하다.

#### 2.3.3 상태 팔레트의 색맹 안전성 — 발견한 문제와 완화

`dataviz` 스킬의 카테고리 팔레트 검증기(6-체크)로 success/warning/error/info 4색을 함께 검사해봤다(참고용 — 이 4색은 "카테고리"가 아니라 "상태" 색이라 스킬이 명시한 검증 범위 밖이지만, 색맹 위험을 놓치지 않기 위해 돌려봤다):

```
node scripts/validate_palette.js "#15793a,#9b5608,#c4271c,#722ed1" --mode light --surface "#F7F9FC" --pairs all
→ [FAIL] CVD separation      worst all-pairs #c4271c↔#9b5608 ΔE 1.6 (deutan)
→ [FAIL] Normal-vision floor  worst all-pairs #c4271c↔#9b5608 ΔE 10.9 (< 15 기준)
```

**warning(주황빛 갈색)과 error/critical(빨강)이 적록색맹(deuteranopia)에서 거의 구분되지 않는다**(ΔE 1.6 — 사실상 동일 색으로 보임). 이는 orange-red 축이 적록색맹의 혼동선과 거의 겹치기 때문에 생기는, 거의 모든 디자인 시스템이 안고 있는 공통 현상이다(Ant Design·Material 등도 warning/error를 hue만으로는 구분 못 하게 설계돼 있다). hue를 더 벌리는 조정(warning을 노랑 쪽으로)도 검토했으나, 그러면 금 마크(`#E8B54B`, H40.5)와 hue가 너무 가까워져 "금은 마크 전용"이라는 신호가 흐려지는 새 문제가 생긴다 — 트레이드오프상 **hue는 그대로 두고, 완화책을 이중 인코딩으로 확정**했다: warning과 error는 절대 색만으로 구분하지 않는다 — **아이콘 모양을 다르게 한다**(warning=삼각형 느낌표 `⚠`, error/critical=원형 X `⊗`). 이는 `40-console-ia.md`가 이미 Org 화면에 요구한 "색 + 아이콘 이중 인코딩" 원칙을 warning/error 쌍에도 그대로 적용하는 것뿐이다. 값 변경은 하지 않았다 — 이 문서 §2.3의 최종값이 그대로 유효하다.

### 2.4 컬러 — 차트 팔레트 (recharts 정합)

`dataviz` 스킬을 먼저 읽고 그 절차(형태 선택 → 색은 마지막 → 6체크 검증 → 마크 스펙 → 인터랙션 → 접근성 재확인)를 따랐다. 스킬이 제공하는 **레퍼런스 카테고리 팔레트**(8색, 이미 두 모드에서 검증됨)를 hex 변경 없이 그대로 채택하고, **우리 캔버스(`#F7F9FC`)를 서피스로 다시 검증**했다(스킬은 값을 바꿀 때만 재검증을 요구하지만, 서피스가 바뀌면 대비 체크는 서피스에 의존하므로 다시 돌렸다).

#### 2.4.1 카테고리 팔레트 (identity — 시리즈 구분)

| 슬롯 | hue | 라이트 | 다크(참고, 미확정 — §8) |
|---|---|---|---|
| chart-1 | blue | `#2a78d6` | `#3987e5` |
| chart-2 | orange | `#eb6834` | `#d95926` |
| chart-3 | aqua | `#1baf7a` | `#199e70` |
| chart-4 | yellow | `#eda100` | `#c98500` |
| chart-5 | magenta | `#e87ba4` | `#d55181` |
| chart-6 | green | `#008300` | `#008300` |
| chart-7 | violet | `#4a3aa7` | `#9085e9` |
| chart-8 | red | `#e34948` | `#e66767` |

**검증(직접 실행, 라이트 · surface `#F7F9FC`)**:

```
$ node scripts/validate_palette.js "#2a78d6,#eb6834,#1baf7a,#eda100,#e87ba4,#008300,#4a3aa7,#e34948" --mode light --surface "#F7F9FC"
[PASS] Lightness band
[PASS] Chroma floor
[PASS] CVD separation         worst adjacent #eda100↔#1baf7a ΔE 9.1 (protan)
[PASS] Normal-vision floor    worst adjacent #e87ba4↔#eda100 ΔE 19.6
[WARN] Contrast vs surface    below 3:1: #1baf7a(2.67) #eda100(2.05) #e87ba4(2.55) → 완화 필요(라벨/표 대안)
```

전체 8색은 인접쌍(막대/라인/스택) 기준 모든 하드 게이트 통과. 3개 슬롯(아쿠아·옐로우·마젠타)은 우리 캔버스 대비 3:1 미만 — 스킬의 완화 규칙에 따라 **이 3색을 쓰는 시리즈는 항상 직접 라벨(값 텍스트) 또는 표 뷰 대안을 동반**한다(레거시가 아니라 필수 규칙).

산점도·소규모 다중 카드처럼 모든 쌍이 이웃할 수 있는 형태(예: Ops의 팀별 비교를 작은 카드로 나열)를 위해 `--pairs all`도 돌렸다:

```
$ node scripts/validate_palette.js "#2a78d6,#eb6834,#1baf7a" --mode light --surface "#F7F9FC" --pairs all
[PASS] 전부 — worst all-pairs CVD ΔE 9.2, normal-vision ΔE 24.0, contrast WARN(#1baf7a 2.67)만
```

→ **all-pairs 형태(산점도류)는 처음 3슬롯까지만 안전** — 스킬의 문서화된 결론과 동일. 4번째 슬롯부터는 "기타"로 접거나 화면을 나눈다(Ops 팀별 토큰 사용량이 팀 4개를 넘으면 이 규칙에 걸린다 — 현재 조직은 팀 4개이므로 임계치에 걸쳐 있다. 팀이 늘면 상위 3개 + "기타"로 접는 규칙을 지금부터 명시해 둔다).

**브랜드 에코**: chart-1(blue, `#2a78d6`, H≈213)은 primary(lapis-600, H≈216)와 hue가 3° 차이로 사실상 같은 계열이지만 lightness가 더 밝다(L50 vs L36) — `DESIGN-admin.md`가 teal-500을 chart-2로 편입했던 것과 같은 전략으로, "조직 전체 합계" 같은 대표 시리즈에 chart-1을 쓰면 브랜드 정체성이 차트에도 묻어난다. 단, chart-1은 `colorPrimary` 그 자체가 아니라 별도 토큰이다 — 시리즈 색과 "클릭 가능한 브랜드 액션색"을 혼동시키지 않기 위해 값을 공유하지 않는다.

#### 2.4.2 상태 팔레트 vs 카테고리 팔레트 — 절대 혼용 금지

`40-console-ia.md`가 이미 명시한 원칙("상태 색상은 카테고리 팔레트가 아니라 상태 팔레트로 고정 예약", Ops의 "헬스는 상태색 전용 트랙, 카테고리 비교는 고정 순서 카테고리 팔레트")을 그대로 시행 규칙으로 못박는다:

- **상태 인코딩**(가동/유휴/오류/응답대기, 예산 소진 임계치, 컨트롤 플레인 정상/이상) → §2.3의 success/warning/error/neutral **본색만** 사용. chart-1~8을 상태에 쓰지 않는다.
- **카테고리 인코딩**(팀별/에이전트별 비교, 프로젝트별 분해) → chart-1~8만 사용. 성공/실패를 뜻하는 것처럼 보일 수 있는 chart-6(초록)·chart-8(빨강)을 상태처럼 강조하지 않는다 — 항상 범례+직접 라벨을 동반해 "이게 상태가 아니라 그냥 4번째 팀"임을 분명히 한다.
- 한 화면에 둘 다 나오는 경우(Ops 에이전트 헬스/비용 탭: 좌측 헬스 요약은 상태색, 우측 팀별 비용은 카테고리색) — 형태 자체를 분리한다(헬스=점+링, 비용=막대/시계열)로 이미 시각적으로 구분되므로 실사용 혼동 위험은 낮다.

#### 2.4.3 시퀀셜/게이지 (Work 진척률, 좌석 여유 게이지)

`40-console-ia.md`가 Work 화면에 이미 명시한 규칙 — "진척률 바는 시퀀셜 단일 색상, 완료 시에만 success로 전환" — 을 그대로 채택한다. 시퀀셜 기본 hue는 §2.4.1의 chart-1(blue) 계열을 쓰되, 진행 중에는 중립(gray-300 채움), 100% 완료 시점에만 success 본색으로 스냅 전환한다(중간값에서 초록으로 서서히 변하는 그라데이션 금지 — dataviz 스킬의 "시퀀셜=단일 hue" 원칙과, "완료 여부는 상태이지 정도가 아니다"라는 판단의 결합).

#### 2.4.4 텍스트/그리드 등 차트 부속 색

| 역할 | 값 | 비고 |
|---|---|---|
| 차트 서피스(라이트) | `#F7F9FC`(카드 안이면 `#ffffff`) | 콘솔 캔버스와 통일, dataviz 기본값(`#fcfcfb`)과 사실상 동일해 별도 값 불필요 |
| 그리드라인 | `#eceff3` | admin의 chart-grid 계승, 수평선만(`vertical={false}`) |
| 축 라벨 | gray-500(`#64748b`) 12px | |
| 툴팁 | 백색 카드 + e2 그림자 + radius 10 | admin과 동일 |

### 2.5 타이포그래피 / 2.6 Spacing / 2.7 Radius / 2.8 Elevation / 2.9 z-index

전부 `DESIGN-admin.md` §2.5~2.9를 **값 그대로 계승**한다(hue-무관 물리량이라 바꿀 이유가 없다). 폰트 체인(`Inter, Pretendard, "Noto Sans KR", …`), spacing 4px 기수, radius 6/8/10/12/999, elevation e0~e3(그림자 대신 보더 우선)까지 동일. CSS 변수 프리픽스만 `--osiris-*`로 치환.

**OSiris 고유 추가분**:
- **모노스페이스 토큰**: `--osiris-font-mono` = `"SF Mono", "Fira Code", Menlo, monospace` — Approvals의 diff 뷰어, Ops 로그 tail, Ontology 노드 속성값에 필요(admin 문서엔 없던 개념 — 원본 앱 콘솔엔 코드 diff·로그 화면이 없었다).
- **z-index**: admin의 `--admin-z-sider: 100`, `--admin-z-topbar: 110` 계승(`--osiris-*`로 개명). 추가로 Ops "컨트롤 플레인 경보 배너"는 전역 오버레이가 아니라 **Ops 화면 콘텐츠 영역 상단에 sticky**로 배치되므로(§5.5, `40-console-ia.md`가 이미 "화면 맨 위 고정 배너"라고 화면 범위를 한정) 새 z-index 레이어를 만들 필요는 없다 — 콘텐츠 내부 sticky(`position: sticky; top: 0`)로 충분하고, 값은 topbar(110)보다 낮게 둔다.

### 2.10 ConfigProvider 목표 형상 (참고 코드)

```ts
// src/theme/tokens.ts — 단일 소스 (발췌)
export const osirisTheme: ThemeConfig = {
  cssVar: true,                       // --ant-* CSS 변수 노출 (antd 5.12+)
  token: {
    colorPrimary: '#1B4F9C',          // lapis-600
    colorLink: '#143C78',             // lapis-700
    colorInfo: '#722ed1',             // 보라 — primary(파랑)와 분리
    colorSuccess: '#15793a',
    colorWarning: '#9b5608',
    colorError: '#c4271c',
    colorBgLayout: '#F7F9FC',
    colorBorder: '#e3e8ef',
    colorBorderSecondary: '#edf0f4',
    colorText: '#172033',
    colorTextSecondary: '#475569',
    colorTextTertiary: '#64748b',
    borderRadius: 10,
    fontSize: 14,
    fontFamily: `Inter, Pretendard, "Noto Sans KR", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`,
  },
  components: {
    Layout: { siderBg: '#ffffff', headerBg: '#ffffff' },
    Menu: { itemBorderRadius: 8, itemSelectedBg: '#EAF0FA', itemSelectedColor: '#1B4F9C' },
    Table: { headerBg: '#f8fafc', headerColor: '#64748b', rowHoverBg: '#eef4fc' }, // lapis 틴트 hover
    Card: { borderRadiusLG: 12 },
    Button: { borderRadius: 8, fontWeight: 600 },
    Tag: { borderRadiusSM: 999 },
  },
}
```

`Table.rowHoverBg`는 admin의 teal 틴트(`#f0fafa`) 자리를 lapis 틴트로 교체한 값(`#eef4fc`, lapis-50과 유사 계열이나 hover 전용으로 살짝 더 진하게 — lapis-50 자체는 선택 상태 전용으로 남겨 hover와 선택을 시각적으로 분리).

### 2.11 온톨로지 그래프 뷰 — antd 밖의 컴포넌트를 어떻게 다루는가

`40-console-ia.md`는 Ontology 화면에 "경량 force-layout" 그래프 캔버스를 요구한다(E절 — 대형 그래프 엔진은 과설계로 기각, 노드 수가 수십 개 미만이므로 소규모 라이브러리로 충분). 이건 antd 컴포넌트 트리 밖에 있는 유일한 화면 요소다. 처리 원칙:

1. **토큰은 그대로 상속받는다.** force-layout 라이브러리 자체는 antd를 모르지만, 노드 채움색·엣지색·라벨 텍스트는 `theme/tokens.ts`에서 가져와 그린다(예: 노드 타입별 색은 §2.4.1 카테고리 팔레트, 상태 표시가 있는 노드는 §2.3 상태색) — 별도 팔레트를 만들지 않는다.
2. **캔버스는 antd `Card`(radius 12, border) 안에 얹힌 `<canvas>`/SVG일 뿐**이다 — 레이아웃 계약(카드 패딩, 헤더 타이포)은 다른 카드와 동일하게 지킨다. antd 밖이라고 다른 밀도/여백 규칙을 쓰지 않는다.
3. **표 뷰 대안이 always-on이다.** dataviz 스킬 원칙("그래프가 안 읽히면 표로") + `40-console-ia.md`가 이미 요구한 "그래프 뷰 | 테이블 뷰" 토글 — 테이블 뷰는 완전히 antd `AdminTable`(§6.1)로 그려서, 그래프 렌더러가 다운되거나 접근성이 필요한 사용자는 언제든 순수 antd 화면으로 이 데이터를 볼 수 있다. **force-layout 컴포넌트가 콘솔의 유일한 antd-이탈 지점이 되지 않도록, 같은 데이터의 antd 경로를 항상 유지하는 것**이 이 문제의 실질적 해법이다.
4. **라이브러리는 아직 미확정** — `[미검증]`. 후보는 `d3-force`(직접 SVG 렌더, 의존성 최소, 노드 수십 개 규모에 충분) 또는 `react-force-graph`(캔버스 렌더, API 간결하나 의존성 무게 있음). 노드 수가 "수십 개 미만"이라는 전제가 유지되는 한 `d3-force` + 손그림 SVG 쪽이 번들 크기·커스터마이징 양쪽에서 유리해 보이나, 실제 구현 착수 시점에 재확인.

---

## 3. 브랜드 컬러 결정 — 기록 (재논의 아님)

사용자 결정을 그대로 문서화한다: primary = 라피스 블루, 금은 마크 전용. 근거는 §0, §2.1.2, §2.1.3에 이미 반영했다. `DESIGN-admin.md` §3과의 구조적 대응:

| | `DESIGN-admin.md` §3 | OSiris |
|---|---|---|
| 채택 hue | teal(레퍼런스의 스카이블루 대신 앱 브랜드 연속성으로 teal 유지) | 라피스 블루(원본 앱 teal과의 차별화 + 이집트 모티프 정합) |
| 이식한 것 | 톤 운용 체계(레퍼런스 8장에서) | 톤 운용 체계(`DESIGN-admin.md` 자체에서) |
| 마크 전용색 | 없음(브랜드색=UI색 동일) | **있음** — 금은 UI 인터랙션색과 분리된 별도 채널 |
| AA 여유 | teal-600 4.6:1 (주장, 미검증) | lapis-600 7.94:1 (실측, §2.1.3) |

---

## 4. 라이트/다크 결정 — 계승(과설계 금지)

`DESIGN-admin.md` §4와 동일한 결론: **라이트 우선, 다크는 확장 여지만 확보, 지금 다크 팔레트를 확정하지 않는다.** 토큰 단일화(`cssVar: true` + `theme.token`)가 끝나면 antd `darkAlgorithm` + `--osiris-*` 다크 오버라이드 한 겹으로 도달 가능한 구조는 admin과 동일하게 유지된다.

**OSiris 고유 메모(결정 아님, 관찰)**: OSiris 브랜드 자체가 "금 온 다크(어두운 배지 위의 금색 눈)"로 설계돼 있다(`brand/README.md`) — 즉 브랜드 마크는 원래 다크 서피스에서 가장 잘 산다. 이 때문에 OSiris는 다크 모드가 단순한 "옵션"이 아니라 브랜드가 자연스럽게 끌리는 방향일 수 있다. 하지만 이것이 **지금 다크 토큰을 설계해야 할 이유는 아니다** — 동시접속 10명 미만의 내부 도구에서 다크 모드는 사용자 개인 취향 기능이지 브랜드 완성도의 필수 조건이 아니다. §2.4.1의 chart 다크값은 dataviz 스킬이 이미 검증해둔 참고값을 그대로 인용했을 뿐 OSiris가 새로 설계한 것이 아니다 — 실제 다크 UI 도입 시점에 재검토.

---

## 5. 레이아웃 스펙

### 5.1 사이드바

`40-console-ia.md`의 화면 목록(A절)을 그대로 사이드바 아이템으로 옮긴다. admin과 달리 **그룹 라벨(overline) 없이 평면 목록** — IA 문서가 명시적으로 "역할별 네비게이션 분기 없이 전원 동일 사이드바"라고 못박았고, 화면이 9개뿐이라 5그룹으로 나누던 admin의 필요성 자체가 없다(§9 차이표).

| 속성 | 스펙 |
|---|---|
| 배경/보더 | `#ffffff`, 우측 1px `colorBorder`. sticky 100vh |
| 폭 | 256px |
| 브랜드 영역 | 높이 64px. `osiris-mark.svg`(currentColor 상속) 24px, 색은 **lapis-600**(라이트 사이드바 위이므로 금 사용 금지 — §6 브랜드 마크 규칙) + "OSiris" 워드마크 15/700 `colorText` |
| 메뉴 아이템(9개, 그룹 없음) | 높이 40px, radius 8. 순서: 💬 Admin Chat(홈) · 🧭 Org · 📋 Work · ✅ Approvals · 🖥️ Ops · 🪄 Onboarding · 🕸️ Ontology · 📥 My Tasks(조건부 노출) · — Notifications는 사이드바 항목이 아니라 탑바 드롭다운(§5.2) |
| 기본 상태 | `gray-600` 텍스트 + `gray-400` 아이콘. hover: `gray-100` 배경 |
| 선택 상태 | **lapis-50 배경 + lapis-600 텍스트/아이콘 + 600 굵기**(admin의 teal 선택 패턴과 동일 구조, hue만 교체) |
| 뱃지 | Approvals: 대기 건수 숫자 뱃지(항상 노출, `40-console-ia.md`가 "상시(알림 기반)"로 지정). Onboarding: 조직 변경 감지 시에만 점 뱃지. My Tasks: 항목 자체가 "사람 계정 로그인 + 배정 건 있을 때만" 조건부 노출(뱃지가 아니라 아이템 존재 여부 조건) |
| 하단 | 계정 카드(아바타 lapis-100 배경 + lapis-700 이니셜, 이름 13/600, 역할 캡션) + 로그아웃 |
| 반응형 | `<900px` 숨김(admin과 동일 — 10명 미만 내부 도구라 모바일 최적화는 범위 밖) |

### 5.2 탑바

| 속성 | 스펙 |
|---|---|
| 높이 | 64px, 백색, 하단 1px `colorBorder` |
| 좌측 | 현재 화면명(14/600) — 페이지 타이틀 본체는 admin과 동일하게 컨텐츠 내 `PageHeader`로 이동 |
| 우측 | **알림 벨(🔔) + 미확인 건수 뱃지** → 클릭 시 드롭다운 패널(Notifications, 화면 #9) — **admin과의 명시적 차이**(§9, §불일치 참고). 그 옆 계정 표시(아바타+이름) |
| Ops 컨트롤 플레인 경보 | 탑바에는 두지 않는다 — Ops 화면 콘텐츠 상단 sticky 배너(§2.9)로 한정. 단, 알림 드롭다운 목록에서는 이 이벤트가 **항상 최상단 첫 줄**(`40-console-ia.md` D설계 요구사항 그대로) |

#### 불일치 기록: `DESIGN-admin.md` vs `40-console-ia.md`

`DESIGN-admin.md` §5.2는 "레퍼런스의 …알림벨은 해당 기능이 없으므로 **도입하지 않음**"이라고 명시한다. 반면 `40-console-ia.md`는 화면 #9(알림 센터)를 "헤더 종형 아이콘 드롭다운"으로 명시하고 ASCII 와이어프레임 전부에 `🔔3`을 박아 넣었다 — **두 문서가 정면으로 다른 말을 한다.** 이 문서는 **`40-console-ia.md`를 따른다** — 원본 앱 admin은 알림이 필요 없는 도메인(단일 관리자가 목록성 페이지를 순회)이었지만, OSiris는 승인 대기·완료보고·오류 등 크로스-화면 이벤트가 구조적으로 존재하고(D절 실시간성 요구사항), 그것을 놓치면 "조직 전체가 블록"되는 시나리오(승인 큐, 컨트롤 플레인 장애)가 명시적으로 정의돼 있기 때문이다. admin의 판단은 그 도메인에서는 옳았고, OSiris 도메인에서는 다른 판단이 옳다 — 톤 운용 체계의 계승과 별개로 **기능 요구사항은 IA 문서가 정본**이라는 원칙을 여기 적용했다.

### 5.3 컨텐츠 + PageHeader

admin §5.3과 동일 구조(max-width 1500, 패딩 28/38/44, `PageHeader{title, description?, extra?}`). 차이: Work·Ontology 등 "좌측 보조 패널 + 우측 메인" 2단 레이아웃이 다수라 `PageHeader`가 컨텐츠 전체 폭이 아니라 **우측 메인 패널 폭 기준**으로 배치되는 경우를 허용한다(admin은 전부 단일 컬럼 목록/상세라 이 이슈가 없었다).

### 5.4 화면별 레이아웃 골격 매핑

| # | 화면 | 골격 | 사용 컴포넌트(§6) |
|---|---|---|---|
| 1 | Admin Chat | 단일 컬럼 채팅 스트림, 하단 고정 입력창 | `ChatMessageList`, `ChatArtifactCard`(WBS 초안), `AssignmentRationalePopover` |
| 2 | Org | 중앙 트리형 조직도 캔버스 + 우측 슬라이드 패널 | `OrgTree`, `OrgNode`, `AssignmentRationalePopover` |
| 3 | Work | 좌 프로젝트 목록 + 우 WBS 트리 | `WbsTree`, `FilterBar` |
| 4 | Approvals | 좌 대기열 리스트 + 우 상세(타입별 미리보기) | `ApprovalQueueList`, `ApprovalPreview`(diff/message/expense 3변형) |
| 5 | Ops | 탭(헬스/비용 ↔ 클러스터/노드) | `StatCard`, `AgentHealthList`, `LogTailPane`, `ControlPlaneAlertBanner`, `NodeCapacityCard`, `SeatCapacityTile` |
| 6 | Onboarding | 스테퍼 + Admin Chat과 동일 패턴 재사용 + 우측 체크리스트 패널 | `ChatMessageList`(재사용), `DefinitionChecklist` |
| 7 | Ontology | 필터바 + 그래프/테이블 토글 캔버스 | `AdminTable`(테이블 뷰), 경량 force-layout 캔버스(§2.11) |
| 8 | My Tasks | 카드 리스트(경량) | `TaskInboxCard` |
| 9 | Notifications | 탑바 드롭다운 패널(별도 화면 아님) | `NotificationDropdown` |

---

## 6. 컴포넌트 스펙

### 6.1 계승 컴포넌트 (`DESIGN-admin.md`에서 그대로) — 값만 lapis로 치환

`AdminTable` · `FilterBar` · `StatCard` · `PageHeader` · `StatusTag`는 admin §6.1~6.5의 스펙을 구조 변경 없이 그대로 채택한다(행 높이 52px, 헤더 gray-50, 틴트 pill, KPI 28/700 등). 색 토큰만 teal→lapis, `--admin-*`→`--osiris-*`로 치환. StatusTag 매핑 테이블은 OSiris 도메인에 맞게 확장:

| 상태값 | 시맨틱 |
|---|---|
| 가동 / 정상 / 완료 / 승인됨 | success |
| 유휴 / 대기 / 검증대기 | neutral |
| 응답대기 / 압박(노드) / 재작업요청 | warning |
| 오류 / critical / 오프라인(노드) / 반려 | error |
| 진행중 / 가입중(join) | info |

### 6.2 OSiris 고유 컴포넌트

`40-console-ia.md`가 요구하는 인터랙션(채팅 안의 트리 편집, 사람+AI 혼합 조직도, diff 포함 승인 큐, 좌석 수 환산, 배정 근거 팝오버, 에이전트 헬스)은 admin에 대응물이 없다. 신규 식별:

#### `ChatArtifactCard` — 채팅 인라인 WBS 카드

- **역할**: `text`/`artifact` 두 메시지 타입 중 `artifact`를 렌더링. 채팅 스트림에 얹힌 카드지만 내부는 트리 편집 위젯(`WbsTree`를 축약 모드로 내장).
- **구조**: 카드 헤더(제목 + 마감), 트리 행(펼침/접힘 chevron, 담당자 배지, ⓘ 근거 아이콘, 인라인 편집 텍스트, 행 끝 `⋯` 메뉴), 하단 고정 액션바(`[전체 승인 및 실행]` primary · `[일부만 승인]` default · `[다시 짜줘]` text + 코멘트 입력).
- **상태 전이**: 초안(편집 가능) → 재생성 중(잠김, 스켈레톤 오버레이 + "다시 짜는 중" 캡션) → 실행 중(진행률 바가 카드 위에서 실시간 갱신, `wbs.task.status_changed` 구독) → 완료(고정, 우상단 `[Work 보드에서 열기]` 링크 상시 노출) → 폐기됨(대화 히스토리에 회색조로 흐리게 남음 — audit trail).
- **행 밀도**: 52~60px가 아니라 **40px** — admin의 표준 행 높이보다 좁힌다. 채팅 카드 안에 6개 이상의 태스크가 중첩 트리로 들어가면 52px 기준으론 스크롤이 과해진다는 판단(§9 차이표 항목). Work 화면의 전체화면 `WbsTree`는 반대로 44~48px을 쓴다 — 카드 안(축약)과 전체화면(상세)이 다른 밀도를 갖는 유일한 예외.
- **담당자 배지 클릭**: 드롭다운(같은 capability 후보만, `AssignmentRationalePopover`의 축약 리스트 재사용) → override 선택 시 낙관적 갱신, 실패 시 롤백(§7.3).

#### `WbsTree` — 공용 계층 트리 (Work 화면 본체 + `ChatArtifactCard` 내부 공유)

- 들여쓰기 기반 트리, 각 행: 체크/진행 아이콘, 제목, 담당자 배지, 진척률 바(§2.4.3 시퀀셜 규칙), 기한.
- 리프 노드의 "완료 방식"이 둘로 분기한다는 게 admin에 없던 요구사항: **코드형**(커밋 링크 아이콘) vs **물리형**(체크리스트 하위 항목 + 사진/메모 첨부 아이콘) — 같은 트리 행 컴포넌트가 리프 타입에 따라 우측 액션 아이콘만 바꿔 렌더링한다.
- 롤업 진척률: 하위 완료율의 가중 평균(가중치는 백엔드 응답값, 프론트는 계산하지 않는다 — `10-core-architecture.md`가 못박은 "결정적 계산은 결정적 계층에서" 원칙을 프론트에도 적용해, WBS 롤업 %도 프론트 재계산 없이 서버값 그대로 표시).

#### `OrgNode` / `OrgTree` — 사람+AI 혼합 조직도

- 노드 = 아바타(사람: 사진/이니셜, 에이전트: 로봇 아이콘) + 이름 + 역할, **상태 이중 인코딩**: 링(ring) 색상(§2.3 상태 팔레트: 가동=success, 유휴=neutral, 오류=error, 응답대기=warning) + 좌하단 작은 상태 아이콘(색과 별개로 모양 자체가 다름 — 재생▶/일시정지⏸/경고!/시계🕐 계열). 색맹 사용자도 아이콘 모양만으로 상태를 읽을 수 있어야 한다는 §1 원칙 6의 1급 구현체.
- 트리형(자유배치 force-graph 아님 — `40-console-ia.md`가 "3~4계층이므로 네트워크 그래프보다 트리형이 이해하기 쉽다"고 명시). CSS flex/grid로 충분, 별도 그래프 라이브러리 불필요(§2.11의 force-layout은 Ontology 전용, Org에는 쓰지 않는다).
- 클릭 → 우측 `Drawer`(admin §6.7 계승, 폭 480). capability 목록, 현재 업무, 로그 링크, 사람이면 "메시지 보내기".

#### `AssignmentRationalePopover` — 배정 근거 팝오버 (Admin Chat / Org / Work 3곳 공용)

`40-console-ia.md` C절이 이미 컴포넌트명까지 지정했다 — 그대로 채택.

- ⓘ 아이콘 하나로 통일, hover 또는 클릭 트리거.
- 내용: 요구 capability 목록 → 후보 비교표(선택됨 ✓ / 제외 사유) → 규칙 설명 한 줄. **탈락자와 탈락 사유를 항상 포함**(IA가 명시한 신뢰 설계의 핵심 — 단일 이유 텍스트로 축약하지 않는다).
- override가 있으면 "규칙 배정: X → override: Y (날짜, 사유)"를 규칙값 위에 얹은 레이어로 표시 — 규칙 계산 결과를 지우지 않는다.
- 시각 스펙: 백색 카드, e2 그림자, radius 10, 폭 320px 고정. 선택된 후보 행은 lapis-50 배경(선택 강조는 UI 전역에서 항상 lapis-50이라는 일관성), 제외된 후보는 gray-400 텍스트 + 취소선 없이 사유만 캡션으로.

#### `ApprovalQueueList` + `ApprovalPreview` — 승인 큐, 타입별 미리보기

- 큐 리스트: 유형 아이콘(커밋/발송/지출) + 시간 임박도 정렬, 좌측 세로 리스트(admin의 `AdminTable` 대신 카드형 리스트 — 상세 패널과 동시에 봐야 하므로 테이블보다 리스트가 적합).
- `ApprovalPreview`는 `approvalType`에 따라 3개 변형을 스위칭:
  - `DiffViewer`: unified diff, `+`/`-` 라인 배경(success/error 틴트 각 1px 좌측 바 + 옅은 배경, 모노스페이스 폰트 §2.5).
  - `MessagePreview`: 실제 발송될 메시지를 렌더링한 카드(수신자·제목·본문 미리보기).
  - `ExpensePreview`: 금액(kpi 토큰 크기) + 영수증/견적 썸네일 + 승인 한도 대비 표시.
- 하단 액션바: 승인(primary) / 반려(danger, 코멘트 필수) / 수정요청(default, 코멘트 필수) — admin §6.6 버튼 위계 그대로.

#### `SeatCapacityTile` — 좌석 수 환산 스탯

- `StatCard`의 변형이 아니라 별도 컴포넌트인 이유: 주 지표(좌석 수, 큰 텍스트)와 보조 지표(원 수치, 작은 캡션)가 **항상 쌍으로** 나온다는 제약이 `StatCard`의 "라벨→숫자→보조행" 순서와 미묘하게 다르다 — 보조행이 아니라 **주 지표 바로 옆 괄호 텍스트**로 원 수치를 병기해야 은유(좌석)가 유일한 정보가 되지 않는다(`40-console-ia.md`의 명시적 요구: "은유가 유일한 정보가 되지 않게").
- 예: `👨‍💻 개발자 3명 더 가능` (28/700 gray-900) `(CPU 4.2/8 core · 6.1/16GiB)` (12px gray-500, 바로 아래).
- 백엔드가 좌석 환산값을 계산해 내려준다(`10-core-architecture.md`/IA F절 계약) — 프론트는 표시만, 재계산 금지.

#### `NodeCapacityCard` — 클러스터 노드 카드

- 카드 헤더: 노드명 + 상태(§2.3 상태 팔레트, Org와 동일한 이중 인코딩 규칙 재사용).
- 본문: "얹힌 직원"(에이전트 아바타 목록, 작게 나열 — Org 화면과 시각적으로 같은 아바타 스타일을 써서 "이 서버에 누가 사는지"가 Org 화면 기억과 바로 연결되게 함), 여유 용량(`SeatCapacityTile` 축약형 인라인).
- `[⋯]` 메뉴: 드레인(drain), 제거.

#### `ControlPlaneAlertBanner` — 최상위 경보 배너

- Ops 화면 콘텐츠 최상단 sticky. 평상시 완전히 접힘(높이 0, 렌더링은 하되 시각적으로 없음 — "조용한 정상" 원칙, §1 원칙 7). 이상 시 화면 전체 폭으로 펼쳐지며 error/critical 본색 배경(틴트 아님 — 유일하게 "본색 배경 채움"을 허용하는 지점, 이 경보가 콘솔에서 가장 심각한 등급이기 때문에 틴트로는 약하다는 판단) + 백색 텍스트.
- 텍스트 vs 배경 대비 확인: error 본색(`#c4271c`) 배경 위 백색 텍스트 = 5.75:1(§2.3.1에서 이미 계산한 값의 역수 관계, 동일 대비) — PASS.
- SSE 연결 자체가 끊기는 경우(게이트웨이-컨트롤플레인 동시 장애)를 **연결 두절 지속시간 임계치 초과 시 배너 자동 노출**로 감지하는 클라이언트 로직이 필요(`40-console-ia.md` D절이 명시한 요구) — 이 배너 컴포넌트는 "서버가 이상하다고 말해준 경우"와 "서버가 죽어서 아무 말도 못 하는 경우" 둘 다의 진입점이다. 후자는 배너 문구를 다르게("연결 응답 없음 — 확인 중")한다.

#### `AgentHealthList` / 헬스 상태 표시

- 리스트 행: 상태 링+아이콘(Org·NodeCapacityCard와 동일 이중 인코딩 컴포넌트 재사용, `HealthStatusDot`으로 별도 추출해 3곳에서 공유), 이름, 최근 재시작, 리소스 사용률(얇은 진행바, 임계치 초과 전엔 중립색 — §2.4.3과 동일한 "평상시 중립, 임계 초과시에만 상태색" 규칙).
- 선택 시 `LogTailPane`(중앙 패널)이 해당 에이전트로 전환 — SSE 실시간 tail(§7.2).

### 6.3 그 외 admin 계승 컴포넌트의 OSiris 적용 메모

- **모달·드로어**(admin §6.7): 그대로. Approvals의 반려/수정요청 confirm에 error 틴트 콜아웃 패턴을 그대로 사용.
- **폼**(admin §6.8): Onboarding 마법사 각 단계, filled variant 통일. 페이지 단위 통일 규칙도 계승(Onboarding=filled, 승인 코멘트 인풋도 filled 소형).
- **빈 상태/로딩/에러**(admin §6.9): 그대로. My Tasks·Approvals 큐가 0건일 때 빈 상태 패턴 적용.
- **아이콘**(admin §6.10): `@ant-design/icons` 유지. 사람/에이전트 구분 아이콘(👤/🤖 계열 또는 line-icon 대응물)은 신규 — 아바타 컴포넌트 내부에 고정 규약으로 못박는다(사람=원형 사진/이니셜, 에이전트=사각 라운드 로봇 글리프 — 모양 자체를 다르게 해 "이 노드가 사람인지 AI인지"도 이중 인코딩 대상으로 삼는다. 이건 상태 색과 무관한 또 하나의 정체성 이중 인코딩).

---

## 7. 실시간 UI 패턴 (OSiris 고유 — `DESIGN-admin.md`에 없음)

`40-console-ia.md` D절이 SSE 단일 채널로 확정한 것을 이 디자인 언어 안에서 어떻게 "보여줄지"를 정의한다. 원칙(§1 원칙 7): 낮은 진폭, 조용한 확실함.

### 7.1 스트리밍 텍스트 (Admin Chat 토큰 단위 응답)

- 타이핑 인디케이터: 전송 직후 응답이 아직 없을 때 3점 펄스(gray-400, 1.2s 주기 — 화려한 바운스 금지, opacity 0.3↔0.7만 오간다).
- 토큰 스트림 중: 커서는 lapis-600 1px 세로 바, 텍스트 끝에서 깜빡임(0.8s, opacity 전환만 — 위치 이동 없음). 텍스트 자체엔 글자 단위 페이드인 애니메이션을 넣지 않는다(과한 모션은 매일 쓰는 내부 도구에서 피로 유발 — 원칙 7).
- 스트림 취소: `/chat/{id}/cancel` 대응 UI는 입력창 옆 아이콘 버튼이 전송 아이콘 → 정지 아이콘(gray-700)으로 스왑, 클릭 시 즉시 스트림 중단 + 커서 제거.

### 7.2 상태 전이 (Org 노드, 에이전트 헬스, 노드 join 진행)

- 상태가 바뀌는 순간(`org.node.status_changed` 등 수신 시): 링 색이 즉시 스냅 전환(그라데이션 트랜지션 없음 — 상태는 이산적 사실이지 정도가 아니므로 색이 서서히 변하면 오독 위험) + **테두리 바깥으로 1회 펄스 링**(0.6s, opacity 0.4→0 확산, 상태색 그대로) — "방금 바뀌었다"는 신호만 짧게 주고 사라진다. 지속적인 반짝임(무한 loop pulse)은 오류 상태에도 쓰지 않는다 — 지속 애니메이션은 "계속 봐야 하는 것"이라는 오인을 유발하고 화면이 산만해진다.
- 노드 join 진행상황(`cluster.node.join_progress`): 3단계 스테퍼(대기 중 → 통신확인 → 준비완료)를 모달 안에서 좌→우 채움 바로, 각 단계 완료 시 체크 아이콘 스냅(펄스 1회) — 위 규칙과 동일 어휘.

### 7.3 낙관적 갱신 (담당자 override, 태스크 인라인 편집)

- 클릭 즉시 UI 반영(서버 응답 기다리지 않음) + 해당 요소에 아주 옅은 lapis-50 배경 플래시(0.4s, 페이드 아웃) — "네 조작이 접수됐다"는 확인.
- 실패 시 롤백: 요소가 원래 값으로 돌아가며 error 틴트 배경 플래시(0.4s) + 우하단 `message.error` 토스트(admin §6.9 규칙 계승 — 토스트는 뮤테이션 실패에만).

### 7.4 SSE 연결 두절 처리 (컨트롤 플레인 신호 부재)

`40-console-ia.md` D절의 요구 — "연결 끊김 지속시간 임계치 초과 시 배너 자동 노출" — 을 컴포넌트 레벨로 구체화:

- 콘솔 전역에 `useSseConnection` 훅(가칭) 하나가 EventSource 상태를 추적. `cluster.heartbeat`(10초 간격, `10-core-architecture.md` D20)가 **20~30초 이상 도달하지 않으면** "연결 이상 의심" 상태로 전환.
- 이 상태에선 탑바에 작은 회색 점 표시("실시간 연결 확인 중")가 먼저 뜨고, Ops 화면에 있다면 `ControlPlaneAlertBanner`가 "연결 응답 없음" 변형으로 즉시 펼쳐진다.
- **재연결 성공 시 조용히 원복** — 재연결 성공을 알리는 별도 토스트/배너는 띄우지 않는다(장애가 아니라 "정상으로 돌아왔다"는 신호는 원칙 7의 "조용함"을 문자 그대로 적용 — 아무 것도 안 뜨는 것 자체가 정상 신호).

### 7.5 비용/집계 대시보드 (폴링, 낮은 실시간성)

`40-console-ia.md` D절이 폴링(30~60초)으로 지정한 영역 — 여기엔 실시간 애니메이션을 아예 넣지 않는다. 갱신 시점에 숫자가 즉시 바뀌고, 필요하면 아주 옅은 배경 플래시(0.3s)만. 스트리밍/펄스 어휘(§7.1~7.2)는 SSE 데이터에만 쓰고 폴링 데이터에는 쓰지 않는다 — 사용자가 "이게 실시간인지 아닌지" 무의식적으로 구분할 수 있게(실시간 어휘를 저빈도 데이터에 쓰면 신뢰를 깎아먹는다 — 30초 전 값인데 마치 방금 일어난 일처럼 펄스가 뜨면 오도).

---

## 8. 다크 모드 — 계승 결정(§4), 재확인만

`DESIGN-admin.md` §4의 "라이트 우선, 다크는 확장 여지만" 결정을 그대로 가져온다. **이 문서는 다크 팔레트를 확정하지 않는다.** §2.4.1의 차트 다크값은 dataviz 스킬이 자체 검증해 이미 문서화해둔 참고값을 인용만 한 것이지 OSiris가 설계한 것이 아니라는 점을 다시 강조한다. §2.1.1에서 관찰한 "gold가 lapis-800/900 위에서 8~9:1로 잘 읽힌다"는 사실은 향후 다크 UI를 실제로 설계할 때 "사이드바/브랜드 영역 다크 배경 = lapis-800~900 계열"이라는 좋은 출발점이 되지만, 지금 그 결정을 내리지는 않는다.

---

## 9. `DESIGN-admin.md` 대비 차이점 표

| # | 항목 | `DESIGN-admin.md` (원본 앱) | OSiris | 이유 |
|---|---|---|---|---|
| 1 | Primary hue | teal `#0f8f8b` | **라피스 블루 `#1B4F9C`** | 사용자 결정 — 이집트 모티프(금+라피스) 정합, 원본 앱 teal과 시각적 차별화 |
| 2 | 브랜드 마크 | UI색=브랜드색 동일(teal 하나) | **UI색(lapis)과 마크색(금) 분리** — 금은 primary로 쓰지 않음 | 금은 백색 위 1.88:1로 AA 미달(실측) — 애초에 UI 색으로 쓸 수 없는 값. 어두운 면 전용으로 용도 자체를 분리 |
| 3 | `colorInfo` hue | 파랑(`#2563eb`) — primary(teal)와 분리 목적으로 파랑 채택 | **보라(`#722ed1`)** | OSiris는 primary 자체가 파랑이라 admin과 같은 매핑을 쓰면 정보색=브랜드색 충돌 재발. 실측 6.94:1 |
| 4 | success/warning/error 본색 | 주장만, 대비 미검증 | **실측 후 lightness 하향 조정**(`#16a34a→#15793a` 등) | 원값이 자체 틴트 배경 대비 2.9~3.0:1로 텍스트 AA 미달(실측). 조정 후 4.96~5.75:1 |
| 5 | 사이드바 구조 | 5개 그룹(overline 라벨) + root 전용 SYSTEM | **그룹 라벨 없는 평면 9항목** | `40-console-ia.md`가 명시한 "평면 IA, 역할별 분기 없음" — 화면 수가 적어 그룹핑 불필요 |
| 6 | 탑바 알림벨 | **도입하지 않음**(명시적 배제) | **도입**(🔔 + 뱃지 + 드롭다운) | §5.2 불일치 기록 — IA 문서가 승인/완료보고/오류 등 크로스-화면 알림을 구조적으로 요구, 이 요구는 admin 도메인엔 없었다 |
| 7 | CSS 변수 프리픽스 | `--admin-*` | `--osiris-*` | 콘솔 식별자 분리, 향후 인프라 공유 대비 네임스페이스 충돌 방지 |
| 8 | 차트 팔레트 근거 | 레퍼런스 관찰 기반 로컬 상수 대체, CVD 검증 없음 | **dataviz 스킬 6-체크 검증 완료**(CVD ΔE·명도대역·대비 실측) | 방법론 자체를 업그레이드 — 이번에 처음 검증 절차를 도입 |
| 9 | 채팅 인라인 편집 위젯 | 없음(해당 도메인 없음) | **`ChatArtifactCard`** 신설, 행 높이 40px(admin 표준 52~60px보다 좁힘) | Admin Chat이 홈 화면이자 핵심 인터랙션 — admin에 대응 화면 자체가 없음. 카드 내 중첩 트리는 좁은 밀도가 필요해 예외를 명시 |
| 10 | 실시간성 어휘 | 없음(정적 SPA) | §7 전체 신설(스트리밍/펄스/낙관적갱신/SSE 두절 처리) | SSE 단일 채널이 IA 확정 사항 — admin엔 실시간 데이터 자체가 없었음 |
| 11 | 모노스페이스 토큰 | 없음 | `--osiris-font-mono` 신설 | diff 뷰어·로그 tail·온톨로지 속성값에 필요 — admin엔 코드/로그 화면이 없었음 |
| 12 | 상태 이중 인코딩 원칙 | 명시 없음(색으로만 상태 표현, pill 패턴) | **원칙 6으로 승격**(색+아이콘/모양 항상 병행) | `40-console-ia.md`가 Org 화면에 이미 요구한 것을 콘솔 전역 규칙으로 확장. §2.3.3에서 warning/error CVD 근접 문제를 발견해 실제로 필요성이 입증됨 |
| 13 | 사람/AI 혼합 노드 | 없음(전부 사람 계정) | 아바타 모양 자체로 사람 vs 에이전트 구분(§6.3) | OSiris 조직은 사람+AI 혼합 — admin 도메인엔 이 구분이 없었음 |
| 14 | 마이그레이션 로드맵(admin §7) | Phase 0~4, 기존 코드 리스킨 순서 | **§10 그린필드 구축 순서**로 대체 | OSiris는 코드가 아예 없다 — "이관"이 아니라 "처음부터 짓기" |

---

## 10. 구축 순서 (그린필드 기준)

`DESIGN-admin.md`의 "토큰 단일화가 모든 것의 전제"라는 원칙을 그대로 계승한다 — 순서는 admin의 Phase 0→1→2→3 골격을 따르되, 코드가 없으므로 "이관 단계"가 아니라 "구축 단계"로 다시 쓴다.

### 1단계 — 토큰 + antd 스캐폴딩

1. `src/theme/tokens.ts`: §2 전체를 TS 상수 + `ThemeConfig`로 정의. `cssVar: true` 활성화 시점부터 시작(admin처럼 나중에 이관할 "인라인 theme 객체"가 애초에 존재하지 않는다 — 처음부터 단일 소스).
2. Vite 프로젝트 스캐폴딩(base path `/` — `osiris-gw`가 정적 자산을 루트에서 서빙하는 것은 무변경이나, 프록시 대상 프리픽스는 `10-core-architecture.md` §2.9(I5 3차 수정)로 `/_api/*`로 옮겨졌다 — 클라이언트가 부를 API base URL을 `/_api`로 설정할 것, `/api/*`가 아니다), antd `ConfigProvider`를 최상위에 1회만 배치.
3. `brand/osiris-mark.svg`를 컴포넌트로 임포트(currentColor 상속 그대로 활용 — 색은 사용하는 곳에서 CSS `color`로 지정).

### 2단계 — 레이아웃 뼈대

1. `AppLayout`: 사이드바(§5.1, 평면 9항목) + 탑바(§5.2, 알림벨 포함) + 컨텐츠. 라우팅은 9개 화면 + Notifications(드롭다운, 별도 라우트 아님) 스텁부터.
2. `PageHeader` 공용 컴포넌트 — 9개 화면 전부 최상단에 적용.
3. **검증**: 다크 잔재 없음(애초에 라이트만 만드므로 자연히 충족), 9개 라우트 전부 사이드바에서 도달 가능.

### 3단계 — 공용 컴포넌트 (admin 계승분 먼저)

1. `AdminTable` · `FilterBar` · `StatCard` · `StatusTag` — Approvals·Work·My Tasks 등 목록형 화면에서 즉시 필요하므로 admin 스펙 그대로(§6.1) 먼저 구현.
2. **검증**: 컴포넌트 API가 Approvals(리스트+상세 2단), Work(트리) 양쪽에서 재사용 가능한지 조기 확인.

### 4단계 — OSiris 고유 컴포넌트 (§6.2)

우선순위는 IA 문서의 "진입 빈도"(A절)를 따른다:
1. **`ChatArtifactCard` + `AssignmentRationalePopover`** (Admin Chat = 홈, 상시 진입) — 가장 먼저.
2. **`WbsTree`**(Work) + `ApprovalQueueList`/`ApprovalPreview`(Approvals) — 둘 다 "상시" 빈도.
3. **`OrgNode`/`OrgTree`** — "수시" 빈도지만 `AssignmentRationalePopover`를 공유하므로 3번째 대상에 Admin Chat과 함께 확정해두는 편이 API 일관성에 유리.
4. **`AgentHealthList`/`ControlPlaneAlertBanner`/`NodeCapacityCard`/`SeatCapacityTile`**(Ops) — "일 1~2회" 빈도, 뒤로.
5. **Ontology 그래프 캔버스**(§2.11) — "드묾" 빈도, 가장 나중. 라이브러리 선택(`[미검증]`, §2.11)도 이 단계에서 확정.

### 5단계 — 실시간 배선 (§7)

SSE 훅(`useSseConnection`)과 이벤트 타입별 리듀서(`chat.token`, `wbs.task.status_changed`, `org.node.status_changed`, `cluster.heartbeat` 등, `40-console-ia.md` F절 이벤트 목록)를 붙인다. 이 단계는 백엔드(`osiris-api`)의 SSE 계약이 먼저 서 있어야 하므로 프론트 단독으로는 목(mock) EventSource로 먼저 개발 가능하게 만들어 둔다 — 백엔드 구현 완료를 블로킹 조건으로 만들지 않는다.

### 6단계 — dataviz 배선

recharts 컴포넌트에 §2.4 팔레트를 CSS 커스텀 프로퍼티로 연결, Ops 비용 대시보드·Work 진척률 바에 적용. 3단계 완료(카드/테이블 뼈대) 이후, 5단계와 병행 가능(차트는 실시간 스트림이 아니라 폴링/온디맨드 데이터가 대부분).

**전 단계 공통 검증 기준**: 매 단계마다 `--osiris-*`/`--ant-*` 외의 리터럴 hex가 컴포넌트 코드에 등장하지 않는지 grep(admin Phase 0의 검증 방식 계승) — 그린필드이므로 애초에 "리터럴 하드코딩이 쌓일" 시간이 없지만, 규율로 명시해 둔다.

---

## 부록 A — 참조한 파일 및 실측 도구

- `DESIGN-admin.md`(375줄, 읽기 전용) — 톤 운용 체계·컴포넌트 추출 전략의 원본.
- `docs/design/40-console-ia.md`(읽기 전용) — 화면 9개, 인터랙션 플로우 3개, 실시간성 요구표, 컴포넌트 요구(§C 배정 근거 팝오버 등)의 출처.
- `brand/README.md`, `brand/osiris-mark.svg` — 브랜드 마크 색·사용 제약의 출처.
- `docs/design/10-core-architecture.md` §2(외부 진입 경로) — 콘솔 루트 마운트(`= /`), 클라이언트 base URL `/_api/*`(§2.9, I5 3차 수정 — 최초 확인 당시는 `/api/*`였으나 omniroute 라우팅 재설계로 이전됨), SSE 버퍼링 정책 확인.
- `dataviz` 스킬(`references/color-formula.md`, `references/palette.md`, `scripts/validate_palette.js`) — 차트 팔레트 검증 절차·레퍼런스 팔레트.
- 대비 계산: WCAG 2.x 상대휘도 공식을 직접 구현한 Node 스크립트(sRGB→선형화→`0.2126R+0.7152G+0.0722B`, 대비=`(L1+0.05)/(L2+0.05)`). 이 문서의 모든 "실측" 표기는 이 스크립트의 출력값이다.

## 부록 B — 미검증 항목 (`[미검증]`) 모음

- Ontology 그래프 렌더러 선택(d3-force vs react-force-graph) — §2.11.
- `osiris-gw`의 SPA 히스토리 폴백 규칙 — **갱신 필요, 단순 경로 치환으로 안 끝남**: `10-core-architecture.md` §2.9(I5 3차 수정)로 라우팅 모델 자체가 바뀌었다. 원래(§2.2 당시) 전제는 "화이트리스트 — `/api/*`만 백엔드로 프록시, 그 외 전부 `index.html`로 폴백"이었으나, 지금은 **블랙리스트**다: OSiris가 예약하는 것은 정확히 `= /`(콘솔 슬롯), `/_api/`, `/pre-status`, `/healthz`, 브랜드 정적 파일 4개뿐이고 **그 외 전부(예약되지 않은 모든 경로)는 omniroute로 프록시된다.** 즉 콘솔이 클라이언트 사이드 라우팅으로 `/`(정확히 그 경로) 밖의 최상위 경로(예: `/org`, `/work`)를 쓰면, 그 경로들은 SPA로 폴백되는 것이 아니라 omniroute로 넘어가 버린다 — `10-core-architecture.md` §2.9 자신이 이미 이 위험을 "다음 워커(콘솔)에게" 경고로 남겨 두었다. 이 문서(§10 구축순서 2단계)의 라우팅 스텁이 `AppLayout` 내부에서 클라이언트 라우터(React Router 등)로 화면을 분기하는 한, 그 라우팅이 브라우저 주소창의 경로를 바꾸는지(바꾸면 새로고침 시 위 충돌 발생) 해시 라우팅으로 우회하는지가 이 문서 범위에서 아직 결정되어 있지 않다 — 다음 워커가 명시적으로 결정할 것.
- `osiris-api`의 실제 REST/SSE 응답 스키마(이 문서는 `40-console-ia.md` F절의 "예시" 스키마를 프론트 설계 전제로 삼았을 뿐, 확정 계약은 백엔드 워커 몫).
