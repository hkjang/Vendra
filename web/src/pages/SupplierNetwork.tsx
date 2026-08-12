import { useEffect, useMemo, useState } from "react";
import {
  AlertTriangle,
  Network,
  Search,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { api, money } from "../api";
import { Badge, Loading, PageHeader, RiskBadge } from "../components";

type Node = {
  id: string;
  name: string;
  riskLevel: string;
  grade?: string;
  annualSpend: number;
  categories: string[];
};
type Edge = {
  id: string;
  source: string;
  target: string;
  relationshipType: string;
  criticality: string;
  dependencyPercent?: number;
};
type NetworkData = { nodes: Node[]; edges: Edge[] };

export default function SupplierNetwork() {
  const [data, setData] = useState<NetworkData>();
  const [selected, setSelected] = useState<Node>();
  const [query, setQuery] = useState("");
  const [zoom, setZoom] = useState(1);
  useEffect(() => {
    api<NetworkData>("/api/v1/supplier-network").then(setData);
  }, []);
  const nodes = useMemo(
    () =>
      data?.nodes
        .filter(
          (node) =>
            !query || node.name.toLowerCase().includes(query.toLowerCase()),
        )
        .slice(0, 24) || [],
    [data, query],
  );
  if (!data) return <Loading label="공급망 관계를 구성하는 중" />;
  const positions = layout(nodes);
  const byId = new Map(positions.map((item) => [item.node.id, item]));
  const viewWidth = 900 / zoom;
  const viewHeight = 610 / zoom;
  const viewBox = `${(900 - viewWidth) / 2} ${(610 - viewHeight) / 2} ${viewWidth} ${viewHeight}`;
  const adjustZoom = (amount: number) =>
    setZoom((value) => Math.min(1.5, Math.max(0.75, value + amount)));
  return (
    <div className="page network-page">
      <PageHeader
        eyebrow="Multi-tier supply network"
        title="공급망 Network"
        description="N차 협력업체 관계, 공급 집중도와 연쇄 리스크를 그래프로 분석합니다."
      />
      <div className="network-toolbar">
        <div className="search-box">
          <Search />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="공급업체 검색"
            aria-label="공급망 공급업체 검색"
          />
        </div>
        <div>
          <span><i className="node-low" />LOW</span>
          <span><i className="node-medium" />MEDIUM</span>
          <span><i className="node-high" />HIGH · CRITICAL</span>
        </div>
        <button
          type="button"
          className="icon-button"
          onClick={() => adjustZoom(-0.15)}
          disabled={zoom <= 0.75}
          aria-label="공급망 축소"
          title="축소"
        >
          <ZoomOut />
        </button>
        <button
          type="button"
          className="network-zoom"
          onClick={() => setZoom(1)}
          title="100%로 초기화"
        >
          {Math.round(zoom * 100)}%
        </button>
        <button
          type="button"
          className="icon-button"
          onClick={() => adjustZoom(0.15)}
          disabled={zoom >= 1.5}
          aria-label="공급망 확대"
          title="확대"
        >
          <ZoomIn />
        </button>
      </div>
      <div className="network-layout">
        <div className="network-canvas">
          <svg viewBox={viewBox} role="img" aria-label="공급업체 관계 그래프">
            <defs>
              <filter id="shadow">
                <feDropShadow
                  dx="0"
                  dy="4"
                  stdDeviation="5"
                  floodOpacity=".13"
                />
              </filter>
              <marker
                id="arrow"
                markerWidth="7"
                markerHeight="7"
                refX="6"
                refY="3.5"
                orient="auto"
              >
                <path d="M0 0L7 3.5L0 7Z" fill="#9aa4b5" />
              </marker>
            </defs>
            {data.edges.map((edge) => {
              const from = byId.get(edge.source);
              const to = byId.get(edge.target);
              if (!from || !to) return null;
              return (
                <g key={edge.id}>
                  <line
                    x1={from.x}
                    y1={from.y}
                    x2={to.x}
                    y2={to.y}
                    className={
                      edge.criticality === "critical" ? "critical-edge" : ""
                    }
                    markerEnd="url(#arrow)"
                  />
                  <text
                    x={(from.x + to.x) / 2}
                    y={(from.y + to.y) / 2 - 5}
                  >
                    {edge.dependencyPercent
                      ? `${edge.dependencyPercent}%`
                      : edge.relationshipType}
                  </text>
                </g>
              );
            })}
            {positions.map(({ node, x, y }, index) => (
              <g
                className={`network-node ${node.riskLevel.toLowerCase()} ${selected?.id === node.id ? "selected" : ""}`}
                key={node.id}
                onClick={() => setSelected(node)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    setSelected(node);
                  }
                }}
                transform={`translate(${x},${y})`}
                tabIndex={0}
                role="button"
                aria-label={`${node.name}, 위험 ${node.riskLevel}`}
              >
                <circle r={index < 3 ? 34 : 27} filter="url(#shadow)" />
                <text textAnchor="middle" y="-2">
                  {node.name.slice(0, 8)}
                </text>
                <text className="node-grade" textAnchor="middle" y="13">
                  {node.grade || node.riskLevel}
                </text>
              </g>
            ))}
          </svg>
          {!nodes.length && (
            <div className="network-empty">
              <Network />
              <b>표시할 공급업체가 없습니다</b>
            </div>
          )}
          <div className="network-hint">
            노드를 선택하면 공급업체의 위험과 의존도를 확인할 수 있습니다.
          </div>
        </div>
        <aside className="network-inspector">
          {selected ? (
            <>
              <div className="network-company">
                <span className="company-avatar hero-avatar">
                  {selected.name.slice(0, 2)}
                </span>
                <div>
                  <h2>{selected.name}</h2>
                  <p>{selected.categories.join(", ") || "품목 미지정"}</p>
                </div>
              </div>
              <div className="network-risk">
                <span>Risk level</span>
                <RiskBadge level={selected.riskLevel} />
                <strong>{selected.grade || "—"}</strong>
                <small>Supplier grade</small>
              </div>
              <dl>
                <dt>연간 구매금액</dt>
                <dd>{money(selected.annualSpend)}</dd>
                <dt>직접 연결</dt>
                <dd>
                  {
                    data.edges.filter(
                      (edge) =>
                        edge.source === selected.id || edge.target === selected.id,
                    ).length
                  }
                  개
                </dd>
                <dt>공급 품목</dt>
                <dd>{selected.categories.length}개</dd>
              </dl>
              {selected.riskLevel === "HIGH" ||
              selected.riskLevel === "CRITICAL" ? (
                <div className="network-alert">
                  <AlertTriangle />
                  <span>
                    <b>연쇄 리스크 주의</b>
                    연결된 상·하위 공급업체의 대체 가능성을 검토하세요.
                  </span>
                </div>
              ) : (
                <div className="network-safe">
                  <Badge tone="success">정상 모니터링</Badge>
                </div>
              )}
              <a className="button" href={`/suppliers/${selected.id}`}>
                Supplier 360 열기
              </a>
            </>
          ) : (
            <div className="inspector-empty">
              <Network />
              <h3>공급업체를 선택하세요</h3>
              <p>평가, 위험, 구매금액과 연결 관계를 표시합니다.</p>
            </div>
          )}
        </aside>
      </div>
    </div>
  );
}

function layout(nodes: Node[]) {
  const centerX = 450;
  const centerY = 300;
  return nodes.map((node, index) => {
    if (index === 0) return { node, x: centerX, y: centerY };
    const ring = index <= 8 ? 1 : 2;
    const inRing = ring === 1 ? 8 : Math.max(1, nodes.length - 9);
    const ringIndex = ring === 1 ? index - 1 : index - 9;
    const angle = (ringIndex / inRing) * Math.PI * 2 - Math.PI / 2;
    const radius = ring === 1 ? 145 : 255;
    return {
      node,
      x: centerX + Math.cos(angle) * radius,
      y: centerY + Math.sin(angle) * radius,
    };
  });
}
