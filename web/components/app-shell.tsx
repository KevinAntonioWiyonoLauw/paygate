"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";

import { AppSidebar } from "@/components/app-sidebar";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Separator } from "@/components/ui/separator";
import {
  SidebarInset,
  SidebarProvider,
  SidebarTrigger,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { ApiError, auth } from "@/lib/api";

const LABELS: Record<string, string> = {
  orders: "Order",
  payments: "Pembayaran",
  settings: "Pengaturan",
  login: "Masuk",
};

/**
 * The breadcrumb reads usePathname, which is URL data. Under cacheComponents
 * that must sit inside a Suspense boundary, so it lives in its own component.
 */
function RouteBreadcrumb() {
  const pathname = usePathname();
  const segments = pathname.split("/").filter(Boolean);

  const crumbs =
    segments.length === 0
      ? [{ label: "Ringkasan", href: "/", last: true }]
      : segments.map((segment, index) => {
          const href = `/${segments.slice(0, index + 1).join("/")}`;
          return {
            label:
              LABELS[segment] ??
              (segment.startsWith("ORD-")
                ? segment
                : segment.replace(/-/g, " ")),
            href,
            last: index === segments.length - 1,
          };
        });

  return (
    <Breadcrumb>
      <BreadcrumbList>
        {crumbs.map((crumb, index) => (
          <React.Fragment key={crumb.href}>
            {index > 0 ? (
              <BreadcrumbSeparator className="hidden md:block" />
            ) : null}
            <BreadcrumbItem
              className={index === 0 ? undefined : "hidden md:block"}
            >
              {crumb.last ? (
                <BreadcrumbPage>{crumb.label}</BreadcrumbPage>
              ) : (
                <BreadcrumbLink render={<Link href={crumb.href} />}>
                  {crumb.label}
                </BreadcrumbLink>
              )}
            </BreadcrumbItem>
          </React.Fragment>
        ))}
      </BreadcrumbList>
    </Breadcrumb>
  );
}

function BreadcrumbFallback() {
  return <Skeleton className="h-4 w-24" />;
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [username, setUsername] = React.useState<string | null>(null);

  React.useEffect(() => {
    let cancelled = false;
    auth
      .me()
      .then((me) => {
        if (!cancelled) setUsername(me.username);
      })
      .catch((err: unknown) => {
        // A stale cookie passes the edge check but fails signature
        // verification here. Bounce to the form rather than render empty.
        if (err instanceof ApiError && err.status === 401) {
          router.replace("/login");
        }
      });
    return () => {
      cancelled = true;
    };
  }, [router]);

  return (
    <SidebarProvider>
      <AppSidebar username={username ?? "admin"} />
      <SidebarInset>
        <header className="sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b bg-background/85 px-4 backdrop-blur">
          <SidebarTrigger className="-ml-1" />
          <Separator
            orientation="vertical"
            className="mr-1 data-[orientation=vertical]:h-4"
          />
          <React.Suspense fallback={<BreadcrumbFallback />}>
            <RouteBreadcrumb />
          </React.Suspense>
          <div className="ml-auto flex items-center gap-2">
            {username === null ? (
              <Skeleton className="h-4 w-20" />
            ) : (
              <span className="hidden text-xs text-muted-foreground sm:inline">
                {username}
              </span>
            )}
          </div>
        </header>

        <div className="flex flex-1 flex-col gap-6 p-4 md:p-6">{children}</div>
      </SidebarInset>
    </SidebarProvider>
  );
}
