"use client";

import * as React from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { QrCodeIcon } from "lucide-react";

import { LoginForm } from "@/components/login-form";

export function LoginScreen() {
  const router = useRouter();
  const params = useSearchParams();
  const next = params.get("next") ?? "/";

  const handleSuccess = React.useCallback(() => {
    router.replace(next);
    router.refresh();
  }, [next, router]);

  return (
    <div className="grid min-h-dvh place-items-center bg-muted/40 px-4 py-10">
      <div className="flex w-full max-w-sm flex-col items-center gap-6">
        <div className="flex flex-col items-center gap-2 text-center">
          <div className="flex size-12 items-center justify-center rounded-xl bg-primary text-primary-foreground">
            <QrCodeIcon className="size-6" />
          </div>
          <h1 className="text-xl font-semibold tracking-tight">Paygate</h1>
          <p className="text-sm text-muted-foreground">
            Gateway QRIS dinamis untuk merchant Shopee.
          </p>
        </div>

        <LoginForm onSuccess={handleSuccess} />
      </div>
    </div>
  );
}
