// Typed client for the gateway API.
//
// Every call goes through Next's rewrite proxy, so the request is same-origin
// and the session cookie travels automatically.

export type OrderStatus =
  | "pending"
  | "paid"
  | "review"
  | "expired"
  | "cancelled";

export type PaymentStatus =
  | "pending"
  | "captured"
  | "settled"
  | "failed"
  | "refunded";

export interface Order {
  public_id: string;
  base_amount: number;
  unique_offset: number;
  total_amount: number;
  status: OrderStatus;
  source: string;
  provider: string;
  qr_payload: string;
  reference?: string;
  description?: string;
  metadata: Record<string, unknown>;
  expires_at: string;
  paid_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface Payment {
  id: number;
  provider: string;
  provider_tx_id: string;
  gross_amount: number;
  real_amount?: number;
  status: PaymentStatus;
  tx_time?: string | null;
  order_public_id?: string;
  match_rule?: number | null;
  created_at: string;
  updated_at: string;
}

export interface DailyPoint {
  date: string;
  count: number;
  revenue: number;
}

export interface Stats {
  paid_today: number;
  revenue_today: number;
  pending_count: number;
  pending_amount: number;
  review_count: number;
  unmatched_count: number;
  expired_today: number;
  volume_7d: DailyPoint[];
}

export interface QRISInfo {
  valid: boolean;
  dynamic: boolean;
  amount?: number;
  currency?: string;
  country?: string;
  merchant_name?: string;
  merchant_city?: string;
  postal_code?: string;
  mcc?: string;
  reference_label?: string;
  bill_number?: string;
  store_label?: string;
  merchant_gui?: string;
  merchant_pan?: string;
  merchant_id?: string;
  acquirer?: string;
}

export interface ProviderStatus {
  provider: string;
  authenticated: boolean;
  expired?: boolean;
  reason?: string;
  merchant_id?: string;
  merchant_name?: string;
  store_id?: string;
  store_name?: string;
  expires_at?: string | null;
  updated_at?: string;
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

async function request<T>(
  path: string,
  init?: RequestInit & { json?: unknown },
): Promise<T> {
  const { json, ...rest } = init ?? {};
  const headers = new Headers(rest.headers);
  if (json !== undefined) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(path, {
    ...rest,
    headers,
    credentials: "same-origin",
    body: json !== undefined ? JSON.stringify(json) : rest.body,
  });

  if (!response.ok) {
    let code = "request_failed";
    let message = `HTTP ${response.status}`;
    try {
      const payload = await response.json();
      if (payload?.error) {
        code = payload.error.code ?? code;
        message = payload.error.message ?? message;
      }
    } catch {
      // Body was not JSON; keep the status-derived message.
    }
    throw new ApiError(response.status, code, message);
  }

  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

// ---- auth ----

export const auth = {
  login: (username: string, password: string) =>
    request<{ username: string; expires_at: string }>("/api/auth/login", {
      method: "POST",
      json: { username, password },
    }),
  logout: () => request<{ ok: boolean }>("/api/auth/logout", { method: "POST" }),
  me: () => request<{ username: string; expires_at: string }>("/api/auth/me"),
};

// ---- data ----

export const api = {
  stats: () => request<Stats>("/api/stats"),

  listOrders: (params: {
    status?: string;
    q?: string;
    limit?: number;
    offset?: number;
  } = {}) => {
    const search = new URLSearchParams();
    if (params.status) search.set("status", params.status);
    if (params.q) search.set("q", params.q);
    if (params.limit) search.set("limit", String(params.limit));
    if (params.offset) search.set("offset", String(params.offset));
    const qs = search.toString();
    return request<{ orders: Order[]; total: number }>(
      `/api/orders${qs ? `?${qs}` : ""}`,
    );
  },

  getOrder: (id: string) => request<Order>(`/api/orders/${id}`),

  createOrder: (input: {
    amount: number;
    reference?: string;
    description?: string;
  }) =>
    request<Order>("/api/orders", {
      method: "POST",
      json: input,
    }),

  cancelOrder: (id: string) =>
    request<Order>(`/api/orders/${id}/cancel`, { method: "POST" }),

  listPayments: (params: { unmatched?: boolean; limit?: number } = {}) => {
    const search = new URLSearchParams();
    if (params.unmatched) search.set("unmatched", "1");
    if (params.limit) search.set("limit", String(params.limit));
    const qs = search.toString();
    return request<{ payments: Payment[]; total: number }>(
      `/api/payments${qs ? `?${qs}` : ""}`,
    );
  },

  matchPayment: (paymentId: number, orderPublicId: string) =>
    request<{ ok: boolean }>(`/api/payments/${paymentId}/match`, {
      method: "POST",
      json: { order_public_id: orderPublicId },
    }),

  getStaticQRIS: () =>
    request<{ payload: string; bound: boolean; info: QRISInfo }>(
      "/api/qris/static",
    ),

  setStaticQRIS: (payload: string) =>
    request<{ bound: boolean; info: QRISInfo }>("/api/qris/static", {
      method: "PUT",
      json: { payload },
    }),

  inspectQRIS: (payload: string) =>
    request<QRISInfo>(
      `/api/qris/inspect?payload=${encodeURIComponent(payload)}`,
    ),

  providerStatus: () => request<ProviderStatus>("/api/provider/status"),

  requestOTP: (input: {
    phone: string;
    password?: string;
    device_report?: string;
  }) =>
    request<{
      sent: boolean;
      channel: number;
      channels?: number[];
      has_password: boolean;
    }>("/api/provider/login/request", { method: "POST", json: input }),

  verifyOTP: (input: { otp: string; merchant_id?: string; store_id?: string }) =>
    request<{
      authenticated?: boolean;
      needs_merchant?: boolean;
      merchants?: { id: string; name: string }[];
      merchant_name?: string;
      store_id?: string;
      expires_at?: string;
    }>("/api/provider/login/verify", { method: "POST", json: input }),
};

// ---- formatting ----

const rupiahFormatter = new Intl.NumberFormat("id-ID", {
  style: "currency",
  currency: "IDR",
  maximumFractionDigits: 0,
});

export function rupiah(value: number): string {
  return rupiahFormatter.format(value);
}

export function compactRupiah(value: number): string {
  if (value >= 1_000_000_000) return `Rp ${(value / 1_000_000_000).toFixed(1)}M`;
  if (value >= 1_000_000) return `Rp ${(value / 1_000_000).toFixed(1)}jt`;
  if (value >= 1_000) return `Rp ${(value / 1_000).toFixed(0)}rb`;
  return `Rp ${value}`;
}

export function formatDateTime(iso?: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("id-ID", {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

export function relativeTime(iso?: string | null): string {
  if (!iso) return "—";
  const diff = Date.now() - new Date(iso).getTime();
  const abs = Math.abs(diff);
  const suffix = diff >= 0 ? "lalu" : "lagi";
  if (abs < 60_000) return `${Math.round(abs / 1000)}d ${suffix}`;
  if (abs < 3_600_000) return `${Math.round(abs / 60_000)}m ${suffix}`;
  if (abs < 86_400_000) return `${Math.round(abs / 3_600_000)}j ${suffix}`;
  return `${Math.round(abs / 86_400_000)}h ${suffix}`;
}

export function secondsUntil(iso: string): number {
  return Math.max(0, Math.floor((new Date(iso).getTime() - Date.now()) / 1000));
}

export function formatCountdown(totalSeconds: number): string {
  const m = Math.floor(totalSeconds / 60);
  const s = totalSeconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}
