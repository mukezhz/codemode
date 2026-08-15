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
};
