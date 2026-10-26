import { Suspense } from "react";
import type { Metadata } from "next";
import { Skeleton } from "@/components/ui/skeleton";
import { OrdersView } from "@/components/orders/orders-view";

export const metadata: Metadata = {
  title: "Order",
};

export default function OrdersPage() {
  return (
    <Suspense
      fallback={
        <div className="flex flex-col gap-4">
          <Skeleton className="h-12 w-64" />
          <Skeleton className="h-96 w-full rounded-xl" />
        </div>
      }
    >
      <OrdersView />
    </Suspense>
  );
}
