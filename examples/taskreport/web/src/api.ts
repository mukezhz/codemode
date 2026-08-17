// The browser's half of the contract in examples/taskreport/view.
//
// Everything the chat renders arrives as one of these blocks, so adding a way
// to visualise an answer means adding a block here and a component in
// components/Blocks.tsx — never a change to the tasks API.

export type Tone = "good" | "warn" | "bad" | undefined;

export type Block =
  | { type: "stats"; heading?: string; items: { label: string; value: number; tone?: Tone }[] }
  | { type: "bar"; heading?: string; series: { label: string; value: number }[] }
  | { type: "line"; heading?: string; series: { label: string; value: number }[] }
  | { type: "table"; heading?: string; columns: string[]; rows: string[][] }
  | { type: "text"; heading?: string; text: string };

export type ViewDocument = {
  title?: string;
  blocks?: Block[];
  // Set when the model could not answer with the capabilities this caller has.
  // A refusal carries no blocks: it is an answer, not a broken one.
  unavailable?: string;
};

export type Task = {
  id: number;
  owner: string;
  title: string;
  project: string;
  priority: "low" | "medium" | "high";
  due_date: string;
  completed: boolean;
  completed_date: string;
  created_date: string;
  tags: string[];
};

export type Session = {
  user: string;
  role: "member" | "admin";
  capabilities: string[];
  namespaces: string[];
  withheld: number;
  model: string;
};

export type StatusCounts = {
  total: number;
  open: number;
  completed: number;
  overdue: number;
  due_today: number;
};

export type ChatResult = {
  document: ViewDocument;
  program: string;
  attempts: number;
  used: { capability: { name: string; description: string }; distance: number }[];
  took: string;
  cached: boolean;
  // Nanoseconds, as Go encodes a duration. Divide before showing.
  timing: { Total: number; Cache: number; Search: number; Generate: number; Run: number };
};

// The demo switches users from a dropdown, so identity lives in localStorage
// and rides on a header. A real deployment uses a verified session and this
// module sends nothing at all.
const USER_KEY = "taskreport.user";
const ROLE_KEY = "taskreport.role";

export function identity(): { user: string; role: string } {
  return {
    user: localStorage.getItem(USER_KEY) ?? "ada",
    role: localStorage.getItem(ROLE_KEY) ?? "member",
  };
}

export function setIdentity(user: string, role: string) {
  localStorage.setItem(USER_KEY, user);
  localStorage.setItem(ROLE_KEY, role);
}

async function call<T>(path: string, init?: RequestInit): Promise<T> {
  const { user, role } = identity();
  const response = await fetch(`/api${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      "X-Demo-User": user,
      "X-Demo-Role": role,
      ...init?.headers,
    },
  });
  const text = await response.text();
  const body = text ? JSON.parse(text) : null;
  if (!response.ok) {
    throw new Error(body?.error ?? `${response.status} ${response.statusText}`);
  }
  return body as T;
}

export const api = {
  session: () => call<Session>("/session"),
  summary: () => call<StatusCounts>("/summary"),

  tasks: (query: Record<string, string> = {}) => {
    const search = new URLSearchParams(
      Object.entries(query).filter(([, value]) => value !== ""),
    ).toString();
    return call<{ tasks: Task[]; count: number }>(`/tasks${search ? `?${search}` : ""}`);
  },

  create: (task: { title: string; project?: string; priority?: string; due_date?: string }) =>
    call<{ task: Task }>("/tasks", { method: "POST", body: JSON.stringify(task) }),

  complete: (id: number) => call<{ task: Task }>(`/tasks/${id}/complete`, { method: "POST" }),
  reopen: (id: number) => call<{ task: Task }>(`/tasks/${id}/reopen`, { method: "POST" }),
  remove: (id: number) => call<{ deleted: boolean }>(`/tasks/${id}`, { method: "DELETE" }),

  chat: (request: string) =>
    call<ChatResult>("/chat", { method: "POST", body: JSON.stringify({ request }) }),

  chatStream: streamChat,
};

// streamChat asks over server-sent events, calling onChunk with the program as
// the model writes it and resolving with the finished answer.
//
// The answer does not arrive sooner than it would from api.chat — a model call
// is a model call. What changes is that the several seconds it takes have
// something in them.
//
// EventSource cannot POST, so this reads the response body directly. That also
// means handling partial frames: a chunk boundary lands wherever the network
// puts it, not on an event boundary.
async function streamChat(
  request: string,
  onChunk: (text: string) => void,
  signal?: AbortSignal,
): Promise<ChatResult> {
  const { user, role } = identity();
  const response = await fetch("/api/chat/stream", {
    method: "POST",
    signal,
    headers: {
      "Content-Type": "application/json",
      "X-Demo-User": user,
      "X-Demo-Role": role,
    },
    body: JSON.stringify({ request }),
  });
  if (!response.ok || !response.body) {
    const text = await response.text();
    throw new Error(parseError(text) ?? `${response.status} ${response.statusText}`);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let result: ChatResult | null = null;

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });

    // Everything up to the last blank line is whole events; whatever follows
    // is an event still being written.
    let boundary = buffer.indexOf("\n\n");
    while (boundary !== -1) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      boundary = buffer.indexOf("\n\n");

      const [nameLine, ...dataLines] = frame.split("\n");
      const name = nameLine.replace(/^event:\s*/, "");
      const data = dataLines.join("\n").replace(/^data:\s*/, "");
      if (!data) continue;

      if (name === "chunk") {
        onChunk((JSON.parse(data) as { text: string }).text);
      } else if (name === "result") {
        result = JSON.parse(data) as ChatResult;
      } else if (name === "error") {
        throw new Error((JSON.parse(data) as { error: string }).error);
      }
    }
  }

  if (!result) {
    throw new Error("the answer stream ended without a result");
  }
  return result;
}

function parseError(text: string): string | null {
  try {
    return (JSON.parse(text) as { error?: string }).error ?? null;
  } catch {
    return null;
  }
}
