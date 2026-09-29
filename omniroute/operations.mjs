#!/usr/bin/env node
// omniroute/operations.mjs — osiris-omniroute 구성 오퍼레이션 (I5).
//
// **이것은 부트스트랩 스크립트가 아니다.** `osiris-up`은 이 파일을 절대
// 호출하지 않는다. osiris-omniroute는 부트스트랩 직후 **구성되지 않은 채로
// healthy**해야 하고, 프로바이더 자격증명 투입·osiris/* 별칭 생성·게이트웨이
// 토큰 발급은 전부 **온보딩 시점**의 일이다(사용자 확정: "온보딩에서 결정할
// 것을 미리 하지 마라 — OSiris는 하나의 프레임워크다, MSA 형태일 뿐이다").
//
// 이 파일은 온보딩 파이프라인(콘솔/`osiris-core`/무엇이 되든, 아직 존재하지
// 않음 — 다음 워커의 몫)이 부를 **오퍼레이션과 그 계약**만 제공한다. 대화
// 흐름·UI·프롬프트는 여기 없다. 각 오퍼레이션은:
//   (1) 순수 파라미터를 받는다(호출자가 config.json이든 사람의 입력이든
//       무엇에서 값을 얻어오는지는 이 모듈이 알 필요가 없다),
//   (2) **수렴적**이다 — 몇 번을 호출해도 실행 후 상태가 인자로 준 선언과
//       일치한다("이미 있으면 스킵"이 아니라 "다르면 갱신", §51의 교훈),
//   (3) 구조화된 결과(JS 객체 / CLI에서는 JSON)를 돌려주고 예외를 던지지
//       않는다(네트워크 도달 불가·로그인 실패 같은 진짜 치명적 오류만
//       throw) — 온보딩 대화가 실패를 사람에게 자연스럽게 설명할 수 있게.
//
// 프로바이더 자격증명 경계(사용자 확정, 10-core-architecture.md §9):
// omniroute가 프로바이더 자격증명의 유일한 소유자다. 이 모듈은 호출자가
// 건넨 apiKey를 omniroute의 관리 API로 전달만 하고, **어떤 파일에도, 어떤
// 로그에도 쓰지 않는다** — grep 가능(module 안에 writeFileSync 호출이
// registerProviderCredential 경로에 없다).
//
// ---------------------------------------------------------------------------
// 익스포트되는 오퍼레이션 (전부 async, 첫 인자는 옵션 {baseUrl, adminPassword}):
//
//   registerProviderCredential({ name, provider, apiKey }, opts)
//     -> { ok, created|updated, id? , error? }
//     프로바이더 커넥션을 만들거나(없으면) apiKey를 갱신한다(있으면, blind
//     overwrite — 비교하지 않음, §51). apiKey는 호출자가 이미 어딘가에서
//     받아온 값을 그대로 넘긴다 — 이 함수는 그 출처를 모른다.
//
//   upsertAlias({ name, providerName, provider, model }, opts)
//     -> { ok, created|converged|inSync, providerMissing?, error? }
//     osiris/* 별칭 하나를 선언과 수렴시킨다. 참조된 providerName이
//     osiris-omniroute에 없거나 타입이 다르면 ok:false, providerMissing:true
//     로 돌아온다(생성/갱신 시도하지 않음) — 호출자가 그 사실을 사람에게
//     보여주고 registerProviderCredential을 먼저 부르게 유도할 자리.
//
//   upsertAliases(entries, opts) -> [{name, ...같은 모양}]
//     여러 별칭에 대한 upsertAlias 반복 (편의 함수).
//
//   issueOrGetGatewayToken({ name, scopes }, opts) -> { ok, token, isNew, id? }
//     OSiris 자신이 쓸 추론 전용 API 키(osiris-agents 등)를 발급하거나
//     이미 있으면 재조회한다. 이건 프로바이더 키가 아니라 omniroute가
//     OSiris에게 발급하는 OSiris 자신의 자격증명이라 이 모듈이 값을
//     반환해도 경계 위반이 아니다 — 저장 여부는 호출자 책임
//     (applyGatewayTokenToEnv는 그 중 하나의 저장 방법일 뿐).
//
//   applyGatewayTokenToEnv(token, { envFile }) -> void
//     .env의 OSIRIS_GATEWAY_TOKEN 라인을 갱신하는 순수 파일 조작 헬퍼.
//     온보딩이 "지금 이 값을 .env에 반영해도 될까?"를 스스로 결정한 뒤에만
//     호출할 헬퍼이지, 다른 오퍼레이션이 자동으로 부르지 않는다.
//
//   exchangeAdminPasswordForManageKey({ password, name }, opts)
//     -> { ok, key, isNew, id?, error? }
//     [2026-07-27 추가, 30-onboarding-provisioning.md §1.0.4] osiris-omniroute
//     관리자 비밀번호를 **정확히 이 함수 안에서만** 소비해 `manage` 스코프
//     API 키로 교환한다 — 비밀번호는 이 호출의 login() 한 번에만 쓰이고
//     어디에도 쓰여지지 않는다(grep 가능: 이 함수 안에 writeFileSync/
//     appendFileSync 호출 없음). 반환되는 `key`만 저장 대상이다
//     (applyManageKeyToEnv). 이후 모든 오퍼레이션은 `opts.manageKey`(또는
//     env `OSIRIS_OMNIROUTE_MANAGE_KEY`)가 있으면 이 login() 경로 자체를
//     완전히 건너뛴다(resolveOpts/authenticate 참고) — 비밀번호는 최초
//     교환 그 순간에만 존재한다.
//
//   applyManageKeyToEnv(key, { envFile }) -> void
//     .env의 OSIRIS_OMNIROUTE_MANAGE_KEY 라인을 갱신하는 순수 파일 조작
//     헬퍼. applyGatewayTokenToEnv와 대칭.
//
//   getConfigurationStatus(opts) -> {
//     reachable, providers: [{name, provider}], aliases: [{name, inSync,
//     provider, model, exists}], agentApiKey: {exists, scopesOk?}
//   }
//     지금 osiris-omniroute가 무엇을 갖고 있는지 읽기 전용으로 보고한다 —
//     온보딩 UI가 "무엇이 아직 비었는지" 보여줄 자리(§30-onboarding의
//     `GET /onboarding/status`와 맞물리는 지점, 다음 워커 인계).
//
//   getDefaultAliasMapping() -> config.json의 aliases 배열 그대로
//     (선언 파일이 아니라 "온보딩이 제시할 기본값"으로 읽는다 — §I5 §53
//     결정 참고. 이 모듈 자신은 이 기본값을 자동 적용하지 않는다.)
//
// CLI(교차 언어 호출용 — osiris-core가 Go라 이 모듈을 직접 import할 수
// 없으므로, 서브프로세스로 부르는 계약도 함께 제공한다):
//   echo '{"name":"osiris-anthropic","provider":"anthropic","apiKey":"sk-..."}' \
//     | node omniroute/operations.mjs register-provider-credential
//   echo '{"name":"osiris/worker","providerName":"osiris-anthropic","provider":"anthropic","model":"claude-sonnet-5"}' \
//     | node omniroute/operations.mjs upsert-alias
//   echo '{"name":"osiris-agents","scopes":[]}' \
//     | node omniroute/operations.mjs issue-or-get-gateway-token
//   node omniroute/operations.mjs status
//   node omniroute/operations.mjs default-alias-mapping
// 각 서브커맨드는 stdin에서 JSON 객체 하나를 읽고(불필요하면 무시),
// stdout에 결과 JSON 한 줄을 쓴다. 종료 코드는 치명적 오류에서만 1.
//
// env: OSIRIS_OMNIROUTE_MANAGE_KEY(있으면 최우선 — §1.0.4, 매 호출
//      `Authorization: Bearer`로 인증하고 login()을 아예 건너뛴다),
//      OSIRIS_OMNIROUTE_ADMIN_PASSWORD(매니지 키가 없을 때만 폴백 — 이
//      인스턴스 자신의 관리자 자격증명이지 프로바이더 키가 아니다),
//      OSIRIS_OMNIROUTE_BASE_URL(기본 http://osiris-omniroute:20128/omni —
//      osiris-edge/osiris-llm 내부 관점. 호스트에서 테스트할 때는
//      osiris-gw의 루프백 경유 http://127.0.0.1:$OSIRIS_GW_HOST_PORT/omni
//      로 오버라이드).

