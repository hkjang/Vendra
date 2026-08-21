import { FormEvent, useCallback, useEffect, useState } from "react";
import {
  Activity,
  AlertCircle,
  Bot,
  ChevronRight,
  ClipboardCheck,
  Download,
  FileText,
  GitBranch,
  KeyRound,
  Layers3,
  LockKeyhole,
  Network,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Save,
  Search,
  Server,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Users,
  Workflow,
} from "lucide-react";
import { useLocation, useNavigate } from "react-router-dom";
import { api, date, del, patch, post, put } from "../api";
import {
  Badge,
  Empty,
  Field,
  Loading,
  Modal,
  PageHeader,
} from "../components";
import { statusTone } from "../status";

type Setting = {
  key: string;
  value: unknown;
  secret: boolean;
  secretConfigured: boolean;
  category: string;
  updatedAt: string;
};
const sections = [
  { id: "general", label: "일반 설정", icon: Settings },
  { id: "identity", label: "인증 · SSO", icon: LockKeyhole },
  { id: "users", label: "사용자 · 권한", icon: Users },
  { id: "workflow", label: "Workflow", icon: Workflow },
  { id: "scorecard", label: "평가 · Risk 규칙", icon: SlidersHorizontal },
  { id: "lifecycle", label: "Lifecycle", icon: GitBranch },
  { id: "integrations", label: "연동 · 알림", icon: Network },
  { id: "ai", label: "AI 모델", icon: Bot },
  { id: "audit", label: "감사로그", icon: Activity },
  { id: "logs", label: "서버 로그", icon: Server },
];
export default function Admin({ notify }: { notify: (s: string) => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const current = location.pathname.split("/")[2] || "general";
  return (
    <div className="page admin-page">
      <PageHeader
        eyebrow="Service administration"
        title="서비스 관리"
        description="조직 정책, 인증, 승인, 평가, 연동과 감사 정책을 코드 수정 없이 관리합니다."
      />
      <div className="admin-layout">
        <aside>
          {sections.map((s) => (
            <button
              className={current === s.id ? "active" : ""}
              onClick={() => navigate("/admin/" + s.id)}
              key={s.id}
            >
              <s.icon />
              {s.label}
              <ChevronRight />
            </button>
          ))}
        </aside>
        <section>
          <AdminSection current={current} notify={notify} />
        </section>
      </div>
    </div>
  );
}
function AdminSection({
  current,
  notify,
}: {
  current: string;
  notify: (s: string) => void;
}) {
  if (current === "users") return <UsersPanel />;
  if (current === "workflow") return <WorkflowPanel notify={notify} />;
  if (current === "scorecard") return <Scorecards />;
  if (current === "lifecycle") return <Lifecycle />;
  if (current === "audit") return <Audit />;
  if (current === "logs") return <ServerLogs />;
  return <SettingsPanel category={current} notify={notify} />;
}

function SettingsPanel({
  category,
  notify,
}: {
  category: string;
  notify: (s: string) => void;
}) {
  const [settings, setSettings] = useState<Setting[]>();
  const load = useCallback(
    () =>
      api<{ items: Setting[] }>("/api/v1/admin/settings").then((x) =>
        setSettings(x.items),
      ),
    [],
  );
  useEffect(() => {
    load();
  }, [load]);
  if (!settings) return <Loading />;
  if (category === "identity")
    return <OIDC settings={settings} notify={notify} reload={load} />;
  if (category === "ai")
    return <AISettings settings={settings} notify={notify} reload={load} />;
  const selected = settings.filter((s) =>
    category === "general"
      ? !["identity", "ai", "integration", "notification", "workflow"].includes(
          s.category,
        )
      : category === "integrations"
        ? ["integration", "notification"].includes(s.category)
        : s.category === category,
  );
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>{sections.find((x) => x.id === category)?.label}</h2>
          <p>JSON 정책 값을 저장하면 실행 중인 서비스에 즉시 반영됩니다.</p>
        </div>
      </header>
      {selected.length ? (
        <div className="raw-settings">
          {selected.map((s) => (
            <SettingRow setting={s} reload={load} notify={notify} key={s.key} />
          ))}
        </div>
      ) : (
        <Empty
          title="등록된 설정이 없습니다"
          description="통합 설정 키를 추가하면 이 카테고리에 표시됩니다."
        />
      )}
    </div>
  );
}
function SettingRow({
  setting,
  reload,
  notify,
}: {
  setting: Setting;
  reload: () => void;
  notify: (s: string) => void;
}) {
  const [value, setValue] = useState(JSON.stringify(setting.value, null, 2));
  async function save() {
    try {
      await put(`/api/v1/admin/settings/${setting.key}`, {
        value: JSON.parse(value),
        category: setting.category,
        secret: setting.secret,
      });
      notify(`${setting.key} 설정을 저장했습니다.`);
      reload();
    } catch (e) {
      notify(e instanceof Error ? e.message : "저장하지 못했습니다");
    }
  }
  return (
    <div>
      <div>
        <b>{setting.key}</b>
        <small>
          {setting.category} · {date(setting.updatedAt)}
        </small>
      </div>
      <textarea
        value={value}
        onChange={(e) => setValue(e.target.value)}
        rows={Math.min(10, value.split("\n").length + 1)}
      />
      <button className="button secondary" onClick={save}>
        <Save />
        저장
      </button>
    </div>
  );
}

