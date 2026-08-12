import { FormEvent, useCallback, useEffect, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router-dom";
import {
  AlertCircle,
  BarChart3,
  Bell,
  Bot,
  Boxes,
  Building2,
  ChevronDown,
  ClipboardCheck,
  ClipboardList,
  Command,
  FileCheck2,
  FileText,
  Gauge,
  Gavel,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  PackageCheck,
  PanelLeftClose,
  PanelLeftOpen,
  ReceiptText,
  Search,
  Settings,
  ShieldCheck,
  Sparkles,
  Truck,
  UserRound,
  Users,
  X,
} from "lucide-react";
import { api, can, post, Principal, Version } from "./api";
import { Logo, Toast } from "./components";
import Dashboard from "./pages/Dashboard";
import Suppliers, { SupplierDetail } from "./pages/Suppliers";
import Objects from "./pages/Objects";
import Approvals from "./pages/Approvals";
import AIAnalyst from "./pages/AIAnalyst";
import Profile from "./pages/Profile";
import Admin from "./pages/Admin";
import Portal from "./pages/Portal";
import SupplierNetwork from "./pages/SupplierNetwork";
import NotificationCenter from "./NotificationCenter";
import SourcingWorkspace from "./pages/Sourcing";

type Session = { user: Principal; version: Version };

export default function App() {
  const location = useLocation();
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);
  const load = useCallback(async () => {
    try {
      const [user, version] = await Promise.all([
        api<Principal>("/api/v1/me"),
        api<Version>("/api/version"),
      ]);
      setSession({ user, version });
    } catch {
      setSession(null);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  if (loading)
    return (
      <div className="boot">
        <Logo />
        <div className="boot-line" />
      </div>
    );
  if (location.pathname === "/register") return <SupplierRegistration />;
  if (!session) return <Login onLogin={load} />;
  if (session.user.userType === "supplier")
    return (
      <Portal
        user={session.user}
        version={session.version}
        onLogout={() => logout(setSession)}
      />
    );
  return <Shell session={session} onLogout={() => logout(setSession)} />;
}

function SupplierRegistration() {
  const token = new URLSearchParams(window.location.search).get("token") || "";
  const [done, setDone] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState<Version>();
  useEffect(() => {
    api<Version>("/api/version").then(setVersion);
  }, []);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const d = new FormData(e.currentTarget);
    try {
      await post("/api/auth/register", {
        token,
        displayName: d.get("displayName"),
        password: d.get("password"),
        supplierName: d.get("supplierName"),
        businessNumber: d.get("businessNumber"),
      });
      setDone(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : "가입을 완료하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login-page registration-page">
      <section className="login-story">
        <Logo />
        <div className="story-copy">
          <p className="eyebrow">Supplier self-service onboarding</p>
          <h1>
            Vendra 공급업체
            <br />
            <em>업무공간을 시작하세요.</em>
          </h1>
          <p>
            회사정보와 계정을 등록하면 내부 심사, 견적, 계약, 발주, 납품과 자료
            제출을 한곳에서 처리할 수 있습니다.
          </p>
        </div>
      </section>
      <section className="login-panel">
        <div className="login-box">
          <div className="mobile-logo">
            <Logo />
          </div>
          <p className="eyebrow">Supplier invitation</p>
          <h2>{done ? "등록이 완료되었습니다" : "공급업체 계정 등록"}</h2>
          {done ? (
            <>
              <p className="login-lead">
                이메일 인증이 완료된 공급업체 계정으로 로그인하세요.
              </p>
              <a className="button login-button" href="/">
                Vendra 로그인
              </a>
            </>
          ) : !token ? (
            <div className="form-error">
              <AlertCircle />
              초대 토큰이 없습니다. 내부 담당자에게 새 링크를 요청하세요.
            </div>
          ) : (
            <>
              <p className="login-lead">
                초대받은 이메일에 연결될 계정과 회사 기본정보를 입력하세요.
              </p>
              {error && (
                <div className="form-error">
                  <AlertCircle />
                  {error}
                </div>
              )}
              <form onSubmit={submit}>
                <label>
                  담당자 이름
                  <input name="displayName" required autoFocus />
                </label>
                <label>
                  비밀번호
                  <input
                    name="password"
                    type="password"
                    minLength={10}
                    autoComplete="new-password"
                    required
                    placeholder="10자 이상"
                  />
                </label>
                <label>
                  회사명 <small>신규 회사 초대인 경우</small>
                  <input name="supplierName" />
                </label>
                <label>
                  사업자번호 <small>신규 회사 초대인 경우</small>
                  <input name="businessNumber" />
                </label>
                <button className="button login-button" disabled={busy}>
                  {busy ? "등록 중…" : "등록 완료"}
                </button>
              </form>
            </>
          )}
          <p className="login-help">
            Vendra {version?.version || "dev"} · 초대 링크는 한 번만 사용할 수
            있습니다.
          </p>
        </div>
      </section>
    </main>
  );
}

async function logout(setSession: (v: null) => void) {
  try {
    await post("/api/auth/logout", {});
  } finally {
    setSession(null);
  }
}

function Login({ onLogin }: { onLogin: () => void }) {
  const [version, setVersion] = useState<Version>();
  const [oidc, setOIDC] = useState<{ enabled: boolean; issuer?: string }>({
    enabled: false,
  });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    api<Version>("/api/version").then(setVersion);
    api<{ enabled: boolean; issuer?: string }>("/api/auth/oidc/config").then(
      setOIDC,
    );
  }, []);
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const data = new FormData(e.currentTarget);
    try {
      await post("/api/auth/login", {
        email: data.get("email"),
        password: data.get("password"),
      });
      onLogin();
    } catch (e) {
      setError(e instanceof Error ? e.message : "로그인에 실패했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <main className="login-page">
      <section className="login-story">
        <Logo />
        <div className="story-copy">
          <p className="eyebrow">Enterprise supplier intelligence</p>
          <h1>
            공급망의 모든 신호를
            <br />
            <em>한곳에서 판단하세요.</em>
          </h1>
          <p>
            발굴부터 계약, 품질, 리스크, 재계약까지. Vendra는 공급업체
            생명주기의 단일 진실 공급원입니다.
          </p>
        </div>
        <div className="story-metrics">
          <span>
            <b>360°</b>Supplier view
          </span>
          <span>
            <b>1</b>Source of truth
          </span>
          <span>
            <b>24/7</b>Risk signals
          </span>
        </div>
        <div className="story-grid" />
      </section>
      <section className="login-panel">
        <div className="login-box">
          <div className="mobile-logo">
            <Logo />
          </div>
          <p className="eyebrow">Welcome back</p>
          <h2>Vendra에 로그인</h2>
          <p className="login-lead">
            조직 계정으로 공급업체 업무 공간에 접속하세요.
          </p>
          {error && (
            <div className="form-error">
              <AlertCircle />
              {error}
            </div>
          )}
          <form onSubmit={submit}>
            <label>
              이메일
              <input
                type="email"
                name="email"
                autoComplete="username"
                placeholder="name@company.com"
                required
                autoFocus
              />
            </label>
            <label>
              비밀번호
              <input
                type="password"
                name="password"
                autoComplete="current-password"
                placeholder="••••••••••"
                required
              />
            </label>
            <button className="button login-button" disabled={busy}>
              {busy ? "로그인 중…" : "로그인"}
            </button>
          </form>
          {oidc.enabled && (
            <>
              <div className="or">
                <span>또는</span>
              </div>
              <a className="button oidc-button" href="/api/auth/oidc/start">
                Keycloak SSO로 계속
              </a>
            </>
          )}
          <p className="login-help">
            계정 또는 접근 권한은 시스템 관리자에게 문의하세요.
          </p>
        </div>
        <footer>
          Vendra {version?.version || "dev"} <span /> Enterprise Supplier
          Intelligence Platform
        </footer>
      </section>
    </main>
  );
}

type NavItem = {
  label: string;
  path: string;
  icon: typeof Gauge;
  permission?: string;
};
const navGroups: { label: string; items: NavItem[] }[] = [
  {
    label: "Overview",
    items: [
      {
        label: "대시보드",
        path: "/",
        icon: LayoutDashboard,
        permission: "dashboard.read",
      },
      {
        label: "통합 검색",
        path: "/search",
        icon: Search,
        permission: "*.read",
      },
    ],
  },
  {
    label: "Supplier",
    items: [
      {
        label: "공급업체",
        path: "/suppliers",
        icon: Building2,
        permission: "supplier.read",
      },
      {
        label: "공급업체 평가",
        path: "/evaluations",
        icon: ClipboardCheck,
        permission: "evaluation.read",
      },
      {
        label: "리스크",
        path: "/risks",
        icon: ShieldCheck,
        permission: "risk.read",
      },
    ],
  },
  {
    label: "Procurement",
    items: [
      {
        label: "구매요청",
        path: "/purchase-requests",
        icon: ClipboardList,
        permission: "purchase_request.read",
      },
      { label: "RFQ", path: "/rfq", icon: ReceiptText, permission: "rfq.read" },
      {
        label: "RFP · 입찰",
        path: "/rfp",
        icon: Gavel,
        permission: "rfp.read",
      },
    ],
  },
  {
    label: "Contract & Purchase",
    items: [
      {
        label: "계약",
        path: "/contracts",
        icon: FileText,
        permission: "contract.read",
      },
      {
        label: "발주",
        path: "/purchase-orders",
        icon: Boxes,
        permission: "purchase_order.read",
      },
      {
        label: "납품",
        path: "/deliveries",
        icon: Truck,
        permission: "delivery.read",
      },
      {
        label: "검수",
        path: "/inspections",
        icon: PackageCheck,
        permission: "inspection.read",
      },
      {
        label: "Invoice",
        path: "/invoices",
        icon: FileCheck2,
        permission: "invoice.read",
      },
      {
        label: "지급",
        path: "/payments",
        icon: ReceiptText,
        permission: "payment.read",
      },
    ],
  },
  {
    label: "Quality",
    items: [
      {
        label: "품질 · CAPA",
        path: "/quality",
        icon: FileCheck2,
        permission: "quality.read",
      },
      {
        label: "이슈",
        path: "/issues",
        icon: AlertCircle,
        permission: "issue.read",
      },
    ],
  },
  {
    label: "Intelligence",
    items: [
      {
        label: "Spend 분석",
        path: "/spend",
        icon: BarChart3,
        permission: "spend.read",
      },
      {
        label: "공급망 Network",
        path: "/network",
        icon: Network,
        permission: "supplier.read",
      },
      { label: "AI Analyst", path: "/ai", icon: Bot, permission: "ai.use" },
    ],
  },
  {
    label: "Workspace",
    items: [
      {
        label: "내 승인함",
        path: "/approvals",
        icon: ClipboardCheck,
        permission: "workflow.read",
      },
    ],
  },
];

function Shell({
  session,
  onLogout,
}: {
  session: Session;
  onLogout: () => void;
}) {
  const { user, version } = session;
  const location = useLocation();
  const navigate = useNavigate();
  const [collapsed, setCollapsed] = useState(false);
  const [mobile, setMobile] = useState(false);
  const [profile, setProfile] = useState(false);
  const [search, setSearch] = useState("");
  const [toast, setToast] = useState("");
  const groups = navGroups
    .map((g) => ({
      ...g,
      items: g.items.filter((i) => !i.permission || can(user, i.permission)),
    }))
    .filter((g) => g.items.length);
  function submitSearch(e: FormEvent) {
    e.preventDefault();
    if (search.trim())
      navigate("/search?q=" + encodeURIComponent(search.trim()));
  }
  return (
    <div className={`app-shell ${collapsed ? "collapsed" : ""}`}>
      <aside className={mobile ? "mobile-open" : ""}>
        <div className="aside-head">
          <Logo compact={collapsed} />
          <button className="mobile-close" onClick={() => setMobile(false)}>
            <X />
          </button>
        </div>
        <nav>
          {groups.map((group) => (
            <div className="nav-group" key={group.label}>
              <span>{collapsed ? "" : group.label}</span>
              {group.items.map((item) => (
                <button
                  key={item.path}
                  title={item.label}
                  className={
                    (
                      item.path === "/"
                        ? location.pathname === "/"
                        : location.pathname.startsWith(item.path)
                    )
                      ? "active"
                      : ""
                  }
                  onClick={() => {
                    navigate(item.path);
                    setMobile(false);
                  }}
                >
                  <item.icon />
                  <b>{item.label}</b>
                </button>
              ))}
            </div>
          ))}
        </nav>
        <div className="aside-foot">
          {can(user, "*") && (
            <button
              className={location.pathname.startsWith("/admin") ? "active" : ""}
              onClick={() => navigate("/admin")}
            >
              <Settings />
              <b>서비스 관리</b>
            </button>
          )}
          <button onClick={() => setCollapsed((v) => !v)} className="collapse">
            {collapsed ? <PanelLeftOpen /> : <PanelLeftClose />}
            <b>메뉴 접기</b>
          </button>
        </div>
      </aside>
      {mobile && (
        <div className="sidebar-scrim" onClick={() => setMobile(false)} />
      )}
      <section className="workspace">
        <header className="topbar">
          <button className="menu-toggle" onClick={() => setMobile(true)}>
            <Menu />
          </button>
          <form className="global-search" onSubmit={submitSearch}>
            <Search />
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="공급업체, 계약, 발주, 이슈 검색"
            />
            <kbd>⌘ K</kbd>
          </form>
          <div className="top-actions">
            <button className="icon-button" title="명령 팔레트">
              <Command />
            </button>
            <NotificationCenter />
            <div className="profile-area">
              <button
                className="profile-button"
                onClick={() => setProfile((v) => !v)}
              >
                <span className="avatar">
                  {user.displayName.slice(0, 1).toUpperCase()}
                </span>
                <span>
                  <b>{user.displayName}</b>
                  <small>{user.email}</small>
                </span>
                <ChevronDown />
              </button>
              {profile && (
                <div className="profile-menu">
                  <div className="profile-menu-head">
                    <span className="avatar large">
                      {user.displayName.slice(0, 1).toUpperCase()}
                    </span>
                    <div>
                      <b>{user.displayName}</b>
                      <small>{user.email}</small>
                    </div>
                  </div>
                  <button
                    onClick={() => {
                      navigate("/profile");
                      setProfile(false);
                    }}
                  >
                    <UserRound />
                    개인화 및 키 관리
                  </button>
                  {can(user, "*") && (
                    <button
                      onClick={() => {
                        navigate("/admin");
                        setProfile(false);
                      }}
                    >
                      <Settings />
                      서비스 관리자
                    </button>
                  )}
                  <div className="version-line">
                    <span>Vendra {version.version}</span>
                    <small>{version.commit.slice(0, 8)}</small>
                  </div>
                  <button className="logout" onClick={onLogout}>
                    <LogOut />
                    로그아웃
                  </button>
                </div>
              )}
            </div>
          </div>
        </header>
        <main className="main-content">
          <Routes>
            <Route path="/" element={<Dashboard />} />
            <Route path="/suppliers" element={<Suppliers />} />
            <Route path="/suppliers/:id" element={<SupplierDetail />} />
            <Route path="/approvals" element={<Approvals />} />
            <Route path="/ai" element={<AIAnalyst />} />
            <Route
              path="/profile"
              element={
                <Profile user={user} version={version} notify={setToast} />
              }
            />
            <Route
              path="/admin/*"
              element={
                can(user, "*") ? (
                  <Admin notify={setToast} />
                ) : (
                  <Navigate to="/" />
                )
              }
            />
            <Route path="/search" element={<Objects type="search" />} />
            <Route
              path="/evaluations"
              element={<Objects type="evaluation" />}
            />
            <Route path="/risks" element={<Objects type="risk" />} />
            <Route path="/spend" element={<Objects type="spend" />} />
            <Route path="/network" element={<SupplierNetwork />} />
            <Route path="/sourcing/:type/:id" element={<SourcingWorkspace />} />
            {objectPages.map((x) => (
              <Route
                key={x.path}
                path={x.path}
                element={<Objects type={x.type} />}
              />
            ))}
            <Route path="*" element={<Navigate to="/" />} />
          </Routes>
        </main>
      </section>
      {toast && <Toast message={toast} onClose={() => setToast("")} />}
    </div>
  );
}

const objectPages = [
  { path: "/contracts", type: "contract" },
  { path: "/purchase-requests", type: "purchase_request" },
  { path: "/rfq", type: "rfq" },
  { path: "/rfp", type: "rfp" },
  { path: "/purchase-orders", type: "purchase_order" },
  { path: "/deliveries", type: "delivery" },
  { path: "/inspections", type: "inspection" },
  { path: "/quality", type: "quality" },
  { path: "/issues", type: "issue" },
  { path: "/invoices", type: "invoice" },
  { path: "/payments", type: "payment" },
];
