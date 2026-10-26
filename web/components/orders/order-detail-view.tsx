"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import {
  ArrowLeftIcon,
  CheckIcon,
  CopyIcon,
  DownloadIcon,
  Loader2Icon,
  QrCodeIcon,
  RefreshCwIcon,
  XCircleIcon,
} from "lucide-react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { OrderStatusBadge } from "@/components/status-badges";
import {
  ApiError,
  api,
  formatCountdown,
  formatDateTime,
  rupiah,
  type Order,
  type OrderStatus,
} from "@/lib/api";
import { useNow, useResource } from "@/lib/hooks";

type Snapshot = {
  public_id: string;
  status: OrderStatus;
  total_amount: number;
  expires_at: string;
  paid_at?: string | null;
};

function DetailRow({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="text-sm text-muted-foreground">{label}</span>
      <span className="text-right text-sm">{children}</span>
    </div>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const [copied, setCopied] = React.useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      toast.error("Tidak bisa menyalin", { description: label });
    }
  }

  return (
    <Button variant="outline" size="sm" onClick={copy}>
      {copied ? <CheckIcon /> : <CopyIcon />}
      {copied ? "Tersalin" : label}
    </Button>
  );
}

/**
 * Streams order status from the gateway. EventSource reconnects on its own; the
 * stream closes server-side once the order reaches a terminal state, so we also
 * stop listening when that happens.
 */
function useOrderStream(
  publicId: string,
  initial: Order | null,
  onUpdate: (snapshot: Snapshot) => void,
) {
  const [live, setLive] = React.useState(false);

  // The stream must not reconnect when the callback identity changes (it does
  // on every snapshot), so the handler is read through a ref. Writing the ref
  // belongs in an effect — mutating it during render is a render-phase side
  // effect the compiler rejects.
  const handlerRef = React.useRef(onUpdate);
  React.useEffect(() => {
    handlerRef.current = onUpdate;
  });

  const terminal =
    initial != null &&
    (initial.status === "paid" ||
      initial.status === "expired" ||
      initial.status === "cancelled");

  React.useEffect(() => {
    if (terminal) return;

    const source = new EventSource(`/api/orders/${publicId}/status`);
    source.addEventListener("open", () => setLive(true));
    source.addEventListener("status", (event) => {
      try {
        handlerRef.current(JSON.parse((event as MessageEvent).data) as Snapshot);
      } catch {
        // Malformed frame: ignore, the next tick will correct it.
      }
    });
    source.addEventListener("error", () => setLive(false));

    return () => {
      source.close();
    };
  }, [publicId, terminal]);

  return live;
}

