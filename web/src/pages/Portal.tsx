import { FormEvent, useCallback, useEffect, useState } from "react";
import {
  Building2,
  ChevronRight,
  FileCheck2,
  FileText,
  HelpCircle,
  LayoutDashboard,
  LogOut,
  Menu,
  PackageCheck,
  ReceiptText,
  Send,
  Settings,
  ShieldCheck,
  Truck,
  Upload,
  Users,
  X,
} from "lucide-react";
import { api, date, money, patch, post, Principal, put, Version } from "../api";
import {
  Badge,
  Empty,
  Field,
  Loading,
  Logo,
  Modal,
  RiskBadge,
} from "../components";
import { statusTone } from "../status";
import { errorMessage, useNotify } from "../toast-context";
import { BusinessObject, Supplier } from "../types";
import { DocumentUpload } from "./Suppliers";

type SourcingItem = BusinessObject & {
  response?: {
    id: string;
    status: string;
    currency: string;
    totalAmount?: number;
    deliveryDays?: number;
    warranty?: string;
    validityDate?: string;
    commercialTerms?: Record<string, unknown>;
    technicalResponse?: Record<string, unknown>;
  };
};
type PortalEvaluation = {
  id: string;
  evaluationType: string;
  templateName?: string;
  totalScore?: number;
  grade?: string;
  comments?: string;
  completedAt: string;
};

const portalSections = new Set([
  "home",
  "rfq",
  "contract",
  "purchase_order",
  "delivery",
  "invoice",
  "evaluation",
  "issue",
  "contacts",
  "profile",
]);

