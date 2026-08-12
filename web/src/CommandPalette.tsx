import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRight, Clock3, CornerDownLeft, Search, X } from "lucide-react";

export type QuickNavigationItem = {
  label: string;
  path: string;
  group: string;
  keywords?: string;
};

const recentKey = "vendra.quick-navigation.recent";

export default function CommandPalette({
  items,
  onNavigate,
  onClose,
}: {
  items: QuickNavigationItem[];
  onNavigate: (path: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const recent = useMemo(() => readRecent(), []);
  const results = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase("ko-KR");
    let candidates = items;
    if (normalized) {
      candidates = items.filter((item) =>
        `${item.label} ${item.group} ${item.keywords || ""}`
          .toLocaleLowerCase("ko-KR")
          .includes(normalized),
      );
    } else if (recent.length) {
      const recentItems = recent
        .map((path) => items.find((item) => item.path === path))
        .filter((item): item is QuickNavigationItem => Boolean(item));
      const used = new Set(recentItems.map((item) => item.path));
      candidates = [
        ...recentItems.map((item) => ({ ...item, group: "최근 이동" })),
        ...items.filter((item) => !used.has(item.path)),
      ];
    }
    const searchItem =
      normalized.length >= 2
        ? [{
            label: `“${query.trim()}” 통합 검색`,
            path: `/search?q=${encodeURIComponent(query.trim())}`,
            group: "데이터 검색",
            keywords: query.trim(),
          }]
        : [];
    return [...searchItem, ...candidates].slice(0, 14);
  }, [items, query, recent]);

  useEffect(() => {
    input.current?.focus();
    const original = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = original;
    };
  }, []);
  function choose(item: QuickNavigationItem) {
    writeRecent(item.path);
    onNavigate(item.path);
    onClose();
  }

  return (
    <div
      className="command-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        className="command-palette"
        role="dialog"
        aria-modal="true"
        aria-label="빠른 이동"
      >
        <header>
          <Search />
          <input
            ref={input}
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") onClose();
              if (event.key === "ArrowDown") {
                event.preventDefault();
                setActive((value) =>
                  results.length ? Math.min(value + 1, results.length - 1) : 0,
                );
              }
              if (event.key === "ArrowUp") {
                event.preventDefault();
                setActive((value) => Math.max(value - 1, 0));
              }
              if (event.key === "Enter" && results[active]) {
                event.preventDefault();
                choose(results[active]);
              }
            }}
            placeholder="화면 이동 또는 전체 데이터 검색"
            aria-label="빠른 이동 검색"
            aria-controls="quick-navigation-results"
          />
          <kbd>ESC</kbd>
          <button type="button" onClick={onClose} aria-label="빠른 이동 닫기">
            <X />
          </button>
        </header>
        <div className="command-results" id="quick-navigation-results">
          {results.map((item, index) => (
            <button
              type="button"
              className={index === active ? "active" : ""}
              key={`${item.group}-${item.path}`}
              onMouseEnter={() => setActive(index)}
              onClick={() => choose(item)}
            >
              <span className="command-result-icon">
                {item.group === "최근 이동" ? <Clock3 /> : <ArrowRight />}
              </span>
              <span>
                <b>{item.label}</b>
                <small>{item.group}</small>
              </span>
              {index === active && <CornerDownLeft />}
            </button>
          ))}
          {!results.length && (
            <div className="command-empty">
              <Search />
              <b>일치하는 화면이 없습니다</b>
              <span>두 글자 이상 입력하면 전체 데이터 검색으로 이동합니다.</span>
            </div>
          )}
        </div>
        <footer>
          <span><kbd>↑</kbd><kbd>↓</kbd> 이동</span>
          <span><kbd>↵</kbd> 열기</span>
          <span>메뉴, 관리자, 개인화와 업무 데이터를 한 번에 찾습니다.</span>
        </footer>
      </section>
    </div>
  );
}

function readRecent(): string[] {
  try {
    const value = JSON.parse(localStorage.getItem(recentKey) || "[]");
    return Array.isArray(value) ? value.slice(0, 5) : [];
  } catch {
    return [];
  }
}

function writeRecent(path: string) {
  if (path.startsWith("/search?q=")) return;
  try {
    const next = [path, ...readRecent().filter((item) => item !== path)].slice(
      0,
      5,
    );
    localStorage.setItem(recentKey, JSON.stringify(next));
  } catch {
    // Private browsing may disallow storage; navigation still works.
  }
}
