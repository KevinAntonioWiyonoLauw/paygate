"use client";

import * as React from "react";
import Link from "next/link";
import { Bar, BarChart, CartesianGrid, XAxis } from "recharts";
import {
  ActivityIcon,
  ArrowUpRightIcon,
  CircleAlertIcon,
  ClockIcon,
  WalletIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { EmptyState, PageHeader } from "@/components/page-header";
import { OrderStatusBadge } from "@/components/status-badges";
import { api, compactRupiah, relativeTime, rupiah } from "@/lib/api";
import { useResource } from "@/lib/hooks";

const chartConfig = {
  revenue: {
    label: "Pendapatan",
    color: "var(--chart-1)",
  },
} satisfies ChartConfig;

function shortDate(iso: string): string {
  const date = new Date(`${iso}T00:00:00`);
  return date.toLocaleDateString("id-ID", { day: "numeric", month: "short" });
}

function StatCard({
  label,
  value,
  hint,
  icon,
}: {
  label: string;
  value: React.ReactNode;
  hint?: React.ReactNode;
  icon: React.ReactNode;
}) {
  return (
    <Card size="sm">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl font-semibold tabular-nums">
          {value}
        </CardTitle>
        <CardAction>
          <div className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground [&_svg]:size-4">
            {icon}
          </div>
        </CardAction>
      </CardHeader>
      {hint ? (
        <CardContent className="text-xs text-muted-foreground">{hint}</CardContent>
      ) : null}
    </Card>
  );
}

export default function OverviewPage() {
  const stats = useResource(() => api.stats());
  const recent = useResource(() => api.listOrders({ limit: 8 }));

  const data = stats.data;
  const chartData = (data?.volume_7d ?? []).map((point) => ({
    day: shortDate(point.date),
    revenue: point.revenue,
    count: point.count,
  }));

  return (
    <>
      <PageHeader
        title="Ringkasan"
        description="Kondisi hari ini dan tujuh hari terakhir."
        actions={
          <Button variant="outline" render={<Link href="/orders" />}>
            Lihat order
            <ArrowUpRightIcon />
          </Button>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {stats.loading && !data ? (
          Array.from({ length: 4 }).map((_, index) => (
            <Skeleton key={index} className="h-[104px] rounded-xl" />
          ))
        ) : (
          <>
            <StatCard
              label="Lunas hari ini"
              value={rupiah(data?.revenue_today ?? 0)}
              hint={`${data?.paid_today ?? 0} transaksi`}
              icon={<WalletIcon />}
            />
            <StatCard
              label="Menunggu bayar"
              value={rupiah(data?.pending_amount ?? 0)}
              hint={`${data?.pending_count ?? 0} order aktif`}
              icon={<ClockIcon />}
            />
            <StatCard
              label="Perlu tinjauan"
              value={data?.review_count ?? 0}
              hint="Dana tertahan atau ambigu"
              icon={<CircleAlertIcon />}
            />
            <StatCard
              label="Tak cocok"
              value={data?.unmatched_count ?? 0}
              hint="Uang masuk tanpa order"
              icon={<ActivityIcon />}
            />
          </>
        )}
      </div>

      {stats.error ? (
        <Card size="sm">
          <CardContent className="text-sm text-destructive">
            {stats.error}
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-5">
        <Card className="lg:col-span-3">
          <CardHeader>
            <CardTitle>Pendapatan 7 hari</CardTitle>
            <CardDescription>Nilai order yang lunas per hari.</CardDescription>
            <CardAction>
              <Badge variant="outline">
                Total {compactRupiah(chartData.reduce((sum, d) => sum + d.revenue, 0))}
              </Badge>
            </CardAction>
          </CardHeader>
          <CardContent>
            {stats.loading && !data ? (
              <Skeleton className="h-[220px] w-full rounded-lg" />
            ) : (
              <ChartContainer config={chartConfig} className="h-[220px] w-full">
                <BarChart data={chartData} margin={{ left: 4, right: 4 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis
                    dataKey="day"
                    tickLine={false}
                    axisLine={false}
                    tickMargin={8}
                  />
                  <ChartTooltip
                    cursor={false}
                    content={
                      <ChartTooltipContent
                        formatter={(value) => rupiah(Number(value))}
                      />
                    }
                  />
                  <Bar
                    dataKey="revenue"
                    fill="var(--color-revenue)"
                    radius={[6, 6, 0, 0]}
                  />
                </BarChart>
              </ChartContainer>
            )}
          </CardContent>
        </Card>

        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>Order terbaru</CardTitle>
            <CardDescription>Delapan order terakhir.</CardDescription>
          </CardHeader>
          <CardContent className="px-0">
            {recent.loading && !recent.data ? (
              <div className="flex flex-col gap-2 px-4">
                {Array.from({ length: 5 }).map((_, index) => (
                  <Skeleton key={index} className="h-9 w-full" />
                ))}
              </div>
            ) : recent.data && recent.data.orders.length > 0 ? (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="pl-4">Order</TableHead>
                    <TableHead>Jumlah</TableHead>
                    <TableHead className="pr-4 text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {recent.data.orders.map((order) => (
                    <TableRow key={order.public_id}>
                      <TableCell className="pl-4">
                        <Link
                          href={`/orders/${order.public_id}`}
                          className="font-medium hover:underline"
                        >
                          {order.public_id}
                        </Link>
                        <p className="text-xs text-muted-foreground">
                          {relativeTime(order.created_at)}
                        </p>
                      </TableCell>
                      <TableCell className="tabular-nums">
                        {rupiah(order.total_amount)}
                      </TableCell>
                      <TableCell className="pr-4 text-right">
                        <OrderStatusBadge status={order.status} />
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            ) : (
              <div className="px-4">
                <EmptyState
                  title="Belum ada order"
                  description="Buat order pertama dari halaman Order."
                  action={
                    <Button size="sm" render={<Link href="/orders" />}>
                      Buat order
                    </Button>
                  }
                />
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </>
  );
}
