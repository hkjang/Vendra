import { date } from "../api";

export type PortalEvaluation = {
  id: string;
  evaluationType: string;
  templateName?: string;
  totalScore?: number;
  grade?: string;
  comments?: string;
  periodStart?: string | null;
  periodEnd?: string | null;
  completedAt: string;
};

/**
 * The period an evaluation covers, for the card a supplier reads.
 *
 * That period is the first thing someone looking at a score wants to know, and
 * the card used to show only completedAt — which is the row's updated_at. Two
 * evaluations of different quarters scored the same rendered as two identical
 * cards, both stamped with today.
 *
 * An empty answer is the caller's signal to say the period is unrecorded. A
 * half-rendered range with an em dash on one side would read as a date.
 */
export function evaluationPeriod(item: PortalEvaluation) {
  const from = date(item.periodStart);
  const to = date(item.periodEnd);
  if (from === "—" && to === "—") return "";
  if (from === "—") return `${to}까지`;
  if (to === "—") return `${from}부터`;
  return `${from} – ${to}`;
}
