import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import Objects from "./Objects";

const draftPath = "/api/v1/me/drafts/new-object:contract";
const contractPath = "/api/v1/contracts";

function deferredResponse() {
  let resolve!: (response: Response) => void;
  const promise = new Promise<Response>((done) => { resolve = done; });
  return { promise, resolve };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// Only replace the network: Objects, React submit events, HTMLFormElement,
// FormData and api/post/put/del all use their production implementations.
function network() {
  const draft = deferredResponse();
  const saved = deferredResponse();
  const deleted = deferredResponse();
  const fetch = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    const path = String(input);
    const method = options?.method || "GET";
    if (path === draftPath) {
      if (method === "GET") return json({ draft: null });
      if (method === "PUT") return draft.promise;
      if (method === "DELETE") return deleted.promise;
    }
    if (method === "POST" && path === contractPath) return saved.promise;
    if (method === "GET") {
      if (path.startsWith(`${contractPath}?`)) return json({ items: [] });
      if (path.startsWith("/api/v1/suppliers?")) return json({ items: [] });
      if (path.startsWith("/api/v1/me/saved-views?"))
        return json({ items: [], canShare: false });
    }
    throw new Error(`Unexpected request: ${method} ${path}`);
  });
  vi.stubGlobal("fetch", fetch);
  const calls = (method: string, path: string) => fetch.mock.calls.filter(
    ([input, options]) => (options?.method || "GET") === method && String(input).split("?")[0] === path,
  );
  return { draft, saved, deleted, calls };
}

async function openContract() {
  render(<MemoryRouter initialEntries={["/contracts"]}><Objects type="contract" /></MemoryRouter>);
  await screen.findByText("계약 데이터가 없습니다");
  fireEvent.click(screen.getByRole("button", { name: "새 계약" }));
  await screen.findByText("입력 내용이 자동 저장됩니다.");
  return screen.getByPlaceholderText("계약 제목");
}

function submit() {
  fireEvent.click(screen.getByRole("button", { name: "초안 저장" }));
}

async function finishDeletion(net: ReturnType<typeof network>) {
  await waitFor(() => expect(net.calls("DELETE", draftPath)).toHaveLength(1));
  // A successful POST must still wait for draft deletion before closing and reloading.
  expect(screen.getByPlaceholderText("계약 제목")).toBeInTheDocument();
  expect(net.calls("GET", contractPath)).toHaveLength(1);
  await act(async () => net.deleted.resolve(new Response(null, { status: 204 })));
  await waitFor(() => expect(screen.queryByPlaceholderText("계약 제목")).not.toBeInTheDocument());
  expect(net.calls("GET", contractPath)).toHaveLength(2);
  expect(screen.getByText("계약 데이터가 없습니다")).toBeInTheDocument();
}

afterEach(() => {
  try {
    cleanup();
  } finally {
    vi.unstubAllGlobals();
  }
});

describe("NewObject draft submission", () => {
  it("waits for autosave and posts the values captured at submission before deleting the draft and reloading", async () => {
    const net = network();
    const title = await openContract();
    fireEvent.change(title, { target: { value: "자동 저장 제목" } });
    await waitFor(() => expect(net.calls("PUT", draftPath)).toHaveLength(1), { timeout: 2000 });

    // The request in flight holds an older draft; explicit save needs the latest form.
    fireEvent.change(title, { target: { value: "제출 시점 제목" } });
    fireEvent.change(screen.getByLabelText("금액"), { target: { value: "15000" } });
    fireEvent.change(screen.getByLabelText("설명"), { target: { value: "제출 시점 설명" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /사용/ }));
    submit();
    expect(net.calls("POST", contractPath)).toHaveLength(0);
    expect(net.calls("DELETE", draftPath)).toHaveLength(0);

    // Capturing only a DOM reference would read these later edits instead.
    fireEvent.change(title, { target: { value: "제출 이후 제목" } });
    fireEvent.change(screen.getByLabelText("금액"), { target: { value: "25000" } });
    fireEvent.change(screen.getByLabelText("설명"), { target: { value: "제출 이후 설명" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /사용/ }));
    await act(async () => net.draft.resolve(json({})));

    await waitFor(() => expect(net.calls("POST", contractPath)).toHaveLength(1));
    const body = JSON.parse(String(net.calls("POST", contractPath)[0][1]?.body));
    expect(body).toMatchObject({
      title: "제출 시점 제목", amount: 15000, currency: "KRW", status: "draft",
      data: { description: "제출 시점 설명", autoRenewal: true },
    });
    expect(net.calls("DELETE", draftPath)).toHaveLength(0);
    await act(async () => net.saved.resolve(json({ id: "contract-1" }, 201)));
    await finishDeletion(net);
    expect(net.calls("POST", contractPath)).toHaveLength(1);
    expect(net.calls("PUT", draftPath)).toHaveLength(1);
  });

  it("submits immediately when no autosave is in flight", async () => {
    const net = network();
    const title = await openContract();
    fireEvent.change(title, { target: { value: "즉시 제출" } });
    submit();
    await waitFor(() => expect(net.calls("POST", contractPath)).toHaveLength(1));
    expect(JSON.parse(String(net.calls("POST", contractPath)[0][1]?.body)).title).toBe("즉시 제출");
    expect(net.calls("PUT", draftPath)).toHaveLength(0);
    await act(async () => net.saved.resolve(json({ id: "contract-1" }, 201)));
    await finishDeletion(net);
  });

  it("continues explicit saving after the pending autosave returns 500", async () => {
    const net = network();
    const title = await openContract();
    fireEvent.change(title, { target: { value: "초안 실패 후 제출" } });
    await waitFor(() => expect(net.calls("PUT", draftPath)).toHaveLength(1), { timeout: 2000 });
    submit();
    expect(net.calls("POST", contractPath)).toHaveLength(0);
    await act(async () => net.draft.resolve(json({ error: { message: "초안 저장 실패" } }, 500)));
    await waitFor(() => expect(net.calls("POST", contractPath)).toHaveLength(1));
    expect(screen.getByText("자동 저장을 확인할 수 없습니다. 직접 저장해 주세요.")).toBeInTheDocument();
    await act(async () => net.saved.resolve(json({ id: "contract-1" }, 201)));
    await finishDeletion(net);
  });

  it("shows the POST error in the form and allows saving corrected input again", async () => {
    const net = network();
    const title = await openContract();
    fireEvent.change(title, { target: { value: "첫 제출" } });
    submit();
    await act(async () => net.saved.resolve(json({ error: { message: "계약을 저장할 수 없습니다" } }, 500)));
    await screen.findByText("계약을 저장할 수 없습니다");
    expect(screen.getByRole("button", { name: "초안 저장" })).toBeEnabled();
    expect(net.calls("DELETE", draftPath)).toHaveLength(0);
    expect(net.calls("GET", contractPath)).toHaveLength(1);

    // Replace only the next network answer; the retry still goes through post/api.
    vi.mocked(fetch).mockImplementationOnce(async () => json({ id: "contract-1" }, 201));
    fireEvent.change(title, { target: { value: "수정 후 제출" } });
    submit();
    await waitFor(() => expect(net.calls("DELETE", draftPath)).toHaveLength(1));
    expect(net.calls("POST", contractPath)).toHaveLength(2);
    expect(JSON.parse(String(net.calls("POST", contractPath)[1][1]?.body)).title).toBe("수정 후 제출");
    expect(screen.queryByText("계약을 저장할 수 없습니다")).not.toBeInTheDocument();
    await finishDeletion(net);
  });
});