function OIDC({
  settings,
  notify,
  reload,
}: {
  settings: Setting[];
  notify: (s: string) => void;
  reload: () => void;
}) {
  const current = (settings.find((s) => s.key === "oidc")?.value ||
    {}) as Record<string, unknown>;
  async function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await put("/api/v1/admin/settings/oidc", {
      category: "identity",
      secret: true,
      value: {
        enabled: d.get("enabled") === "on",
        issuer: d.get("issuer"),
        clientId: d.get("clientId"),
        scopes: String(d.get("scopes")).split(" ").filter(Boolean),
        autoCreate: d.get("autoCreate") === "on",
        defaultRole: d.get("defaultRole"),
      },
      secretValue: d.get("clientSecret") || undefined,
    });
    notify("Keycloak OIDC 설정을 저장했습니다.");
    reload();
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>Keycloak SSO · OIDC</h2>
          <p>
            Issuer URL, Client ID와 Secret만 입력하면 Discovery를 통해 자동
            연동합니다.
          </p>
        </div>
        <Badge tone={current.enabled ? "success" : "neutral"}>
          {current.enabled ? "사용 중" : "비활성"}
        </Badge>
      </header>
      <div className="security-banner">
        <ShieldCheck />
        <div>
          <b>OIDC Authorization Code + PKCE</b>
          <p>
            Discovery, state, nonce, PKCE와 ID Token 서명을 검증합니다. Client
            Secret은 AES-256-GCM으로 암호화됩니다.
          </p>
        </div>
      </div>
      <form onSubmit={save}>
        <label className="toggle-row">
          <span>
            <b>Keycloak SSO 사용</b>
            <small>로그인 화면에 SSO 버튼을 표시합니다.</small>
          </span>
          <input
            type="checkbox"
            name="enabled"
            defaultChecked={Boolean(current.enabled)}
          />
        </label>
        <div className="form-grid">
          <Field label="Issuer URL" required>
            <input
              name="issuer"
              defaultValue={String(current.issuer || "")}
              placeholder="https://keycloak.internal/realms/company"
            />
          </Field>
          <Field label="Client ID" required>
            <input
              name="clientId"
              defaultValue={String(current.clientId || "")}
              placeholder="vendra"
            />
          </Field>
          <Field
            label="Client Secret"
            hint="비워두면 기존 secret을 유지합니다."
          >
            <input
              name="clientSecret"
              type="password"
              placeholder={
                settings.find((s) => s.key === "oidc")?.secretConfigured
                  ? "설정됨 · 변경하려면 입력"
                  : "Secret 입력"
              }
            />
          </Field>
          <Field label="Scope">
            <input
              name="scopes"
              defaultValue={
                Array.isArray(current.scopes)
                  ? current.scopes.join(" ")
                  : "openid profile email"
              }
            />
          </Field>
          <Field label="신규 사용자 기본 역할">
            <select
              name="defaultRole"
              defaultValue={String(current.defaultRole || "business_user")}
            >
              <option value="business_user">현업 담당자</option>
              <option value="procurement_manager">구매 관리자</option>
              <option value="supplier_user">공급업체 사용자</option>
            </select>
          </Field>
        </div>
        <label className="toggle-row">
          <span>
            <b>첫 로그인 시 사용자 자동 생성</b>
            <small>Keycloak 이메일과 이름으로 Vendra 계정을 만듭니다.</small>
          </span>
          <input
            type="checkbox"
            name="autoCreate"
            defaultChecked={Boolean(current.autoCreate)}
          />
        </label>
        <div className="form-actions">
          <button className="button">
            <Save />
            OIDC 설정 저장
          </button>
        </div>
      </form>
    </div>
  );
}

function AISettings({
  settings,
  notify,
  reload,
}: {
  settings: Setting[];
  notify: (s: string) => void;
  reload: () => void;
}) {
  const current = (settings.find((s) => s.key === "ai")?.value || {}) as Record<
    string,
    unknown
  >;
  async function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await put("/api/v1/admin/settings/ai", {
      category: "ai",
      secret: true,
      value: {
        enabled: d.get("enabled") === "on",
        baseUrl: d.get("baseUrl"),
        model: d.get("model"),
        timeoutSeconds: Number(d.get("timeoutSeconds")),
      },
      secretValue: d.get("apiKey") || undefined,
    });
    notify("AI 모델 설정을 저장했습니다.");
    reload();
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>OpenAI 호환 AI Gateway</h2>
          <p>
            인터넷 또는 내부망의 OpenAI 호환 API를 Vendra Analyst에 연결합니다.
          </p>
        </div>
        <Badge tone={current.enabled ? "success" : "neutral"}>
          {current.enabled ? "연결 사용" : "비활성"}
        </Badge>
      </header>
      <form onSubmit={save}>
        <label className="toggle-row">
          <span>
            <b>AI 기능 사용</b>
            <small>AI Supplier Analyst와 계약 분석을 활성화합니다.</small>
          </span>
          <input
            type="checkbox"
            name="enabled"
            defaultChecked={Boolean(current.enabled)}
          />
        </label>
        <div className="form-grid">
          <Field label="Base URL" required>
            <input
              name="baseUrl"
              defaultValue={String(current.baseUrl || "")}
              placeholder="https://ai.internal/v1"
            />
          </Field>
          <Field label="모델" required>
            <input
              name="model"
              defaultValue={String(current.model || "")}
              placeholder="gpt-5-mini"
            />
          </Field>
          <Field label="API Key">
            <input
              name="apiKey"
              type="password"
              placeholder={
                settings.find((s) => s.key === "ai")?.secretConfigured
                  ? "설정됨 · 변경하려면 입력"
                  : "선택 사항"
              }
            />
          </Field>
          <Field label="Timeout (초)">
            <input
              type="number"
              name="timeoutSeconds"
              defaultValue={Number(current.timeoutSeconds || 60)}
            />
          </Field>
        </div>
        <div className="form-actions">
          <button className="button">
            <Save />
            AI 설정 저장
          </button>
        </div>
      </form>
    </div>
  );
}

