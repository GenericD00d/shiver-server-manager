// Talks to the manager's web back end. Every request that changes something
// carries the app's header; the back end refuses requests without it.

export class ApiError extends Error {
  status: number;
  body: Record<string, unknown>;
  constructor(status: number, body: Record<string, unknown>) {
    super(String(body.error ?? `request failed (${status})`));
    this.status = status;
    this.body = body;
  }
}

let onLoggedOut: () => void = () => {};
export const setOnLoggedOut = (fn: () => void) => {
  onLoggedOut = fn;
};

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(method !== 'GET' ? { 'X-Requested-By': 'shiver-server-manager' } : {})
    },
    body: body === undefined ? undefined : JSON.stringify(body)
  });
  let data: unknown = null;
  const text = await res.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text };
    }
  }
  if (!res.ok) {
    if (res.status === 401 && path !== '/api/login') onLoggedOut();
    throw new ApiError(res.status, (data as Record<string, unknown>) ?? {});
  }
  return data as T;
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {})
};

export type Me = { user: string; version: string; app: string };

export type Server = {
  id: string;
  status: string;
  domain: string;
  tls: 'letsencrypt' | 'selfsigned';
  testMode: boolean;
  sharkordVersion: string;
  location: string;
  dataPath: string;
  httpPort: number;
  webrtcPort: number;
  encrypted: boolean;
  settings?: Record<string, string>;
  stopped?: boolean;
  skipDNSCheck?: boolean;
  memoryMaxMiB?: number;
  cpuPercent?: number;
  operation: { name: string; lastStep?: string; failedStep?: string; error?: string } | null;
  active: string;
  healthy: boolean;
  tokenSaved: boolean;
  locked: boolean;
};

export type CatalogPlugin = {
  id: string;
  name: string;
  description: string;
  repo: string;
  latest?: string;
  error?: string;
  servers: { instance: string; installed?: string; update?: boolean }[];
};

export type PluginList = { instance: string; plugins: { id: string; name: string; version: string; author: string }[] };

export type Notice = { key: string; level: 'error' | 'warning' | 'info'; instance?: string; message: string };
export type Health = { checkedAt?: string; notices: Notice[] | null };

export type Meta = {
  settings: { Name: string; Help: string; Kind: string }[];
  actions: Record<string, { title: string; reauth: boolean }>;
  help: string;
  version: string;
};

export type Job = { id: string; action: string; title: string; user: string; started: string; done: boolean; exitCode: number };

/** A Sharkord release a server can run, as `instance versions` lists them: newest first. */
export type SharkordRelease = { version: string; publishedAt: string; tested: boolean };
