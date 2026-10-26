import type { OrderStatus, PaymentStatus } from "@/lib/api";
import { Badge } from "@/components/ui/badge";

const ORDER_LABEL: Record<OrderStatus, string> = {
  pending: "Menunggu",
  paid: "Lunas",
  review: "Tinjau",
  expired: "Kedaluwarsa",
  cancelled: "Dibatalkan",
};

const ORDER_CLASS: Record<OrderStatus, string> = {
  pending: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  paid: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  review: "bg-violet-500/10 text-violet-700 dark:text-violet-400",
  expired: "bg-muted text-muted-foreground",
  cancelled: "bg-muted text-muted-foreground line-through",
};

export function OrderStatusBadge({ status }: { status: OrderStatus }) {
  return (
    <Badge variant="outline" className={ORDER_CLASS[status] ?? ""}>
      {ORDER_LABEL[status] ?? status}
    </Badge>
  );
}

const PAYMENT_LABEL: Record<PaymentStatus, string> = {
  pending: "Menunggu",
  captured: "Tertahan",
  settled: "Settled",
  failed: "Gagal",
  refunded: "Refund",
};

const PAYMENT_CLASS: Record<PaymentStatus, string> = {
  pending: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
  captured: "bg-violet-500/10 text-violet-700 dark:text-violet-400",
  settled: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  failed: "bg-destructive/10 text-destructive",
  refunded: "bg-muted text-muted-foreground",
};

export function PaymentStatusBadge({ status }: { status: PaymentStatus }) {
  return (
    <Badge variant="outline" className={PAYMENT_CLASS[status] ?? ""}>
      {PAYMENT_LABEL[status] ?? status}
    </Badge>
  );
}

const RULE_LABEL: Record<number, string> = {
  1: "Jumlah pas",
  2: "Tertahan",
  3: "Ambigu",
  4: "Tak cocok",
  5: "Manual",
};

export function MatchRuleLabel({ rule }: { rule?: number | null }) {
  if (rule == null) return <span className="text-muted-foreground">—</span>;
  return (
    <span className="text-xs text-muted-foreground">
      {RULE_LABEL[rule] ?? `Aturan ${rule}`}
    </span>
  );
}