import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join, resolve } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = resolve(__dirname, "..");
const DEFAULT_CONFIG_PATH = join(REPO_ROOT, "omniroute", "config.json");
const DEFAULT_ENV_PATH = join(REPO_ROOT, ".env");

// I5 2차 수정: osiris-omniroute는 이제 OMNIROUTE_BASE_PATH=/omni로 빌드되어
// 있고(서브패스가 요구사항으로 승격됨, §9.7) 호스트 포트를 게시하지 않는다
// (osiris-gw가 /omni/ 로 프록시하는 것이 유일한 경로). 기본값은 osiris-edge/
// osiris-llm에 붙은 온보딩 파이프라인 컨테이너 관점의 내부 주소다 — 호스트에서
// 직접 테스트할 때는 OSIRIS_OMNIROUTE_BASE_URL=http://127.0.0.1:$OSIRIS_GW_HOST_PORT/omni
// 로 오버라이드한다.
function resolveOpts(opts = {}) {
  return {
    baseUrl: opts.baseUrl || process.env.OSIRIS_OMNIROUTE_BASE_URL || "http://osiris-omniroute:20128/omni",
    adminPassword: opts.adminPassword || process.env.OSIRIS_OMNIROUTE_ADMIN_PASSWORD || "",
    // §1.0.4: when a manage key is available, it is used for EVERY call and
    // adminPassword/login() is never touched. adminPassword only still
    // matters for exchangeAdminPasswordForManageKey's very first exchange
    // (which passes its password explicitly, see that function).
    manageKey: opts.manageKey || process.env.OSIRIS_OMNIROUTE_MANAGE_KEY || "",
  };
}

