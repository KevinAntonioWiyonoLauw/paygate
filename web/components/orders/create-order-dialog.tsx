"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Loader2Icon, PlusIcon, SearchIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api, rupiah } from "@/lib/api";

/** Strips everything but digits so "50.000" and "50000" both parse. */
function parseAmount(raw: string): number {
  const digits = raw.replace(/[^\d]/g, "");
  return digits ? Number.parseInt(digits, 10) : 0;
}

export function CreateOrderDialog() {
  const router = useRouter();
  const [open, setOpen] = React.useState(false);
  const [amount, setAmount] = React.useState("");
  const [reference, setReference] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [pending, setPending] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  const numericAmount = parseAmount(amount);

  function reset() {
    setAmount("");
    setReference("");
    setDescription("");
    setError(null);
  }

  async function handleSubmit(event: React.FormEvent) {
    event.preventDefault();
    if (numericAmount <= 0) {
      setError("Jumlah harus lebih dari nol.");
      return;
    }
    setPending(true);
    setError(null);
    try {
      const created = await api.createOrder({
        amount: numericAmount,
        reference: reference.trim() || undefined,
        description: description.trim() || undefined,
      });
      toast.success("Order dibuat", {
        description: `${created.public_id} · ${rupiah(created.total_amount)}`,
      });
      setOpen(false);
      reset();
      router.push(`/orders/${created.public_id}`);
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Tidak bisa membuat order",
      );
    } finally {
      setPending(false);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) reset();
      }}
    >
      <DialogTrigger render={<Button />}>
        <PlusIcon />
        Buat order
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Order baru</DialogTitle>
            <DialogDescription>
              Harga tetap. Gateway menambahkan kode unik kecil agar pembayaran
              bisa dicocokkan otomatis.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-4 py-4">
            <div className="grid gap-2">
              <Label htmlFor="amount">Jumlah (Rp)</Label>
              <Input
                id="amount"
                inputMode="numeric"
                placeholder="50000"
                value={amount}
                onChange={(event) => setAmount(event.target.value)}
                autoFocus
                required
              />
              {numericAmount > 0 ? (
                <p className="text-xs text-muted-foreground">
                  Tagihan pembeli: {rupiah(numericAmount)} + kode unik
                </p>
              ) : null}
            </div>

            <div className="grid gap-2">
              <Label htmlFor="reference">Referensi</Label>
              <Input
                id="reference"
                placeholder="INV-001"
                value={reference}
                onChange={(event) => setReference(event.target.value)}
              />
            </div>

            <div className="grid gap-2">
              <Label htmlFor="description">Keterangan</Label>
              <Textarea
                id="description"
                placeholder="Kopi arabika 1 kg"
                rows={2}
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </div>

            {error ? (
              <p className="text-sm text-destructive">{error}</p>
            ) : null}
          </div>

          <DialogFooter>
            <DialogClose render={<Button type="button" variant="outline" />}>
              Batal
            </DialogClose>
            <Button type="submit" disabled={pending || numericAmount <= 0}>
              {pending ? <Loader2Icon className="animate-spin" /> : null}
              {pending ? "Membuat…" : "Buat order"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function OrderSearch({
  value,
  onCommit,
}: {
  value: string;
  onCommit: (next: string) => void;
}) {
  const [draft, setDraft] = React.useState(value);
  const [committed, setCommitted] = React.useState(value);

  // The committed query lives in the URL, so it can change from outside this
  // input (pagination reset, "Hapus filter"). Adjust during render rather than
  // from an effect: the two renders collapse into one.
  if (value !== committed) {
    setCommitted(value);
    setDraft(value);
  }

  return (
    <form
      className="relative"
      onSubmit={(event) => {
        event.preventDefault();
        onCommit(draft.trim());
      }}
    >
      <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        placeholder="Cari id, referensi, keterangan…"
        className="w-full pl-8 sm:w-72"
        aria-label="Cari order"
      />
    </form>
  );
}