export default function Portal({
  user,
  version,
  onLogout,
}: {
  user: Principal;
  version: Version;
  onLogout: () => void;
}) {
  const [profile, setProfile] = useState<{
    supplier: Supplier;
    user: Principal;
  }>();
  const [work, setWork] = useState<BusinessObject[]>();
  const [sourcing, setSourcing] = useState<SourcingItem[]>();
  const [evaluations, setEvaluations] = useState<PortalEvaluation[]>();
  const [section, setSection] = useState(() => {
    const initial = window.location.hash.slice(1);
    return portalSections.has(initial) ? initial : "home";
  });
  const [mobileNav, setMobileNav] = useState(false);
  const [help, setHelp] = useState(false);
  const load = useCallback(() => {
    Promise.all([
      api<{ supplier: Supplier; user: Principal }>("/api/v1/portal/profile"),
      api<{ items: BusinessObject[] }>("/api/v1/portal/work"),
      api<{ items: SourcingItem[] }>("/api/v1/portal/sourcing"),
      api<{ items: PortalEvaluation[] }>("/api/v1/portal/evaluations"),
    ]).then(([p, w, q, e]) => {
      setProfile(p);
      setWork(w.items);
      setSourcing(q.items);
      setEvaluations(e.items);
    });
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    const syncSection = () => {
      const next = window.location.hash.slice(1);
      if (portalSections.has(next)) setSection(next);
    };
    window.addEventListener("hashchange", syncSection);
    window.addEventListener("popstate", syncSection);
    return () => {
      window.removeEventListener("hashchange", syncSection);
      window.removeEventListener("popstate", syncSection);
    };
  }, []);
  useEffect(() => {
    if (!mobileNav) return;
    const overflow = document.body.style.overflow;
    const close = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMobileNav(false);
    };
    document.body.style.overflow = "hidden";
    document.addEventListener("keydown", close);
    return () => {
      document.body.style.overflow = overflow;
      document.removeEventListener("keydown", close);
    };
  }, [mobileNav]);
  if (!profile || !work || !sourcing || !evaluations)
    return <Loading label="공급업체 포털을 준비하는 중" />;
  const s = profile.supplier;
  const selectSection = (next: string) => {
    setSection(next);
    if (window.location.hash !== `#${next}`) {
      window.history.pushState(null, "", `#${next}`);
    }
  };
  const nav = [
    { id: "home", label: "홈", icon: LayoutDashboard },
    { id: "rfq", label: "견적 · 입찰", icon: ReceiptText },
    { id: "contract", label: "계약", icon: FileText },
    { id: "purchase_order", label: "발주", icon: PackageCheck },
    { id: "delivery", label: "납품", icon: Truck },
    { id: "invoice", label: "Invoice", icon: FileCheck2 },
    { id: "evaluation", label: "평가 결과", icon: FileCheck2 },
    { id: "issue", label: "개선요청 · 문의", icon: HelpCircle },
    { id: "contacts", label: "담당자", icon: ShieldCheck },
    { id: "profile", label: "회사정보", icon: Building2 },
  ];
  return (
    <div className="portal-shell">
      <a className="skip-link" href="#portal-main">
        본문으로 바로가기
      </a>
      <aside className={mobileNav ? "mobile-open" : ""}>
        <div className="portal-aside-head">
          <Logo />
          <button
            className="icon-button portal-mobile-close"
            onClick={() => setMobileNav(false)}
            aria-label="포털 메뉴 닫기"
          >
            <X />
          </button>
        </div>
        <div className="portal-company">
          <span className="company-avatar large">{s.name.slice(0, 2)}</span>
          <div>
            <b>{s.name}</b>
            <small>{s.supplierNumber}</small>
          </div>
        </div>
        <nav>
          {nav.map((n) => (
            <button
              key={n.id}
              className={section === n.id ? "active" : ""}
              onClick={() => {
                selectSection(n.id);
                setMobileNav(false);
              }}
            >
              <n.icon />
              {n.label}
            </button>
          ))}
        </nav>
        <footer>
          <button onClick={() => setHelp(true)}>
            <HelpCircle />
            도움말
          </button>
          <button onClick={onLogout}>
            <LogOut />
            로그아웃
          </button>
          <span>Vendra Supplier Portal {version.version}</span>
        </footer>
      </aside>
      {mobileNav && (
        <div
          className="portal-scrim"
          onClick={() => setMobileNav(false)}
          role="presentation"
        />
      )}
      <main id="portal-main" tabIndex={-1}>
        <header>
          <button
            className="icon-button"
            onClick={() => setMobileNav(true)}
            aria-label="포털 메뉴 열기"
          >
            <Menu />
          </button>
          <div>
            <span>Supplier portal</span>
            <b>{user.displayName}</b>
          </div>
        </header>
        {section === "home" ? (
          <PortalHome
            supplier={s}
            work={[...work, ...sourcing]}
            setSection={selectSection}
          />
        ) : section === "profile" ? (
          <CompanyProfile supplier={s} onSaved={load} />
        ) : section === "contacts" ? (
          <PortalContacts />
        ) : section === "rfq" ? (
          <PortalSourcing items={sourcing} onSaved={load} />
        ) : section === "evaluation" ? (
          <PortalEvaluations items={evaluations} />
        ) : (
          <PortalWork
            type={section}
            items={work.filter((x) => x.objectType === section)}
            related={work}
            onSaved={load}
          />
        )}
      </main>
      {help && (
        <Modal title="Vendra Supplier Portal 도움말" onClose={() => setHelp(false)}>
          <div className="portal-help">
            <p><b>견적 · 입찰</b> 초대된 RFQ/RFP 응답을 임시 저장하거나 최종 제출합니다.</p>
            <p><b>계약 · 발주</b> 내부 담당자가 전달한 내용을 확인하고 수신 상태를 기록합니다.</p>
            <p><b>납품 · Invoice</b> 관련 발주를 선택해 실적과 증빙 정보를 등록합니다.</p>
            <p><b>자료 제출</b> 문서는 체크섬과 버전, 다운로드 이력과 함께 보관됩니다.</p>
            <p><b>빠른 이동</b> 모바일에서는 상단 메뉴 버튼으로 모든 포털 업무로 이동합니다.</p>
          </div>
        </Modal>
      )}
    </div>
  );
}
function PortalHome({
  supplier: s,
  work,
  setSection,
}: {
  supplier: Supplier;
  work: BusinessObject[];
  setSection: (s: string) => void;
}) {
  const open = work.filter((x) => !["completed", "closed"].includes(x.status));
  const [upload, setUpload] = useState(false);
  return (
    <div className="portal-page">
      <div className="portal-welcome">
        <div>
          <p className="eyebrow">Partner workspace</p>
          <h1>
            {s.name} 담당자님,
            <br />
            안녕하세요.
          </h1>
          <p>요청된 업무와 제출 기한을 확인하고 자료를 안전하게 공유하세요.</p>
        </div>
        <div className="portal-grade">
          <span>Supplier status</span>
          <Badge tone={statusTone(s.status)}>{s.status}</Badge>
          <RiskBadge level={s.riskLevel} />
          <strong>{s.grade || "—"}</strong>
          <small>최근 평가 등급</small>
        </div>
      </div>
      <div className="portal-kpis">
        <div>
          <ReceiptText />
          <span>
            <b>
              {work.filter((x) => ["rfq", "rfp"].includes(x.objectType)).length}
            </b>
            진행 중인 견적
          </span>
        </div>
        <div>
          <FileText />
          <span>
            <b>{work.filter((x) => x.objectType === "contract").length}</b>계약
          </span>
        </div>
        <div>
          <PackageCheck />
          <span>
            <b>
              {work.filter((x) => x.objectType === "purchase_order").length}
            </b>
            발주
          </span>
        </div>
        <div>
          <Truck />
          <span>
            <b>{work.filter((x) => x.objectType === "delivery").length}</b>납품
          </span>
        </div>
      </div>
      <section className="portal-grid">
        <div className="card">
          <div className="card-head">
            <h2>조치가 필요한 업무</h2>
            <Badge tone="warning">{open.length}</Badge>
          </div>
          {open.slice(0, 8).map((o) => (
            <button
              className="portal-task"
              onClick={() => setSection(o.objectType)}
              key={o.id}
            >
              <span className="object-icon">
                <Send />
              </span>
              <div>
                <b>{o.title}</b>
                <p>
                  {o.number} · {date(o.dueDate)}
                </p>
              </div>
              <Badge tone={statusTone(o.status)}>{o.status}</Badge>
              <ChevronRight />
            </button>
          ))}
          {!open.length && (
            <Empty
              title="대기 중인 업무가 없습니다"
              description="새 견적, 발주 또는 개선 요청을 받으면 이곳에 표시됩니다."
            />
          )}
        </div>
        <div className="card document-drop">
          <Upload />
          <h2>자료 제출</h2>
          <p>사업자등록증, 인증서, 제안서와 납품 증빙을 안전하게 제출하세요.</p>
          <button className="button" onClick={() => setUpload(true)}>
            <Upload />
            문서 선택
          </button>
          <small>파일 체크섬과 다운로드 감사기록이 보존됩니다.</small>
        </div>
      </section>
      {upload && (
        <DocumentUpload
          portal
          onClose={() => setUpload(false)}
          onSaved={() => setUpload(false)}
        />
      )}
    </div>
  );
}
function PortalWork({
  type,
  items,
  related,
  onSaved,
}: {
  type: string;
  items: BusinessObject[];
  related: BusinessObject[];
  onSaved: () => void;
}) {
  const [create, setCreate] = useState(false);
  const [confirming, setConfirming] = useState<string>();
  const notify = useNotify();
  async function confirm(id: string, objectType: string) {
    const endpoint =
      objectType === "contract" ? "contracts" : "purchase-orders";
    setConfirming(id);
    try {
      await post(`/api/v1/portal/${endpoint}/${id}/confirm`, {});
      notify(
        objectType === "contract"
          ? "계약을 확인했습니다."
          : "발주를 확인했습니다.",
      );
      onSaved();
    } catch (e) {
      notify(errorMessage(e, "확인 처리를 하지 못했습니다"), "error");
    } finally {
      setConfirming(undefined);
    }
  }
  return (
    <div className="portal-page">
      <div className="portal-page-head">
        <div>
          <p className="eyebrow">Supplier work</p>
          <h1>
            {
              (
                {
                  rfq: "견적 · 입찰",
                  contract: "계약",
                  purchase_order: "발주",
                  delivery: "납품",
                  invoice: "Invoice",
                  issue: "개선요청 · 문의",
                } as Record<string, string>
              )[type]
            }
          </h1>
        </div>
        {["delivery", "invoice", "issue"].includes(type) && (
          <button className="button" onClick={() => setCreate(true)}>
            <Upload />
            {type === "issue" ? "문의 등록" : "새로 등록"}
          </button>
        )}
      </div>
      <div className="data-card">
        {items.length ? (
          <table>
            <thead>
              <tr>
                <th>번호 · 제목</th>
                <th>상태</th>
                <th>금액</th>
                <th>기한</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {items.map((o) => (
                <tr key={o.id}>
                  <td>
                    <span className="stack">
                      <b>{o.title}</b>
                      <small>{o.number}</small>
                    </span>
                  </td>
                  <td>
                    <Badge tone={statusTone(o.status)}>{o.status}</Badge>
                  </td>
                  <td>{money(o.amount)}</td>
                  <td>{date(o.dueDate || o.endDate)}</td>
                  <td>
                    {type === "purchase_order" &&
                    ["approved", "sent"].includes(o.status) ? (
                      <button
                        className="button secondary compact"
                        onClick={() => confirm(o.id, type)}
                        disabled={confirming === o.id}
                      >
                        {confirming === o.id ? "확인 중…" : "발주 확인"}
                      </button>
                    ) : type === "contract" &&
                      ["approved", "active", "sent", "executed"].includes(
                        o.status,
                      ) &&
                      !o.data?.supplierAcknowledgedAt ? (
                      <button
                        className="button secondary compact"
                        onClick={() => confirm(o.id, type)}
                        disabled={confirming === o.id}
                      >
                        {confirming === o.id ? "확인 중…" : "계약 확인"}
                      </button>
                    ) : (
                      <ChevronRight />
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <Empty
            title="표시할 업무가 없습니다"
            description="내부 담당자가 요청을 보내면 이 목록에 표시됩니다."
          />
        )}
      </div>
      {create && (
        <PortalObjectForm
          type={type}
          parents={
            type === "delivery"
              ? related.filter((x) => x.objectType === "purchase_order")
              : []
          }
          onClose={() => setCreate(false)}
          onSaved={() => {
            setCreate(false);
            onSaved();
          }}
        />
      )}
    </div>
  );
}

function PortalSourcing({
  items,
  onSaved,
}: {
  items: SourcingItem[];
  onSaved: () => void;
}) {
  const [selected, setSelected] = useState<SourcingItem>();
  const [declining, setDeclining] = useState<SourcingItem>();
  const [declineReason, setDeclineReason] = useState("");
  const [declineBusy, setDeclineBusy] = useState(false);
  const notify = useNotify();
  async function decline() {
    if (!declining) return;
    setDeclineBusy(true);
    try {
      await post(`/api/v1/portal/sourcing/${declining.id}/decline`, {
        reason: declineReason,
      });
      notify("참여 거절을 전달했습니다.");
      setDeclining(undefined);
      setDeclineReason("");
      onSaved();
    } catch (e) {
      notify(errorMessage(e, "거절을 전달하지 못했습니다"), "error");
    } finally {
      setDeclineBusy(false);
    }
  }
  return (
    <div className="portal-page">
      <div className="portal-page-head">
        <div>
          <p className="eyebrow">Quotation & proposal workspace</p>
          <h1>견적 · 입찰</h1>
          <p>초대받은 RFQ/RFP의 요구사항을 확인하고 응답을 제출하세요.</p>
        </div>
      </div>
      <div className="portal-sourcing-grid">
        {items.map((item) => (
          <article className="card portal-sourcing-card" key={item.id}>
            <header>
              <span className="object-icon">
                <ReceiptText />
              </span>
              <div>
                <small>{item.objectType.toUpperCase()}</small>
                <h2>{item.title}</h2>
                <p>{item.number}</p>
              </div>
              <Badge tone={statusTone(item.response?.status || item.status)}>
                {item.response?.status || item.status}
              </Badge>
            </header>
            <dl>
              <dt>제출 마감</dt>
              <dd>{date(item.dueDate)}</dd>
              <dt>제출 금액</dt>
              <dd>{money(item.response?.totalAmount)}</dd>
              <dt>요청 개요</dt>
              <dd>{String(item.data?.description || "세부 요구조건 참조")}</dd>
            </dl>
            <footer>
              {!item.response && item.status !== "closed" && (
                <button
                  className="button ghost danger-text"
                  onClick={() => setDeclining(item)}
                >
                  참여 거절
                </button>
              )}
              <button
                className="button"
                disabled={item.status === "closed"}
                onClick={() => setSelected(item)}
              >
                <Send />
                {item.response ? "응답 확인 · 수정" : "응답 작성"}
              </button>
            </footer>
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="초대받은 견적·입찰이 없습니다"
          description="새 RFQ 또는 RFP 초대를 받으면 마감일과 제출 상태를 이곳에서 확인할 수 있습니다."
        />
      )}
      {selected && (
        <SourcingResponseForm
          item={selected}
          onClose={() => setSelected(undefined)}
          onSaved={() => {
            setSelected(undefined);
            onSaved();
          }}
        />
      )}
      {declining && (
        <Modal
          title="견적 · 입찰 참여 거절"
          description={`${declining.number} · ${declining.title}`}
          onClose={() => setDeclining(undefined)}
        >
          <Field label="거절 사유" required>
            <textarea
              rows={5}
              value={declineReason}
              onChange={(event) => setDeclineReason(event.target.value)}
              placeholder="내부 담당자가 후속 조치를 판단할 수 있도록 사유를 입력하세요."
              autoFocus
            />
          </Field>
          <div className="form-actions">
            <button
              type="button"
              className="button secondary"
              onClick={() => setDeclining(undefined)}
            >
              취소
            </button>
            <button
              type="button"
              className="button danger"
              onClick={() => void decline()}
              disabled={declineBusy || !declineReason.trim()}
            >
              {declineBusy ? "전달 중…" : "참여 거절 확정"}
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function SourcingResponseForm({
  item,
  onClose,
  onSaved,
}: {
  item: SourcingItem;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setBusy(true);
    setError("");
    const d = new FormData(e.currentTarget);
    const submitter = (e.nativeEvent as SubmitEvent)
      .submitter as HTMLButtonElement | null;
    try {
      await put(`/api/v1/portal/sourcing/${item.id}/response`, {
        submit: submitter?.value === "submit",
        currency: d.get("currency"),
        totalAmount: Number(d.get("totalAmount")) || undefined,
        deliveryDays: Number(d.get("deliveryDays")) || undefined,
        warranty: d.get("warranty"),
        validityDate: d.get("validityDate"),
        commercialTerms: { text: d.get("commercialTerms") },
        technicalResponse: { text: d.get("technicalResponse") },
        lineItems: [],
      });
      onSaved();
    } catch (e) {
      setError(e instanceof Error ? e.message : "응답을 저장하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  const response = item.response;
  return (
    <Modal
      title={`${item.objectType.toUpperCase()} 응답`}
      description={`${item.number} · ${item.title} · 마감 ${date(item.dueDate)}`}
      onClose={onClose}
      wide
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="총 견적금액" required>
            <input
              name="totalAmount"
              type="number"
              min="0"
              required
              defaultValue={response?.totalAmount}
            />
          </Field>
          <Field label="통화">
            <select name="currency" defaultValue={response?.currency || "KRW"}>
              <option>KRW</option>
              <option>USD</option>
              <option>EUR</option>
              <option>JPY</option>
            </select>
          </Field>
          <Field label="납기 (일)">
            <input
              name="deliveryDays"
              type="number"
              min="1"
              defaultValue={response?.deliveryDays}
            />
          </Field>
          <Field label="견적 유효일">
            <input
              name="validityDate"
              type="date"
              defaultValue={response?.validityDate?.slice(0, 10)}
            />
          </Field>
          <Field label="Warranty">
            <input
              name="warranty"
              placeholder="예: 검수 후 2년"
              defaultValue={response?.warranty}
            />
          </Field>
          <Field label="상업 조건">
            <textarea
              name="commercialTerms"
              rows={4}
              defaultValue={String(response?.commercialTerms?.text || "")}
              placeholder="지급조건, 가격 조건, 유효기간 등"
            />
          </Field>
          <Field label="기술 제안">
            <textarea
              name="technicalResponse"
              rows={6}
              defaultValue={String(response?.technicalResponse?.text || "")}
              placeholder="요구사항 충족 방법과 기술 제안을 입력하세요."
            />
          </Field>
        </div>
        {error && <div className="form-error">{error}</div>}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button
            className="button secondary"
            name="action"
            value="draft"
            disabled={busy}
          >
            임시 저장
          </button>
          <button
            className="button"
            name="action"
            value="submit"
            disabled={busy}
          >
            <Send /> 최종 제출
          </button>
        </div>
      </form>
      <PortalSourcingQuestions sourcingId={item.id} />
    </Modal>
  );
}

function PortalSourcingQuestions({ sourcingId }: { sourcingId: string }) {
  const [items, setItems] = useState<
    {
      id: string;
      askedBy: string;
      question: string;
      answer?: string;
      visibility: string;
    }[]
  >();
  const [question, setQuestion] = useState("");
  const [privateQuestion, setPrivateQuestion] = useState(false);
  const load = useCallback(
    () =>
      api<{ items: typeof items }>(
        `/api/v1/portal/sourcing/${sourcingId}/questions`,
      ).then((x) => setItems(x.items || [])),
    [sourcingId],
  );
  useEffect(() => {
    void load();
  }, [load]);
  async function ask(e: FormEvent) {
    e.preventDefault();
    await post(`/api/v1/portal/sourcing/${sourcingId}/questions`, {
      question,
      private: privateQuestion,
    });
    setQuestion("");
    load();
  }
  return (
    <section className="portal-questions">
      <h3>질의응답</h3>
      {items?.map((item) => (
        <article key={item.id}>
          <b>{item.question}</b>
          {item.answer && <p>{item.answer}</p>}
          <small>{item.visibility}</small>
        </article>
      ))}
      <form onSubmit={ask}>
        <input
          value={question}
          onChange={(e) => setQuestion(e.target.value)}
          placeholder="질문을 입력하세요"
          required
        />
        <label>
          <input
            type="checkbox"
            checked={privateQuestion}
            onChange={(e) => setPrivateQuestion(e.target.checked)}
          />
          비공개 질문
        </label>
        <button className="button secondary compact">질문 등록</button>
      </form>
    </section>
  );
}

function PortalObjectForm({
  type,
  parents,
  onClose,
  onSaved,
}: {
  type: string;
  parents: BusinessObject[];
  onClose: () => void;
  onSaved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const endpoint =
      type === "delivery"
        ? "deliveries"
        : type === "issue"
          ? "inquiries"
          : "invoices";
    await post(`/api/v1/portal/${endpoint}`, {
      title: d.get("title"),
      parentId: d.get("parentId"),
      amount: Number(d.get("amount")) || undefined,
      currency: "KRW",
      dueDate: d.get("dueDate"),
      data: {
        referenceNumber: d.get("referenceNumber"),
        quantity: Number(d.get("quantity")) || undefined,
        notes: d.get("notes"),
      },
    });
    onSaved();
  }
  return (
    <Modal
      title={
        type === "delivery"
          ? "납품 등록"
          : type === "issue"
            ? "개선요청 · 문의 등록"
            : "Invoice 등록"
      }
      description="내부 담당자가 확인할 거래 정보와 증빙 문서를 등록하세요."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <Field label="제목" required>
          <input name="title" required autoFocus />
        </Field>
        {parents.length > 0 && (
          <Field label="관련 발주">
            <select name="parentId">
              <option value="">선택 안 함</option>
              {parents.map((parent) => (
                <option value={parent.id} key={parent.id}>
                  {parent.number} · {parent.title}
                </option>
              ))}
            </select>
          </Field>
        )}
        <div className="form-grid">
          <Field label="참조번호">
            <input name="referenceNumber" />
          </Field>
          <Field label="수량">
            <input name="quantity" type="number" min="0" />
          </Field>
          <Field label="금액">
            <input name="amount" type="number" min="0" />
          </Field>
          <Field label="예정일">
            <input name="dueDate" type="date" />
          </Field>
        </div>
        <Field label="비고">
          <textarea name="notes" rows={3} />
        </Field>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">등록</button>
        </div>
      </form>
    </Modal>
  );
}

type PortalContact = {
  id: string;
  name: string;
  title?: string;
  department?: string;
  email?: string;
  phone?: string;
  primary: boolean;
  emailVerified: boolean;
};

function PortalEvaluations({ items }: { items: PortalEvaluation[] }) {
  return (
    <div className="portal-page">
      <div className="portal-page-head">
        <div>
          <p className="eyebrow">Supplier performance</p>
          <h1>평가 결과</h1>
          <p>확정된 공급업체 성과평가와 개선 의견을 확인하세요.</p>
        </div>
      </div>
      <div className="portal-evaluation-grid">
        {items.map((item) => (
          <article className="card" key={item.id}>
            <header>
              <div>
                <small>{item.evaluationType}</small>
                <h2>{item.templateName || "공급업체 평가"}</h2>
              </div>
              <Badge tone={statusTone(item.grade)}>{item.grade || "—"}</Badge>
            </header>
            <strong>
              {item.totalScore?.toFixed(1) || "—"}
              <small> / 100</small>
            </strong>
            <p>{item.comments || "등록된 평가 의견이 없습니다."}</p>
            <footer>{date(item.completedAt)}</footer>
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="공개된 평가 결과가 없습니다"
          description="내부 평가가 완료되면 이곳에 표시됩니다."
        />
      )}
    </div>
  );
}

function PortalContacts() {
  const [items, setItems] = useState<PortalContact[]>();
  const [create, setCreate] = useState(false);
  const [verificationUrl, setVerificationUrl] = useState("");
  const notify = useNotify();
  const load = useCallback(
    () =>
      api<{ items: PortalContact[] }>("/api/v1/portal/contacts").then((x) =>
        setItems(x.items),
      ),
    [],
  );
  useEffect(() => {
    void load();
  }, [load]);
  if (!items) return <Loading />;
  async function verify(id: string) {
    try {
      const result = await post<{ verificationUrl: string }>(
        `/api/v1/portal/contacts/${id}/verification`,
        {},
      );
      setVerificationUrl(result.verificationUrl);
    } catch (e) {
      notify(errorMessage(e, "인증 링크를 만들지 못했습니다"), "error");
    }
  }
  return (
    <div className="portal-page">
      <div className="portal-page-head">
        <div>
          <p className="eyebrow">People & verification</p>
          <h1>담당자</h1>
          <p>업무 담당자와 이메일 인증 상태를 관리합니다.</p>
        </div>
        <button className="button" onClick={() => setCreate(true)}>
          <Users /> 담당자 추가
        </button>
      </div>
      {verificationUrl && (
        <div className="security-banner portal-verification">
          <ShieldCheck />
          <div>
            <b>이메일 인증 링크가 생성되었습니다.</b>
            <p>알림 Adapter 또는 사내 메일로 아래 링크를 전달하세요.</p>
            <code>{verificationUrl}</code>
          </div>
          <button
            className="button secondary compact"
            onClick={() =>
              navigator.clipboard.writeText(location.origin + verificationUrl)
            }
          >
            링크 복사
          </button>
        </div>
      )}
      <div className="portal-contact-grid">
        {items.map((contact) => (
          <article className="card" key={contact.id}>
            <header>
              <span className="avatar">{contact.name.slice(0, 1)}</span>
              <div>
                <h2>{contact.name}</h2>
                <p>{contact.title || contact.department || "담당자"}</p>
              </div>
              {contact.primary && <Badge tone="purple">대표</Badge>}
            </header>
            <p>{contact.email || "이메일 없음"}</p>
            <p>{contact.phone || "전화번호 없음"}</p>
            {contact.email && (
              <button
                className={`button ${contact.emailVerified ? "secondary" : ""}`}
                disabled={contact.emailVerified}
                onClick={() => verify(contact.id)}
              >
                <ShieldCheck />
                {contact.emailVerified
                  ? "이메일 인증 완료"
                  : "이메일 인증 요청"}
              </button>
            )}
          </article>
        ))}
      </div>
      {!items.length && (
        <Empty
          title="등록된 담당자가 없습니다"
          description="첫 담당자를 등록하세요."
        />
      )}
      {create && (
        <NewPortalContact
          onClose={() => setCreate(false)}
          onSaved={() => {
            setCreate(false);
            load();
          }}
        />
      )}
    </div>
  );
}

function NewPortalContact({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    await post("/api/v1/portal/contacts", {
      name: d.get("name"),
      title: d.get("title"),
      department: d.get("department"),
      email: d.get("email"),
      phone: d.get("phone"),
      primary: d.get("primary") === "on",
    });
    onSaved();
  }
  return (
    <Modal
      title="담당자 추가"
      description="이메일은 별도 인증할 수 있습니다."
      onClose={onClose}
    >
      <form onSubmit={submit}>
        <div className="form-grid">
          <Field label="이름" required>
            <input name="name" required autoFocus />
          </Field>
          <Field label="직함">
            <input name="title" />
          </Field>
          <Field label="부서">
            <input name="department" />
          </Field>
          <Field label="이메일">
            <input name="email" type="email" />
          </Field>
          <Field label="전화번호">
            <input name="phone" />
          </Field>
        </div>
        <label className="toggle-row">
          <span>
            <b>대표 담당자</b>
            <small>업체의 기본 연락 담당자로 표시합니다.</small>
          </span>
          <input name="primary" type="checkbox" />
        </label>
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={onClose}>
            취소
          </button>
          <button className="button">담당자 저장</button>
        </div>
      </form>
    </Modal>
  );
}

function CompanyProfile({
  supplier: s,
  onSaved,
}: {
  supplier: Supplier;
  onSaved: () => void;
}) {
  const [edit, setEdit] = useState(false);
  return (
    <div className="portal-page">
      <div className="portal-page-head">
        <div>
          <p className="eyebrow">Company profile</p>
          <h1>회사정보</h1>
          <p>연락처와 인증 정보를 최신 상태로 유지하세요.</p>
        </div>
        <button className="button" onClick={() => setEdit(true)}>
          <Settings />
          수정
        </button>
      </div>
      <div className="card company-profile">
        <div className="profile-company-head">
          <span className="company-avatar hero-avatar">
            {s.name.slice(0, 2)}
          </span>
          <div>
            <h2>{s.name}</h2>
            <p>{s.legalName || s.supplierNumber}</p>
          </div>
          <Badge tone={statusTone(s.status)}>{s.status}</Badge>
        </div>
        <dl>
          <dt>사업자번호</dt>
          <dd>{s.businessNumber}</dd>
          <dt>대표자</dt>
          <dd>{s.representative || "—"}</dd>
          <dt>업종</dt>
          <dd>{s.industry || "—"}</dd>
          <dt>대표 이메일</dt>
          <dd>{s.email || "—"}</dd>
          <dt>대표 전화</dt>
          <dd>{s.phone || "—"}</dd>
          <dt>공급 품목</dt>
          <dd>{s.categories?.join(", ") || "—"}</dd>
        </dl>
        <div className="form-error warning">
          계좌정보, 사업자번호와 법적 정보 변경은 내부 승인 후 반영됩니다.
        </div>
      </div>
      {edit && (
        <Modal
          title="회사 연락정보 수정"
          description="법적 정보와 계좌 변경은 내부 승인 대상으로 별도 요청됩니다."
          onClose={() => setEdit(false)}
        >
          <form
            onSubmit={async (e) => {
              e.preventDefault();
              const d = new FormData(e.currentTarget);
              await patch("/api/v1/portal/profile", {
                email: d.get("email"),
                phone: d.get("phone"),
                website: d.get("website"),
              });
              setEdit(false);
              onSaved();
            }}
          >
            <Field label="대표 이메일">
              <input name="email" type="email" defaultValue={s.email} />
            </Field>
            <Field label="대표 전화">
              <input name="phone" defaultValue={s.phone} />
            </Field>
            <Field label="웹사이트">
              <input name="website" type="url" defaultValue={s.website} />
            </Field>
            <div className="form-actions">
              <button
                type="button"
                className="button secondary"
                onClick={() => setEdit(false)}
              >
                취소
              </button>
              <button className="button">
                <Settings /> 저장
              </button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
