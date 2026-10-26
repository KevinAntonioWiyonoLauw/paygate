"use client";

import * as React from "react";
import { toast } from "sonner";
import {
  Loader2Icon,
  RefreshCwIcon,
  SearchIcon,
  TriangleAlertIcon,
  WalletIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState, PageHeader } from "@/components/page-header";
import {
  MatchRuleLabel,
  PaymentStatusBadge,
} from "@/components/status-badges";
import {
  ApiError,
  api,
  formatDateTime,
  rupiah,
  type Payment,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";

function MatchDialog({
  payment,
  onClose,
  onMatched,
}: {
  payment: Payment | null;
  onClose: () => void;
  onMatched: () => void;
}) {
  const [orderId, setOrderId] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    if (!payment) return;
    setPending(true);
    setError(null);
    try {
      await api.matchPayment(payment.id, orderId.trim());
      toast.success("Pembayaran dicocokkan", {
        description: `${rupiah(payment.gross_amount)} → ${orderId.trim()}`,
      });
      onMatched();
      onClose();
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Tidak bisa mencocokkan",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog open={payment !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Cocokkan manual</DialogTitle>
            <DialogDescription>
              Tandai transaksi ini sebagai pembayaran sebuah order. Order akan
              langsung berstatus lunas dan tercatat di audit log.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-4 py-4">
            <div className="rounded-lg bg-muted p-3 text-sm">
              <div className="flex justify-between">
                <span className="text-muted-foreground">Nominal</span>
                <span className="font-medium tabular-nums">
                  {payment ? rupiah(payment.gross_amount) : "—"}
                </span>
              </div>
              <div className="flex justify-between">
                <span className="text-muted-foreground">Transaksi</span>
                <span className="font-mono text-xs">
                  {payment?.provider_tx_id}
                </span>
              </div>
            </div>

            <div className="grid gap-2">
              <Label htmlFor="order-id">ID order</Label>
              <Input
                id="order-id"
                placeholder="ORD-20261026-40DA56C5"
                value={orderId}
                onChange={(event) => setOrderId(event.target.value)}
                autoFocus
                required
              />
            </div>

            {error ? <p className="text-sm text-destructive">{error}</p> : null}
          </div>

          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" />}>
              Batal
            </DialogClose>
            <Button type="submit" disabled={pending || orderId.trim() === ""}>
              {pending ? <Loader2Icon className="animate-spin" /> : null}
              Tandai lunas
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function PaymentsView() {
  const [tab, setTab] = React.useState<"all" | "unmatched">("all");
  const [selected, setSelected] = React.useState<Payment | null>(null);

  const payments = useResource(
    () => api.listPayments({ unmatched: tab === "unmatched", limit: 100 }),
    [tab],
  );

  const rows = payments.data?.payments ?? [];
  const unmatchedCount = tab === "unmatched" ? rows.length : undefined;

  return (
    <>
      <PageHeader
        title="Pembayaran"
        description="Transaksi yang terlihat di feed Shopee, cocok maupun tidak."
        actions={
          <Button
            variant="outline"
            size="sm"
            onClick={payments.reload}
            disabled={payments.loading}
          >
            <RefreshCwIcon className={payments.loading ? "animate-spin" : ""} />
            Muat ulang
          </Button>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Transaksi</CardTitle>
          <CardDescription>
            {payments.loading && !payments.data
              ? "Memuat…"
              : `${payments.data?.total ?? 0} transaksi`}
          </CardDescription>
          <div className="col-span-full pt-2">
            <Tabs
              value={tab}
              onValueChange={(next) => setTab(next as "all" | "unmatched")}
            >
              <TabsList>
                <TabsTrigger value="all">Semua</TabsTrigger>
                <TabsTrigger value="unmatched">
                  Tak cocok
                  {unmatchedCount ? (
                    <Badge variant="destructive" className="ml-1 h-4 px-1.5">
                      {unmatchedCount}
                    </Badge>
                  ) : null}
                </TabsTrigger>
              </TabsList>
            </Tabs>
          </div>
        </CardHeader>

        <CardContent className="px-0">
          {payments.loading && !payments.data ? (
            <div className="flex flex-col gap-2 px-4">
              {Array.from({ length: 5 }).map((_, index) => (
                <Skeleton key={index} className="h-11 w-full" />
              ))}
            </div>
          ) : payments.error ? (
            <p className="px-4 text-sm text-destructive">{payments.error}</p>
          ) : rows.length === 0 ? (
            <div className="px-4">
              <EmptyState
                icon={
                  tab === "unmatched" ? <TriangleAlertIcon /> : <WalletIcon />
                }
                title={
                  tab === "unmatched"
                    ? "Tidak ada transaksi tak cocok"
                    : "Belum ada transaksi"
                }
                description={
                  tab === "unmatched"
                    ? "Semua uang masuk berhasil diatribusikan ke order."
                    : "Transaksi muncul di sini setelah feed Shopee tersambung dan ada pembayaran."
                }
              />
            </div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="pl-4">Transaksi</TableHead>
                  <TableHead className="text-right">Nominal</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Order</TableHead>
                  <TableHead>Waktu</TableHead>
                  <TableHead className="pr-4 text-right">Aksi</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((payment) => (
                  <TableRow key={payment.provider_tx_id}>
                    <TableCell className="pl-4">
                      <span className="font-mono text-xs">
                        {payment.provider_tx_id}
                      </span>
                      <p className="text-xs text-muted-foreground">
                        {payment.provider}
                      </p>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {rupiah(payment.gross_amount)}
                      {payment.real_amount &&
                      payment.real_amount !== payment.gross_amount ? (
                        <p className="text-xs text-muted-foreground">
                          net {rupiah(payment.real_amount)}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell>
                      <PaymentStatusBadge status={payment.status} />
                    </TableCell>
                    <TableCell>
                      {payment.order_public_id ? (
                        <>
                          <span className="font-mono text-xs">
                            {payment.order_public_id}
                          </span>
                          <div>
                            <MatchRuleLabel rule={payment.match_rule} />
                          </div>
                        </>
                      ) : (
                        <span className="text-xs text-destructive">suspense</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(payment.tx_time ?? payment.created_at)}
                    </TableCell>
                    <TableCell className="pr-4 text-right">
                      {payment.order_public_id ? null : (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => setSelected(payment)}
                        >
                          <SearchIcon />
                          Cocokkan
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <MatchDialog
        key={selected?.id ?? "none"}
        payment={selected}
        onClose={() => setSelected(null)}
        onMatched={payments.reload}
      />
    </>
  );
}