export function OrderDetailView({ publicId }: { publicId: string }) {
  const router = useRouter();
  const order = useResource(() => api.getOrder(publicId), [publicId]);
  const now = useNow();
  const [cancelling, setCancelling] = React.useState(false);

  const handleSnapshot = React.useCallback(
    (snapshot: Snapshot) => {
      order.setData((current) => {
        if (!current) return current;
        if (current.status === snapshot.status) return current;
        if (snapshot.status === "paid") {
          toast.success("Pembayaran diterima", {
            description: `${snapshot.public_id} · ${rupiah(snapshot.total_amount)}`,
          });
        }
        return {
          ...current,
          status: snapshot.status,
          paid_at: snapshot.paid_at ?? current.paid_at,
        };
      });
    },
    [order],
  );

  const live = useOrderStream(publicId, order.data, handleSnapshot);

  async function handleCancel() {
    setCancelling(true);
    try {
      const updated = await api.cancelOrder(publicId);
      order.setData(updated);
      toast.success("Order dibatalkan");
      router.refresh();
    } catch (err) {
      toast.error("Gagal membatalkan", {
        description:
          err instanceof ApiError ? err.message : "Coba lagi sebentar.",
      });
    } finally {
      setCancelling(false);
    }
  }

  if (order.loading && !order.data) {
    return (
      <div className="flex flex-col gap-4">
        <Skeleton className="h-10 w-64" />
        <div className="grid gap-4 lg:grid-cols-3">
          <Skeleton className="h-[420px] rounded-xl lg:col-span-2" />
          <Skeleton className="h-[420px] rounded-xl" />
        </div>
      </div>
    );
  }

  if (order.error || !order.data) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>Order tidak ditemukan</CardTitle>
          <CardDescription>
            {order.error ?? "Order ini tidak ada atau sudah dihapus."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Button variant="outline" render={<Link href="/orders" />}>
            <ArrowLeftIcon />
            Kembali ke daftar
          </Button>
        </CardContent>
      </Card>
    );
  }

  const data = order.data;
  const expiresAt = new Date(data.expires_at).getTime();
  const totalWindow = Math.max(1, expiresAt - new Date(data.created_at).getTime());
  const remainingMs = Math.max(0, expiresAt - now);
  const remainingSeconds = Math.floor(remainingMs / 1000);
  const progress = Math.min(100, Math.max(0, (remainingMs / totalWindow) * 100));
  const isPending = data.status === "pending";
  const isPaid = data.status === "paid";
  const qrSrc = `/api/qr/${data.public_id}.png?size=640`;

  return (
    <>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-3">
          <Button
            variant="ghost"
            size="icon"
            aria-label="Kembali"
            render={<Link href="/orders" />}
          >
            <ArrowLeftIcon />
          </Button>
          <div>
            <div className="flex items-center gap-2">
              <h1 className="font-mono text-lg font-semibold tracking-tight">
                {data.public_id}
              </h1>
              <OrderStatusBadge status={data.status} />
              {live ? (
                <Badge variant="outline" className="gap-1 text-[10px]">
                  <span className="size-1.5 animate-pulse rounded-full bg-emerald-500" />
                  live
                </Badge>
              ) : null}
            </div>
            <p className="text-sm text-muted-foreground">
              {data.description || data.reference || "Tanpa keterangan"}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={order.reload}
            disabled={order.loading}
          >
            <RefreshCwIcon className={order.loading ? "animate-spin" : ""} />
            Segarkan
          </Button>
          {isPending ? (
            <AlertDialog>
              <AlertDialogTrigger
                render={
                  <Button variant="destructive" size="sm">
                    <XCircleIcon />
                    Batalkan
                  </Button>
                }
              />
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>Batalkan order ini?</AlertDialogTitle>
                  <AlertDialogDescription>
                    Nominal unik akan dilepas untuk order berikutnya. Kalau
                    pembeli sudah terlanjur membayar, dana tetap akan tercatat
                    sebagai transaksi tak cocok.
                  </AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel render={<Button variant="outline" />}>
                    Tidak
                  </AlertDialogCancel>
                  <AlertDialogAction
                    render={<Button variant="destructive" />}
                    onClick={handleCancel}
                    disabled={cancelling}
                  >
                    {cancelling ? <Loader2Icon className="animate-spin" /> : null}
                    Ya, batalkan
                  </AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          ) : null}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>QRIS pembayaran</CardTitle>
            <CardDescription>
              Pindai dengan aplikasi bank atau e-wallet apa pun yang mendukung
              QRIS.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col items-center gap-4">
            <div className="relative rounded-xl bg-white p-4 ring-1 ring-black/5">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img
                src={qrSrc}
                alt={`QRIS untuk ${data.public_id}`}
                width={320}
                height={320}
                className="size-72 sm:size-80"
              />
              {!isPending ? (
                <div className="absolute inset-0 flex items-center justify-center rounded-xl bg-background/80 backdrop-blur-sm">
                  <div className="flex flex-col items-center gap-2 text-center">
                    {isPaid ? (
                      <>
                        <div className="flex size-12 items-center justify-center rounded-full bg-emerald-500/15 text-emerald-600">
                          <CheckIcon className="size-6" />
                        </div>
                        <p className="text-sm font-medium">Sudah dibayar</p>
                      </>
                    ) : (
                      <>
                        <div className="flex size-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
                          <XCircleIcon className="size-6" />
                        </div>
                        <p className="text-sm font-medium">
                          QR tidak berlaku lagi
                        </p>
                      </>
                    )}
                  </div>
                </div>
              ) : null}
            </div>

            <div className="flex flex-col items-center gap-1">
              <span className="text-xs uppercase tracking-wide text-muted-foreground">
                Nominal tagihan
              </span>
              <span className="text-3xl font-semibold tabular-nums">
                {rupiah(data.total_amount)}
              </span>
              {data.unique_offset > 0 ? (
                <span className="text-xs text-muted-foreground">
                  {rupiah(data.base_amount)} + kode unik {data.unique_offset}
                </span>
              ) : null}
            </div>

            <div className="flex flex-wrap items-center justify-center gap-2">
              <CopyButton value={data.public_id} label="Salin ID" />
              <CopyButton value={data.qr_payload} label="Salin payload" />
              <Button
                variant="outline"
                size="sm"
                render={
                  <a href={`/api/qr/${data.public_id}.png?size=1024`} download />
                }
              >
                <DownloadIcon />
                Unduh PNG
              </Button>
              <Button
                variant="outline"
                size="sm"
                render={<a href={`/api/qr/${data.public_id}.svg`} download />}
              >
                <DownloadIcon />
                Unduh SVG
              </Button>
            </div>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-4">
          <Card>
            <CardHeader>
              <CardTitle>Status</CardTitle>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              {isPending ? (
                <>
                  <div className="flex items-end justify-between">
                    <span className="text-sm text-muted-foreground">
                      Sisa waktu
                    </span>
                    <span
                      className={`font-mono text-2xl font-semibold tabular-nums ${
                        remainingSeconds < 60 ? "text-destructive" : ""
                      }`}
                    >
                      {formatCountdown(remainingSeconds)}
                    </span>
                  </div>
                  <Progress value={progress} />
                  <p className="text-xs text-muted-foreground">
                    Berlaku sampai {formatDateTime(data.expires_at)}
                  </p>
                </>
              ) : (
                <div className="flex items-center gap-2 rounded-lg bg-muted px-3 py-2 text-sm">
                  <QrCodeIcon className="size-4 text-muted-foreground" />
                  {isPaid
                    ? `Dibayar ${formatDateTime(data.paid_at)}`
                    : "Order ini sudah tidak aktif"}
                </div>
              )}

              <Separator />

              <DetailRow label="Provider">{data.provider}</DetailRow>
              <DetailRow label="Sumber">{data.source}</DetailRow>
              <DetailRow label="Dibuat">
                {formatDateTime(data.created_at)}
              </DetailRow>
              {data.reference ? (
                <DetailRow label="Referensi">{data.reference}</DetailRow>
              ) : null}
            </CardContent>
          </Card>

          <Card size="sm">
            <CardHeader>
              <CardTitle className="text-sm">Payload QRIS</CardTitle>
              <CardDescription>
                Tag 62/05 memuat ID order, jadi pembayaran bisa dicocokkan tanpa
                mengubah harga.
              </CardDescription>
            </CardHeader>
            <CardContent>
              <pre className="max-h-32 overflow-auto rounded-lg bg-muted p-2 font-mono text-[11px] leading-relaxed break-all whitespace-pre-wrap">
                {data.qr_payload}
              </pre>
            </CardContent>
          </Card>
        </div>
      </div>
    </>
  );
}
