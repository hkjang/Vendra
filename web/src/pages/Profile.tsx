import { FormEvent, useEffect, useState } from "react";
import {
  AlertCircle,
  Check,
  Clock3,
  Copy,
  Globe2,
  KeyRound,
  Laptop2,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  UserRound,
} from "lucide-react";
import { api, date, del, patch, post, Principal, Version } from "../api";
import { Badge, Confirm, Empty, Field, Modal, PageHeader } from "../components";
import { APIKey } from "../types";

export default function Profile({
  user,
  version,
  notify,
}: {
  user: Principal;
  version: Version;
  notify: (s: string) => void;
}) {
  const [tab, setTab] = useState("profile");
  return (
    <div className="page profile-page">
      <PageHeader
        eyebrow="Personal workspace"
        title="개인화 및 보안"
        description="개인 프로필, 환경설정, 세션과 API 키를 관리합니다."
      />
      <div className="settings-layout">
        <aside>
          <button
            className={tab === "profile" ? "active" : ""}
            onClick={() => setTab("profile")}
          >
            <UserRound />
            프로필
          </button>
          <button
            className={tab === "keys" ? "active" : ""}
            onClick={() => setTab("keys")}
          >
            <KeyRound />
            API 키
          </button>
          <button
            className={tab === "sessions" ? "active" : ""}
            onClick={() => setTab("sessions")}
          >
            <Laptop2 />
            세션 및 보안
          </button>
          <div />
          <span>Vendra {version.version}</span>
          <small>Commit {version.commit.slice(0, 8)}</small>
        </aside>
        <section>
          {tab === "profile" ? (
            <ProfileForm user={user} notify={notify} />
          ) : tab === "keys" ? (
            <KeyManager notify={notify} />
          ) : (
            <Sessions notify={notify} />
          )}
        </section>
      </div>
    </div>
  );
}

function ProfileForm({
  user,
  notify,
}: {
  user: Principal;
  notify: (s: string) => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await patch("/api/v1/me", {
      displayName: d.get("displayName"),
      locale: d.get("locale"),
      timezone: d.get("timezone"),
    });
    notify("개인 설정을 저장했습니다.");
  }
  return (
    <div className="settings-card">
      <header>
        <span className="profile-hero-avatar">
          {user.displayName.slice(0, 1)}
        </span>
        <div>
          <h2>프로필 정보</h2>
          <p>이 정보는 내부 협업과 승인 기록에 표시됩니다.</p>
        </div>
      </header>
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="이름" required>
            <input
              name="displayName"
              defaultValue={user.displayName}
              required
            />
          </Field>
          <Field label="이메일">
            <input value={user.email} disabled />
          </Field>
          <Field label="언어">
            <select name="locale" defaultValue="ko">
              <option value="ko">한국어</option>
              <option value="en">English</option>
            </select>
          </Field>
          <Field label="시간대">
            <select name="timezone" defaultValue="Asia/Seoul">
              <option>Asia/Seoul</option>
              <option>UTC</option>
              <option>America/New_York</option>
              <option>Europe/London</option>
            </select>
          </Field>
          <Field label="데이터 범위">
            <input value={user.dataScope} disabled />
          </Field>
          <Field label="계정 유형">
            <input value={user.userType} disabled />
          </Field>
        </div>
        <div className="form-actions">
          <button className="button">변경사항 저장</button>
        </div>
      </form>
    </div>
  );
}

