import { describe, expect, it, vi } from "vitest";
import { api, date, dateTime, isoDate, logTime, todayISO } from "./api";

describe("date", () => {
  it("reads a bare YYYY-MM-DD as a day, not an instant", () => {
    // `new Date("2026-12-24")` is UTC midnight, which renders as the 23rd
    // everywhere west of UTC — a contract ending on the 24th showed as the
    // 23rd. It has to be built in local time.
    expect(date("2026-12-24")).toContain("24");
    expect(date("2026-01-01")).toContain("1");
  });

  it("accepts the two-digit offset PostgreSQL writes", () => {
    // A timestamptz scanned into a Go string comes back as `+09`, and
    // `new Date` answers Invalid Date for that — the browser wants `+09:00`.
    expect(date("2026-08-26T11:17:33+09")).not.toBe("—");
    expect(date("2026-08-26T02:17:33+00")).not.toBe("—");
    expect(dateTime("2026-08-26T11:17:33+09")).not.toBe("—");
  });

  it("refuses a date the calendar does not have", () => {
    // The Date constructor wraps overflow instead of refusing it, so
    // "2026-13-45" rendered as the 14th of February 2027 — a plausible wrong
    // day, which is worse than nothing.
    expect(date("2026-13-45")).toBe("—");
    expect(date("2026-02-31")).toBe("—");
    expect(date("2026-02-29")).toBe("—"); // 2026 is not a leap year
    expect(date("2024-02-29")).not.toBe("—"); // 2024 is
    expect(date("2026-00-10")).toBe("—");
    expect(date("2026-01-00")).toBe("—");
  });

  it("answers an em dash rather than throwing on something unreadable", () => {
    // Intl.DateTimeFormat raises RangeError on an invalid date rather than
    // returning anything, so an unguarded call takes its whole panel down.
    for (const value of [undefined, null, "", "쓰레기", "2026-13-45", "not a date"]) {
      expect(date(value)).toBe("—");
      expect(dateTime(value)).toBe("—");
      expect(logTime(value)).toBe("—");
    }
  });

  it("renders the same instant identically through every helper", () => {
    const value = "2026-08-26T11:17:33+09:00";
    for (const rendered of [date(value), dateTime(value), logTime(value)]) {
      expect(rendered).not.toBe("—");
    }
  });
});

describe("isoDate", () => {
  it("names the day in the viewer's own calendar", () => {
    // toISOString answers in UTC, so it names yesterday for the first nine
    // hours of every Korean day.
    const midnightLocal = new Date(2026, 7, 26, 0, 30);
    expect(isoDate(midnightLocal)).toBe("2026-08-26");
    expect(isoDate(new Date(2026, 0, 5))).toBe("2026-01-05");
  });

  it("pads a single-digit month and day", () => {
    expect(isoDate(new Date(2026, 2, 7))).toBe("2026-03-07");
  });

  it("agrees with todayISO", () => {
    expect(todayISO()).toBe(isoDate(new Date()));
  });
});

describe("APIError", () => {
  it("carries the envelope's code, so a screen can branch on the refusal", async () => {
    // Only the message used to survive the fetch wrapper. A page that wants to
    // offer the way out — sign in, rather than retype the form — had nothing to
    // branch on but the Korean sentence, which is not a contract.
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 409,
      json: async () => ({
        error: { code: "email_registered", message: "이미 가입된 이메일입니다" },
      }),
    });
    vi.stubGlobal("fetch", fetchMock);
    try {
      await expect(api("/api/auth/register")).rejects.toMatchObject({
        status: 409,
        code: "email_registered",
        message: "이미 가입된 이메일입니다",
      });
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("leaves the code empty when the server sent no envelope", async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: false,
      status: 502,
      json: async () => {
        throw new Error("not json");
      },
    });
    vi.stubGlobal("fetch", fetchMock);
    try {
      await expect(api("/api/version")).rejects.toMatchObject({ code: "" });
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
