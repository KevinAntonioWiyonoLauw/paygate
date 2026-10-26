"use client";

import * as React from "react";
import { toast } from "sonner";
import {
  CircleCheckIcon,
  CircleAlertIcon,
  Loader2Icon,
  QrCodeIcon,
  ShieldCheckIcon,
  SmartphoneIcon,
  UnlinkIcon,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Separator } from "@/components/ui/separator";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { PageHeader } from "@/components/page-header";
import {
  ApiError,
  api,
  formatDateTime,
  rupiah,
  type QRISInfo,
} from "@/lib/api";
import { useResource } from "@/lib/hooks";

function InfoRow({
  label,
  value,
  mono,
}: {
  label: string;
  value?: string | null;
  mono?: boolean;
}) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-sm">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span
        className={`truncate text-right ${mono ? "font-mono text-xs" : ""} ${
          value ? "" : "text-muted-foreground"
        }`}
        title={value ?? undefined}
      >
        {value || "—"}
      </span>
    </div>
  );
}

function QRISCard() {
  const current = useResource(() => api.getStaticQRIS());
  const [draft, setDraft] = React.useState("");
  const [seededFrom, setSeededFrom] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  // Inspection result is keyed by the payload it describes, so a stale result
  // is never shown for text the operator has since edited.
  const [inspected, setInspected] = React.useState<{
    payload: string;
    info: QRISInfo | null;
  }>({ payload: "", info: null });

  const serverPayload = current.data?.payload ?? "";
  if (current.data && seededFrom !== serverPayload) {
    setSeededFrom(serverPayload);
    setDraft(serverPayload);
  }

  const payload = draft.trim();
  const tooShort = payload.length < 16;
  const checking = !tooShort && inspected.payload !== payload;
  const preview = tooShort
    ? null
    : inspected.payload === payload
      ? inspected.info
      : null;

  // Debounced validation: describe the payload through the API so the operator
  // sees the merchant name before committing. State is only written from the
  // async callback, never synchronously in the effect body.
  React.useEffect(() => {
    if (tooShort) return;

    let cancelled = false;
    const timer = window.setTimeout(() => {
      api
        .inspectQRIS(payload)
        .then((info) => {
          if (!cancelled) setInspected({ payload, info });
        })
        .catch(() => {
          if (!cancelled) {
            setInspected({ payload, info: { valid: false, dynamic: false } });
          }
        });
    }, 400);

    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [payload, tooShort]);

  const bound = current.data?.bound ?? false;
  const info = current.data?.info;

  async function handleSave() {
    setSaving(true);
    try {
      const result = await api.setStaticQRIS(draft.trim());
      toast.success(result.bound ? "QRIS terpasang" : "QRIS dilepas", {
        description: result.bound
          ? result.info.merchant_name ?? undefined
          : "Order baru tidak bisa dibuat sampai QRIS dipasang lagi.",
      });
      current.reload();
    } catch (err) {
      toast.error("Gagal menyimpan", {
        description:
          err instanceof ApiError ? err.message : "Payload tidak diterima.",
      });
    } finally {
      setSaving(false);
    }
  }

  const canSave =
    !saving &&
    draft.trim() !== (current.data?.payload ?? "") &&
    (draft.trim() === "" || preview?.valid === true);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <QrCodeIcon className="size-4" />
          QRIS statis merchant
        </CardTitle>
        <CardDescription>
          Tempel payload QRIS statis dari Shopee. Gateway memakainya sebagai
          cetak biru untuk membuat QRIS dinamis per order.
        </CardDescription>
        <div className="col-span-full pt-1">
          {bound ? (
            <Badge variant="outline" className="gap-1 text-emerald-600">
              <CircleCheckIcon className="size-3" />
              Terpasang
            </Badge>
          ) : (
            <Badge variant="outline" className="gap-1 text-amber-600">
              <CircleAlertIcon className="size-3" />
              Belum terpasang
            </Badge>
          )}
        </div>
      </CardHeader>

      <CardContent className="flex flex-col gap-4">
        <div className="grid gap-2">
          <Label htmlFor="qris-payload">Payload</Label>
          <Textarea
            id="qris-payload"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            rows={4}
            spellCheck={false}
            className="font-mono text-xs"
            placeholder="0002010102112661..."
          />
          {checking ? (
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Loader2Icon className="size-3 animate-spin" />
              Memeriksa payload…
            </p>
          ) : preview ? (
            preview.valid ? (
              <p className="flex items-center gap-1.5 text-xs text-emerald-600">
                <CircleCheckIcon className="size-3" />
                Checksum valid · {preview.merchant_name ?? "merchant tanpa nama"}
              </p>
            ) : (
              <p className="flex items-center gap-1.5 text-xs text-destructive">
                <CircleAlertIcon className="size-3" />
                Checksum tidak valid — QRIS ini akan ditolak.
              </p>
            )
          ) : null}
        </div>

        {preview?.valid ? (
          <div className="rounded-lg bg-muted p-3">
            <InfoRow label="Merchant" value={preview.merchant_name} />
            <InfoRow
              label="Kota"
              value={[preview.merchant_city, preview.postal_code]
                .filter(Boolean)
                .join(", ")}
            />
            <InfoRow label="Acquirer" value={preview.acquirer} mono />
            <InfoRow label="Merchant ID" value={preview.merchant_id} mono />
            <InfoRow label="NMID" value={preview.merchant_pan} mono />
            <InfoRow label="MCC" value={preview.mcc} />
            {preview.amount ? (
              <InfoRow label="Nominal" value={rupiah(preview.amount)} />
            ) : null}
            {preview.dynamic ? (
              <p className="pt-2 text-xs text-amber-600">
                Payload ini sudah dinamis. Sebaiknya pakai QRIS statis (tanpa
                nominal) sebagai sumber.
              </p>
            ) : null}
          </div>
        ) : null}

        {bound && info ? (
          <>
            <Separator />
            <div>
              <p className="pb-1 text-xs font-medium text-muted-foreground">
                Terpasang sekarang
              </p>
              <InfoRow label="Merchant" value={info.merchant_name} />
              <InfoRow label="Merchant ID" value={info.merchant_id} mono />
            </div>
          </>
        ) : null}
      </CardContent>

      <CardFooter className="flex flex-wrap gap-2">
        <Button onClick={handleSave} disabled={!canSave}>
          {saving ? <Loader2Icon className="animate-spin" /> : null}
          {bound ? "Perbarui" : "Pasang"}
        </Button>
        {bound ? (
          <Button
            variant="outline"
            disabled={saving}
            onClick={() => {
              setDraft("");
              void api
                .setStaticQRIS("")
                .then(() => {
                  toast.success("QRIS dilepas");
                  current.reload();
                })
                .catch(() =>
                  toast.error("Gagal melepas QRIS"),
                );
            }}
          >
            <UnlinkIcon />
            Lepas
          </Button>
        ) : null}
        {current.loading ? <Skeleton className="h-8 w-24" /> : null}
      </CardFooter>
    </Card>
  );
}

