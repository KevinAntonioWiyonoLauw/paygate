"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { ReceiptTextIcon, RefreshCwIcon } from "lucide-react";

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
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { CreateOrderDialog, OrderSearch } from "@/components/orders/create-order-dialog";
import { EmptyState, PageHeader } from "@/components/page-header";
import { OrderStatusBadge } from "@/components/status-badges";
import { api, formatDateTime, relativeTime, rupiah } from "@/lib/api";
import { useResource } from "@/lib/hooks";

const STATUS_OPTIONS = [
  { value: "all", label: "Semua status" },
  { value: "pending", label: "Menunggu" },
  { value: "paid", label: "Lunas" },
  { value: "review", label: "Tinjau" },
  { value: "expired", label: "Kedaluwarsa" },
  { value: "cancelled", label: "Dibatalkan" },
];

const PAGE_SIZE = 25;

export function OrdersView() {
  const router = useRouter();
  const params = useSearchParams();

  const status = params.get("status") ?? "all";
  const query = params.get("q") ?? "";
  const page = Math.max(1, Number.parseInt(params.get("page") ?? "1", 10) || 1);

  const orders = useResource(
    () =>
      api.listOrders({
        status: status === "all" ? undefined : status,
        q: query || undefined,
        limit: PAGE_SIZE,
        offset: (page - 1) * PAGE_SIZE,
      }),
    [status, query, page],
  );

  const push = React.useCallback(
    (next: Record<string, string | undefined>) => {
      const search = new URLSearchParams(params.toString());
      for (const [key, value] of Object.entries(next)) {
        if (!value || value === "all") search.delete(key);
        else search.set(key, value);
      }
      // Any filter change invalidates the page offset.
      if (!("page" in next)) search.delete("page");
      const qs = search.toString();
      router.push(qs ? `/orders?${qs}` : "/orders");
    },
    [params, router],
  );

  const total = orders.data?.total ?? 0;
  const rows = orders.data?.orders ?? [];
  const lastPage = Math.max(1, Math.ceil(total / PAGE_SIZE));

  return (
    <>
      <PageHeader
        title="Order"
        description="Setiap order menghasilkan QRIS dinamis dengan nominal unik."
        actions={<CreateOrderDialog />}
      />

      <Card>
        <CardHeader>
          <CardTitle>Daftar order</CardTitle>
          <CardDescription>
            {orders.loading && !orders.data
              ? "Memuat…"
              : `${total} order tercatat`}
          </CardDescription>
          <div className="col-span-full flex flex-col gap-2 pt-2 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-wrap items-center gap-2">
              <Select
                value={status}
                onValueChange={(next) => push({ status: String(next) })}
              >
                <SelectTrigger className="w-40" aria-label="Filter status">
                  <SelectValue>
                    {(value) =>
                      STATUS_OPTIONS.find((o) => o.value === value)?.label ??
                      "Semua status"
                    }
                  </SelectValue>
                </SelectTrigger>
                <SelectContent>
                  {STATUS_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {option.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <OrderSearch value={query} onCommit={(q) => push({ q })} />
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={orders.reload}
              disabled={orders.loading}
            >
              <RefreshCwIcon className={orders.loading ? "animate-spin" : ""} />
              Muat ulang
            </Button>
          </div>
        </CardHeader>

        <CardContent className="px-0">
          {orders.loading && !orders.data ? (
            <div className="flex flex-col gap-2 px-4">
              {Array.from({ length: 6 }).map((_, index) => (
                <Skeleton key={index} className="h-11 w-full" />
              ))}
            </div>
          ) : orders.error ? (
            <p className="px-4 text-sm text-destructive">{orders.error}</p>
          ) : rows.length === 0 ? (
            <div className="px-4">
              <EmptyState
                icon={<ReceiptTextIcon />}
                title="Tidak ada order"
                description={
                  query || status !== "all"
                    ? "Coba ubah filter atau kata kunci."
                    : "Buat order pertama untuk mulai menerima pembayaran."
                }
                action={
                  query || status !== "all" ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => router.push("/orders")}
                    >
                      Hapus filter
                    </Button>
                  ) : (
                    <CreateOrderDialog />
                  )
                }
              />
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">Order</TableHead>
                    <TableHead>Keterangan</TableHead>
                    <TableHead className="text-right">Tagihan</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Dibuat</TableHead>
                    <TableHead className="pr-4 text-right">Kedaluwarsa</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rows.map((order) => (
                    <TableRow key={order.public_id}>
                      <TableCell className="pl-4">
                        <Link
                          href={`/orders/${order.public_id}`}
                          className="font-medium hover:underline"
                        >
                          {order.public_id}
                        </Link>
                        {order.reference ? (
                          <p className="text-xs text-muted-foreground">
                            {order.reference}
                          </p>
                        ) : null}
                      </TableCell>
                      <TableCell className="max-w-[220px] truncate text-muted-foreground">
                        {order.description || "—"}
                      </TableCell>
                      <TableCell className="text-right tabular-nums">
                        {rupiah(order.total_amount)}
                        <p className="text-xs text-muted-foreground">
                          {rupiah(order.base_amount)} + {order.unique_offset}
                        </p>
                      </TableCell>
                      <TableCell>
                        <OrderStatusBadge status={order.status} />
                      </TableCell>
                      <TableCell className="text-muted-foreground">
                        {relativeTime(order.created_at)}
                      </TableCell>
                      <TableCell className="pr-4 text-right text-xs text-muted-foreground">
                        {order.status === "pending" ? (
                          formatDateTime(order.expires_at)
                        ) : (
                          <span className="text-muted-foreground/60">—</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              <div className="flex items-center justify-between gap-2 px-4 pt-3">
                <Badge variant="outline">
                  Halaman {page} dari {lastPage}
                </Badge>
                <div className="flex gap-2">
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page <= 1}
                    onClick={() => push({ page: String(page - 1) })}
                  >
                    Sebelumnya
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={page >= lastPage}
                    onClick={() => push({ page: String(page + 1) })}
                  >
                    Berikutnya
                  </Button>
                </div>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </>
  );
}
