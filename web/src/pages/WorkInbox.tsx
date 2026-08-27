import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  AlertTriangle,
  BellRing,
  CalendarClock,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleDot,
  Clock3,
  FileClock,
  ListChecks,
  Radar,
  RefreshCw,
  Search,
  ShieldAlert,
  TimerReset,
} from "lucide-react";
import { useNavigate } from "react-router-dom";
import { api, date, post } from "../api";
import { Badge, Empty, Loading, PageHeader } from "../components";

type WorkItem = {
  key: string;
  kind: string;
  category: string;
  title: string;
  description: string;
  urgency: "critical" | "high" | "medium" | "normal" | "low";
  timeBucket: "overdue" | "today" | "soon" | "later" | "undated";
  objectType?: string;
  objectId?: string;
  number?: string;
  supplierName?: string;
  dueDate?: string;
  createdAt: string;
  url: string;
  actionable: boolean;
};

type InboxResponse = {
  items: WorkItem[];
  summary: Record<string, number>;
  categories: Record<string, number>;
  generatedAt: string;
};

const categories = [
  { key: "all", label: "전체", icon: ListChecks },
  { key: "approval", label: "승인", icon: CheckCircle2 },
  { key: "task", label: "기한 업무", icon: CalendarClock },
  { key: "contract", label: "계약", icon: FileClock },
  { key: "risk", label: "리스크", icon: ShieldAlert },
  { key: "document", label: "문서", icon: FileClock },
  { key: "notification", label: "알림", icon: BellRing },
];

const lanes = [
  {
    key: "overdue",
    label: "기한 경과",
    description: "즉시 확인",
    tone: "critical",
  },
  { key: "today", label: "오늘", description: "오늘 안에 처리", tone: "high" },
  {
    key: "soon",
    label: "7일 이내",
    description: "곧 다가오는 업무",
    tone: "medium",
  },
  { key: "later", label: "예정", description: "미리 준비할 업무", tone: "low" },
  {
    key: "undated",
    label: "상시 확인",
    description: "기한 없는 알림",
    tone: "normal",
  },
] as const;

