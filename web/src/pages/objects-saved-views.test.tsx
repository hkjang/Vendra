import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Objects from "./Objects";

/**
 * The saved-view dropdown used to answer every refusal the same way the empty
 * register does. `loadViews` carried `.catch(() => setViews([]))`, so a 500 the
 * server had explained in a sentence ("저장된 보기를 조회하지 못했습니다", two of
 * them in listSavedViews) and an expired session reached the screen as a
 * dropdown holding nothing but 기본 보기 — not one character different from a
 * reader who has never saved a view.
 *
 * Worse, saveView and removeView both await loadViews() after they succeed: the
 * POST was stored with a 201, the following GET was refused, and setViews([])
 * then wiped the list while the modal was already closed and viewError empty.
 * The reader saw no message at all and saved the same view again.
 *
 * Only the network is replaced here. Objects, React events, HTMLFormElement,
 * FormData and the real api/post/del all run, because the scenario above is a
 * successful `post` followed by a refused `api` — and `post` calls the `api`
 * inside api.ts, which a module mock of the exported `api` does not replace.
 */

const viewsPath = "/api/v1/me/saved-views";
const contractPath = "/api/v1/contracts";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const refusal = (message: string, status = 500) =>
  json({ error: { code: "database_error", message } }, status);

const savedView = (id: string, name: string) => ({
  id,
  name,
  context: "object:contract",
  filters: {},
  columns: [],
  shared: false,
  owned: true,
});

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
}

// `answers` is read one per GET of the saved-view list, so a test can say "this
// attempt is refused and the next one is not"; the last entry repeats.
function network(answers: Array<() => Response | Promise<Response>>) {
  let answered = 0;
  const fetch = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    const path = String(input);
    const method = options?.method || "GET";
    if (path.split("?")[0] === viewsPath) {
      if (method === "GET") {
        const answer = answers[Math.min(answered, answers.length - 1)];
        answered += 1;
        return answer();
      }
      if (method === "POST") return json({ id: "view-saved" }, 201);
    }
    if (method === "DELETE" && path.startsWith(`${viewsPath}/`))
      return new Response(null, { status: 204 });
    if (method === "GET") {
      if (path.startsWith(`${contractPath}?`)) return json({ items: [] });
      if (path.startsWith("/api/v1/suppliers?")) return json({ items: [] });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal("fetch", fetch);
  const calls = (method: string, path: string) =>
    fetch.mock.calls.filter(
      ([input, options]) =>
        (options?.method || "GET") === method && String(input).split("?")[0] === path,
    );
  return { calls };
}

async function openContracts() {
  render(
    <MemoryRouter initialEntries={["/contracts"]}>
      <Objects type="contract" />
    </MemoryRouter>,
  );
  await screen.findByText("계약 데이터가 없습니다");
}

const banner = () => document.querySelector(".saved-views-load-failed");

const viewOptions = () =>
  Array.from(
    screen.getByLabelText("저장된 보기").querySelectorAll("option"),
  ).map((option) => option.textContent);

afterEach(() => {
  try {
    cleanup();
  } finally {
    vi.unstubAllGlobals();
  }
});

describe("ObjectList 저장된 보기 조회 실패", () => {
  it("says what the server said instead of looking like an empty register", async () => {
    network([() => refusal("저장된 보기를 조회하지 못했습니다")]);
    await openContracts();

    await waitFor(() => expect(banner()).toBeTruthy());
    expect(banner()).toHaveTextContent("저장된 보기를 조회하지 못했습니다");
  });

  it("falls back to its own wording when the refusal carries no message", async () => {
    network([() => new Response(null, { status: 401 })]);
    await openContracts();

    await waitFor(() => expect(banner()).toBeTruthy());
    // api() words a bodiless refusal as 요청 실패 (401), which is what a reader
    // whose session expired gets to see rather than nothing at all.
    expect(banner()).toHaveTextContent("요청 실패 (401)");
  });

  it("keeps the views already on screen when the refresh after a save is refused", async () => {
    const refused = deferredResponse();
    const net = network([
      () => json({ items: [savedView("view-1", "지난달 만료 계약")], canShare: false }),
      () => refused.promise,
    ]);
    await openContracts();
    await waitFor(() => expect(viewOptions()).toContain("지난달 만료 계약"));

    fireEvent.click(screen.getByRole("button", { name: "보기 저장" }));
    const modal = within(await screen.findByRole("dialog"));
    fireEvent.change(screen.getByPlaceholderText("예: 이번 달 만료 계약"), {
      target: { value: "이번 주 계약" },
    });
    fireEvent.click(modal.getByRole("button", { name: "보기 저장" }));

    await waitFor(() => expect(net.calls("POST", viewsPath)).toHaveLength(1));
    await waitFor(() => expect(net.calls("GET", viewsPath)).toHaveLength(2));
    // Resolving inside act lands the refusal and flushes the render it causes,
    // so the two assertions below read a settled screen rather than a race.
    await act(async () => refused.resolve(refusal("저장된 보기를 조회하지 못했습니다")));

    // The POST was stored. The refused refresh must not take the list away as
    // well — what the server last actually said is still the best thing to show.
    expect(viewOptions()).toContain("지난달 만료 계약");
    expect(banner()).toHaveTextContent("저장된 보기를 조회하지 못했습니다");
  });

  it("retries the query itself and clears the notice once it answers", async () => {
    const net = network([
      () => refusal("저장된 보기를 조회하지 못했습니다"),
      () => json({ items: [savedView("view-1", "지난달 만료 계약")], canShare: false }),
    ]);
    await openContracts();
    await waitFor(() => expect(banner()).toBeTruthy());
    expect(net.calls("GET", viewsPath)).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: "저장된 보기 다시 시도" }));

    await waitFor(() => expect(viewOptions()).toContain("지난달 만료 계약"));
    expect(net.calls("GET", viewsPath)).toHaveLength(2);
    expect(banner()).toBeNull();
  });
});
