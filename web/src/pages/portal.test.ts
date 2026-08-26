import { describe, expect, it } from "vitest";
import { evaluationPeriod, type PortalEvaluation } from "./portal-format";

const evaluation = (over: Partial<PortalEvaluation> = {}): PortalEvaluation => ({
  id: "e1",
  evaluationType: "정기",
  completedAt: "2026-08-26T11:00:00+09:00",
  ...over,
});

describe("evaluationPeriod", () => {
  it("names the period the score covers", () => {
    // The card used to show completedAt, which is the row's updated_at: two
    // evaluations of different quarters with the same score rendered as two
    // identical cards, both stamped with today.
    const first = evaluationPeriod(
      evaluation({ periodStart: "2026-02-27", periodEnd: "2026-05-28" }),
    );
    const second = evaluationPeriod(
      evaluation({ periodStart: "2025-08-31", periodEnd: "2025-11-29" }),
    );
    expect(first).toContain("2026");
    expect(second).toContain("2025");
    expect(first).not.toBe(second);
  });

  it("says what it knows when only one end is recorded", () => {
    expect(evaluationPeriod(evaluation({ periodStart: "2026-02-27" }))).toMatch(/부터$/);
    expect(evaluationPeriod(evaluation({ periodEnd: "2026-05-28" }))).toMatch(/까지$/);
  });

  it("says nothing rather than something wrong", () => {
    // An empty string is the caller's signal to fall back to "평가 기간 미지정";
    // a half-rendered range with an em dash on one side would read as a date.
    expect(evaluationPeriod(evaluation())).toBe("");
    expect(evaluationPeriod(evaluation({ periodStart: null, periodEnd: null }))).toBe("");
    expect(evaluationPeriod(evaluation({ periodStart: "쓰레기", periodEnd: "2026-13-45" }))).toBe("");
  });
});