function KeyManager({ notify }: { notify: (s: string) => void }) {
  const [keys, setKeys] = useState<APIKey[]>();
  const [create, setCreate] = useState(false);
  const [rotate, setRotate] = useState<APIKey>();
  const [revoke, setRevoke] = useState<APIKey>();
  const [secret, setSecret] = useState("");
  const load = () =>
    api<{ items: APIKey[] }>("/api/v1/me/api-keys").then((x) =>
      setKeys(x.items),
    );
  useEffect(() => {
    load();
  }, []);
  async function createKey(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const r = await post<{ key: string }>("/api/v1/me/api-keys", {
      name: d.get("name"),
      scopes: String(d.get("scopes"))
        .split(",")
        .map((x) => x.trim())
        .filter(Boolean),
      expiresInDays: Number(d.get("expiresInDays")),
    });
    setSecret(r.key);
    setCreate(false);
    load();
  }
  async function rotateKey() {
    if (!rotate) return;
    const r = await post<{ key: string }>(
      `/api/v1/me/api-keys/${rotate.id}/rotate`,
      {},
    );
    setSecret(r.key);
    setRotate(undefined);
    load();
  }
  async function revokeKey() {
    if (!revoke) return;
    await del(`/api/v1/me/api-keys/${revoke.id}`);
    setRevoke(undefined);
    notify("API 키를 폐기했습니다.");
    load();
  }
  return (
    <div className="settings-card">
      <div className="settings-card-head">
        <div>
          <h2>개인 API 키</h2>
          <p>
            REST API와 MCP 클라이언트 인증에 사용합니다. 키별 최소 권한을
            적용하세요.
          </p>
        </div>
        <button className="button" onClick={() => setCreate(true)}>
          <Plus />새 키
        </button>
      </div>
      <div className="security-banner">
        <ShieldCheck />
        <div>
          <b>개인별 키 회전 정책</b>
          <p>
            키 값은 생성 시 한 번만 표시됩니다. 회전하면 기존 키가 즉시 폐기되고
            감사로그가 남습니다.
          </p>
        </div>
      </div>
      {keys?.length ? (
        <div className="key-list">
          {keys.map((k) => (
            <div className={k.revokedAt ? "revoked" : ""} key={k.id}>
              <span className="key-icon">
                <KeyRound />
              </span>
              <div>
                <b>{k.name}</b>
                <code>{k.prefix}••••••••••••</code>
                <small>
                  생성 {date(k.createdAt)} · 최근 사용 {date(k.lastUsedAt)} ·
                  만료 {date(k.expiresAt)}
                </small>
                <span>
                  {k.scopes.map((s) => (
                    <Badge key={s}>{s}</Badge>
                  ))}
                </span>
              </div>
              <div>
                {!k.revokedAt ? (
                  <>
                    <button
                      className="icon-button"
                      title="키 회전"
                      onClick={() => setRotate(k)}
                    >
                      <RefreshCw />
                    </button>
                    <button
                      className="icon-button danger-icon"
                      title="폐기"
                      onClick={() => setRevoke(k)}
                    >
                      <Trash2 />
                    </button>
                  </>
                ) : (
                  <Badge tone="danger">폐기됨</Badge>
                )}
              </div>
            </div>
          ))}
        </div>
      ) : (
        <Empty
          icon={<KeyRound />}
          title="발급된 API 키가 없습니다"
          description="AI Agent, ERP 연동 또는 개발 도구에 사용할 최소 권한 키를 만드세요."
        />
      )}
      {create && (
        <Modal
          title="새 API 키"
          description="키 이름, 권한 범위와 만료 기간을 지정하세요."
          onClose={() => setCreate(false)}
        >
          <form onSubmit={createKey}>
            <Field label="키 이름" required>
              <input
                name="name"
                required
                autoFocus
                placeholder="ERP read integration"
              />
            </Field>
            <Field
              label="Scope"
              hint="비워두면 현재 계정의 읽기 권한을 사용합니다. 쉼표로 구분하세요."
            >
              <input name="scopes" placeholder="supplier.read, contract.read" />
            </Field>
            <Field label="만료 기간">
              <select name="expiresInDays" defaultValue="90">
                <option value="30">30일</option>
                <option value="90">90일</option>
                <option value="180">180일</option>
                <option value="365">1년</option>
              </select>
            </Field>
            <div className="form-actions">
              <button
                type="button"
                className="button secondary"
                onClick={() => setCreate(false)}
              >
                취소
              </button>
              <button className="button">키 생성</button>
            </div>
          </form>
        </Modal>
      )}
      {rotate && (
        <Confirm
          title="API 키를 회전할까요?"
          body="기존 키는 즉시 작동을 멈춥니다. 연결된 클라이언트를 새 키로 업데이트해야 합니다."
          confirmLabel="회전"
          onConfirm={rotateKey}
          onClose={() => setRotate(undefined)}
        />
      )}{" "}
      {revoke && (
        <Confirm
          title="API 키를 폐기할까요?"
          body="이 작업은 되돌릴 수 없으며 이 키를 사용하는 모든 연동이 즉시 중단됩니다."
          confirmLabel="키 폐기"
          danger
          onConfirm={revokeKey}
          onClose={() => setRevoke(undefined)}
        />
      )}{" "}
      {secret && (
        <Modal
          title="API 키가 생성되었습니다"
          description="보안을 위해 이 키는 다시 표시되지 않습니다."
          onClose={() => setSecret("")}
        >
          <div className="secret-reveal">
            <code>{secret}</code>
            <button
              className="button"
              onClick={() => {
                navigator.clipboard.writeText(secret);
                notify("클립보드에 복사했습니다.");
              }}
            >
              <Copy />
              복사
            </button>
          </div>
          <div className="form-error warning">
            <AlertCircle />이 창을 닫기 전에 안전한 비밀 저장소에 보관하세요.
          </div>
          <div className="form-actions">
            <button className="button" onClick={() => setSecret("")}>
              보관 완료
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function PasswordForm({ notify }: { notify: (s: string) => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = e.currentTarget;
    const d = new FormData(form);
    const next = String(d.get("newPassword") || "");
    if (next !== String(d.get("confirmPassword") || "")) {
      setError("새 비밀번호가 서로 일치하지 않습니다.");
      return;
    }
    setError(undefined);
    setBusy(true);
    try {
      const result = await post<{ revokedSessions: number }>(
        "/api/v1/me/password",
        {
          currentPassword: d.get("currentPassword"),
          newPassword: next,
        },
      );
      form.reset();
      notify(
        result.revokedSessions > 0
          ? `비밀번호를 변경하고 다른 세션 ${result.revokedSessions}개를 로그아웃했습니다.`
          : "비밀번호를 변경했습니다.",
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "비밀번호를 변경하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="password-form" onSubmit={submit}>
      <h3>
        <ShieldCheck />
        비밀번호 변경
      </h3>
      <p>
        변경하면 이 브라우저를 제외한 모든 세션이 즉시 로그아웃됩니다. 외부
        인증(OIDC) 계정은 사용할 수 없습니다.
      </p>
      <div className="form-grid">
        <Field label="현재 비밀번호" required>
          <input
            name="currentPassword"
            type="password"
            autoComplete="current-password"
            required
          />
        </Field>
        <div />
        <Field label="새 비밀번호" required hint="10자 이상, 최대 72바이트">
          <input
            name="newPassword"
            type="password"
            autoComplete="new-password"
            required
          />
        </Field>
        <Field label="새 비밀번호 확인" required>
          <input
            name="confirmPassword"
            type="password"
            autoComplete="new-password"
            required
          />
        </Field>
      </div>
      {error && (
        <p className="form-error" role="alert">
          <AlertCircle />
          {error}
        </p>
      )}
      <div className="form-actions">
        <button className="button" disabled={busy}>
          {busy ? "변경 중" : "비밀번호 변경"}
        </button>
      </div>
    </form>
  );
}

function Sessions({ notify }: { notify: (s: string) => void }) {
  return (
    <div className="settings-card">
      <div className="settings-card-head">
        <div>
          <h2>세션 및 보안</h2>
          <p>현재 로그인 세션과 계정 보안 상태를 확인합니다.</p>
        </div>
      </div>
      <div className="session-row">
        <span className="session-icon">
          <Laptop2 />
        </span>
        <div>
          <b>현재 브라우저</b>
          <p>{navigator.userAgent}</p>
          <small>
            <Clock3 />
            지금 활동 중
          </small>
        </div>
        <Badge tone="success">
          <Check />
          현재 세션
        </Badge>
      </div>
      <div className="security-checks">
        <h3>보안 상태</h3>
        <p>
          <Check />
          HttpOnly 세션 쿠키 사용
        </p>
        <p>
          <Check />
          API 키는 SHA-256 해시만 저장
        </p>
        <p>
          <Check />
          모든 키 생성·회전·폐기 감사 추적
        </p>
        <p>
          <Globe2 />
          SSO 정책은 서비스 관리자가 설정
        </p>
      </div>
      <PasswordForm notify={notify} />
    </div>
  );
}
