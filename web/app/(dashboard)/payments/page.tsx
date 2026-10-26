import type { Metadata } from "next";
import { PaymentsView } from "@/components/payments/payments-view";

export const metadata: Metadata = {
  title: "Pembayaran",
};

export default function PaymentsPage() {
  return <PaymentsView />;
}