async function login({ baseUrl, adminPassword }) {
  if (!adminPassword) {
    throw new Error("OSIRIS_OMNIROUTE_ADMIN_PASSWORD is not set");
  }
  let res;
  try {
    res = await fetch(`${baseUrl}/api/auth/login`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ password: adminPassword }),
    });
  } catch (e) {
    throw new Error(`cannot reach osiris-omniroute at ${baseUrl}: ${e.message}`);
  }
  const setCookie = res.headers.get("set-cookie");
  if (res.status !== 200 || !setCookie) {
    throw new Error(`login failed (status=${res.status})`);
  }
  return setCookie.split(";")[0];
}

// authenticate() is the single decision point for §1.0.4's precedence: a
// manage key, if present, is used directly (no login() call at all — the
// admin password is never touched once a manage key exists). Only when no
// manage key is configured does this fall back to the password-login path,
// which is meant to cover exactly one moment in this module's lifecycle: the
// very first exchange, before OSIRIS_OMNIROUTE_MANAGE_KEY exists anywhere.
async function authenticate(o) {
  if (o.manageKey) return { bearer: o.manageKey };
  const cookie = await login(o);
  return { cookie };
}

async function api(baseUrl, auth, method, path, body) {
  const headers = { "content-type": "application/json" };
  if (auth.bearer) {
    headers.authorization = `Bearer ${auth.bearer}`;
  } else if (auth.cookie) {
    headers.cookie = auth.cookie;
  }
  const res = await fetch(`${baseUrl}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json;
  try {
    json = text ? JSON.parse(text) : {};
  } catch {
    json = { raw: text };
  }
  return { status: res.status, json };
}

export async function registerProviderCredential({ name, provider, apiKey }, opts = {}) {
  const o = resolveOpts(opts);
  if (!apiKey) return { ok: false, error: "apiKey is required" };
  const auth = await authenticate(o);
  const list = await api(o.baseUrl, auth, "GET", "/api/providers");
  if (list.status !== 200) return { ok: false, error: `GET /api/providers failed (status=${list.status})` };
  const existing = (list.json.connections || []).find((c) => c.name === name);

  if (existing) {
    // Blind overwrite — never compared against anything (there is nothing
    // in this repo to compare it to; omniroute is the sole owner of this
    // secret). Idempotent by construction: same value in, same value out.
    const upd = await api(o.baseUrl, auth, "PUT", `/api/providers/${existing.id}`, { apiKey });
    if (upd.status !== 200) return { ok: false, error: `update failed (status=${upd.status})`, id: existing.id };
    return { ok: true, updated: true, id: existing.id };
  }

  const created = await api(o.baseUrl, auth, "POST", "/api/providers", { provider, apiKey, name });
  if (created.status !== 201) return { ok: false, error: `create failed (status=${created.status})` };
  return { ok: true, created: true, id: created.json.connection?.id };
}

// Wire representation of a combo's model step: "<provider>/<model>"
// (confirmed empirically — see IMPLEMENTATION-NOTES I5 §50).
function qualifiedModel(entry) {
  return `${entry.provider}/${entry.model}`;
}
function aliasInSync(existing, entry) {
  const steps = existing.models || [];
  if (steps.length !== 1) return false;
  const step = steps[0];
  return step.providerId === entry.provider && step.model === qualifiedModel(entry);
}

export async function upsertAlias({ name, providerName, provider, model }, opts = {}) {
  const o = resolveOpts(opts);
  const auth = await authenticate(o);

  const providers = await api(o.baseUrl, auth, "GET", "/api/providers");
  if (providers.status !== 200) return { ok: false, error: `GET /api/providers failed (status=${providers.status})` };
  const providerConn = (providers.json.connections || []).find((c) => c.name === providerName);
  if (!providerConn) {
    return { ok: false, providerMissing: true, error: `provider "${providerName}" not registered in osiris-omniroute` };
  }
  if (providerConn.provider !== provider) {
    return {
      ok: false,
      providerMissing: true,
      error: `provider "${providerName}" is registered as type "${providerConn.provider}", not "${provider}"`,
    };
  }

  const list = await api(o.baseUrl, auth, "GET", "/api/combos");
  if (list.status !== 200) return { ok: false, error: `GET /api/combos failed (status=${list.status})` };
  const existing = (list.json.combos || []).find((c) => c.name === name);
  const entry = { provider, model };

  if (existing) {
    if (aliasInSync(existing, entry)) {
      return { ok: true, inSync: true, id: existing.id };
    }
    const before = (existing.models || []).map((m) => m.model).join(",") || "(none)";
    const upd = await api(o.baseUrl, auth, "PUT", `/api/combos/${existing.id}`, {
      models: [{ provider, model }],
    });
    if (upd.status !== 200) return { ok: false, error: `update failed (status=${upd.status})` };
    return { ok: true, converged: true, before, after: qualifiedModel(entry), id: existing.id };
  }

  const created = await api(o.baseUrl, auth, "POST", "/api/combos", {
    name,
    models: [{ provider, model }],
  });
  if (created.status !== 201) return { ok: false, error: `create failed (status=${created.status})` };
  return { ok: true, created: true, after: qualifiedModel(entry), id: created.json.id };
}

export async function upsertAliases(entries, opts = {}) {
  const results = [];
  for (const entry of entries) {
    results.push({ name: entry.name, ...(await upsertAlias(entry, opts)) });
  }
  return results;
}

const FORCED_SCOPE = "self:usage";
function normalizeScopes(scopes) {
  return [...new Set((scopes || []).filter((s) => s !== FORCED_SCOPE))].sort();
}
function scopesEqual(a, b) {
  const na = normalizeScopes(a);
  const nb = normalizeScopes(b);
  return na.length === nb.length && na.every((v, i) => v === nb[i]);
}

export async function issueOrGetGatewayToken({ name, scopes }, opts = {}) {
  const o = resolveOpts(opts);
  const declaredScopes = scopes || [];
  const auth = await authenticate(o);
  const list = await api(o.baseUrl, auth, "GET", "/api/keys");
  if (list.status !== 200) return { ok: false, error: `GET /api/keys failed (status=${list.status})` };
  const existing = (list.json.keys || []).find((k) => k.name === name);

  if (existing) {
    if (!scopesEqual(existing.scopes, declaredScopes)) {
      await api(o.baseUrl, auth, "PATCH", `/api/keys/${existing.id}`, { scopes: declaredScopes });
    }
    const rev = await api(o.baseUrl, auth, "GET", `/api/keys/${existing.id}/reveal`);
    if (rev.status === 200 && rev.json.key) {
      return { ok: true, isNew: false, token: rev.json.key, id: existing.id };
    }
    return { ok: false, error: `key exists but reveal failed (status=${rev.status})`, id: existing.id };
  }

  const created = await api(o.baseUrl, auth, "POST", "/api/keys", { name, scopes: declaredScopes });
  if (created.status !== 201) return { ok: false, error: `create failed (status=${created.status})` };
  return { ok: true, isNew: true, token: created.json.key, id: created.json.id };
}

// exchangeAdminPasswordForManageKey — §1.0.4's new operation. `password` is
// the ONLY place this function reads a password from (never opts.adminPassword
// / env — the caller, osiris-api's POST /onboarding/step0/omniroute-admin
// handler, holds the password only for the lifetime of that one HTTP
// request and passes it straight through). It is used exactly once, inside
// login(), and never stored, logged, or echoed. Only the resulting
// manage-scoped key is returned — the caller decides whether/when to persist
// it via applyManageKeyToEnv.
export async function exchangeAdminPasswordForManageKey({ password, name }, opts = {}) {
  if (!password) return { ok: false, error: "password is required" };
  const o = resolveOpts(opts);
  let cookie;
  try {
    cookie = await login({ baseUrl: o.baseUrl, adminPassword: password });
  } catch (e) {
    return { ok: false, error: e.message };
  }
  const auth = { cookie };
  const scopes = ["manage"];

  const list = await api(o.baseUrl, auth, "GET", "/api/keys");
  if (list.status !== 200) return { ok: false, error: `GET /api/keys failed (status=${list.status})` };
  const existing = (list.json.keys || []).find((k) => k.name === name);

  if (existing) {
    if (!scopesEqual(existing.scopes, scopes)) {
      await api(o.baseUrl, auth, "PATCH", `/api/keys/${existing.id}`, { scopes });
    }
    const rev = await api(o.baseUrl, auth, "GET", `/api/keys/${existing.id}/reveal`);
    if (rev.status === 200 && rev.json.key) {
      return { ok: true, isNew: false, key: rev.json.key, id: existing.id };
    }
    return { ok: false, error: `key exists but reveal failed (status=${rev.status})`, id: existing.id };
  }

  const created = await api(o.baseUrl, auth, "POST", "/api/keys", { name, scopes });
  if (created.status !== 201) return { ok: false, error: `create failed (status=${created.status})` };
  return { ok: true, isNew: true, key: created.json.key, id: created.json.id };
}

export function applyManageKeyToEnv(key, { envFile } = {}) {
  return writeEnvLine("OSIRIS_OMNIROUTE_MANAGE_KEY", key, envFile);
}

// writeEnvLine is the shared implementation behind applyGatewayTokenToEnv and
// applyManageKeyToEnv — both are "update exactly one KEY= line, in place,
// only if the value actually changed" and nothing else in the file is read
// into any return value or logged.
function writeEnvLine(key, value, envFile) {
  const path = resolve(envFile || DEFAULT_ENV_PATH);
  if (!existsSync(path)) return { ok: false, error: `env file not found: ${path}` };
  const original = readFileSync(path, "utf8");
  const lines = original.split("\n");
  let found = false;
  const pattern = new RegExp(`^${key}=`);
  const next = lines.map((line) => {
    if (pattern.test(line)) {
      found = true;
      return `${key}=${value}`;
    }
    return line;
  });
  if (!found) next.push(`${key}=${value}`);
  const nextContent = next.join("\n");
  if (nextContent !== original) {
    writeFileSync(path, nextContent, "utf8");
    return { ok: true, changed: true };
  }
  return { ok: true, changed: false };
}

export function applyGatewayTokenToEnv(token, { envFile } = {}) {
  return writeEnvLine("OSIRIS_GATEWAY_TOKEN", token, envFile);
}

// ---------------------------------------------------------------------------
// [2026-09-11 추가] 콘솔 "프로바이더 연결" 탭용 CRUD + OAuth 대리 오퍼레이션.
// 위 registerProviderCredential은 온보딩 0단계(자유 provider 문자열, apiKey만)
// 전용이라 남겨둔다 — 아래는 그 상위 호환으로, 콘솔이 등록된 연결의 목록/수정
// /삭제와 세 가지 OAuth 등록 방식(redirect/device/import)까지 다룰 수 있게
// omniroute의 실제 `/api/providers`, `/api/oauth/{provider}/*` 표면을 그대로
// 얇게 감싼다. 이 파일이 여전히 유일한 omniroute wire-contract 소스다 —
// api/internal/omniroute/ops.go는 이 함수들의 타입 있는 래퍼만 추가한다.
//
//   listProviders(opts) -> { ok, connections: [...] }
//   createProvider({provider, apiKey?, name, url?, priority?}, opts)
//     -> { ok, connection?, error? }
//   updateProvider({id, name?, priority?, isActive?, defaultModel?, apiKey?}, opts)
//     -> { ok, connection?, error? }
//   deleteProvider({id}, opts) -> { ok, error? }
//   oauthAction({provider, action, query?, body?}, opts)
//     action은 화이트리스트(OAUTH_ACTIONS)만 허용 — authorize|exchange|
//     device-code|poll|import-token. provider는 `^[a-z0-9-]+$`만 허용(임의
//     경로 주입 방지 — 이 화이트리스트 밖은 osiris-api 라우팅 테이블 자체에
//     등록돼 있지 않아 애초에 이 함수까지 도달하지 못한다. 여기서의 체크는
//     방어 종심).
//     -> { ok, status, data }

export async function listProviders(_input = {}, opts = {}) {
  const o = resolveOpts(opts);
  const auth = await authenticate(o);
  const res = await api(o.baseUrl, auth, "GET", "/api/providers");
  if (res.status !== 200) return { ok: false, error: `list failed (status=${res.status})` };
  return { ok: true, connections: res.json.connections || [] };
}

export async function createProvider({ provider, apiKey, name, url, priority }, opts = {}) {
  const o = resolveOpts(opts);
  if (!provider) return { ok: false, error: "provider is required" };
  if (!name) return { ok: false, error: "name is required" };
  const auth = await authenticate(o);
  const body = { provider, name };
  if (apiKey !== undefined) body.apiKey = apiKey;
  if (url !== undefined) body.url = url;
  if (priority !== undefined) body.priority = priority;
  const created = await api(o.baseUrl, auth, "POST", "/api/providers", body);
  if (created.status !== 201) {
    return { ok: false, error: created.json?.error || `create failed (status=${created.status})` };
  }
  return { ok: true, connection: created.json.connection || created.json };
}

export async function updateProvider({ id, name, priority, isActive, defaultModel, apiKey }, opts = {}) {
  const o = resolveOpts(opts);
  if (!id) return { ok: false, error: "id is required" };
  const auth = await authenticate(o);
  const body = {};
  if (name !== undefined) body.name = name;
  if (priority !== undefined) body.priority = priority;
  if (isActive !== undefined) body.isActive = isActive;
  if (defaultModel !== undefined) body.defaultModel = defaultModel;
  if (apiKey !== undefined) body.apiKey = apiKey;
  const upd = await api(o.baseUrl, auth, "PUT", `/api/providers/${id}`, body);
  if (upd.status !== 200) {
    return { ok: false, error: upd.json?.error || `update failed (status=${upd.status})` };
  }
  return { ok: true, connection: upd.json.connection || upd.json };
}

export async function deleteProvider({ id }, opts = {}) {
  const o = resolveOpts(opts);
  if (!id) return { ok: false, error: "id is required" };
  const auth = await authenticate(o);
  const del = await api(o.baseUrl, auth, "DELETE", `/api/providers/${id}`);
  if (del.status !== 200 && del.status !== 204) {
    return { ok: false, error: del.json?.error || `delete failed (status=${del.status})` };
  }
  return { ok: true };
}

const PROVIDER_ID_PATTERN = /^[a-z0-9-]+$/;
const OAUTH_ACTIONS = {
  authorize: { method: "GET", path: (p) => `/api/oauth/${p}/authorize` },
  exchange: { method: "POST", path: (p) => `/api/oauth/${p}/exchange` },
  "device-code": { method: "GET", path: (p) => `/api/oauth/${p}/device-code` },
  poll: { method: "POST", path: (p) => `/api/oauth/${p}/poll` },
  "import-token": { method: "POST", path: (p) => `/api/oauth/${p}/import-token` },
};

export async function oauthAction({ provider, action, query, body }, opts = {}) {
  const o = resolveOpts(opts);
  if (!provider || !PROVIDER_ID_PATTERN.test(provider)) {
    return { ok: false, error: `invalid provider "${provider}"` };
  }
  const def = OAUTH_ACTIONS[action];
  if (!def) return { ok: false, error: `unknown oauth action "${action}"` };
  const auth = await authenticate(o);
  let path = def.path(provider);
  if (def.method === "GET" && query && Object.keys(query).length > 0) {
    path += `?${new URLSearchParams(query).toString()}`;
  }
  const res = await api(o.baseUrl, auth, def.method, path, def.method === "GET" ? undefined : body || {});
  return { ok: res.status >= 200 && res.status < 300, status: res.status, data: res.json };
}

export async function getConfigurationStatus(opts = {}) {
  const o = resolveOpts(opts);
  let auth;
  try {
    auth = await authenticate(o);
  } catch (e) {
    return { reachable: false, error: e.message };
  }

  const [providersRes, combosRes, keysRes] = await Promise.all([
    api(o.baseUrl, auth, "GET", "/api/providers"),
    api(o.baseUrl, auth, "GET", "/api/combos"),
    api(o.baseUrl, auth, "GET", "/api/keys"),
  ]);

  const providers = (providersRes.json.connections || []).map((c) => ({ name: c.name, provider: c.provider }));
  const defaults = getDefaultAliasMapping();
  const aliases = defaults.map((entry) => {
    const existing = (combosRes.json.combos || []).find((c) => c.name === entry.name);
    return {
      name: entry.name,
      declaredProvider: entry.provider,
      declaredModel: entry.model,
      providerName: entry.providerName,
      providerRegistered: providers.some((p) => p.name === entry.providerName && p.provider === entry.provider),
      exists: Boolean(existing),
      inSync: existing ? aliasInSync(existing, entry) : false,
      currentModel: existing?.models?.[0]?.model ?? null,
    };
  });

  const agentKeyDeclared = getDefaultConfig().agentApiKey;
  const agentKey = agentKeyDeclared
    ? (keysRes.json.keys || []).find((k) => k.name === agentKeyDeclared.name)
    : undefined;

  return {
    reachable: true,
    providers,
    aliases,
    agentApiKey: agentKeyDeclared
      ? { name: agentKeyDeclared.name, exists: Boolean(agentKey), id: agentKey?.id ?? null }
      : null,
  };
}

let _configCache = null;
function getDefaultConfig(configPath) {
  if (!_configCache || configPath) {
    _configCache = JSON.parse(readFileSync(configPath || DEFAULT_CONFIG_PATH, "utf8"));
  }
  return _configCache;
}

// Read-only: the framework's DEFAULT alias -> model mapping (§I5 §53 — this
// is presented to onboarding as a starting point, never applied by this
// module or by any bootstrap path on its own).
export function getDefaultAliasMapping(configPath) {
  return getDefaultConfig(configPath).aliases || [];
}

// ---------------------------------------------------------------------------
// CLI dispatcher — lets a non-Node caller (e.g. osiris-core, written in Go)
// invoke a single operation as a subprocess: JSON in on stdin, JSON out on
// stdout, one line, and a clean process exit. This is the cross-language
// half of the "operations, not a script" contract.
// ---------------------------------------------------------------------------
async function readStdinJson() {
  if (process.stdin.isTTY) return {};
  let data = "";
  for await (const chunk of process.stdin) data += chunk;
  data = data.trim();
  if (!data) return {};
  return JSON.parse(data);
}

async function runCli() {
  const op = process.argv[2];
  const input = await readStdinJson();
  let result;
  switch (op) {
    case "register-provider-credential":
      result = await registerProviderCredential(input);
      break;
    case "upsert-alias":
      result = await upsertAlias(input);
      break;
    case "upsert-aliases":
      result = await upsertAliases(input.entries || input);
      break;
    case "issue-or-get-gateway-token":
      result = await issueOrGetGatewayToken(input);
      break;
    case "apply-gateway-token-to-env":
      result = applyGatewayTokenToEnv(input.token, { envFile: input.envFile });
      break;
    case "exchange-admin-password-for-manage-key":
      result = await exchangeAdminPasswordForManageKey(input);
      break;
    case "apply-manage-key-to-env":
      result = applyManageKeyToEnv(input.key, { envFile: input.envFile });
      break;
    case "status":
      result = await getConfigurationStatus(input);
      break;
    case "default-alias-mapping":
      result = getDefaultAliasMapping(input.configPath);
      break;
    case "list-providers":
      result = await listProviders(input);
      break;
    case "create-provider":
      result = await createProvider(input);
      break;
    case "update-provider":
      result = await updateProvider(input);
      break;
    case "delete-provider":
      result = await deleteProvider(input);
      break;
    case "oauth-action":
      result = await oauthAction(input);
      break;
    default:
      process.stderr.write(
        `unknown operation "${op}". Valid: register-provider-credential, upsert-alias, upsert-aliases, ` +
          `issue-or-get-gateway-token, apply-gateway-token-to-env, exchange-admin-password-for-manage-key, ` +
          `apply-manage-key-to-env, status, default-alias-mapping, list-providers, create-provider, ` +
          `update-provider, delete-provider, oauth-action\n`
      );
      process.exit(1);
  }
  process.stdout.write(JSON.stringify(result) + "\n");
}

// Only run the CLI when this file is executed directly (not when imported
// as a module by a future Node-based onboarding pipeline).
if (import.meta.url === `file://${process.argv[1]}`) {
  runCli().catch((e) => {
    console.error(`[operations] FATAL: ${e.message}`);
    process.exit(1);
  });
}
