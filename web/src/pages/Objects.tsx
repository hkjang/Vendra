import {
  FormEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  AlertCircle,
  BarChart3,
  BookmarkPlus,
  Bot,
  CalendarClock,
  Check,
  ClipboardCheck,
  Columns3,
  FileSearch,
  Filter,
  Plus,
  Search,
  Send,
  Share2,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import { Link, useSearchParams } from "react-router-dom";
import { api, date, del, isoDate, money, post, put, todayISO } from "../api";
import {
  Badge,
  Empty,
  Field,
  Loading,
  Modal,
  PageHeader,
  RiskBadge,
} from "../components";
import { statusTone } from "../status";
import { BusinessObject, Supplier } from "../types";

const defaultObjectColumns = ["supplier", "status", "amount", "risk", "start", "due"];
const objectColumnOptions = [
  { key: "supplier", label: "공급업체" },
  { key: "status", label: "상태" },
  { key: "amount", label: "금액" },
  { key: "risk", label: "Risk" },
  { key: "start", label: "시작일" },
  { key: "due", label: "기한 · 종료" },
];

const configs: Record<
  string,
  {
    title: string;
    eyebrow: string;
    description: string;
    endpoint: string;
    create: string;
    icon: typeof Search;
  }
> = {
  contract: {
    title: "계약",
    eyebrow: "Contract management",
    description: "계약 조건, 금액, 기간, SLA와 갱신을 통합 관리합니다.",
    endpoint: "/api/v1/contracts",
    create: "새 계약",
    icon: CalendarClock,
  },
  purchase_request: {
    title: "구매요청",
    eyebrow: "Procurement intake",
    description:
      "현업의 구매 수요를 접수하고 검토·견적·선정 과정을 연결합니다.",
    endpoint: "/api/v1/purchase-requests",
    create: "구매 요청",
    icon: ClipboardCheck,
  },
  rfq: {
    title: "RFQ",
    eyebrow: "Request for quotation",
    description:
      "복수 공급업체 견적을 수집하고 가격·품질·납기·위험으로 비교합니다.",
    endpoint: "/api/v1/rfq",
    create: "RFQ 생성",
    icon: FileSearch,
  },
  rfp: {
    title: "RFP · 입찰",
    eyebrow: "Strategic sourcing",
    description:
      "복합 요구사항, 질의응답, 기술·가격 평가와 우선협상을 관리합니다.",
    endpoint: "/api/v1/rfp",
    create: "RFP 생성",
    icon: FileSearch,
  },
  purchase_order: {
    title: "발주",
    eyebrow: "Purchase order",
    description: "계약 기반 발주부터 공급업체 확인, 납품, 지급까지 추적합니다.",
    endpoint: "/api/v1/purchase-orders",
    create: "PO 생성",
    icon: ClipboardCheck,
  },
  delivery: {
    title: "납품",
    eyebrow: "Delivery management",
    description: "납품 예정, 실적, 반품과 재납품을 투명하게 관리합니다.",
    endpoint: "/api/v1/deliveries",
    create: "납품 등록",
    icon: ClipboardCheck,
  },
  inspection: {
    title: "검수",
    eyebrow: "Inspection",
    description: "납품 수량과 품질을 검수하고 반품 또는 재납품을 연결합니다.",
    endpoint: "/api/v1/inspections",
    create: "검수 등록",
    icon: ClipboardCheck,
  },
  quality: {
    title: "품질 · CAPA",
    eyebrow: "Supplier quality",
    description:
      "불량, NCR, 원인분석, 시정·예방조치를 공급업체 성과에 반영합니다.",
    endpoint: "/api/v1/quality",
    create: "품질 이슈 등록",
    icon: ShieldAlert,
  },
  issue: {
    title: "공급업체 이슈",
    eyebrow: "Issue management",
    description:
      "납기, 품질, 보안, 계약, 재무 이슈의 원인과 조치계획을 추적합니다.",
    endpoint: "/api/v1/issues",
    create: "이슈 등록",
    icon: AlertCircle,
  },
  invoice: {
    title: "Invoice",
    eyebrow: "Invoice management",
    description: "공급업체 청구서와 검수·계약·발주 연결 상태를 관리합니다.",
    endpoint: "/api/v1/invoices",
    create: "Invoice 등록",
    icon: ClipboardCheck,
  },
  payment: {
    title: "지급",
    eyebrow: "Payment management",
    description:
      "승인된 Invoice의 지급 예정, 완료와 회계 연계 상태를 추적합니다.",
    endpoint: "/api/v1/payments",
    create: "지급 등록",
    icon: ClipboardCheck,
  },
};

export default function Objects({ type }: { type: string }) {
  if (type === "search") return <SearchPage />;
  if (type === "spend") return <SpendPage />;
  if (type === "risk") return <RiskIntelligence />;
  if (type === "evaluation") return <SupplierIntelligence />;
  return <ObjectList type={type} />;
}

function ObjectList({ type }: { type: string }) {
  const c = configs[type];
  const [params, setParams] = useSearchParams();
  const q = params.get("q") || "";
  const status = params.get("status") || "";
  const order = params.get("order") || "updated_desc";
  const requestKey = `${c.endpoint}\u0000${q}\u0000${status}\u0000${order}`;
  const [result, setResult] = useState<{
    key: string;
    items: BusinessObject[];
    truncated: boolean;
  }>({ key: "", items: [], truncated: false });
  const [modal, setModal] = useState(false);
  const [suppliers, setSuppliers] = useState<Supplier[]>([]);
  const [views, setViews] = useState<SavedView[]>([]);
  const [activeView, setActiveView] = useState("");
  const [viewModal, setViewModal] = useState(false);
  const [columnModal, setColumnModal] = useState(false);
  const [visibleColumns, setVisibleColumns] = useState(defaultObjectColumns);
  const [viewName, setViewName] = useState("");
  const [shareView, setShareView] = useState(false);
  const [canShareViews, setCanShareViews] = useState(false);
  const [viewError, setViewError] = useState("");
  const loadSequence = useRef(0);
  const viewContext = `object:${type}`;
  const load = useCallback(() => {
    const sequence = ++loadSequence.current;
    return api<{ items: BusinessObject[]; truncated?: boolean }>(
      `${c.endpoint}?q=${encodeURIComponent(q)}&status=${status}&order=${order}`,
    ).then((response) => {
      if (sequence === loadSequence.current)
        setResult({
          key: requestKey,
          items: response.items,
          truncated: Boolean(response.truncated),
        });
    });
  }, [c.endpoint, order, q, requestKey, status]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    api<{ items: Supplier[] }>("/api/v1/suppliers?limit=500")
      .then((x) => setSuppliers(x.items))
      .catch(() => {});
  }, []);
  const loadViews = useCallback(() => {
    return api<{ items: SavedView[]; canShare: boolean }>(
      `/api/v1/me/saved-views?context=${encodeURIComponent(viewContext)}`,
    )
      .then((response) => {
        setViews(response.items);
        setCanShareViews(response.canShare);
      })
      .catch(() => setViews([]));
  }, [viewContext]);
  useEffect(() => {
    void loadViews();
  }, [loadViews]);
  function set(key: string, v: string) {
    const n = new URLSearchParams(params);
    if (v) n.set(key, v);
    else n.delete(key);
    setActiveView("");
    setParams(n);
  }
  function applyView(id: string) {
    setActiveView(id);
    const selected = views.find((view) => view.id === id);
    if (!selected) {
      setParams({});
      setVisibleColumns(defaultObjectColumns);
      return;
    }
    const next = new URLSearchParams();
    for (const [key, value] of Object.entries(selected.filters)) {
      if (typeof value === "string" && value) next.set(key, value);
    }
    setVisibleColumns(selected.columns?.length ? selected.columns : defaultObjectColumns);
    setParams(next);
  }
  async function saveView(event: FormEvent) {
    event.preventDefault();
    setViewError("");
    try {
      await post("/api/v1/me/saved-views", {
        name: viewName,
        context: viewContext,
        filters: { q, status, order },
        columns: visibleColumns,
        shared: shareView,
      });
      setViewModal(false);
      setViewName("");
      setShareView(false);
      await loadViews();
    } catch (cause) {
      setViewError(cause instanceof Error ? cause.message : "보기를 저장하지 못했습니다");
    }
  }
  async function removeView() {
    const selected = views.find((view) => view.id === activeView);
    if (!selected?.owned) return;
    try {
      await del(`/api/v1/me/saved-views/${selected.id}`);
      setActiveView("");
      await loadViews();
    } catch (cause) {
      setViewError(cause instanceof Error ? cause.message : "보기를 삭제하지 못했습니다");
    }
  }
  const loading = result.key !== requestKey;
  const items = loading ? [] : result.items;
  const truncated = !loading && result.truncated;
  return (
    <div className="page">
      <PageHeader
        eyebrow={c.eyebrow}
        title={c.title}
        description={c.description}
        actions={
          <button className="button" onClick={() => setModal(true)}>
            <Plus />
            {c.create}
          </button>
        }
      />
      {truncated && (
        <p className="form-error warning list-truncated" role="status">
          <AlertCircle />
          결과가 많아 일부만 표시했습니다. 검색이나 필터로 범위를 좁히세요.
        </p>
      )}
      <div className="toolbar object-toolbar">
        <div className="search-box">
          <Search />
          <input
            value={q}
            onChange={(e) => set("q", e.target.value)}
            placeholder="번호 또는 제목 검색"
            aria-label="번호 또는 제목 검색"
          />
        </div>
        <select
          value={status}
          onChange={(e) => set("status", e.target.value)}
          aria-label="상태 필터"
        >
          <option value="">모든 상태</option>
          <option value="draft">초안</option>
          <option value="pending_approval">승인 대기</option>
          <option value="approved">승인</option>
          <option value="active">진행 중</option>
          <option value="completed">완료</option>
          <option value="rejected">반려</option>
        </select>
        <select value={order} onChange={(event) => set("order", event.target.value)} aria-label="정렬 기준">
          <option value="updated_desc">최근 수정순</option>
          <option value="due_asc">기한 임박순</option>
          <option value="amount_desc">금액 높은순</option>
          <option value="title_asc">제목 가나다순</option>
        </select>
        <div className="saved-view-tools">
          <select
            value={activeView}
            onChange={(event) => applyView(event.target.value)}
            aria-label="저장된 보기"
          >
            <option value="">기본 보기</option>
            {views.map((view) => (
              <option value={view.id} key={view.id}>
                {view.shared ? "공유 · " : ""}{view.name}
              </option>
            ))}
          </select>
          <button className="button ghost" onClick={() => setViewModal(true)}>
            <BookmarkPlus />보기 저장
          </button>
          <button className="icon-button" onClick={() => setColumnModal(true)} title="표시 열 설정" aria-label="표시 열 설정">
            <Columns3 />
          </button>
          {views.find((view) => view.id === activeView)?.owned && (
            <button className="icon-button" onClick={() => void removeView()} title="현재 보기 삭제" aria-label="현재 보기 삭제">
              <Trash2 />
            </button>
          )}
        </div>
      </div>
      {viewError && !viewModal && <div className="form-error"><AlertCircle />{viewError}</div>}
      {loading ? (
        <Loading />
      ) : (
        <ObjectTable
          items={items}
          empty={`${c.title} 데이터가 없습니다`}
          onSubmit={load}
          visibleColumns={visibleColumns}
        />
      )}{" "}
      {modal && (
        <NewObject
          config={c}
          type={type}
          suppliers={suppliers}
          onClose={() => setModal(false)}
          onSaved={() => {
            setModal(false);
            load();
          }}
        />
      )}
      {viewModal && (
        <Modal
          title="현재 보기를 저장"
          description="검색어와 상태 필터를 저장해 다시 사용하거나 같은 조직에 공유할 수 있습니다."
          onClose={() => setViewModal(false)}
        >
          <form onSubmit={saveView}>
            <Field label="보기 이름" required>
              <input autoFocus required maxLength={60} value={viewName} onChange={(event) => setViewName(event.target.value)} placeholder="예: 이번 달 만료 계약" />
            </Field>
            <div className="saved-view-preview">
              <Filter />
              <span><b>현재 조건</b><small>검색어 {q || "없음"} · 상태 {status || "전체"} · 표시 열 {visibleColumns.length + 1}개</small></span>
            </div>
            <label className="checkbox-line saved-view-share">
              <input type="checkbox" disabled={!canShareViews} checked={shareView} onChange={(event) => setShareView(event.target.checked)} />
              <Share2 />{canShareViews ? "같은 조직 사용자에게 공유" : "조직 소속 사용자만 공유할 수 있습니다"}
            </label>
            {viewError && <div className="form-error"><AlertCircle />{viewError}</div>}
            <div className="form-actions">
              <button type="button" className="button secondary" onClick={() => setViewModal(false)}>취소</button>
              <button className="button"><BookmarkPlus />보기 저장</button>
            </div>
          </form>
        </Modal>
      )}
      {columnModal && (
        <Modal title="표시 열 설정" description="업무에 필요한 열만 남기고 저장된 보기에 함께 보관할 수 있습니다." onClose={() => setColumnModal(false)}>
          <div className="column-picker">
            <label><input type="checkbox" checked disabled />번호 · 제목 <small>필수</small></label>
            {objectColumnOptions.map((column) => (
              <label key={column.key}>
                <input
                  type="checkbox"
                  checked={visibleColumns.includes(column.key)}
                  onChange={(event) => {
                    setActiveView("");
                    setVisibleColumns((current) => event.target.checked ? [...current, column.key] : current.filter((key) => key !== column.key));
                  }}
                />
                {column.label}
              </label>
            ))}
          </div>
          <div className="form-actions">
            <button className="button secondary" onClick={() => setVisibleColumns(defaultObjectColumns)}>기본값 복원</button>
            <button className="button" onClick={() => setColumnModal(false)}><Check />적용</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

type SavedView = {
  id: string;
  name: string;
  context: string;
  filters: Record<string, unknown>;
  columns: string[];
  shared: boolean;
  owned: boolean;
};

function ObjectTable({
  items,
  empty,
  onSubmit,
  visibleColumns,
}: {
  items: BusinessObject[];
  empty: string;
  onSubmit: () => void;
  visibleColumns: string[];
}) {
  const [analysis, setAnalysis] = useState<{
    title: string;
    content: unknown;
  }>();
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkMessage, setBulkMessage] = useState("");
  async function submit(id: string, type: string) {
    await post(`${configs[type].endpoint}/${id}/submit`, {});
    onSubmit();
  }
  async function analyzeContract(object: BusinessObject) {
    try {
      const result = await post(
        `/api/v1/ai/contracts/${object.id}/analyze`,
        {},
      );
      setAnalysis({ title: `${object.number} AI 계약 분석`, content: result });
    } catch (error) {
      setAnalysis({
        title: `${object.number} AI 계약 분석`,
        content: {
          error: error instanceof Error ? error.message : "분석하지 못했습니다",
        },
      });
    }
  }
  async function submitSelected() {
    const targets = items.filter((item) => selected.has(item.id) && item.status === "draft");
    if (!targets.length) return;
    setBulkBusy(true);
    setBulkMessage("");
    const results = await Promise.allSettled(
      targets.map((item) => post(`${configs[item.objectType].endpoint}/${item.id}/submit`, {})),
    );
    const succeeded = results.filter((result) => result.status === "fulfilled").length;
    const failed = results.length - succeeded;
    setSelected(new Set());
    setBulkBusy(false);
    setBulkMessage(failed ? `${succeeded}건 처리, ${failed}건 실패했습니다.` : `${succeeded}건을 승인 요청했습니다.`);
    onSubmit();
  }
  const selectable = items.filter((item) => item.status === "draft");
  function toggleSelection(id: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }
  return (
    <div className="data-card">
      {(selected.size > 0 || bulkMessage) && (
        <div className="table-bulk-actions">
          <span><Check />{selected.size ? `${selected.size}건 선택` : bulkMessage}</span>
          {selected.size > 0 && <>
            <button className="button ghost" onClick={() => setSelected(new Set())}>선택 해제</button>
            <button className="button" disabled={bulkBusy} onClick={() => void submitSelected()}><Send />{bulkBusy ? "처리 중…" : "일괄 승인 요청"}</button>
          </>}
        </div>
      )}
      {items.length ? (
        <table>
          <thead>
            <tr>
              <th className="selection-cell">
                <input
                  type="checkbox"
                  aria-label="모든 초안 선택"
                  checked={selectable.length > 0 && selectable.every((item) => selected.has(item.id))}
                  onChange={(event) => setSelected(event.target.checked ? new Set(selectable.map((item) => item.id)) : new Set())}
                />
              </th>
              <th>번호 · 제목</th>
              {visibleColumns.includes("supplier") && <th>공급업체</th>}
              {visibleColumns.includes("status") && <th>상태</th>}
              {visibleColumns.includes("amount") && <th>금액</th>}
              {visibleColumns.includes("risk") && <th>Risk</th>}
              {visibleColumns.includes("start") && <th>시작일</th>}
              {visibleColumns.includes("due") && <th>기한 · 종료</th>}
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((o) => (
              <tr key={o.id}>
                <td className="selection-cell">
                  <input type="checkbox" aria-label={`${o.title} 선택`} disabled={o.status !== "draft"} checked={selected.has(o.id)} onChange={() => toggleSelection(o.id)} />
                </td>
                <td>
                  <span className="stack">
                    {o.objectType === "rfq" || o.objectType === "rfp" ? (
                      <Link
                        className="object-title-link"
                        to={`/sourcing/${o.objectType}/${o.id}`}
                      >
                        {o.title}
                      </Link>
                    ) : (
                      <b>{o.title}</b>
                    )}
                    <small>{o.number}</small>
                  </span>
                </td>
                {visibleColumns.includes("supplier") && <td>{o.supplierName || "—"}</td>}
                {visibleColumns.includes("status") && <td>
                  <Badge tone={statusTone(o.status)}>{o.status}</Badge>
                </td>}
                {visibleColumns.includes("amount") && <td className="number">{money(o.amount)}</td>}
                {visibleColumns.includes("risk") && <td>
                  {o.riskLevel ? (
                    <RiskBadge level={o.riskLevel} />
                  ) : (
                    <span>—</span>
                  )}
                </td>}
                {visibleColumns.includes("start") && <td>{date(o.startDate)}</td>}
                {visibleColumns.includes("due") && <td>{date(o.endDate || o.dueDate)}</td>}
                <td>
                  <div className="row-actions">
                    {o.objectType === "contract" && (
                      <button
                        title="AI 계약 조건 · Risk 분석"
                        className="icon-button"
                        onClick={() => analyzeContract(o)}
                      >
                        <Bot />
                      </button>
                    )}
                    {o.status === "draft" && (
                      <button
                        title="승인 요청"
                        className="icon-button"
                        onClick={() => submit(o.id, o.objectType)}
                      >
                        <Send />
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : (
        <Empty
          title={empty}
          description="새 항목을 등록하면 이 목록에서 상태와 업무 흐름을 추적할 수 있습니다."
        />
      )}
      {analysis && (
        <Modal
          title={analysis.title}
          description="계약금액·기간·자동갱신·해지·SLA·위약금·손해배상·보증·개인정보·보안·재위탁과 위험조항을 구조화했습니다."
          onClose={() => setAnalysis(undefined)}
          wide
        >
          <pre className="analysis-json">
            {JSON.stringify(analysis.content, null, 2)}
          </pre>
        </Modal>
      )}
    </div>
  );
}

function NewObject({
  config,
  type,
  suppliers,
  onClose,
  onSaved,
}: {
  config: (typeof configs)[string];
  type: string;
  suppliers: Supplier[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [draftState, setDraftState] = useState<"loading" | "restored" | "saving" | "saved" | "error">("loading");
  const formRef = useRef<HTMLFormElement>(null);
  const saveTimer = useRef<number | undefined>(undefined);
  const draftRequest = useRef<Promise<void> | null>(null);
  const submitted = useRef(false);
  const draftDirty = useRef(false);
  const draftKey = `new-object:${type}`;
  const saveDraft = useCallback(async () => {
    if (!formRef.current || submitted.current) return;
    const payload = formPayload(formRef.current);
    if (!Object.values(payload).some((value) => value !== "" && value !== false)) return;
    setDraftState("saving");
    const request = put(`/api/v1/me/drafts/${draftKey}`, { payload })
      .then(() => setDraftState("saved"))
      .catch(() => setDraftState("error"))
      .then(() => undefined);
    draftRequest.current = request;
    await request;
    if (draftRequest.current === request) draftRequest.current = null;
  }, [draftKey]);
  useEffect(() => {
    let active = true;
    api<{ draft: { payload: Record<string, unknown> } | null }>(`/api/v1/me/drafts/${draftKey}`)
      .then((result) => {
        if (!active || submitted.current || draftDirty.current) return;
        if (!result.draft || !formRef.current) {
          setDraftState("saved");
          return;
        }
        restoreForm(formRef.current, result.draft.payload);
        setDraftState("restored");
      })
      .catch(() => active && setDraftState("error"));
    return () => {
      active = false;
      if (saveTimer.current) window.clearTimeout(saveTimer.current);
    };
  }, [draftKey]);
  function queueDraft() {
    draftDirty.current = true;
    if (saveTimer.current) window.clearTimeout(saveTimer.current);
    setDraftState("saving");
    saveTimer.current = window.setTimeout(() => void saveDraft(), 700);
  }
  async function save(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    if (saveTimer.current) window.clearTimeout(saveTimer.current);
    submitted.current = true;
    if (draftRequest.current) await draftRequest.current;
    setBusy(true);
    setError("");
    const d = new FormData(e.currentTarget);
    try {
      await post(config.endpoint, {
        title: d.get("title"),
        supplierId: d.get("supplierId"),
        parentId: d.get("parentId"),
        amount: Number(d.get("amount")) || undefined,
        currency: "KRW",
        startDate: d.get("startDate"),
        dueDate: d.get("dueDate"),
        endDate: d.get("endDate"),
        riskLevel: d.get("riskLevel"),
        status: "draft",
        data: {
          description: d.get("description"),
          category: d.get("category"),
          purchasePurpose: d.get("purchasePurpose"),
          item: d.get("item"),
          budget: Number(d.get("budget")) || undefined,
          desiredDate: d.get("desiredDate"),
          quantity: Number(d.get("quantity")) || undefined,
          unit: d.get("unit"),
          unitPrice: Number(d.get("unitPrice")) || undefined,
          deliveryLocation: d.get("deliveryLocation"),
          paymentTerms: d.get("paymentTerms"),
          contractType: d.get("contractType"),
          autoRenewal: d.get("autoRenewal") === "on",
          sla: d.get("sla"),
          warranty: d.get("warranty"),
          issueType: d.get("issueType"),
          severity: d.get("severity"),
          occurredDate: d.get("occurredDate"),
          actionPlan: d.get("actionPlan"),
          rootCause: d.get("rootCause"),
          capa: d.get("capa"),
        },
      });
      await del(`/api/v1/me/drafts/${draftKey}`).catch(() => undefined);
      onSaved();
    } catch (e) {
      submitted.current = false;
      setError(e instanceof Error ? e.message : "저장하지 못했습니다");
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title={config.create}
      description={`${config.title} 기본정보를 입력하세요. 저장 후 세부 조건과 문서를 추가할 수 있습니다.`}
      onClose={() => {
        void saveDraft();
        onClose();
      }}
      wide
    >
      <form ref={formRef} onSubmit={save} onInput={queueDraft} onChange={queueDraft}>
        <div className={`draft-status ${draftState}`} aria-live="polite">
          {draftState === "loading" && "임시저장 확인 중…"}
          {draftState === "restored" && "이전에 작성하던 내용을 복원했습니다."}
          {draftState === "saving" && "입력 내용 저장 중…"}
          {draftState === "saved" && "입력 내용이 자동 저장됩니다."}
          {draftState === "error" && "자동 저장을 확인할 수 없습니다. 직접 저장해 주세요."}
        </div>
        <div className="form-grid">
          <Field label="제목" required>
            <input
              name="title"
              required
              autoFocus
              placeholder={`${config.title} 제목`}
            />
          </Field>
          <Field label="공급업체">
            <select name="supplierId">
              <option value="">미지정</option>
              {suppliers.map((s) => (
                <option value={s.id} key={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="금액">
            <input name="amount" type="number" min="0" placeholder="0" />
          </Field>
          {(type === "delivery" ||
            type === "inspection" ||
            type === "invoice" ||
            type === "payment") && (
            <Field label="상위 업무 ID">
              <input
                name="parentId"
                placeholder="PO / Delivery / Invoice UUID"
              />
            </Field>
          )}
          <Field label="리스크">
            <select name="riskLevel">
              <option value="">미평가</option>
              <option>LOW</option>
              <option>MEDIUM</option>
              <option>HIGH</option>
              <option>CRITICAL</option>
            </select>
          </Field>
          <Field label="시작일">
            <input name="startDate" type="date" />
          </Field>
          <Field label="기한">
            <input name="dueDate" type="date" />
          </Field>
          <Field label="종료일">
            <input name="endDate" type="date" />
          </Field>
          <Field label="품목 · 카테고리">
            <input name="category" />
          </Field>
          {type === "purchase_request" && (
            <>
              <Field label="구매 목적" required>
                <input name="purchasePurpose" required />
              </Field>
              <Field label="구매 품목" required>
                <input name="item" required />
              </Field>
              <Field label="예산">
                <input name="budget" type="number" min="0" />
              </Field>
              <Field label="구매 희망일">
                <input name="desiredDate" type="date" />
              </Field>
            </>
          )}
          {(type === "purchase_order" ||
            type === "delivery" ||
            type === "inspection") && (
            <>
              <Field label="품목">
                <input name="item" />
              </Field>
              <Field label="단위">
                <input name="unit" placeholder="EA" />
              </Field>
              <Field label="단가">
                <input name="unitPrice" type="number" min="0" />
              </Field>
            </>
          )}
          <Field label="수량">
            <input name="quantity" type="number" min="0" />
          </Field>
          <Field label="납품 장소">
            <input name="deliveryLocation" />
          </Field>
          <Field label="지급 조건">
            <input name="paymentTerms" placeholder="검수 후 30일" />
          </Field>
          {type === "contract" && (
            <>
              <Field label="계약 유형">
                <select name="contractType">
                  <option value="goods">물품</option>
                  <option value="service">서비스</option>
                  <option value="outsourcing">용역</option>
                </select>
              </Field>
              <Field label="SLA">
                <input name="sla" placeholder="99.9%, P1 4시간" />
              </Field>
              <Field label="Warranty">
                <input name="warranty" placeholder="1년" />
              </Field>
              <Field label="자동 갱신">
                <label className="checkbox-line">
                  <input name="autoRenewal" type="checkbox" />
                  사용
                </label>
              </Field>
            </>
          )}
          {(type === "issue" || type === "quality") && (
            <>
              <Field label="이슈 유형">
                <select name="issueType">
                  <option>납기 지연</option>
                  <option>품질 문제</option>
                  <option>계약 위반</option>
                  <option>보안 사고</option>
                  <option>개인정보 사고</option>
                  <option>서비스 장애</option>
                  <option>SLA 위반</option>
                  <option>재무 악화</option>
                  <option>법적 문제</option>
                </select>
              </Field>
              <Field label="심각도">
                <select name="severity">
                  <option>LOW</option>
                  <option>MEDIUM</option>
                  <option>HIGH</option>
                  <option>CRITICAL</option>
                </select>
              </Field>
              <Field label="발생일">
                <input name="occurredDate" type="date" />
              </Field>
              <Field label="조치계획">
                <input name="actionPlan" />
              </Field>
              <Field label="원인분석 (RCA)">
                <textarea name="rootCause" rows={2} />
              </Field>
              <Field label="CAPA">
                <textarea name="capa" rows={2} />
              </Field>
            </>
          )}
          <Field label="설명">
            <textarea name="description" rows={3} />
          </Field>
        </div>
        {error && (
          <div className="form-error">
            <AlertCircle />
            {error}
          </div>
        )}
        <div className="form-actions">
          <button type="button" className="button secondary" onClick={() => { void saveDraft(); onClose(); }}>
            취소
          </button>
          <button className="button" disabled={busy}>
            {busy ? "저장 중…" : "초안 저장"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

function formPayload(form: HTMLFormElement) {
  const payload: Record<string, string | boolean> = {};
  for (const element of Array.from(form.elements)) {
    if (!(element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) || !element.name) continue;
    payload[element.name] = element instanceof HTMLInputElement && element.type === "checkbox" ? element.checked : element.value;
  }
  return payload;
}

function restoreForm(form: HTMLFormElement, payload: Record<string, unknown>) {
  for (const [name, value] of Object.entries(payload)) {
    const element = form.elements.namedItem(name);
    if (element instanceof HTMLInputElement && element.type === "checkbox") element.checked = value === true;
    else if (element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) element.value = typeof value === "string" ? value : "";
  }
}

function SearchPage() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") || "";
  const [result, setResult] = useState<{
    query: string;
    items: Record<string, string>[];
  }>({ query: "", items: [] });
  useEffect(() => {
    if (q.length < 2) return;
    let active = true;
    api<{ items: Record<string, string>[] }>(
      `/api/v1/search?q=${encodeURIComponent(q)}`,
    )
      .then((response) => {
        if (active) setResult({ query: q, items: response.items });
      });
    return () => {
      active = false;
    };
  }, [q]);
  const loading = q.length >= 2 && result.query !== q;
  const items = result.query === q ? result.items : [];
  return (
    <div className="page">
      <PageHeader
        eyebrow="Global search"
        title="통합 검색"
        description="사용자 권한 범위 안에서 공급업체, 계약, PO, RFQ, RFP, 문서, 이슈와 평가를 검색합니다."
      />
      <div className="hero-search">
        <Search />
        <input
          autoFocus
          value={q}
          onChange={(e) =>
            setParams(e.target.value ? { q: e.target.value } : {})
          }
          placeholder="두 글자 이상 입력하세요"
        />
      </div>
      {loading ? (
        <Loading />
      ) : items.length ? (
        <div className="search-results">
          {items.map((x, i) => (
            <div key={x.id || i}>
              <span className="object-icon">
                <FileSearch />
              </span>
              <div>
                <b>{x.title}</b>
                <p>
                  {x.number} · {x.type}
                </p>
              </div>
              <Badge tone={statusTone(x.status)}>{x.status}</Badge>
            </div>
          ))}
        </div>
      ) : (
        <Empty
          icon={<Search />}
          title={q ? "검색 결과가 없습니다" : "무엇을 찾고 계세요?"}
          description={
            q
              ? "다른 검색어 또는 더 넓은 조건을 사용하세요."
              : "공급업체명, 계약번호, 발주 제목 등을 검색하세요."
          }
        />
      )}
    </div>
  );
}

type RiskRecord = {
  id: string;
  supplierId: string;
  supplierName: string;
  riskType: string;
  probability: number;
  impact: number;
  score: number;
  severity: string;
  status: string;
  description?: string;
  mitigation?: string;
  reviewDate?: string;
};

function RiskIntelligence() {
  const [items, setItems] = useState<RiskRecord[]>();
  useEffect(() => {
    api<{ items: RiskRecord[] }>("/api/v1/risks?limit=500").then((x) =>
      setItems(x.items),
    );
  }, []);
  if (!items) return <Loading />;
  const open = items.filter((item) => item.status !== "closed");
  return (
    <div className="page">
      <PageHeader
        eyebrow="Supplier risk management"
        title="공급업체 리스크"
        description="실제 등록된 재무·운영·보안·준법·품질·공급망 위험을 발생가능성과 영향도로 모니터링합니다."
      />
      <div className="risk-summary-grid">
        <div>
          <span>전체 Risk</span>
          <strong>{items.length}</strong>
        </div>
        <div>
          <span>Open</span>
          <strong>{open.length}</strong>
        </div>
        <div>
          <span>High · Critical</span>
          <strong>
            {
              open.filter((x) => ["HIGH", "CRITICAL"].includes(x.severity))
                .length
            }
          </strong>
        </div>
        <div>
          <span>검토일 경과</span>
          <strong>
            {
              // Both sides are calendar dates, so compare them as such rather
              // than routing one through a UTC instant.
              open.filter((x) => x.reviewDate && x.reviewDate < todayISO())
                .length
            }
          </strong>
        </div>
      </div>
      {items.length ? (
        <div className="risk-layout">
          <section className="card risk-matrix-card">
            <header className="card-title">
              <div>
                <b>Risk 사분면</b>
                <small>영향도 × 발생가능성 (0~10)</small>
              </div>
            </header>
            <div className="risk-matrix" aria-label="Risk 사분면">
              <span className="risk-quadrant monitor">Monitor</span>
              <span className="risk-quadrant critical">Critical</span>
              <span className="risk-quadrant low">Low</span>
              <span className="risk-quadrant mitigate">Mitigate</span>
              {open.map((item) => (
                <Link
                  className={`risk-dot ${item.severity.toLowerCase()}`}
                  key={item.id}
                  style={{
                    left: `${Math.min(95, Math.max(5, item.impact * 9 + 5))}%`,
                    bottom: `${Math.min(95, Math.max(5, item.probability * 9 + 5))}%`,
                  }}
                  title={`${item.supplierName} · ${item.riskType} · ${item.score}`}
                  aria-label={`${item.supplierName} ${item.riskType}`}
                  to={`/suppliers/${item.supplierId}?tab=risks`}
                />
              ))}
              <b className="risk-axis probability">발생가능성</b>
              <b className="risk-axis impact">영향도</b>
            </div>
          </section>
          <section className="card risk-register-card">
            <header className="card-title">
              <div>
                <b>Risk Register</b>
                <small>위험도 순 정렬</small>
              </div>
            </header>
            <div className="risk-register">
              {[...items]
                .sort((a, b) => b.score - a.score)
                .map((item) => (
                  <Link
                    to={`/suppliers/${item.supplierId}?tab=risks`}
                    key={item.id}
                  >
                    <div>
                      <b>{item.supplierName}</b>
                      <small>
                        {item.riskType} · {item.description || "설명 없음"}
                      </small>
                    </div>
                    <strong>{item.score.toFixed(1)}</strong>
                    <RiskBadge level={item.severity} />
                  </Link>
                ))}
            </div>
          </section>
        </div>
      ) : (
        <Empty
          icon={<ShieldAlert />}
          title="등록된 Risk가 없습니다"
          description="Supplier 360의 Risk 탭에서 위험을 등록하면 사분면과 Risk Register에 즉시 반영됩니다."
        />
      )}
    </div>
  );
}

function SupplierIntelligence() {
  const [items, setItems] = useState<Supplier[]>();
  useEffect(() => {
    api<{ items: Supplier[] }>("/api/v1/suppliers?limit=500").then((x) =>
      setItems(x.items),
    );
  }, []);
  const sorted = useMemo(
    () => [...(items || [])].sort((a, b) => (b.score || 0) - (a.score || 0)),
    [items],
  );
  if (!items) return <Loading />;
  return (
    <div className="page">
      <PageHeader
        eyebrow="Dynamic scorecard"
        title="공급업체 평가"
        description="관리자 정의 가중치에 따라 종합점수와 등급을 자동 산정합니다."
      />
      <div className="intelligence-grid">
        {sorted.map((s) => (
          <div className="card intel-card" key={s.id}>
            <header>
              <span className="company-avatar">{s.name.slice(0, 2)}</span>
              <div>
                <b>{s.name}</b>
                <small>{s.industry || s.supplierNumber}</small>
              </div>
              <Badge tone={statusTone(s.grade)}>{s.grade || "미평가"}</Badge>
            </header>
            <div className="eval-score">
              <strong>{s.score?.toFixed(1) || "—"}</strong>
              <span>/ 100</span>
            </div>
            <footer>
              <span>{money(s.annualSpend)}</span>
              <a href={`/suppliers/${s.id}`}>Supplier 360</a>
            </footer>
          </div>
        ))}
      </div>
    </div>
  );
}

type SpendRow = {
  key?: string;
  supplierId?: string;
  supplierName?: string;
  amount?: number;
  annualSpend?: number;
  sharePercent?: number;
  dependencyRisk?: string;
  transactionCount?: number;
  contractedAmount?: number;
  nonContractedAmount?: number;
};

function SpendPage() {
  const [items, setItems] = useState<SpendRow[]>();
  const [groupBy, setGroupBy] = useState("supplier");
  const [from, setFrom] = useState(() => {
    const d = new Date();
    d.setFullYear(d.getFullYear() - 1);
    return isoDate(d);
  });
  const [to, setTo] = useState(todayISO);
  useEffect(() => {
    const grouping = groupBy === "supplier" ? "" : groupBy;
    api<{ items: SpendRow[] }>(
      `/api/v1/spend?groupBy=${grouping}&from=${from}&to=${to}&limit=300`,
    ).then((x) => setItems(x.items));
  }, [groupBy, from, to]);
  if (!items) return <Loading />;
  const amount = (row: SpendRow) => row.annualSpend ?? row.amount ?? 0;
  const total = items.reduce((n, row) => n + amount(row), 0);
  const sorted = [...items].sort((a, b) => amount(b) - amount(a));
  const nonContracted = items.reduce(
    (n, row) => n + (row.nonContractedAmount || 0),
    0,
  );
  return (
    <div className="page">
      <PageHeader
        eyebrow="Spend intelligence"
        title="구매금액 분석"
        description="공급업체 집중도와 구매 의존도를 파악하고 비용 최적화 기회를 찾습니다."
        actions={
          <div className="spend-controls">
            <Filter />
            <input
              aria-label="시작일"
              type="date"
              value={from}
              onChange={(e) => {
                setItems(undefined);
                setFrom(e.target.value);
              }}
            />
            <span>–</span>
            <input
              aria-label="종료일"
              type="date"
              value={to}
              onChange={(e) => {
                setItems(undefined);
                setTo(e.target.value);
              }}
            />
          </div>
        }
      />
      <div className="spend-kpis">
        <div>
          <BarChart3 />
          <span>
            총 구매금액<strong>{money(total)}</strong>
          </span>
        </div>
        <div>
          <ClipboardCheck />
          <span>
            거래 건수
            <strong>
              {items.reduce((n, row) => n + (row.transactionCount || 0), 0)}
            </strong>
          </span>
        </div>
        <div>
          <ShieldAlert />
          <span>
            Top 1 의존도
            <strong>
              {total
                ? ((amount(sorted[0] || {}) / total) * 100).toFixed(1)
                : "0"}
              %
            </strong>
          </span>
        </div>
        <div>
          <AlertCircle />
          <span>
            비계약 구매<strong>{money(nonContracted)}</strong>
          </span>
        </div>
      </div>
      <div className="card spend-chart">
        <div className="card-head">
          <div>
            <h2>구매금액 Breakdown</h2>
            <div className="segmented">
              {[
                ["supplier", "공급업체"],
                ["category", "품목"],
                ["organization", "조직"],
                ["month", "월별 추이"],
              ].map(([value, label]) => (
                <button
                  key={value}
                  className={groupBy === value ? "active" : ""}
                  onClick={() => {
                    setItems(undefined);
                    setGroupBy(value);
                  }}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          <Badge
            tone={
              total && amount(sorted[0] || {}) / total > 0.4
                ? "danger"
                : "success"
            }
          >
            집중도{" "}
            {total && amount(sorted[0] || {}) / total > 0.4 ? "HIGH" : "NORMAL"}
          </Badge>
        </div>
        <div className="spend-bars">
          {sorted.slice(0, 18).map((row, index) => {
            const value = amount(row);
            const pct = total ? (value / total) * 100 : 0;
            return (
              <div key={row.supplierId || row.key || index}>
                <span>
                  <b>{row.supplierName || row.key || "미분류"}</b>
                  <small>
                    {money(value)} · {row.transactionCount || 0}건
                  </small>
                </span>
                <i>
                  <b style={{ width: `${Math.max(1, pct)}%` }} />
                </i>
                <em>{pct.toFixed(1)}%</em>
              </div>
            );
          })}
          {!sorted.length && (
            <Empty
              title="구매 원장 데이터가 없습니다"
              description="ERP 또는 REST API로 구매 거래를 적재하면 품목·조직·월별 분석이 표시됩니다."
            />
          )}
        </div>
      </div>
    </div>
  );
}