export default function WorkInbox() {
  const navigate = useNavigate();
  const [data, setData] = useState<InboxResponse>();
  const [category, setCategory] = useState("all");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const requestSequence = useRef(0);
  const load = useCallback(async () => {
    const sequence = ++requestSequence.current;
    setError("");
    try {
      const result = await api<InboxResponse>(
        `/api/v1/me/work-inbox?category=${encodeURIComponent(category)}&q=${encodeURIComponent(query.trim())}`,
      );
      if (sequence !== requestSequence.current) return;
      setData(result);
      setSelected((current) => {
        const visible = new Set(result.items.map((item) => item.key));
        return new Set([...current].filter((key) => visible.has(key)));
      });
    } catch (cause) {
      if (sequence !== requestSequence.current) return;
      setError(
        cause instanceof Error ? cause.message : "업무를 조회하지 못했습니다",
      );
    }
  }, [category, query]);

  useEffect(() => {
    const timer = window.setTimeout(() => void load(), 220);
    return () => window.clearTimeout(timer);
  }, [load]);

  const grouped = useMemo(() => {
    const result = new Map<string, WorkItem[]>();
    for (const lane of lanes) result.set(lane.key, []);
    for (const item of data?.items || [])
      result.get(item.timeBucket)?.push(item);
    return result;
  }, [data]);

  async function updateState(
    itemKeys: string[],
    state: "done" | "snoozed",
    days = 1,
  ) {
    if (!itemKeys.length) return;
    setBusy(true);
    setError("");
    try {
      const snoozedUntil = new Date();
      snoozedUntil.setDate(snoozedUntil.getDate() + days);
      snoozedUntil.setHours(9, 0, 0, 0);
      await post("/api/v1/me/work-items/state", {
        itemKeys,
        state,
        snoozedUntil: state === "snoozed" ? snoozedUntil.toISOString() : "",
      });
      setSelected(new Set());
      await load();
    } catch (cause) {
      setError(
        cause instanceof Error
          ? cause.message
          : "업무 상태를 저장하지 못했습니다",
      );
    } finally {
      setBusy(false);
    }
  }

  function toggle(key: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  // Without the second branch the page rendered with data undefined, so every
  // figure fell back to zero and the hero announced "지금 먼저 볼 업무가 0건" —
  // a confident all-clear on the one screen whose job is to say what is
  // outstanding, with the failure buried twelve zeroes below it and the empty
  // state claiming "모든 긴급 업무를 확인했습니다". Dashboard already returns an
  // Empty on failure; this only takes the page over when there is nothing to
  // show, so a failed refresh keeps the rows the reader is looking at and
  // reports itself in the banner below.
  if (!data) {
    if (!error) return <Loading />;
    return (
      <Empty
        title="업무를 불러오지 못했습니다"
        description={error}
        action={
          <button
            className="button secondary"
            onClick={() => void load()}
            disabled={busy}
          >
            <RefreshCw className={busy ? "spin" : ""} />
            다시 시도
          </button>
        }
      />
    );
  }
  const summary = data?.summary || {};
  const availableCount = Object.values(data?.categories || {}).reduce(
    (total, count) => total + count,
    0,
  );
  return (
    <div className="page work-tower-page">
      <PageHeader
        eyebrow="Work control tower"
        title="업무 관제탑"
        description="승인, 계약, 리스크, 문서와 기한 업무를 긴급도와 시간축으로 통합해 다음 행동을 안내합니다."
        actions={
          <button
            className="button secondary"
            onClick={() => void load()}
            disabled={busy}
          >
            <RefreshCw className={busy ? "spin" : ""} />
            새로고침
          </button>
        }
      />

      <section className="control-tower-hero" aria-label="업무 신호 요약">
        <div className="tower-copy">
          <span className="tower-live">
            <CircleDot /> Live signal
          </span>
          <h2>
            지금 먼저 볼 업무가
            <br />
            <em>{summary.critical || 0}건</em> 있습니다.
          </h2>
          <p>
            업무 신호를 기한 순으로 정렬했습니다. 빨간 신호부터 확인하면 중요한
            누락을 줄일 수 있습니다.
          </p>
        </div>
        <div className="tower-radar" aria-hidden="true">
          <i />
          <i />
          <i />
          <span>
            <Radar />
          </span>
          <b className="radar-ping ping-one" />
          <b className="radar-ping ping-two" />
          <b className="radar-sweep" />
        </div>
        <div className="tower-signals">
          <Signal
            icon={<AlertTriangle />}
            label="긴급"
            value={summary.critical || 0}
            tone="critical"
          />
          <Signal
            icon={<Clock3 />}
            label="기한 경과"
            value={summary.overdue || 0}
            tone="high"
          />
          <Signal
            icon={<CalendarClock />}
            label="오늘"
            value={summary.today || 0}
            tone="medium"
          />
          <Signal
            icon={<ListChecks />}
            label="전체"
            value={summary.total || 0}
            tone="normal"
          />
        </div>
      </section>

      <section className="tower-toolbar" aria-label="업무 필터">
        <div className="tower-category-scroll">
          {categories.map((item) => (
            <button
              key={item.key}
              className={category === item.key ? "active" : ""}
              onClick={() => setCategory(item.key)}
              aria-pressed={category === item.key}
            >
              <item.icon />
              {item.label}
              <b>
                {item.key === "all"
                  ? availableCount
                  : data?.categories[item.key] || 0}
              </b>
            </button>
          ))}
        </div>
        <label className="tower-search">
          <Search />
          <input
            aria-label="업무 관제탑 검색"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="제목, 번호, 공급업체 검색"
          />
        </label>
      </section>

      {selected.size > 0 && (
        <div
          className="tower-bulk"
          role="region"
          aria-label="선택 업무 일괄 처리"
        >
          <span>
            <CheckCircle2 />
            <b>{selected.size}건</b> 선택
          </span>
          <button
            className="button ghost"
            disabled={busy}
            onClick={() => setSelected(new Set())}
          >
            선택 해제
          </button>
          <button
            className="button secondary"
            disabled={busy}
            onClick={() => void updateState([...selected], "snoozed", 1)}
          >
            <TimerReset />
            내일까지 보류
          </button>
          <button
            className="button"
            disabled={busy}
            onClick={() => void updateState([...selected], "done")}
          >
            <Check />
            확인 완료
          </button>
        </div>
      )}

      {error && (
        <div className="form-error">
          <AlertTriangle />
          {error}
        </div>
      )}

      {data?.items.length ? (
        <div className="tower-lanes">
          {lanes.map((lane) => {
            const items = grouped.get(lane.key) || [];
            if (!items.length) return null;
            return (
              <section className={`tower-lane ${lane.tone}`} key={lane.key}>
                <header>
                  <span className="lane-signal" />
                  <div>
                    <h3>{lane.label}</h3>
                    <p>{lane.description}</p>
                  </div>
                  <b>{items.length}</b>
                </header>
                <div className="tower-items">
                  {items.map((item) => (
                    <article
                      className={`work-signal-card ${item.urgency}`}
                      key={item.key}
                    >
                      <label className="work-select" title="일괄 처리 선택">
                        <input
                          type="checkbox"
                          disabled={item.category === "approval"}
                          checked={selected.has(item.key)}
                          onChange={() => toggle(item.key)}
                          aria-label={
                            item.category === "approval"
                              ? "승인은 승인함에서 처리"
                              : `${item.title} 선택`
                          }
                        />
                        <span />
                      </label>
                      <div className="work-signal-icon">
                        {categoryIcon(item.category)}
                      </div>
                      <div className="work-signal-copy">
                        <div className="work-signal-meta">
                          <Badge tone={urgencyTone(item.urgency)}>
                            {urgencyLabel(item.urgency)}
                          </Badge>
                          <span>{categoryLabel(item.category)}</span>
                          {item.number && <small>{item.number}</small>}
                        </div>
                        <h4>{item.title}</h4>
                        <p>{item.description}</p>
                        <footer>
                          {item.supplierName && (
                            <span>{item.supplierName}</span>
                          )}
                          <span>
                            {item.dueDate
                              ? `기한 ${date(item.dueDate)}`
                              : `등록 ${date(item.createdAt)}`}
                          </span>
                        </footer>
                      </div>
                      <div className="work-signal-actions">
                        <button
                          className="button ghost"
                          disabled={busy}
                          onClick={() =>
                            void updateState([item.key], "snoozed", 1)
                          }
                          title="내일 오전 9시까지 보류"
                        >
                          <TimerReset />
                          보류
                        </button>
                        <button
                          className="button secondary"
                          onClick={() => navigate(item.url)}
                        >
                          {item.category === "approval" ? "검토" : "열기"}
                          <ChevronRight />
                        </button>
                        {item.category !== "approval" && (
                          <button
                            className="icon-button work-done"
                            disabled={busy}
                            onClick={() => void updateState([item.key], "done")}
                            title="관제탑에서 확인 완료"
                            aria-label={`${item.title} 확인 완료`}
                          >
                            <Check />
                          </button>
                        )}
                      </div>
                    </article>
                  ))}
                </div>
              </section>
            );
          })}
        </div>
      ) : (
        <Empty
          icon={<Radar />}
          title={
            query || category !== "all"
              ? "조건에 맞는 업무 신호가 없습니다"
              : "현재 감지된 업무 신호가 없습니다"
          }
          description={
            query || category !== "all"
              ? "검색어 또는 업무 유형 필터를 바꿔 보세요."
              : "모든 긴급 업무를 확인했습니다. 새로고침해 최신 상태를 확인할 수 있습니다."
          }
        />
      )}
    </div>
  );
}

function Signal({
  icon,
  label,
  value,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: number;
  tone: string;
}) {
  return (
    <div className={`tower-signal ${tone}`}>
      {icon}
      <span>{label}</span>
      <b>{value}</b>
    </div>
  );
}

function categoryIcon(category: string) {
  const Icon =
    categories.find((item) => item.key === category)?.icon || ListChecks;
  return <Icon />;
}

function categoryLabel(category: string) {
  return categories.find((item) => item.key === category)?.label || "업무";
}

function urgencyTone(urgency: WorkItem["urgency"]) {
  if (urgency === "critical") return "danger";
  if (urgency === "high" || urgency === "medium") return "warning";
  if (urgency === "normal") return "info";
  return "neutral";
}

function urgencyLabel(urgency: WorkItem["urgency"]) {
  return {
    critical: "긴급",
    high: "높음",
    medium: "주의",
    normal: "알림",
    low: "예정",
  }[urgency];
}