type Stage = "idle" | "sent" | "verifying";

function ShopeeCard() {
  const status = useResource(() => api.providerStatus());
  const [stage, setStage] = React.useState<Stage>("idle");
  const [phone, setPhone] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [deviceReport, setDeviceReport] = React.useState("");
  const [otp, setOtp] = React.useState("");
  const [pending, setPending] = React.useState(false);

  const authenticated = status.data?.authenticated ?? false;

  async function handleRequest(event: React.FormEvent) {
    event.preventDefault();
    setPending(true);
    try {
      await api.requestOTP({
        phone: phone.trim(),
        password: password.trim() || undefined,
        device_report: deviceReport.trim() || undefined,
      });
      setStage("sent");
      toast.success("OTP dikirim", {
        description: "Cek SMS atau notifikasi Shopee.",
      });
    } catch (err) {
      toast.error("Gagal meminta OTP", {
        description:
          err instanceof ApiError ? err.message : "Periksa nomor dan password.",
      });
    } finally {
      setPending(false);
    }
  }

  async function handleVerify(event: React.FormEvent) {
    event.preventDefault();
    setPending(true);
    setStage("verifying");
    try {
      const result = await api.verifyOTP({ otp: otp.trim() });
      if (result.needs_merchant) {
        toast.warning("Pilih merchant", {
          description:
            "Akun ini punya beberapa merchant. Login ulang dengan merchant_id.",
        });
        setStage("sent");
        return;
      }
      toast.success("Shopee tersambung", {
        description: result.merchant_name ?? "Sesi tersimpan.",
      });
      setStage("idle");
      setOtp("");
      setPassword("");
      status.reload();
    } catch (err) {
      toast.error("OTP ditolak", {
        description: err instanceof ApiError ? err.message : "Coba lagi.",
      });
      setStage("sent");
    } finally {
      setPending(false);
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <SmartphoneIcon className="size-4" />
          Sesi Shopee
        </CardTitle>
        <CardDescription>
          Login OTP dipakai untuk membaca feed transaksi. Sesi disimpan terenkripsi
          di database.
        </CardDescription>
        <div className="col-span-full pt-1">
          {status.loading && !status.data ? (
            <Skeleton className="h-5 w-28" />
          ) : authenticated ? (
            <Badge variant="outline" className="gap-1 text-emerald-600">
              <ShieldCheckIcon className="size-3" />
              Tersambung
            </Badge>
          ) : (
            <Badge variant="outline" className="gap-1 text-muted-foreground">
              <CircleAlertIcon className="size-3" />
              Belum login
            </Badge>
          )}
        </div>
      </CardHeader>

      <CardContent className="flex flex-col gap-4">
        {authenticated && status.data ? (
          <div className="rounded-lg bg-muted p-3">
            <InfoRow label="Merchant" value={status.data.merchant_name} />
            <InfoRow label="Merchant ID" value={status.data.merchant_id} mono />
            <InfoRow label="Store" value={status.data.store_id} mono />
            <InfoRow
              label="Berakhir"
              value={formatDateTime(status.data.expires_at ?? null)}
            />
            <InfoRow
              label="Diperbarui"
              value={formatDateTime(status.data.updated_at ?? null)}
            />
          </div>
        ) : (
          <Alert>
            <CircleAlertIcon />
            <AlertTitle>Belum ada sesi</AlertTitle>
            <AlertDescription>
              Tanpa sesi, gateway tidak bisa membaca transaksi dan tidak ada
              pembayaran yang tercatat otomatis. Order tetap bisa dibuat dan
              dibayar, tapi harus dicocokkan manual.
            </AlertDescription>
          </Alert>
        )}

        {stage === "idle" ? (
          <form onSubmit={handleRequest} className="flex flex-col gap-3">
            <div className="grid gap-2">
              <Label htmlFor="phone">Nomor HP</Label>
              <Input
                id="phone"
                inputMode="tel"
                placeholder="0812xxxxxxx"
                value={phone}
                onChange={(event) => setPhone(event.target.value)}
                required
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="shopee-password">Password Shopee</Label>
              <Input
                id="shopee-password"
                type="password"
                autoComplete="off"
                placeholder="Kosongkan bila akun tanpa password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="device-report">
                Device report{" "}
                <span className="font-normal text-muted-foreground">
                  (opsional)
                </span>
              </Label>
              <Textarea
                id="device-report"
                rows={2}
                spellCheck={false}
                className="font-mono text-xs"
                placeholder="Blob dari browser sendiri; kosong = blob bawaan upstream"
                value={deviceReport}
                onChange={(event) => setDeviceReport(event.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                Kalau kosong, semua instalasi melaporkan perangkat yang sama dan
                akun jadi mudah dikaitkan. Isi dengan blob hasil tangkapan
                browser sendiri bila memungkinkan.
              </p>
            </div>
            <Button type="submit" disabled={pending || phone.trim() === ""}>
              {pending ? <Loader2Icon className="animate-spin" /> : null}
              Kirim OTP
            </Button>
          </form>
        ) : (
          <form onSubmit={handleVerify} className="flex flex-col gap-3">
            <div className="grid gap-2">
              <Label htmlFor="otp">Kode OTP</Label>
              <Input
                id="otp"
                inputMode="numeric"
                autoComplete="one-time-code"
                placeholder="123456"
                value={otp}
                onChange={(event) => setOtp(event.target.value)}
                autoFocus
                required
              />
            </div>
            <div className="flex gap-2">
              <Button
                type="submit"
                disabled={pending || otp.trim().length < 4}
                className="flex-1"
              >
                {pending ? <Loader2Icon className="animate-spin" /> : null}
                Verifikasi
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setStage("idle");
                  setOtp("");
                }}
                disabled={pending}
              >
                Batal
              </Button>
            </div>
          </form>
        )}
      </CardContent>
    </Card>
  );
}

export function SettingsView() {
  return (
    <>
      <PageHeader
        title="Pengaturan"
        description="Sumber QRIS dan sesi provider yang membuat rekonsiliasi otomatis."
      />

      <div className="grid gap-4 lg:grid-cols-2">
        <QRISCard />
        <ShopeeCard />
      </div>
    </>
  );
}