type User = {
  id: string;
  email: string;
  displayName: string;
  userType: string;
  status: string;
  organizationId?: string;
  supplierId?: string;
  roles: { code: string; name: string }[];
  lastLoginAt?: string;
  createdAt: string;
};
type AdminRole = {
  id: string;
  code: string;
  name: string;
  permissions: string[];
  dataScope: string;
  system: boolean;
};
type Organization = {
  id: string;
  name: string;
  parentId?: string;
  path: string;
};
type AccessGrant = {
  id: string;
  userId: string;
  email: string;
  permission: string;
  resourceType?: string;
  resourceId?: string;
  conditions: Record<string, unknown>;
  validFrom: string;
  validUntil?: string;
};
function UsersPanel() {
  const [users, setUsers] = useState<User[]>();
  const [roles, setRoles] = useState<AdminRole[]>([]);
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [grants, setGrants] = useState<AccessGrant[]>([]);
  const [tab, setTab] = useState("users");
  const [modal, setModal] = useState<
    "user" | "role" | "organization" | "grant"
  >();
  const [editingUser, setEditingUser] = useState<User>();
  const [editingRole, setEditingRole] = useState<AdminRole>();
  const load = () =>
    Promise.all([
      api<{ items: User[] }>("/api/v1/admin/users"),
      api<{ items: AdminRole[] }>("/api/v1/admin/roles"),
      api<{ items: Organization[] }>("/api/v1/admin/organizations"),
      api<{ items: AccessGrant[] }>("/api/v1/admin/access-grants"),
    ]).then(([u, r, o, g]) => {
      setUsers(u.items);
      setRoles(r.items);
      setOrganizations(o.items);
      setGrants(g.items);
    });
  useEffect(() => {
    load();
  }, []);
  if (!users || !roles) return <Loading />;
  const addLabel = {
    users: "사용자 추가",
    roles: "역할 추가",
    organizations: "조직 추가",
    grants: "임시 권한 위임",
  }[tab];
  function openCreate() {
    setEditingUser(undefined);
    setEditingRole(undefined);
    setModal(
      tab === "users"
        ? "user"
        : tab === "roles"
          ? "role"
          : tab === "organizations"
            ? "organization"
            : "grant",
    );
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>조직 · 사용자 · 권한</h2>
          <p>
            조직 계층, RBAC, Data Scope와 기간·조건부 위임 권한을 관리합니다.
          </p>
        </div>
        <button className="button" onClick={openCreate}>
          <Plus />
          {addLabel}
        </button>
      </header>
      <div className="admin-subnav">
        {[
          ["users", "사용자"],
          ["roles", "역할 · RBAC"],
          ["organizations", "조직"],
          ["grants", "위임 · 임시 권한"],
        ].map(([id, label]) => (
          <button
            className={tab === id ? "active" : ""}
            onClick={() => setTab(id)}
            key={id}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "users" && (
        <table>
          <thead>
            <tr>
              <th>사용자</th>
              <th>유형</th>
              <th>역할</th>
              <th>조직</th>
              <th>상태</th>
              <th>최근 로그인</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr
                className="clickable-row"
                onClick={() => {
                  setEditingUser(u);
                  setModal("user");
                }}
                key={u.id}
              >
                <td>
                  <span className="supplier-cell">
                    <span className="avatar">{u.displayName.slice(0, 1)}</span>
                    <span>
                      <b>{u.displayName}</b>
                      <small>{u.email}</small>
                    </span>
                  </span>
                </td>
                <td>{u.userType}</td>
                <td>
                  {u.roles.map((r) => (
                    <Badge key={r.code}>{r.name}</Badge>
                  ))}
                </td>
                <td>
                  {organizations.find((o) => o.id === u.organizationId)?.name ||
                    "—"}
                </td>
                <td>
                  <Badge tone={statusTone(u.status)}>{u.status}</Badge>
                </td>
                <td>{date(u.lastLoginAt)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {tab === "roles" && (
        <table>
          <thead>
            <tr>
              <th>역할</th>
              <th>코드</th>
              <th>Data Scope</th>
              <th>권한</th>
              <th>유형</th>
            </tr>
          </thead>
          <tbody>
            {roles.map((role) => (
              <tr
                className={role.system ? "" : "clickable-row"}
                onClick={() => {
                  if (!role.system) {
                    setEditingRole(role);
                    setModal("role");
                  }
                }}
                key={role.id}
              >
                <td>
                  <b>{role.name}</b>
                </td>
                <td>
                  <code>{role.code}</code>
                </td>
                <td>
                  <Badge>{role.dataScope}</Badge>
                </td>
                <td>
                  <span className="permission-summary">
                    {role.permissions.join(", ")}
                  </span>
                </td>
                <td>
                  {role.system ? (
                    <Badge tone="purple">System</Badge>
                  ) : (
                    <Badge>Custom</Badge>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {tab === "organizations" && (
        <div className="organization-tree">
          {organizations.map((org) => (
            <div
              style={{
                paddingLeft: `${Math.max(0, org.path.split("/").filter(Boolean).length - 1) * 24}px`,
              }}
              key={org.id}
            >
              <span className="object-icon">
                <Layers3 />
              </span>
              <div>
                <b>{org.name}</b>
                <small>
                  {org.path}
                  {org.id}/
                </small>
              </div>
            </div>
          ))}
        </div>
      )}
      {tab === "grants" &&
        (grants.length ? (
          <table>
            <thead>
              <tr>
                <th>사용자</th>
                <th>권한</th>
                <th>리소스</th>
                <th>조건</th>
                <th>유효기간</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {grants.map((grant) => (
                <tr key={grant.id}>
                  <td>{grant.email}</td>
                  <td>
                    <code>{grant.permission}</code>
                  </td>
                  <td>
                    {grant.resourceType || "전체"}
                    {grant.resourceId
                      ? ` · ${grant.resourceId.slice(0, 8)}`
                      : ""}
                  </td>
                  <td>
                    <code>{JSON.stringify(grant.conditions)}</code>
                  </td>
                  <td>
                    {date(grant.validFrom)} ~ {date(grant.validUntil)}
                  </td>
                  <td>
                    <button
                      className="icon-button danger-icon"
                      title="위임 철회"
                      onClick={async () => {
                        await del(`/api/v1/admin/access-grants/${grant.id}`);
                        load();
                      }}
                    >
                      <KeyRound />
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty
            icon={<KeyRound />}
            title="활성 위임 권한이 없습니다"
            description="기간과 리소스, 요청 조건을 제한한 임시 권한을 부여할 수 있습니다."
          />
        ))}
      {modal === "user" && (
        <NewUser
          roles={roles}
          organizations={organizations}
          user={editingUser}
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
      {modal === "role" && (
        <RoleForm
          role={editingRole}
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
      {modal === "organization" && (
        <OrganizationForm
          organizations={organizations}
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
      {modal === "grant" && (
        <GrantForm
          users={users}
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
    </div>
  );
}
function NewUser({
  roles,
  organizations,
  user,
  onClose,
  saved,
}: {
  roles: AdminRole[];
  organizations: Organization[];
  user?: User;
  onClose: () => void;
  saved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    if (user)
      await patch(`/api/v1/admin/users/${user.id}`, {
        displayName: d.get("displayName"),
        status: d.get("status"),
        organizationId: d.get("organizationId"),
        roleCodes: d.getAll("roles"),
      });
    else
      await post("/api/v1/admin/users", {
        email: d.get("email"),
        displayName: d.get("displayName"),
        password: d.get("password"),
        userType: d.get("userType"),
        status: d.get("status"),
        organizationId: d.get("organizationId"),
        roleCodes: d.getAll("roles"),
      });
    saved();
  }
  return (
    <Modal
      title={user ? "사용자 편집" : "사용자 추가"}
      description="내부 사용자, 공급업체 사용자 또는 API 클라이언트 계정을 만듭니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="이름" required>
          <input
            name="displayName"
            defaultValue={user?.displayName}
            required
            autoFocus
          />
        </Field>
        <Field label="이메일" required>
          <input
            name="email"
            type="email"
            defaultValue={user?.email}
            disabled={Boolean(user)}
            required
          />
        </Field>
        {!user && (
          <Field label="초기 비밀번호">
            <input name="password" type="password" minLength={10} />
          </Field>
        )}
        <Field label="사용자 유형">
          <select
            name="userType"
            defaultValue={user?.userType || "internal"}
            disabled={Boolean(user)}
          >
            <option value="internal">내부 사용자</option>
            <option value="supplier">공급업체 사용자</option>
            <option value="api">API Client</option>
          </select>
        </Field>
        <div className="form-grid">
          <Field label="소속 조직">
            <select
              name="organizationId"
              defaultValue={user?.organizationId || ""}
            >
              <option value="">미지정</option>
              {organizations.map((org) => (
                <option value={org.id} key={org.id}>
                  {org.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="상태">
            <select name="status" defaultValue={user?.status || "active"}>
              <option value="active">active</option>
              <option value="disabled">disabled</option>
              <option value="invited">invited</option>
            </select>
          </Field>
        </div>
        <fieldset className="check-grid">
          <legend>역할</legend>
          {roles.map((r) => (
            <label key={r.code}>
              <input
                type="checkbox"
                name="roles"
                value={r.code}
                defaultChecked={user?.roles.some(
                  (assigned) => assigned.code === r.code,
                )}
              />
              {r.name}
            </label>
          ))}
        </fieldset>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">
            {user ? "사용자 저장" : "사용자 생성"}
          </button>
        </div>
      </form>
      {user && <ResetUserPassword user={user} />}
    </Modal>
  );
}

// Rendered outside the edit form because HTML forbids nested forms, and because
// resetting a password is a separate, immediately-applied action.
function ResetUserPassword({ user }: { user: User }) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [error, setError] = useState<string>();
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const next = String(new FormData(form).get("newPassword") || "");
    setError(undefined);
    setMessage(undefined);
    setBusy(true);
    try {
      const result = await post<{ revokedSessions: number }>(
        `/api/v1/admin/users/${user.id}/password`,
        { newPassword: next },
      );
      form.reset();
      setMessage(
        `비밀번호를 재설정하고 세션 ${result.revokedSessions}개를 폐기했습니다. 새 비밀번호를 안전한 경로로 전달하세요.`,
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "재설정하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="password-reset" onSubmit={submit}>
      <h3>
        <LockKeyhole />
        비밀번호 재설정
      </h3>
      <p>
        {user.email} 계정의 비밀번호를 설정하고 해당 사용자의 모든 세션을 즉시
        폐기합니다. 자격증명 유출 시 사용하세요.
      </p>
      <Field label="새 비밀번호" required hint="10자 이상, 최대 72바이트">
        <input
          name="newPassword"
          type="password"
          autoComplete="new-password"
          minLength={10}
          required
        />
      </Field>
      {message && (
        <p className="form-notice" role="status">
          <ShieldCheck />
          {message}
        </p>
      )}
      {error && (
        <p className="form-error" role="alert">
          <AlertCircle />
          {error}
        </p>
      )}
      <div className="form-actions">
        <button className="button danger" disabled={busy}>
          {busy ? "재설정 중" : "재설정하고 세션 폐기"}
        </button>
      </div>
    </form>
  );
}

function RoleForm({
  role,
  onClose,
  saved,
}: {
  role?: AdminRole;
  onClose: () => void;
  saved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const common = {
      name: d.get("name"),
      dataScope: d.get("dataScope"),
      permissions: String(d.get("permissions"))
        .split(/[\n,]/)
        .map((x) => x.trim())
        .filter(Boolean),
    };
    if (role) await patch(`/api/v1/admin/roles/${role.id}`, common);
    else await post("/api/v1/admin/roles", { ...common, code: d.get("code") });
    saved();
  }
  return (
    <Modal
      title={role ? "역할 편집" : "사용자 역할 생성"}
      description="최소 권한과 데이터 범위를 조합해 사용자 역할을 정의합니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="역할 이름" required>
            <input name="name" defaultValue={role?.name} required autoFocus />
          </Field>
          <Field label="역할 코드" required>
            <input
              name="code"
              defaultValue={role?.code}
              disabled={Boolean(role)}
              required
            />
          </Field>
          <Field label="Data Scope">
            <select name="dataScope" defaultValue={role?.dataScope || "own"}>
              <option value="own">본인</option>
              <option value="department">부서</option>
              <option value="division">본부</option>
              <option value="company">전사</option>
            </select>
          </Field>
        </div>
        <Field
          label="권한"
          hint="쉼표 또는 줄바꿈으로 구분. wildcard를 지원합니다."
        >
          <textarea
            name="permissions"
            rows={8}
            defaultValue={role?.permissions.join("\n")}
            placeholder="supplier.read&#10;contract.*"
            required
          />
        </Field>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">저장</button>
        </div>
      </form>
    </Modal>
  );
}

function OrganizationForm({
  organizations,
  onClose,
  saved,
}: {
  organizations: Organization[];
  onClose: () => void;
  saved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await post("/api/v1/admin/organizations", {
      name: d.get("name"),
      parentId: d.get("parentId"),
    });
    saved();
  }
  return (
    <Modal
      title="조직 추가"
      description="본부·부서 계층은 Data Scope 필터에 즉시 반영됩니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="조직 이름" required>
          <input name="name" required autoFocus />
        </Field>
        <Field label="상위 조직">
          <select name="parentId">
            <option value="">최상위</option>
            {organizations.map((org) => (
              <option value={org.id} key={org.id}>
                {org.name}
              </option>
            ))}
          </select>
        </Field>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">조직 저장</button>
        </div>
      </form>
    </Modal>
  );
}

function GrantForm({
  users,
  onClose,
  saved,
}: {
  users: User[];
  onClose: () => void;
  saved: () => void;
}) {
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    const d = new FormData(e.currentTarget);
    try {
      const conditionText = String(d.get("conditions") || "{}");
      await post("/api/v1/admin/access-grants", {
        userId: d.get("userId"),
        permission: d.get("permission"),
        resourceType: d.get("resourceType"),
        resourceId: d.get("resourceId"),
        conditions: JSON.parse(conditionText),
        validFrom: d.get("validFrom"),
        validUntil: d.get("validUntil"),
      });
      saved();
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "임시 권한을 저장하지 못했습니다",
      );
    }
  }
  return (
    <Modal
      title="임시 권한 · 대결 위임"
      description="기간, 리소스와 요청 조건을 만족하는 경우에만 권한을 활성화합니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="사용자" required>
          <select name="userId" required>
            <option value="">선택</option>
            {users
              .filter((u) => u.userType === "internal")
              .map((u) => (
                <option value={u.id} key={u.id}>
                  {u.displayName} · {u.email}
                </option>
              ))}
          </select>
        </Field>
        <div className="form-grid">
          <Field label="권한" required>
            <input name="permission" placeholder="contract.read" required />
          </Field>
          <Field label="리소스 유형">
            <input name="resourceType" placeholder="contract" />
          </Field>
          <Field label="리소스 ID">
            <input name="resourceId" placeholder="UUID (선택)" />
          </Field>
          <Field label="시작">
            <input name="validFrom" type="datetime-local" />
          </Field>
          <Field label="종료">
            <input name="validUntil" type="datetime-local" required />
          </Field>
        </div>
        <Field
          label="ABAC 조건 JSON"
          hint="지원: method(s), pathPrefix, userType, dataScope, organizationId, supplierId, query"
        >
          <textarea name="conditions" rows={4} defaultValue="{}" />
        </Field>
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">권한 위임</button>
        </div>
      </form>
    </Modal>
  );
}

type Wf = {
  id: string;
  name: string;
  objectType: string;
  enabled: boolean;
  conditions: unknown;
  steps: { name: string; role: string }[];
  version: number;
  updatedAt: string;
};
function WorkflowPanel({ notify }: { notify: (s: string) => void }) {
  const [items, setItems] = useState<Wf[]>();
  const [settings, setSettings] = useState<Setting[]>();
  const [modal, setModal] = useState(false);
  const load = () =>
    Promise.all([
      api<{ items: Wf[] }>("/api/v1/workflows"),
      api<{ items: Setting[] }>("/api/v1/admin/settings"),
    ]).then(([w, s]) => {
      setItems(w.items);
      setSettings(s.items);
    });
  useEffect(() => {
    void load();
  }, []);
  if (!items || !settings) return <Loading />;
  const enabled = Boolean(
    settings.find((s) => s.key === "workflow.approval_enabled")?.value,
  );
  async function toggle() {
    await put("/api/v1/admin/settings/workflow.approval_enabled", {
      category: "workflow",
      secret: false,
      value: !enabled,
    });
    notify(
      !enabled
        ? "검토·승인 프로세스를 활성화했습니다."
        : "검토·승인 프로세스를 제외했습니다. 제출 시 자동 승인됩니다.",
    );
    load();
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>승인 Workflow Engine</h2>
          <p>
            금액, 조직, 계약 유형, Risk, 품목과 보안등급에 따라 승인 단계를
            구성합니다.
          </p>
        </div>
        <button className="button" onClick={() => setModal(true)}>
          <Plus />
          Workflow
        </button>
      </header>
      <label className="toggle-row highlighted">
        <span>
          <b>서비스 검토 · 승인 프로세스</b>
          <small>
            끄면 검토/승인/반려 단계가 모든 업무에서 제외되고 제출 즉시
            승인됩니다.
          </small>
        </span>
        <input type="checkbox" checked={enabled} onChange={toggle} />
      </label>
      <div className="workflow-list">
        {items.map((w) => (
          <div key={w.id}>
            <span className="workflow-icon">
              <Workflow />
            </span>
            <div>
              <span>
                <b>{w.name}</b>
                <Badge tone={w.enabled ? "success" : "neutral"}>
                  {w.enabled ? "활성" : "비활성"}
                </Badge>
              </span>
              <p>
                {w.objectType} · v{w.version}
              </p>
              <div className="workflow-steps">
                {w.steps.map((s, i) => (
                  <span key={i}>
                    <i>{i + 1}</i>
                    {s.name}
                    <ChevronRight />
                  </span>
                ))}
              </div>
            </div>
          </div>
        ))}
        {!items.length && (
          <Empty
            icon={<Workflow />}
            title="정의된 Workflow가 없습니다"
            description="Workflow가 없거나 전체 프로세스가 꺼져 있으면 제출 즉시 자동 승인됩니다."
          />
        )}
      </div>
      {modal && (
        <NewWorkflow
          onClose={() => setModal(false)}
          saved={() => {
            setModal(false);
            load();
          }}
        />
      )}
    </div>
  );
}
function NewWorkflow({
  onClose,
  saved,
}: {
  onClose: () => void;
  saved: () => void;
}) {
  const [steps, setSteps] = useState([
    { name: "팀장 승인", role: "procurement_manager" },
  ]);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await post("/api/v1/workflows", {
      name: d.get("name"),
      objectType: d.get("objectType"),
      enabled: true,
      conditions: {
        minAmount: Number(d.get("minAmount")) || 0,
        riskLevel: d.get("riskLevel"),
      },
      steps,
    });
    saved();
  }
  return (
    <Modal
      title="승인 Workflow 만들기"
      description="조건에 맞는 업무 제출 시 순서대로 승인 단계를 실행합니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="Workflow 이름" required>
            <input name="name" required autoFocus />
          </Field>
          <Field label="업무 유형">
            <select name="objectType">
              <option value="purchase_request">구매요청</option>
              <option value="contract">계약</option>
              <option value="purchase_order">발주</option>
              <option value="supplier">공급업체</option>
            </select>
          </Field>
          <Field label="최소 금액">
            <input name="minAmount" type="number" min="0" />
          </Field>
          <Field label="공급업체 Risk 조건">
            <select name="riskLevel">
              <option value="">모든 등급</option>
              <option>LOW</option>
              <option>MEDIUM</option>
              <option>HIGH</option>
              <option>CRITICAL</option>
            </select>
          </Field>
        </div>
        <fieldset className="steps-editor">
          <legend>승인 단계</legend>
          {steps.map((s, i) => (
            <div key={i}>
              <span>{i + 1}</span>
              <input
                value={s.name}
                onChange={(e) =>
                  setSteps((v) =>
                    v.map((x, n) =>
                      n === i ? { ...x, name: e.target.value } : x,
                    ),
                  )
                }
              />
              <select
                value={s.role}
                onChange={(e) =>
                  setSteps((v) =>
                    v.map((x, n) =>
                      n === i ? { ...x, role: e.target.value } : x,
                    ),
                  )
                }
              >
                <option value="procurement_manager">구매 관리자</option>
                <option value="contract_manager">계약 담당자</option>
                <option value="legal">법무 담당자</option>
                <option value="finance">재무 담당자</option>
                <option value="security">보안 담당자</option>
                <option value="system_admin">시스템 관리자</option>
              </select>
            </div>
          ))}
          <button
            type="button"
            className="button ghost"
            onClick={() =>
              setSteps([
                ...steps,
                { name: "추가 승인", role: "procurement_manager" },
              ])
            }
          >
            <Plus />
            단계 추가
          </button>
        </fieldset>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">Workflow 저장</button>
        </div>
      </form>
    </Modal>
  );
}

function Scorecards() {
  const [items, setItems] = useState<Record<string, unknown>[]>();
  const [screenings, setScreenings] = useState<Record<string, unknown>[]>();
  const [modal, setModal] = useState<"scorecard" | "screening">();
  const load = () =>
    Promise.all([
      api<{ items: Record<string, unknown>[] }>("/api/v1/admin/scorecards"),
      api<{ items: Record<string, unknown>[] }>(
        "/api/v1/admin/screening-templates",
      ),
    ]).then(([s, c]) => {
      setItems(s.items);
      setScreenings(c.items);
    });
  useEffect(() => {
    void load();
  }, []);
  if (!items || !screenings) return <Loading />;
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>평가 · 심사 · Risk 규칙</h2>
          <p>
            평가항목별 가중치, 등급 기준, 등록 심사와 필수 문서를 동적으로
            구성합니다.
          </p>
        </div>
        <div className="header-actions">
          <button
            className="button secondary"
            onClick={() => setModal("screening")}
          >
            <Plus />
            심사표
          </button>
          <button className="button" onClick={() => setModal("scorecard")}>
            <Plus />
            평가표
          </button>
        </div>
      </header>
      <h3 className="admin-subtitle">성과 평가 Scorecard</h3>
      {items.map((x, i) => (
        <div className="scorecard-rule" key={i}>
          <div>
            <ClipboardCheck />
            <span>
              <b>{String(x.name)}</b>
              <small>
                {String(x.evaluationType)} · {x.active ? "활성" : "비활성"}
              </small>
            </span>
          </div>
          <div className="criteria-list">
            {(x.criteria as { name: string; weight: number }[]).map((c) => (
              <span key={c.name}>
                <b>{c.name}</b>
                <i>
                  <em style={{ width: `${c.weight * 3}%` }} />
                </i>
                <strong>{c.weight}%</strong>
              </span>
            ))}
          </div>
        </div>
      ))}
      <h3 className="admin-subtitle">신규 공급업체 심사</h3>
      {screenings.map((x, i) => (
        <div className="scorecard-rule" key={i}>
          <div>
            <ShieldCheck />
            <span>
              <b>{String(x.name)}</b>
              <small>
                {x.active ? "활성" : "비활성"} · 필수문서{" "}
                {Array.isArray(x.requiredDocumentTypes)
                  ? x.requiredDocumentTypes.length
                  : 0}
                종
              </small>
            </span>
          </div>
          <div className="criteria-list">
            {(x.items as { name: string; weight: number }[]).map((c) => (
              <span key={c.name}>
                <b>{c.name}</b>
                <i>
                  <em style={{ width: `${c.weight * 3}%` }} />
                </i>
                <strong>{c.weight}%</strong>
              </span>
            ))}
          </div>
        </div>
      ))}
      {modal === "scorecard" && (
        <NewScorecard
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
      {modal === "screening" && (
        <NewScreeningTemplate
          onClose={() => setModal(undefined)}
          saved={() => {
            setModal(undefined);
            load();
          }}
        />
      )}
    </div>
  );
}

function parsePolicyLines(value: string) {
  return value
    .split("\n")
    .map((x) => x.trim())
    .filter(Boolean)
    .map((line) => {
      const [code, name, weight, required] = line
        .split(":")
        .map((x) => x.trim());
      return {
        code,
        name: name || code,
        weight: Number(weight) || 0,
        required: required === "필수" || required === "true",
      };
    });
}
function NewScorecard({
  onClose,
  saved,
}: {
  onClose: () => void;
  saved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const criteria = parsePolicyLines(String(d.get("criteria"))).map(
      ({ code, name, weight }) => ({ code, name, weight }),
    );
    if (criteria.reduce((n, x) => n + x.weight, 0) !== 100)
      throw new Error("가중치 합계는 100이어야 합니다.");
    await post("/api/v1/admin/scorecards", {
      name: d.get("name"),
      evaluationType: d.get("evaluationType"),
      active: true,
      criteria,
      gradeRules: [
        { min: 90, grade: "S" },
        { min: 80, grade: "A" },
        { min: 70, grade: "B" },
        { min: 60, grade: "C" },
        { min: 0, grade: "D" },
      ],
    });
    saved();
  }
  return (
    <Modal
      title="평가 Scorecard 생성"
      description="항목은 code:이름:가중치 형식으로 한 줄씩 입력하며 합계는 100이어야 합니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="평가표 이름" required>
            <input name="name" required autoFocus />
          </Field>
          <Field label="평가 유형">
            <select name="evaluationType">
              <option value="periodic">정기 평가</option>
              <option value="new_supplier">신규업체 평가</option>
              <option value="project">프로젝트 평가</option>
              <option value="contract">계약 평가</option>
              <option value="quality">품질 평가</option>
              <option value="delivery">납기 평가</option>
              <option value="security">보안 평가</option>
              <option value="emergency">긴급 평가</option>
            </select>
          </Field>
        </div>
        <Field label="평가 항목">
          <textarea
            name="criteria"
            rows={10}
            required
            defaultValue={
              "price:가격 경쟁력:20\nquality:품질:25\ndelivery:납기:20\nservice:서비스:10\ntechnology:기술력:10\nsecurity:보안:5\nfinance:재무 안정성:5\nesg:ESG:5"
            }
          />
        </Field>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">평가표 저장</button>
        </div>
      </form>
    </Modal>
  );
}
function NewScreeningTemplate({
  onClose,
  saved,
}: {
  onClose: () => void;
  saved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const items = parsePolicyLines(String(d.get("items")));
    if (items.reduce((n, x) => n + x.weight, 0) !== 100)
      throw new Error("가중치 합계는 100이어야 합니다.");
    await post("/api/v1/admin/screening-templates", {
      name: d.get("name"),
      active: true,
      items,
      resultRules: {
        passMin: Number(d.get("passMin")),
        conditionalMin: Number(d.get("conditionalMin")),
        reviewMin: Number(d.get("reviewMin")),
        requiredFailureResult: "REVIEW_REQUIRED",
      },
      requiredDocumentTypes: String(d.get("documents"))
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
    });
    saved();
  }
  return (
    <Modal
      title="공급업체 심사표 생성"
      description="항목은 code:이름:가중치:필수 형식으로 한 줄씩 입력합니다."
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <Field label="심사표 이름" required>
          <input name="name" required autoFocus />
        </Field>
        <Field label="심사 항목">
          <textarea
            name="items"
            rows={10}
            required
            defaultValue={
              "eligibility:기본 적격성:15:필수\nfinance:재무:20:필수\nquality:품질:20:필수\ntechnology:기술:15:false\nsecurity:보안:15:필수\ncompliance:준법:10:필수\nesg:ESG:5:false"
            }
          />
        </Field>
        <div className="form-grid">
          <Field label="PASS 최소점">
            <input name="passMin" type="number" defaultValue="80" />
          </Field>
          <Field label="조건부 PASS 최소점">
            <input name="conditionalMin" type="number" defaultValue="70" />
          </Field>
          <Field label="검토 필요 최소점">
            <input name="reviewMin" type="number" defaultValue="60" />
          </Field>
          <Field label="필수 문서 유형" hint="쉼표로 구분">
            <input
              name="documents"
              defaultValue="business_registration,financial_statement,security_pledge"
            />
          </Field>
        </div>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">심사표 저장</button>
        </div>
      </form>
    </Modal>
  );
}

type LifecycleItem = {
  id?: string;
  entityType: string;
  code: string;
  name: string;
  color: string;
  sortOrder: number;
  terminal: boolean;
  enabled: boolean;
};
function Lifecycle() {
  const [items, setItems] = useState<LifecycleItem[]>();
  const [entityType, setEntityType] = useState("supplier");
  const [adding, setAdding] = useState(false);
  const [newCode, setNewCode] = useState("");
  const [codeError, setCodeError] = useState("");
  useEffect(() => {
    api<{ items: LifecycleItem[] }>("/api/v1/admin/lifecycle").then((x) =>
      setItems(x.items),
    );
  }, []);
  if (!items) return <Loading />;
  const loadedItems = items;
  const visible = loadedItems
    .filter((item) => item.entityType === entityType)
    .sort((a, b) => a.sortOrder - b.sortOrder);
  function change(code: string, update: Partial<LifecycleItem>) {
    setItems((current) =>
      current?.map((item) =>
        item.entityType === entityType && item.code === code
          ? { ...item, ...update }
          : item,
      ),
    );
  }
  function addState(event: FormEvent) {
    event.preventDefault();
    const code = newCode
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9_]/g, "_");
    if (!code) {
      setCodeError("영문 소문자, 숫자 또는 밑줄로 상태 코드를 입력하세요.");
      return;
    }
    if (
      loadedItems.some(
        (item) => item.entityType === entityType && item.code === code,
      )
    ) {
      setCodeError("이미 사용 중인 상태 코드입니다.");
      return;
    }
    setItems([
      ...loadedItems,
      {
        entityType,
        code,
        name: code,
        color: "#64748b",
        sortOrder: visible.length,
        terminal: false,
        enabled: true,
      },
    ]);
    setAdding(false);
    setNewCode("");
    setCodeError("");
  }
  async function save() {
    await put(`/api/v1/admin/lifecycle/${entityType}`, {
      items: visible.map(
        ({ code, name, color, sortOrder, terminal, enabled }) => ({
          code,
          name,
          color,
          sortOrder,
          terminal,
          enabled,
        }),
      ),
    });
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>Lifecycle 상태 편집기</h2>
          <p>
            업무 상태, 표시명, 색상, 순서와 종료 여부를 코드 수정 없이
            관리합니다.
          </p>
        </div>
        <div className="header-actions">
          <button
            className="button secondary"
            onClick={() => setAdding(true)}
          >
            <Plus />
            상태 추가
          </button>
          <button className="button" onClick={save}>
            <Save />
            저장
          </button>
        </div>
      </header>
      <div className="admin-subnav">
        {["supplier", "contract", "purchase_request", "rfq", "rfp"].map(
          (type) => (
            <button
              className={entityType === type ? "active" : ""}
              onClick={() => setEntityType(type)}
              key={type}
            >
              {type}
            </button>
          ),
        )}
      </div>
      <div className="lifecycle-flow">
        {visible
          .filter((x) => x.enabled)
          .map((x, i) => (
            <div key={x.code}>
              <span style={{ background: x.color }}>{i + 1}</span>
              <b>{x.name}</b>
              <small>{x.code}</small>
              {i < visible.filter((y) => y.enabled).length - 1 && (
                <ChevronRight />
              )}
            </div>
          ))}
      </div>
      <table className="lifecycle-editor">
        <thead>
          <tr>
            <th>코드</th>
            <th>표시명</th>
            <th>색상</th>
            <th>순서</th>
            <th>종료 상태</th>
            <th>사용</th>
          </tr>
        </thead>
        <tbody>
          {visible.map((item) => (
            <tr key={item.code}>
              <td>
                <code>{item.code}</code>
              </td>
              <td>
                <input
                  value={item.name}
                  onChange={(e) => change(item.code, { name: e.target.value })}
                />
              </td>
              <td>
                <input
                  type="color"
                  value={item.color}
                  onChange={(e) => change(item.code, { color: e.target.value })}
                />
              </td>
              <td>
                <input
                  type="number"
                  min="0"
                  value={item.sortOrder}
                  onChange={(e) =>
                    change(item.code, { sortOrder: Number(e.target.value) })
                  }
                />
              </td>
              <td>
                <input
                  type="checkbox"
                  checked={item.terminal}
                  onChange={(e) =>
                    change(item.code, { terminal: e.target.checked })
                  }
                />
              </td>
              <td>
                <input
                  type="checkbox"
                  checked={item.enabled}
                  onChange={(e) =>
                    change(item.code, { enabled: e.target.checked })
                  }
                />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="form-error warning">
        <AlertCircle />
        상태 코드는 API와 감사로그의 불변 식별자입니다. 이름과 색상, 순서는
        자유롭게 변경할 수 있습니다.
      </div>
      {adding && (
        <Modal
          title="Lifecycle 상태 추가"
          description={`${entityType} 업무에 사용할 변경되지 않는 상태 코드를 등록합니다.`}
          onClose={() => setAdding(false)}
        >
          <form onSubmit={addState}>
            <Field
              label="상태 코드"
              hint="영문 소문자, 숫자, 밑줄(_)만 사용합니다. 예: renewed"
              required
            >
              <input
                value={newCode}
                onChange={(event) => {
                  setNewCode(event.target.value);
                  setCodeError("");
                }}
                placeholder="renewed"
                pattern="[A-Za-z0-9_]+"
                autoFocus
                required
              />
            </Field>
            {codeError && <div className="form-error">{codeError}</div>}
            <div className="form-actions">
              <button
                type="button"
                className="button secondary"
                onClick={() => setAdding(false)}
              >
                취소
              </button>
              <button className="button">상태 추가</button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}

function Audit() {
  const [items, setItems] = useState<Record<string, unknown>[]>();
  useEffect(() => {
    api<{ items: Record<string, unknown>[] }>(
      "/api/v1/admin/audit?limit=300",
    ).then((x) => setItems(x.items));
  }, []);
  if (!items) return <Loading />;
  function exportCSV() {
    const cell = (value: unknown) => {
      let text =
        value !== null && typeof value === "object"
          ? JSON.stringify(value)
          : String(value ?? "");
      if (/^[=+\-@]/.test(text)) text = `'${text}`;
      return `"${text.replaceAll('"', '""')}"`;
    };
    const columns = [
      "occurredAt",
      "actorEmail",
      "action",
      "objectType",
      "objectId",
      "previousValue",
      "newValue",
      "ip",
      "sessionId",
      "requestId",
    ];
    const csv = [
      columns.map(cell).join(","),
      ...items!.map((item) =>
        columns
          .map((column) =>
            cell(
              column === "actorEmail"
                ? item.actorEmail || item.actor
                : item[column],
            ),
          )
          .join(","),
      ),
    ].join("\r\n");
    const url = URL.createObjectURL(
      new Blob(["\uFEFF", csv], { type: "text/csv;charset=utf-8" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = `vendra-audit-${new Date().toISOString().slice(0, 10)}.csv`;
    link.click();
    URL.revokeObjectURL(url);
  }
  return (
    <div className="admin-card">
      <header>
        <div>
          <h2>감사로그</h2>
          <p>
            사용자, 시간, 작업, 대상, 변경 전후 값, 세션과 Request ID를
            보존합니다.
          </p>
        </div>
        <button
          className="button secondary"
          onClick={exportCSV}
          disabled={!items.length}
        >
          <FileText />
          내보내기
        </button>
      </header>
      {items.length ? (
        <table>
          <thead>
            <tr>
              <th>시각</th>
              <th>사용자</th>
              <th>작업</th>
              <th>대상</th>
              <th>Request ID</th>
            </tr>
          </thead>
          <tbody>
            {items.map((x, i) => (
              <tr key={i}>
                <td>{date(String(x.occurredAt))}</td>
                <td>{String(x.actor || "system")}</td>
                <td>
                  <Badge tone={statusTone(String(x.action))}>
                    {String(x.action)}
                  </Badge>
                </td>
                <td>
                  <span className="stack">
                    <b>{String(x.objectType)}</b>
                    <small>{String(x.objectId || "—")}</small>
                  </span>
                </td>
                <td>
                  <code>{String(x.requestId)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <Empty
          title="감사 이벤트가 없습니다"
          description="주요 객체 생성, 변경, 승인과 키 관리 작업이 여기에 기록됩니다."
        />
      )}
    </div>
  );
}

type ServerLog = {
  id: number;
  occurredAt: string;
  level: "DEBUG" | "INFO" | "WARN" | "ERROR";
  message: string;
  attributes: Record<string, unknown>;
};
type ServerLogResponse = {
  items: ServerLog[];
  stats: {
    retained: number;
    debug: number;
    info: number;
    warning: number;
    error: number;
  };
  capacity: number;
  startedAt: string;
  generatedAt: string;
};

function ServerLogs() {
  const [data, setData] = useState<ServerLogResponse>();
  const [level, setLevel] = useState("ALL");
  const [query, setQuery] = useState("");
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [expanded, setExpanded] = useState<number>();
  const [loadError, setLoadError] = useState("");
  const load = useCallback(async () => {
    const params = new URLSearchParams({ level, query, limit: "300" });
    try {
      const result = await api<ServerLogResponse>(
        `/api/v1/admin/logs?${params}`,
      );
      setData(result);
      setLoadError("");
    } catch (error) {
      setLoadError(
        error instanceof Error ? error.message : "서버 로그를 조회하지 못했습니다.",
      );
    }
  }, [level, query]);
  useEffect(() => {
    const initial = window.setTimeout(() => void load(), 200);
    if (!autoRefresh) return () => window.clearTimeout(initial);
    const timer = window.setInterval(() => void load(), 5000);
    return () => {
      window.clearTimeout(initial);
      window.clearInterval(timer);
    };
  }, [autoRefresh, load]);
  if (!data && !loadError) return <Loading label="서버 로그를 불러오는 중" />;
  const items = data?.items || [];
  const stats = data?.stats || {
    retained: 0,
    debug: 0,
    info: 0,
    warning: 0,
    error: 0,
  };
  function exportLogs() {
    const blob = new Blob([JSON.stringify(data, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `vendra-server-logs-${new Date().toISOString().replaceAll(":", "-")}.json`;
    link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  return (
    <div className="admin-card server-logs-card">
      <header>
        <div>
          <h2>서버 로그</h2>
          <p>
            현재 Vendra 프로세스의 구조화 로그를 실시간으로 확인합니다. 민감한
            속성은 수집 단계에서 마스킹됩니다.
          </p>
        </div>
        <div className="log-header-actions">
          <button
            className={`button secondary ${autoRefresh ? "active" : ""}`}
            onClick={() => setAutoRefresh((value) => !value)}
            aria-pressed={autoRefresh}
          >
            {autoRefresh ? <Pause /> : <Play />}
            {autoRefresh ? "자동 갱신 중" : "자동 갱신"}
          </button>
          <button className="button secondary" onClick={() => void load()}>
            <RefreshCw />새로고침
          </button>
          <button
            className="button secondary"
            onClick={exportLogs}
            disabled={!items.length}
          >
            <Download />JSON
          </button>
        </div>
      </header>
      <div className="log-stats" aria-label="서버 로그 요약">
        <div>
          <span>보관 중</span>
          <strong>{stats.retained.toLocaleString()}</strong>
          <small>최대 {data?.capacity.toLocaleString() || 0}건</small>
        </div>
        <div className="info">
          <span>INFO</span>
          <strong>{stats.info.toLocaleString()}</strong>
        </div>
        <div className="warning">
          <span>WARN</span>
          <strong>{stats.warning.toLocaleString()}</strong>
        </div>
        <div className="error">
          <span>ERROR</span>
          <strong>{stats.error.toLocaleString()}</strong>
        </div>
      </div>
      <div className="log-toolbar">
        <div className="search-box">
          <Search />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="메시지, Request ID, 경로, 컴포넌트 검색"
            aria-label="서버 로그 검색"
          />
        </div>
        <select
          value={level}
          onChange={(event) => setLevel(event.target.value)}
          aria-label="로그 레벨"
        >
          <option value="ALL">전체 레벨</option>
          <option value="ERROR">ERROR</option>
          <option value="WARN">WARN</option>
          <option value="INFO">INFO</option>
          <option value="DEBUG">DEBUG</option>
        </select>
        <span className="log-updated" aria-live="polite">
          {data?.generatedAt
            ? `${logTime(data.generatedAt)} 갱신`
            : "갱신 대기"}
        </span>
      </div>
      {loadError && (
        <div className="form-error">
          <AlertCircle />
          {loadError}
        </div>
      )}
      <div className="server-log-list" role="log" aria-live="polite">
        {items.map((item) => {
          const requestID = String(item.attributes.request_id || "");
          const path = String(item.attributes.path || "");
          const isExpanded = expanded === item.id;
          return (
            <button
              className={`server-log-row ${item.level.toLowerCase()} ${isExpanded ? "expanded" : ""}`}
              key={item.id}
              onClick={() => setExpanded(isExpanded ? undefined : item.id)}
              aria-expanded={isExpanded}
            >
              <time dateTime={item.occurredAt}>{logTime(item.occurredAt)}</time>
              <span className={`log-level ${item.level.toLowerCase()}`}>
                {item.level}
              </span>
              <span className="log-message">
                <b>{item.message}</b>
                {(path || requestID) && (
                  <small>
                    {path}
                    {path && requestID ? " · " : ""}
                    {requestID && `Request ${requestID}`}
                  </small>
                )}
                {isExpanded && (
                  <code>{JSON.stringify(item.attributes, null, 2)}</code>
                )}
              </span>
              <ChevronRight />
            </button>
          );
        })}
        {!items.length && !loadError && (
          <Empty
            icon={<Server />}
            title="조건에 맞는 서버 로그가 없습니다"
            description="검색어 또는 로그 레벨을 변경해 보세요. 새 로그는 자동 갱신 시 표시됩니다."
          />
        )}
      </div>
      <div className="log-retention-note">
        <Server />
        <span>
          UI에는 현재 프로세스의 최근 로그만 순환 보관합니다. 장기 보관은 Docker
          로그 드라이버 또는 중앙 로그 수집기를 사용하세요.
          {data?.startedAt && ` 프로세스 시작: ${logTime(data.startedAt)}`}
        </span>
      </div>
    </div>
  );
}

function logTime(value: string) {
  return new Intl.DateTimeFormat("ko-KR", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).format(new Date(value));
}
